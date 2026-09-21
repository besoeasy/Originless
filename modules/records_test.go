package modules

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	h := NewHandler(nil, NewMetrics())
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
	if _, _, err := st.InsertRecord(recOld); err != nil {
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

func TestKeysetPagination(t *testing.T) {
	st := testStore(t)
	_, priv, owner := testKeys(t)
	now := time.Now().Unix()

	// Insert 25 records with strictly increasing created_at, newest last.
	ids := make([]string, 25)
	for i := 0; i < 25; i++ {
		created := now - int64(25-i) // i=0 oldest
		payload := signRecord(t, priv, owner, "pager", created, created+3600,
			map[string]any{"n": i}, []string{})
		id := payload["_id"].(string)
		delete(payload, "_id")
		raw, _ := json.Marshal(payload)
		rec, err := ValidateRecordBody(raw, now)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := st.InsertRecord(rec); err != nil {
			t.Fatal(err)
		}
		ids[i] = id
	}
	// Expected DESC order: newest (i=24) first.
	var want []string
	for i := 24; i >= 0; i-- {
		want = append(want, ids[i])
	}

	var got []string
	cursor := ""
	for {
		req := httptest.NewRequest(http.MethodGet, "/records?collection=pager&limit=10&cursor="+cursor, nil)
		rec := httptest.NewRecorder()
		h := NewHandler(nil, NewMetrics())
		h.SetStore(st)
		h.ListRecords(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("page status=%d body=%s", rec.Code, rec.Body.String())
		}
		var resp struct {
			Records    []Record `json:"records"`
			NextCursor string   `json:"next_cursor"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		for _, r := range resp.Records {
			got = append(got, r.ID)
		}
		if resp.NextCursor == "" {
			break
		}
		cursor = resp.NextCursor
		if len(got) > 30 { // sanity: no infinite loop
			t.Fatal("pagination did not terminate")
		}
	}
	if len(got) != 25 {
		t.Fatalf("got %d records, want 25", len(got))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("page order mismatch at %d: got %s want %s", i, got[i], want[i])
		}
	}
}

func TestKeysetPaginationLegacyOffset(t *testing.T) {
	st := testStore(t)
	_, priv, owner := testKeys(t)
	now := time.Now().Unix()

	for i := 0; i < 5; i++ {
		created := now - int64(6-i)
		payload := signRecord(t, priv, owner, "pager", created, created+3600,
			map[string]any{"n": i}, []string{})
		delete(payload, "_id")
		raw, _ := json.Marshal(payload)
		rec, err := ValidateRecordBody(raw, now)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := st.InsertRecord(rec); err != nil {
			t.Fatal(err)
		}
	}

	h := NewHandler(nil, NewMetrics())
	h.SetStore(st)
	req := httptest.NewRequest(http.MethodGet, "/records?collection=pager&limit=2&cursor=1", nil)
	rec := httptest.NewRecorder()
	h.ListRecords(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("offset page status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Records []Record `json:"records"`
		Cursor  string   `json:"cursor"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Records) != 2 {
		t.Fatalf("offset page len=%d, want 2", len(resp.Records))
	}
	if resp.Cursor != "1" {
		t.Fatalf("cursor echo=%q, want 1", resp.Cursor)
	}
}

func TestBroadcastDropCount(t *testing.T) {
	b := NewRecordBroadcaster()
	if b.Dropped() != 0 {
		t.Fatalf("expected 0 dropped, got %d", b.Dropped())
	}
	// 1-buffer channel: a matching broadcast before the consumer drains
	// must be counted as dropped.
	sub := &RecordSubscriber{
		Ch:         make(chan *Record, 1),
		Collection: "chat",
	}
	b.Subscribe(sub)
	defer b.Unsubscribe(sub)

	rec := &Record{ID: "r1", Collection: "chat", Labels: []string{}, Data: []byte(`{}`)}
	b.Broadcast(rec) // fills the 1-slot buffer
	select {
	case <-sub.Ch:
	default:
		t.Fatal("first broadcast should have been delivered")
	}
	if b.Dropped() != 0 {
		t.Fatalf("no drop expected yet, got %d", b.Dropped())
	}

	// Refill then broadcast again without draining: buffer full -> dropped.
	b.Broadcast(rec)
	b.Broadcast(rec)
	if b.Dropped() != 1 {
		t.Fatalf("expected 1 dropped, got %d", b.Dropped())
	}
}

func TestRecordBroadcaster(t *testing.T) {
	b := NewRecordBroadcaster()
	if b.SubscriberCount() != 0 {
		t.Fatalf("expected 0 subscribers, got %d", b.SubscriberCount())
	}

	subChat := &RecordSubscriber{
		Ch:         make(chan *Record, 10),
		Collection: "chat",
		Label:      "room:lobby",
	}
	subGame := &RecordSubscriber{
		Ch:         make(chan *Record, 10),
		Collection: "games",
	}

	b.Subscribe(subChat)
	b.Subscribe(subGame)
	if b.SubscriberCount() != 2 {
		t.Fatalf("expected 2 subscribers, got %d", b.SubscriberCount())
	}

	recChat := &Record{
		ID:         "rec1",
		Collection: "chat",
		Labels:     []string{"room:lobby", "user:alice"},
		Data:       []byte(`{"text":"hello"}`),
	}
	recGame := &Record{
		ID:         "rec2",
		Collection: "games",
		Labels:     []string{"slot:1"},
		Data:       []byte(`{"score":100}`),
	}

	b.Broadcast(recChat)
	select {
	case got := <-subChat.Ch:
		if got.ID != "rec1" {
			t.Fatalf("subChat expected rec1, got %s", got.ID)
		}
	default:
		t.Fatal("subChat did not receive recChat")
	}

	select {
	case <-subGame.Ch:
		t.Fatal("subGame should not have received recChat")
	default:
	}

	b.Broadcast(recGame)
	select {
	case got := <-subGame.Ch:
		if got.ID != "rec2" {
			t.Fatalf("subGame expected rec2, got %s", got.ID)
		}
	default:
		t.Fatal("subGame did not receive recGame")
	}

	b.Unsubscribe(subChat)
	if b.SubscriberCount() != 1 {
		t.Fatalf("expected 1 subscriber, got %d", b.SubscriberCount())
	}

	sseBytes, err := FormatSSE(recChat)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(sseBytes), "event: record") ||
		!strings.Contains(string(sseBytes), "id: rec1") ||
		!strings.Contains(string(sseBytes), `"text":"hello"`) {
		t.Fatalf("unexpected SSE format: %s", string(sseBytes))
	}
}

func TestStreamRecordsSSE(t *testing.T) {
	st := testStore(t)
	h := NewHandler(nil, NewMetrics())
	h.SetStore(st)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req := httptest.NewRequest(http.MethodGet, "/records/stream?collection=chat&label=room:lobby", nil).WithContext(ctx)
	rec := httptest.NewRecorder()

	streamDone := make(chan struct{})
	go func() {
		defer close(streamDone)
		h.StreamRecords(rec, req)
	}()

	// Allow subscriber registration and initial header flush
	time.Sleep(50 * time.Millisecond)

	_, priv, owner := testKeys(t)
	now := time.Now().Unix()
	p := signRecord(t, priv, owner, "chat", now, now+3600, map[string]any{"msg": "hi"}, []string{"room:lobby"})
	delete(p, "_id")
	raw, _ := json.Marshal(p)

	pubReq := httptest.NewRequest(http.MethodPost, "/records", bytes.NewReader(raw))
	pubRec := httptest.NewRecorder()
	h.PublishRecord(pubRec, pubReq)

	if pubRec.Code != http.StatusCreated {
		t.Fatalf("publish failed: %d body=%s", pubRec.Code, pubRec.Body.String())
	}

	// Give broadcaster a moment to deliver to the SSE subscriber
	time.Sleep(50 * time.Millisecond)

	cancel()
	<-streamDone

	body := rec.Body.String()
	if !strings.Contains(body, ": connected") {
		t.Fatalf("expected ': connected', got %q", body)
	}
	if !strings.Contains(body, "event: record") {
		t.Fatalf("expected 'event: record', got %q", body)
	}
	if !strings.Contains(body, `"msg":"hi"`) {
		t.Fatalf("expected data payload in stream, got %q", body)
	}
}

func TestListRecordsInvalidCursor(t *testing.T) {
	st := testStore(t)
	h := NewHandler(nil, NewMetrics())
	h.SetStore(st)

	for _, c := range []string{"garbage", "-1", "abc:def", "0:deadbeef", "123:", ":abc"} {
		req := httptest.NewRequest(http.MethodGet, "/records?cursor="+c, nil)
		rec := httptest.NewRecorder()
		h.ListRecords(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("cursor=%q status=%d want 400 (body=%s)", c, rec.Code, rec.Body.String())
		}
	}
}

func TestStatusSSEBlock(t *testing.T) {
	st := testStore(t)
	h := NewHandler(nil, NewMetrics())
	h.SetStore(st)

	var resp struct {
		SSE map[string]any `json:"sse"`
	}
	req := httptest.NewRequest(http.MethodGet, "/status", nil)

	rec := httptest.NewRecorder()
	h.Status(rec, req)
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if c := resp.SSE["clients"].(float64); c != 0 {
		t.Fatalf("expected 0 clients, got %v", c)
	}
	if d := resp.SSE["dropped"].(float64); d != 0 {
		t.Fatalf("expected 0 dropped, got %v", d)
	}

	sub := &RecordSubscriber{Ch: make(chan *Record, 1), Collection: "chat"}
	h.broadcaster.Subscribe(sub)
	defer h.broadcaster.Unsubscribe(sub)

	rec2 := httptest.NewRecorder()
	h.Status(rec2, req)
	if err := json.Unmarshal(rec2.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if c := resp.SSE["clients"].(float64); c != 1 {
		t.Fatalf("expected 1 client, got %v", c)
	}
}

func TestGetRecordByID500OnDBError(t *testing.T) {
	st := testStore(t)
	h := NewHandler(nil, NewMetrics())
	h.SetStore(st)

	req := httptest.NewRequest(http.MethodGet, "/records/nonexistent", nil)
	req.SetPathValue("id", "nonexistent")

	rec := httptest.NewRecorder()
	h.GetRecordByID(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing id status=%d want 404", rec.Code)
	}

	// A genuine DB failure must surface as 500, not be masked as 404.
	if err := st.db.Close(); err != nil {
		t.Fatal(err)
	}
	rec2 := httptest.NewRecorder()
	h.GetRecordByID(rec2, req)
	if rec2.Code != http.StatusInternalServerError {
		t.Fatalf("db-error status=%d want 500 (body=%s)", rec2.Code, rec2.Body.String())
	}
}

// failingResponseWriter implements http.Flusher and fails every Write from
// failFrom onwards, mimicking a peer that died or stopped reading.
type failingResponseWriter struct {
	header   http.Header
	failFrom int
	writes   int
}

func (w *failingResponseWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *failingResponseWriter) WriteHeader(code int) {}

func (w *failingResponseWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.writes >= w.failFrom {
		return 0, errors.New("connection closed by peer")
	}
	return len(p), nil
}

func (w *failingResponseWriter) Flush() {}

func TestStreamRecordsExitsOnWriteError(t *testing.T) {
	prev := sseKeepaliveInterval
	sseKeepaliveInterval = 5 * time.Millisecond
	t.Cleanup(func() { sseKeepaliveInterval = prev })

	h := NewHandler(nil, NewMetrics())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req := httptest.NewRequest(http.MethodGet, "/records/stream", nil).WithContext(ctx)
	// Write 1 (": connected") succeeds; write 2 (keepalive) fails.
	fw := &failingResponseWriter{failFrom: 2}

	done := make(chan struct{})
	go func() {
		defer close(done)
		h.StreamRecords(fw, req)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("StreamRecords did not exit on write error")
	}
	if fw.writes < 2 {
		t.Fatalf("expected a failed keepalive write, writes=%d", fw.writes)
	}
}

func TestStreamRecordsOverCapReturns503(t *testing.T) {
	prev := MaxSSESubscribers
	MaxSSESubscribers = 1
	t.Cleanup(func() { MaxSSESubscribers = prev })

	h := NewHandler(nil, NewMetrics())
	h.broadcaster.Subscribe(&RecordSubscriber{Ch: make(chan *Record, 1)})

	req := httptest.NewRequest(http.MethodGet, "/records/stream", nil)
	rec := httptest.NewRecorder()
	h.StreamRecords(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want 503 (body=%s)", rec.Code, rec.Body.String())
	}
}

func TestBroadcastCap(t *testing.T) {
	prev := MaxSSESubscribers
	MaxSSESubscribers = 2
	t.Cleanup(func() { MaxSSESubscribers = prev })

	b := NewRecordBroadcaster()
	var subs []*RecordSubscriber
	for i := 0; i < 2; i++ {
		s := &RecordSubscriber{Ch: make(chan *Record, 1)}
		if !b.TrySubscribe(s) {
			t.Fatalf("subscriber %d should fit under cap", i)
		}
		subs = append(subs, s)
	}
	extra := &RecordSubscriber{Ch: make(chan *Record, 1)}
	if b.TrySubscribe(extra) {
		t.Fatal("third subscriber must be refused at cap")
	}
	if b.SubscriberCount() != 2 {
		t.Fatalf("count=%d want 2", b.SubscriberCount())
	}
	for _, s := range subs {
		b.Unsubscribe(s)
	}

	// Cap <= 0 means unlimited.
	MaxSSESubscribers = 0
	if !b.TrySubscribe(extra) {
		t.Fatal("cap disabled should accept any subscriber")
	}
}

func TestConcurrentPublishDuplicateRace(t *testing.T) {
	st := testStore(t)
	h := NewHandler(nil, NewMetrics())
	h.SetStore(st)

	sub := &RecordSubscriber{
		Ch:         make(chan *Record, 128),
		Collection: "race",
	}
	h.broadcaster.Subscribe(sub)
	defer h.broadcaster.Unsubscribe(sub)

	_, priv, owner := testKeys(t)

	const iters = 40
	base := time.Now().Unix() - 5
	delivered := 0
	for i := 0; i < iters; i++ {
		created := base + int64(i)
		p := signRecord(t, priv, owner, "race", created, created+3600, map[string]any{"n": i}, []string{})
		delete(p, "_id")
		raw, _ := json.Marshal(p)

		results := make(chan int, 2)
		var wg sync.WaitGroup
		for g := 0; g < 2; g++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				req := httptest.NewRequest(http.MethodPost, "/records", bytes.NewReader(raw))
				rec := httptest.NewRecorder()
				h.PublishRecord(rec, req)
				results <- rec.Code
			}()
		}
		wg.Wait()
		close(results)

		created201, dup200 := 0, 0
		for code := range results {
			switch code {
			case http.StatusCreated:
				created201++
			case http.StatusOK:
				dup200++
			default:
				t.Fatalf("iter %d: unexpected publish code %d", i, code)
			}
		}
		if created201 != 1 || dup200 != 1 {
			t.Fatalf("iter %d: created=%d dup=%d, want exactly 1 and 1", i, created201, dup200)
		}

		// Exactly one broadcast total (only the created side fans out).
		select {
		case <-sub.Ch:
			delivered++
		default:
		}
	}
	if delivered != iters {
		t.Fatalf("expected %d broadcasts, got %d", iters, delivered)
	}
}
