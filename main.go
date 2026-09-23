package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
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
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: requestTimeout,
		},
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

type homeHandler struct {
	template *template.Template
}

type homeData struct {
	AppVersion string
}

type statsHandler struct {
	client   *ipfsClient
	template *template.Template
}

type pageData struct {
	AppVersion string
	Stats      IPFSStats
	FetchedAt  string
	HasError   bool
}

var homePage = template.Must(template.New("home").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Originless</title>
  <style>
    :root { color-scheme: light dark; font-family: system-ui, sans-serif; }
    body { margin: 0; min-height: 100vh; display: grid; place-items: center; background: #10131a; color: #f4f6fb; }
    main { width: min(42rem, calc(100% - 2rem)); padding: 2rem; border: 1px solid #2d3545; border-radius: 1rem; background: #181d27; box-shadow: 0 1rem 3rem #0004; }
    h1 { margin: 0; font-size: 2rem; }
    p { color: #9da9bd; line-height: 1.6; }
    ul { padding-left: 1.25rem; line-height: 2; }
    a { color: #9ecbff; }
    footer { margin-top: 1.75rem; color: #778399; font-size: .85rem; }
  </style>
</head>
<body>
  <main>
    <h1>Originless</h1>
    <p>Originless is running with an embedded IPFS node.</p>
    <ul>
      <li><a href="/stats">IPFS statistics (JSON)</a></li>
      <li><a href="/stats?format=html">IPFS statistics (HTML)</a></li>
      <li><a href="/healthz">Health check</a></li>
    </ul>
    <footer>Originless v{{.AppVersion}}</footer>
  </main>
</body>
</html>`))

var statsPage = template.Must(template.New("stats").Funcs(template.FuncMap{
	"formatBytes": formatBytes,
}).Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Originless IPFS Stats</title>
  <style>
    :root { color-scheme: light dark; font-family: system-ui, sans-serif; }
    body { margin: 0; min-height: 100vh; display: grid; place-items: center; background: #10131a; color: #f4f6fb; }
    main { width: min(42rem, calc(100% - 2rem)); padding: 2rem; border: 1px solid #2d3545; border-radius: 1rem; background: #181d27; box-shadow: 0 1rem 3rem #0004; }
    h1 { margin: 0; font-size: 2rem; }
    h2 { margin: 0 0 1.25rem; font-size: 1rem; color: #9da9bd; font-weight: 500; }
    .subtitle { margin: .35rem 0 1.75rem; color: #9da9bd; }
    dl { display: grid; grid-template-columns: minmax(9rem, 1fr) 2fr; gap: .8rem 1rem; margin: 0; }
    dt { color: #9da9bd; }
    dd { margin: 0; overflow-wrap: anywhere; }
    code { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
    .error { padding: 1rem; border-radius: .5rem; background: #4a2028; color: #ffd9df; }
    footer { margin-top: 1.75rem; color: #778399; font-size: .85rem; }
  </style>
</head>
<body>
  <main>
    <h1>Originless</h1>
    <p class="subtitle">IPFS node stats · v{{.AppVersion}}</p>
    {{if .HasError}}
      <h2>IPFS node unavailable</h2>
      <div class="error">The stats endpoint could not be reached. Check that the IPFS daemon is running.</div>
    {{else}}
      <h2>Repository statistics</h2>
      <dl>
        <dt>Repository size</dt>
        <dd>{{formatBytes .Stats.SizeStat.RepoSize}}</dd>
        <dt>Storage limit</dt>
        <dd>{{if .Stats.SizeStat.StorageMax}}{{formatBytes .Stats.SizeStat.StorageMax}}{{else}}Unlimited{{end}}</dd>
        <dt>Objects</dt>
        <dd>{{.Stats.NumObjects}}</dd>
        <dt>Repository path</dt>
        <dd><code>{{if .Stats.RepoPath}}{{.Stats.RepoPath}}{{else}}—{{end}}</code></dd>
        <dt>Repository version</dt>
        <dd>{{if .Stats.Version}}{{.Stats.Version}}{{else}}Unknown{{end}}</dd>
      </dl>
    {{end}}
    <footer>Updated {{.FetchedAt}}</footer>
  </main>
</body>
</html>`))

func (h *homeHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.template.Execute(w, homeData{AppVersion: version}); err != nil {
		log.Printf("render home page: %v", err)
	}
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

	data := pageData{
		AppVersion: version,
		FetchedAt:  time.Now().UTC().Format("2006-01-02 15:04:05 UTC"),
	}
	stats, err := h.client.repoStats(ctx)
	if err != nil {
		log.Printf("IPFS stats request failed: %v", err)
		if wantsHTML(r) {
			data.HasError = true
			h.render(w, http.StatusServiceUnavailable, data)
			return
		}
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "IPFS node unavailable",
		})
		return
	}

	if wantsHTML(r) {
		data.Stats = stats
		h.render(w, http.StatusOK, data)
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func wantsHTML(r *http.Request) bool {
	return strings.EqualFold(r.URL.Query().Get("format"), "html")
}

func (h *statsHandler) render(w http.ResponseWriter, status int, data pageData) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := h.template.Execute(w, data); err != nil {
		log.Printf("render stats page: %v", err)
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
	mux.Handle("/", &homeHandler{template: homePage})
	mux.Handle("/stats", &statsHandler{client: client, template: statsPage})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, "ok\n")
	})
	return mux
}

func formatBytes(bytes uint64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}

	value := float64(bytes)
	units := []string{"KiB", "MiB", "GiB", "TiB", "PiB"}
	for _, suffix := range units {
		value /= unit
		if value < unit {
			return fmt.Sprintf("%.1f %s", value, suffix)
		}
	}
	return fmt.Sprintf("%.1f EiB", value)
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
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
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
