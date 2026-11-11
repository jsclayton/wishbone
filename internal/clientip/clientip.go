// Package clientip finds the real client IP of a request behind a reverse proxy.
package clientip

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// Resolver resolves client IPs. It trusts X-Forwarded-For only when the direct
// peer is inside one of the trusted CIDRs.
type Resolver struct {
	trusted []netip.Prefix
}

// New parses the trusted CIDRs. A bare IP counts as a single-address range.
func New(cidrs []string) (*Resolver, error) {
	r := &Resolver{}
	for _, c := range cidrs {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if p, err := netip.ParsePrefix(c); err == nil {
			r.trusted = append(r.trusted, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(c)
		if err != nil {
			return nil, fmt.Errorf("invalid CIDR %q", c)
		}
		r.trusted = append(r.trusted, netip.PrefixFrom(a, a.BitLen()))
	}
	return r, nil
}

func (r *Resolver) isTrusted(a netip.Addr) bool {
	a = a.Unmap()
	for _, p := range r.trusted {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// IP returns the client IP for req. It walks X-Forwarded-For from right to
// left, skipping trusted proxies, and returns the first other address. If
// every hop is trusted, it returns the leftmost hop. Entries
// further left are client-controlled, so it never reads past that one.
func (r *Resolver) IP(req *http.Request) string {
	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		host = req.RemoteAddr
	}
	peer, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	peer = peer.Unmap()
	if !r.isTrusted(peer) {
		return peer.String()
	}
	var hops []string
	for _, v := range req.Header.Values("X-Forwarded-For") {
		hops = append(hops, strings.Split(v, ",")...)
	}
	leftmost := peer
	for i := len(hops) - 1; i >= 0; i-- {
		a, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			return peer.String()
		}
		a = a.Unmap()
		if !r.isTrusted(a) {
			return a.String()
		}
		leftmost = a
	}
	// Every hop is a trusted proxy, so the leftmost one is the real client
	// (for example a device on the same LAN as the proxy).
	return leftmost.String()
}
