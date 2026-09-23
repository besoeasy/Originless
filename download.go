package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
)

type downloadableFile struct {
	CID       string
	Name      string
	Extension string
	MIME      string
	Size      int64
}

type downloadRegistry struct {
	mu    sync.RWMutex
	files map[string]downloadableFile
}

func newDownloadRegistry() *downloadRegistry {
	return &downloadRegistry{files: make(map[string]downloadableFile)}
}

func (r *downloadRegistry) add(file downloadableFile) {
	if r == nil || file.CID == "" || !isDownloadableExtension(file.Extension) {
		return
	}
	r.mu.Lock()
	r.files[file.CID] = file
	r.mu.Unlock()
}

func (r *downloadRegistry) get(cid string) (downloadableFile, bool) {
	if r == nil {
		return downloadableFile{}, false
	}
	r.mu.RLock()
	file, ok := r.files[cid]
	r.mu.RUnlock()
	return file, ok
}

func isDownloadableExtension(extension string) bool {
	switch strings.ToLower(extension) {
	case ".bin", ".blob", ".json":
		return true
	default:
		return false
	}
}

type downloadHandler struct {
	client   *ipfsClient
	registry *downloadRegistry
}

func (h *downloadHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	const prefix = "/down/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{
			"error": "method not allowed",
		})
		return
	}

	rawCID := strings.TrimPrefix(r.URL.Path, prefix)
	if rawCID == "" || strings.Contains(rawCID, "/") {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "CID not found"})
		return
	}
	cid, err := url.PathUnescape(rawCID)
	if err != nil || cid == "" || strings.Contains(cid, "/") {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "CID not found"})
		return
	}

	file, ok := h.registry.get(cid)
	if !ok || !isDownloadableExtension(file.Extension) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "CID not found"})
		return
	}
	if file.MIME == "" {
		file.MIME = "application/octet-stream"
	}

	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Type", file.MIME)
	w.Header().Set("Content-Length", strconv.FormatInt(file.Size, 10))
	w.Header().Set("ETag", fmt.Sprintf("\"%s\"", file.CID))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	filename := path.Base(strings.ReplaceAll(file.Name, "\\", "/"))
	if filename == "." || filename == "/" || filename == "" {
		filename = file.CID + file.Extension
	}
	if disposition := mime.FormatMediaType("attachment", map[string]string{"filename": filename}); disposition != "" {
		w.Header().Set("Content-Disposition", disposition)
	}

	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}

	body, err := h.client.cat(r.Context(), file.CID)
	if err != nil {
		log.Printf("IPFS download failed for %s: %v", file.CID, err)
		status := http.StatusBadGateway
		var ipfsErr *ipfsDownloadError
		if errors.As(err, &ipfsErr) && ipfsErr.status == http.StatusNotFound {
			status = http.StatusNotFound
		}
		writeJSON(w, status, map[string]string{"error": "content unavailable"})
		return
	}
	defer body.Close()

	w.WriteHeader(http.StatusOK)
	if _, err := io.Copy(w, body); err != nil {
		log.Printf("stream IPFS download %s: %v", file.CID, err)
	}
}

type ipfsDownloadError struct {
	status  int
	message string
}

func (e *ipfsDownloadError) Error() string {
	return e.message
}

func (c *ipfsClient) cat(ctx context.Context, cid string) (io.ReadCloser, error) {
	endpointURL, err := url.Parse(c.endpoint("api/v0/cat"))
	if err != nil {
		return nil, fmt.Errorf("parse IPFS cat URL: %w", err)
	}
	query := endpointURL.Query()
	query.Set("arg", cid)
	query.Set("offline", "true")
	query.Set("progress", "false")
	endpointURL.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointURL.String(), http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("create IPFS cat request: %w", err)
	}
	req.Header.Set("Accept", "application/octet-stream")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request IPFS cat: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxAPIResponse))
		message := strings.TrimSpace(string(body))
		if message == "" {
			message = resp.Status
		}
		return nil, &ipfsDownloadError{
			status:  resp.StatusCode,
			message: fmt.Sprintf("IPFS cat returned %s: %s", resp.Status, message),
		}
	}
	return resp.Body, nil
}
