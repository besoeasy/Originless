package modules

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

const (
	refBlobHash  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	loneBlobHash = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func TestRecordBlobHashFormat(t *testing.T) {
	// absent -> ""
	if h, err := RecordBlobHash(json.RawMessage(`{"slot":1}`)); err != nil || h != "" {
		t.Fatalf("absent: h=%q err=%v", h, err)
	}
	// explicit null -> absent
	if h, err := RecordBlobHash(json.RawMessage(`{"_blob":null}`)); err != nil || h != "" {
		t.Fatalf("null: h=%q err=%v", h, err)
	}
	// valid, uppercased -> normalized lowercase
	if h, err := RecordBlobHash(json.RawMessage(`{"_blob":"` + strings.ToUpper(refBlobHash) + `"}`)); err != nil || h != refBlobHash {
		t.Fatalf("valid: h=%q err=%v", h, err)
	}
	// malformed values -> error
	for _, data := range []string{
		`{"_blob":"xyz"}`,
		`{"_blob":"abc"}`,
		`{"_blob":123}`,
		`{"_blob":["` + refBlobHash + `"]}`,
	} {
		if _, err := RecordBlobHash(json.RawMessage(data)); err == nil {
			t.Fatalf("expected error for %s", data)
		}
	}
	// garbage JSON -> no linkage, no error (shape checked elsewhere)
	if h, err := RecordBlobHash(json.RawMessage(`not json`)); err != nil || h != "" {
		t.Fatalf("garbage: h=%q err=%v", h, err)
	}
}

func TestValidateRecordBodyRejectsBadBlobRef(t *testing.T) {
	_, priv, owner := testKeys(t)
	now := time.Now().Unix()
	created := now - 10
	payload := signRecord(t, priv, owner, "saves", created, created+3600,
		map[string]any{"slot": 1, "_blob": "not-a-hash"}, []string{})
	delete(payload, "_id")
	raw, _ := json.Marshal(payload)
	if _, err := ValidateRecordBody(raw, now); err == nil {
		t.Fatal("expected _blob format error")
	}
}

func postRecord(t *testing.T, h *Handler, payload map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	delete(payload, "_id")
	raw, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/records", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.PublishRecord(rec, req)
	return rec
}

func TestPublishRecordBlobRefExistence(t *testing.T) {
	st := testStore(t)
	h := NewHandler(nil, NewMetrics())
	h.SetStore(st)
	_, priv, owner := testKeys(t)
	now := time.Now().Unix()
	created := now - 5

	// Referenced blob stored -> 201.
	if _, err := st.UpsertBlob(refBlobHash, 1024); err != nil {
		t.Fatal(err)
	}
	ok := signRecord(t, priv, owner, "saves", created, created+3600,
		map[string]any{"slot": 1, "_blob": refBlobHash}, []string{})
	if rec := postRecord(t, h, ok); rec.Code != http.StatusCreated {
		t.Fatalf("linked publish status=%d body=%s", rec.Code, rec.Body.String())
	}

	// Unknown blob -> 400 with hash echoed.
	bad := signRecord(t, priv, owner, "saves", created, created+3600,
		map[string]any{"slot": 2, "_blob": loneBlobHash}, []string{})
	rec := postRecord(t, h, bad)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["error"] != "referenced blob not found" || body["hash"] != loneBlobHash {
		t.Fatalf("unexpected body %v", body)
	}
}

func TestGetRecordResolveBlob(t *testing.T) {
	st := testStore(t)
	h := NewHandler(nil, NewMetrics())
	h.SetStore(st)
	_, priv, owner := testKeys(t)
	now := time.Now().Unix()

	if _, err := st.UpsertBlob(refBlobHash, 2048); err != nil {
		t.Fatal(err)
	}
	payload := signRecord(t, priv, owner, "saves", now-5, now+3600,
		map[string]any{"slot": 1, "_blob": refBlobHash}, []string{})
	id := payload["_id"].(string)
	if rec := postRecord(t, h, payload); rec.Code != http.StatusCreated {
		t.Fatalf("publish status=%d body=%s", rec.Code, rec.Body.String())
	}

	// Plain fetch carries no blob key.
	req := httptest.NewRequest(http.MethodGet, "/records/"+id, nil)
	req.SetPathValue("id", id)
	plain := httptest.NewRecorder()
	h.GetRecordByID(plain, req)
	if plain.Code != http.StatusOK || strings.Contains(plain.Body.String(), `"blob"`) {
		t.Fatalf("plain fetch should omit blob: %d %s", plain.Code, plain.Body.String())
	}

	// Resolved fetch inlines blob metadata.
	req2 := httptest.NewRequest(http.MethodGet, "/records/"+id+"?resolve=blob", nil)
	req2.SetPathValue("id", id)
	resolved := httptest.NewRecorder()
	h.GetRecordByID(resolved, req2)
	if resolved.Code != http.StatusOK {
		t.Fatalf("resolve status=%d body=%s", resolved.Code, resolved.Body.String())
	}
	var resp struct {
		Status string `json:"status"`
		Record Record `json:"record"`
		Blob   *struct {
			Hash      string `json:"hash"`
			Size      int64  `json:"size"`
			URL       string `json:"url"`
			Protected bool   `json:"protected"`
		} `json:"blob"`
	}
	if err := json.Unmarshal(resolved.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Blob == nil || resp.Blob.Hash != refBlobHash || resp.Blob.Size != 2048 {
		t.Fatalf("bad resolved blob %+v", resp.Blob)
	}
	if resp.Blob.URL != "/down/"+refBlobHash || !resp.Blob.Protected {
		t.Fatalf("bad resolved blob fields %+v", resp.Blob)
	}

	// Blob row deleted -> blob null, record still served.
	if err := st.DeleteBlob(refBlobHash); err != nil {
		t.Fatal(err)
	}
	req3 := httptest.NewRequest(http.MethodGet, "/records/"+id+"?resolve=blob", nil)
	req3.SetPathValue("id", id)
	gone := httptest.NewRecorder()
	h.GetRecordByID(gone, req3)
	var resp3 map[string]any
	_ = json.Unmarshal(gone.Body.Bytes(), &resp3)
	blob, hasBlob := resp3["blob"]
	if gone.Code != http.StatusOK || !hasBlob || blob != nil {
		t.Fatalf("expected blob:null, got %d %s", gone.Code, gone.Body.String())
	}
}

func backdateBlob(t *testing.T, st *Store, hash string, ago time.Duration) {
	t.Helper()
	ts := time.Now().Add(-ago).UTC().Format("2006-01-02 15:04:05")
	if _, err := st.db.Exec(`UPDATE blobs SET created_at = ?, last_access = ? WHERE hash = ?`, ts, ts, hash); err != nil {
		t.Fatal(err)
	}
}

func TestJanitorHonorsBlobReferences(t *testing.T) {
	st := testStore(t)
	dir := t.TempDir()
	withBlobDir(t, dir)

	for _, hx := range []string{refBlobHash, loneBlobHash} {
		if err := os.WriteFile(BlobPath(dir, hx), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		// Max-size rows: 30-day retention, backdated past it.
		if _, err := st.UpsertBlob(hx, BlobMaxSizeBytes); err != nil {
			t.Fatal(err)
		}
	}
	// Referenced blob scanned first (older) to prove exemption isn't luck.
	backdateBlob(t, st, refBlobHash, 32*24*time.Hour)
	backdateBlob(t, st, loneBlobHash, 31*24*time.Hour)

	// Live record links the referenced blob.
	_, priv, owner := testKeys(t)
	now := time.Now().Unix()
	payload := signRecord(t, priv, owner, "saves", now-5, now+3600,
		map[string]any{"slot": 1, "_blob": refBlobHash}, []string{})
	delete(payload, "_id")
	raw, _ := json.Marshal(payload)
	rec, err := ValidateRecordBody(raw, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.InsertRecord(rec); err != nil {
		t.Fatal(err)
	}

	mgr := NewJanitor(st, 10) // tiny quota forces eviction
	if err := mgr.EvictBlobsLRU(); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetBlob(loneBlobHash); err == nil {
		t.Fatal("unreferenced expired blob should have been evicted")
	}
	if _, err := os.Stat(BlobPath(dir, loneBlobHash)); !os.IsNotExist(err) {
		t.Fatal("unreferenced blob file should be gone")
	}
	if _, err := st.GetBlob(refBlobHash); err != nil {
		t.Fatalf("referenced blob must survive: %v", err)
	}
	if _, err := os.Stat(BlobPath(dir, refBlobHash)); err != nil {
		t.Fatalf("referenced blob file must survive: %v", err)
	}
}

func TestGetReferencedBlobHashes(t *testing.T) {
	st := testStore(t)
	_, priv, owner := testKeys(t)
	now := time.Now().Unix()

	live := signRecord(t, priv, owner, "saves", now-5, now+3600,
		map[string]any{"_blob": refBlobHash}, []string{})
	delete(live, "_id")
	rawLive, _ := json.Marshal(live)
	recLive, err := ValidateRecordBody(rawLive, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.InsertRecord(recLive); err != nil {
		t.Fatal(err)
	}

	// Expired record's reference must not protect.
	oldCreated := now - 7200
	dead := signRecord(t, priv, owner, "saves", oldCreated, oldCreated+100,
		map[string]any{"_blob": loneBlobHash}, []string{})
	delete(dead, "_id")
	rawDead, _ := json.Marshal(dead)
	recDead, err := ValidateRecordBody(rawDead, oldCreated+10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.InsertRecord(recDead); err != nil {
		t.Fatal(err)
	}

	set, err := st.GetReferencedBlobHashes(now)
	if err != nil {
		t.Fatal(err)
	}
	if !set[refBlobHash] {
		t.Fatal("live reference missing from set")
	}
	if set[loneBlobHash] {
		t.Fatal("expired reference must not be in set")
	}
}
