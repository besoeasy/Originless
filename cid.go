package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
)

type cidHandler struct {
	client *ipfsClient
}

type blockStat struct {
	Key  string `json:"Key"`
	Size int64  `json:"Size"`
}

type objectStat struct {
	Hash           string `json:"Hash"`
	NumLinks       int64  `json:"NumLinks"`
	BlockSize      int64  `json:"BlockSize"`
	LinksSize      int64  `json:"LinksSize"`
	DataSize       int64  `json:"DataSize"`
	CumulativeSize int64  `json:"CumulativeSize"`
}

type lsLink struct {
	Name   string `json:"Name"`
	Hash   string `json:"Hash,omitempty"`
	Size   uint64 `json:"Size"`
	Type   int    `json:"Type"`
	Target string `json:"Target,omitempty"`
}

type lsObject struct {
	Hash  string   `json:"Hash"`
	Links []lsLink `json:"Links"`
}

type lsResponse struct {
	Objects []lsObject `json:"Objects"`
}

type cidResponse struct {
	CID       string          `json:"cid"`
	Available bool            `json:"available"`
	Block     *blockStat      `json:"block,omitempty"`
	Object    *objectStat     `json:"object,omitempty"`
	Links     []lsLink        `json:"links,omitempty"`
	Data      string          `json:"data,omitempty"`
	JSON      json.RawMessage `json:"json,omitempty"`
	Error     string          `json:"error,omitempty"`
}

func (h *cidHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{
			"error": "method not allowed",
		})
		return
	}

	cid := r.PathValue("cid")
	if cid == "" || strings.ContainsAny(cid, "/?#") {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "CID not found"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()

	response := cidResponse{CID: cid}

	block, err := h.client.blockStat(ctx, cid)
	if err != nil {
		var apiErr *ipfsAPIError
		if errors.As(err, &apiErr) {
			response.Available = false
			response.Error = "content unavailable on this node"
			log.Printf("IPFS block stat reported unavailable for %s: %v", cid, err)
			writeJSON(w, http.StatusOK, response)
			return
		}
		log.Printf("IPFS block stat failed for %s: %v", cid, err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "IPFS node unavailable",
		})
		return
	}
	response.Available = true
	response.Block = &block

	obj, err := h.client.objectStat(ctx, cid)
	if err != nil {
		log.Printf("IPFS object stat failed for %s: %v", cid, err)
		writeJSON(w, http.StatusOK, response)
		return
	}
	response.Object = &obj

	if obj.NumLinks > 0 {
		links, err := h.client.ls(ctx, cid)
		if err != nil {
			log.Printf("IPFS ls failed for %s: %v", cid, err)
		} else {
			response.Links = links
		}
	} else if obj.CumulativeSize <= maxAPIResponse {
		data, decoded, err := h.client.content(ctx, cid)
		if err != nil {
			log.Printf("IPFS cat failed for %s: %v", cid, err)
		} else {
			response.Data = base64.StdEncoding.EncodeToString(data)
			response.JSON = decoded
		}
	}

	writeJSON(w, http.StatusOK, response)
}

type ipfsAPIError struct {
	status  int
	message string
}

func (e *ipfsAPIError) Error() string {
	return e.message
}

func (c *ipfsClient) postIPFSJSON(ctx context.Context, endpointPath string, query url.Values, target any) error {
	endpointURL, err := url.Parse(c.endpoint(endpointPath))
	if err != nil {
		return fmt.Errorf("parse IPFS %s URL: %w", endpointPath, err)
	}
	endpointURL.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointURL.String(), http.NoBody)
	if err != nil {
		return fmt.Errorf("create IPFS %s request: %w", endpointPath, err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request IPFS %s: %w", endpointPath, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxAPIResponse))
	if err != nil {
		return fmt.Errorf("read IPFS %s response: %w", endpointPath, err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		message := strings.TrimSpace(string(body))
		if message == "" {
			message = resp.Status
		}
		return &ipfsAPIError{
			status:  resp.StatusCode,
			message: fmt.Sprintf("IPFS %s returned %s: %s", endpointPath, resp.Status, message),
		}
	}
	if target != nil {
		if err := json.Unmarshal(body, target); err != nil {
			return fmt.Errorf("decode IPFS %s response: %w", endpointPath, err)
		}
	}
	return nil
}

func (c *ipfsClient) blockStat(ctx context.Context, cid string) (blockStat, error) {
	var stat blockStat
	if err := c.postIPFSJSON(ctx, "api/v0/block/stat", url.Values{"arg": {cid}}, &stat); err != nil {
		return blockStat{}, err
	}
	return stat, nil
}

func (c *ipfsClient) objectStat(ctx context.Context, cid string) (objectStat, error) {
	var stat objectStat
	if err := c.postIPFSJSON(ctx, "api/v0/object/stat", url.Values{"arg": {cid}}, &stat); err != nil {
		return objectStat{}, err
	}
	return stat, nil
}

func (c *ipfsClient) ls(ctx context.Context, cid string) ([]lsLink, error) {
	var output lsResponse
	if err := c.postIPFSJSON(ctx, "api/v0/ls", url.Values{"arg": {cid}}, &output); err != nil {
		return nil, err
	}
	if len(output.Objects) > 0 {
		return output.Objects[0].Links, nil
	}
	return nil, nil
}

func (c *ipfsClient) content(ctx context.Context, cid string) ([]byte, json.RawMessage, error) {
	resp, err := c.cat(ctx, cid)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxAPIResponse))
	if err != nil {
		return nil, nil, err
	}
	var decoded json.RawMessage
	if err := json.Unmarshal(data, &decoded); err == nil {
		return data, decoded, nil
	}
	return data, nil, nil
}
