package p2p

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/besoeasy/originless/modules"
)

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

	// Canonical JSON
	var canonicalBuf []byte
	canonicalBuf, err = json.Marshal(data)
	if err != nil {
		return nil, err
	}

	blobHash := ""
	if len(blob) > 0 {
		blobHash = blob[0]
	}

	// Compute message to sign (mirrors modules.computeRecordID:
	// owner:collection:created:expires:canonical(data):blob:labels).
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

func TestP2PEndToEndReconciliationAndGossip(t *testing.T) {
	// 1. Setup Node A
	dirA := t.TempDir()
	blobDirA := filepath.Join(dirA, "blobs")
	_ = os.MkdirAll(blobDirA, 0o755)
	storeA, err := modules.NewStore(filepath.Join(dirA, "nodeA.db"))
	if err != nil {
		t.Fatalf("create storeA: %v", err)
	}
	defer storeA.Close()

	// 2. Setup Node B
	dirB := t.TempDir()
	blobDirB := filepath.Join(dirB, "blobs")
	_ = os.MkdirAll(blobDirB, 0o755)
	storeB, err := modules.NewStore(filepath.Join(dirB, "nodeB.db"))
	if err != nil {
		t.Fatalf("create storeB: %v", err)
	}
	defer storeB.Close()

	networkID := "test-swarm-xyz"

	engineA := NewSyncEngine(storeA, blobDirA, networkID, "node-A", modules.NewRecordBroadcaster())
	transportA := NewTransport(engineA, networkID, "node-A", 3232, 8)

	engineB := NewSyncEngine(storeB, blobDirB, networkID, "node-B", modules.NewRecordBroadcaster())
	transportB := NewTransport(engineB, networkID, "node-B", 3233, 8)

	// Populate Node A with 1 blob and 1 event linking to it
	now := time.Now().Unix()
	blobContent := []byte("this is a sample binary blob content for p2p sync")
	blobSum := sha256.Sum256(blobContent)
	blobHash := hex.EncodeToString(blobSum[:])
	destA := modules.BlobPath(blobDirA, blobHash)
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

	// Host Node A on test HTTP server
	serverA := httptest.NewServer(http.HandlerFunc(transportA.ServeHTTP))
	defer serverA.Close()

	// Extract port from serverA URL
	serverAddr := strings.TrimPrefix(serverA.URL, "http://")
	host, portStr, _ := strings.Cut(serverAddr, ":")
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	// Node B dials Node A
	transportB.DialPeer(PeerAddress{IP: host, Port: port})

	// Wait for connection and Bloom reconciliation (up to 2 seconds)
	deadline := time.Now().Add(2 * time.Second)
	synced := false
	for time.Now().Before(deadline) {
		recB, _ := storeB.GetRecord(testEvent.ID)
		metaB, _ := storeB.GetBlob(blobHash)
		if recB != nil && metaB != nil {
			synced = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if !synced {
		t.Fatalf("timed out waiting for Node B to sync event and blob from Node A")
	}

	// Verify Node B received the exact blob on disk
	destB := modules.BlobPath(blobDirB, blobHash)
	contentB, err := os.ReadFile(destB)
	if err != nil {
		t.Fatalf("read blob on B: %v", err)
	}
	if string(contentB) != string(blobContent) {
		t.Fatalf("blob content mismatch on B")
	}

	// Now test LIVE GOSSIP: Node B publishes a new event, Node A should receive it instantly
	eventFromB, err := createTestSignedRecord("chat", map[string]any{"text": "live gossip from Node B"}, now+1)
	if err != nil {
		t.Fatalf("create event B: %v", err)
	}
	if _, _, err := storeB.InsertRecord(eventFromB); err != nil {
		t.Fatalf("insert event on B: %v", err)
	}

	// Broadcast from B
	transportB.BroadcastEvent(eventFromB, "")

	// Node A should receive it
	deadlineGossip := time.Now().Add(1 * time.Second)
	gossipReceived := false
	for time.Now().Before(deadlineGossip) {
		recA, _ := storeA.GetRecord(eventFromB.ID)
		if recA != nil {
			gossipReceived = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if !gossipReceived {
		t.Fatalf("timed out waiting for Node A to receive live gossip event from Node B")
	}

	transportA.Stop()
	transportB.Stop()
}
