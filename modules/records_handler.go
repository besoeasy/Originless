package modules

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// PublishRecord verifies a signed record and stores it (append-only, idempotent).
func (h *Handler) PublishRecord(w http.ResponseWriter, r *http.Request) {
	st := h.recordStore()
	if st == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"status": "error", "error": "records store unavailable",
		})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, MaxRecordSize+1024)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"status": "error", "error": "cannot read body",
		})
		return
	}
	if len(raw) > MaxRecordSize {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{
			"status": "error", "error": "record too large", "maxSize": MaxRecordSize,
		})
		return
	}

	rec, err := ValidateRecordBody(raw, time.Now().Unix())
	if err != nil {
		status := http.StatusBadRequest
		msg := err.Error()
		switch {
		case contains(msg, "too large"):
			status = http.StatusRequestEntityTooLarge
		case contains(msg, "bad sig"):
			status = http.StatusUnauthorized
		}
		writeJSON(w, status, map[string]any{"status": "error", "error": msg})
		return
	}

	// Linked blob must already be stored (upload first, then reference).
	if blobHash, _ := RecordBlobHash(rec.Data); blobHash != "" {
		if _, err := st.GetBlob(blobHash); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"status": "error", "error": "referenced blob not found", "hash": blobHash,
			})
			return
		}
	}

	// Idempotent: InsertRecord reports whether this ID already existed,
	// so a publish is a single round trip and a concurrent duplicate race
	// can't get a spurious 201 (or a re-broadcast).
	created, storedAt, err := st.InsertRecord(rec)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"status": "error", "error": "failed to store record",
		})
		return
	}
	if !created {
		existing, derr := st.GetRecord(rec.ID)
		if derr != nil {
			writeJSON(w, http.StatusOK, map[string]any{
				"status": "success", "id": rec.ID, "duplicate": true,
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "success", "id": existing.ID, "stored_at": existing.StoredAt, "duplicate": true,
		})
		return
	}

	if h.broadcaster != nil {
		h.broadcaster.Broadcast(rec)
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"status": "success", "id": rec.ID, "stored_at": storedAt,
	})
}

// ListRecords: GET /records?owner=&collection=&label=&since=&until=&search=&limit=&cursor=&include_expired=
func (h *Handler) ListRecords(w http.ResponseWriter, r *http.Request) {
	st := h.recordStore()
	if st == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"status": "error", "error": "records store unavailable",
		})
		return
	}

	q := r.URL.Query()
	limit := 50
	if v := q.Get("limit"); v != "" {
		if p, err := strconv.Atoi(v); err == nil && p > 0 && p <= 100 {
			limit = p
		} else if err == nil && (p <= 0 || p > 100) {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"status": "error", "error": "limit must be 1..100",
			})
			return
		}
	}
	// cursor is a keyset token "<created_at>:<id>" (opaque object position).
	// Plain numeric cursors are accepted for backward compatibility as
	// OFFSETs.
	var cursor int
	var afterCreated int64
	var afterID string
	if v := q.Get("cursor"); v != "" {
		if n, err := fmt.Sscanf(v, "%d:%64s", &afterCreated, &afterID); err == nil && n == 2 && afterCreated > 0 {
			// keyset mode — afterCreated/afterID describe the last row
		} else {
			afterCreated, afterID = 0, "" // partial scan must not leak
			if p, err := strconv.Atoi(v); err != nil || p < 0 {
				writeJSON(w, http.StatusBadRequest, map[string]any{
					"status": "error", "error": "invalid cursor",
				})
				return
			} else {
				cursor = p
			}
		}
	}

	var since, until int64
	if v := q.Get("since"); v != "" {
		p, err := strconv.ParseInt(v, 10, 64)
		if err != nil || p < 0 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"status": "error", "error": "invalid since"})
			return
		}
		since = p
	}
	if v := q.Get("until"); v != "" {
		p, err := strconv.ParseInt(v, 10, 64)
		if err != nil || p < 0 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"status": "error", "error": "invalid until"})
			return
		}
		until = p
	}

	f := RecordFilter{
		Owner:          q.Get("owner"),
		Collection:     q.Get("collection"),
		Label:          q.Get("label"),
		Since:          since,
		Until:          until,
		Search:         q.Get("search"),
		Limit:          limit,
		Offset:         cursor,
		IncludeExpired: q.Get("include_expired") == "true",
		AfterCreated:   afterCreated,
		AfterID:        afterID,
		Now:            time.Now().Unix(),
	}

	records, err := st.QueryRecords(f)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"status": "error", "error": "query failed",
		})
		return
	}

	next := ""
	if len(records) > 0 && len(records) == limit {
		last := records[len(records)-1]
		next = fmt.Sprintf("%d:%s", last.CreatedAt, last.ID)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "success", "records": records,
		"limit": limit, "cursor": q.Get("cursor"), "next_cursor": next,
	})
}

// GetRecordByID: GET /records/{id}
func (h *Handler) GetRecordByID(w http.ResponseWriter, r *http.Request) {
	st := h.recordStore()
	if st == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"status": "error", "error": "records store unavailable",
		})
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"status": "error", "error": "missing id"})
		return
	}
	rec, err := st.GetRecord(id)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			// Real database failure must not masquerade as a 404.
			writeJSON(w, http.StatusInternalServerError, map[string]any{
				"status": "error", "error": "query failed",
			})
			return
		}
		writeJSON(w, http.StatusNotFound, map[string]any{"status": "error", "error": "not found"})
		return
	}
	if r.URL.Query().Get("include_expired") != "true" && rec.ExpiresAt <= time.Now().Unix() {
		writeJSON(w, http.StatusNotFound, map[string]any{"status": "error", "error": "expired"})
		return
	}
	// ?resolve=blob inlines the linked blob's metadata so clients fetch
	// record + attachment in one round trip.
	if r.URL.Query().Get("resolve") == "blob" {
		if h, _ := RecordBlobHash(rec.Data); h != "" {
			if meta, err := st.GetBlob(h); err == nil {
				writeJSON(w, http.StatusOK, map[string]any{
					"status": "success",
					"record": rec,
					"blob": map[string]any{
						"hash":           meta.Hash,
						"size":           meta.Size,
						"sizeStr":        FormatBytes(meta.Size),
						"url":            "/down/" + meta.Hash,
						"retained_until": meta.RetainedUntil.UTC().Format(time.RFC3339),
						"protected":      meta.Protected,
					},
				})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"status": "success", "record": rec, "blob": nil,
			})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "success", "record": rec})
}

// StreamRecords streams newly published records matching query filters over Server-Sent Events (SSE).
// GET /records/stream?owner=&collection=&label=&search=
func (h *Handler) StreamRecords(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	q := r.URL.Query()
	sub := &RecordSubscriber{
		Ch:         make(chan *Record, 64),
		Owner:      q.Get("owner"),
		Collection: q.Get("collection"),
		Label:      q.Get("label"),
		Search:     q.Get("search"),
	}

	if h.broadcaster != nil {
		h.broadcaster.Subscribe(sub)
		defer h.broadcaster.Unsubscribe(sub)
	}

	_, _ = w.Write([]byte(": connected\n\n"))
	flusher.Flush()

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, err := w.Write([]byte(": keepalive\n\n"))
			if err != nil {
				return
			}
			flusher.Flush()
		case rec := <-sub.Ch:
			sse, err := FormatSSE(rec)
			if err != nil {
				continue
			}
			_, err = w.Write(sse)
			if err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func contains(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
