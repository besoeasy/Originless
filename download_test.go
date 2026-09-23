package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type downloadTestServer struct {
	server      *httptest.Server
	addCalls    int
	catCalls    int
	lastCatCID  string
	lastOffline string
	addResponse string
	catContent  string
}

func newDownloadTestServer(t *testing.T, addResponse, catContent string) *downloadTestServer {
	t.Helper()
	testServer := &downloadTestServer{
		addResponse: addResponse,
		catContent:  catContent,
	}
	testServer.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v0/add":
			testServer.addCalls++
			_, _ = io.Copy(io.Discard, r.Body)
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, testServer.addResponse)
		case "/api/v0/cat":
			testServer.catCalls++
			testServer.lastCatCID = r.URL.Query().Get("arg")
			testServer.lastOffline = r.URL.Query().Get("offline")
			_, _ = io.WriteString(w, testServer.catContent)
		default:
			http.NotFound(w, r)
		}
	}))
	return testServer
}

func (s *downloadTestServer) close() {
	s.server.Close()
}

func TestDownloadAllowedUpload(t *testing.T) {
	upstream := newDownloadTestServer(t,
		"{\"Name\":\"payload.bin\",\"Hash\":\"bafy-bin\",\"Size\":\"6\"}\n",
		"binary",
	)
	defer upstream.close()

	client, err := newIPFSClient(upstream.server.URL)
	if err != nil {
		t.Fatalf("newIPFSClient() error = %v", err)
	}
	router := newRouter(client)

	uploadRecorder := httptest.NewRecorder()
	router.ServeHTTP(uploadRecorder, newUploadRequest(t, "/up", testUploadPart{
		fieldName: "file",
		fileName:  "payload.bin",
		content:   "binary",
	}))
	upload := decodeUploadResponse(t, uploadRecorder)

	downloadRecorder := httptest.NewRecorder()
	router.ServeHTTP(downloadRecorder, httptest.NewRequest(http.MethodGet, "/down/"+upload.CID, nil))

	if downloadRecorder.Code != http.StatusOK {
		t.Fatalf("download status = %d, want %d; body = %s", downloadRecorder.Code, http.StatusOK, downloadRecorder.Body.String())
	}
	if downloadRecorder.Body.String() != "binary" {
		t.Errorf("download body = %q, want binary", downloadRecorder.Body.String())
	}
	if got := downloadRecorder.Header().Get("Content-Type"); got != "application/octet-stream" {
		t.Errorf("content type = %q, want application/octet-stream", got)
	}
	if got := downloadRecorder.Header().Get("Content-Length"); got != "6" {
		t.Errorf("content length = %q, want 6", got)
	}
	if got := downloadRecorder.Header().Get("Content-Disposition"); !strings.Contains(got, "attachment") || !strings.Contains(got, "payload.bin") {
		t.Errorf("content disposition = %q, want attachment filename", got)
	}
	if upstream.catCalls != 1 || upstream.lastCatCID != upload.CID || upstream.lastOffline != "true" {
		t.Errorf("cat calls = %d, CID = %q, offline = %q; want one local cat request", upstream.catCalls, upstream.lastCatCID, upstream.lastOffline)
	}
}

func TestDownloadRejectsDisallowedUpload(t *testing.T) {
	upstream := newDownloadTestServer(t,
		"{\"Name\":\"photo.jpg\",\"Hash\":\"bafy-jpg\",\"Size\":\"6\"}\n",
		"photo",
	)
	defer upstream.close()

	client, err := newIPFSClient(upstream.server.URL)
	if err != nil {
		t.Fatalf("newIPFSClient() error = %v", err)
	}
	router := newRouter(client)

	uploadRecorder := httptest.NewRecorder()
	router.ServeHTTP(uploadRecorder, newUploadRequest(t, "/up", testUploadPart{
		fieldName: "file",
		fileName:  "photo.jpg",
		content:   "photo",
	}))
	upload := decodeUploadResponse(t, uploadRecorder)

	downloadRecorder := httptest.NewRecorder()
	router.ServeHTTP(downloadRecorder, httptest.NewRequest(http.MethodGet, "/down/"+upload.CID, nil))

	if downloadRecorder.Code != http.StatusNotFound {
		t.Fatalf("download status = %d, want %d", downloadRecorder.Code, http.StatusNotFound)
	}
	if upstream.catCalls != 0 {
		t.Errorf("cat calls = %d, want 0 for a disallowed extension", upstream.catCalls)
	}
}

func TestDownloadUnknownCID(t *testing.T) {
	upstream := newDownloadTestServer(t, "", "unused")
	defer upstream.close()

	client, err := newIPFSClient(upstream.server.URL)
	if err != nil {
		t.Fatalf("newIPFSClient() error = %v", err)
	}
	recorder := httptest.NewRecorder()
	newRouter(client).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/down/bafy-unknown", nil))

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNotFound)
	}
	if upstream.catCalls != 0 {
		t.Errorf("cat calls = %d, want 0 for an unknown CID", upstream.catCalls)
	}
}

func TestDownloadHeadDoesNotFetchContent(t *testing.T) {
	upstream := newDownloadTestServer(t,
		"{\"Name\":\"data.json\",\"Hash\":\"bafy-json\",\"Size\":\"4\"}\n",
		"data",
	)
	defer upstream.close()

	client, err := newIPFSClient(upstream.server.URL)
	if err != nil {
		t.Fatalf("newIPFSClient() error = %v", err)
	}
	router := newRouter(client)
	uploadRecorder := httptest.NewRecorder()
	router.ServeHTTP(uploadRecorder, newUploadRequest(t, "/up", testUploadPart{
		fieldName: "file",
		fileName:  "data.json",
		content:   "data",
	}))
	upload := decodeUploadResponse(t, uploadRecorder)

	headRecorder := httptest.NewRecorder()
	router.ServeHTTP(headRecorder, httptest.NewRequest(http.MethodHead, "/down/"+upload.CID, nil))
	if headRecorder.Code != http.StatusOK {
		t.Fatalf("HEAD status = %d, want %d", headRecorder.Code, http.StatusOK)
	}
	if headRecorder.Body.Len() != 0 {
		t.Errorf("HEAD body length = %d, want 0", headRecorder.Body.Len())
	}
	if upstream.catCalls != 0 {
		t.Errorf("cat calls = %d, want 0 for HEAD", upstream.catCalls)
	}
}
