package p2p

import (
	"os"
	"strings"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
)

const (
	DefaultP2PPort         = 3232
	DefaultNetworkID       = "originless"
	DefaultMaxPeers        = 8
	DefaultSyncInterval    = 60 * time.Second
	DefaultGossipCacheSize = 10000
	IdentityFileName       = "p2p.key"
	SyncProtocolID         = "/originless/sync/1.0.0"
	DHTProtocolPrefix      = "/originless"
	RendezvousPrefix       = "originless/"
)

// DefaultBootstrapPeers are public libp2p DHT bootstrap peers used to join the
// public global DHT mesh when NETWORK_ID=originless (default).
var DefaultBootstrapPeers = []string{
	"/dnsaddr/bootstrap.libp2p.io/p2p/QmNnooDu7bfjPFoTZYxMNLWUQJyrVwtbZg5gBMjTezGAJN",
	"/dnsaddr/bootstrap.libp2p.io/p2p/QmQCU2EcMqAqQPR2i9bChDtGNJchTbq5TbXJJ16u19uLTa",
	"/dnsaddr/bootstrap.libp2p.io/p2p/QmbLHAnMoJPWSCR5Zhtx6BHJX9KiKNN6tpvbUcqanj75Nb",
	"/dnsaddr/bootstrap.libp2p.io/p2p/QmcZf59bWwK5XFi76CZX8cbJ4BhTzzA3gU1ZjYZcYW3dwt",
	"/ip4/104.131.131.82/tcp/4001/p2p/QmaCpDMGvV2BGHeYERUEnRQAwe3N8SzbUtfsmvsqQLuvuJ",
}

// Config represents the active P2P configuration.
type Config struct {
	Enabled        bool
	NetworkID      string
	Port           int
	MaxPeers       int
	BootstrapPeers []peer.AddrInfo
	AnnounceAddrs  []ma.Multiaddr
}

// LoadConfig reads the environment to configure the P2P subsystem.
// Default NETWORK_ID is "originless". Set NETWORK_ID=off or none to disable.
func LoadConfig() Config {
	netID := strings.TrimSpace(os.Getenv("NETWORK_ID"))
	if netID == "" {
		netID = strings.TrimSpace(os.Getenv("NETWORKID"))
	}
	if netID == "" {
		netID = DefaultNetworkID
	}

	enabled := true
	lower := strings.ToLower(netID)
	if lower == "none" || lower == "off" || lower == "disabled" || lower == "false" || lower == "0" {
		enabled = false
		netID = ""
	}

	cfg := Config{
		Enabled:   enabled,
		NetworkID: netID,
		Port:      DefaultP2PPort,
		MaxPeers:  DefaultMaxPeers,
	}
	if !enabled {
		return cfg
	}

	boot := strings.TrimSpace(os.Getenv("BOOTSTRAP_PEERS"))
	if boot != "" {
		cfg.BootstrapPeers = parseAddrInfos(boot)
	} else if netID == DefaultNetworkID {
		cfg.BootstrapPeers = parseAddrInfos(strings.Join(DefaultBootstrapPeers, ","))
	}

	if announce := strings.TrimSpace(os.Getenv("ANNOUNCE_ADDRS")); announce != "" {
		cfg.AnnounceAddrs = parseMultiaddrs(announce)
	}

	return cfg
}

func parseAddrInfos(list string) []peer.AddrInfo {
	var out []peer.AddrInfo
	for _, part := range strings.Split(list, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		m, err := ma.NewMultiaddr(part)
		if err != nil {
			continue
		}
		info, err := peer.AddrInfoFromP2pAddr(m)
		if err != nil {
			continue
		}
		out = append(out, *info)
	}
	return out
}

func parseMultiaddrs(list string) []ma.Multiaddr {
	var out []ma.Multiaddr
	for _, part := range strings.Split(list, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		m, err := ma.NewMultiaddr(part)
		if err != nil {
			continue
		}
		out = append(out, m)
	}
	return out
}

func rendezvousNamespace(networkID string) string {
	return RendezvousPrefix + networkID
}
