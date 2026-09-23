package modules

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Manager struct {
	store              *Store
	guard              *DiskGuard
	mu                 sync.RWMutex
	lastRun            time.Time
	purgedRecordsTotal int64
	lastEmergency      EmergencyEvictStats
}

// SetDiskGuard attaches the admission ledger (wired in main).
func (m *Manager) SetDiskGuard(g *DiskGuard) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.guard = g
}

// Guard returns the admission ledger, possibly nil.
func (m *Manager) Guard() *DiskGuard {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.guard
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

func (m *Manager) recordSweep(purged int64) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.lastRun = time.Now().UTC()
	if purged > 0 {
		m.purgedRecordsTotal += purged
	}
	m.mu.Unlock()
}

// Status returns a summary of janitor policy and activity for /status.
func (m *Manager) Status() map[string]any {
	if m == nil {
		return map[string]any{
			"interval_mins":     JanitorInterval,
			"orphan_grace_days": BlobOrphanGraceDays,
			"purged_records":    int64(0),
		}
	}
	m.mu.RLock()
	lastRun := m.lastRun
	purged := m.purgedRecordsTotal
	le := m.lastEmergency
	m.mu.RUnlock()

	res := map[string]any{
		"interval_mins":     JanitorInterval,
		"orphan_grace_days": BlobOrphanGraceDays,
		"purged_records":    purged,
	}
	if !lastRun.IsZero() {
		res["last_run"] = lastRun.Format(time.RFC3339)
	}
	if !le.At.IsZero() {
		res["last_emergency"] = map[string]any{
			"at":          le.At.Format(time.RFC3339),
			"items":       le.Items,
			"freed_bytes": le.FreedBytes,
			"tier":        le.Tier,
		}
	}
	return res
}

func (m *Manager) Run(ctx context.Context, interval time.Duration) {
	log.Printf("[janitor] started (interval: %s)", interval)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Initial purge so restarts promptly clear backlog.
	if n, err := m.PurgeExpiredRecords(time.Now().Unix()); err != nil {
		log.Printf("[janitor] initial record purge error: %v", err)
	} else {
		m.recordSweep(n)
		if n > 0 {
			log.Printf("[janitor] initial record purge: %d expired removed", n)
		}
	}

	for {
		select {
		case <-ctx.Done():
			log.Printf("[janitor] stopped")
			return
		case <-ticker.C:
			if n, err := m.PurgeExpiredRecords(time.Now().Unix()); err != nil {
				log.Printf("[janitor] record purge error: %v", err)
			} else {
				m.recordSweep(n)
				if n > 0 {
					log.Printf("[janitor] purged %d expired records", n)
				}
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

// ErrEmergencyCooldown signals an emergency pass skipped because a cleanup
// ran within the cooldown window.
var ErrEmergencyCooldown = errors.New("emergency eviction on cooldown")

// EmergencyEvictStats describes one emergency pass for /status and logs.
// FreedBytes for deleted DB rows is estimated from row sizes: SQLite
// reclaims pages lazily (incremental vacuum runs per delete).
type EmergencyEvictStats struct {
	At         time.Time `json:"at"`
	Items      int64     `json:"items"`
	FreedBytes int64     `json:"freed_bytes"`
	Tier       string    `json:"tier"`
}

// EmergencyEvict frees disk in value order until freeBytes() reports at
// least targetBytes free or maxItems items were removed. Tiers:
//
//  1. expired records, biggest first (dead data, violates nothing);
//  2. orphan blobs ignoring grace, biggest first (unreferenced bytes);
//  3. if includeLive: unexpired records, soonest-expiry first with biggest
//     tiebreak, then their newly orphaned blobs.
//
// Blob files are unlinked before their DB rows so freed bytes materialize
// even when SQLite itself can barely write. A pass that deletes nothing
// still counts against cooldown: concurrent failing writers must back off,
// not stampede.
func (m *Manager) EmergencyEvict(targetBytes int64, maxItems int, includeLive bool, freeBytes func() int64) (EmergencyEvictStats, error) {
	var zero EmergencyEvictStats
	if m == nil || m.store == nil {
		return zero, nil
	}
	if maxItems <= 0 || maxItems > EmergencyMaxItems {
		maxItems = EmergencyMaxItems
	}
	m.mu.Lock()
	if !m.lastEmergency.At.IsZero() && time.Since(m.lastEmergency.At) < time.Duration(EmergencyCooldownSecs)*time.Second {
		m.mu.Unlock()
		return zero, ErrEmergencyCooldown
	}
	m.lastEmergency.At = time.Now().UTC()
	m.mu.Unlock()

	stats := EmergencyEvictStats{At: time.Now().UTC(), Tier: "none"}
	now := time.Now().Unix()
	var freed, items int64
	done := func() bool {
		if items >= int64(maxItems) {
			return true
		}
		if targetBytes > 0 && freeBytes != nil && freeBytes() >= targetBytes {
			return true
		}
		return false
	}
	remaining := func() int {
		r := maxItems - int(items)
		if r < 1 {
			return 0
		}
		if r > 100 {
			return 100
		}
		return r
	}

	// Tier 1: biggest expired records.
	for !done() {
		recs, err := m.store.BiggestExpiredRecords(now, remaining())
		if err != nil {
			log.Printf("[emergency] expired scan failed: %v", err)
			break
		}
		if len(recs) == 0 {
			break
		}
		ids := make([]string, len(recs))
		var sz int64
		for i, r := range recs {
			ids[i] = r.ID
			sz += r.Size
		}
		n, err := m.store.DeleteRecords(ids)
		if err != nil {
			log.Printf("[emergency] expired delete failed: %v", err)
			break
		}
		if n == 0 {
			break
		}
		items += n
		freed += sz
		stats.Tier = "expired"
	}

	// Tier 2: biggest orphan blobs, grace ignored. On exemption-list
	// failure the tier is skipped outright: deleting possibly-live blobs
	// blind is worse than freeing nothing.
	if !done() {
		referenced, err := m.store.GetReferencedBlobHashes(now)
		if err != nil {
			log.Printf("[emergency] exemption list failed, skipping orphan tier: %v", err)
		} else {
			for !done() {
				cand, err := m.store.BiggestBlobs(remaining())
				if err != nil {
					log.Printf("[emergency] blob scan failed: %v", err)
					break
				}
				if len(cand) == 0 {
					break
				}
				progress := false
				for _, b := range cand {
					if done() {
						break
					}
					if referenced[b.Hash] {
						continue
					}
					path := BlobPath(BlobDir, b.Hash)
					if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
						log.Printf("[emergency] failed to remove blob %s: %v", b.Hash, err)
						continue
					}
					freed += b.Size
					progress = true
					stats.Tier = "orphan"
					if err := m.store.DeleteBlob(b.Hash); err != nil {
						log.Printf("[emergency] failed to delete blob row %s: %v", b.Hash, err)
						continue
					}
					items++
				}
				if !progress {
					break
				}
			}
		}
	}

	// Tier 3: live records, soonest-expiry first. Deleting the event turns
	// solely-referenced blobs into orphans, swept right after — never a
	// referenced blob alone, so no event is left dangling.
	if includeLive && !done() {
		for !done() {
			recs, err := m.store.EarliestLiveRecords(now, remaining())
			if err != nil {
				log.Printf("[emergency] live scan failed: %v", err)
				break
			}
			if len(recs) == 0 {
				break
			}
			ids := make([]string, len(recs))
			var sz int64
			for i, r := range recs {
				ids[i] = r.ID
				sz += r.Size
			}
			n, err := m.store.DeleteRecords(ids)
			if err != nil {
				log.Printf("[emergency] live delete failed: %v", err)
				break
			}
			if n == 0 {
				break
			}
			items += n
			freed += sz
			stats.Tier = "live"
		}
		if !done() {
			referenced, err := m.store.GetReferencedBlobHashes(now)
			if err != nil {
				log.Printf("[emergency] post-live exemption list failed: %v", err)
			} else {
				for !done() {
					cand, err := m.store.BiggestBlobs(remaining())
					if err != nil || len(cand) == 0 {
						break
					}
					progress := false
					for _, b := range cand {
						if done() {
							break
						}
						if referenced[b.Hash] {
							continue
						}
						if err := os.Remove(BlobPath(BlobDir, b.Hash)); err != nil && !os.IsNotExist(err) {
							continue
						}
						freed += b.Size
						progress = true
						stats.Tier = "orphan"
						if err := m.store.DeleteBlob(b.Hash); err != nil {
							continue
						}
						items++
					}
					if !progress {
						break
					}
				}
			}
		}
	}

	stats.Items = items
	stats.FreedBytes = freed
	m.mu.Lock()
	m.lastEmergency = stats
	m.mu.Unlock()
	log.Printf("[emergency] pass done: tier=%s items=%d freed~%s", stats.Tier, items, FormatBytes(freed))
	return stats, nil
}

// MaybeEarlySweep runs a standard purge+evict pass when the guard reports
// pressure above the soft mark. Throttled by the shared cleanup clock so
// hot request paths can't trigger sweep storms. Safe tiers only: expired
// records and grace-eligible orphans — never live data.
func (m *Manager) MaybeEarlySweep(g *DiskGuard) {
	if m == nil || m.store == nil || g == nil {
		return
	}
	if !g.OverSoftMark() {
		return
	}
	m.mu.Lock()
	if !m.lastEmergency.At.IsZero() && time.Since(m.lastEmergency.At) < time.Duration(EmergencyCooldownSecs)*time.Second {
		m.mu.Unlock()
		return
	}
	m.lastEmergency.At = time.Now().UTC()
	m.mu.Unlock()
	now := time.Now().Unix()
	if n, err := m.store.DeleteExpiredRecords(now); err == nil && n > 0 {
		m.recordSweep(n)
		log.Printf("[janitor] early sweep purged %d expired records (soft mark)", n)
	}
	if err := m.EvictBlobs(); err != nil {
		log.Printf("[janitor] early sweep eviction error: %v", err)
	}
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
