package modules

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	s, err := NewStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close(); os.Remove(dbPath) })
	return s
}

func testKeys(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey, string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	owner := "ed25519:" + hex.EncodeToString(pub)
	return pub, priv, owner
}

func signRecord(t *testing.T, priv ed25519.PrivateKey, owner, collection string, created, expires int64, data any, labels []string) map[string]any {
	t.Helper()
	rawData, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := canonicalJSON(rawData)
	if err != nil {
		t.Fatal(err)
	}
	idHex, idBytes := computeRecordID(owner, collection, created, expires, canonical, labels)
	sig := ed25519.Sign(priv, idBytes)
	var dataObj map[string]any
	if err := json.Unmarshal(rawData, &dataObj); err != nil {
		t.Fatal(err)
	}
	return map[string]any{
		"owner":      owner,
		"collection": collection,
		"created_at": created,
		"expires_at": expires,
		"data":       dataObj,
		"labels":     labels,
		"sig":        hex.EncodeToString(sig),
		"_id":        idHex,
	}
}

func TestValidateRecordBodyOK(t *testing.T) {
	_, priv, owner := testKeys(t)
	now := time.Now().Unix()
	created := now - 10
	expires := created + 3600
	payload := signRecord(t, priv, owner, "chat", created, expires, map[string]any{"text": "gg"}, []string{"room:general"})
	delete(payload, "_id")
	raw, _ := json.Marshal(payload)
	rec, err := ValidateRecordBody(raw, now)
	if err != nil {
		t.Fatalf("validate failed: %v", err)
	}
	if rec.Collection != "chat" || rec.Owner != owner {
		t.Fatalf("unexpected record %+v", rec)
	}
	if len(rec.ID) != 64 {
		t.Fatalf("bad id %q", rec.ID)
	}
}

func TestValidateRejectsBadSigAndTTL(t *testing.T) {
	_, priv, owner := testKeys(t)
	now := time.Now().Unix()
	created := now - 10

	// bad sig
	payload := signRecord(t, priv, owner, "chat", created, created+100, map[string]any{"a": 1}, []string{})
	delete(payload, "_id")
	payload["sig"] = "00"
	raw, _ := json.Marshal(payload)
	if _, err := ValidateRecordBody(raw, now); err == nil {
		t.Fatal("expected bad sig error")
	}

	// ttl too long
	payload2 := signRecord(t, priv, owner, "chat", created, created+MaxRecordTTL+1, map[string]any{"a": 1}, []string{})
	delete(payload2, "_id")
	// re-sign with long ttl so sig verifies but TTL check fails
	raw2, _ := json.Marshal(payload2)
	if _, err := ValidateRecordBody(raw2, now); err == nil {
		t.Fatal("expected TTL error")
	}

	// oversize
	big := bytes.Repeat([]byte("x"), MaxRecordSize)
	payload3 := signRecord(t, priv, owner, "chat", created, created+100, map[string]any{"blob": string(big)}, []string{})
	delete(payload3, "_id")
	raw3, _ := json.Marshal(payload3)
	if _, err := ValidateRecordBody(raw3, now); err == nil {
		t.Fatal("expected oversize error")
	}
}

func TestRecordsEndToEnd(t *testing.T) {
	st := testStore(t)
	h := NewHandler(nil, nil, NewMetrics(), nil)
	h.SetStore(st)

	_, priv, owner := testKeys(t)
	now := time.Now().Unix()
	created := now - 5
	expires := now + 3600

	payload := signRecord(t, priv, owner, "saves", created, expires, map[string]any{"slot": 1, "level": 12}, []string{"slot1"})
	id := payload["_id"].(string)
	delete(payload, "_id")
	raw, _ := json.Marshal(payload)

	// POST
	req := httptest.NewRequest(http.MethodPost, "/records", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.PublishRecord(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST status=%d body=%s", rec.Code, rec.Body.String())
	}
	var postResp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &postResp); err != nil {
		t.Fatal(err)
	}
	if postResp["id"] != id {
		t.Fatalf("id mismatch: got %v want %v", postResp["id"], id)
	}

	// duplicate POST -> 200 duplicate
	req2 := httptest.NewRequest(http.MethodPost, "/records", bytes.NewReader(raw))
	rec2 := httptest.NewRecorder()
	h.PublishRecord(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("dup status=%d body=%s", rec2.Code, rec2.Body.String())
	}

	// GET by id
	req3 := httptest.NewRequest(http.MethodGet, "/records/"+id, nil)
	req3.SetPathValue("id", id)
	rec3 := httptest.NewRecorder()
	h.GetRecordByID(rec3, req3)
	if rec3.Code != http.StatusOK {
		t.Fatalf("GET id status=%d body=%s", rec3.Code, rec3.Body.String())
	}

	// GET list filtered
	req4 := httptest.NewRequest(http.MethodGet, "/records?collection=saves&label=slot1&owner="+owner, nil)
	rec4 := httptest.NewRecorder()
	h.ListRecords(rec4, req4)
	if rec4.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", rec4.Code, rec4.Body.String())
	}
	var listResp struct {
		Status  string   `json:"status"`
		Records []Record `json:"records"`
	}
	if err := json.Unmarshal(rec4.Body.Bytes(), &listResp); err != nil {
		t.Fatal(err)
	}
	if len(listResp.Records) != 1 || listResp.Records[0].ID != id {
		t.Fatalf("list mismatch %+v", listResp)
	}

	// expired hidden: insert an already-expired record directly (bypass now-check via raw insert)
	_, priv2, owner2 := testKeys(t)
	oldCreated := now - 7200
	oldExpires := now - 3600 // already expired
	p2 := signRecord(t, priv2, owner2, "chat", oldCreated, oldExpires, map[string]any{"text": "old"}, []string{})
	delete(p2, "_id")
	rawOld, _ := json.Marshal(p2)
	recOld, err := ValidateRecordBody(rawOld, oldCreated+10) // validate as-of then
	if err != nil {
		t.Fatalf("validate old failed: %v", err)
	}
	if _, err := st.InsertRecord(recOld); err != nil {
		t.Fatal(err)
	}
	req5 := httptest.NewRequest(http.MethodGet, "/records?collection=chat", nil)
	rec5 := httptest.NewRecorder()
	h.ListRecords(rec5, req5)
	var list5 struct {
		Records []Record `json:"records"`
	}
	_ = json.Unmarshal(rec5.Body.Bytes(), &list5)
	for _, r := range list5.Records {
		if r.ID == recOld.ID {
			t.Fatal("expired record should be hidden")
		}
	}
}
