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

	janitorMgr := modules.NewJanitor(database, modules.StorageMaxBytes)

	log.Printf("[STARTUP] reconciling blob store...")
	if err := janitorMgr.ReconcileBlobs(); err != nil {
		log.Printf("[STARTUP] blob reconciliation skipped: %v", err)
	}

	workerCtx, workerCancel := context.WithCancel(context.Background())
	go janitorMgr.Run(workerCtx, time.Duration(modules.JanitorInterval)*time.Minute)

	router := modules.NewRouter(janitorMgr, uiFS())

	server := &http.Server{
		Addr:              fmt.Sprintf("%s:%d", modules.Host, modules.Port),
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      120 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		log.Printf("[STARTUP] SERVER_LISTENING host=%s port=%d url=http://%s:%d", modules.Host, modules.Port, modules.Host, modules.Port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server failed: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	sig := <-stop
	log.Printf("[SHUTDOWN] SIGNAL_RECEIVED signal=%s action=graceful_shutdown", sig)

	workerCancel()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Printf("[SHUTDOWN] HTTP_SERVER_ERROR error=%v", err)
	} else {
		log.Printf("[SHUTDOWN] HTTP_SERVER_CLOSED")
	}
}