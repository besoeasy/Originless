package p2p

import (
	"os"
	"strings"
	"time"
)

// Hardcoded sensible defaults for BitTorrent Mainline DHT & P2P Swarm.
// Average users never need to configure these knobs.
var (
	DefaultDHTBootstrapRouters = []string{
		"router.bittorrent.com:6881",
		"dht.transmissionbt.com:6881",
		"router.utorrent.com:6881",
	}

	DefaultP2PPath        = "/p2p"
	DefaultP2PPort        = 3232
	DefaultMaxPeers       = 8
	DefaultSyncInterval   = 60 * time.Second
	DefaultDHTPollInterval = 120 * time.Second
	DefaultGossipCacheSize = 10000
)

// Config represents the active P2P configuration.
type Config struct {
	Enabled   bool
	NetworkID string
	Port      int
	Routers   []string
	MaxPeers  int
}

// LoadConfig reads the environment to configure the P2P subsystem.
// If NETWORK_ID is non-empty, P2P sync is enabled.
func LoadConfig() Config {
	netID := strings.TrimSpace(os.Getenv("NETWORK_ID"))
	if netID == "" {
		netID = strings.TrimSpace(os.Getenv("NETWORKID"))
	}

	cfg := Config{
		Enabled:   netID != "",
		NetworkID: netID,
		Port:      DefaultP2PPort,
		Routers:   DefaultDHTBootstrapRouters,
		MaxPeers:  DefaultMaxPeers,
	}

	return cfg
}
