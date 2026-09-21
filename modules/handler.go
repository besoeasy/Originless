package modules

import (
	"encoding/json"
	"net/http"
	"time"
)

// Version is the Originless server build version reported by /status.
// Overridable at compile time via -ldflags="-X github.com/besoeasy/originless/modules.Version=...".
var Version = "1.0.0"

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
