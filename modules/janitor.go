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
	store *Store
}

func NewJanitor(store *Store) *Manager {
	return &Manager{store: store}
}

// Store exposes the underlying DB so record handlers work without signature changes.
func (m *Manager) Store() *Store {
	if m == nil {
		return nil
	}
	return m.store
}

func (m *Manager) Run(ctx context.Context, interval time.Duration) {
	log.Printf("[janitor] started (interval: %s)", interval)

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
			if err := m.EvictBlobs(); err != nil {
				log.Printf("[janitor] blob eviction error: %v", err)
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

// EvictBlobs deletes orphan blobs whose grace period has elapsed.
// A blob is an orphan when no live record references it via data.blob
// (linked blobs live as long as their referencing record). Orphans are
// evicted oldest-access-first; there is no total storage quota, so blobs
// with live references are never evicted to make room.
func (m *Manager) EvictBlobs() error {
	if m == nil || m.store == nil {
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
	// Blobs referenced via data.blob by live records are exempt: evicting
	// them would dangle a stored event. Computed once per pass from the
	// indexed records.blob_hash column; a concurrent publish may pin a
	// blob mid-pass, in which case it is picked up on the next janitor tick.
	referenced, err := m.store.GetReferencedBlobHashes(now.Unix())
	if err != nil {
		log.Printf("[janitor] failed to list referenced blobs (proceeding without exemptions): %v", err)
		referenced = nil
	}
	for {
		candidates, err := m.store.GetBlobsByLRU(pageSize, offset)
		if err != nil {
			return err
		}
		scanned := 0
		for _, b := range candidates {
			scanned++
			if referenced[b.Hash] {
				offset++
				protected++
				continue
			}
			if b.CreatedAt.Add(orphanGrace).After(now) {
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
			evicted++
			log.Printf("[janitor] evicted orphan blob %s (%s, grace %s)", b.Hash, FormatBytes(b.Size), orphanGrace)
		}
		if scanned < pageSize {
			break
		}
	}
	if protected > 0 {
		log.Printf("[janitor] %d blobs still protected (live reference or orphan grace)", protected)
	}
	if evicted > 0 {
		log.Printf("[janitor] blob eviction done: evicted %d blobs, freed %s", evicted, FormatBytes(freed))
	}
	return nil
}

// ReconcileBlobs imports untracked blob files into the DB and drops rows
// whose files vanished. On-disk names are bare sha256 hex digests; anything
// else is ignored (staging temps are swept). Call at startup after
// EnsureBlobDir.
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
	var imported, quarantined, tempRemoved int
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		// Staging temps (up-*) leak only if the process died between
		// CreateTemp and the final rename — sweep them at startup so a
		// crash never permanently consumes blob storage.
		if strings.HasPrefix(name, "up-") {
			if err := os.Remove(filepath.Join(BlobDir, name)); err == nil {
				tempRemoved++
			}
			continue
		}
		// Only bare 64-hex names qualify; everything else is foreign.
		norm, err := NormalizeBlobHash(name)
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
	if imported > 0 || missing > 0 || quarantined > 0 || tempRemoved > 0 {
		log.Printf("[janitor] blob reconcile: %d imported, %d missing dropped, %d quarantined, %d stale temp files removed", imported, missing, quarantined, tempRemoved)
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
