package modules

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

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

// StorageLimit returns the hard quota blobs may not exceed.
func (m *Manager) StorageLimit() int64 {
	if m == nil || m.limit <= 0 {
		return StorageMaxBytes
	}
	return m.limit
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

	// Initial purge so restarts promptly clear backlog.
	if n, err := m.PurgeExpiredRecords(time.Now().Unix()); err != nil {
		log.Printf("[janitor] initial record purge error: %v", err)
	} else if n > 0 {
		log.Printf("[janitor] initial record purge: %d expired removed", n)
	}

	for {
		select {
		case <-ctx.Done():
			log.Printf("[janitor] stopped")
			return
		case <-ticker.C:
			if n, err := m.PurgeExpiredRecords(time.Now().Unix()); err != nil {
				log.Printf("[janitor] record purge error: %v", err)
			} else if n > 0 {
				log.Printf("[janitor] purged %d expired records", n)
			}
			if err := m.CheckBlobThreshold(); err != nil {
				log.Printf("[janitor] blob threshold check error: %v", err)
			}
		}
	}
}

// PurgeExpiredRecords deletes expired records + orphan labels.
func (m *Manager) PurgeExpiredRecords(nowUnix int64) (int64, error) {
	if m == nil || m.store == nil {
		return 0, nil
	}
	return m.store.DeleteExpiredRecords(nowUnix)
}

// totalUsage returns tracked blob bytes against the shared STORAGE_MAX quota.
func (m *Manager) totalUsage() int64 {
	if m == nil || m.store == nil {
		return 0
	}
	size, _ := m.store.GetBlobSize()
	return size
}

// CheckBlobThreshold evicts LRU blobs whose size-weighted retention has
// expired while total usage exceeds the threshold share of the limit.
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

// EvictBlobsLRU deletes blobs LRU-first, never touching blobs still inside
// their size-weighted retention (30 days at 512 MiB .. 1 year at size 0)
// or linked via "_blob" from a live record. Stops once total usage drops
// to target.
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
	var protected int
	const pageSize = 50
	// offset counts scanned-but-surviving rows; evicted rows vanish from
	// the table so they must not advance the page cursor.
	offset := 0
	now := time.Now()
	// Blobs linked via "_blob" from live records are exempt: evicting
	// them would dangle a stored record. Fetched once per pass; a
	// concurrent publish may reference a blob mid-pass, in which case it
	// is picked up on the next janitor tick.
	referenced, err := m.store.GetReferencedBlobHashes(now.Unix())
	if err != nil {
		log.Printf("[janitor] failed to list referenced blobs (proceeding without exemptions): %v", err)
		referenced = nil
	}
	for total > target {
		candidates, err := m.store.GetBlobsByLRU(pageSize, offset)
		if err != nil {
			return err
		}
		if len(candidates) == 0 {
			log.Printf("[janitor] no more blobs to scan (%d evicted, %d still protected)", evicted, protected)
			break
		}
		for _, b := range candidates {
			if total <= target {
				break
			}
			if referenced[b.Hash] {
				offset++
				protected++
				continue
			}
			if !BlobRetentionExpired(b.CreatedAt, b.Size, now) {
				offset++
				protected++
				continue
			}
			path := BlobPath(BlobDir, b.Hash)
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				log.Printf("[janitor] failed to remove blob %s: %v", b.Hash, err)
				offset++
				protected++
				continue
			}
			if err := m.store.DeleteBlob(b.Hash); err != nil {
				log.Printf("[janitor] failed to delete blob row %s: %v", b.Hash, err)
				offset++
				protected++
				continue
			}
			freed += b.Size
			total -= b.Size
			evicted++
			log.Printf("[janitor] evicted blob %s (%s, retention %s)", b.Hash, FormatBytes(b.Size), BlobRetentionForSize(b.Size))
		}
		if total > target && len(candidates) < pageSize {
			log.Printf("[janitor] %d blobs still protected (retention or live reference), usage %s above target %s",
				protected, FormatBytes(total), FormatBytes(target))
			break
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
	var imported, quarantined int
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.EqualFold(filepath.Ext(name), ".bin") {
			continue
		}
		hash := name[:len(name)-len(".bin")]
		norm, err := NormalizeBlobHash(hash)
		if err != nil {
			continue
		}
		if known[norm] {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		// Verify content hash matches the filename before trusting it:
		// a tampered or half-written file must not be imported under a
		// wrong address. Mismatches are quarantined, not deleted.
		actual, err := hashBlobFile(filepath.Join(BlobDir, name))
		if err != nil {
			log.Printf("[janitor] failed to hash untracked blob %s: %v", name, err)
			continue
		}
		if actual != norm {
			qdir := filepath.Join(BlobDir, "quarantine")
			if mkErr := os.MkdirAll(qdir, 0o755); mkErr != nil {
				log.Printf("[janitor] hash mismatch for %s (want %s got %s), quarantine unavailable: %v", name, norm, actual, mkErr)
				continue
			}
			if mvErr := os.Rename(filepath.Join(BlobDir, name), filepath.Join(qdir, name)); mvErr != nil {
				log.Printf("[janitor] failed to quarantine mismatched blob %s: %v", name, mvErr)
				continue
			}
			log.Printf("[janitor] quarantined hash-mismatch blob %s (content sha256=%s)", name, actual)
			quarantined++
			continue
		}
		if _, err := m.store.UpsertBlob(norm, info.Size()); err != nil {
			log.Printf("[janitor] failed to import blob %s: %v", norm, err)
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
	if imported > 0 || missing > 0 || quarantined > 0 {
		log.Printf("[janitor] blob reconcile: %d imported, %d missing dropped, %d quarantined", imported, missing, quarantined)
	}
	return nil
}

// hashBlobFile streams a file and returns its lowercase hex sha256.
func hashBlobFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}