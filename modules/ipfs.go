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
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Client struct {
	baseURL    string
	httpClient *http.Client
}

type HealthResult struct {
	Healthy bool
	Peers   int
	Error   string
}

type Stats struct {
	Bandwidth struct {
		TotalIn  int64  `json:"totalIn"`
		TotalOut int64  `json:"totalOut"`
		RateIn   int64  `json:"rateIn"`
		RateOut  int64  `json:"rateOut"`
		Interval string `json:"interval"`
	} `json:"bandwidth"`
	Repository struct {
		Size       int64  `json:"size"`
		StorageMax int64  `json:"storageMax"`
		NumObjects int64  `json:"numObjects"`
		Path       string `json:"path"`
		Version    string `json:"version"`
	} `json:"repository"`
	Node struct {
		ID              string `json:"id"`
		PublicKey       string `json:"publicKey"`
		AgentVersion    string `json:"agentVersion"`
		ProtocolVersion string `json:"protocolVersion"`
	} `json:"node"`
	Peers struct {
		Count int `json:"count"`
	} `json:"peers"`
}

type addResponse struct {
	Name string `json:"Name"`
	Hash string `json:"Hash"`
	Size string `json:"Size"`
}

func NewClient() *Client {
	return &Client{
		baseURL: IPFSAPI,
		// Single shared client — connection to 127.0.0.1:5001 is reused across requests.
		httpClient: &http.Client{Timeout: 10 * time.Minute},
	}
}

func (c *Client) CheckHealth(ctx context.Context) HealthResult {
	peers, err := c.postJSON(ctx, "/api/v0/swarm/peers", 5*time.Second, nil)
	if err != nil {
		return HealthResult{Healthy: false, Error: err.Error()}
	}

	count := 0
	if rawPeers, ok := peers["Peers"].([]any); ok {
		count = len(rawPeers)
	}

	return HealthResult{
		Healthy: count >= 1,
		Peers:   count,
	}
}

// GetStats fetches bandwidth, repo, node identity, and peer stats concurrently.
func (c *Client) GetStats(ctx context.Context) (*Stats, error) {
	type fetchResult struct {
		data map[string]any
		err  error
	}

	fetch := func(path string) chan fetchResult {
		ch := make(chan fetchResult, 1)
		go func() {
			data, err := c.postJSON(ctx, path, 5*time.Second, nil)
			ch <- fetchResult{data, err}
		}()
		return ch
	}

	bwCh := fetch("/api/v0/stats/bw?interval=5m")
	repoCh := fetch("/api/v0/repo/stat")
	idCh := fetch("/api/v0/id")
	peersCh := fetch("/api/v0/swarm/peers")

	bwR := <-bwCh
	repoR := <-repoCh
	idR := <-idCh
	peersR := <-peersCh

	if bwR.err != nil {
		return nil, bwR.err
	}
	if repoR.err != nil {
		return nil, repoR.err
	}
	if idR.err != nil {
		return nil, idR.err
	}
	if peersR.err != nil {
		return nil, peersR.err
	}

	bw := bwR.data
	repo := repoR.data
	id := idR.data
	peers := peersR.data

	stats := &Stats{}
	stats.Bandwidth.TotalIn = jsonInt64(bw["TotalIn"])
	stats.Bandwidth.TotalOut = jsonInt64(bw["TotalOut"])
	stats.Bandwidth.RateIn = jsonInt64(bw["RateIn"])
	stats.Bandwidth.RateOut = jsonInt64(bw["RateOut"])
	stats.Bandwidth.Interval = "5m"

	stats.Repository.Size = jsonInt64(repo["RepoSize"])
	stats.Repository.StorageMax = jsonInt64(repo["StorageMax"])
	stats.Repository.NumObjects = jsonInt64(repo["NumObjects"])
	stats.Repository.Path = jsonString(repo["RepoPath"])
	stats.Repository.Version = jsonString(repo["Version"])

	stats.Node.ID = jsonString(id["ID"])
	stats.Node.PublicKey = jsonString(id["PublicKey"])
	stats.Node.AgentVersion = jsonString(id["AgentVersion"])
	stats.Node.ProtocolVersion = jsonString(id["ProtocolVersion"])

	if rawPeers, ok := peers["Peers"].([]any); ok {
		stats.Peers.Count = len(rawPeers)
	}

	return stats, nil
}

// AddFile streams filePath directly into the IPFS API without buffering in RAM.
func (c *Client) AddFile(ctx context.Context, filePath, filename string) (string, error) {
	pr, pw := io.Pipe()
	defer pr.CloseWithError(fmt.Errorf("pipe closed"))
	mw := multipart.NewWriter(pw)
	contentType := mw.FormDataContentType()

	go func() {
		part, err := mw.CreateFormFile("file", filename)
		if err != nil {
			pw.CloseWithError(err)
			return
		}
		f, err := os.Open(filePath)
		if err != nil {
			pw.CloseWithError(err)
			return
		}
		defer f.Close()
		if _, err := io.Copy(part, f); err != nil {
			pw.CloseWithError(err)
			return
		}
		pw.CloseWithError(mw.Close())
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v0/add?pin=false", pr)
	if err != nil {
		pr.CloseWithError(err)
		return "", err
	}
	req.Header.Set("Content-Type", contentType)

	resp, err := c.do(req)
	if err != nil {
		return "", err
	}
	defer resp.Close()

	var result addResponse
	if err := json.NewDecoder(resp).Decode(&result); err != nil {
		return "", err
	}
	if result.Hash == "" {
		return "", fmt.Errorf("invalid IPFS response for file upload")
	}
	return result.Hash, nil
}

// AddDirectory streams all files in the map directly into the IPFS API without buffering in RAM.
func (c *Client) AddDirectory(ctx context.Context, files map[string]string) (string, error) {
	pr, pw := io.Pipe()
	defer pr.CloseWithError(fmt.Errorf("pipe closed"))
	mw := multipart.NewWriter(pw)
	contentType := mw.FormDataContentType()

	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	go func() {
		for _, relativePath := range paths {
			part, err := mw.CreateFormFile("file", relativePath)
			if err != nil {
				pw.CloseWithError(fmt.Errorf("creating multipart part for %s: %w", relativePath, err))
				return
			}
			f, err := os.Open(files[relativePath])
			if err != nil {
				pw.CloseWithError(fmt.Errorf("opening %s: %w", relativePath, err))
				return
			}
			if _, err := io.Copy(part, f); err != nil {
				f.Close()
				pw.CloseWithError(fmt.Errorf("reading %s: %w", relativePath, err))
				return
			}
			f.Close()
		}
		pw.CloseWithError(mw.Close())
	}()

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.baseURL+"/api/v0/add?pin=false&wrap-with-directory=true&recursive=true",
		pr,
	)
	if err != nil {
		pr.CloseWithError(err)
		return "", err
	}
	req.Header.Set("Content-Type", contentType)

	resp, err := c.do(req)
	if err != nil {
		return "", err
	}
	defer resp.Close()

	entries, err := parseAddResponses(resp)
	if err != nil {
		return "", err
	}
	if len(entries) == 0 {
		return "", fmt.Errorf("invalid IPFS response for folder upload")
	}
	root := entries[len(entries)-1]
	if root.Hash == "" {
		return "", fmt.Errorf("invalid IPFS response for folder upload")
	}
	return root.Hash, nil
}

// postJSON applies a per-call timeout via context rather than allocating a new http.Client.
func (c *Client) postJSON(ctx context.Context, path string, timeout time.Duration, body io.Reader) (map[string]any, error) {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, body)
	if err != nil {
		return nil, err
	}

	reader, err := c.do(req)
	if err != nil {
		return nil, err
	}
	defer reader.Close()

	var payload map[string]any
	if err := json.NewDecoder(reader).Decode(&payload); err != nil {
		return nil, err
	}
	return payload, nil
}

// do executes the request using the shared client, preserving connection pooling.
func (c *Client) do(req *http.Request) (io.ReadCloser, error) {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("HTTP %d %s: %s", resp.StatusCode, resp.Status, strings.TrimSpace(string(body)))
	}

	return resp.Body, nil
}

// parseAddResponses stream-decodes the NDJSON response from the IPFS add API.
func parseAddResponses(reader io.Reader) ([]addResponse, error) {
	var entries []addResponse
	dec := json.NewDecoder(reader)
	for {
		var entry addResponse
		if err := dec.Decode(&entry); err == io.EOF {
			break
		} else if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func jsonInt64(value any) int64 {
	switch typed := value.(type) {
	case float64:
		return int64(typed)
	case int64:
		return typed
	case int:
		return int64(typed)
	case string:
		parsed, _ := strconvParseInt(typed)
		return parsed
	default:
		return 0
	}
}

func jsonString(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprint(value)
}

func strconvParseInt(value string) (int64, error) {
	var parsed int64
	_, err := fmt.Sscan(value, &parsed)
	return parsed, err
}

func MimeType(path string) string {
	ext := filepath.Ext(path)
	if ext == "" {
		return "application/octet-stream"
	}
	if mime := mimeTypeByExtension(ext); mime != "" {
		return mime
	}
	return "application/octet-stream"
}

func mimeTypeByExtension(ext string) string {
	switch strings.ToLower(ext) {
	case ".html", ".htm":
		return "text/html"
	case ".css":
		return "text/css"
	case ".js":
		return "application/javascript"
	case ".json":
		return "application/json"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".svg":
		return "image/svg+xml"
	case ".pdf":
		return "application/pdf"
	case ".zip":
		return "application/zip"
	case ".mp3":
		return "audio/mpeg"
	case ".mp4":
		return "video/mp4"
	case ".txt":
		return "text/plain"
	case ".wasm":
		return "application/wasm"
	default:
		return ""
	}
}

func (c *Client) PinAdd(ctx context.Context, cid string) error {
	return c.PinAddWait(ctx, cid, 30*time.Second)
}

func (c *Client) PinAddWait(ctx context.Context, cid string, timeout time.Duration) error {
	_, err := c.postJSON(ctx, "/api/v0/pin/add?arg="+url.QueryEscape(cid), timeout, nil)
	return err
}

func (c *Client) PinRemove(ctx context.Context, cid string) error {
	_, err := c.postJSON(ctx, "/api/v0/pin/rm?arg="+url.QueryEscape(cid), 30*time.Second, nil)
	return err
}

func (c *Client) PinList(ctx context.Context) (map[string]bool, error) {
	data, err := c.postJSON(ctx, "/api/v0/pin/ls?type=recursive", 30*time.Second, nil)
	if err != nil {
		return nil, err
	}

	result := make(map[string]bool)
	if keys, ok := data["Keys"].(map[string]any); ok {
		for cid := range keys {
			result[cid] = true
		}
	}
	return result, nil
}

func (c *Client) ObjectStat(ctx context.Context, cid string) (int64, error) {
	data, err := c.postJSON(ctx, "/api/v0/object/stat?arg="+url.QueryEscape(cid), 10*time.Second, nil)
	if err != nil {
		return 0, err
	}
	return jsonInt64(data["CumulativeSize"]), nil
}
