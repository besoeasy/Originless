package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRepoStats(t *testing.T) {
	var method string
	var requestPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		requestPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"NumObjects": 12,
			"RepoPath":   "/data/ipfs",
			"SizeStat": map[string]uint64{
				"RepoSize":   2048,
				"StorageMax": 4096,
			},
			"Version": "fs-repo@16",
		})
	}))
	defer server.Close()

	client, err := newIPFSClient(server.URL)
	if err != nil {
		t.Fatalf("newIPFSClient() error = %v", err)
	}
	stats, err := client.repoStats(context.Background())
	if err != nil {
		t.Fatalf("repoStats() error = %v", err)
	}

	if method != http.MethodPost {
		t.Errorf("request method = %q, want %q", method, http.MethodPost)
	}
	if requestPath != "/api/v0/stats/repo" {
		t.Errorf("request path = %q, want %q", requestPath, "/api/v0/stats/repo")
	}
	if stats.NumObjects != 12 || stats.SizeStat.RepoSize != 2048 || stats.SizeStat.StorageMax != 4096 {
		t.Errorf("stats = %+v, want expected repository statistics", stats)
	}
	if stats.Version != "fs-repo@16" {
		t.Errorf("metadata = %+v, want expected repository metadata", stats)
	}
}

func TestHomePageIsEmbeddedAndDoesNotRequireIPFS(t *testing.T) {
	client, err := newIPFSClient("http://127.0.0.1:1")
	if err != nil {
		t.Fatalf("newIPFSClient() error = %v", err)
	}

	router := newRouter(client)
	for _, target := range []string{"/", "/index.html"} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, target, nil)
		router.ServeHTTP(recorder, request)

		if recorder.Code != http.StatusOK {
			t.Fatalf("target %s status = %d, want %d", target, recorder.Code, http.StatusOK)
		}
		if contentType := recorder.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "text/html") {
			t.Errorf("target %s content type = %q, want text/html", target, contentType)
		}
		body := recorder.Body.String()
		for _, want := range []string{"Originless", `fetch("/stats"`, "Refresh stats", "Shared Canvas", "Signed Chat"} {
			if !strings.Contains(body, want) {
				t.Errorf("target %s response body does not contain %q", target, want)
			}
		}
	}
}

func TestStatsJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"NumObjects":7,"RepoPath":"/repo","SizeStat":{"RepoSize":1024,"StorageMax":0},"Version":"fs-repo@16"}`))
	}))
	defer server.Close()

	client, err := newIPFSClient(server.URL)
	if err != nil {
		t.Fatalf("newIPFSClient() error = %v", err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/stats", nil)
	router := newRouter(client)
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if contentType := recorder.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "application/json") {
		t.Errorf("content type = %q, want application/json", contentType)
	}
	var response statsResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.NumObjects != 7 || response.SizeStat.RepoSize != 1024 || response.StorageMode != "ephemeral" {
		t.Errorf("stats = %+v, want expected statistics", response)
	}
	if response.Events.Total != 0 || response.Events.Count != 0 || response.Events.UniqueOwners != 0 {
		t.Errorf("event stats = %+v, want empty event stats", response.Events)
	}
}

func TestStatsIncludesEventStats(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"NumObjects":1,"RepoPath":"/repo","SizeStat":{"RepoSize":10,"StorageMax":100},"Version":"fs-repo@18"}`))
	}))
	defer server.Close()

	client, err := newIPFSClient(server.URL)
	if err != nil {
		t.Fatalf("newIPFSClient() error = %v", err)
	}
	router := newRouter(client)
	now := time.Now().Truncate(time.Second)
	raw, _ := makeSignedEvent(t, now.Unix(), now.Unix()+3600, "chat", map[string]any{"message": "hello"}, []string{"room:lobby"}, "")
	publishTestEvent(t, router, raw)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/stats", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	var response statsResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Events.Count != 1 || response.Events.Total != 1 || response.Events.UniqueOwners != 1 {
		t.Errorf("event counts = %+v, want one active event and owner", response.Events)
	}
	if len(response.Events.TopCollections) != 1 || response.Events.TopCollections[0].Collection != "chat" || response.Events.TopCollections[0].Count != 1 {
		t.Errorf("top collections = %+v, want chat:1", response.Events.TopCollections)
	}
}

func TestStatsJSONUnavailable(t *testing.T) {
	client, err := newIPFSClient("http://127.0.0.1:1")
	if err != nil {
		t.Fatalf("newIPFSClient() error = %v", err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/stats", nil)
	newRouter(client).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}
	if contentType := recorder.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "application/json") {
		t.Errorf("content type = %q, want application/json", contentType)
	}
	if !strings.Contains(recorder.Body.String(), "IPFS node unavailable") {
		t.Errorf("response body = %q, want unavailable message", recorder.Body.String())
	}
}

func TestHealthzJSON(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	newRouter(nil).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if contentType := recorder.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "application/json") {
		t.Errorf("content type = %q, want application/json", contentType)
	}
	var payload map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload["status"] != "ok" {
		t.Errorf("payload = %+v, want status ok", payload)
	}
}

func TestCORSMiddleware(t *testing.T) {
	preflight := httptest.NewRecorder()
	newRouter(nil).ServeHTTP(preflight, httptest.NewRequest(http.MethodOptions, "/events", nil))

	if preflight.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d, want %d", preflight.Code, http.StatusNoContent)
	}

	get := httptest.NewRecorder()
	newRouter(nil).ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	for _, recorder := range []*httptest.ResponseRecorder{preflight, get} {
		if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "*" {
			t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, "*")
		}
		if got := recorder.Header().Get("Access-Control-Allow-Methods"); got != "GET, POST, OPTIONS, HEAD" {
			t.Errorf("Access-Control-Allow-Methods = %q, want GET, POST, OPTIONS, HEAD", got)
		}
		if got := recorder.Header().Get("Access-Control-Allow-Headers"); got != "*" {
			t.Errorf("Access-Control-Allow-Headers = %q, want %q", got, "*")
		}
	}
}
