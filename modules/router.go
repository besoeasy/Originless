package modules

import (
	"io/fs"
	"net/http"
)

func NewRouter(ipfsClient *Client, janitorManager *Manager, uiFS fs.FS, examplesFS fs.FS) http.Handler {
	metrics := NewMetrics()
	handler := NewHandler(ipfsClient, janitorManager, metrics, examplesFS)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", handler.Health)
	mux.HandleFunc("GET /status", handler.Status)
	mux.HandleFunc("POST /upload", handler.Upload)
	mux.HandleFunc("POST /media", handler.Media)
	mux.HandleFunc("POST /uploadfolder", handler.UploadFolder)
	mux.HandleFunc("GET /history", handler.History)
	mux.HandleFunc("GET /pins", handler.PinStats)
	mux.HandleFunc("POST /records", handler.PublishRecord)
	mux.HandleFunc("GET /records", handler.ListRecords)
	mux.HandleFunc("GET /records/{id}", handler.GetRecordByID)
	mux.HandleFunc("POST /up", handler.Up)
	mux.HandleFunc("GET /down/{hash}", handler.Down)
	mux.HandleFunc("HEAD /down/{hash}", handler.Down)
	mux.HandleFunc("GET /metrics", metrics.Handler(janitorManager, ipfsClient))

	// Path-gateway requests at /ipfs and /ipns are dispatched by the wrapper
	// below (isGatewayRequest) so they bypass CORS/Gzip — Kubo emits its own
	// headers for gateway responses. Everything else goes through the mux.

	// Dynamic examples endpoint & routes
	mux.HandleFunc("GET /api/examples", handler.ExamplesList)
	mux.HandleFunc("GET /examples/manifest.json", handler.ExamplesList)

	// Direct serving of examples pages (live disk fallback when running locally)
	mux.HandleFunc("GET /examples", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/examples/", http.StatusMovedPermanently)
	})
	mux.HandleFunc("/examples/", func(w http.ResponseWriter, r *http.Request) {
		fsys := GetExamplesFS(examplesFS)
		http.StripPrefix("/examples/", http.FileServer(http.FS(fsys))).ServeHTTP(w, r)
	})

	mux.HandleFunc("GET /library.html", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/", http.StatusMovedPermanently)
	})

	mux.HandleFunc("GET /agent", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/agent.html", http.StatusMovedPermanently)
	})

	mux.Handle("/", http.FileServer(http.FS(uiFS)))

	// CORS and Gzip Set headers before the handler runs. ReverseProxy then
	// Add's Kubo's copies, so those middlewares must not wrap /ipfs, /ipns,
	// or {cid}.ipfs.* hosts. Metrics still wraps everything (no response headers).
	api := Chain(mux, CORS, Gzip)
	return metrics.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isGatewayRequest(r) {
			handler.Gateway(w, r)
			return
		}
		api.ServeHTTP(w, r)
	}))
}
