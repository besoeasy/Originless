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

// publishEventWithBlob signs an event that claims data.blob = sha256(content)
// and issues one combined multipart POST /events carrying both parts.
// Overrides data map with the blob hash; returns the event id and response.
// mangle wraps text with non-UTF8 leading bytes so http sniffing reports
// application/octet-stream instead of text/plain.
func mangle(s string) []byte {
	return append([]byte{0xff, 0x00, 0x01}, []byte(s)...)
}

func publishEventWithBlob(t *testing.T, h *Handler, content []byte, data map[string]any) (string, *httptest.ResponseRecorder) {
	t.Helper()
	sum := sha256.Sum256(content)
	blob := hex.EncodeToString(sum[:])
	if data == nil {
		data = map[string]any{}
	}

	_, priv, owner := testKeys(t)
	now := time.Now().Unix()
	payload := signRecord(t, priv, owner, "blobs", now-5, now+3600, data, []string{}, blob)
	id := payload["_id"].(string)
	delete(payload, "_id")
	ev, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}

	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	evPart, err := w.CreateFormField("event")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := evPart.Write(ev); err != nil {
		t.Fatal(err)
	}
	dataPart, err := w.CreateFormFile("blob", "blob.bin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dataPart.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/events", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	h.PublishRecord(rec, req)
	return id, rec
}

func TestCombinedPublishDownRoundTrip(t *testing.T) {
	st := testStore(t)
	withBlobDir(t, t.TempDir())
	h := NewHandler(nil, NewMetrics())
	h.SetStore(st)

	content := []byte("hello-blob-world\x00\xff\x89\x01")
	id, rec := publishEventWithBlob(t, h, content, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /events status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["id"] != id {
		t.Fatalf("id=%v want %v", resp["id"], id)
	}
	sum := sha256.Sum256(content)
	wantHash := hex.EncodeToString(sum[:])
	// File on disk at <hash>.bin, account row present, events+blob linked.
	if _, err := os.Stat(filepath.Join(BlobDir, wantHash+".bin")); err != nil {
		t.Fatalf("blob file missing: %v", err)
	}
	if _, err := st.GetBlob(wantHash); err != nil {
		t.Fatalf("blob row missing: %v", err)
	}
	refs, err := st.GetReferencedBlobHashes(time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	if !refs[wantHash] {
		t.Fatal("published blob should be referenced by the live event")
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

	// Same bytes, new event (different created_at): dedupe to one file.
	_, rec2 := publishEventWithBlob(t, h, content, map[string]any{"note": "again"})
	if rec2.Code != http.StatusCreated {
		t.Fatalf("dedupe publish status=%d body=%s", rec2.Code, rec2.Body.String())
	}
	info, err := os.Stat(filepath.Join(BlobDir, wantHash+".bin"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != int64(len(content)) {
		t.Fatalf("dedupe must not duplicate bytes: size=%d", info.Size())
	}
	if n, _ := st.GetBlobCount(); n != 1 {
		t.Fatalf("blob count=%d want 1 (content-addressed dedupe)", n)
	}
	if n, _ := st.GetRecordCount(); n != 2 {
		t.Fatalf("record count=%d want 2 (two distinct events sharing one blob)", n)
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

func TestCombinedRejectsSniffedNonBinary(t *testing.T) {
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
		{"text", []byte("just some plain pasted text for the blob store"), "text/plain"},
	}
	for _, tc := range cases {
		_, rec := publishEventWithBlob(t, h, tc.content, nil)
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
		_, rec := publishEventWithBlob(t, h, content, nil)
		if rec.Code != http.StatusCreated {
			t.Fatalf("binary should pass, got %d body=%s", rec.Code, rec.Body.String())
		}
	}

	// Empty data still hits the empty-file 400, not the sniff 415.
	_, empty := publishEventWithBlob(t, h, []byte{}, nil)
	if empty.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty, got %d body=%s", empty.Code, empty.Body.String())
	}
}

func TestCombinedBlobHashMismatch(t *testing.T) {
	st := testStore(t)
	withBlobDir(t, t.TempDir())
	h := NewHandler(nil, NewMetrics())
	h.SetStore(st)

	// Event claims a valid-format hash that does NOT match the bytes.
	sentinel := strings.Repeat("ab", 32) // 64 hex chars, not the content hash
	_, priv, owner := testKeys(t)
	now := time.Now().Unix()
	payload := signRecord(t, priv, owner, "blobs", now-5, now+3600,
		map[string]any{"note": "wrong hash"}, []string{}, sentinel)
	delete(payload, "_id")
	ev, _ := json.Marshal(payload)

	content := mangle("real bytes with a different digest")
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	p1, _ := w.CreateFormField("event")
	p1.Write(ev)
	p2, _ := w.CreateFormFile("blob", "blob.bin")
	p2.Write(content)
	w.Close()

	req := httptest.NewRequest(http.MethodPost, "/events", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	h.PublishRecord(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on hash mismatch, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["error"] != "blob hash mismatch" {
		t.Fatalf("error=%v", resp["error"])
	}
	// Nothing must be stored: blob row, file, and temp all absent.
	realSum := sha256.Sum256(content)
	realHash := hex.EncodeToString(realSum[:])
	if _, err := os.Stat(filepath.Join(BlobDir, realHash+".bin")); !os.IsNotExist(err) {
		t.Fatal("mismatched bytes must not be committed")
	}
	if _, err := st.GetBlob(realHash); err == nil {
		t.Fatal("mismatched blob row must not exist")
	}
	entries, _ := os.ReadDir(BlobDir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "up-") {
			t.Fatalf("staged temp left behind: %s", e.Name())
		}
	}
}

func TestCombinedRequiresBlobWhenBlobPresent(t *testing.T) {
	st := testStore(t)
	withBlobDir(t, t.TempDir())
	h := NewHandler(nil, NewMetrics())
	h.SetStore(st)

	// Event data has no blob key but a blob part is attached.
	_, priv, owner := testKeys(t)
	now := time.Now().Unix()
	payload := signRecord(t, priv, owner, "blobs", now-5, now+3600,
		map[string]any{"text": "no binary claim"}, []string{})
	delete(payload, "_id")
	ev, _ := json.Marshal(payload)

	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	p1, _ := w.CreateFormField("event")
	p1.Write(ev)
	p2, _ := w.CreateFormFile("blob", "blob.bin")
	p2.Write(mangle("unsignable bytes"))
	w.Close()

	req := httptest.NewRequest(http.MethodPost, "/events", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	h.PublishRecord(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["error"] != "blob part requires data.blob in the event" {
		t.Fatalf("error=%v", resp["error"])
	}
}

func TestCombinedRejectsMissingEventPart(t *testing.T) {
	st := testStore(t)
	withBlobDir(t, t.TempDir())
	h := NewHandler(nil, NewMetrics())
	h.SetStore(st)

	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	p2, _ := w.CreateFormFile("blob", "blob.bin")
	p2.Write(mangle("bytes but no event"))
	w.Close()

	req := httptest.NewRequest(http.MethodPost, "/events", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	h.PublishRecord(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestDownSendsNosniff(t *testing.T) {
	st := testStore(t)
	withBlobDir(t, t.TempDir())
	h := NewHandler(nil, NewMetrics())
	h.SetStore(st)

	content := []byte("nosniff-probe\x00\xff")
	_, up := publishEventWithBlob(t, h, content, nil)
	if up.Code != http.StatusCreated {
		t.Fatalf("publish status=%d body=%s", up.Code, up.Body.String())
	}
	sum := sha256.Sum256(content)
	hash := hex.EncodeToString(sum[:])

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

func TestBlobOrphanEviction(t *testing.T) {
	st := testStore(t)
	dir := t.TempDir()
	withBlobDir(t, dir)

	// orphanHash: backdated past grace, unreferenced -> evicted.
	// refHash: backdated past grace but pinned by a live event -> survives.
	// freshHash: just uploaded, unreferenced -> survives (grace not elapsed).
	orphanHash := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	refHash := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	freshHash := "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	for _, hx := range []string{orphanHash, refHash, freshHash} {
		if err := os.WriteFile(BlobPath(dir, hx), []byte(hx[:9]), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := st.UpsertBlob(hx, 9); err != nil {
			t.Fatal(err)
		}
	}
	// Backdate orphan + referenced past the 7-day orphan grace.
	backdateBlob(t, st, orphanHash, 8*24*time.Hour)
	backdateBlob(t, st, refHash, 8*24*time.Hour)

	// Live event pins the referenced blob.
	_, priv, owner := testKeys(t)
	now := time.Now().Unix()
	payload := signRecord(t, priv, owner, "saves", now-5, now+3600,
		map[string]any{"slot": 1}, []string{}, refHash)
	delete(payload, "_id")
	raw, _ := json.Marshal(payload)
	rec, err := ValidateRecordBody(raw, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.InsertRecord(rec); err != nil {
		t.Fatal(err)
	}

	mgr := NewJanitor(st)
	if err := mgr.EvictBlobs(); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetBlob(orphanHash); err == nil {
		t.Fatal("expired orphan blob should have been evicted")
	}
	if _, err := os.Stat(BlobPath(dir, orphanHash)); !os.IsNotExist(err) {
		t.Fatal("expired orphan blob file should be gone")
	}
	if _, err := st.GetBlob(refHash); err != nil {
		t.Fatalf("referenced blob must survive orphan grace: %v", err)
	}
	if _, err := os.Stat(BlobPath(dir, refHash)); err != nil {
		t.Fatalf("referenced blob file must survive: %v", err)
	}
	if _, err := st.GetBlob(freshHash); err != nil {
		t.Fatalf("fresh blob inside orphan grace must survive: %v", err)
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
	// non-temp, non-blob file that must be untouched. Reconcile imports
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

	mgr := NewJanitor(st)
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
		t.Fatalf("non-temp non-blob file must survive reconcile: %v", err)
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

	publishEventWithBlob(t, h, []byte("blobby-data\x00\xff"), nil)

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
func TestEventsIdBlobSubresource(t *testing.T) {
	st := testStore(t)
	withBlobDir(t, t.TempDir())
	h := NewHandler(nil, NewMetrics())
	h.SetStore(st)

	content := mangle("subresource bytes")
	id, rec := publishEventWithBlob(t, h, content, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("publish status=%d body=%s", rec.Code, rec.Body.String())
	}

	// Direct byte serve in one hop.
	req := httptest.NewRequest(http.MethodGet, "/events/"+id+"/blob", nil)
	req.SetPathValue("id", id)
	got := httptest.NewRecorder()
	h.GetRecordBlob(got, req)
	if got.Code != http.StatusOK {
		t.Fatalf("subresource status=%d body=%s", got.Code, got.Body.String())
	}
	if !bytes.Equal(got.Body.Bytes(), content) {
		t.Fatal("subresource content mismatch")
	}
	if ct := got.Header().Get("Content-Type"); ct != "application/octet-stream" {
		t.Fatalf("content-type=%q", ct)
	}

	// Unknown event -> 404.
	req404 := httptest.NewRequest(http.MethodGet, "/events/deadbeef/blob", nil)
	req404.SetPathValue("id", "deadbeef")
	r404 := httptest.NewRecorder()
	h.GetRecordBlob(r404, req404)
	if r404.Code != http.StatusNotFound {
		t.Fatalf("unknown id status=%d", r404.Code)
	}

	// Blob-less event -> 404 (event exists, nothing attached).
	_, priv, owner := testKeys(t)
	now := time.Now().Unix()
	plain := signRecord(t, priv, owner, "notes", now-5, now+3600,
		map[string]any{"text": "no attachment"}, []string{})
	plainID := plain["_id"].(string)
	delete(plain, "_id")
	raw, _ := json.Marshal(plain)
	prec, err := ValidateRecordBody(raw, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.InsertRecord(prec); err != nil {
		t.Fatal(err)
	}
	reqPlain := httptest.NewRequest(http.MethodGet, "/events/"+plainID+"/blob", nil)
	reqPlain.SetPathValue("id", plainID)
	rPlain := httptest.NewRecorder()
	h.GetRecordBlob(rPlain, reqPlain)
	if rPlain.Code != http.StatusNotFound {
		t.Fatalf("blob-less status=%d body=%s", rPlain.Code, rPlain.Body.String())
	}
}

func TestMultipartRequiresEventFirst(t *testing.T) {
	st := testStore(t)
	withBlobDir(t, t.TempDir())
	h := NewHandler(nil, NewMetrics())
	h.SetStore(st)

	content := mangle("ordered parts")
	_, priv, owner := testKeys(t)
	now := time.Now().Unix()
	sum := sha256.Sum256(content)
	payload := signRecord(t, priv, owner, "blobs", now-5, now+3600,
		map[string]any{"note": "order"}, []string{}, hex.EncodeToString(sum[:]))
	delete(payload, "_id")
	ev, _ := json.Marshal(payload)

	// Blob part BEFORE the event part must be refused.
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	p2, _ := w.CreateFormFile("blob", "blob.bin")
	p2.Write(content)
	p1, _ := w.CreateFormField("event")
	p1.Write(ev)
	w.Close()

	req := httptest.NewRequest(http.MethodPost, "/events", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	h.PublishRecord(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["error"] != "event part must come first" {
		t.Fatalf("error=%v", resp["error"])
	}
	if n, _ := st.GetBlobCount(); n != 0 {
		t.Fatalf("blob count=%d want 0", n)
	}
}

func TestMultipartBadSigStagesNothing(t *testing.T) {
	st := testStore(t)
	dir := t.TempDir()
	withBlobDir(t, dir)
	h := NewHandler(nil, NewMetrics())
	h.SetStore(st)

	// Valid envelope, then corrupt the signature: fail-fast must answer
	// 401 without committing bytes, rows, or temps.
	content := mangle("forged bytes here")
	sum := sha256.Sum256(content)
	_, priv, owner := testKeys(t)
	now := time.Now().Unix()
	payload := signRecord(t, priv, owner, "blobs", now-5, now+3600,
		map[string]any{"note": "x"}, []string{}, hex.EncodeToString(sum[:]))
	payload["sig"] = strings.Repeat("0", 128)
	delete(payload, "_id")
	ev, _ := json.Marshal(payload)

	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	p1, _ := w.CreateFormField("event")
	p1.Write(ev)
	p2, _ := w.CreateFormFile("blob", "blob.bin")
	p2.Write(content)
	w.Close()

	req := httptest.NewRequest(http.MethodPost, "/events", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	h.PublishRecord(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", rec.Code, rec.Body.String())
	}
	if n, _ := st.GetBlobCount(); n != 0 {
		t.Fatalf("blob count=%d want 0", n)
	}
	if n, _ := st.GetRecordCount(); n != 0 {
		t.Fatalf("record count=%d want 0", n)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		t.Fatalf("dir must be empty after forged publish, found %s", e.Name())
	}
}

func TestListRecordsBlobFilter(t *testing.T) {
	st := testStore(t)
	withBlobDir(t, t.TempDir())
	h := NewHandler(nil, NewMetrics())
	h.SetStore(st)

	content := mangle("filter me")
	sum := sha256.Sum256(content)
	wantHash := hex.EncodeToString(sum[:])
	linkedID, rec := publishEventWithBlob(t, h, content, map[string]any{"note": "linked"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("publish status=%d body=%s", rec.Code, rec.Body.String())
	}

	// A second, blob-less event that must NOT match.
	_, priv, owner := testKeys(t)
	now := time.Now().Unix()
	plain := signRecord(t, priv, owner, "notes", now-5, now+3600,
		map[string]any{"text": "plain"}, []string{})
	delete(plain, "_id")
	raw, _ := json.Marshal(plain)
	prec, err := ValidateRecordBody(raw, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.InsertRecord(prec); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/events?blob="+wantHash, nil)
	got := httptest.NewRecorder()
	h.ListRecords(got, req)
	if got.Code != http.StatusOK {
		t.Fatalf("filter status=%d body=%s", got.Code, got.Body.String())
	}
	var resp struct {
		Events []Record `json:"events"`
	}
	if err := json.Unmarshal(got.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Events) != 1 || resp.Events[0].ID != linkedID {
		t.Fatalf("filter returned %+v want [%s]", resp.Events, linkedID)
	}
	if resp.Events[0].Blob != wantHash {
		t.Fatalf("event blob=%q want %q", resp.Events[0].Blob, wantHash)
	}

	// Malformed hash -> 400.
	bad := httptest.NewRequest(http.MethodGet, "/events?blob=zzz", nil)
	badRec := httptest.NewRecorder()
	h.ListRecords(badRec, bad)
	if badRec.Code != http.StatusBadRequest {
		t.Fatalf("bad filter status=%d", badRec.Code)
	}
}
