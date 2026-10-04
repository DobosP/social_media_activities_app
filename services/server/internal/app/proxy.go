package app

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// forwardedPeer accepts headers only from explicitly configured network peers.
// The rightmost configured hop is used; a caller-prepended address cannot mint a
// new login-rate bucket. Without trusted CIDRs, RemoteAddr stays authoritative.
func (a *App) forwardedPeer(r *http.Request) *http.Request {
	if len(a.proxyNetworks) == 0 || a.Config.ProxyHops < 1 {
		return r
	}
	peer, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r
	}
	address, err := netip.ParseAddr(peer)
	if err != nil {
		return r
	}
	trusted := false
	for _, network := range a.proxyNetworks {
		if network.Contains(address.Unmap()) {
			trusted = true
			break
		}
	}
	if !trusted {
		return r
	}
	raw := r.Header.Get("X-Forwarded-For")
	if len(raw) > 4096 {
		return r
	}
	parts := strings.Split(raw, ",")
	if len(parts) < a.Config.ProxyHops || len(parts) > 32 {
		return r
	}
	addresses := []netip.Addr{}
	for _, part := range parts {
		parsed, err := netip.ParseAddr(strings.TrimSpace(part))
		if err != nil {
			return r
		}
		addresses = append(addresses, parsed.Unmap())
	}
	client := addresses[len(addresses)-a.Config.ProxyHops]
	copy := r.Clone(r.Context())
	copy.RemoteAddr = net.JoinHostPort(client.String(), "0")
	return copy
}
