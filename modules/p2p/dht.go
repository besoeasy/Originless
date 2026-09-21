package p2p

import (
	"context"
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
	networkID      string
	infoHash       [20]byte
	nodeID         [20]byte
	listenPort     int
	udpConn        *net.UDPConn
	lsdConn        *net.UDPConn
	onPeer         func(PeerAddress)
	mu             sync.Mutex
	knownPeers     map[string]time.Time
	seenDHTNodes   map[string]bool
	externalIP     string
	natMapper      *NATPortMapper
	stopChan       chan struct{}
	wg             sync.WaitGroup
	bootstrapNodes []string
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
		seenDHTNodes:   make(map[string]bool),
		natMapper:      NewNATPortMapper(listenPort),
		stopChan:       make(chan struct{}),
		bootstrapNodes: DefaultDHTBootstrapRouters,
	}, nil
}

// Start launches background discovery workers.
func (d *Discovery) Start() {
	d.wg.Add(4)
	go d.readLoop()
	go d.dhtPollLoop()
	go d.lsdLoop()
	go d.localProbeLoop()
	go d.natMapper.TryPortMapping(context.Background())
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

	// Extract our external IP as observed by the DHT node (BEP 5 / BEP 42)
	if ipBytes, ok := dict["ip"].(string); ok && len(ipBytes) >= 4 {
		extIP := net.IP([]byte(ipBytes[:4]))
		if extIP.To4() != nil && !extIP.IsLoopback() && !extIP.IsPrivate() {
			d.mu.Lock()
			if d.externalIP != extIP.String() {
				d.externalIP = extIP.String()
				log.Printf("[P2P-DHT] Observed public IP: %s", d.externalIP)
			}
			d.mu.Unlock()
		}
	}

	// 1. Check for peer values (compact peer addresses: 6 bytes each)
	if rawVals, exists := rDict["values"]; exists {
		if list, ok := rawVals.([]any); ok {
			for _, item := range list {
				if peerBytes, ok := item.(string); ok && len(peerBytes) == 6 {
					ip := net.IP([]byte(peerBytes[0:4]))
					port := binary.BigEndian.Uint16([]byte(peerBytes[4:6]))
					d.mu.Lock()
					isSelf := (ip.String() == d.externalIP && int(port) == d.listenPort)
					d.mu.Unlock()
					if !isSelf {
						d.registerPeer(PeerAddress{IP: ip.String(), Port: int(port)})
					}
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
				nodeKey := fmt.Sprintf("%s:%d", ip.String(), port)

				d.mu.Lock()
				if d.seenDHTNodes[nodeKey] || len(d.seenDHTNodes) > 256 {
					d.mu.Unlock()
					continue
				}
				d.seenDHTNodes[nodeKey] = true
				d.mu.Unlock()

				nodeAddr := &net.UDPAddr{IP: ip, Port: int(port)}
				// Query this closer node for our info_hash
				d.queryGetPeers(nodeAddr)
			}
		}
	}

	// 3. Check for token in get_peers reply and announce our node
	if tokenStr, ok := rDict["token"].(string); ok && tokenStr != "" {
		d.queryAnnouncePeer(from, tokenStr)
	}
}

// queryAnnouncePeer registers our node in the Mainline DHT for this info_hash.
func (d *Discovery) queryAnnouncePeer(addr *net.UDPAddr, token string) {
	query := map[string]any{
		"t": "ap",
		"y": "q",
		"q": "announce_peer",
		"a": map[string]any{
			"id":        string(d.nodeID[:]),
			"info_hash": string(d.infoHash[:]),
			"port":      d.listenPort,
			"token":     token,
		},
	}
	data, err := BencodeEncode(query)
	if err != nil {
		return
	}
	_, _ = d.udpConn.WriteToUDP(data, addr)
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
	d.mu.Lock()
	d.seenDHTNodes = make(map[string]bool)
	d.mu.Unlock()

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
	msg := fmt.Sprintf("ORIGINLESS %s %d %x", d.networkID, d.listenPort, d.nodeID[:4])
	data := []byte(msg)

	// 1. Broadcast to 255.255.255.255:3234
	if bAddr, err := net.ResolveUDPAddr("udp4", "255.255.255.255:3234"); err == nil {
		_, _ = d.udpConn.WriteToUDP(data, bAddr)
	}

	// 2. Multicast to standard BEP 14 group 239.192.152.143:3234
	if mAddr, err := net.ResolveUDPAddr("udp4", "239.192.152.143:3234"); err == nil {
		_, _ = d.udpConn.WriteToUDP(data, mAddr)
	}

	// 3. Subnet broadcast on all active non-loopback interfaces
	ifaces, err := net.Interfaces()
	if err == nil {
		for _, ifi := range ifaces {
			if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagLoopback != 0 {
				continue
			}
			addrs, err := ifi.Addrs()
			if err != nil {
				continue
			}
			for _, a := range addrs {
				if ipNet, ok := a.(*net.IPNet); ok && ipNet.IP.To4() != nil {
					ip := ipNet.IP.To4()
					mask := ipNet.Mask
					if len(mask) == 4 {
						bcast := net.IPv4(
							ip[0]|^mask[0],
							ip[1]|^mask[1],
							ip[2]|^mask[2],
							ip[3]|^mask[3],
						)
						subAddr := &net.UDPAddr{IP: bcast, Port: 3234}
						_, _ = d.udpConn.WriteToUDP(data, subAddr)
					}
				}
			}
		}
	}

	// 4. Broadcast to physical LAN subnets discovered via container host
	for _, host := range []string{"host.containers.internal", "host.docker.internal"} {
		if ips, err := net.LookupIP(host); err == nil {
			for _, ip := range ips {
				if ipv4 := ip.To4(); ipv4 != nil && !ipv4.IsLoopback() {
					lanBcast := net.IPv4(ipv4[0], ipv4[1], ipv4[2], 255)
					subAddr := &net.UDPAddr{IP: lanBcast, Port: 3234}
					_, _ = d.udpConn.WriteToUDP(data, subAddr)
				}
			}
		}
	}
}

// localProbeLoop periodically checks local container host and localhost fallback endpoints.
func (d *Discovery) localProbeLoop() {
	defer d.wg.Done()

	d.probeCandidates()

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-d.stopChan:
			return
		case <-ticker.C:
			d.probeCandidates()
		}
	}
}

func (d *Discovery) probeCandidates() {
	candidates := []string{
		"host.containers.internal",
		"host.docker.internal",
		"127.0.0.1",
	}

	for _, host := range candidates {
		ips, err := net.LookupIP(host)
		if err != nil {
			continue
		}
		for _, ip := range ips {
			ipv4 := ip.To4()
			if ipv4 == nil {
				continue
			}
			d.registerPeer(PeerAddress{IP: ipv4.String(), Port: 3232})
		}
	}
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
