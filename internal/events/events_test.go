package events

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/besoeasy/originless/internal/testutil"
)

func publishTestEvent(t *testing.T, router http.Handler, raw []byte) *httptest.ResponseRecorder {
	t.Helper()
	return testutil.PublishTestEvent(t, router, raw)
}

type eventPublishResponse struct {
	Status    string `json:"status"`
	ID        string `json:"id"`
	StoredAt  string `json:"stored_at"`
	Duplicate bool   `json:"duplicate"`
}

func TestSignedEventLifecycle(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	createdAt := now.Unix() - 5
	expiresAt := now.Unix() + 3600
	raw, owner := testutil.MakeSignedEvent(t, createdAt, expiresAt, "chat", map[string]any{
		"user":    "alice",
		"message": "Hello world!",
	}, []string{"room:lobby"}, "")
	router := NewHandler(NewStore())

	recorder := publishTestEvent(t, router, raw)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("publish status = %d, want %d; body = %s", recorder.Code, http.StatusCreated, recorder.Body.String())
	}
	var published eventPublishResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &published); err != nil {
		t.Fatalf("decode publish response: %v", err)
	}
	if published.Status != "success" || published.ID == "" || published.StoredAt == "" {
		t.Fatalf("publish response = %+v, want success with id and stored_at", published)
	}

	getRecorder := httptest.NewRecorder()
	getRequest := httptest.NewRequest(http.MethodGet, "/events/"+published.ID, nil)
	router.ServeHTTP(getRecorder, getRequest)
	if getRecorder.Code != http.StatusOK {
		t.Fatalf("get status = %d, want %d; body = %s", getRecorder.Code, http.StatusOK, getRecorder.Body.String())
	}
	var got struct {
		Event *Event `json:"event"`
	}
	if err := json.Unmarshal(getRecorder.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode get response: %v", err)
	}
	if got.Event == nil || got.Event.ID != published.ID || got.Event.Owner != owner || got.Event.Collection != "chat" {
		t.Fatalf("event = %+v, want published event", got.Event)
	}
	if string(got.Event.Data) != `{"message":"Hello world!","user":"alice"}` {
		t.Errorf("canonical data = %s, want sorted compact JSON", got.Event.Data)
	}

	queryRecorder := httptest.NewRecorder()
	queryRequest := httptest.NewRequest(http.MethodGet, "/events?collection=chat&label=room:lobby&owner="+owner, nil)
	router.ServeHTTP(queryRecorder, queryRequest)
	if queryRecorder.Code != http.StatusOK {
		t.Fatalf("query status = %d, want %d", queryRecorder.Code, http.StatusOK)
	}
	var query struct {
		Events []Event `json:"events"`
	}
	if err := json.Unmarshal(queryRecorder.Body.Bytes(), &query); err != nil {
		t.Fatalf("decode query response: %v", err)
	}
	if len(query.Events) != 1 || query.Events[0].ID != published.ID {
		t.Errorf("query events = %+v, want published event", query.Events)
	}

	duplicateRecorder := publishTestEvent(t, router, raw)
	if duplicateRecorder.Code != http.StatusOK {
		t.Fatalf("duplicate status = %d, want %d", duplicateRecorder.Code, http.StatusOK)
	}
	var duplicate eventPublishResponse
	if err := json.Unmarshal(duplicateRecorder.Body.Bytes(), &duplicate); err != nil {
		t.Fatalf("decode duplicate response: %v", err)
	}
	if !duplicate.Duplicate || duplicate.ID != published.ID {
		t.Errorf("duplicate response = %+v, want duplicate success", duplicate)
	}
}

func TestExpiredEventRequiresIncludeExpired(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	raw, _ := testutil.MakeSignedEvent(t, now.Unix()-100, now.Unix()-1, "chat", map[string]any{"message": "expired"}, []string{}, "")
	store := NewStore()
	handler := NewHandler(store)
	published := publishTestEvent(t, handler, raw)
	publishedID := eventIDFromResponse(t, published)

	queryRecorder := httptest.NewRecorder()
	handler.ServeHTTP(queryRecorder, httptest.NewRequest(http.MethodGet, "/events?include_expired=true", nil))
	if queryRecorder.Code != http.StatusOK || !strings.Contains(queryRecorder.Body.String(), "expired") {
		t.Errorf("include_expired response = %d/%s, want expired event", queryRecorder.Code, queryRecorder.Body.String())
	}

	getRecorder := httptest.NewRecorder()
	handler.ServeHTTP(getRecorder, httptest.NewRequest(http.MethodGet, "/events/"+publishedID, nil))
	if getRecorder.Code != http.StatusNotFound {
		t.Errorf("expired get status = %d, want %d", getRecorder.Code, http.StatusNotFound)
	}
}

func eventIDFromResponse(t *testing.T, recorder *httptest.ResponseRecorder) string {
	t.Helper()
	var response eventPublishResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode publish response: %v", err)
	}
	return response.ID
}

func TestEventQueryCursorAndFilters(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	store := NewStore()
	handler := NewHandler(store)
	first, _ := testutil.MakeSignedEvent(t, now.Unix()-20, now.Unix()+3600, "chat", map[string]any{"message": "first"}, []string{"room:lobby"}, "")
	second, _ := testutil.MakeSignedEvent(t, now.Unix()-10, now.Unix()+3600, "chat", map[string]any{"message": "second"}, []string{"room:lobby"}, "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	publishTestEvent(t, handler, first)
	secondRecorder := publishTestEvent(t, handler, second)
	var secondPublished eventPublishResponse
	if err := json.Unmarshal(secondRecorder.Body.Bytes(), &secondPublished); err != nil {
		t.Fatalf("decode second publish response: %v", err)
	}

	firstPage := httptest.NewRecorder()
	handler.ServeHTTP(firstPage, httptest.NewRequest(http.MethodGet, "/events?collection=chat&limit=1", nil))
	var page struct {
		Events     []Event `json:"events"`
		NextCursor string  `json:"next_cursor"`
	}
	if err := json.Unmarshal(firstPage.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode first page: %v", err)
	}
	if len(page.Events) != 1 || page.NextCursor == "" {
		t.Fatalf("first page = %+v, want one event and cursor", page)
	}

	secondPage := httptest.NewRecorder()
	handler.ServeHTTP(secondPage, httptest.NewRequest(http.MethodGet, "/events?collection=chat&limit=1&cursor="+page.NextCursor, nil))
	var secondResult struct {
		Events []Event `json:"events"`
	}
	if err := json.Unmarshal(secondPage.Body.Bytes(), &secondResult); err != nil {
		t.Fatalf("decode second page: %v", err)
	}
	if len(secondResult.Events) != 1 || secondResult.Events[0].ID == page.Events[0].ID {
		t.Errorf("second page = %+v, want a different event", secondResult.Events)
	}

	blobPage := httptest.NewRecorder()
	handler.ServeHTTP(blobPage, httptest.NewRequest(http.MethodGet, "/events?blob=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", nil))
	if !strings.Contains(blobPage.Body.String(), secondPublished.ID) {
		t.Errorf("blob filter response = %s, want second event", blobPage.Body.String())
	}
}

func TestEventValidationRejectsBadSignatureAndTTL(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	raw, _ := testutil.MakeSignedEvent(t, now.Unix(), now.Unix()+3600, "chat", map[string]any{"message": "hello"}, []string{}, "")
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("decode event: %v", err)
	}
	payload["data"] = map[string]any{"message": "tampered"}
	tampered, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal tampered event: %v", err)
	}
	router := NewHandler(NewStore())
	badSignature := publishTestEvent(t, router, tampered)
	if badSignature.Code != http.StatusUnauthorized {
		t.Errorf("tampered status = %d, want %d", badSignature.Code, http.StatusUnauthorized)
	}

	tooLong, _ := testutil.MakeSignedEvent(t, now.Unix(), now.Unix()+MaxEventTTL+1, "chat", map[string]any{"message": "hello"}, []string{}, "")
	tooLong = append(tooLong, bytes.Repeat([]byte(" "), MaxEventSize)...)
	tooLongRecorder := publishTestEvent(t, router, tooLong)
	if tooLongRecorder.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized status = %d, want %d", tooLongRecorder.Code, http.StatusRequestEntityTooLarge)
	}
}

func TestEventStreamEndpoint(t *testing.T) {
	store := NewStore()
	handler := NewHandler(store)
	server := httptest.NewServer(handler)
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/events/stream?collection=chat", nil)
	if err != nil {
		t.Fatalf("create stream request: %v", err)
	}
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatalf("open event stream: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("stream status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	if contentType := response.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "text/event-stream") {
		t.Fatalf("stream content type = %q, want text/event-stream", contentType)
	}

	reader := bufio.NewReader(response.Body)
	connected, err := reader.ReadString('\n')
	if err != nil || connected != ": connected\n" {
		t.Fatalf("connected frame = %q, error = %v", connected, err)
	}
	if _, err := reader.ReadString('\n'); err != nil {
		t.Fatalf("read connected frame terminator: %v", err)
	}

	now := time.Now().Truncate(time.Second)
	raw, _ := testutil.MakeSignedEvent(t, now.Unix(), now.Unix()+3600, "chat", map[string]any{"message": "streamed"}, []string{}, "")
	publishTestEvent(t, handler, raw)

	var frame strings.Builder
	for {
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			t.Fatalf("read event frame: %v", readErr)
		}
		frame.WriteString(line)
		if line == "\n" {
			break
		}
	}
	if !strings.Contains(frame.String(), "event: event") || !strings.Contains(frame.String(), "data: {") {
		t.Errorf("event frame = %q, want event and JSON data", frame.String())
	}
}

func TestEventStoreBroadcastsToMatchingSubscriber(t *testing.T) {
	store := NewStore()
	subscriber, ok := store.subscribe(eventFilter{Collection: "chat"})
	if !ok {
		t.Fatal("subscribe returned false")
	}
	defer store.unsubscribe(subscriber)

	now := time.Now().Truncate(time.Second)
	raw, _ := testutil.MakeSignedEvent(t, now.Unix(), now.Unix()+3600, "chat", map[string]any{"message": "streamed"}, []string{}, "")
	publishTestEvent(t, NewHandler(store), raw)

	select {
	case event := <-subscriber.ch:
		if event.Collection != "chat" {
			t.Errorf("broadcast collection = %q, want chat", event.Collection)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for broadcast event")
	}
}
