package p2p

import (
	"net"

	ma "github.com/multiformats/go-multiaddr"
)

var dockerBridgeNets = []net.IPNet{
	{IP: net.IPv4(172, 17, 0, 0), Mask: net.CIDRMask(16, 32)},
	{IP: net.IPv4(10, 88, 0, 0), Mask: net.CIDRMask(16, 32)},
}

func isDockerBridgeIP(ip net.IP) bool {
	ip4 := ip.To4()
	if ip4 == nil {
		return false
	}
	for i := range dockerBridgeNets {
		if dockerBridgeNets[i].Contains(ip4) {
			return true
		}
	}
	return false
}

func addrIP(a ma.Multiaddr) net.IP {
	if v, err := a.ValueForProtocol(ma.P_IP4); err == nil {
		return net.ParseIP(v)
	}
	if v, err := a.ValueForProtocol(ma.P_IP6); err == nil {
		return net.ParseIP(v)
	}
	return nil
}

// filterAnnounceAddrs drops default Docker/Podman bridge IPs from DHT
// advertisements. Loopback is kept when it is the only remaining address so
// local tests and --network host still work.
func filterAnnounceAddrs(addrs []ma.Multiaddr) []ma.Multiaddr {
	if len(addrs) == 0 {
		return addrs
	}
	filtered := make([]ma.Multiaddr, 0, len(addrs))
	var loopback []ma.Multiaddr
	for _, a := range addrs {
		ip := addrIP(a)
		if ip == nil {
			filtered = append(filtered, a)
			continue
		}
		if ip.IsLoopback() {
			loopback = append(loopback, a)
			continue
		}
		if isDockerBridgeIP(ip) {
			continue
		}
		filtered = append(filtered, a)
	}
	if len(filtered) == 0 {
		return append(filtered, loopback...)
	}
	return filtered
}

func newAddrsFactory(announce []ma.Multiaddr) func([]ma.Multiaddr) []ma.Multiaddr {
	return func(addrs []ma.Multiaddr) []ma.Multiaddr {
		if len(announce) > 0 {
			return announce
		}
		return filterAnnounceAddrs(addrs)
	}
}
