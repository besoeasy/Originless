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

	content := []byte("hello-blob-world")
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

	postBin(t, h, "test.bin", []byte("blobby-data"))

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
