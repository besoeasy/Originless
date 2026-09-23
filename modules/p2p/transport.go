package p2p

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/besoeasy/originless/modules"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	ma "github.com/multiformats/go-multiaddr"
)

type deadlineRWC interface {
	io.ReadWriteCloser
	SetReadDeadline(time.Time) error
}

// PeerSession represents an active full-duplex P2P connection with a peer node.
type PeerSession struct {
	id          string
	remoteAddr  string
	conn        deadlineRWC
	engine      *SyncEngine
	transport   *Transport
	writeMu     sync.Mutex
	closeOnce   sync.Once
	isOutbound  bool
	remoteHello HelloPayload
	closed      chan struct{}
}

func newPeerSession(conn deadlineRWC, remoteAddr string, engine *SyncEngine, transport *Transport, isOutbound bool) *PeerSession {
	return &PeerSession{
		id:         remoteAddr,
		remoteAddr: remoteAddr,
		conn:       conn,
		engine:     engine,
		transport:  transport,
		isOutbound: isOutbound,
		closed:     make(chan struct{}),
	}
}

func (s *PeerSession) Send(msgType byte, payload []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return WriteFrame(s.conn, msgType, payload)
}

func (s *PeerSession) SetRemoteHello(hello HelloPayload) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.remoteHello = hello
}

func (s *PeerSession) Close() {
	s.closeOnce.Do(func() {
		close(s.closed)
		_ = s.conn.Close()
		if s.transport != nil {
			s.transport.unregisterSession(s.id)
		}
	})
}

func (s *PeerSession) start() {
	go s.readLoop()
	go s.keepAliveLoop()
}

func (s *PeerSession) readLoop() {
	defer s.Close()

	for {
		select {
		case <-s.closed:
			return
		default:
		}

		_ = s.conn.SetReadDeadline(time.Now().Add(90 * time.Second))
		msgType, payload, err := ReadFrame(s.conn)
		if err != nil {
			if err != io.EOF && !strings.Contains(err.Error(), "use of closed network connection") && !strings.Contains(err.Error(), "stream reset") {
				log.Printf("[P2P-SESSION] Read error from %s: %v", s.remoteAddr, err)
			}
			return
		}

		s.engine.Dispatch(s, msgType, payload)
	}
}

func (s *PeerSession) keepAliveLoop() {
	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.closed:
			return
		case <-ticker.C:
			if err := s.Send(MsgPing, nil); err != nil {
				return
			}
		}
	}
}

// Transport manages libp2p sync streams.
type Transport struct {
	engine     *SyncEngine
	networkID  string
	nodeID     string
	listenPort int
	host       host.Host
	sessions   map[string]*PeerSession
	mu         sync.RWMutex
	maxPeers   int
	stopChan   chan struct{}
}

func NewTransport(engine *SyncEngine, networkID, nodeID string, listenPort, maxPeers int) *Transport {
	if maxPeers <= 0 {
		maxPeers = DefaultMaxPeers
	}
	t := &Transport{
		engine:     engine,
		networkID:  networkID,
		nodeID:     nodeID,
		listenPort: listenPort,
		sessions:   make(map[string]*PeerSession),
		maxPeers:   maxPeers,
		stopChan:   make(chan struct{}),
	}
	engine.SetTransport(t)
	return t
}

func (t *Transport) AttachHost(h host.Host) {
	t.mu.Lock()
	t.host = h
	t.nodeID = h.ID().String()
	t.mu.Unlock()
	t.engine.nodeID = h.ID().String()

	h.SetStreamHandler(protocol.ID(SyncProtocolID), t.handleIncomingStream)
	h.Network().Notify(&network.NotifyBundle{
		ConnectedF: func(_ network.Network, c network.Conn) {
			go t.maybeOpenSync(c.RemotePeer())
		},
	})
}

func (t *Transport) ActivePeersCount() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.sessions)
}

func (t *Transport) registerSession(s *PeerSession) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	if len(t.sessions) >= t.maxPeers {
		return false
	}
	if _, exists := t.sessions[s.id]; exists {
		return false
	}
	t.sessions[s.id] = s
	log.Printf("[P2P-TRANSPORT] Peer connected: %s (total: %d)", s.remoteAddr, len(t.sessions))
	return true
}

func (t *Transport) unregisterSession(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, exists := t.sessions[id]; exists {
		delete(t.sessions, id)
		log.Printf("[P2P-TRANSPORT] Peer disconnected: %s (total: %d)", id, len(t.sessions))
	}
}

func (t *Transport) handleIncomingStream(s network.Stream) {
	pid := s.Conn().RemotePeer().String()
	session := newPeerSession(s, pid, t.engine, t, false)
	session.id = pid
	if !t.registerSession(session) {
		_ = s.Reset()
		return
	}
	session.start()
	t.sendHello(session)
}

func (t *Transport) maybeOpenSync(pid peer.ID) {
	if t.host == nil || pid == t.host.ID() {
		return
	}
	if strings.Compare(t.host.ID().String(), pid.String()) > 0 {
		return
	}
	t.mu.RLock()
	_, exists := t.sessions[pid.String()]
	atCap := len(t.sessions) >= t.maxPeers
	stopped := false
	select {
	case <-t.stopChan:
		stopped = true
	default:
	}
	t.mu.RUnlock()
	if exists || atCap || stopped {
		return
	}
	t.openSyncStream(pid)
}

func (t *Transport) openSyncStream(pid peer.ID) {
	if t.host == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	s, err := t.host.NewStream(ctx, pid, protocol.ID(SyncProtocolID))
	if err != nil {
		return
	}
	session := newPeerSession(s, pid.String(), t.engine, t, true)
	session.id = pid.String()
	if !t.registerSession(session) {
		_ = s.Reset()
		return
	}
	session.start()
	t.sendHello(session)
}

func (t *Transport) ConnectPeer(info peer.AddrInfo) {
	if t.host == nil || info.ID == "" || info.ID == t.host.ID() {
		return
	}
	t.mu.RLock()
	_, exists := t.sessions[info.ID.String()]
	atCap := len(t.sessions) >= t.maxPeers
	t.mu.RUnlock()
	if exists || atCap {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := t.host.Connect(ctx, info); err != nil {
		log.Printf("[P2P-TRANSPORT] connect %s: %v", info.ID, err)
		return
	}
	t.maybeOpenSync(info.ID)
}

func (t *Transport) DialPeerHint(hint PeerHint) {
	if hint.ID == "" || hint.ID == t.nodeID {
		return
	}
	pid, err := peer.Decode(hint.ID)
	if err != nil {
		return
	}
	info := peer.AddrInfo{ID: pid}
	addrs := hint.Addrs
	if len(addrs) > 8 {
		addrs = addrs[:8]
	}
	for _, a := range addrs {
		m, err := ma.NewMultiaddr(a)
		if err != nil {
			continue
		}
		info.Addrs = append(info.Addrs, m)
	}
	t.ConnectPeer(info)
}

func (t *Transport) sendHello(session *PeerSession) {
	evCount, evRoot, _ := t.engine.store.EventsStateRoot()
	bCount, bRoot, _ := t.engine.store.BlobsStateRoot()

	var peers []PeerHint
	t.mu.RLock()
	h := t.host
	for id, s := range t.sessions {
		if s.id == session.id {
			continue
		}
		hint := PeerHint{ID: id}
		if h != nil {
			if pid, err := peer.Decode(id); err == nil {
				for _, a := range h.Peerstore().Addrs(pid) {
					hint.Addrs = append(hint.Addrs, a.String())
				}
			}
		}
		peers = append(peers, hint)
	}
	t.mu.RUnlock()

	hello := HelloPayload{
		NodeID:     t.nodeID,
		NetworkID:  t.networkID,
		Version:    modules.Version,
		EventCount: evCount,
		BlobCount:  int(bCount),
		EventRoot:  evRoot,
		BlobRoot:   bRoot,
		ListenPort: t.listenPort,
		Peers:      peers,
	}
	data, _ := json.Marshal(hello)
	_ = session.Send(MsgHello, data)
}

func (t *Transport) BroadcastEvent(rec *modules.Record, excludePeerID string) {
	data, err := json.Marshal(rec)
	if err != nil {
		return
	}

	t.mu.RLock()
	targets := make([]*PeerSession, 0, len(t.sessions))
	for id, s := range t.sessions {
		if id == excludePeerID {
			continue
		}
		targets = append(targets, s)
	}
	t.mu.RUnlock()

	for _, s := range targets {
		_ = s.Send(MsgEventBroadcast, data)
	}
}

func (t *Transport) BroadcastBlob(hash string, size int64, excludePeerID string) {
	payload, err := json.Marshal(BlobBroadcastPayload{Hash: hash, Size: size})
	if err != nil {
		return
	}

	t.mu.RLock()
	targets := make([]*PeerSession, 0, len(t.sessions))
	for id, s := range t.sessions {
		if id == excludePeerID {
			continue
		}
		targets = append(targets, s)
	}
	t.mu.RUnlock()

	for _, s := range targets {
		_ = s.Send(MsgBlobBroadcast, payload)
	}
}

func (t *Transport) Stop() {
	select {
	case <-t.stopChan:
	default:
		close(t.stopChan)
	}

	t.mu.Lock()
	if t.host != nil {
		t.host.RemoveStreamHandler(protocol.ID(SyncProtocolID))
	}
	sessions := make([]*PeerSession, 0, len(t.sessions))
	for _, s := range t.sessions {
		sessions = append(sessions, s)
	}
	t.sessions = make(map[string]*PeerSession)
	t.mu.Unlock()

	for _, s := range sessions {
		s.Close()
	}
}
