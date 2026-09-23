package modules

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newEvictStore(t *testing.T) *Store {
	t.Helper()
	s, err := NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func insertEvictRec(t *testing.T, s *Store, id string, size int64, expiresAt int64, blob string) {
	t.Helper()
	rec := &Record{
		ID:         id,
		Owner:      "ed25519:test",
		Collection: "c",
		CreatedAt:  time.Now().Unix() - 100,
		ExpiresAt:  expiresAt,
		Data:       json.RawMessage(`{"x":1}`),
		Blob:       blob,
		Labels:     []string{},
		Sig:        "sig",
		Size:       size,
	}
	if _, _, err := s.InsertRecord(rec); err != nil {
		t.Fatalf("InsertRecord(%s): %v", id, err)
	}
}

func trackBlob(t *testing.T, s *Store, dir, hash string, size int64) {
	t.Helper()
	if _, err := s.UpsertBlob(hash, size); err != nil {
		t.Fatalf("UpsertBlob: %v", err)
	}
	if err := os.WriteFile(BlobPath(dir, hash), make([]byte, size), 0o644); err != nil {
		t.Fatalf("write blob file: %v", err)
	}
}

func TestBiggestExpiredOrder(t *testing.T) {
	s := newEvictStore(t)
	now := time.Now().Unix()
	insertEvictRec(t, s, "small", 100, now-10, "")
	insertEvictRec(t, s, "big", 300, now-10, "")
	insertEvictRec(t, s, "mid", 200, now-10, "")
	insertEvictRec(t, s, "live", 9999, now+3600, "")

	recs, err := s.BiggestExpiredRecords(now, 10)
	if err != nil {
		t.Fatalf("BiggestExpiredRecords: %v", err)
	}
	if len(recs) != 3 {
		t.Fatalf("got %d expired, want 3", len(recs))
	}
	if recs[0].ID != "big" || recs[1].ID != "mid" || recs[2].ID != "small" {
		t.Errorf("wrong size order: %v", recs)
	}
}

func TestEarliestLiveOrder(t *testing.T) {
	s := newEvictStore(t)
	now := time.Now().Unix()
	insertEvictRec(t, s, "far-big", 9000, now+7200, "")
	insertEvictRec(t, s, "near-small", 50, now+60, "")
	insertEvictRec(t, s, "near-big", 8000, now+60, "")

	recs, err := s.EarliestLiveRecords(now, 10)
	if err != nil {
		t.Fatalf("EarliestLiveRecords: %v", err)
	}
	if len(recs) != 3 {
		t.Fatalf("got %d live, want 3", len(recs))
	}
	// Soonest expiry first; size breaks the tie.
	if recs[0].ID != "near-big" || recs[1].ID != "near-small" || recs[2].ID != "far-big" {
		t.Errorf("wrong order: %v", recs)
	}
}

func TestDeleteRecords(t *testing.T) {
	s := newEvictStore(t)
	now := time.Now().Unix()
	insertEvictRec(t, s, "a", 10, now-10, "")
	insertEvictRec(t, s, "b", 10, now-10, "")
	n, err := s.DeleteRecords([]string{"a", "missing"})
	if err != nil {
		t.Fatalf("DeleteRecords: %v", err)
	}
	if n != 1 {
		t.Errorf("deleted %d, want 1", n)
	}
	if _, err := s.GetRecord("a"); err == nil {
		t.Error("record a should be gone")
	}
}

func TestEmergencyExpiredBiggestFirst(t *testing.T) {
	dir := t.TempDir()
	withBlobDir(t, dir)
	s := newEvictStore(t)
	now := time.Now().Unix()
	insertEvictRec(t, s, "s", 100, now-10, "")
	insertEvictRec(t, s, "b", 300, now-10, "")

	mgr := NewJanitor(s)
	stats, err := mgr.EmergencyEvict(1<<40, 10, false, func() int64 { return 0 })
	if err != nil {
		t.Fatalf("EmergencyEvict: %v", err)
	}
	if stats.Items != 2 || stats.Tier != "expired" {
		t.Errorf("stats = %+v, want 2 items tier expired", stats)
	}
	if _, err := s.GetRecord("s"); err == nil {
		t.Error("expired record s should be gone")
	}
	if _, err := s.GetRecord("b"); err == nil {
		t.Error("expired record b should be gone")
	}
}

func TestEmergencyOrphanIgnoresGrace(t *testing.T) {
	dir := t.TempDir()
	withBlobDir(t, dir)
	s := newEvictStore(t)
	hash := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	trackBlob(t, s, dir, hash, 2048)

	mgr := NewJanitor(s)
	stats, err := mgr.EmergencyEvict(1<<40, 10, false, func() int64 { return 0 })
	if err != nil {
		t.Fatalf("EmergencyEvict: %v", err)
	}
	if stats.Items != 1 || stats.Tier != "orphan" {
		t.Errorf("stats = %+v, want 1 item tier orphan", stats)
	}
	if _, err := os.Stat(BlobPath(dir, hash)); !os.IsNotExist(err) {
		t.Error("orphan blob file should be unlinked first")
	}
	if _, err := s.GetBlob(hash); err == nil {
		t.Error("orphan blob row should be gone")
	}
}

func TestEmergencyLiveExemptUnlessOptedIn(t *testing.T) {
	newLive := func(t *testing.T) *Store {
		s := newEvictStore(t)
		now := time.Now().Unix()
		insertEvictRec(t, s, "live1", 100, now+3600, "")
		return s
	}

	dir := t.TempDir()
	withBlobDir(t, dir)

	mgr := NewJanitor(newLive(t))
	stats, err := mgr.EmergencyEvict(1<<40, 10, false, func() int64 { return 0 })
	if err != nil {
		t.Fatalf("EmergencyEvict: %v", err)
	}
	if stats.Items != 0 {
		t.Errorf("safe-only pass must not touch live data, evicted %d", stats.Items)
	}

	mgr2 := NewJanitor(newLive(t))
	stats2, err := mgr2.EmergencyEvict(1<<40, 10, true, func() int64 { return 0 })
	if err != nil {
		t.Fatalf("EmergencyEvict live: %v", err)
	}
	if stats2.Items != 1 || stats2.Tier != "live" {
		t.Errorf("stats = %+v, want 1 item tier live", stats2)
	}
}

func TestEmergencyReferencedBlobProtected(t *testing.T) {
	dir := t.TempDir()
	withBlobDir(t, dir)
	s := newEvictStore(t)
	now := time.Now().Unix()
	hash := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	trackBlob(t, s, dir, hash, 4096)
	insertEvictRec(t, s, "owner", 50, now+3600, hash) // live reference pins the blob

	mgr := NewJanitor(s)
	stats, err := mgr.EmergencyEvict(1<<40, 10, false, func() int64 { return 0 })
	if err != nil {
		t.Fatalf("EmergencyEvict: %v", err)
	}
	if stats.Items != 0 {
		t.Errorf("referenced blob must survive safe tiers, evicted %d", stats.Items)
	}
	if _, err := os.Stat(BlobPath(dir, hash)); err != nil {
		t.Errorf("pinned blob file must survive: %v", err)
	}
}

func TestEmergencyCooldown(t *testing.T) {
	withBlobDir(t, t.TempDir())
	s := newEvictStore(t)
	mgr := NewJanitor(s)
	if _, err := mgr.EmergencyEvict(1, 10, false, func() int64 { return 1 << 40 }); err != nil {
		t.Fatalf("first pass: %v", err)
	}
	if _, err := mgr.EmergencyEvict(1, 10, false, func() int64 { return 0 }); err != ErrEmergencyCooldown {
		t.Errorf("second pass must hit cooldown, got %v", err)
	}
}

func TestEmergencyTargetStopsEarly(t *testing.T) {
	withBlobDir(t, t.TempDir())
	s := newEvictStore(t)
	now := time.Now().Unix()
	insertEvictRec(t, s, "x", 100, now-10, "")
	mgr := NewJanitor(s)
	stats, err := mgr.EmergencyEvict(10, 10, false, func() int64 { return 1 << 40 })
	if err != nil {
		t.Fatalf("EmergencyEvict: %v", err)
	}
	if stats.Items != 0 {
		t.Errorf("satisfied target must delete nothing, deleted %d", stats.Items)
	}
	if _, err := s.GetRecord("x"); err != nil {
		t.Error("record x must survive a satisfied target")
	}
}

func TestMaybeEarlySweep(t *testing.T) {
	withBlobDir(t, t.TempDir())
	s := newEvictStore(t)
	now := time.Now().Unix()
	insertEvictRec(t, s, "old", 10, now-10, "")
	mgr := NewJanitor(s)

	calm := &DiskGuard{statFn: fixedStats(1000, 500)}
	mgr.MaybeEarlySweep(calm)
	if _, err := s.GetRecord("old"); err != nil {
		t.Error("calm disk must not trigger early sweep")
	}

	hot := &DiskGuard{statFn: fixedStats(1000, 100)}
	mgr.MaybeEarlySweep(hot)
	if _, err := s.GetRecord("old"); err == nil {
		t.Error("soft-mark pressure must purge expired records early")
	}
}
