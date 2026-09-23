package modules

import (
	"io/fs"
	"net/http"
)

func NewRouter(janitorManager *Manager, uiFS fs.FS) http.Handler {
	metrics := NewMetrics()
	handler := NewHandler(janitorManager, metrics)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", handler.Status)
	mux.HandleFunc("POST /events", handler.PublishRecord)
	mux.HandleFunc("GET /events", handler.ListRecords)
	mux.HandleFunc("GET /events/stream", handler.StreamRecords)
	mux.HandleFunc("GET /events/{id}", handler.GetRecordByID)
	mux.HandleFunc("GET /blobs", handler.ListBlobs)
	mux.HandleFunc("GET /blob/{hash}", handler.Down)
	mux.HandleFunc("HEAD /blob/{hash}", handler.Down)
	mux.HandleFunc("GET /metrics", metrics.Handler(janitorManager, handler.broadcaster))

	mux.HandleFunc("GET /agent", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/agent.txt", http.StatusMovedPermanently)
	})

	mux.Handle("/", http.FileServer(http.FS(uiFS)))

	return metrics.Middleware(Chain(mux, CORS, Gzip))
}
