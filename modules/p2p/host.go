package p2p

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/host/autorelay"
	rcmgr "github.com/libp2p/go-libp2p/p2p/host/resource-manager"
	"github.com/libp2p/go-libp2p/p2p/net/connmgr"
	"github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/relay"
	libp2pquic "github.com/libp2p/go-libp2p/p2p/transport/quic"
	"github.com/libp2p/go-libp2p/p2p/transport/websocket"
)

type hostSettings struct {
	port             int
	listenHost       string
	httpHandler      http.Handler
	bootstrap        []peer.AddrInfo
	forcePublic      bool
	forcePrivate     bool
	relayHop         bool
	disableQUIC      bool
	disableAutoRelay bool
	staticRelays     []peer.AddrInfo
}

func buildHost(priv crypto.PrivKey, cfg Config, s hostSettings) (host.Host, error) {
	if s.listenHost == "" {
		s.listenHost = "0.0.0.0"
	}
	port := s.port
	if port < 0 {
		port = cfg.Port
	}
	if port < 0 {
		port = DefaultP2PPort
	}
	if port == 0 && s.listenHost == "" {
		s.listenHost = "0.0.0.0"
	}

	handler := s.httpHandler
	if handler == nil {
		handler = http.NotFoundHandler()
	}

	cm, err := connmgr.NewConnManager(16, 64, connmgr.WithGracePeriod(time.Minute))
	if err != nil {
		return nil, fmt.Errorf("connmgr: %w", err)
	}

	limits := rcmgr.DefaultLimits
	libp2p.SetDefaultServiceLimits(&limits)
	rm, err := rcmgr.NewResourceManager(rcmgr.NewFixedLimiter(limits.AutoScale()))
	if err != nil {
		return nil, fmt.Errorf("resource manager: %w", err)
	}

	wsListen := fmt.Sprintf("/ip4/%s/tcp/%d/ws", s.listenHost, port)

	opts := []libp2p.Option{
		libp2p.Identity(priv),
		libp2p.ConnectionManager(cm),
		libp2p.ResourceManager(rm),
		libp2p.AddrsFactory(newAddrsFactory(cfg.AnnounceAddrs)),
		libp2p.EnableHolePunching(),
		libp2p.EnableNATService(),
		libp2p.EnableRelay(),
		libp2p.ListenAddrStrings(wsListen),
		libp2p.Transport(websocket.New,
			websocket.WithHTTPHandler(handler),
			websocket.WithHTTPServerConfig(func(srv *http.Server) {
				srv.ReadHeaderTimeout = 10 * time.Second
				srv.IdleTimeout = 120 * time.Second
			}),
		),
	}

	if s.relayHop {
		opts = append(opts, libp2p.EnableRelayService(relay.WithResources(relayResources())))
	}
	if s.forcePublic {
		opts = append(opts, libp2p.ForceReachabilityPublic())
	}
	if s.forcePrivate {
		opts = append(opts, libp2p.ForceReachabilityPrivate())
	}
	if !s.disableAutoRelay {
		peerSrc := autoRelayPeerSource(s.bootstrap, s.staticRelays)
		opts = append(opts, libp2p.EnableAutoRelayWithPeerSource(peerSrc, autorelay.WithNumRelays(2)))
	}
	if !s.disableQUIC {
		quicListen := fmt.Sprintf("/ip4/%s/udp/%d/quic-v1", s.listenHost, port)
		opts = append(opts,
			libp2p.ListenAddrStrings(quicListen),
			libp2p.Transport(libp2pquic.NewTransport),
		)
	}

	h, err := libp2p.New(opts...)
	if err != nil && !s.disableQUIC {
		log.Printf("[P2P] QUIC listen failed (%v); retrying WebSocket only", err)
		s.disableQUIC = true
		return buildHost(priv, cfg, s)
	}
	if err != nil {
		return nil, err
	}
	return h, nil
}

func autoRelayPeerSource(bootstrap, static []peer.AddrInfo) func(ctx context.Context, num int) <-chan peer.AddrInfo {
	return func(ctx context.Context, num int) <-chan peer.AddrInfo {
		out := make(chan peer.AddrInfo, num)
		go func() {
			defer close(out)
			n := 0
			emit := func(info peer.AddrInfo) bool {
				if n >= num || info.ID == "" {
					return n >= num
				}
				select {
				case out <- info:
					n++
					return n >= num
				case <-ctx.Done():
					return true
				}
			}
			for _, p := range static {
				if emit(p) {
					return
				}
			}
			for _, p := range bootstrap {
				if emit(p) {
					return
				}
			}
		}()
		return out
	}
}

func reachabilityString(r network.Reachability) string {
	switch r {
	case network.ReachabilityPublic:
		return "public"
	case network.ReachabilityPrivate:
		return "private"
	default:
		return "unknown"
	}
}
