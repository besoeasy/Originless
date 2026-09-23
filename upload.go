package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
)

const (
	maxUploadBytes       int64 = 1 << 30
	maxMultipartOverhead int64 = 1 << 20
)

var (
	errNoUploadFiles  = errors.New("no files provided")
	errUploadTooLarge = errors.New("upload exceeds maximum size")
)

type uploadFile struct {
	name      string
	tempPath  string
	size      int64
	directory bool
}

type uploadResponse struct {
	CID       string `json:"cid"`
	Size      int64  `json:"size"`
	Bytes     int64  `json:"bytes"`
	Name      string `json:"name,omitempty"`
	Extension string `json:"extension"`
	MIME      string `json:"mime"`
	Files     int    `json:"files,omitempty"`
}

type addEntry struct {
	Name string          `json:"Name"`
	Hash string          `json:"Hash"`
	Size json.RawMessage `json:"Size"`
}

type addResult struct {
	CID  string
	Size int64
	Name string
}

type uploadHandler struct {
	client    *ipfsClient
	folder    bool
	downloads *downloadRegistry
}

func (h *uploadHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	expectedPath := "/up"
	if h.folder {
		expectedPath = "/upf"
	}
	if r.URL.Path != expectedPath {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{
			"error": "method not allowed",
		})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes+maxMultipartOverhead)
	files, totalBytes, err := parseUploadFiles(r, maxUploadBytes)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errUploadTooLarge) {
			status = http.StatusRequestEntityTooLarge
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	defer removeUploadFiles(files)

	folder := h.folder || uploadIsFolder(files)
	result, err := h.client.add(r.Context(), files, folder)
	if err != nil {
		log.Printf("IPFS add failed: %v", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error": "IPFS add failed",
		})
		return
	}

	name := result.Name
	if name == "" && len(files) > 0 {
		name = files[0].name
	}
	extension, mimeType := uploadMetadata(name, folder)
	if h.downloads != nil && !folder && isDownloadableExtension(extension) {
		h.downloads.add(downloadableFile{
			CID:       result.CID,
			Name:      name,
			Extension: extension,
			MIME:      mimeType,
			Size:      totalBytes,
		})
	}
	size := result.Size
	if size == 0 {
		size = totalBytes
	}
	writeJSON(w, http.StatusOK, uploadResponse{
		CID:       result.CID,
		Size:      size,
		Bytes:     totalBytes,
		Name:      name,
		Extension: extension,
		MIME:      mimeType,
		Files:     countUploadFiles(files),
	})
}

func parseUploadFiles(r *http.Request, maxBytes int64) (files []uploadFile, totalBytes int64, err error) {
	defer func() {
		if err != nil {
			removeUploadFiles(files)
			files = nil
		}
	}()

	reader, err := r.MultipartReader()
	if err != nil {
		return nil, 0, fmt.Errorf("request must be multipart/form-data: %w", err)
	}

	seen := make(map[string]struct{})
	for {
		part, nextErr := reader.NextPart()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			return nil, totalBytes, fmt.Errorf("read multipart upload: %w", nextErr)
		}

		name, nameErr := multipartPartFilename(part)
		if nameErr != nil {
			_ = part.Close()
			return nil, totalBytes, nameErr
		}
		if name == "" {
			_, copyErr := io.Copy(io.Discard, part)
			_ = part.Close()
			if copyErr != nil {
				return nil, totalBytes, fmt.Errorf("read multipart field: %w", copyErr)
			}
			continue
		}
		if _, exists := seen[name]; exists {
			_ = part.Close()
			return nil, totalBytes, fmt.Errorf("duplicate upload path %q", name)
		}
		seen[name] = struct{}{}

		contentType, _, _ := mime.ParseMediaType(part.Header.Get("Content-Type"))
		isDirectory := strings.EqualFold(contentType, "application/x-directory")
		if isDirectory {
			written, copyErr := copyUploadPart(io.Discard, part, maxBytes-totalBytes)
			_ = part.Close()
			if copyErr != nil {
				return nil, totalBytes, copyErr
			}
			totalBytes += written
			files = append(files, uploadFile{name: name, directory: true})
			continue
		}

		temp, createErr := os.CreateTemp("", "originless-upload-*")
		if createErr != nil {
			_ = part.Close()
			return nil, totalBytes, fmt.Errorf("create upload temporary file: %w", createErr)
		}
		tempPath := temp.Name()
		written, copyErr := copyUploadPart(temp, part, maxBytes-totalBytes)
		closeErr := temp.Close()
		_ = part.Close()
		if copyErr != nil {
			_ = os.Remove(tempPath)
			return nil, totalBytes, copyErr
		}
		if closeErr != nil {
			_ = os.Remove(tempPath)
			return nil, totalBytes, fmt.Errorf("close upload temporary file: %w", closeErr)
		}
		totalBytes += written
		files = append(files, uploadFile{
			name:     name,
			tempPath: tempPath,
			size:     written,
		})
	}

	if len(files) == 0 {
		return nil, totalBytes, errNoUploadFiles
	}
	return files, totalBytes, nil
}

func copyUploadPart(dst io.Writer, src io.Reader, remaining int64) (int64, error) {
	if remaining < 0 {
		return 0, errUploadTooLarge
	}
	written, err := io.Copy(dst, io.LimitReader(src, remaining+1))
	if err != nil {
		return written, fmt.Errorf("copy upload data: %w", err)
	}
	if written > remaining {
		return written, errUploadTooLarge
	}
	return written, nil
}

func multipartPartFilename(part *multipart.Part) (string, error) {
	disposition := part.Header.Get("Content-Disposition")
	_, params, err := mime.ParseMediaType(disposition)
	if err != nil {
		return "", fmt.Errorf("parse multipart Content-Disposition: %w", err)
	}

	name := params["filename"]
	if headerName := strings.TrimSpace(part.Header.Get("X-File-Path")); headerName != "" {
		name = headerName
	}
	name = strings.TrimSpace(strings.ReplaceAll(name, "\\", "/"))
	if name == "" {
		return "", nil
	}
	if strings.ContainsRune(name, '\x00') {
		return "", fmt.Errorf("invalid upload path")
	}
	if strings.HasPrefix(name, "/") || (len(name) >= 2 && name[1] == ':') {
		return "", fmt.Errorf("upload path must be relative")
	}

	name = path.Clean(name)
	if name == "." || name == ".." || strings.HasPrefix(name, "../") {
		return "", fmt.Errorf("upload path escapes the upload root")
	}
	return name, nil
}

func removeUploadFiles(files []uploadFile) {
	for _, file := range files {
		if file.tempPath != "" {
			_ = os.Remove(file.tempPath)
		}
	}
}

func countUploadFiles(files []uploadFile) int {
	count := 0
	for _, file := range files {
		if !file.directory {
			count++
		}
	}
	return count
}

func uploadIsFolder(files []uploadFile) bool {
	if len(files) > 1 {
		return true
	}
	for _, file := range files {
		if file.directory || strings.Contains(file.name, "/") {
			return true
		}
	}
	return false
}

func uploadMetadata(name string, folder bool) (string, string) {
	if folder {
		return "", "inode/directory"
	}
	if strings.TrimSpace(name) == "" {
		return "", "application/octet-stream"
	}

	baseName := path.Base(strings.ReplaceAll(name, "\\", "/"))
	extension := strings.ToLower(path.Ext(baseName))
	if extension == "" {
		return "", "application/octet-stream"
	}

	switch extension {
	case ".jpg", ".jpeg":
		return extension, "image/jpeg"
	case ".opus":
		return extension, "audio/opus"
	case ".bin":
		return extension, "application/octet-stream"
	}

	mimeType := mime.TypeByExtension(extension)
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	return extension, mimeType
}

func (c *ipfsClient) add(ctx context.Context, files []uploadFile, folder bool) (addResult, error) {
	endpointURL, err := url.Parse(c.endpoint("api/v0/add"))
	if err != nil {
		return addResult{}, fmt.Errorf("parse IPFS add URL: %w", err)
	}
	query := endpointURL.Query()
	query.Set("pin", "false")
	query.Set("progress", "false")
	if folder {
		query.Set("empty-dirs", "true")
		query.Set("recursive", "true")
		query.Set("wrap-with-directory", "true")
	}
	endpointURL.RawQuery = query.Encode()

	pipeReader, pipeWriter := io.Pipe()
	multipartWriter := multipart.NewWriter(pipeWriter)
	writeDone := make(chan error, 1)
	go func() {
		writeErr := writeAddMultipart(multipartWriter, files)
		_ = pipeWriter.CloseWithError(writeErr)
		writeDone <- writeErr
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointURL.String(), pipeReader)
	if err != nil {
		_ = pipeReader.CloseWithError(err)
		<-writeDone
		return addResult{}, fmt.Errorf("create IPFS add request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", multipartWriter.FormDataContentType())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		_ = pipeReader.CloseWithError(err)
		<-writeDone
		return addResult{}, fmt.Errorf("request IPFS add: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxAPIResponse))
		message := strings.TrimSpace(string(body))
		if message == "" {
			message = resp.Status
		}
		<-writeDone
		return addResult{}, fmt.Errorf("IPFS add returned %s: %s", resp.Status, message)
	}

	result, readErr := readAddResponse(resp.Body)
	writeErr := <-writeDone
	if readErr != nil {
		return addResult{}, readErr
	}
	if writeErr != nil {
		return addResult{}, fmt.Errorf("send upload to IPFS: %w", writeErr)
	}
	return result, nil
}

func writeAddMultipart(writer *multipart.Writer, files []uploadFile) error {
	for _, file := range files {
		if file.directory {
			header := make(textproto.MIMEHeader)
			header.Set("Content-Disposition", fmt.Sprintf(
				`form-data; name="file"; filename="%s"`,
				escapeMultipartFilename(file.name),
			))
			header.Set("Content-Type", "application/x-directory")
			if _, err := writer.CreatePart(header); err != nil {
				return fmt.Errorf("create directory multipart part: %w", err)
			}
			continue
		}

		part, err := writer.CreateFormFile("file", file.name)
		if err != nil {
			return fmt.Errorf("create file multipart part: %w", err)
		}
		source, err := os.Open(file.tempPath)
		if err != nil {
			return fmt.Errorf("open upload temporary file: %w", err)
		}
		_, copyErr := io.Copy(part, source)
		closeErr := source.Close()
		if copyErr != nil {
			return fmt.Errorf("stream upload temporary file: %w", copyErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close upload temporary file: %w", closeErr)
		}
	}
	return writer.Close()
}

func escapeMultipartFilename(name string) string {
	return strings.NewReplacer(
		`\`, `\\`,
		`"`, `\"`,
		"\r", "",
		"\n", "",
	).Replace(name)
}

func readAddResponse(reader io.Reader) (addResult, error) {
	decoder := json.NewDecoder(reader)
	var last addEntry
	found := false
	for {
		var entry addEntry
		err := decoder.Decode(&entry)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return addResult{}, fmt.Errorf("decode IPFS add response: %w", err)
		}
		if entry.Hash != "" {
			last = entry
			found = true
		}
	}
	if !found {
		return addResult{}, fmt.Errorf("IPFS add response did not contain a CID")
	}

	size, err := parseAddSize(last.Size)
	if err != nil {
		return addResult{}, fmt.Errorf("parse IPFS add size: %w", err)
	}
	return addResult{CID: last.Hash, Size: size, Name: last.Name}, nil
}

func parseAddSize(raw json.RawMessage) (int64, error) {
	value := strings.TrimSpace(string(raw))
	if value == "" || value == "null" {
		return 0, nil
	}
	if strings.HasPrefix(value, `"`) {
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return 0, err
		}
		return strconv.ParseInt(text, 10, 64)
	}
	return strconv.ParseInt(value, 10, 64)
}
