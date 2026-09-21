package p2p

import (
	"crypto/rand"
	"encoding/hex"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/besoeasy/originless/modules"
)

// Manager coordinates configuration, peer discovery, transport, and data synchronization.
type Manager struct {
	config      Config
	store       *modules.Store
	broadcaster *modules.RecordBroadcaster
	engine      *SyncEngine
	transport   *Transport
	discovery   *Discovery
	nodeID      string
	stopChan    chan struct{}
	wg          sync.WaitGroup
}

// NewManager creates a P2P manager if NETWORK_ID is set.
// If NETWORK_ID is empty or unset, returns nil (P2P disabled).
func NewManager(store *modules.Store, blobDir string, broadcaster *modules.RecordBroadcaster, listenPort int) *Manager {
	cfg := LoadConfig()
	if !cfg.Enabled {
		return nil
	}

	// Generate random 16-hex node ID
	var idBytes [8]byte
	_, _ = rand.Read(idBytes[:])
	nodeID := hex.EncodeToString(idBytes[:])

	if listenPort <= 0 {
		listenPort = DefaultP2PPort
	}

	engine := NewSyncEngine(store, blobDir, cfg.NetworkID, nodeID, broadcaster)
	transport := NewTransport(engine, cfg.NetworkID, nodeID, listenPort, cfg.MaxPeers)

	return &Manager{
		config:      cfg,
		store:       store,
		broadcaster: broadcaster,
		engine:      engine,
		transport:   transport,
		nodeID:      nodeID,
		stopChan:    make(chan struct{}),
	}
}

// SetBroadcaster connects the local SSE record broadcaster to the P2P engine.
func (m *Manager) SetBroadcaster(b *modules.RecordBroadcaster) {
	if m == nil {
		return
	}
	m.broadcaster = b
	if m.engine != nil {
		m.engine.SetBroadcaster(b)
	}
}

// Start launches the BitTorrent DHT, LSD discovery, and background sync worker.
func (m *Manager) Start() error {
	if m == nil || !m.config.Enabled {
		return nil
	}

	log.Printf("[P2P] Starting node in network=%s (node_id=%s)", m.config.NetworkID, m.nodeID)

	disc, err := NewDiscovery(m.config.NetworkID, m.config.Port, func(addr PeerAddress) {
		m.transport.DialPeer(addr)
	})
	if err != nil {
		return err
	}
	m.discovery = disc
	disc.Start()

	m.wg.Add(1)
	go m.reconciliationLoop()

	return nil
}

// Stop shuts down the P2P subsystem.
func (m *Manager) Stop() {
	if m == nil || !m.config.Enabled {
		return
	}
	close(m.stopChan)
	if m.discovery != nil {
		m.discovery.Stop()
	}
	if m.transport != nil {
		m.transport.Stop()
	}
	m.wg.Wait()
	log.Printf("[P2P] Stopped P2P subsystem")
}

// Handler returns the HTTP handler for GET /p2p.
func (m *Manager) Handler() http.Handler {
	if m == nil || !m.config.Enabled {
		return nil
	}
	return m.transport
}

// ServeHTTP implements http.Handler for GET /p2p.
func (m *Manager) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if m == nil || !m.config.Enabled || m.transport == nil {
		http.NotFound(w, r)
		return
	}
	m.transport.ServeHTTP(w, r)
}

// BroadcastRecord broadcasts a newly published event across connected peers in real time.
func (m *Manager) BroadcastRecord(rec *modules.Record) {
	if m == nil || !m.config.Enabled || rec == nil {
		return
	}
	// Record in seen cache so we don't re-process if a peer echoes it
	if m.engine.seenCache.Add(rec.ID) {
		m.transport.BroadcastEvent(rec, "")
	}
}

// BroadcastBlob broadcasts a newly uploaded blob announcement across connected peers in real time.
func (m *Manager) BroadcastBlob(hash string, size int64) {
	if m == nil || !m.config.Enabled || hash == "" {
		return
	}
	if m.engine.seenCache.Add(hash) {
		m.transport.BroadcastBlob(hash, size, "")
	}
}

// Status returns a telemetry map for reporting in /status.
func (m *Manager) Status() map[string]any {
	if m == nil || !m.config.Enabled {
		return map[string]any{
			"enabled": false,
		}
	}

	evSynced, bSynced, lastSync := m.engine.Stats()
	return map[string]any{
		"enabled":         true,
		"network_id":      m.config.NetworkID,
		"node_id":         m.nodeID,
		"peers_connected": m.transport.ActivePeersCount(),
		"events_synced":   evSynced,
		"blobs_synced":    bSynced,
		"last_sync":       lastSync,
	}
}

func (m *Manager) reconciliationLoop() {
	defer m.wg.Done()

	ticker := time.NewTicker(DefaultSyncInterval)
	defer ticker.Stop()

	for {
		select {
		case <-m.stopChan:
			return
		case <-ticker.C:
			m.reconcileAll()
		}
	}
}

func (m *Manager) reconcileAll() {
	m.transport.mu.RLock()
	sessions := make([]*PeerSession, 0, len(m.transport.sessions))
	for _, s := range m.transport.sessions {
		sessions = append(sessions, s)
	}
	m.transport.mu.RUnlock()

	for _, s := range sessions {
		go m.engine.startReconciliation(s)
	}
}
