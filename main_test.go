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

func TestStatsPage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"NumObjects":7,"RepoPath":"/repo","SizeStat":{"RepoSize":1024,"StorageMax":0},"Version":"fs-repo@16"}`))
	}))
	defer server.Close()

	client, err := newIPFSClient(server.URL)
	if err != nil {
		t.Fatalf("newIPFSClient() error = %v", err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	newRouter(client).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	body := recorder.Body.String()
	for _, want := range []string{"Originless", "1.0 KiB", "Unlimited", "7", "/repo", "fs-repo@16"} {
		if !strings.Contains(body, want) {
			t.Errorf("response body does not contain %q", want)
		}
	}
}

func TestStatsPageUnavailable(t *testing.T) {
	client, err := newIPFSClient("http://127.0.0.1:1")
	if err != nil {
		t.Fatalf("newIPFSClient() error = %v", err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	newRouter(client).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}
	if !strings.Contains(recorder.Body.String(), "IPFS node unavailable") {
		t.Errorf("response body = %q, want unavailable message", recorder.Body.String())
	}
}

func TestFormatBytes(t *testing.T) {
	tests := map[uint64]string{
		0:           "0 B",
		1023:        "1023 B",
		1024:        "1.0 KiB",
		1024 * 1024: "1.0 MiB",
	}
	for input, want := range tests {
		if got := formatBytes(input); got != want {
			t.Errorf("formatBytes(%d) = %q, want %q", input, got, want)
		}
	}
}
