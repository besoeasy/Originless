package modules

import (
	"io/fs"
	"net/http"
)

func NewRouter(ipfsClient *Client, janitorManager *Manager, uiFS fs.FS) http.Handler {
	metrics := NewMetrics()
	handler := NewHandler(ipfsClient, janitorManager, metrics)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", handler.Health)
	mux.HandleFunc("GET /status", handler.Status)
	mux.HandleFunc("POST /upload", handler.Upload)
	mux.HandleFunc("POST /uploadfolder", handler.UploadFolder)
	mux.HandleFunc("GET /history", handler.History)
	mux.HandleFunc("GET /pins", handler.PinStats)
	mux.HandleFunc("POST /records", handler.PublishRecord)
	mux.HandleFunc("GET /records", handler.ListRecords)
	mux.HandleFunc("GET /records/stream", handler.StreamRecords)
	mux.HandleFunc("GET /records/{id}", handler.GetRecordByID)
	mux.HandleFunc("POST /up", handler.Up)
	mux.HandleFunc("GET /down/{hash}", handler.Down)
	mux.HandleFunc("HEAD /down/{hash}", handler.Down)
	mux.HandleFunc("GET /metrics", metrics.Handler(janitorManager, ipfsClient))

	mux.HandleFunc("GET /library.html", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/", http.StatusMovedPermanently)
	})

	mux.HandleFunc("GET /agent", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/agent.html", http.StatusMovedPermanently)
	})

	mux.Handle("/", http.FileServer(http.FS(uiFS)))

	return metrics.Middleware(Chain(mux, CORS, Gzip))
}
