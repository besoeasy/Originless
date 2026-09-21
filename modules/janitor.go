package modules

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"time"
)

// blobMinAge is the minimum retention for /up blobs before LRU may evict them.
const blobMinAge = 7 * 24 * time.Hour

type Manager struct {
	store     *Store
	limit     int64
	threshold float64
}

func NewJanitor(store *Store, limit int64) *Manager {
	return &Manager{
		store:     store,
		limit:     limit,
		threshold: 0.75, // evict when total usage exceeds 75% of the storage limit
	}
}

// Store exposes the underlying DB so record handlers work without signature changes.
func (m *Manager) Store() *Store {
	if m == nil {
		return nil
	}
	return m.store
}

func (m *Manager) Run(ctx context.Context, interval time.Duration) {
	log.Printf("[janitor] started (interval: %s, threshold: %d%%)",
		interval, PinThresholdPercent)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Printf("[janitor] stopped")
			return
		case <-ticker.C:
			if err := m.CheckBlobThreshold(); err != nil {
				log.Printf("[janitor] blob threshold check error: %v", err)
			}
		}
	}
}

// totalUsage returns tracked blob bytes against the shared STORAGE_MAX quota.
func (m *Manager) totalUsage() int64 {
	if m == nil || m.store == nil {
		return 0
	}
	size, _ := m.store.GetBlobSize()
	return size
}

// CheckBlobThreshold evicts LRU blobs (min 7 days old) while total usage
// exceeds the threshold share of the storage limit.
func (m *Manager) CheckBlobThreshold() error {
	if m == nil || m.store == nil {
		return nil
	}
	total := m.totalUsage()
	target := int64(float64(m.limit) * m.threshold)
	if total <= target {
		return nil
	}
	log.Printf("[janitor] total usage %s exceeds %d%% of %s, evicting LRU blobs",
		FormatBytes(total), PinThresholdPercent, FormatBytes(m.limit))
	return m.EvictBlobsLRU()
}

// EvictBlobsLRU deletes blobs LRU-first, never touching blobs younger than
// blobMinAge (7 days). Stops once total usage drops to target.
func (m *Manager) EvictBlobsLRU() error {
	if m == nil || m.store == nil {
		return nil
	}
	target := int64(float64(m.limit) * m.threshold)
	total := m.totalUsage()
	if total <= target {
		return nil
	}
	var evicted int
	var freed int64
	for total > target {
		candidates, err := m.store.GetEvictableBlobs(blobMinAge, 50)
		if err != nil {
			return err
		}
		if len(candidates) == 0 {
			log.Printf("[janitor] no evictable blobs (all younger than %s)", blobMinAge)
			break
		}
		for _, b := range candidates {
			if total <= target {
				break
			}
			path := BlobPath(BlobDir, b.Hash)
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				log.Printf("[janitor] failed to remove blob %s: %v", b.Hash, err)
				continue
			}
			if err := m.store.DeleteBlob(b.Hash); err != nil {
				log.Printf("[janitor] failed to delete blob row %s: %v", b.Hash, err)
				continue
			}
			freed += b.Size
			total -= b.Size
			evicted++
			log.Printf("[janitor] evicted blob %s (%s)", b.Hash, FormatBytes(b.Size))
		}
	}
	log.Printf("[janitor] blob eviction done: evicted %d blobs, freed %s", evicted, FormatBytes(freed))
	return nil
}

// ReconcileBlobs imports untracked *.bin files into the DB and drops rows
// whose files vanished. Call at startup after EnsureBlobDir.
func (m *Manager) ReconcileBlobs() error {
	if m == nil || m.store == nil {
		return nil
	}
	if err := EnsureBlobDir(BlobDir); err != nil {
		log.Printf("[janitor] blob dir %s unavailable: %v (skipping blob reconcile)", BlobDir, err)
		return err
	}
	entries, err := os.ReadDir(BlobDir)
	if err != nil {
		log.Printf("[janitor] failed to list blob dir: %v", err)
		return err
	}
	tracked, err := m.store.ListBlobHashes()
	if err != nil {
		log.Printf("[janitor] failed to list tracked blobs: %v", err)
		return err
	}
	known := make(map[string]bool, len(tracked))
	for _, h := range tracked {
		known[h] = true
	}
	var imported int
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if filepath.Ext(name) != ".bin" {
			continue
		}
		hash := name[:len(name)-len(".bin")]
		if _, err := NormalizeBlobHash(hash); err != nil {
			continue
		}
		if known[hash] {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if _, err := m.store.UpsertBlob(hash, info.Size()); err != nil {
			log.Printf("[janitor] failed to import blob %s: %v", hash, err)
			continue
		}
		imported++
	}
	var missing int
	for _, h := range tracked {
		if _, err := os.Stat(BlobPath(BlobDir, h)); os.IsNotExist(err) {
			if err := m.store.DeleteBlob(h); err == nil {
				missing++
			}
		}
	}
	if imported > 0 || missing > 0 {
		log.Printf("[janitor] blob reconcile: %d imported, %d missing dropped", imported, missing)
	}
	return nil
}