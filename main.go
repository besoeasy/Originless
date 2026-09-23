package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	defaultPort       = "3232"
	defaultIPFSAPIURL = "http://127.0.0.1:5001"
	requestTimeout    = 5 * time.Second
	maxAPIResponse    = 1 << 20
)

var version = "1.0.0"

//go:embed static/index.html
var indexHTML []byte

type IPFSStats struct {
	NumObjects uint64 `json:"NumObjects"`
	RepoPath   string `json:"RepoPath"`
	SizeStat   struct {
		RepoSize   uint64 `json:"RepoSize"`
		StorageMax uint64 `json:"StorageMax"`
	} `json:"SizeStat"`
	Version string `json:"Version"`
}

type ipfsClient struct {
	baseURL    *url.URL
	httpClient *http.Client
}

func newIPFSClient(rawURL string) (*ipfsClient, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		rawURL = defaultIPFSAPIURL
	}

	baseURL, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse IPFS API URL: %w", err)
	}
	if (baseURL.Scheme != "http" && baseURL.Scheme != "https") || baseURL.Host == "" {
		return nil, fmt.Errorf("IPFS API URL must be an http(s) URL")
	}

	return &ipfsClient{
		baseURL:    baseURL,
		httpClient: &http.Client{},
	}, nil
}

func (c *ipfsClient) endpoint(endpointPath string) string {
	endpointURL := *c.baseURL
	basePath := strings.Trim(endpointURL.Path, "/")
	endpointURL.Path = "/" + path.Join(basePath, endpointPath)
	return endpointURL.String()
}

func (c *ipfsClient) repoStats(ctx context.Context) (IPFSStats, error) {
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.endpoint("api/v0/stats/repo"),
		http.NoBody,
	)
	if err != nil {
		return IPFSStats{}, fmt.Errorf("create IPFS stats request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return IPFSStats{}, fmt.Errorf("request IPFS stats: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxAPIResponse))
	if err != nil {
		return IPFSStats{}, fmt.Errorf("read IPFS stats response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		message := strings.TrimSpace(string(body))
		if message == "" {
			message = resp.Status
		}
		return IPFSStats{}, fmt.Errorf("IPFS API returned %s: %s", resp.Status, message)
	}

	var stats IPFSStats
	if err := json.Unmarshal(body, &stats); err != nil {
		return IPFSStats{}, fmt.Errorf("decode IPFS stats response: %w", err)
	}
	return stats, nil
}

type statsHandler struct {
	client *ipfsClient
}

func (h *statsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/stats" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()

	stats, err := h.client.repoStats(ctx)
	if err != nil {
		log.Printf("IPFS stats request failed: %v", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "IPFS node unavailable",
		})
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func serveIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.Method == http.MethodHead {
		return
	}
	if _, err := w.Write(indexHTML); err != nil {
		log.Printf("write home page: %v", err)
	}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("encode JSON response: %v", err)
	}
}

func newRouter(client *ipfsClient) http.Handler {
	mux := http.NewServeMux()
	downloads := newDownloadRegistry()
	mux.HandleFunc("/", serveIndex)
	mux.Handle("/stats", &statsHandler{client: client})
	mux.Handle("/up", &uploadHandler{client: client, downloads: downloads})
	mux.Handle("/upf", &uploadHandler{client: client, folder: true, downloads: downloads})
	mux.Handle("/down/", &downloadHandler{client: client, registry: downloads})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	return mux
}

func configuredPort() (string, error) {
	port := strings.TrimSpace(os.Getenv("PORT"))
	if port == "" {
		return defaultPort, nil
	}

	value, err := strconv.Atoi(port)
	if err != nil || value < 1 || value > 65535 {
		return "", fmt.Errorf("invalid PORT %q: must be between 1 and 65535", port)
	}
	return strconv.Itoa(value), nil
}

func main() {
	port, err := configuredPort()
	if err != nil {
		log.Fatal(err)
	}

	client, err := newIPFSClient(os.Getenv("IPFS_API_URL"))
	if err != nil {
		log.Fatal(err)
	}

	server := &http.Server{
		Addr:              ":" + port,
		Handler:           newRouter(client),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       0,
		WriteTimeout:      0,
		IdleTimeout:       60 * time.Second,
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-stop
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			log.Printf("HTTP shutdown failed: %v", err)
		}
	}()

	log.Printf("Originless %s listening on :%s (IPFS API %s)", version, port, client.baseURL)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
