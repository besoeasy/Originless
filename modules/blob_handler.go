package modules

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// Up stores a .bin upload as <sha256>.bin (content-addressed, deduped).
// POST /up with multipart field "file" (filename must end in .bin).
func (h *Handler) Up(w http.ResponseWriter, r *http.Request) {
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
	// Best-effort quota pre-check: reject obviously-over-quota uploads
	// before staging bytes to disk. Duplicates (same hash) use no extra
	// space, so the check is re-evaluated post-hash below.
	quota := StorageMaxBytes
	if h.janitor != nil {
		quota = h.janitor.StorageLimit()
	}
	if r.ContentLength > 0 {
		if total, err := st.GetBlobSize(); err == nil && total+r.ContentLength > quota {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{
				"status": "error", "error": "storage quota exceeded",
				"limit": quota, "used": total,
			})
			return
		}
	}
	if err := EnsureBlobDir(BlobDir); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"status": "error", "error": "blob storage unavailable",
		})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, FileLimit+1024*1024)
	reader, err := r.MultipartReader()
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"status": "error", "error": "expected multipart/form-data with field \"file\"",
		})
		return
	}

	var partName string
	var partReader io.Reader
	var partCloser io.Closer
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"status": "error", "error": err.Error()})
			return
		}
		if part.FormName() != "file" {
			part.Close()
			continue
		}
		partName = part.FileName()
		partReader = part
		partCloser = part
		break
	}
	if partReader == nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"status": "error", "error": "missing \"file\" part",
		})
		return
	}
	defer partCloser.Close()

	if !IsBinFile(partName) {
		if c, ok := partCloser.(interface{ Close() error }); ok {
			_ = c
		}
		writeJSON(w, http.StatusUnsupportedMediaType, map[string]any{
			"status": "error", "error": "only .bin files accepted",
		})
		return
	}

	// Extension lies: renamed PNGs/HTML/text sail through IsBinFile, so
	// sniff the head bytes and refuse renderable or textual payloads.
	// (Empty bodies skip the sniff and hit the empty-file check below —
	// DetectContentType reports "" as text/plain.)
	var head [512]byte
	headLen, _ := io.ReadFull(partReader, head[:])
	headBytes := head[:headLen]
	if len(headBytes) > 0 {
		if ctype := http.DetectContentType(headBytes); isBlockedContentType(ctype) {
			writeJSON(w, http.StatusUnsupportedMediaType, map[string]any{
				"status": "error", "error": "non-binary content rejected", "detected": ctype,
			})
			return
		}
	}

	tmp, err := os.CreateTemp(BlobDir, "up-*")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"status": "error", "error": "cannot stage upload"})
		return
	}
	tmpName := tmp.Name()
	defer func() {
		tmp.Close()
		// Best-effort cleanup: if rename succeeded the temp name is gone.
		os.Remove(tmpName)
	}()

	hasher := sha256.New()
	// Replays the sniffed head so no byte is lost or double-counted.
	stream := io.MultiReader(bytes.NewReader(headBytes), partReader)
	limited := io.LimitReader(stream, FileLimit+1)
	written, err := io.Copy(tmp, io.TeeReader(limited, hasher))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"status": "error", "error": err.Error()})
		return
	}
	if written > FileLimit {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{
			"status": "error", "error": "file too large", "maxSize": FormatBytes(FileLimit),
		})
		return
	}
	if written == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"status": "error", "error": "empty file"})
		return
	}
	if err := tmp.Close(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"status": "error", "error": "cannot store upload"})
		return
	}

	sum := hasher.Sum(nil)
	hash := hex.EncodeToString(sum)
	dest := BlobPath(BlobDir, hash)

	// Post-hash quota enforcement (covers chunked uploads with unknown
	// ContentLength). Deduped bytes use no new space, so existing dests
	// always pass. Uses a fresh SUM so the hard cap is never bypassed by
	// the cached storage total.
	if _, err := os.Stat(dest); os.IsNotExist(err) {
		if total, err := st.GetBlobSizeFresh(); err == nil && total+written > quota {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{
				"status": "error", "error": "storage quota exceeded",
				"limit": quota, "used": total,
			})
			return
		}
	}

	duplicate := false
	if _, err := os.Stat(dest); err == nil {
		duplicate = true
		if _, err := st.UpsertBlob(hash, written); err != nil {
			log.Printf("[blob] touch failed for %s: %v", hash, err)
		}
	} else {
		// Atomic publish: temp (0600) -> dest (0644, hashed name is the address).
		if err := os.Chmod(tmpName, 0o644); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"status": "error", "error": "cannot store upload"})
			return
		}
		if err := os.Rename(tmpName, dest); err != nil {
			// Lost a rename race with an identical concurrent upload.
			if _, statErr := os.Stat(dest); statErr == nil {
				duplicate = true
			} else {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"status": "error", "error": "cannot store upload"})
				return
			}
		}
		if _, err := st.UpsertBlob(hash, written); err != nil {
			log.Printf("[blob] db insert failed for %s: %v", hash, err)
		}
	}

	h.metrics.IncUpload(written)

	// Best-effort LRU pressure relief (never fails the upload itself).
	if h.janitor != nil {
		if err := h.janitor.CheckBlobThreshold(); err != nil {
			log.Printf("[blob] threshold eviction error: %v", err)
		}
	}

	status := http.StatusCreated
	if duplicate {
		status = http.StatusOK
	}
	writeJSON(w, status, map[string]any{
		"status":    "success",
		"hash":      hash,
		"size":      written,
		"url":       "/down/" + hash,
		"duplicate": duplicate,
	})
}

// Down serves a stored blob by sha256.
// GET /down/{hash} (HEAD also allowed). Touches LRU on every hit.
func (h *Handler) Down(w http.ResponseWriter, r *http.Request) {
	st := h.recordStore()
	if st == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"status": "error", "error": "store unavailable",
		})
		return
	}
	raw := r.PathValue("hash")
	hash, err := NormalizeBlobHash(raw)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"status": "error", "error": err.Error()})
		return
	}
	meta, err := st.GetBlob(hash)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"status": "error", "error": "not found"})
		return
	}
	path := BlobPath(BlobDir, hash)
	info, err := os.Stat(path)
	if err != nil {
		// DB row without bytes: drop the stale row so reconcile stays clean.
		_ = st.DeleteBlob(hash)
		writeJSON(w, http.StatusNotFound, map[string]any{"status": "error", "error": "not found"})
		return
	}
	if info.Size() != meta.Size {
		log.Printf("[blob] size mismatch for %s: db=%d fs=%d", hash, meta.Size, info.Size())
	}

	// LRU touch before serving (failure is non-fatal).
	if err := st.TouchBlob(hash); err != nil {
		log.Printf("[blob] touch failed for %s: %v", hash, err)
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `inline; filename="`+filepath.Base(BlobFileName(hash))+`"`)
	w.Header().Set("ETag", `"`+hash+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "public, max-age=86400, immutable")
	http.ServeFile(w, r, path)
}

// ListBlobs handles GET /blobs?limit=50&offset=0
func (h *Handler) ListBlobs(w http.ResponseWriter, r *http.Request) {
	st := h.recordStore()
	if st == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "error", "error": "store unavailable"})
		return
	}
	limit := 50
	offset := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		if p, err := strconv.Atoi(v); err == nil && p > 0 && p <= 100 {
			limit = p
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if p, err := strconv.Atoi(v); err == nil && p >= 0 {
			offset = p
		}
	}
	blobs, err := st.ListBlobs(limit, offset)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"status": "error", "error": err.Error()})
		return
	}
	count, _ := st.GetBlobCount()
	totalBytes, _ := st.GetBlobSize()
	writeJSON(w, http.StatusOK, map[string]any{
		"status":          "success",
		"blobs":           blobs,
		"count":           count,
		"total_bytes":     totalBytes,
		"total_bytes_str": FormatBytes(totalBytes),
		"limit":           limit,
		"offset":          offset,
	})
}
