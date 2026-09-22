package p2p

import (
	"context"
	"fmt"
	"log"
	"time"

	dht "github.com/libp2p/go-libp2p-kad-dht"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/libp2p/go-libp2p/p2p/discovery/mdns"
	"github.com/libp2p/go-libp2p/p2p/discovery/routing"
	"github.com/libp2p/go-libp2p/p2p/discovery/util"
)

type mdnsNotifee struct {
	onPeer func(peer.AddrInfo)
}

func (n *mdnsNotifee) HandlePeerFound(info peer.AddrInfo) {
	if n.onPeer != nil {
		n.onPeer(info)
	}
}

type meshDiscovery struct {
	h          host.Host
	dht        *dht.IpfsDHT
	mdns       mdns.Service
	routing    *routing.RoutingDiscovery
	namespace  string
	bootstrap  []peer.AddrInfo
	onPeer     func(peer.AddrInfo)
	cancel     context.CancelFunc
}

func startMeshDiscovery(parent context.Context, h host.Host, networkID string, bootstrap []peer.AddrInfo, disableDHT, disableMDNS bool, onPeer func(peer.AddrInfo)) (*meshDiscovery, error) {
	ctx, cancel := context.WithCancel(parent)
	d := &meshDiscovery{
		h:         h,
		namespace: rendezvousNamespace(networkID),
		bootstrap: bootstrap,
		onPeer:    onPeer,
		cancel:    cancel,
	}

	if !disableDHT {
		dhtOpts := []dht.Option{
			dht.Mode(dht.ModeAuto),
			dht.BootstrapPeers(bootstrap...),
		}
		if networkID != DefaultNetworkID {
			dhtOpts = append(dhtOpts, dht.ProtocolPrefix(protocol.ID(DHTProtocolPrefix)))
		}
		kad, err := dht.New(h, dhtOpts...)
		if err != nil {
			cancel()
			return nil, fmt.Errorf("kad dht: %w", err)
		}
		d.dht = kad
		if err := kad.Bootstrap(ctx); err != nil {
			log.Printf("[P2P-DHT] bootstrap: %v", err)
		}
		d.routing = routing.NewRoutingDiscovery(kad)
		util.Advertise(ctx, d.routing, d.namespace)
		go d.findLoop(ctx)
	}

	if !disableMDNS {
		svc := mdns.NewMdnsService(h, mdnsServiceName(networkID), &mdnsNotifee{onPeer: onPeer})
		if err := svc.Start(); err != nil {
			log.Printf("[P2P-MDNS] start: %v", err)
		} else {
			d.mdns = svc
		}
	}

	go d.dialBootstrap(ctx)
	return d, nil
}

func (d *meshDiscovery) Stop() {
	if d == nil {
		return
	}
	d.cancel()
	if d.mdns != nil {
		_ = d.mdns.Close()
	}
	if d.dht != nil {
		_ = d.dht.Close()
	}
}

func (d *meshDiscovery) DHT() *dht.IpfsDHT {
	if d == nil {
		return nil
	}
	return d.dht
}

func (d *meshDiscovery) dialBootstrap(ctx context.Context) {
	for _, info := range d.bootstrap {
		if info.ID == d.h.ID() {
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		err := d.h.Connect(cctx, info)
		cancel()
		if err != nil {
			log.Printf("[P2P-DISCOVERY] bootstrap %s: %v", info.ID, err)
			continue
		}
	}
}

func (d *meshDiscovery) findLoop(ctx context.Context) {
	if d.routing == nil {
		return
	}
	d.findOnce(ctx)
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.findOnce(ctx)
		}
	}
}

func (d *meshDiscovery) findOnce(ctx context.Context) {
	fctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	peers, err := util.FindPeers(fctx, d.routing, d.namespace)
	if err != nil {
		return
	}
	for _, p := range peers {
		if p.ID == d.h.ID() || len(p.Addrs) == 0 {
			continue
		}
		if d.onPeer != nil {
			d.onPeer(p)
		}
	}
}

func mdnsServiceName(networkID string) string {
	name := "ol-" + networkID
	if len(name) > 32 {
		name = name[:32]
	}
	return name
}