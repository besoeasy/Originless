package modules

import (
	"encoding/json"
	"net/http"
	"time"
)

// Version is the Originless server build version reported by /status.
const Version = "0.1.0"

type Handler struct {
	janitor     *Manager
	metrics     *Metrics
	semaphore   chan struct{}
	store       *Store
	broadcaster *RecordBroadcaster
}

func NewHandler(janitorManager *Manager, metrics *Metrics) *Handler {
	h := &Handler{
		janitor:     janitorManager,
		metrics:     metrics,
		semaphore:   make(chan struct{}, MaxConcurrentOps),
		broadcaster: NewRecordBroadcaster(),
	}
	if janitorManager != nil {
		h.store = janitorManager.Store()
	}
	return h
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
	payload := map[string]any{
		"status":    "success",
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"version":   Version,
		"blobs": map[string]any{
			"count": 0, "size": int64(0), "sizeStr": FormatBytes(0),
		},
		"events": map[string]any{
			"count": int64(0),
		},
		"records": map[string]any{
			"count": int64(0),
		},
		"sse": map[string]any{
			"clients": 0, "dropped": int64(0),
		},
	}

	if st := h.recordStore(); st != nil {
		if bCount, err := st.GetBlobCount(); err == nil {
			bSize, _ := st.GetBlobSize()
			payload["blobs"] = map[string]any{
				"count":   bCount,
				"size":    bSize,
				"sizeStr": FormatBytes(bSize),
			}
		}
		if rCount, err := st.GetRecordCount(); err == nil {
			payload["events"] = map[string]any{
				"count": rCount,
			}
			payload["records"] = map[string]any{
				"count": rCount,
			}
		}
	}

	if h.broadcaster != nil {
		payload["sse"] = map[string]any{
			"clients": h.broadcaster.SubscriberCount(),
			"dropped": h.broadcaster.Dropped(),
		}
	}

	writeJSON(w, http.StatusOK, payload)
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
