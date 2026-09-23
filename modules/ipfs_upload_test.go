package modules

import (
	"bytes"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

type ipfsMultipartTestFile struct {
	name    string
	content string
}

func newIPFSMultipartRequest(t *testing.T, files []ipfsMultipartTestFile) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for _, file := range files {
		part, err := writer.CreateFormFile("file", file.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(part, bytes.NewBufferString(file.content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/ipfs/upload", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req
}

func stagedFileContent(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func assertTempDirEmpty(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("temporary directory not clean: %v", entries)
	}
}

func TestStageSingleIPFSFile(t *testing.T) {
	tempDir := t.TempDir()
	req := newIPFSMultipartRequest(t, []ipfsMultipartTestFile{{name: "fakepath/photo.png", content: "image-bytes"}})

	file, err := stageSingleIPFSFile(req, tempDir, 1024, nil)
	if err != nil {
		t.Fatal(err)
	}
	if file.Name != "photo.png" || file.Size != int64(len("image-bytes")) {
		t.Fatalf("staged file = %+v", file)
	}
	if got := stagedFileContent(t, file.Path); got != "image-bytes" {
		t.Fatalf("staged content = %q", got)
	}
	removeStagedIPFSFiles([]StagedIPFSFile{*file})
	assertTempDirEmpty(t, tempDir)
}

func TestStageIPFSFolderPreservesRelativePaths(t *testing.T) {
	tempDir := t.TempDir()
	req := newIPFSMultipartRequest(t, []ipfsMultipartTestFile{
		{name: "index.html", content: "index"},
		{name: "assets/app.js", content: "app"},
		{name: `assets\styles\app.css`, content: "css"},
	})

	folder, err := stageIPFSFolder(req, tempDir, 1024, nil)
	if err != nil {
		t.Fatal(err)
	}
	if folder.Count != 3 || folder.Total != int64(len("index")+len("app")+len("css")) {
		t.Fatalf("folder summary = %+v", folder)
	}
	got := make(map[string]string, folder.Count)
	for _, file := range folder.Files {
		got[file.Name] = stagedFileContent(t, file.Path)
	}
	want := map[string]string{
		"index.html":            "index",
		"assets/app.js":         "app",
		"assets/styles/app.css": "css",
	}
	for name, content := range want {
		if got[name] != content {
			t.Errorf("path %q content = %q, want %q", name, got[name], content)
		}
	}
	removeStagedIPFSFiles(folder.Files)
	assertTempDirEmpty(t, tempDir)
}

func TestStageIPFSFolderRejectsTraversalAndCleansFiles(t *testing.T) {
	tempDir := t.TempDir()
	req := newIPFSMultipartRequest(t, []ipfsMultipartTestFile{
		{name: "safe.txt", content: "safe"},
		{name: "../escape.txt", content: "escape"},
	})

	_, err := stageIPFSFolder(req, tempDir, 1024, nil)
	if !errors.Is(err, ErrIPFSInvalidPath) {
		t.Fatalf("error = %v, want ErrIPFSInvalidPath", err)
	}
	assertTempDirEmpty(t, tempDir)
}

func TestStageIPFSFolderEnforcesAggregateLimit(t *testing.T) {
	tempDir := t.TempDir()
	req := newIPFSMultipartRequest(t, []ipfsMultipartTestFile{
		{name: "one.txt", content: "123"},
		{name: "two.txt", content: "456"},
	})

	_, err := stageIPFSFolder(req, tempDir, 5, nil)
	if !errors.Is(err, ErrIPFSFileTooLarge) {
		t.Fatalf("error = %v, want ErrIPFSFileTooLarge", err)
	}
	assertTempDirEmpty(t, tempDir)
}

func TestStageIPFSFolderAllowsEmptyFileAtExactLimit(t *testing.T) {
	tempDir := t.TempDir()
	req := newIPFSMultipartRequest(t, []ipfsMultipartTestFile{
		{name: "full.txt", content: "12345"},
		{name: "empty.txt", content: ""},
	})

	folder, err := stageIPFSFolder(req, tempDir, 5, nil)
	if err != nil {
		t.Fatal(err)
	}
	if folder.Count != 2 || folder.Total != 5 {
		t.Fatalf("folder summary = %+v", folder)
	}
	removeStagedIPFSFiles(folder.Files)
	assertTempDirEmpty(t, tempDir)
}

func TestSanitizeIPFSUploadPath(t *testing.T) {
	tests := []struct {
		name   string
		raw    string
		folder bool
		want   string
		bad    bool
	}{
		{name: "single strips path", raw: "C:/fakepath/file.bin", want: "file.bin"},
		{name: "folder keeps nested path", raw: "dist/assets/app.js", folder: true, want: "dist/assets/app.js"},
		{name: "folder normalizes separators", raw: `dist\assets\app.js`, folder: true, want: "dist/assets/app.js"},
		{name: "folder rejects absolute", raw: "/etc/passwd", folder: true, bad: true},
		{name: "folder rejects traversal", raw: "dist/../../secret", folder: true, bad: true},
		{name: "folder rejects Windows volume", raw: `C:\secret`, folder: true, bad: true},
		{name: "rejects control character", raw: "bad\nname", folder: true, bad: true},
		{name: "rejects long path", raw: string(bytes.Repeat([]byte{'a'}, maxIPFSUploadPathBytes+1)), folder: true, bad: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := sanitizeIPFSUploadPath(tt.raw, tt.folder)
			if tt.bad {
				if !errors.Is(err, ErrIPFSInvalidPath) {
					t.Fatalf("error = %v, want ErrIPFSInvalidPath", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("path = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStageIPFSFolderRejectsDuplicatePaths(t *testing.T) {
	tempDir := t.TempDir()
	req := newIPFSMultipartRequest(t, []ipfsMultipartTestFile{
		{name: "same.txt", content: "one"},
		{name: "same.txt", content: "two"},
	})
	_, err := stageIPFSFolder(req, tempDir, 1024, nil)
	if !errors.Is(err, ErrIPFSInvalidPath) {
		t.Fatalf("error = %v, want duplicate-path error", err)
	}
	entries, readErr := os.ReadDir(tempDir)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("temporary directory not clean: %v", entries)
	}
}
