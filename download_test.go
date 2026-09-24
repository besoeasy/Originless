package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type localContentServer struct {
	server      *httptest.Server
	catCalls    int
	lastCID     string
	lastOffline string
	content     string
	status      int
}

func newLocalContentServer(t *testing.T, content string, status int) *localContentServer {
	t.Helper()
	local := &localContentServer{content: content, status: status}
	local.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v0/cat" {
			http.NotFound(w, r)
			return
		}
		local.catCalls++
		local.lastCID = r.URL.Query().Get("arg")
		local.lastOffline = r.URL.Query().Get("offline")
		if local.status != http.StatusOK {
			http.Error(w, "not found", local.status)
			return
		}
		_, _ = io.WriteString(w, local.content)
	}))
	return local
}

func (s *localContentServer) close() {
	s.server.Close()
}

func TestDownloadAllowsAnyLocalCID(t *testing.T) {
	upstream := newLocalContentServer(t, "local content", http.StatusOK)
	defer upstream.close()

	client, err := newIPFSClient(upstream.server.URL)
	if err != nil {
		t.Fatalf("newIPFSClient() error = %v", err)
	}
	recorder := httptest.NewRecorder()
	newRouter(client).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/ipfs/bafy-any-local-cid", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if recorder.Body.String() != "local content" {
		t.Errorf("body = %q, want local content", recorder.Body.String())
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/octet-stream" {
		t.Errorf("content type = %q, want application/octet-stream", got)
	}
	if got := recorder.Header().Get("Content-Disposition"); !strings.Contains(got, "attachment") || !strings.Contains(got, "bafy-any-local-cid") {
		t.Errorf("content disposition = %q, want attachment filename", got)
	}
	if upstream.catCalls != 1 || upstream.lastCID != "bafy-any-local-cid" || upstream.lastOffline != "true" {
		t.Errorf("cat calls = %d, CID = %q, offline = %q; want one local cat request", upstream.catCalls, upstream.lastCID, upstream.lastOffline)
	}
}

func TestLegacyDownloadPathIsNotExposed(t *testing.T) {
	upstream := newLocalContentServer(t, "unused", http.StatusOK)
	defer upstream.close()

	client, err := newIPFSClient(upstream.server.URL)
	if err != nil {
		t.Fatalf("newIPFSClient() error = %v", err)
	}
	recorder := httptest.NewRecorder()
	newRouter(client).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/down/bafy-legacy-path", nil))

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNotFound)
	}
	if upstream.catCalls != 0 {
		t.Errorf("cat calls = %d, want 0 for the removed legacy path", upstream.catCalls)
	}
}

func TestDownloadMissingLocalCID(t *testing.T) {
	upstream := newLocalContentServer(t, "", http.StatusNotFound)
	defer upstream.close()

	client, err := newIPFSClient(upstream.server.URL)
	if err != nil {
		t.Fatalf("newIPFSClient() error = %v", err)
	}
	recorder := httptest.NewRecorder()
	newRouter(client).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/ipfs/bafy-missing", nil))

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNotFound)
	}
	if !strings.Contains(recorder.Body.String(), "content unavailable") {
		t.Errorf("body = %q, want content unavailable", recorder.Body.String())
	}
}

func TestDownloadHeadChecksLocalContent(t *testing.T) {
	upstream := newLocalContentServer(t, "data", http.StatusOK)
	defer upstream.close()

	client, err := newIPFSClient(upstream.server.URL)
	if err != nil {
		t.Fatalf("newIPFSClient() error = %v", err)
	}
	recorder := httptest.NewRecorder()
	newRouter(client).ServeHTTP(recorder, httptest.NewRequest(http.MethodHead, "/ipfs/bafy-head", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if recorder.Body.Len() != 0 {
		t.Errorf("body length = %d, want 0", recorder.Body.Len())
	}
	if upstream.catCalls != 1 || upstream.lastOffline != "true" {
		t.Errorf("cat calls = %d, offline = %q; want one local cat request", upstream.catCalls, upstream.lastOffline)
	}
}

func TestDownloadRejectsInvalidPath(t *testing.T) {
	upstream := newLocalContentServer(t, "unused", http.StatusOK)
	defer upstream.close()

	client, err := newIPFSClient(upstream.server.URL)
	if err != nil {
		t.Fatalf("newIPFSClient() error = %v", err)
	}
	recorder := httptest.NewRecorder()
	newRouter(client).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/ipfs/bafy-one/bafy-two", nil))

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNotFound)
	}
	if upstream.catCalls != 0 {
		t.Errorf("cat calls = %d, want 0 for an invalid path", upstream.catCalls)
	}
}
