package modules

import (
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"time"
)

// Version is the Originless server build version reported by /status.
// Overridable at compile time via -ldflags="-X github.com/besoeasy/originless/modules.Version=...".
var Version = "1.0.0"

var processStartTime = time.Now().UTC()

// FormatUptime returns a human-readable duration string (e.g. "2h 15m 4s").
func FormatUptime(d time.Duration) string {
	d = d.Round(time.Second)
	days := d / (24 * time.Hour)
	d -= days * 24 * time.Hour
	hours := d / time.Hour
	d -= hours * time.Hour
	mins := d / time.Minute
	d -= mins * time.Minute
	secs := d / time.Second

	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm %ds", days, hours, mins, secs)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm %ds", hours, mins, secs)
	}
	if mins > 0 {
		return fmt.Sprintf("%dm %ds", mins, secs)
	}
	return fmt.Sprintf("%ds", secs)
}

// P2PBroadcaster defines the interface for gossip broadcasting to P2P swarms.
type P2PBroadcaster interface {
	BroadcastRecord(rec *Record)
	BroadcastBlob(hash string, size int64)
	Status() map[string]any
}

type Handler struct {
	janitor     *Manager
	metrics     *Metrics
	semaphore   chan struct{}
	store       *Store
	broadcaster *RecordBroadcaster
	p2p         P2PBroadcaster
	startTime   time.Time
}

func NewHandler(janitorManager *Manager, metrics *Metrics) *Handler {
	h := &Handler{
		janitor:     janitorManager,
		metrics:     metrics,
		semaphore:   make(chan struct{}, MaxConcurrentOps),
		broadcaster: NewRecordBroadcaster(),
		startTime:   time.Now().UTC(),
	}
	if janitorManager != nil {
		h.store = janitorManager.Store()
	}
	return h
}

// SetP2P registers a P2P broadcaster for gossip and status reporting.
func (h *Handler) SetP2P(p P2PBroadcaster) {
	h.p2p = p
}

// SetStore allows tests / embedding to provide a DB without changing signatures.
func (h *Handler) SetStore(s *Store) {
	h.store = s
}

func (h *Handler) recordStore() *Store {
	if h.store != nil {
		return h.store
	}
	if h.janitor != nil {
		return h.janitor.Store()
	}
	return nil
}

// Status reports node health, storage policy, and primitive counters.
func (h *Handler) Status(w http.ResponseWriter, r *http.Request) {
	stTime := h.startTime
	if stTime.IsZero() {
		stTime = processStartTime
	}
	uptime := time.Since(stTime)

	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	vitals := map[string]any{
		"alloc_bytes": m.Alloc,
		"alloc_str":   FormatBytes(int64(m.Alloc)),
		"sys_bytes":   m.Sys,
		"sys_str":     FormatBytes(int64(m.Sys)),
		"goroutines":  runtime.NumGoroutine(),
		"num_cpu":     runtime.NumCPU(),
		"arch":        runtime.GOARCH,
		"os":          runtime.GOOS,
		"gc_cycles":   m.NumGC,
	}

	var bCount int64
	var bSize int64
	var rCount int64
	var dbSize int64
	var topCollections []CollectionStat

	if st := h.recordStore(); st != nil {
		if bc, err := st.GetBlobCount(); err == nil {
			bCount = bc
		}
		if bs, err := st.GetBlobSize(); err == nil {
			bSize = bs
		}
		if rc, err := st.GetRecordCount(); err == nil {
			rCount = rc
		}
		dbSize = st.GetDBFileSize()
		if top, err := st.GetTopCollections(10); err == nil {
			topCollections = top
		}
	}
	if topCollections == nil {
		topCollections = []CollectionStat{}
	}

	totalSize := dbSize + bSize
	storage := map[string]any{
		"blob_count":      bCount,
		"blob_size":       bSize,
		"blob_size_str":   FormatBytes(bSize),
		"record_count":    rCount,
		"db_size":         dbSize,
		"db_size_str":     FormatBytes(dbSize),
		"total_size":      totalSize,
		"total_size_str":  FormatBytes(totalSize),
		"top_collections": topCollections,
	}

	sseClients := 0
	var sseDropped int64
	if h.broadcaster != nil {
		sseClients = h.broadcaster.SubscriberCount()
		sseDropped = h.broadcaster.Dropped()
	}

	payload := map[string]any{
		"status":      "success",
		"timestamp":   time.Now().UTC().Format(time.RFC3339),
		"version":     Version,
		"uptime_secs": int64(uptime.Seconds()),
		"uptime":      FormatUptime(uptime),
		"vitals":      vitals,
		"storage":     storage,
		"blobs": map[string]any{
			"count": bCount, "size": bSize, "sizeStr": FormatBytes(bSize),
		},
		"events": map[string]any{
			"count": rCount,
		},
		"records": map[string]any{
			"count": rCount,
		},
		"sse": map[string]any{
			"clients":     sseClients,
			"max_clients": MaxSSESubscribers,
			"dropped":     sseDropped,
		},
	}

	if h.metrics != nil {
		payload["traffic"] = h.metrics.Snapshot()
	}

	if h.janitor != nil {
		payload["janitor"] = h.janitor.Status()
	}

	if h.p2p != nil {
		payload["p2p"] = h.p2p.Status()
	} else {
		payload["p2p"] = map[string]any{
			"enabled": false,
		}
	}

	writeJSON(w, http.StatusOK, payload)
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
