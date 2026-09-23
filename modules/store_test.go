package modules

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func createTestRecord(t *testing.T, id string, nowUnix int64) *Record {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	owner := "ed25519:" + hex.EncodeToString(pub)
	created := nowUnix
	expires := nowUnix + 3600
	labels := []string{"test:root"}
	data := map[string]any{"id": id}
	dataJSON, _ := json.Marshal(data)

	msg := owner + ":test:" + strconv.FormatInt(created, 10) + ":" +
		strconv.FormatInt(expires, 10) + ":" + string(dataJSON) + "::" + strings.Join(labels, ",")
	h := sha256.Sum256([]byte(msg))
	sig := ed25519.Sign(priv, h[:])

	return &Record{
		ID:         hex.EncodeToString(h[:]),
		Owner:      owner,
		Collection: "test",
		CreatedAt:  created,
		ExpiresAt:  expires,
		Data:       json.RawMessage(dataJSON),
		Labels:     labels,
		Sig:        hex.EncodeToString(sig),
		Size:       int64(len(dataJSON)),
	}
}

func TestStoreStateRoots(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	// 1. Empty state
	evCount, evRoot, err := store.EventsStateRoot()
	if err != nil {
		t.Fatalf("EventsStateRoot failed: %v", err)
	}
	if evCount != 0 {
		t.Fatalf("expected count 0, got %d", evCount)
	}
	if evRoot != "0000000000000000000000000000000000000000000000000000000000000000" {
		t.Fatalf("expected 64 zero hex root, got %s", evRoot)
	}

	bCount, bRoot, err := store.BlobsStateRoot()
	if err != nil {
		t.Fatalf("BlobsStateRoot failed: %v", err)
	}
	if bCount != 0 || bRoot != "0000000000000000000000000000000000000000000000000000000000000000" {
		t.Fatalf("expected empty blob root, got count=%d root=%s", bCount, bRoot)
	}

	// 2. Add records
	now := time.Now().Unix()
	r1 := createTestRecord(t, "1", now)
	r2 := createTestRecord(t, "2", now+1)

	if _, _, err := store.InsertRecord(r1); err != nil {
		t.Fatal(err)
	}
	cnt1, root1, _ := store.EventsStateRoot()
	if cnt1 != 1 || root1 == evRoot {
		t.Fatalf("expected root to change after insert: cnt=%d root=%s", cnt1, root1)
	}

	if _, _, err := store.InsertRecord(r2); err != nil {
		t.Fatal(err)
	}
	cnt2, root2, _ := store.EventsStateRoot()
	if cnt2 != 2 || root2 == root1 {
		t.Fatalf("expected root to change after second insert: cnt=%d root=%s", cnt2, root2)
	}

	// 3. Add Blobs
	blobHash1 := "1111111111111111111111111111111111111111111111111111111111111111"
	blobHash2 := "2222222222222222222222222222222222222222222222222222222222222222"
	if _, err := store.UpsertBlob(blobHash1, 100); err != nil {
		t.Fatal(err)
	}
	bcnt1, broot1, _ := store.BlobsStateRoot()
	if bcnt1 != 1 || broot1 != blobHash1 {
		t.Fatalf("single blob root should equal hash: got %s, want %s", broot1, blobHash1)
	}

	if _, err := store.UpsertBlob(blobHash2, 200); err != nil {
		t.Fatal(err)
	}
	bcnt2, broot2, _ := store.BlobsStateRoot()
	if bcnt2 != 2 || broot2 == broot1 {
		t.Fatalf("expected blob root change after second blob: got %s", broot2)
	}
}

func TestStoreStateRootsOrderIndependence(t *testing.T) {
	dir1 := t.TempDir()
	store1, err := NewStore(filepath.Join(dir1, "node1.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store1.Close()

	dir2 := t.TempDir()
	store2, err := NewStore(filepath.Join(dir2, "node2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store2.Close()

	now := time.Now().Unix()
	r1 := createTestRecord(t, "alpha", now)
	r2 := createTestRecord(t, "beta", now+1)
	r3 := createTestRecord(t, "gamma", now+2)

	// Store1 inserts 1, 2, 3
	store1.InsertRecord(r1)
	store1.InsertRecord(r2)
	store1.InsertRecord(r3)

	// Store2 inserts 3, 1, 2 (different order)
	store2.InsertRecord(r3)
	store2.InsertRecord(r1)
	store2.InsertRecord(r2)

	cnt1, root1, _ := store1.EventsStateRoot()
	cnt2, root2, _ := store2.EventsStateRoot()

	if cnt1 != cnt2 || root1 != root2 {
		t.Fatalf("order independence failed: store1(cnt=%d, root=%s) vs store2(cnt=%d, root=%s)",
			cnt1, root1, cnt2, root2)
	}
}
