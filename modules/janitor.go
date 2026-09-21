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
	ipfs      *Client
	limit     int64
	threshold float64
	expiry    time.Duration
}

func NewJanitor(store *Store, ipfsClient *Client, limit int64) *Manager {
	return &Manager{
		store:     store,
		ipfs:      ipfsClient,
		limit:     limit,
		threshold: float64(PinThreshold) / 100.0,
		expiry:    time.Duration(PinExpiryDays) * 24 * time.Hour,
	}
}

// Store exposes the underlying DB so record handlers work without signature changes.
func (m *Manager) Store() *Store {
	if m == nil {
		return nil
	}
	return m.store
}

func (m *Manager) PinOnUpload(cid, filename string, size int64) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := m.ipfs.PinAdd(ctx, cid); err != nil {
		log.Printf("[janitor] failed to pin %s: %v", cid, err)
		return err
	}

	if err := m.store.InsertUpload(cid, filename, size); err != nil {
		log.Printf("[janitor] db insert failed for %s: %v — rolling back pin", cid, err)
		if rbErr := m.ipfs.PinRemove(context.Background(), cid); rbErr != nil {
			log.Printf("[janitor] rollback unpin failed for %s: %v", cid, rbErr)
		}
		return err
	}

	pinnedSize, _ := m.store.GetPinnedSize()
	log.Printf("[janitor] pinned %s (%s) — total pinned: %s",
		cid, FormatBytes(size), FormatBytes(pinnedSize))

	if float64(pinnedSize) > float64(m.limit)*m.threshold {
		log.Printf("[janitor] storage exceeds %d%% threshold, evicting oldest", PinThreshold)
		if err := m.EvictOldest(); err != nil {
			log.Printf("[janitor] eviction failed: %v", err)
		}
	}

	return nil
}

func (m *Manager) Reconcile() error {
	log.Printf("[janitor] starting startup reconciliation...")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ipfsPins, err := m.ipfs.PinList(ctx)
	if err != nil {
		log.Printf("[janitor] failed to list IPFS pins: %v (skipping reconciliation)", err)
		return err
	}

	dbCIDs, err := m.store.GetAllTrackedCIDs()
	if err != nil {
		log.Printf("[janitor] failed to get tracked CIDs: %v", err)
		return err
	}

	var orphaned int
	for cid := range ipfsPins {
		if _, tracked := dbCIDs[cid]; !tracked {
			size, err := m.ipfs.ObjectStat(ctx, cid)
			if err != nil {
				log.Printf("[janitor] failed to stat orphan %s: %v (using size=0)", cid, err)
				size = 0
			}
			if err := m.store.InsertOrphaned(cid, size); err != nil {
				log.Printf("[janitor] failed to import orphan %s: %v", cid, err)
				continue
			}
			orphaned++
		}
	}

	var missing []string
	for cid, unpinned := range dbCIDs {
		if unpinned {
			continue
		}
		if _, exists := ipfsPins[cid]; !exists {
			missing = append(missing, cid)
		}
	}

	if len(missing) > 0 {
		if err := m.store.MarkMissingAsUnpinned(missing); err != nil {
			log.Printf("[janitor] failed to mark missing: %v", err)
		}
	}

	pinnedCount, _ := m.store.GetPinnedCount()
	pinnedSize, _ := m.store.GetPinnedSize()
	log.Printf("[janitor] reconciliation done: %d orphaned imported, %d missing marked — %d pinned (%s)",
		orphaned, len(missing), pinnedCount, FormatBytes(pinnedSize))

	return nil
}

func (m *Manager) Run(ctx context.Context, interval time.Duration) {
	log.Printf("[janitor] started (interval: %s, expiry: %s, threshold: %d%%)",
		interval, m.expiry, PinThreshold)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Printf("[janitor] stopped")
			return
		case <-ticker.C:
			if err := m.UnpinExpired(); err != nil {
				log.Printf("[janitor] expired unpin cycle error: %v", err)
			}
			if err := m.CheckThreshold(); err != nil {
				log.Printf("[janitor] threshold check error: %v", err)
			}
			if err := m.CheckBlobThreshold(); err != nil {
				log.Printf("[janitor] blob threshold check error: %v", err)
			}
		}
	}
}

func (m *Manager) UnpinExpired() error {
	expired, err := m.store.GetExpiredUnpins(m.expiry)
	if err != nil {
		return err
	}

	if len(expired) == 0 {
		return nil
	}

	log.Printf("[janitor] unpinning %d expired files", len(expired))

	var unpinned int
	for _, u := range expired {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if err := m.ipfs.PinRemove(ctx, u.CID); err != nil {
			cancel()
			log.Printf("[janitor] failed to unpin %s: %v", u.CID, err)
			continue
		}
		cancel()
		if err := m.store.MarkUnpinned(u.CID); err != nil {
			log.Printf("[janitor] failed to mark unpinned %s: %v", u.CID, err)
			continue
		}
		unpinned++
	}

	pinnedSize, _ := m.store.GetPinnedSize()
	log.Printf("[janitor] unpinned %d expired files — pinned: %s", unpinned, FormatBytes(pinnedSize))
	return nil
}

func (m *Manager) CheckThreshold() error {
	pinnedSize, err := m.store.GetPinnedSize()
	if err != nil {
		return err
	}

	if float64(pinnedSize) <= float64(m.limit)*m.threshold {
		return nil
	}

	log.Printf("[janitor] pinned size %s exceeds %d%% of %s, evicting",
		FormatBytes(pinnedSize), PinThreshold, FormatBytes(m.limit))

	return m.EvictOldest()
}

func (m *Manager) EvictOldest() error {
	pinnedSize, err := m.store.GetPinnedSize()
	if err != nil {
		return err
	}

	targetSize := int64(float64(m.limit) * m.threshold)
	if pinnedSize <= targetSize {
		return nil
	}

	var freed int64
	var unpinned int

	for pinnedSize > targetSize {
		oldest, err := m.store.GetOldestPinnedFiles(50)
		if err != nil {
			return err
		}
		if len(oldest) == 0 {
			log.Printf("[janitor] warning: cannot evict enough — no more pinned files")
			break
		}

		for _, u := range oldest {
			if pinnedSize-freed <= targetSize {
				break
			}

			if err := m.ipfs.PinRemove(context.Background(), u.CID); err != nil {
				log.Printf("[janitor] failed to unpin %s for eviction: %v", u.CID, err)
				continue
			}
			if err := m.store.MarkUnpinned(u.CID); err != nil {
				log.Printf("[janitor] failed to mark %s unpinned: %v", u.CID, err)
				continue
			}

			freed += u.Size
			unpinned++
			log.Printf("[janitor] evicted %s (%s)", u.Filename, FormatBytes(u.Size))
		}

		pinnedSize, _ = m.store.GetPinnedSize()
	}

	newSize, _ := m.store.GetPinnedSize()
	log.Printf("[janitor] eviction done: unpinned %d files, freed %s — now at %s",
		unpinned, FormatBytes(freed), FormatBytes(newSize))
	return nil
}

func (m *Manager) GetHistory(limit, offset int) ([]Upload, error) {
	return m.store.GetUploadHistory(limit, offset)
}

func (m *Manager) GetStats() (count int64, size int64, err error) {
	count, err = m.store.GetPinnedCount()
	if err != nil {
		return
	}
	size, err = m.store.GetPinnedSize()
	return
}

// totalUsage returns pinned IPFS bytes + tracked blob bytes against the same
// STORAGE_MAX quota (/data holds both).
func (m *Manager) totalUsage() (pinned, blobs, total int64) {
	if m == nil || m.store == nil {
		return 0, 0, 0
	}
	pinned, _ = m.store.GetPinnedSize()
	blobs, _ = m.store.GetBlobSize()
	return pinned, blobs, pinned + blobs
}

// CheckBlobThreshold evicts LRU blobs (min 7 days old) while total usage
// exceeds the PinThreshold share of the storage limit.
func (m *Manager) CheckBlobThreshold() error {
	if m == nil || m.store == nil {
		return nil
	}
	_, _, total := m.totalUsage()
	target := int64(float64(m.limit) * m.threshold)
	if total <= target {
		return nil
	}
	log.Printf("[janitor] total usage %s exceeds %d%% of %s, evicting LRU blobs",
		FormatBytes(total), PinThreshold, FormatBytes(m.limit))
	return m.EvictBlobsLRU()
}

// EvictBlobsLRU deletes blobs LRU-first, never touching blobs younger than
// blobMinAge (7 days). Stops once total usage drops to target.
func (m *Manager) EvictBlobsLRU() error {
	if m == nil || m.store == nil {
		return nil
	}
	target := int64(float64(m.limit) * m.threshold)
	_, _, total := m.totalUsage()
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
