package p2p

import (
	"encoding/binary"
	"net"
	"testing"
	"time"
)

func TestDiscoveryStartStop(t *testing.T) {
	d, err := NewDiscovery("test-network-123", 3232, func(addr PeerAddress) {})
	if err != nil {
		t.Fatalf("NewDiscovery failed: %v", err)
	}

	d.Start()
	time.Sleep(50 * time.Millisecond)
	d.Stop()
}

func TestDiscoveryParseKRPCResponse(t *testing.T) {
	var discovered []PeerAddress
	d, err := NewDiscovery("test-network-456", 3232, func(addr PeerAddress) {
		discovered = append(discovered, addr)
	})
	if err != nil {
		t.Fatalf("NewDiscovery: %v", err)
	}
	defer d.Stop()

	// Simulate KRPC get_peers response with compact peer: 192.168.1.50:3232
	compactPeer := make([]byte, 6)
	copy(compactPeer[0:4], net.ParseIP("192.168.1.50").To4())
	binary.BigEndian.PutUint16(compactPeer[4:6], 3232)

	resp := map[string]any{
		"t": "gp",
		"y": "r",
		"r": map[string]any{
			"id":     "dummy-node-id-20byte",
			"values": []any{string(compactPeer)},
		},
	}
	encoded, err := BencodeEncode(resp)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	d.handleKRPCResponse(encoded, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 6881})

	// Wait briefly for callback
	time.Sleep(50 * time.Millisecond)

	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.knownPeers) == 0 {
		t.Fatalf("expected 1 peer discovered, got 0")
	}
	if _, exists := d.knownPeers["192.168.1.50:3232"]; !exists {
		t.Fatalf("expected 192.168.1.50:3232 in known peers")
	}
}

func TestLSDSendReceive(t *testing.T) {
	d, err := NewDiscovery("test-net", 3232, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Stop()

	bAddr, err := net.ResolveUDPAddr("udp4", "255.255.255.255:3234")
	if err != nil {
		t.Fatal(err)
	}
	_, err = d.udpConn.WriteToUDP([]byte("TEST"), bAddr)
	if err != nil {
		t.Logf("WriteToUDP broadcast returned error: %v", err)
	} else {
		t.Logf("WriteToUDP broadcast succeeded")
	}
}

func TestTwoDiscoveryInstances(t *testing.T) {
	d1, err := NewDiscovery("test-net", 3232, nil)
	if err != nil {
		t.Fatal(err)
	}
	d2, err := NewDiscovery("test-net", 3233, nil)
	if err != nil {
		t.Fatal(err)
	}
	d1.Start()
	d2.Start()
	time.Sleep(100 * time.Millisecond)
	d1.Stop()
	d2.Stop()
}


