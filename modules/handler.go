package modules

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"strconv"
	"time"
)

type Handler struct {
	ipfs       *Client
	janitor    *Manager
	metrics    *Metrics
	examplesFS fs.FS
	semaphore  chan struct{}
	store      *Store
}

func NewHandler(ipfsClient *Client, janitorManager *Manager, metrics *Metrics, examplesFS fs.FS) *Handler {
	h := &Handler{
		ipfs:       ipfsClient,
		janitor:    janitorManager,
		metrics:    metrics,
		examplesFS: examplesFS,
		semaphore:  make(chan struct{}, MaxConcurrentOps),
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

func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	result := h.ipfs.CheckHealth(r.Context())

	if result.Healthy {
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "healthy",
			"peers":  result.Peers,
		})
		return
	}

	reason := result.Error
	if reason == "" {
		reason = "No peers connected"
	}

	writeJSON(w, http.StatusServiceUnavailable, map[string]any{
		"status": "unhealthy",
		"peers":  result.Peers,
		"reason": reason,
	})
}

func (h *Handler) Status(w http.ResponseWriter, r *http.Request) {
	stats, err := h.ipfs.GetStats(r.Context())
	if err != nil {
		log.Printf("Status check error: %v", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error":     "Failed to retrieve IPFS status",
			"details":   err.Error(),
			"status":    "failed",
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		})
		return
	}

	payload := map[string]any{
		"status":     "success",
		"timestamp":  time.Now().UTC().Format(time.RFC3339),
		"bandwidth":  stats.Bandwidth,
		"repository": stats.Repository,
		"node":       stats.Node,
		"peers":      stats.Peers,
		"storageLimit": map[string]any{
			"configured": StorageMax,
			"current":    FormatBytes(stats.Repository.StorageMax),
		},
		"fileLimit": map[string]any{
			"configured": FormatBytes(FileLimit),
			"bytes":      FileLimit,
		},
		"appVersion": AppVersion,
	}
	writeJSON(w, http.StatusOK, payload)
}

func (h *Handler) Upload(w http.ResponseWriter, r *http.Request) {
	select {
	case h.semaphore <- struct{}{}:
		defer func() { <-h.semaphore }()
	default:
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error":     "Server busy",
			"status":    "error",
			"message":   "Too many concurrent uploads, try again later",
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, FileLimit+1024*1024)

	saved, err := SaveMultipartFile(r, FileLimit)
	if err != nil {
		h.handleUploadError(w, err)
		return
	}
	defer RemovePath(saved.Path)

	mimeType := MimeType(saved.OriginalName)
	start := time.Now()
	log.Printf("Starting IPFS upload for %s ...", saved.OriginalName)

	cid, err := h.ipfs.AddFile(r.Context(), saved.Path, saved.OriginalName)
	if err != nil {
		log.Printf("IPFS upload error: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error":     "Failed to upload to IPFS",
			"details":   err.Error(),
			"status":    "error",
			"message":   "Failed to upload to IPFS",
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		})
		return
	}

	log.Printf("File uploaded successfully: name=%s size_bytes=%d mime_type=%s cid=%s upload_duration_ms=%d",
		saved.OriginalName, saved.Size, mimeType, cid, time.Since(start).Milliseconds())
	h.metrics.IncUpload(saved.Size)

	pinned := true
	if err := h.janitor.PinOnUpload(cid, saved.OriginalName, saved.Size); err != nil {
		log.Printf("Pin failed for %s: %v", cid, err)
		pinned = false
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":   "success",
		"cid":      cid,
		"size":     saved.Size,
		"type":     mimeType,
		"filename": saved.OriginalName,
		"pinned":   pinned,
	})
}


func (h *Handler) UploadFolder(w http.ResponseWriter, r *http.Request) {
	select {
	case h.semaphore <- struct{}{}:
		defer func() { <-h.semaphore }()
	default:
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error":     "Server busy",
			"status":    "error",
			"message":   "Too many concurrent uploads, try again later",
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, FileLimit+1024*1024)

	saved, err := SaveMultipartFiles(r, FileLimit)
	if err != nil {
		h.handleUploadError(w, err)
		return
	}
	defer RemoveAll(saved.Files)

	start := time.Now()
	log.Printf("Starting IPFS folder upload: %d files ...", saved.Count)

	cid, err := h.ipfs.AddDirectory(r.Context(), saved.Files)
	if err != nil {
		log.Printf("Folder upload error: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error":     "Failed to upload folder to IPFS",
			"details":   err.Error(),
			"status":    "error",
			"message":   "Failed to upload folder to IPFS",
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		})
		return
	}

	log.Printf("Folder uploaded successfully: files=%d size_bytes=%d cid=%s upload_duration_ms=%d",
		saved.Count, saved.Total, cid, time.Since(start).Milliseconds())
	h.metrics.IncUpload(saved.Total)

	pinned := true
	if err := h.janitor.PinOnUpload(cid, "folder", saved.Total); err != nil {
		log.Printf("Pin failed for folder %s: %v", cid, err)
		pinned = false
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status": "success",
		"cid":    cid,
		"files":  saved.Count,
		"size":   saved.Total,
		"pinned": pinned,
	})
}

func (h *Handler) handleUploadError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNoFile):
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error":     "No file uploaded",
			"status":    "error",
			"message":   err.Error(),
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		})
	case errors.Is(err, ErrFileTooLarge):
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{
			"error":   "File too large",
			"message": err.Error(),
			"maxSize": FormatBytes(FileLimit),
		})
	default:
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error":     err.Error(),
			"status":    "error",
			"message":   err.Error(),
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		})
	}
}

func (h *Handler) History(w http.ResponseWriter, r *http.Request) {
	limit := 50
	offset := 0

	if v := r.URL.Query().Get("limit"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 && parsed <= 100 {
			limit = parsed
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed >= 0 {
			offset = parsed
		}
	}

	uploads, err := h.janitor.GetHistory(limit, offset)
	if err != nil {
		log.Printf("History error: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error":   "Failed to fetch history",
			"details": err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "success",
		"uploads": uploads,
		"limit":   limit,
		"offset":  offset,
	})
}

func (h *Handler) PinStats(w http.ResponseWriter, r *http.Request) {
	count, size, err := h.janitor.GetStats()
	if err != nil {
		log.Printf("Pin stats error: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": "Failed to fetch pin stats",
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":        "success",
		"pinnedCount":   count,
		"pinnedSize":    size,
		"pinnedSizeStr": FormatBytes(size),
		"storageLimit":  StorageMax,
		"threshold":     PinThreshold,
	})
}

func (h *Handler) ExamplesList(w http.ResponseWriter, r *http.Request) {
	fsys := GetExamplesFS(h.examplesFS)
	tools, err := ScanExamples(fsys)
	if err != nil {
		log.Printf("Scan examples error: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error":   "Failed to scan examples",
			"details": err.Error(),
		})
		return
	}
	if tools == nil {
		tools = []ExampleTool{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "success",
		"tools":  tools,
		"count":  len(tools),
	})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
