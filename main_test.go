package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
	if stats.RepoPath != "/data/ipfs" || stats.Version != "fs-repo@16" {
		t.Errorf("metadata = %+v, want expected repository metadata", stats)
	}
}

func TestHomePageIsEmbeddedAndDoesNotRequireIPFS(t *testing.T) {
	client, err := newIPFSClient("http://127.0.0.1:1")
	if err != nil {
		t.Fatalf("newIPFSClient() error = %v", err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	newRouter(client).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if contentType := recorder.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "text/html") {
		t.Errorf("content type = %q, want text/html", contentType)
	}
	body := recorder.Body.String()
	for _, want := range []string{"Originless", `fetch("/stats"`, "Refresh stats"} {
		if !strings.Contains(body, want) {
			t.Errorf("response body does not contain %q", want)
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
	newRouter(client).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if contentType := recorder.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "application/json") {
		t.Errorf("content type = %q, want application/json", contentType)
	}
	var stats IPFSStats
	if err := json.Unmarshal(recorder.Body.Bytes(), &stats); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if stats.NumObjects != 7 || stats.SizeStat.RepoSize != 1024 || stats.RepoPath != "/repo" {
		t.Errorf("stats = %+v, want expected statistics", stats)
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
