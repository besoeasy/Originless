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
	guard        *modules.DiskGuard
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

// SetDiskGuard attaches the admission ledger. The sync path admits but
// never evicts: a refused record is simply skipped and re-offered later.
func (se *SyncEngine) SetDiskGuard(g *modules.DiskGuard) {
	se.mu.Lock()
	defer se.mu.Unlock()
	se.guard = g
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
		"owner":      rec.Owner,
		"collection": rec.Collection,
		"created_at": rec.CreatedAt,
		"expires_at": rec.ExpiresAt,
		"data":       rec.Data,
		"labels":     rec.Labels,
		"sig":        rec.Sig,
	}
	if rec.Blob != "" {
		inputMap["blob"] = rec.Blob
	}
	rawJSON, err := json.Marshal(inputMap)
	if err != nil {
		return false, fmt.Errorf("marshal for validation: %w", err)
	}

	// Zero-Trust verification: recompute ID, verify Ed25519 signature, check TTL
	nowUnix := time.Now().Unix()
	validRec, err := modules.ValidateRecordBody(rawJSON, nowUnix)
	if err != nil {
		return false, fmt.Errorf("cryptographic validation failed: %w", err)
	}

	if validRec.ExpiresAt <= nowUnix {
		return false, fmt.Errorf("record is already expired")
	}

	// Admission: the swarm is an unmetered writer unless gated. Refusals
	// are silent skips — the peer re-offers later, and this path never
	// triggers eviction.
	se.mu.RLock()
	g := se.guard
	se.mu.RUnlock()
	want := validRec.Size
	if want <= 0 {
		want = int64(len(rawJSON))
	}
	if g != nil {
		if !g.Admit(want) {
			se.seenCache.Remove(rec.ID)
			return false, fmt.Errorf("disk ceiling would be exceeded")
		}
		defer g.Release(want)
	}

	created, _, err := se.store.InsertRecord(validRec)
	if err != nil {
		se.seenCache.Remove(rec.ID)
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

	// Admission before touching disk. Oversize blobs are refused outright;
	// the reservation covers the temp-file window.
	se.mu.RLock()
	g := se.guard
	se.mu.RUnlock()
	if int64(len(data)) > modules.MaxBlobBytes {
		return false, fmt.Errorf("blob exceeds MAX_BLOB_BYTES")
	}
	if g != nil {
		if !g.Admit(int64(len(data))) {
			se.seenCache.Remove(hash)
			return false, fmt.Errorf("disk ceiling would be exceeded")
		}
		defer g.Release(int64(len(data)))
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
		se.seenCache.Remove(hash)
		return false, fmt.Errorf("create temp blob: %w", err)
	}
	tmpName := tmpFile.Name()
	defer os.Remove(tmpName)

	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		se.seenCache.Remove(hash)
		return false, err
	}
	if err := tmpFile.Close(); err != nil {
		se.seenCache.Remove(hash)
		return false, err
	}

	_ = os.Chmod(tmpName, 0o644)
	if err := os.Rename(tmpName, dest); err != nil {
		se.seenCache.Remove(hash)
		return false, fmt.Errorf("rename temp blob: %w", err)
	}

	created, err := se.store.UpsertBlob(hash, int64(len(data)))
	if err != nil {
		se.seenCache.Remove(hash)
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

		// Peer Exchange (PEX): dial shared peers (bounded to avoid dial storms)
		if len(hello.Peers) > 0 && session.transport != nil {
			hints := hello.Peers
			if len(hints) > 16 {
				hints = hints[:16]
			}
			for _, hint := range hints {
				go session.transport.DialPeerHint(hint)
			}
		}

		// Start reconciliation: fallback to direct bloom for legacy peers, else use state root fast-check
		if hello.EventRoot == "" && hello.BlobRoot == "" {
			go se.sendEventsBloomFilter(session)
			go se.sendBlobsBloomFilter(session)
		} else {
			go se.startReconciliation(session)
		}

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
		// Continuous burst: if sender hit batch limit (500 records), immediately
		// request next chunk without waiting for the 60s periodic reconciliation tick.
		if len(recs) >= 500 {
			go se.sendEventsBloomFilter(session)
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
		if r.Blob != "" && !se.seenCache.Has(r.Blob) {
			dest := modules.BlobPath(se.blobDir, r.Blob)
			if _, statErr := os.Stat(dest); os.IsNotExist(statErr) {
				req, _ := json.Marshal(BlobPullPayload{Hash: r.Blob})
				_ = session.Send(MsgBlobPull, req)
			}
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
		// If batch limit was reached, schedule another check once blobs are fetched
		if len(resp.MissingHashes) >= 100 {
			go func() {
				time.Sleep(5 * time.Second)
				se.sendBlobsBloomFilter(session)
			}()
		}

	case MsgBlobPull:
		var req BlobPullPayload
		if err := json.Unmarshal(payload, &req); err != nil || req.Hash == "" {
			return
		}
		go func() {
			blobBytes, err := se.ReadBlob(req.Hash)
			if err != nil {
				return
			}
			// Send blob data: 64 bytes hash + raw bytes
			var frame bytes.Buffer
			frame.WriteString(req.Hash)
			frame.Write(blobBytes)
			_ = session.Send(MsgBlobData, frame.Bytes())
		}()

	case MsgBlobData:
		if len(payload) < 64 {
			return
		}
		hash := string(payload[0:64])
		data := payload[64:]
		go func() {
			created, err := se.IngestBlob(hash, data)
			if err == nil && created && se.transport != nil {
				se.transport.BroadcastBlob(hash, int64(len(data)), session.id)
			}
		}()

	case MsgBlobBroadcast:
		var bcast BlobBroadcastPayload
		if err := json.Unmarshal(payload, &bcast); err != nil || bcast.Hash == "" {
			return
		}
		if !se.seenCache.Has(bcast.Hash) {
			dest := modules.BlobPath(se.blobDir, bcast.Hash)
			if _, statErr := os.Stat(dest); os.IsNotExist(statErr) {
				// Request this new blob from the announcing peer
				req, _ := json.Marshal(BlobPullPayload{Hash: bcast.Hash})
				_ = session.Send(MsgBlobPull, req)
			}
		}

	case MsgSyncCheck:
		var peerSync SyncCheckPayload
		if err := json.Unmarshal(payload, &peerSync); err != nil {
			return
		}
		evCount, evRoot, _ := se.store.EventsStateRoot()
		bCount, bRoot, _ := se.store.BlobsStateRoot()

		// Always reply with local state roots so requesting peer can also evaluate
		resp, _ := json.Marshal(SyncCheckPayload{
			EventCount: evCount,
			EventRoot:  evRoot,
			BlobCount:  bCount,
			BlobRoot:   bRoot,
		})
		_ = session.Send(MsgSyncCheckResp, resp)

		// Reconcile events only if roots differ
		if peerSync.EventCount != evCount || peerSync.EventRoot != evRoot {
			se.sendEventsBloomFilter(session)
		}

		// Reconcile blobs only if roots differ
		if peerSync.BlobCount != bCount || peerSync.BlobRoot != bRoot {
			se.sendBlobsBloomFilter(session)
		}

	case MsgSyncCheckResp:
		var peerSync SyncCheckPayload
		if err := json.Unmarshal(payload, &peerSync); err != nil {
			return
		}
		evCount, evRoot, _ := se.store.EventsStateRoot()
		bCount, bRoot, _ := se.store.BlobsStateRoot()

		// Reconcile events only if roots differ
		if peerSync.EventCount != evCount || peerSync.EventRoot != evRoot {
			se.sendEventsBloomFilter(session)
		}

		// Reconcile blobs only if roots differ
		if peerSync.BlobCount != bCount || peerSync.BlobRoot != bRoot {
			se.sendBlobsBloomFilter(session)
		}

	case MsgPing:
		_ = session.Send(MsgPong, nil)

	case MsgPong:
		// Heartbeat received
	}
}

// sendEventsBloomFilter builds and sends our Event Bloom filter to the peer.
func (se *SyncEngine) sendEventsBloomFilter(session *PeerSession) {
	if evFilter, err := se.BuildEventsBloomFilter(); err == nil {
		_ = session.Send(MsgBloomEventsReq, evFilter.Bytes())
	}
}

// sendBlobsBloomFilter builds and sends our Blob Bloom filter to the peer.
func (se *SyncEngine) sendBlobsBloomFilter(session *PeerSession) {
	if blobFilter, err := se.BuildBlobsBloomFilter(); err == nil {
		_ = session.Send(MsgBloomBlobsReq, blobFilter.Bytes())
	}
}

// startReconciliation initiates synchronization with a peer.
// It first attempts a lightweight O(1) state-root fast check via MsgSyncCheck.
func (se *SyncEngine) startReconciliation(session *PeerSession) {
	evCount, evRoot, _ := se.store.EventsStateRoot()
	bCount, bRoot, _ := se.store.BlobsStateRoot()

	check := SyncCheckPayload{
		EventCount: evCount,
		EventRoot:  evRoot,
		BlobCount:  bCount,
		BlobRoot:   bRoot,
	}
	data, err := json.Marshal(check)
	if err == nil {
		_ = session.Send(MsgSyncCheck, data)
	}
}
