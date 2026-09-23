package modules

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeIPFSTestFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestIPFSClientAddFile(t *testing.T) {
	dir := t.TempDir()
	localPath := writeIPFSTestFile(t, dir, "upload.tmp", "single-file-content")

	const wantCID = "bafybeigdyrztw5k7abcdefghijklmnopqrstuvwxyz234567abcdefgh"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if got := r.URL.Query().Get("pin"); got != "true" {
			t.Errorf("pin = %q, want true", got)
		}
		if got := r.URL.Query().Get("cid-version"); got != "1" {
			t.Errorf("cid-version = %q, want 1", got)
		}
		if _, ok := r.URL.Query()["wrap-with-directory"]; ok {
			t.Error("single-file add unexpectedly wraps a directory")
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("ParseMultipartForm: %v", err)
			return
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			t.Errorf("FormFile: %v", err)
			return
		}
		defer file.Close()
		if header.Filename != "photo.png" {
			t.Errorf("filename = %q, want photo.png", header.Filename)
		}
		buf, err := io.ReadAll(file)
		if err != nil {
			t.Errorf("read upload: %v", err)
		}
		if string(buf) != "single-file-content" {
			t.Errorf("content = %q", buf)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, "{\"Name\":\"photo.png\",\"Hash\":\"%s\",\"Size\":\"19\"}\n", wantCID)
	}))
	defer server.Close()

	client := newIPFSClient(server.URL, server.Client())
	got, err := client.AddFile(context.Background(), localPath, "photo.png")
	if err != nil {
		t.Fatal(err)
	}
	if got.CID != wantCID || got.Name != "photo.png" || got.Size != 19 {
		t.Fatalf("AddFile result = %+v", got)
	}
}

func TestIPFSClientAddFolderReturnsWrappedRoot(t *testing.T) {
	dir := t.TempDir()
	indexPath := writeIPFSTestFile(t, dir, "index.tmp", "index")
	assetPath := writeIPFSTestFile(t, dir, "asset.tmp", "asset")
	files := []IPFSFile{
		{Path: assetPath, Name: "assets/app.js"},
		{Path: indexPath, Name: "index.html"},
	}

	const rootCID = "bafybeifolderrootcidabcdefghijklmnopqrstuvwxyz234567"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("pin"); got != "true" {
			t.Errorf("pin = %q, want true", got)
		}
		if got := r.URL.Query().Get("wrap-with-directory"); got != "true" {
			t.Errorf("wrap-with-directory = %q, want true", got)
		}
		if got := r.URL.Query().Get("recursive"); got != "true" {
			t.Errorf("recursive = %q, want true", got)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read multipart body: %v", err)
			return
		}
		raw := string(body)
		// Go's high-level multipart helpers strip directories from filename;
		// inspect the wire body because Kubo's boxo/files parser intentionally
		// reads the full Content-Disposition filename to preserve the tree.
		for _, want := range []string{`filename="assets/app.js"`, `filename="index.html"`} {
			if !strings.Contains(raw, want) {
				t.Errorf("multipart body missing %q", want)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"Name":"assets/app.js","Hash":"bafyasset","Size":"5"}`)
		fmt.Fprintln(w, `{"Name":"index.html","Hash":"bafyindex","Size":"5"}`)
		fmt.Fprintf(w, "{\"Name\":\"\",\"Hash\":\"%s\",\"Size\":\"10\"}\n", rootCID)
	}))
	defer server.Close()

	client := newIPFSClient(server.URL, server.Client())
	got, err := client.AddFolder(context.Background(), files)
	if err != nil {
		t.Fatal(err)
	}
	if got.CID != rootCID {
		t.Fatalf("root CID = %q, want %q", got.CID, rootCID)
	}
}

func TestIPFSClientAddReportsKuboError(t *testing.T) {
	dir := t.TempDir()
	localPath := writeIPFSTestFile(t, dir, "upload.tmp", "content")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "repo full", http.StatusInsufficientStorage)
	}))
	defer server.Close()

	client := newIPFSClient(server.URL, server.Client())
	if _, err := client.AddFile(context.Background(), localPath, "file.bin"); err == nil {
		t.Fatal("expected Kubo error")
	}
}
