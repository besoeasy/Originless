package modules

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

func fileLikeHandler(body []byte) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.Header().Set("Accept-Ranges", "bytes")
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		_, _ = w.Write(body)
	})
}

func TestGzipSkipsHEAD(t *testing.T) {
	body := bytes.Repeat([]byte("n"), 2048)
	h := Gzip(fileLikeHandler(body))

	req := httptest.NewRequest(http.MethodHead, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Header().Get("Content-Encoding") == "gzip" {
		t.Fatal("HEAD must not be gzip-wrapped")
	}
	if rec.Header().Get("Content-Length") != strconv.Itoa(len(body)) {
		t.Fatalf("HEAD Content-Length = %q, want uncompressed size", rec.Header().Get("Content-Length"))
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("HEAD must not write a body, got %d bytes", rec.Body.Len())
	}
}

func TestGzipDropsHandlerContentLength(t *testing.T) {
	body := bytes.Repeat([]byte("n"), 2048)
	h := Gzip(fileLikeHandler(body))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("Content-Encoding = %q", rec.Header().Get("Content-Encoding"))
	}
	if rec.Header().Get("Content-Length") == strconv.Itoa(len(body)) {
		t.Fatal("gzip response advertised the uncompressed Content-Length")
	}
	if rec.Header().Get("Accept-Ranges") != "" {
		t.Fatalf("gzip response should not advertise Accept-Ranges, got %q", rec.Header().Get("Accept-Ranges"))
	}

	zr, err := gzip.NewReader(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	got, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("gzip read: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("decompressed length %d, want %d", len(got), len(body))
	}
}

