package p2p

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/besoeasy/originless/modules"
	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
)

func boolPtr(v bool) *bool { return &v }

func createTestSignedRecord(collection string, data map[string]any, nowUnix int64, blob ...string) (*modules.Record, error) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		return nil, err
	}
	owner := "ed25519:" + hex.EncodeToString(pub)
	expires := nowUnix + 86400*30
	labels := []string{"test:p2p"}

	dataJSON, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}

	canonicalBuf, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}

	blobHash := ""
	if len(blob) > 0 {
		blobHash = blob[0]
	}

	msg := owner + ":" + collection + ":" + strconv.FormatInt(nowUnix, 10) + ":" +
		strconv.FormatInt(expires, 10) + ":" + string(canonicalBuf) + ":" + blobHash + ":" + strings.Join(labels, ",")
	h := sha256.Sum256([]byte(msg))
	sig := ed25519.Sign(priv, h[:])
	sigHex := hex.EncodeToString(sig)
	idHex := hex.EncodeToString(h[:])

	return &modules.Record{
		ID:         idHex,
		Owner:      owner,
		Collection: collection,
		CreatedAt:  nowUnix,
		ExpiresAt:  expires,
		Data:       json.RawMessage(dataJSON),
		Blob:       blobHash,
		Labels:     labels,
		Sig:        sigHex,
		Size:       int64(len(dataJSON)),
	}, nil
}

func startTestNode(t *testing.T, networkID string, extra Options) (*Manager, *modules.Store) {
	t.Helper()
	t.Setenv("NETWORK_ID", networkID)
	dir := t.TempDir()
	blobDir := filepath.Join(dir, "blobs")
	if err := os.MkdirAll(blobDir, 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := modules.NewStore(filepath.Join(dir, "node.db"))
	if err != nil {
		t.Fatal(err)
	}
	extra.Store = store
	extra.BlobDir = blobDir
	extra.DataDir = dir
	extra.ListenPort = 0
	if extra.ListenHost == "" {
		extra.ListenHost = "127.0.0.1"
	}
	extra.DisableQUIC = true
	extra.DisableMDNS = true
	if extra.RelayHop == nil {
		extra.RelayHop = boolPtr(false)
	}
	m := NewManagerWithOptions(extra)
	if m == nil {
		t.Fatal("expected manager")
	}
	if err := m.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() {
		m.Stop()
		store.Close()
	})
	return m, store
}

func waitSynced(t *testing.T, store *modules.Store, eventID, blobHash string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		rec, _ := store.GetRecord(eventID)
		if rec == nil {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		if blobHash == "" {
			return
		}
		meta, _ := store.GetBlob(blobHash)
		if meta != nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for event %s blob %s", eventID, blobHash)
}

func TestP2PEndToEndReconciliationAndGossip(t *testing.T) {
	networkID := "test-swarm-xyz"
	mgrA, storeA := startTestNode(t, networkID, Options{DisableDHT: true, DisableAutoRelay: true})
	mgrB, storeB := startTestNode(t, networkID, Options{DisableDHT: true, DisableAutoRelay: true})

	now := time.Now().Unix()
	blobContent := []byte("this is a sample binary blob content for p2p sync")
	blobSum := sha256.Sum256(blobContent)
	blobHash := hex.EncodeToString(blobSum[:])
	destA := modules.BlobPath(mgrA.opts.BlobDir, blobHash)
	if err := os.WriteFile(destA, blobContent, 0o644); err != nil {
		t.Fatalf("write blob A: %v", err)
	}
	if _, err := storeA.UpsertBlob(blobHash, int64(len(blobContent))); err != nil {
		t.Fatalf("upsert blob A: %v", err)
	}

	testEvent, err := createTestSignedRecord("chat", map[string]any{"text": "hello from Node A"}, now, blobHash)
	if err != nil {
		t.Fatalf("create test record: %v", err)
	}
	if _, _, err := storeA.InsertRecord(testEvent); err != nil {
		t.Fatalf("insert record into A: %v", err)
	}

	mgrB.Connect(context.Background(), mgrA.AddrInfo())
	waitSynced(t, storeB, testEvent.ID, blobHash, 8*time.Second)

	destB := modules.BlobPath(mgrB.opts.BlobDir, blobHash)
	contentB, err := os.ReadFile(destB)
	if err != nil {
		t.Fatalf("read blob on B: %v", err)
	}
	if string(contentB) != string(blobContent) {
		t.Fatalf("blob content mismatch on B")
	}

	eventFromB, err := createTestSignedRecord("chat", map[string]any{"text": "live gossip from Node B"}, now+1)
	if err != nil {
		t.Fatalf("create event B: %v", err)
	}
	if _, _, err := storeB.InsertRecord(eventFromB); err != nil {
		t.Fatalf("insert event on B: %v", err)
	}
	mgrB.BroadcastRecord(eventFromB)
	waitSynced(t, storeA, eventFromB.ID, "", 8*time.Second)
}

func TestNetworkIDMismatchRefusesSync(t *testing.T) {
	mgrA, storeA := startTestNode(t, "net-aaa", Options{DisableDHT: true, DisableAutoRelay: true})
	mgrB, storeB := startTestNode(t, "net-bbb", Options{DisableDHT: true, DisableAutoRelay: true})

	ev, err := createTestSignedRecord("chat", map[string]any{"text": "secret"}, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := storeA.InsertRecord(ev); err != nil {
		t.Fatal(err)
	}

	mgrB.Connect(context.Background(), mgrA.AddrInfo())
	time.Sleep(1500 * time.Millisecond)
	got, _ := storeB.GetRecord(ev.ID)
	if got != nil {
		t.Fatalf("expected network mismatch to refuse sync")
	}
}

func TestPersistentPeerIDAcrossRestart(t *testing.T) {
	t.Setenv("NETWORK_ID", "persist-mesh")
	dir := t.TempDir()
	blobDir := filepath.Join(dir, "blobs")
	_ = os.MkdirAll(blobDir, 0o755)
	store, err := modules.NewStore(filepath.Join(dir, "node.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	opts := Options{
		Store: store, BlobDir: blobDir, DataDir: dir, ListenPort: 0, ListenHost: "127.0.0.1",
		DisableQUIC: true, DisableMDNS: true, DisableDHT: true, DisableAutoRelay: true,
		RelayHop: boolPtr(false),
	}
	m1 := NewManagerWithOptions(opts)
	if err := m1.Start(); err != nil {
		t.Fatal(err)
	}
	id1 := m1.Host().ID()
	m1.Stop()

	m2 := NewManagerWithOptions(opts)
	if err := m2.Start(); err != nil {
		t.Fatal(err)
	}
	defer m2.Stop()
	if m2.Host().ID() != id1 {
		t.Fatalf("peer id changed: %s vs %s", id1, m2.Host().ID())
	}
}

func circuitAddr(relay peer.AddrInfo, dest peer.ID) (peer.AddrInfo, error) {
	var addrs []ma.Multiaddr
	for _, a := range relay.Addrs {
		full, err := ma.NewMultiaddr(fmt.Sprintf("%s/p2p/%s/p2p-circuit", a.String(), relay.ID.String()))
		if err != nil {
			continue
		}
		addrs = append(addrs, full)
	}
	if len(addrs) == 0 {
		return peer.AddrInfo{}, fmt.Errorf("no circuit addrs")
	}
	return peer.AddrInfo{ID: dest, Addrs: addrs}, nil
}

func TestPrivatePeersSyncViaRelay(t *testing.T) {
	networkID := "relay-swarm"
	relayMgr, _ := startTestNode(t, networkID, Options{
		DisableDHT: true, DisableAutoRelay: true, ForcePublic: true, RelayHop: boolPtr(true),
	})
	relayInfo := relayMgr.AddrInfo()

	mgrA, storeA := startTestNode(t, networkID, Options{
		DisableDHT: true, ForcePrivate: true, StaticRelays: []peer.AddrInfo{relayInfo},
	})
	mgrB, storeB := startTestNode(t, networkID, Options{
		DisableDHT: true, ForcePrivate: true, StaticRelays: []peer.AddrInfo{relayInfo},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	mgrA.Connect(ctx, relayInfo)
	mgrB.Connect(ctx, relayInfo)

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		via, err := circuitAddr(relayMgr.AddrInfo(), mgrB.Host().ID())
		if err == nil {
			mgrA.Connect(ctx, via)
		}
		if mgrA.transport.ActivePeersCount() > 0 && mgrB.transport.ActivePeersCount() > 0 {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}

	ev, err := createTestSignedRecord("chat", map[string]any{"text": "via relay"}, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := storeA.InsertRecord(ev); err != nil {
		t.Fatal(err)
	}
	mgrA.BroadcastRecord(ev)
	mgrA.reconcileAll()
	waitSynced(t, storeB, ev.ID, "", 20*time.Second)
}
