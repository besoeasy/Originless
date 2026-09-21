package p2p

import (
	"crypto/rand"
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"time"
)

// PeerAddress represents a discovered Originless peer endpoint.
type PeerAddress struct {
	IP   string
	Port int
}

func (pa PeerAddress) String() string {
	return fmt.Sprintf("%s:%d", pa.IP, pa.Port)
}

// Discovery coordinates BitTorrent Mainline DHT and Local Service Discovery (LSD).
type Discovery struct {
	networkID       string
	infoHash        [20]byte
	nodeID          [20]byte
	listenPort      int
	udpConn         *net.UDPConn
	lsdConn         *net.UDPConn
	onPeer          func(PeerAddress)
	mu              sync.Mutex
	knownPeers      map[string]time.Time
	stopChan        chan struct{}
	wg              sync.WaitGroup
	bootstrapNodes  []string
}

// NewDiscovery creates a new P2P discovery service for the given network ID.
func NewDiscovery(networkID string, listenPort int, onPeer func(PeerAddress)) (*Discovery, error) {
	if networkID == "" {
		return nil, fmt.Errorf("network ID cannot be empty")
	}

	// Compute 20-byte infohash: sha1("originless:" + networkID)
	infoHash := sha1.Sum([]byte("originless:" + networkID))

	// Generate random 20-byte Node ID
	var nodeID [20]byte
	if _, err := rand.Read(nodeID[:]); err != nil {
		return nil, fmt.Errorf("generate node ID: %w", err)
	}

	// Bind ephemeral UDP socket for DHT KRPC queries
	udpAddr, err := net.ResolveUDPAddr("udp4", ":0")
	if err != nil {
		return nil, err
	}
	udpConn, err := net.ListenUDP("udp4", udpAddr)
	if err != nil {
		return nil, fmt.Errorf("listen UDP: %w", err)
	}

	return &Discovery{
		networkID:      networkID,
		infoHash:       infoHash,
		nodeID:         nodeID,
		listenPort:     listenPort,
		udpConn:        udpConn,
		onPeer:         onPeer,
		knownPeers:     make(map[string]time.Time),
		stopChan:       make(chan struct{}),
		bootstrapNodes: DefaultDHTBootstrapRouters,
	}, nil
}

// Start launches background discovery workers.
func (d *Discovery) Start() {
	d.wg.Add(3)
	go d.readLoop()
	go d.dhtPollLoop()
	go d.lsdLoop()
}

// Stop terminates all discovery routines.
func (d *Discovery) Stop() {
	close(d.stopChan)
	if d.udpConn != nil {
		_ = d.udpConn.Close()
	}
	if d.lsdConn != nil {
		_ = d.lsdConn.Close()
	}
	d.wg.Wait()
}

func (d *Discovery) registerPeer(addr PeerAddress) {
	d.mu.Lock()
	defer d.mu.Unlock()

	key := addr.String()
	// Skip if seen recently (< 30 seconds)
	if last, exists := d.knownPeers[key]; exists && time.Since(last) < 30*time.Second {
		return
	}
	d.knownPeers[key] = time.Now()

	log.Printf("[P2P-DISCOVERY] Discovered peer: %s", key)
	if d.onPeer != nil {
		go d.onPeer(addr)
	}
}

// readLoop listens for incoming KRPC UDP responses from DHT routers.
func (d *Discovery) readLoop() {
	defer d.wg.Done()
	buf := make([]byte, 2048)

	for {
		select {
		case <-d.stopChan:
			return
		default:
		}

		_ = d.udpConn.SetReadDeadline(time.Now().Add(1 * time.Second))
		n, from, err := d.udpConn.ReadFromUDP(buf)
		if err != nil {
			continue
		}

		d.handleKRPCResponse(buf[:n], from)
	}
}

// handleKRPCResponse parses KRPC bencoded replies.
func (d *Discovery) handleKRPCResponse(data []byte, from *net.UDPAddr) {
	val, err := BencodeDecode(data)
	if err != nil {
		return
	}
	dict, ok := val.(map[string]any)
	if !ok {
		return
	}

	// We only care about replies (y == "r")
	if dict["y"] != "r" {
		return
	}
	rDict, ok := dict["r"].(map[string]any)
	if !ok {
		return
	}

	// 1. Check for peer values (compact peer addresses: 6 bytes each)
	if rawVals, exists := rDict["values"]; exists {
		if list, ok := rawVals.([]any); ok {
			for _, item := range list {
				if peerBytes, ok := item.(string); ok && len(peerBytes) == 6 {
					ip := net.IP([]byte(peerBytes[0:4]))
					port := binary.BigEndian.Uint16([]byte(peerBytes[4:6]))
					d.registerPeer(PeerAddress{IP: ip.String(), Port: int(port)})
				}
			}
		}
	}

	// 2. Check for nodes (compact node addresses: 26 bytes each)
	if rawNodes, exists := rDict["nodes"]; exists {
		if nodesStr, ok := rawNodes.(string); ok {
			nodesBytes := []byte(nodesStr)
			for i := 0; i+26 <= len(nodesBytes); i += 26 {
				// Node ID: bytes 0..20, IP: bytes 20..24, Port: bytes 24..26
				ip := net.IP(nodesBytes[i+20 : i+24])
				port := binary.BigEndian.Uint16(nodesBytes[i+24 : i+26])
				nodeAddr := &net.UDPAddr{IP: ip, Port: int(port)}
				// Query this closer node for our info_hash
				d.queryGetPeers(nodeAddr)
			}
		}
	}
}

// queryGetPeers sends a get_peers KRPC query to a DHT node.
func (d *Discovery) queryGetPeers(addr *net.UDPAddr) {
	query := map[string]any{
		"t": "gp",
		"y": "q",
		"q": "get_peers",
		"a": map[string]any{
			"id":        string(d.nodeID[:]),
			"info_hash": string(d.infoHash[:]),
		},
	}
	data, err := BencodeEncode(query)
	if err != nil {
		return
	}
	_, _ = d.udpConn.WriteToUDP(data, addr)
}

// queryFindNode sends a find_node query to bootstrap routers.
func (d *Discovery) queryFindNode(routerHostPort string) {
	rAddr, err := net.ResolveUDPAddr("udp4", routerHostPort)
	if err != nil {
		return
	}
	query := map[string]any{
		"t": "fn",
		"y": "q",
		"q": "find_node",
		"a": map[string]any{
			"id":     string(d.nodeID[:]),
			"target": string(d.infoHash[:]),
		},
	}
	data, err := BencodeEncode(query)
	if err != nil {
		return
	}
	_, _ = d.udpConn.WriteToUDP(data, rAddr)
}

// dhtPollLoop periodically crawls the DHT and announces our node.
func (d *Discovery) dhtPollLoop() {
	defer d.wg.Done()

	// Initial crawl
	d.pollRouters()

	ticker := time.NewTicker(DefaultDHTPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-d.stopChan:
			return
		case <-ticker.C:
			d.pollRouters()
		}
	}
}

func (d *Discovery) pollRouters() {
	for _, r := range d.bootstrapNodes {
		d.queryFindNode(r)
	}
}

// lsdLoop runs Local Service Discovery over UDP broadcast for instant local network / Docker peer discovery.
func (d *Discovery) lsdLoop() {
	defer d.wg.Done()

	// Listen for local broadcasts
	lAddr, err := net.ResolveUDPAddr("udp4", ":3234")
	if err == nil {
		conn, err := net.ListenUDP("udp4", lAddr)
		if err == nil {
			d.lsdConn = conn
			go d.lsdReadLoop(conn)
		}
	}

	// Broadcast presence every 10 seconds locally
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	d.sendLSDAnnouncement()

	for {
		select {
		case <-d.stopChan:
			return
		case <-ticker.C:
			d.sendLSDAnnouncement()
		}
	}
}

func (d *Discovery) sendLSDAnnouncement() {
	bAddr, err := net.ResolveUDPAddr("udp4", "255.255.255.255:3234")
	if err != nil {
		return
	}
	msg := fmt.Sprintf("ORIGINLESS %s %d %x", d.networkID, d.listenPort, d.nodeID[:4])
	_, _ = d.udpConn.WriteToUDP([]byte(msg), bAddr)
}

func (d *Discovery) lsdReadLoop(conn *net.UDPConn) {
	buf := make([]byte, 512)
	for {
		select {
		case <-d.stopChan:
			return
		default:
		}

		_ = conn.SetReadDeadline(time.Now().Add(1 * time.Second))
		n, remoteAddr, err := conn.ReadFromUDP(buf)
		if err != nil {
			continue
		}

		parts := strings.Fields(string(buf[:n]))
		if len(parts) >= 3 && parts[0] == "ORIGINLESS" {
			netID := parts[1]
			if netID == d.networkID {
				// Ignore self announcements
				if len(parts) >= 4 && parts[3] == fmt.Sprintf("%x", d.nodeID[:4]) {
					continue
				}
				var port int
				if _, err := fmt.Sscanf(parts[2], "%d", &port); err == nil && port > 0 {
					// Discovered a local peer!
					d.registerPeer(PeerAddress{IP: remoteAddr.IP.String(), Port: port})
				}
			}
		}
	}
}
