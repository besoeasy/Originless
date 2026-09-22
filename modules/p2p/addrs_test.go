package p2p

import (
	"testing"

	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
)

func TestFilterDockerBridgeAddrs(t *testing.T) {
	bridge, _ := ma.NewMultiaddr("/ip4/172.17.0.2/tcp/3232/ws")
	podman, _ := ma.NewMultiaddr("/ip4/10.88.0.5/tcp/3232/ws")
	lan, _ := ma.NewMultiaddr("/ip4/192.168.1.20/tcp/3232/ws")
	loop, _ := ma.NewMultiaddr("/ip4/127.0.0.1/tcp/3232/ws")

	out := filterAnnounceAddrs([]ma.Multiaddr{bridge, podman, lan})
	if len(out) != 1 || out[0].String() != lan.String() {
		t.Fatalf("expected only LAN addr, got %v", out)
	}

	onlyLoop := filterAnnounceAddrs([]ma.Multiaddr{loop, bridge})
	if len(onlyLoop) != 1 || !onlyLoop[0].Equal(loop) {
		t.Fatalf("expected loopback fallback, got %v", onlyLoop)
	}
}

func TestParseBootstrapPeers(t *testing.T) {
	priv, err := LoadOrCreateIdentity(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	id, err := peer.IDFromPrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	addr := "/ip4/1.2.3.4/tcp/3232/ws/p2p/" + id.String()
	infos := parseAddrInfos(addr + ", not-a-multiaddr")
	if len(infos) != 1 || infos[0].ID != id {
		t.Fatalf("parsed %#v", infos)
	}
}
