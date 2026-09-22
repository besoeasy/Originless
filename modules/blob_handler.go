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
)

// errBlockedContent signals an upload whose head bytes sniffed as
// renderable or textual content (mapped to 415 by the caller).
type errBlockedContent struct{ detected string }

func (e *errBlockedContent) Error() string {
	return "non-binary content rejected"
}

// errEmptyBlob signals an upload that contained zero bytes (mapped to 400).
var errEmptyBlob = errors.New("empty file")

// stageBlobData streams opaque binary bytes to a temp file while hashing
// them with SHA-256, applying the same abuse-immunity guard the old /up
// used: renderable or textual payloads (text/*, image/*, application/pdf)
// are refused even though there is no filename to trust. Returns the temp
// path, byte count and hash; callers either commit via commitBlob or remove
// the temp.
func stageBlobData(dir string, r io.Reader) (tmpName string, size int64, h [32]byte, err error) {
	// Sniff the head before trusting the bytes. Empty bodies skip the
	// sniff (DetectContentType reports "" as text/plain) and fall through
	// to the empty-file check.
	var head [512]byte
	headLen, _ := io.ReadFull(r, head[:])
	headBytes := head[:headLen]
	if len(headBytes) > 0 {
		if ctype := http.DetectContentType(headBytes); isBlockedContentType(ctype) {
			return "", 0, [32]byte{}, &errBlockedContent{detected: ctype}
		}
	}

	tmp, err := os.CreateTemp(dir, "up-*")
	if err != nil {
		return "", 0, [32]byte{}, err
	}
	tmpName = tmp.Name()
	abort := func() {
		tmp.Close()
		os.Remove(tmpName)
	}
	hasher := sha256.New()
	// Replays the sniffed head so no byte is lost or double-counted.
	stream := io.MultiReader(bytes.NewReader(headBytes), r)
	written, err := io.Copy(tmp, io.TeeReader(stream, hasher))
	if err != nil {
		abort()
		return "", 0, [32]byte{}, err
	}
	if written == 0 {
		abort()
		return "", 0, [32]byte{}, errEmptyBlob
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return "", 0, [32]byte{}, err
	}
	sum := hasher.Sum(nil)
	var digest [32]byte
	copy(digest[:], sum)
	return tmpName, written, digest, nil
}

// commitBlob atomically publishes a staged temp file under its content
// address (<sha256>.bin) and upserts the accounting row. Identical bytes
// already on disk collapse to a dedupe touch (duplicate=true).
func commitBlob(dir string, st *Store, tmpName string, size int64, digest [32]byte) (duplicate bool, err error) {
	hash := hex.EncodeToString(digest[:])
	dest := BlobPath(dir, hash)
	duplicate = false
	if _, err := os.Stat(dest); err == nil {
		os.Remove(tmpName)
		duplicate = true
	} else if err := os.Chmod(tmpName, 0o644); err != nil {
		return false, err
	} else if err := os.Rename(tmpName, dest); err != nil {
		// Lost a rename race with an identical concurrent upload.
		if _, statErr := os.Stat(dest); statErr == nil {
			os.Remove(tmpName)
			duplicate = true
		} else {
			return false, err
		}
	}
	if _, err := st.UpsertBlob(hash, size); err != nil {
		return false, err
	}
	return duplicate, nil
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
