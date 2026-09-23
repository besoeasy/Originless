package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type downloadHandler struct {
	client *ipfsClient
}

func (h *downloadHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	const prefix = "/down/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		http.NotFound(w, r)
		return
	}
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

	resp, err := h.client.cat(r.Context(), cid)
	if err != nil {
		log.Printf("IPFS download failed for %s: %v", cid, err)
		status := http.StatusBadGateway
		var ipfsErr *ipfsDownloadError
		if errors.As(err, &ipfsErr) && ipfsErr.status == http.StatusNotFound {
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

type ipfsDownloadError struct {
	status  int
	message string
}

func (e *ipfsDownloadError) Error() string {
	return e.message
}

func (c *ipfsClient) cat(ctx context.Context, cid string) (*http.Response, error) {
	endpointURL, err := url.Parse(c.endpoint("api/v0/cat"))
	if err != nil {
		return nil, fmt.Errorf("parse IPFS cat URL: %w", err)
	}
	query := endpointURL.Query()
	query.Set("arg", cid)
	query.Set("offline", "true")
	query.Set("progress", "false")
	endpointURL.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointURL.String(), http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("create IPFS cat request: %w", err)
	}
	req.Header.Set("Accept", "application/octet-stream")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request IPFS cat: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxAPIResponse))
		message := strings.TrimSpace(string(body))
		if message == "" {
			message = resp.Status
		}
		return nil, &ipfsDownloadError{
			status:  resp.StatusCode,
			message: fmt.Sprintf("IPFS cat returned %s: %s", resp.Status, message),
		}
	}
	return resp, nil
}
