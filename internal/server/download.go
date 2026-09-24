package server

import (
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/besoeasy/originless/internal/ipfs"
)

type downloadHandler struct {
	client *ipfs.Client
}

const ipfsPathPrefix = "/ipfs/"

func (h *downloadHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.URL.Path, ipfsPathPrefix) {
		http.NotFound(w, r)
		return
	}
	prefix := ipfsPathPrefix
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{
			"error": "method not allowed",
		})
		return
	}

	rawCID := strings.TrimPrefix(r.URL.Path, prefix)
	cid, err := url.PathUnescape(rawCID)
	if err != nil || cid == "" || strings.ContainsAny(cid, "/?#") {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "CID not found"})
		return
	}

	resp, err := h.client.Cat(r.Context(), cid)
	if err != nil {
		log.Printf("IPFS download failed for %s: %v", cid, err)
		status := http.StatusBadGateway
		var ipfsErr *ipfs.StatusError
		if errors.As(err, &ipfsErr) && ipfsErr.StatusCode() == http.StatusNotFound {
			status = http.StatusNotFound
		}
		writeJSON(w, status, map[string]string{"error": "content unavailable"})
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("ETag", fmt.Sprintf("\"%s\"", cid))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if resp.ContentLength >= 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(resp.ContentLength, 10))
	}
	if disposition := mime.FormatMediaType("attachment", map[string]string{"filename": cid}); disposition != "" {
		w.Header().Set("Content-Disposition", disposition)
	}

	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}

	w.WriteHeader(http.StatusOK)
	if _, err := io.Copy(w, resp.Body); err != nil {
		log.Printf("stream IPFS download %s: %v", cid, err)
	}
}
