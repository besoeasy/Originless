package modules

import (
	"io/fs"
	"net/http"
)

func NewRouter(janitorManager *Manager, uiFS fs.FS, p2pBroadcaster ...P2PBroadcaster) http.Handler {
	metrics := NewMetrics()
	handler := NewHandler(janitorManager, metrics)
	if len(p2pBroadcaster) > 0 && p2pBroadcaster[0] != nil {
		handler.SetP2P(p2pBroadcaster[0])
		if setter, ok := p2pBroadcaster[0].(interface{ SetBroadcaster(*RecordBroadcaster) }); ok {
			setter.SetBroadcaster(handler.broadcaster)
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", handler.Status)
	mux.HandleFunc("POST /events", handler.PublishRecord)
	mux.HandleFunc("GET /events", handler.ListRecords)
	mux.HandleFunc("GET /events/stream", handler.StreamRecords)
	mux.HandleFunc("GET /events/{id}", handler.GetRecordByID)
	mux.HandleFunc("POST /records", handler.PublishRecord)
	mux.HandleFunc("GET /records", handler.ListRecords)
	mux.HandleFunc("GET /records/stream", handler.StreamRecords)
	mux.HandleFunc("GET /records/{id}", handler.GetRecordByID)
	mux.HandleFunc("POST /up", handler.Up)
	mux.HandleFunc("GET /blobs", handler.ListBlobs)
	mux.HandleFunc("GET /down/{hash}", handler.Down)
	mux.HandleFunc("HEAD /down/{hash}", handler.Down)
	mux.HandleFunc("GET /metrics", metrics.Handler(janitorManager, handler.broadcaster))

	if len(p2pBroadcaster) > 0 && p2pBroadcaster[0] != nil {
		if h, ok := p2pBroadcaster[0].(http.Handler); ok {
			mux.Handle("GET /p2p", h)
		}
	}

	mux.HandleFunc("GET /library.html", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/", http.StatusMovedPermanently)
	})

	mux.HandleFunc("GET /agent", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/agent.txt", http.StatusMovedPermanently)
	})

	mux.HandleFunc("GET /agent.html", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/agent.txt", http.StatusMovedPermanently)
	})

	mux.Handle("/", http.FileServer(http.FS(uiFS)))

	return metrics.Middleware(Chain(mux, CORS, Gzip))
}
