package modules

import (
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// PublishRecord stores a verified signed event. The write path is a single
// endpoint: a JSON body publishes an event alone, while a multipart body
// (parts "event" + "blob") atomically publishes the event together with the
// bytes it references via the reserved data.blob key. Uploading and linking
// are one request, so a blob is always born owned by a signed event.
func (h *Handler) PublishRecord(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		h.publishEventMultipart(w, r)
		return
	}
	h.publishEventJSON(w, r)
}

// publishEventJSON handles blob-less (or link-only) signed event publishes.
func (h *Handler) publishEventJSON(w http.ResponseWriter, r *http.Request) {
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

	// Linked blob must already be stored (link-only mode): upload the
	// bytes in the same multipart request, or publish the event that
	// claims an existing blob.
	if rec.Blob != "" {
		if _, err := st.GetBlob(rec.Blob); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"status": "error", "error": "referenced blob not found", "hash": rec.Blob,
			})
			return
		}
	}

	h.finishPublish(w, st, rec)
}

// errValidated marks an event part whose validation response was already
// written inline (fail-fast path); the part loop just stops.
var errValidated = errors.New("event validation already answered")

// publishEventMultipart handles the combined blob + event publish. The
// "event" part is the signed JSON (same schema as the JSON path); the
// optional "blob" part streams the raw bytes. The server re-hashes the
// bytes and refuses a mismatch with the signed data.blob, so the client
// must compute sha256 before signing.
func (h *Handler) publishEventMultipart(w http.ResponseWriter, r *http.Request) {
	select {
	case h.semaphore <- struct{}{}:
		defer func() { <-h.semaphore }()
	default:
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": "Server busy", "status": "error",
			"message":   "Too many concurrent uploads, try again later",
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		})
		return
	}

	st := h.recordStore()
	if st == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"status": "error", "error": "store unavailable",
		})
		return
	}
	if err := EnsureBlobDir(BlobDir); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"status": "error", "error": "blob storage unavailable",
		})
		return
	}

	reader, err := r.MultipartReader()
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"status": "error", "error": "expected multipart/form-data with part \"event\" and optional part \"blob\"",
		})
		return
	}

	var rec *Record
	var eventValidated bool
	var overflow bool
	var stageName string
	var stagedSize int64
	var stagedDigest [32]byte
	var blobSeen bool
	nowUnix := time.Now().Unix()
	defer func() {
		// Best-effort cleanup: committed blobs already renamed the temp away.
		if stageName != "" {
			os.Remove(stageName)
		}
	}()

	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"status": "error", "error": err.Error()})
			return
		}
		err = func() error {
			defer part.Close()
			switch part.FormName() {
			case "event":
				if overflow {
					return nil
				}
				raw, err := io.ReadAll(io.LimitReader(part, MaxRecordSize+1024+1))
				if err != nil {
					return fmt.Errorf("cannot read event part: %w", err)
				}
				if len(raw) > MaxRecordSize+1024 {
					overflow = true
					return nil
				}
				// Fail fast: a forged event must not cost a blob staging.
				// Bytes arriving before a valid event are refused outright.
				v, verr := ValidateRecordBody(raw, nowUnix)
				if verr != nil {
					status := http.StatusBadRequest
					msg := verr.Error()
					switch {
					case contains(msg, "too large"):
						status = http.StatusRequestEntityTooLarge
					case contains(msg, "bad sig"):
						status = http.StatusUnauthorized
					}
					writeJSON(w, status, map[string]any{"status": "error", "error": msg})
					return errValidated
				}
				rec = v
				eventValidated = true
				return nil
			case "blob":
				if !eventValidated {
					return fmt.Errorf("event part must come first")
				}
				if blobSeen {
					return fmt.Errorf("duplicate blob part")
				}
				blobSeen = true
				tmp, size, digest, err := stageBlobData(BlobDir, part, h.diskGuard())
				if err != nil {
					return err
				}
				stageName = tmp
				stagedSize = size
				stagedDigest = digest
				return nil
			default:
				return nil
			}
		}()
		if err != nil {
			if errors.Is(err, errValidated) {
				return // response already written
			}
			var bc *errBlockedContent
			switch {
			case errors.As(err, &bc):
				writeJSON(w, http.StatusUnsupportedMediaType, map[string]any{
					"status": "error", "error": "non-binary content rejected", "detected": bc.detected,
				})
			case errors.Is(err, errEmptyBlob):
				writeJSON(w, http.StatusBadRequest, map[string]any{"status": "error", "error": "empty file"})
			case errors.Is(err, errDiskCapExceeded):
				writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{
					"status": "error", "error": "blob too large", "maxSize": MaxBlobBytes,
				})
			case IsStorageExhausted(err):
				// The part stream is consumed; the client retries and the
				// freed space is waiting.
				stats := h.emergencyPass(0)
				writeInsufficientStorage(w, stats, "")
			default:
				writeJSON(w, http.StatusBadRequest, map[string]any{"status": "error", "error": err.Error()})
			}
			return
		}
	}

	if overflow {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{
			"status": "error", "error": "record too large", "maxSize": MaxRecordSize,
		})
		return
	}
	if !eventValidated {
		writeJSON(w, http.StatusBadRequest, map[string]any{"status": "error", "error": "missing event part"})
		return
	}

	blobHash := rec.Blob
	stagedHex := hex.EncodeToString(stagedDigest[:])

	switch {
	case blobHash == "" && blobSeen:
		// The hash must be signed before the bytes arrive; the server
		// cannot invent it after the fact.
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"status": "error", "error": "blob part requires data.blob in the event",
		})
		return
	case blobHash != "" && blobSeen:
		if blobHash != stagedHex {
			// Bytes present and signed hash disagree: refuse to store the
			// data under a false address.
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"status": "error", "error": "blob hash mismatch", "claimed": blobHash, "actual": stagedHex,
			})
			return
		}
		if _, err := commitBlob(BlobDir, st, stageName, stagedSize, stagedDigest); err != nil {
			if IsStorageExhausted(err) {
				// Rename is space-free; only the accounting row can fail
				// here, and its retry is idempotent (dest-exists → dupe).
				stats := h.emergencyPass(stagedSize)
				if _, rerr := commitBlob(BlobDir, st, stageName, stagedSize, stagedDigest); rerr != nil {
					if IsStorageExhausted(rerr) {
						log.Printf("[events] blob commit failed after eviction: %v", rerr)
						writeInsufficientStorage(w, stats, "")
						return
					}
					log.Printf("[events] blob commit failed: %v", rerr)
					writeJSON(w, http.StatusInternalServerError, map[string]any{"status": "error", "error": "cannot store blob"})
					return
				}
			} else {
				log.Printf("[events] blob commit failed: %v", err)
				writeJSON(w, http.StatusInternalServerError, map[string]any{"status": "error", "error": "cannot store blob"})
				return
			}
		}
		stageName = "" // consumed by commit
	case blobHash != "":
		// Link-only against an existing blob (dedupe: many events, one blob).
		if _, err := st.GetBlob(blobHash); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"status": "error", "error": "referenced blob not found", "hash": blobHash,
			})
			return
		}
	}

	created, storedAt, err := h.insertAndBroadcast(w, st, rec)
	if err != nil || !created {
		return
	}
	if blobHash != "" && blobSeen {
		h.metrics.IncUpload(stagedSize)
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"status": "success", "id": rec.ID, "stored_at": storedAt,
	})
}

// errDiskAdmit marks an admission refusal (ceiling would break).
var errDiskAdmit = errors.New("disk ceiling would be exceeded")

// writeInsufficientStorage answers 507 with what the emergency pass freed.
func writeInsufficientStorage(w http.ResponseWriter, stats EmergencyEvictStats, detail string) {
	payload := map[string]any{"status": "error", "error": "insufficient storage"}
	if detail != "" {
		payload["detail"] = detail
	}
	if !stats.At.IsZero() {
		payload["evicted_items"] = stats.Items
		payload["freed_bytes"] = stats.FreedBytes
		payload["evict_tier"] = stats.Tier
	}
	writeJSON(w, http.StatusInsufficientStorage, payload)
}

// emergencyPass runs one janitor emergency pass targeting wantBytes newly
// free (at least the emergency free-space target). Shared by every
// write path; cooldown + caps live in EmergencyEvict.
func (h *Handler) emergencyPass(wantBytes int64) EmergencyEvictStats {
	var zero EmergencyEvictStats
	if h.janitor == nil {
		return zero
	}
	target := wantBytes
	st, err := StatDisk(BlobDir)
	if err == nil {
		if t := TargetFreeBytes(st); t > target {
			target = t
		}
		var freeFn func() int64
		freeFn = func() int64 {
			s, e := StatDisk(BlobDir)
			if e != nil {
				return 0
			}
			return s.FreeBytes
		}
		stats, _ := h.janitor.EmergencyEvict(target, EmergencyMaxItems, EmergencyIncludeLive, freeFn)
		if h.metrics != nil && stats.Items > 0 {
			h.metrics.IncEmergency(stats.FreedBytes)
		}
		return stats
	}
	stats, _ := h.janitor.EmergencyEvict(target, EmergencyMaxItems, EmergencyIncludeLive, nil)
	if h.metrics != nil && stats.Items > 0 {
		h.metrics.IncEmergency(stats.FreedBytes)
	}
	return stats
}

// storeRecord persists one validated event behind admission control:
// reserve → soft-mark sweep → insert; on storage-exhausted failure run one
// emergency pass and retry the insert once (transactional rollback makes
// the retry safe). Reservations release on return.
func (h *Handler) storeRecord(st *Store, rec *Record) (created bool, storedAt string, evStats EmergencyEvictStats, err error) {
	g := h.diskGuard()
	want := rec.Size
	if want <= 0 {
		want = 512
	}
	if g != nil {
		h.janitor.MaybeEarlySweep(g)
		if !g.Admit(want) {
			evStats = h.emergencyPass(want)
			if !g.Admit(want) {
				return false, "", evStats, errDiskAdmit
			}
		}
		defer g.Release(want)
	}
	created, storedAt, err = st.InsertRecord(rec)
	if err != nil && IsStorageExhausted(err) {
		evStats = h.emergencyPass(want)
		created, storedAt, err = st.InsertRecord(rec)
	}
	return created, storedAt, evStats, err
}

// insertAndBroadcast stores a record and fans it out once (only the created
// side broadcasts; a duplicate replay must not re-broadcast). Writes the
// duplicate (200) response itself and reports created + storedAt.
func (h *Handler) insertAndBroadcast(w http.ResponseWriter, st *Store, rec *Record) (created bool, storedAt string, err error) {
	created, storedAt, evStats, err := h.storeRecord(st, rec)
	if err != nil {
		if errors.Is(err, errDiskAdmit) || IsStorageExhausted(err) {
			writeInsufficientStorage(w, evStats, "")
			return false, "", err
		}
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"status": "error", "error": "failed to store record",
		})
		return false, "", err
	}
	if !created {
		existing, derr := st.GetRecord(rec.ID)
		if derr != nil {
			writeJSON(w, http.StatusOK, map[string]any{
				"status": "success", "id": rec.ID, "duplicate": true,
			})
			return false, "", nil
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "success", "id": existing.ID, "stored_at": existing.StoredAt, "duplicate": true,
		})
		return false, "", nil
	}
	if h.broadcaster != nil {
		h.broadcaster.Broadcast(rec)
	}
	return true, storedAt, nil
}

// finishPublish stores a JSON-path record and writes the 201 response.
func (h *Handler) finishPublish(w http.ResponseWriter, st *Store, rec *Record) {
	created, storedAt, err := h.insertAndBroadcast(w, st, rec)
	if err != nil || !created {
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"status": "success", "id": rec.ID, "stored_at": storedAt,
	})
}

// ListRecords: GET /events?owner=&collection=&label=&since=&until=&search=&blob=&limit=&cursor=&include_expired=
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
	if v := q.Get("blob"); v != "" {
		h, err := NormalizeBlobHash(v)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"status": "error", "error": "invalid blob: " + err.Error()})
			return
		}
		f.Blob = h
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
		"status": "success", "events": records, "records": records,
		"limit": limit, "cursor": q.Get("cursor"), "next_cursor": next,
	})
}

// GetRecordByID: GET /events/{id}
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
		if rec.Blob != "" {
			if meta, err := st.GetBlob(rec.Blob); err == nil {
				writeJSON(w, http.StatusOK, map[string]any{
					"status": "success",
					"event":  rec,
					"record": rec,
					"blob": map[string]any{
						"hash":           meta.Hash,
						"size":           meta.Size,
						"sizeStr":        FormatBytes(meta.Size),
						"url":            "/blob/" + meta.Hash,
						"retained_until": meta.RetainedUntil.UTC().Format(time.RFC3339),
						"protected":      meta.Protected,
					},
				})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"status": "success", "event": rec, "record": rec, "blob": nil,
			})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "success", "event": rec, "record": rec})
}

// sseKeepaliveInterval and sseWriteTimeout bound long-lived streams.
// The keepalive interval is a var (not const) so tests can shorten it.
var sseKeepaliveInterval = 15 * time.Second

// sseWriteTimeout is the per-frame write deadline: with the server-wide
// WriteTimeout disabled, a client that stops reading would block the
// handler goroutine forever. A short deadline on each write (reset after
// success) turns that into a bounded disconnect.
const sseWriteTimeout = 5 * time.Second

// StreamRecords streams newly published records matching query filters over Server-Sent Events (SSE).
// GET /events/stream?owner=&collection=&label=&search=&blob=
func (h *Handler) StreamRecords(w http.ResponseWriter, r *http.Request) {
	if _, ok := w.(http.Flusher); !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	q := r.URL.Query()
	sub := &RecordSubscriber{
		Ch:         make(chan *Record, 64),
		Owner:      q.Get("owner"),
		Collection: q.Get("collection"),
		Label:      q.Get("label"),
		Search:     q.Get("search"),
	}
	if v := q.Get("blob"); v != "" {
		h, err := NormalizeBlobHash(v)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"status": "error", "error": "invalid blob: " + err.Error()})
			return
		}
		sub.Blob = h
	}

	if h.broadcaster != nil {
		if !h.broadcaster.TrySubscribe(sub) {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"status": "error", "error": "too many stream subscribers",
			})
			return
		}
		defer h.broadcaster.Unsubscribe(sub)
	}

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	rc := http.NewResponseController(w)
	deadlineErrOnce := sync.Once{}
	var writeFailed bool
	writeFrame := func(b []byte) {
		if writeFailed {
			return
		}
		if err := rc.SetWriteDeadline(time.Now().Add(sseWriteTimeout)); err != nil {
			deadlineErrOnce.Do(func() { log.Printf("sse: SetWriteDeadline unsupported: %v", err) })
		}
		// The socket write for a streamed response happens on Flush, not on
		// Write (which only fills net/http's internal bufio), so the
		// deadline must stay active across Flush and the Flush error must
		// be observed. ResponseController.Flush reports conn-level errors
		// that http.Flusher silently swallows.
		_, werr := w.Write(b)
		var ferr error
		if werr == nil {
			ferr = rc.Flush()
		}
		_ = rc.SetWriteDeadline(time.Time{})
		if werr != nil || ferr != nil {
			// Peer gone or stuck un-acked for sseWriteTimeout: stop the
			// stream instead of leaking the goroutine and connection.
			writeFailed = true
		}
	}

	ticker := time.NewTicker(sseKeepaliveInterval)
	defer ticker.Stop()

	writeFrame([]byte(": connected\n\n"))

	ctx := r.Context()
	for {
		if writeFailed {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			writeFrame([]byte(": keepalive\n\n"))
		case rec := <-sub.Ch:
			sse, err := FormatSSE(rec)
			if err != nil {
				continue
			}
			writeFrame(sse)
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
