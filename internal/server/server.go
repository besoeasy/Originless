// Package server wires the HTTP routes served by Originless.
package server

import (
	_ "embed"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/besoeasy/originless/internal/events"
	"github.com/besoeasy/originless/internal/ipfs"
)

const requestTimeout = 5 * time.Second

//go:embed static/index.html
var indexHTML []byte

func serveIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" && r.URL.Path != "/index.html" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.Method == http.MethodHead {
		return
	}
	if _, err := w.Write(indexHTML); err != nil {
		log.Printf("write home page: %v", err)
	}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("encode JSON response: %v", err)
	}
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS, HEAD")
		w.Header().Set("Access-Control-Allow-Headers", "*")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func NewRouter(client *ipfs.Client) http.Handler {
	mux := http.NewServeMux()
	store := events.NewStore()
	mux.HandleFunc("/", serveIndex)
	mux.Handle("/stats", &statsHandler{client: client, events: store})
	mux.Handle("/up", &uploadHandler{client: client})
	mux.Handle("/upf", &uploadHandler{client: client, folder: true})
	mux.Handle("/ipfs/", &downloadHandler{client: client})
	mux.Handle("/cid/{cid}", &cidHandler{client: client})
	mux.Handle("/events", events.NewHandler(store))
	mux.Handle("/events/", events.NewHandler(store))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	return corsMiddleware(mux)
}
