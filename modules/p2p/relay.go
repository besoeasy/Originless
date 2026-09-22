package p2p

import (
	"github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/relay"
)

func relayResources() relay.Resources {
	r := relay.DefaultResources()
	r.MaxReservations = 32
	r.MaxCircuits = 16
	r.MaxReservationsPerPeer = 2
	r.MaxReservationsPerIP = 8
	r.MaxReservationsPerASN = 16
	if r.Limit != nil {
		r.Limit.Data = MaxFramePayloadSize
		r.Limit.Duration = DefaultSyncInterval * 30
	}
	return r
}
