package httpapi

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// SetTrustedProxies names the reverse proxies (the ingress) whose
// X-Forwarded-For the API believes. Without any, a request's client
// address is its peer's; with them, it is the rightmost address in
// X-Forwarded-For that is not itself a trusted proxy — the one the
// nearest proxy we trust saw — so a client cannot pick its own address
// by sending the header. Rate limits per client address (device sign-in
// starts, RFC 0006 §7.2) key on it.
func (a *API) SetTrustedProxies(prefixes []netip.Prefix) { a.trustedProxies = prefixes }

func (a *API) clientAddress(r *http.Request) string {
	peer, ok := remoteAddr(r.RemoteAddr)
	if !ok {
		return r.RemoteAddr
	}
	if !a.trusted(peer) {
		return peer.String()
	}
	hops := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
	for i := len(hops) - 1; i >= 0; i-- {
		hop, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			break // a malformed hop ends what can be believed
		}
		hop = hop.Unmap()
		if !a.trusted(hop) {
			return hop.String()
		}
		peer = hop
	}
	return peer.String()
}

func (a *API) trusted(addr netip.Addr) bool {
	for _, p := range a.trustedProxies {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

func remoteAddr(s string) (netip.Addr, bool) {
	host, _, err := net.SplitHostPort(s)
	if err != nil {
		host = s
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, false
	}
	return addr.Unmap(), true
}
