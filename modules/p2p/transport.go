package p2p

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/besoeasy/originless/modules"
)

// PeerSession represents an active full-duplex P2P connection with a peer node.
type PeerSession struct {
	id          string
	remoteAddr  string
	conn        net.Conn
	engine      *SyncEngine
	transport   *Transport
	writeMu     sync.Mutex
	closeOnce   sync.Once
	isOutbound  bool
	remoteHello HelloPayload
	closed      chan struct{}
}

func newPeerSession(conn net.Conn, remoteAddr string, engine *SyncEngine, transport *Transport, isOutbound bool) *PeerSession {
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

// Send serializes and writes a protocol frame to the peer.
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

// Close gracefully closes the peer session.
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
			if err != io.EOF && !strings.Contains(err.Error(), "use of closed network connection") {
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

// Transport manages P2P incoming HTTP upgrades and outgoing peer dials.
type Transport struct {
	engine     *SyncEngine
	networkID  string
	nodeID     string
	listenPort int
	sessions   map[string]*PeerSession
	mu         sync.RWMutex
	maxPeers   int
	stopChan   chan struct{}
}

// NewTransport creates a new Transport layer.
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

// ActivePeersCount returns the number of currently connected peers.
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

// ServeHTTP handles incoming P2P connection upgrades on GET /p2p.
func (t *Transport) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	netID := r.Header.Get("Originless-Network-ID")
	if netID != t.networkID {
		http.Error(w, "Forbidden: network mismatch", http.StatusForbidden)
		return
	}

	remoteNodeID := r.Header.Get("Originless-Node-ID")
	if remoteNodeID != "" && remoteNodeID == t.nodeID {
		http.Error(w, "Self connection", http.StatusBadRequest)
		return
	}

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "Webserver doesn't support hijacking", http.StatusInternalServerError)
		return
	}

	conn, _, err := hijacker.Hijack()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Write HTTP 101 Switching Protocols response
	resp := fmt.Sprintf("HTTP/1.1 101 Switching Protocols\r\n"+
		"Upgrade: originless-p2p\r\n"+
		"Connection: Upgrade\r\n"+
		"Originless-Network-ID: %s\r\n"+
		"Originless-Node-ID: %s\r\n\r\n", t.networkID, t.nodeID)

	if _, err := conn.Write([]byte(resp)); err != nil {
		_ = conn.Close()
		return
	}

	remoteAddr := conn.RemoteAddr().String()
	sessionID := remoteAddr
	if remoteNodeID != "" {
		sessionID = remoteNodeID
	}
	session := newPeerSession(conn, remoteAddr, t.engine, t, false)
	session.id = sessionID
	if !t.registerSession(session) {
		_ = conn.Close()
		return
	}

	session.start()
	t.sendHello(session)
}

// DialPeer attempts to establish a direct P2P connection to an advertised peer address.
func (t *Transport) DialPeer(addr PeerAddress) {
	peerEndpoint := addr.String()

	t.mu.RLock()
	if _, exists := t.sessions[peerEndpoint]; exists || len(t.sessions) >= t.maxPeers {
		t.mu.RUnlock()
		return
	}
	t.mu.RUnlock()

	conn, err := net.DialTimeout("tcp", peerEndpoint, 5*time.Second)
	if err != nil {
		return
	}

	// Send HTTP 101 Upgrade request
	req := fmt.Sprintf("GET %s HTTP/1.1\r\n"+
		"Host: %s\r\n"+
		"Upgrade: originless-p2p\r\n"+
		"Connection: Upgrade\r\n"+
		"Originless-Network-ID: %s\r\n"+
		"Originless-Node-ID: %s\r\n\r\n", DefaultP2PPath, peerEndpoint, t.networkID, t.nodeID)

	if _, err := conn.Write([]byte(req)); err != nil {
		_ = conn.Close()
		return
	}

	// Read response status
	br := bufio.NewReader(conn)
	statusLine, err := br.ReadString('\n')
	if err != nil || !strings.Contains(statusLine, "101") {
		_ = conn.Close()
		return
	}

	// Read until end of headers (\r\n\r\n)
	var remoteNodeID string
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			_ = conn.Close()
			return
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			break
		}
		if strings.HasPrefix(strings.ToLower(trimmed), "originless-node-id:") {
			parts := strings.SplitN(trimmed, ":", 2)
			if len(parts) == 2 {
				remoteNodeID = strings.TrimSpace(parts[1])
			}
		}
	}

	if remoteNodeID != "" && remoteNodeID == t.nodeID {
		// Self connection
		_ = conn.Close()
		return
	}

	sessionID := peerEndpoint
	if remoteNodeID != "" {
		sessionID = remoteNodeID
	}

	session := newPeerSession(conn, peerEndpoint, t.engine, t, true)
	session.id = sessionID
	if !t.registerSession(session) {
		_ = conn.Close()
		return
	}

	session.start()
	t.sendHello(session)
}

func (t *Transport) sendHello(session *PeerSession) {
	evCount, _ := t.engine.store.GetRecordCount()
	bCount, _ := t.engine.store.GetBlobCount()

	var peers []PeerAddress
	t.mu.RLock()
	for _, s := range t.sessions {
		if s.id != session.id && s.remoteAddr != "" {
			if host, portStr, err := net.SplitHostPort(s.remoteAddr); err == nil {
				var p int
				if _, err := fmt.Sscanf(portStr, "%d", &p); err == nil && p > 0 {
					peers = append(peers, PeerAddress{IP: host, Port: p})
				}
			}
		}
	}
	t.mu.RUnlock()

	hello := HelloPayload{
		NodeID:     t.nodeID,
		NetworkID:  t.networkID,
		Version:    modules.Version,
		EventCount: evCount,
		BlobCount:  int(bCount),
		ListenPort: t.listenPort,
		Peers:      peers,
	}
	data, _ := json.Marshal(hello)
	_ = session.Send(MsgHello, data)
}

// BroadcastEvent relays a newly published event to all connected peers in real time.
func (t *Transport) BroadcastEvent(rec *modules.Record, excludePeerID string) {
	data, err := json.Marshal(rec)
	if err != nil {
		return
	}

	t.mu.RLock()
	defer t.mu.RUnlock()

	for id, s := range t.sessions {
		if id == excludePeerID {
			continue
		}
		_ = s.Send(MsgEventBroadcast, data)
	}
}

// BroadcastBlob relays a newly uploaded blob announcement to all connected peers in real time.
func (t *Transport) BroadcastBlob(hash string, size int64, excludePeerID string) {
	payload, _ := json.Marshal(BlobBroadcastPayload{Hash: hash, Size: size})

	t.mu.RLock()
	defer t.mu.RUnlock()

	for id, s := range t.sessions {
		if id == excludePeerID {
			continue
		}
		_ = s.Send(MsgBlobBroadcast, payload)
	}
}

// Stop closes all active peer sessions.
func (t *Transport) Stop() {
	t.mu.Lock()
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
