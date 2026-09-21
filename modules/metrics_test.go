package modules

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMetricsSSEOutput(t *testing.T) {
	b := NewRecordBroadcaster()
	sub := &RecordSubscriber{Ch: make(chan *Record, 1)}
	b.Subscribe(sub)
	defer b.Unsubscribe(sub)

	req := httptest.NewRequest("GET", "/metrics", nil)

	rec := httptest.NewRecorder()
	NewMetrics().Handler(nil, b)(rec, req)
	body := rec.Body.String()
	if !strings.Contains(body, "originless_sse_clients 1") {
		t.Fatalf("expected clients 1 in metrics, got:\n%s", body)
	}
	if !strings.Contains(body, "originless_sse_dropped_total 0") {
		t.Fatalf("expected dropped 0 in metrics, got:\n%s", body)
	}

	// nil broadcaster must produce both series as zero, not omit them.
	rec2 := httptest.NewRecorder()
	NewMetrics().Handler(nil, nil)(rec2, req)
	body2 := rec2.Body.String()
	if !strings.Contains(body2, "originless_sse_clients 0") {
		t.Fatalf("expected clients 0 with nil broadcaster, got:\n%s", body2)
	}
	if !strings.Contains(body2, "originless_sse_dropped_total 0") {
		t.Fatalf("expected dropped 0 with nil broadcaster, got:\n%s", body2)
	}
}
