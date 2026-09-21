package modules

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// Metrics collects Prometheus-style metrics for the originless node.
// It is intentionally dependency-free: metrics are rendered directly in the
// Prometheus text exposition format (version 0.0.4).
type Metrics struct {
	mu       sync.Mutex
	requests map[string]*atomic.Int64

	errors     atomic.Int64
	uploads    atomic.Int64
	uploadSize atomic.Int64

	storageUsed atomic.Int64
}

func NewMetrics() *Metrics {
	return &Metrics{requests: make(map[string]*atomic.Int64)}
}

func metricPath(path string) string {
	if path == "" {
		return "/"
	}
	if path == "/records/stream" {
		return "/records/stream"
	}
	if strings.HasPrefix(path, "/records/") {
		return "/records/{id}"
	}
	if strings.HasPrefix(path, "/down/") {
		return "/down/{hash}"
	}
	return path
}

// Middleware counts every HTTP request by URL path and flags 4xx/5xx responses
// as errors. It is the outermost middleware so it sees all traffic.
func (m *Metrics) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.IncRequest(metricPath(r.URL.Path))
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		if rec.status >= http.StatusBadRequest {
			m.IncError()
		}
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := r.ResponseWriter.(http.Hijacker); ok {
		return hj.Hijack()
	}
	return nil, nil, fmt.Errorf("underlying ResponseWriter does not implement http.Hijacker")
}

func (r *statusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

func (m *Metrics) IncRequest(path string) {
	m.mu.Lock()
	c, ok := m.requests[path]
	if !ok {
		c = &atomic.Int64{}
		m.requests[path] = c
	}
	m.mu.Unlock()
	c.Add(1)
}

func (m *Metrics) IncError() { m.errors.Add(1) }

func (m *Metrics) IncUpload(size int64) {
	m.uploads.Add(1)
	m.uploadSize.Add(size)
}

func (m *Metrics) SetStorageUsed(size int64) { m.storageUsed.Store(size) }

// Handler serves the /metrics endpoint in Prometheus text format. Gauges are
// refreshed on each scrape so they always reflect current state.
func (m *Metrics) Handler(janitor *Manager, broadcaster *RecordBroadcaster) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if janitor != nil {
			if size, err := janitor.store.GetBlobSize(); err == nil {
				m.SetStorageUsed(size)
			}
		}

		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

		var sb strings.Builder
		sb.WriteString("# HELP originless_http_requests_total Total HTTP requests by path.\n")
		sb.WriteString("# TYPE originless_http_requests_total counter\n")
		m.mu.Lock()
		paths := make([]string, 0, len(m.requests))
		for p := range m.requests {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		for _, p := range paths {
			fmt.Fprintf(&sb, "originless_http_requests_total{path=%q} %d\n", p, m.requests[p].Load())
		}
		m.mu.Unlock()

		sb.WriteString("# HELP originless_http_errors_total Total HTTP responses with status >= 400.\n")
		sb.WriteString("# TYPE originless_http_errors_total counter\n")
		fmt.Fprintf(&sb, "originless_http_errors_total %d\n", m.errors.Load())

		sb.WriteString("# HELP originless_uploads_total Total files uploaded.\n")
		sb.WriteString("# TYPE originless_uploads_total counter\n")
		fmt.Fprintf(&sb, "originless_uploads_total %d\n", m.uploads.Load())

		sb.WriteString("# HELP originless_upload_bytes_total Total bytes uploaded.\n")
		sb.WriteString("# TYPE originless_upload_bytes_total counter\n")
		fmt.Fprintf(&sb, "originless_upload_bytes_total %d\n", m.uploadSize.Load())

		sb.WriteString("# HELP originless_storage_used_bytes Storage used by tracked blobs.\n")
		sb.WriteString("# TYPE originless_storage_used_bytes gauge\n")
		fmt.Fprintf(&sb, "originless_storage_used_bytes %d\n", m.storageUsed.Load())

		var clients, dropped int64
		if broadcaster != nil {
			clients = int64(broadcaster.SubscriberCount())
			dropped = broadcaster.Dropped()
		}
		sb.WriteString("# HELP originless_sse_clients Active records/stream subscribers.\n")
		sb.WriteString("# TYPE originless_sse_clients gauge\n")
		fmt.Fprintf(&sb, "originless_sse_clients %d\n", clients)

		sb.WriteString("# HELP originless_sse_dropped_total Records skipped for slow SSE consumers.\n")
		sb.WriteString("# TYPE originless_sse_dropped_total counter\n")
		fmt.Fprintf(&sb, "originless_sse_dropped_total %d\n", dropped)

		w.Write([]byte(sb.String()))
	}
}
