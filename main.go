package main

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/besoeasy/originless/modules"
	"github.com/besoeasy/originless/modules/p2p"
)

//go:embed static
var staticFS embed.FS

func uiFS() fs.FS {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err)
	}
	return sub
}

func main() {
	dataDir := "/data"
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		log.Fatalf("failed to create data directory: %v", err)
	}

	if err := os.MkdirAll(modules.BlobDir, 0o755); err != nil {
		log.Fatalf("failed to create blob directory: %v", err)
	}

	dbPath := filepath.Join(dataDir, "originless.db")
	database, err := modules.NewStore(dbPath)
	if err != nil {
		log.Fatalf("failed to open database: %v", err)
	}
	defer database.Close()

	janitorMgr := modules.NewJanitor(database)
	janitorMgr.SetDiskGuard(modules.NewDiskGuard(dataDir))
	log.Printf("[STARTUP] max blob size: %s (%d bytes)", modules.FormatBytes(modules.MaxBlobBytes), modules.MaxBlobBytes)

	log.Printf("[STARTUP] reconciling blob store...")
	if err := janitorMgr.ReconcileBlobs(); err != nil {
		log.Printf("[STARTUP] blob reconciliation skipped: %v", err)
	}

	workerCtx, workerCancel := context.WithCancel(context.Background())
	go janitorMgr.Run(workerCtx, time.Duration(modules.JanitorInterval)*time.Minute)

	p2pMgr := p2p.NewManager(database, modules.BlobDir, nil, modules.Port, dataDir)
	if p2pMgr != nil {
		p2pMgr.SetDiskGuard(janitorMgr.Guard())
	}
	router := modules.NewRouter(janitorMgr, uiFS(), p2pMgr)
	if p2pMgr != nil {
		p2pMgr.SetHTTPHandler(router)
		if err := p2pMgr.Start(); err != nil {
			log.Printf("[STARTUP] P2P initialization failed: %v", err)
			p2pMgr = nil
		}
	}

	// ReadTimeout/WriteTimeout are intentionally 0: full-body reads must
	// tolerate slow uploads (there is no upload size cap), and the SSE live
	// stream is long-lived (a fixed WriteTimeout would kill every stream at
	// 2 min — Go sets one absolute write deadline per request). Header reads
	// are still bounded by ReadHeaderTimeout and keep-alive idle by
	// IdleTimeout.
	server := &http.Server{
		Addr:              fmt.Sprintf("%s:%d", modules.Host, modules.Port),
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       0,
		WriteTimeout:      0,
		IdleTimeout:       120 * time.Second,
	}

	serveHTTP := p2pMgr == nil || !p2pMgr.OwnsListener()
	if serveHTTP {
		go func() {
			log.Printf("[STARTUP] SERVER_LISTENING host=%s port=%d url=http://%s:%d", modules.Host, modules.Port, modules.Host, modules.Port)
			if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Fatalf("server failed: %v", err)
			}
		}()
	} else {
		log.Printf("[STARTUP] SERVER_LISTENING host=%s port=%d url=http://%s:%d p2p=libp2p", modules.Host, modules.Port, modules.Host, modules.Port)
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	sig := <-stop
	log.Printf("[SHUTDOWN] SIGNAL_RECEIVED signal=%s action=graceful_shutdown", sig)

	workerCancel()

	if p2pMgr != nil {
		p2pMgr.Stop()
	}

	if serveHTTP {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			log.Printf("[SHUTDOWN] HTTP_SERVER_ERROR error=%v", err)
		} else {
			log.Printf("[SHUTDOWN] HTTP_SERVER_CLOSED")
		}
	}
}
