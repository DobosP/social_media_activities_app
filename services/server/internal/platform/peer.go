package platform

import (
	"net"
	"net/netip"
	"strings"
)

// PeerKey is the single admission identity for the server's already trusted
// client address: an IPv4 address or an IPv6 /64. Source ports, IPv4-mapped
// spelling and zones cannot mint independent counters, and one IPv6 subscriber
// prefix cannot rotate addresses into fresh buckets. Unparsable input shares one
// fail-safe bucket. Forwarded headers are interpreted at the app boundary only.
func PeerKey(address string) string {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		host = address
	}
	addr, err := netip.ParseAddr(strings.TrimSpace(host))
	if err != nil {
		return "unknown"
	}
	addr = addr.Unmap().WithZone("")
	if addr.Is4() {
		return addr.String()
	}
	return netip.PrefixFrom(addr, 64).Masked().String()
}
