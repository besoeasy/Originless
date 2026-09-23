package modules

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	// DefaultIPFSAPIURL is Kubo's loopback RPC endpoint inside the container.
	DefaultIPFSAPIURL = "http://127.0.0.1:5001"

	defaultIPFSUploadTimeout = 15 * time.Minute
)

// IPFSFile is one staged file to add through Kubo's multipart RPC endpoint.
// Path is the local temporary file; Name is the path stored in IPFS.
type IPFSFile struct {
	Path string
	Name string
}

// IPFSAddResult is the CID and metadata returned by Kubo.
type IPFSAddResult struct {
	CID  string
	Name string
	Size int64
}

// IPFSClient is a small, streaming client for the Kubo HTTP RPC API.
type IPFSClient struct {
	baseURL    string
	httpClient *http.Client
}

// NewIPFSClient creates a client for IPFS_API_URL, defaulting to Kubo's
// loopback API. The timeout covers large single-file and directory uploads.
func NewIPFSClient() *IPFSClient {
	baseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("IPFS_API_URL")), "/")
	if baseURL == "" {
		baseURL = DefaultIPFSAPIURL
	}
	return newIPFSClient(baseURL, &http.Client{Timeout: defaultIPFSUploadTimeout})
}

func newIPFSClient(baseURL string, httpClient *http.Client) *IPFSClient {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultIPFSUploadTimeout}
	}
	return &IPFSClient{
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: httpClient,
	}
}

// AddFile adds and pins one file, preserving its sanitized name.
func (c *IPFSClient) AddFile(ctx context.Context, filePath, filename string) (IPFSAddResult, error) {
	if strings.TrimSpace(filename) == "" || filePath == "" {
		return IPFSAddResult{}, fmt.Errorf("IPFS file path and name are required")
	}
	return c.add(ctx, false, []IPFSFile{{Path: filePath, Name: filename}})
}

// AddFolder adds and pins all files as one wrapped directory. The last entry
// returned by Kubo is the recursive directory root CID.
func (c *IPFSClient) AddFolder(ctx context.Context, files []IPFSFile) (IPFSAddResult, error) {
	if len(files) == 0 {
		return IPFSAddResult{}, fmt.Errorf("IPFS folder requires at least one file")
	}
	return c.add(ctx, true, files)
}

type ipfsAddResponse struct {
	Name string `json:"Name"`
	Hash string `json:"Hash"`
	Size string `json:"Size"`
}

func (c *IPFSClient) add(ctx context.Context, folder bool, files []IPFSFile) (IPFSAddResult, error) {
	if c == nil || c.httpClient == nil || strings.TrimSpace(c.baseURL) == "" {
		return IPFSAddResult{}, fmt.Errorf("IPFS client is not configured")
	}
	if len(files) == 0 {
		return IPFSAddResult{}, fmt.Errorf("no files supplied")
	}

	ordered := append([]IPFSFile(nil), files...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Name < ordered[j].Name })

	pr, pw := io.Pipe()
	multipartWriter := multipart.NewWriter(pw)
	contentType := multipartWriter.FormDataContentType()

	go func() {
		for _, file := range ordered {
			if file.Path == "" || file.Name == "" {
				_ = pw.CloseWithError(fmt.Errorf("invalid staged IPFS file"))
				return
			}
			part, err := multipartWriter.CreateFormFile("file", file.Name)
			if err != nil {
				_ = pw.CloseWithError(fmt.Errorf("create IPFS multipart part: %w", err))
				return
			}
			f, err := os.Open(file.Path)
			if err != nil {
				_ = pw.CloseWithError(fmt.Errorf("open staged IPFS file %q: %w", file.Name, err))
				return
			}
			_, copyErr := io.Copy(part, f)
			closeErr := f.Close()
			if copyErr != nil {
				_ = pw.CloseWithError(fmt.Errorf("stream staged IPFS file %q: %w", file.Name, copyErr))
				return
			}
			if closeErr != nil {
				_ = pw.CloseWithError(fmt.Errorf("close staged IPFS file %q: %w", file.Name, closeErr))
				return
			}
		}
		_ = pw.CloseWithError(multipartWriter.Close())
	}()

	query := url.Values{}
	query.Set("pin", "true")
	query.Set("cid-version", "1")
	query.Set("raw-leaves", "true")
	if folder {
		query.Set("wrap-with-directory", "true")
		query.Set("recursive", "true")
	}
	endpoint := c.baseURL + "/api/v0/add?" + query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, pr)
	if err != nil {
		_ = pr.CloseWithError(err)
		return IPFSAddResult{}, fmt.Errorf("create Kubo add request: %w", err)
	}
	req.Header.Set("Content-Type", contentType)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		_ = pr.Close()
		return IPFSAddResult{}, fmt.Errorf("Kubo add request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return IPFSAddResult{}, fmt.Errorf("Kubo add returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	decoder := json.NewDecoder(resp.Body)
	var last ipfsAddResponse
	entryCount := 0
	for {
		var entry ipfsAddResponse
		if err := decoder.Decode(&entry); err != nil {
			if err == io.EOF {
				break
			}
			return IPFSAddResult{}, fmt.Errorf("decode Kubo add response: %w", err)
		}
		entryCount++
		last = entry
	}
	if entryCount == 0 || strings.TrimSpace(last.Hash) == "" {
		return IPFSAddResult{}, fmt.Errorf("Kubo add returned no CID")
	}

	size, _ := strconv.ParseInt(last.Size, 10, 64)
	return IPFSAddResult{CID: last.Hash, Name: last.Name, Size: size}, nil
}
