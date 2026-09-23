package p2p

import (
	"context"
	"log"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/besoeasy/originless/modules"
	"github.com/libp2p/go-libp2p/core/event"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
)

// Options configures a P2P manager. Production uses NewManager; tests set extra fields.
type Options struct {
	Store            *modules.Store
	BlobDir          string
	DataDir          string
	Broadcaster      *modules.RecordBroadcaster
	ListenPort       int
	ListenHost       string
	HTTPHandler      http.Handler
	ForcePublic      bool
	ForcePrivate     bool
	RelayHop         *bool
	DisableMDNS      bool
	DisableDHT       bool
	DisableQUIC      bool
	DisableAutoRelay bool
	StaticRelays     []peer.AddrInfo
	DiskGuard        *modules.DiskGuard
}

// Manager coordinates configuration, peer discovery, transport, and data synchronization.
type Manager struct {
	config       Config
	opts         Options
	store        *modules.Store
	broadcaster  *modules.RecordBroadcaster
	engine       *SyncEngine
	transport    *Transport
	host         host.Host
	discovery    *meshDiscovery
	nodeID       string
	ownsHTTP     bool
	reachability atomic.Value // network.Reachability
	ctx          context.Context
	cancel       context.CancelFunc
	stopChan     chan struct{}
	wg           sync.WaitGroup
}

// NewManager creates a P2P manager if NETWORK_ID is set.
func NewManager(store *modules.Store, blobDir string, broadcaster *modules.RecordBroadcaster, listenPort int, dataDir string) *Manager {
	if listenPort <= 0 {
		listenPort = DefaultP2PPort
	}
	return NewManagerWithOptions(Options{
		Store:       store,
		BlobDir:     blobDir,
		DataDir:     dataDir,
		Broadcaster: broadcaster,
		ListenPort:  listenPort,
	})
}

// NewManagerWithOptions creates a manager from explicit options (used by tests).
func NewManagerWithOptions(opts Options) *Manager {
	cfg := LoadConfig()
	if !cfg.Enabled {
		return nil
	}
	relayHop := true
	if opts.RelayHop != nil {
		relayHop = *opts.RelayHop
	}

	engine := NewSyncEngine(opts.Store, opts.BlobDir, cfg.NetworkID, "", opts.Broadcaster)
	if opts.DiskGuard != nil {
		engine.SetDiskGuard(opts.DiskGuard)
	}
	transport := NewTransport(engine, cfg.NetworkID, "", opts.ListenPort, cfg.MaxPeers)

	m := &Manager{
		config:      cfg,
		opts:        opts,
		store:       opts.Store,
		broadcaster: opts.Broadcaster,
		engine:      engine,
		transport:   transport,
		stopChan:    make(chan struct{}),
	}
	m.opts.RelayHop = &relayHop
	m.reachability.Store(network.ReachabilityUnknown)
	return m
}

func (m *Manager) SetHTTPHandler(h http.Handler) {
	if m == nil {
		return
	}
	m.opts.HTTPHandler = h
}

func (m *Manager) SetBroadcaster(b *modules.RecordBroadcaster) {
	if m == nil {
		return
	}
	m.broadcaster = b
	if m.engine != nil {
		m.engine.SetBroadcaster(b)
	}
}

// SetDiskGuard attaches the admission ledger to the sync engine.
func (m *Manager) SetDiskGuard(g *modules.DiskGuard) {
	if m == nil {
		return
	}
	m.opts.DiskGuard = g
	if m.engine != nil {
		m.engine.SetDiskGuard(g)
	}
}

func (m *Manager) OwnsListener() bool {
	return m != nil && m.ownsHTTP
}

func (m *Manager) Host() host.Host {
	if m == nil {
		return nil
	}
	return m.host
}

func (m *Manager) AddrInfo() peer.AddrInfo {
	if m == nil || m.host == nil {
		return peer.AddrInfo{}
	}
	return peer.AddrInfo{ID: m.host.ID(), Addrs: m.host.Addrs()}
}

func (m *Manager) Connect(ctx context.Context, info peer.AddrInfo) {
	if m == nil || m.transport == nil {
		return
	}
	m.transport.ConnectPeer(info)
}

func (m *Manager) Start() error {
	if m == nil || !m.config.Enabled {
		return nil
	}

	priv, err := LoadOrCreateIdentity(m.opts.DataDir)
	if err != nil {
		return err
	}

	hop := true
	if m.opts.RelayHop != nil {
		hop = *m.opts.RelayHop
	}

	h, err := buildHost(priv, m.config, hostSettings{
		port:             m.opts.ListenPort,
		listenHost:       m.opts.ListenHost,
		httpHandler:      m.opts.HTTPHandler,
		bootstrap:        m.config.BootstrapPeers,
		forcePublic:      m.opts.ForcePublic,
		forcePrivate:     m.opts.ForcePrivate,
		relayHop:         hop,
		disableQUIC:      m.opts.DisableQUIC,
		disableAutoRelay: m.opts.DisableAutoRelay,
		staticRelays:     m.opts.StaticRelays,
	})
	if err != nil {
		return err
	}

	m.host = h
	m.nodeID = h.ID().String()
	m.ownsHTTP = m.opts.HTTPHandler != nil
	m.ctx, m.cancel = context.WithCancel(context.Background())
	m.transport.AttachHost(h)

	if m.opts.ForcePublic {
		m.reachability.Store(network.ReachabilityPublic)
	} else if m.opts.ForcePrivate {
		m.reachability.Store(network.ReachabilityPrivate)
	}

	disc, err := startMeshDiscovery(m.ctx, h, m.config.NetworkID, m.config.BootstrapPeers, m.opts.DisableDHT, m.opts.DisableMDNS, func(info peer.AddrInfo) {
		m.transport.ConnectPeer(info)
	})
	if err != nil {
		_ = h.Close()
		return err
	}
	m.discovery = disc

	go m.watchReachability()

	m.wg.Add(1)
	go m.reconciliationLoop()

	log.Printf("[P2P] Starting node in network=%s (peer_id=%s) addrs=%v", m.config.NetworkID, m.nodeID, h.Addrs())
	return nil
}

func (m *Manager) Stop() {
	if m == nil || !m.config.Enabled {
		return
	}
	select {
	case <-m.stopChan:
	default:
		close(m.stopChan)
	}
	if m.cancel != nil {
		m.cancel()
	}
	if m.discovery != nil {
		m.discovery.Stop()
	}
	if m.transport != nil {
		m.transport.Stop()
	}
	if m.host != nil {
		_ = m.host.Close()
	}
	m.wg.Wait()
	log.Printf("[P2P] Stopped P2P subsystem")
}

func (m *Manager) BroadcastRecord(rec *modules.Record) {
	if m == nil || !m.config.Enabled || rec == nil {
		return
	}
	if m.engine.seenCache.Add(rec.ID) {
		m.transport.BroadcastEvent(rec, "")
	}
}

func (m *Manager) BroadcastBlob(hash string, size int64) {
	if m == nil || !m.config.Enabled || hash == "" {
		return
	}
	if m.engine.seenCache.Add(hash) {
		m.transport.BroadcastBlob(hash, size, "")
	}
}

func (m *Manager) Status() map[string]any {
	if m == nil || !m.config.Enabled {
		return map[string]any{
			"enabled": false,
		}
	}

	evSynced, bSynced, lastSync := m.engine.Stats()
	r := network.ReachabilityUnknown
	if v := m.reachability.Load(); v != nil {
		r = v.(network.Reachability)
	}

	var listen []string
	var observed []string
	if m.host != nil {
		for _, a := range m.host.Addrs() {
			listen = append(listen, a.String())
		}
		for _, a := range m.host.Network().Peerstore().Addrs(m.host.ID()) {
			observed = append(observed, a.String())
		}
	}

	hop := r == network.ReachabilityPublic
	if m.opts.RelayHop != nil {
		hop = hop && *m.opts.RelayHop
	}

	peers := 0
	if m.transport != nil {
		peers = m.transport.ActivePeersCount()
	}

	return map[string]any{
		"enabled":         true,
		"network_id":      m.config.NetworkID,
		"peer_id":         m.nodeID,
		"node_id":         m.nodeID,
		"listen_port":     m.opts.ListenPort,
		"listen_addrs":    listen,
		"observed_addrs":  observed,
		"nat":             reachabilityString(r),
		"relay_hop":       hop,
		"max_peers":       m.config.MaxPeers,
		"peers_connected": peers,
		"events_synced":   evSynced,
		"blobs_synced":    bSynced,
		"last_sync":       lastSync,
	}
}

func (m *Manager) watchReachability() {
	if m.host == nil {
		return
	}
	sub, err := m.host.EventBus().Subscribe(new(event.EvtLocalReachabilityChanged))
	if err != nil {
		return
	}
	defer sub.Close()
	for {
		select {
		case <-m.stopChan:
			return
		case e, ok := <-sub.Out():
			if !ok {
				return
			}
			ev, ok := e.(event.EvtLocalReachabilityChanged)
			if !ok {
				continue
			}
			m.reachability.Store(ev.Reachability)
			log.Printf("[P2P] reachability=%s", reachabilityString(ev.Reachability))
		}
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
