package p2p

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/besoeasy/originless/modules"
)

// SyncEngine coordinates bidirectional event & blob reconciliation,
// live gossip broadcasting, and zero-trust cryptographic ingestion.
type SyncEngine struct {
	store        *modules.Store
	blobDir      string
	networkID    string
	nodeID       string
	seenCache    *SeenCache
	broadcaster  *modules.RecordBroadcaster
	eventsSynced int64
	blobsSynced  int64
	lastSync     time.Time
	mu           sync.RWMutex
	transport    *Transport
}

// NewSyncEngine initializes a sync engine.
func NewSyncEngine(store *modules.Store, blobDir, networkID, nodeID string, broadcaster *modules.RecordBroadcaster) *SyncEngine {
	return &SyncEngine{
		store:       store,
		blobDir:     blobDir,
		networkID:   networkID,
		nodeID:      nodeID,
		seenCache:   NewSeenCache(DefaultGossipCacheSize),
		broadcaster: broadcaster,
	}
}

func (se *SyncEngine) SetTransport(t *Transport) {
	se.transport = t
}

func (se *SyncEngine) SetBroadcaster(b *modules.RecordBroadcaster) {
	se.broadcaster = b
}

// Stats returns sync counters and metadata for /status reporting.
func (se *SyncEngine) Stats() (eventsSynced int64, blobsSynced int64, lastSync string) {
	se.mu.RLock()
	defer se.mu.RUnlock()
	ls := ""
	if !se.lastSync.IsZero() {
		ls = se.lastSync.UTC().Format(time.RFC3339)
	}
	return se.eventsSynced, se.blobsSynced, ls
}

// BuildEventsBloomFilter creates a Bloom filter containing all unexpired event IDs in SQLite.
func (se *SyncEngine) BuildEventsBloomFilter() (*BloomFilter, error) {
	ids, err := se.store.ListRecordIDs(true)
	if err != nil {
		return nil, fmt.Errorf("list record IDs: %w", err)
	}

	bf := NewBloomFilter(len(ids)+64, 0.01)
	for _, id := range ids {
		bf.AddString(id)
	}
	return bf, nil
}

// BuildBlobsBloomFilter creates a Bloom filter containing all SHA-256 blob hashes in SQLite.
func (se *SyncEngine) BuildBlobsBloomFilter() (*BloomFilter, error) {
	hashes, err := se.store.ListBlobHashes()
	if err != nil {
		return nil, fmt.Errorf("list blob hashes: %w", err)
	}

	bf := NewBloomFilter(len(hashes)+64, 0.01)
	for _, h := range hashes {
		bf.AddString(h)
	}
	return bf, nil
}

// IngestRecord re-validates a received event cryptographically and inserts it into SQLite.
func (se *SyncEngine) IngestRecord(rec *modules.Record) (bool, error) {
	if rec == nil || rec.ID == "" {
		return false, fmt.Errorf("nil or empty record")
	}

	// Loop suppression
	if !se.seenCache.Add(rec.ID) {
		return false, nil
	}

	// Serialize record into input format (omitting server-computed fields)
	inputMap := map[string]any{
		"owner":       rec.Owner,
		"collection":  rec.Collection,
		"created_at":  rec.CreatedAt,
		"expires_at":  rec.ExpiresAt,
		"data":        rec.Data,
		"labels":      rec.Labels,
		"sig":         rec.Sig,
	}
	rawJSON, err := json.Marshal(inputMap)
	if err != nil {
		return false, fmt.Errorf("marshal for validation: %w", err)
	}

	// Zero-Trust verification: recompute ID, verify Ed25519 signature, check TTL
	validRec, err := modules.ValidateRecordBody(rawJSON, time.Now().Unix())
	if err != nil {
		return false, fmt.Errorf("cryptographic validation failed: %w", err)
	}

	created, _, err := se.store.InsertRecord(validRec)
	if err != nil {
		return false, fmt.Errorf("insert record: %w", err)
	}

	if created {
		se.mu.Lock()
		se.eventsSynced++
		se.lastSync = time.Now()
		se.mu.Unlock()

		// Stream to local Server-Sent Events clients
		if se.broadcaster != nil {
			se.broadcaster.Broadcast(validRec)
		}
	}

	return created, nil
}

// IngestBlob verifies the SHA-256 hash and writes the blob file to disk and SQLite.
func (se *SyncEngine) IngestBlob(hash string, data []byte) (bool, error) {
	if len(hash) != 64 {
		return false, fmt.Errorf("invalid blob hash length")
	}

	// Loop suppression
	if !se.seenCache.Add(hash) {
		return false, nil
	}

	// Verify content integrity
	sum := sha256.Sum256(data)
	computedHash := hex.EncodeToString(sum[:])
	if computedHash != hash {
		return false, fmt.Errorf("blob integrity mismatch: got %s, want %s", computedHash, hash)
	}

	// Atomic write: write to temp file then rename
	dest := modules.BlobPath(se.blobDir, hash)
	if _, err := os.Stat(dest); err == nil {
		// Already on disk
		_, _ = se.store.UpsertBlob(hash, int64(len(data)))
		return false, nil
	}

	tmpFile, err := os.CreateTemp(se.blobDir, "sync-up-*")
	if err != nil {
		return false, fmt.Errorf("create temp blob: %w", err)
	}
	tmpName := tmpFile.Name()
	defer os.Remove(tmpName)

	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return false, err
	}
	if err := tmpFile.Close(); err != nil {
		return false, err
	}

	_ = os.Chmod(tmpName, 0o644)
	if err := os.Rename(tmpName, dest); err != nil {
		return false, fmt.Errorf("rename temp blob: %w", err)
	}

	created, err := se.store.UpsertBlob(hash, int64(len(data)))
	if err != nil {
		return false, fmt.Errorf("upsert blob in db: %w", err)
	}

	if created {
		se.mu.Lock()
		se.blobsSynced++
		se.lastSync = time.Now()
		se.mu.Unlock()
	}

	return created, nil
}

// ReadBlob reads a stored blob file by hash.
func (se *SyncEngine) ReadBlob(hash string) ([]byte, error) {
	dest := modules.BlobPath(se.blobDir, hash)
	return os.ReadFile(dest)
}

// Dispatch handles an incoming protocol frame from a peer session.
func (se *SyncEngine) Dispatch(session *PeerSession, msgType byte, payload []byte) {
	switch msgType {
	case MsgHello:
		var hello HelloPayload
		if err := json.Unmarshal(payload, &hello); err != nil {
			log.Printf("[P2P-SYNC] Bad HELLO from %s: %v", session.remoteAddr, err)
			session.Close()
			return
		}
		if hello.NodeID == se.nodeID {
			// Loopback connection to self, close quietly
			session.Close()
			return
		}
		if hello.NetworkID != se.networkID {
			log.Printf("[P2P-SYNC] Network ID mismatch from %s (%s != %s)", session.remoteAddr, hello.NetworkID, se.networkID)
			session.Close()
			return
		}
		session.SetRemoteHello(hello)

		// Peer Exchange (PEX): dial shared peers
		if len(hello.Peers) > 0 && session.transport != nil {
			for _, peerAddr := range hello.Peers {
				go session.transport.DialPeer(peerAddr)
			}
		}

		// Start reconciliation: send our Event & Blob Bloom filters
		go se.startReconciliation(session)

	case MsgBloomEventsReq:
		bf, err := DeserializeBloomFilter(payload)
		if err != nil {
			return
		}
		// Find unexpired events we have that the peer does NOT have
		ids, err := se.store.ListRecordIDs(true)
		if err != nil {
			return
		}
		var recordsToPush []*modules.Record
		for _, id := range ids {
			if !bf.ContainsString(id) {
				if r, err := se.store.GetRecord(id); err == nil && r != nil {
					recordsToPush = append(recordsToPush, r)
					if len(recordsToPush) >= 500 {
						break
					}
				}
			}
		}
		if len(recordsToPush) > 0 {
			data, _ := json.Marshal(recordsToPush)
			_ = session.Send(MsgEventPush, data)
		}

	case MsgEventPush:
		var recs []*modules.Record
		if err := json.Unmarshal(payload, &recs); err != nil {
			return
		}
		for _, r := range recs {
			_, _ = se.IngestRecord(r)
		}

	case MsgEventBroadcast:
		var r modules.Record
		if err := json.Unmarshal(payload, &r); err != nil {
			return
		}
		created, err := se.IngestRecord(&r)
		if err == nil && created && se.transport != nil {
			// Forward to other peers (excluding sender)
			se.transport.BroadcastEvent(&r, session.id)
		}

	case MsgBloomBlobsReq:
		bf, err := DeserializeBloomFilter(payload)
		if err != nil {
			return
		}
		hashes, err := se.store.ListBlobHashes()
		if err != nil {
			return
		}
		var missingHashes []string
		for _, h := range hashes {
			if !bf.ContainsString(h) {
				missingHashes = append(missingHashes, h)
				if len(missingHashes) >= 100 {
					break
				}
			}
		}
		respPayload, _ := json.Marshal(MissingBlobsPayload{MissingHashes: missingHashes})
		_ = session.Send(MsgBloomBlobsResp, respPayload)

	case MsgBloomBlobsResp:
		var resp MissingBlobsPayload
		if err := json.Unmarshal(payload, &resp); err != nil {
			return
		}
		for _, h := range resp.MissingHashes {
			// Request missing blob
			req, _ := json.Marshal(BlobPullPayload{Hash: h})
			_ = session.Send(MsgBlobPull, req)
		}

	case MsgBlobPull:
		var req BlobPullPayload
		if err := json.Unmarshal(payload, &req); err != nil || req.Hash == "" {
			return
		}
		blobBytes, err := se.ReadBlob(req.Hash)
		if err != nil {
			return
		}
		// Send blob data: 64 bytes hash + raw bytes
		var frame bytes.Buffer
		frame.WriteString(req.Hash)
		frame.Write(blobBytes)
		_ = session.Send(MsgBlobData, frame.Bytes())

	case MsgBlobData:
		if len(payload) < 64 {
			return
		}
		hash := string(payload[0:64])
		data := payload[64:]
		created, err := se.IngestBlob(hash, data)
		if err == nil && created && se.transport != nil {
			se.transport.BroadcastBlob(hash, int64(len(data)), session.id)
		}

	case MsgBlobBroadcast:
		var bcast BlobBroadcastPayload
		if err := json.Unmarshal(payload, &bcast); err != nil || bcast.Hash == "" {
			return
		}
		if !se.seenCache.Has(bcast.Hash) {
			// Request this new blob from the announcing peer
			req, _ := json.Marshal(BlobPullPayload{Hash: bcast.Hash})
			_ = session.Send(MsgBlobPull, req)
		}

	case MsgPing:
		_ = session.Send(MsgPong, nil)

	case MsgPong:
		// Heartbeat received
	}
}

// startReconciliation generates our Bloom filters and exchanges them with the peer.
func (se *SyncEngine) startReconciliation(session *PeerSession) {
	// 1. Send Event Bloom Filter
	if evFilter, err := se.BuildEventsBloomFilter(); err == nil {
		_ = session.Send(MsgBloomEventsReq, evFilter.Bytes())
	}

	// 2. Send Blob Bloom Filter
	if blobFilter, err := se.BuildBlobsBloomFilter(); err == nil {
		_ = session.Send(MsgBloomBlobsReq, blobFilter.Bytes())
	}
}
