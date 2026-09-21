package modules

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func withBlobDir(t *testing.T, dir string) {
	t.Helper()
	prev := BlobDir
	BlobDir = dir
	t.Cleanup(func() { BlobDir = prev })
	if err := EnsureBlobDir(dir); err != nil {
		t.Fatal(err)
	}
}

func postBin(t *testing.T, h *Handler, filename string, content []byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/up", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	h.Up(rec, req)
	return rec
}

func TestUpDownRoundTrip(t *testing.T) {
	st := testStore(t)
	withBlobDir(t, t.TempDir())
	h := NewHandler(nil, NewMetrics())
	h.SetStore(st)

	content := []byte("hello-blob-world\x00\xff\x89\x01")
	rec := postBin(t, h, "data.bin", content)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /up status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)
	wantHash := hex.EncodeToString(sum[:])
	if resp["hash"] != wantHash {
		t.Fatalf("hash=%v want %v", resp["hash"], wantHash)
	}
	// File on disk at <hash>.bin
	if _, err := os.Stat(filepath.Join(BlobDir, wantHash+".bin")); err != nil {
		t.Fatalf("blob file missing: %v", err)
	}

	// GET /down/{hash}
	req := httptest.NewRequest(http.MethodGet, "/down/"+wantHash, nil)
	req.SetPathValue("hash", wantHash)
	got := httptest.NewRecorder()
	h.Down(got, req)
	if got.Code != http.StatusOK {
		t.Fatalf("GET /down status=%d body=%s", got.Code, got.Body.String())
	}
	if !bytes.Equal(got.Body.Bytes(), content) {
		t.Fatalf("content mismatch: got %q", got.Body.String())
	}
	if ct := got.Header().Get("Content-Type"); ct != "application/octet-stream" {
		t.Fatalf("content-type=%q", ct)
	}

	// Duplicate upload -> 200 duplicate=true
	dup := postBin(t, h, "other.bin", content)
	if dup.Code != http.StatusOK {
		t.Fatalf("dup status=%d body=%s", dup.Code, dup.Body.String())
	}
	var dupResp map[string]any
	_ = json.Unmarshal(dup.Body.Bytes(), &dupResp)
	if dupResp["duplicate"] != true || dupResp["hash"] != wantHash {
		t.Fatalf("dup resp=%v", dupResp)
	}

	// HEAD works
	headReq := httptest.NewRequest(http.MethodHead, "/down/"+wantHash, nil)
	headReq.SetPathValue("hash", wantHash)
	headRec := httptest.NewRecorder()
	h.Down(headRec, headReq)
	if headRec.Code != http.StatusOK {
		t.Fatalf("HEAD status=%d", headRec.Code)
	}
}

func TestUpRejectsNonBin(t *testing.T) {
	st := testStore(t)
	withBlobDir(t, t.TempDir())
	h := NewHandler(nil, NewMetrics())
	h.SetStore(st)

	rec := postBin(t, h, "photo.png", []byte("x"))
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("expected 415, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestUpRejectsSniffedNonBinary(t *testing.T) {
	st := testStore(t)
	withBlobDir(t, t.TempDir())
	h := NewHandler(nil, NewMetrics())
	h.SetStore(st)

	pngMagic := append([]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}, bytes.Repeat([]byte{0}, 32)...)
	cases := []struct {
		name     string
		content  []byte
		detected string
	}{
		{"html", []byte("<!DOCTYPE html><html><body>pwn</body></html>"), "text/html"},
		{"svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`), "text/plain"},
		{"png", pngMagic, "image/png"},
		{"pdf", []byte("%PDF-1.4 fake pdf body"), "application/pdf"},
		{"text", []byte("just some plain pasted text for the bin store"), "text/plain"},
	}
	for _, tc := range cases {
		rec := postBin(t, h, "payload.bin", tc.content)
		if rec.Code != http.StatusUnsupportedMediaType {
			t.Fatalf("%s: expected 415, got %d body=%s", tc.name, rec.Code, rec.Body.String())
		}
		var body map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if body["error"] != "non-binary content rejected" {
			t.Fatalf("%s: unexpected body %v", tc.name, body)
		}
		if det, _ := body["detected"].(string); !strings.HasPrefix(det, strings.Split(tc.detected, ";")[0]) {
			t.Fatalf("%s: detected=%q want prefix %q", tc.name, det, tc.detected)
		}
	}

	// Opaque bytes still pass: unknown binary, archives, media containers.
	for _, content := range [][]byte{
		{0x00, 0x01, 0x02, 0xFF, 0xFE, 0x80, 0x7F},
		append([]byte("PK\x03\x04"), bytes.Repeat([]byte{0xAA}, 64)...),
		append([]byte("\x1A\x45\xDF\xA3"), bytes.Repeat([]byte{0x10}, 64)...), // ebml
	} {
		rec := postBin(t, h, "opaque.bin", content)
		if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
			t.Fatalf("binary should pass, got %d body=%s", rec.Code, rec.Body.String())
		}
	}

	// Empty files still hit the empty-file 400, not the sniff 415.
	empty := postBin(t, h, "empty.bin", []byte{})
	if empty.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty, got %d body=%s", empty.Code, empty.Body.String())
	}
}

func TestDownSendsNosniff(t *testing.T) {
	st := testStore(t)
	withBlobDir(t, t.TempDir())
	h := NewHandler(nil, NewMetrics())
	h.SetStore(st)

	content := []byte("nosniff-probe\x00\xff")
	up := postBin(t, h, "probe.bin", content)
	if up.Code != http.StatusCreated {
		t.Fatalf("POST /up status=%d body=%s", up.Code, up.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(up.Body.Bytes(), &resp)
	hash, _ := resp["hash"].(string)

	req := httptest.NewRequest(http.MethodGet, "/down/"+hash, nil)
	req.SetPathValue("hash", hash)
	got := httptest.NewRecorder()
	h.Down(got, req)
	if got.Code != http.StatusOK {
		t.Fatalf("GET /down status=%d", got.Code)
	}
	if v := got.Header().Get("X-Content-Type-Options"); v != "nosniff" {
		t.Fatalf("X-Content-Type-Options=%q, want nosniff", v)
	}
}

func TestIsBlockedContentType(t *testing.T) {
	blocked := []string{
		"text/html; charset=utf-8",
		"text/plain; charset=utf-8",
		"text/xml; charset=utf-8",
		"image/png",
		"image/svg+xml",
		"application/pdf",
	}
	for _, c := range blocked {
		if !isBlockedContentType(c) {
			t.Fatalf("expected blocked: %q", c)
		}
	}
	allowed := []string{
		"application/octet-stream",
		"application/zip",
		"application/gzip",
		"video/mp4",
		"audio/mpeg",
		"application/x-executable",
		"",
	}
	for _, c := range allowed {
		if isBlockedContentType(c) {
			t.Fatalf("expected allowed: %q", c)
		}
	}
}

func TestDownBadHashAndMissing(t *testing.T) {
	st := testStore(t)
	withBlobDir(t, t.TempDir())
	h := NewHandler(nil, NewMetrics())
	h.SetStore(st)

	req := httptest.NewRequest(http.MethodGet, "/down/notahash", nil)
	req.SetPathValue("hash", "notahash")
	rec := httptest.NewRecorder()
	h.Down(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}

	missing := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	req2 := httptest.NewRequest(http.MethodGet, "/down/"+missing, nil)
	req2.SetPathValue("hash", missing)
	rec2 := httptest.NewRecorder()
	h.Down(rec2, req2)
	if rec2.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", rec2.Code, rec2.Body.String())
	}
}

func TestBlobLRURespectsRetention(t *testing.T) {
	st := testStore(t)
	dir := t.TempDir()
	withBlobDir(t, dir)

	// Big blob (max_size => 30-day retention, backdated 31d: expired) and
	// small blob (tiny => ~1-year retention, backdated 31d: protected),
	// plus a fresh blob (protected).
	oldHash := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	smallHash := "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	newHash := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	oldContent := []byte("old-bytes")
	smallContent := []byte("small-bytes")
	newContent := []byte("new-bytes")
	if err := os.WriteFile(BlobPath(dir, oldHash), oldContent, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(BlobPath(dir, smallHash), smallContent, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(BlobPath(dir, newHash), newContent, 0o644); err != nil {
		t.Fatal(err)
	}
	// DB sizes drive retention: max-size blob expires in 30d, tiny blob in ~1y.
	if _, err := st.UpsertBlob(oldHash, BlobMaxSizeBytes); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertBlob(smallHash, int64(len(smallContent))); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertBlob(newHash, int64(len(newContent))); err != nil {
		t.Fatal(err)
	}
	// Backdate the big + small rows (big 32d ago, small 31d ago) past 30d.
	oldTime := time.Now().Add(-32 * 24 * time.Hour).UTC().Format("2006-01-02 15:04:05")
	smallTime := time.Now().Add(-31 * 24 * time.Hour).UTC().Format("2006-01-02 15:04:05")
	if _, err := st.db.Exec(`UPDATE blobs SET created_at = ?, last_access = ? WHERE hash = ?`, oldTime, oldTime, oldHash); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`UPDATE blobs SET created_at = ?, last_access = ? WHERE hash = ?`, smallTime, smallTime, smallHash); err != nil {
		t.Fatal(err)
	}

	// Tiny quota forces eviction; only the retention-expired big blob is eligible.
	mgr := NewJanitor(st, 10)
	if err := mgr.EvictBlobsLRU(); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetBlob(oldHash); err == nil {
		t.Fatal("expired big blob should have been evicted")
	}
	if _, err := os.Stat(BlobPath(dir, oldHash)); !os.IsNotExist(err) {
		t.Fatal("expired big blob file should be gone")
	}
	if _, err := st.GetBlob(smallHash); err != nil {
		t.Fatalf("small blob inside ~1y retention must survive: %v", err)
	}
	if _, err := st.GetBlob(newHash); err != nil {
		t.Fatalf("fresh blob must survive retention: %v", err)
	}
}

func TestReconcileCleansStaleTemps(t *testing.T) {
	st := testStore(t)
	dir := t.TempDir()
	withBlobDir(t, dir)

	// Simulate a crash mid-upload: a staged up-* temp file left behind.
	stale := filepath.Join(dir, "up-1234567890")
	if err := os.WriteFile(stale, []byte("half-uploaded"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A legitimate blob (real content hash matches its filename) + a
	// non-temp, non-bin file that must be untouched. Reconcile imports
	// untracked .bin files whose content hashes to their name.
	goodSum := sha256.Sum256([]byte("real-blob-bytes-with-verified-hash"))
	goodHash := hex.EncodeToString(goodSum[:])
	good := filepath.Join(dir, goodHash+".bin")
	if err := os.WriteFile(good, []byte("real-blob-bytes-with-verified-hash"), 0o644); err != nil {
		t.Fatal(err)
	}
	elsewhere := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(elsewhere, []byte("hands off"), 0o644); err != nil {
		t.Fatal(err)
	}

	mgr := NewJanitor(st, StorageMaxBytes)
	if err := mgr.ReconcileBlobs(); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("stale up-* temp should have been swept")
	}
	if _, err := os.Stat(good); err != nil {
		t.Fatalf("legitimate blob must survive reconcile: %v", err)
	}
	if _, err := os.Stat(elsewhere); err != nil {
		t.Fatalf("non-temp non-bin file must survive reconcile: %v", err)
	}
}

func TestListBlobsAndCounts(t *testing.T) {
	st := testStore(t)
	withBlobDir(t, t.TempDir())
	h := NewHandler(nil, NewMetrics())
	h.SetStore(st)

	req := httptest.NewRequest(http.MethodGet, "/blobs", nil)
	rec := httptest.NewRecorder()
	h.ListBlobs(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["count"].(float64) != 0 {
		t.Fatalf("expected count=0, got %v", resp["count"])
	}

	postBin(t, h, "test.bin", []byte("blobby-data\x00\xff"))

	rec2 := httptest.NewRecorder()
	h.ListBlobs(rec2, req)
	if rec2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec2.Code)
	}
	var resp2 map[string]any
	if err := json.Unmarshal(rec2.Body.Bytes(), &resp2); err != nil {
		t.Fatal(err)
	}
	if resp2["count"].(float64) != 1 {
		t.Fatalf("expected count=1, got %v", resp2["count"])
	}
	blobs := resp2["blobs"].([]any)
	if len(blobs) != 1 {
		t.Fatalf("expected 1 blob, got %d", len(blobs))
	}
}
