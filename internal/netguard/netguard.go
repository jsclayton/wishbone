// Package netguard keeps server-side fetches of user-supplied URLs away from
// the host's own network. It is the one copy of this rule: the page fetcher,
// the store-link follower and the image downloader all dial through it.
package netguard

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sync"
	"syscall"
)

// globalUnicast is the only IPv6 space [RefusePrivate] allows. IANA hands out
// public addresses from it; the rest of the IPv6 space is reserved or special
// purpose (loopback, NAT64, discard-only, SRv6, site-local, and whatever IANA
// reserves later). One check refuses all of it, with no list to keep current.
// IPv4-mapped addresses are unmapped first and use the IPv4 rules.
var globalUnicast = netip.MustParsePrefix("2000::/3")

// deniedPrefixes are non-public ranges the netip helpers don't cover. The IPv6
// entries are the ranges inside [globalUnicast] that must still be refused.
var deniedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"), // CGNAT, Tailscale
	netip.MustParsePrefix("198.18.0.0/15"), // benchmarking
	// 6to4 carries an IPv4 address. Refused whole; the embedded address is not
	// decoded.
	netip.MustParsePrefix("2002::/16"), // 6to4
	// The IETF protocol assignments block (for example Teredo, benchmarking,
	// ORCHID, AMT and AS112). No public site lives here, so it is refused
	// whole, which also covers later assignments. It ends at 2001:1ff:..., so
	// ordinary space such as 2001:200::/23 stays allowed.
	netip.MustParsePrefix("2001::/23"),
	// Reserved IPv4 space.
	netip.MustParsePrefix("192.0.0.0/24"),       // IETF protocol assignments
	netip.MustParsePrefix("240.0.0.0/4"),        // reserved
	netip.MustParsePrefix("255.255.255.255/32"), // limited broadcast
}

// ErrPrivateAddress is what [RefusePrivate] wraps when it blocks a connection,
// including to an address it can't check. It passes through net.OpError and
// url.Error, so callers find it with errors.Is.
var ErrPrivateAddress = errors.New("refusing to connect to non-public address")

// RefusePrivate is a net.Dialer Control that blocks non-public addresses. It
// runs after DNS, on every connection, so redirects and DNS rebinding can't
// skip it. Dial with [DialContext], not with RefusePrivate alone: a dialer
// reports only the first address's error, so a refusal of a later address is
// lost.
func RefusePrivate(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrPrivateAddress, err)
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrPrivateAddress, err)
	}
	// Prefix.Contains is false for any zoned address, so drop the zone.
	ip = ip.Unmap().WithZone("")
	if ip.Is6() && !globalUnicast.Contains(ip) {
		return fmt.Errorf("%w %s", ErrPrivateAddress, ip)
	}
	for _, p := range deniedPrefixes {
		if p.Contains(ip) {
			return fmt.Errorf("%w %s", ErrPrivateAddress, ip)
		}
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified() {
		return fmt.Errorf("%w %s", ErrPrivateAddress, ip)
	}
	return nil
}

// refusals collects what [RefusePrivate] blocked during one dial. It lives on
// the dial's context, because a net.Dialer that fails on one address and then
// tries the next returns only the first error, and a later refusal would be lost.
type refusals struct {
	mu  sync.Mutex
	err error
}

type refusalsKey struct{}

func (r *refusals) record(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err == nil {
		r.err = err
	}
}

func (r *refusals) first() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.err
}

// DialContext returns a DialContext function for an http.Transport. It dials
// with a copy of d that runs [RefusePrivate] on every address. If the dial
// fails and the guard refused any address the dialer tried, the error wraps
// [ErrPrivateAddress], whatever its position in the list. A dial that
// connects to a public address succeeds as usual.
//
// d is not changed. The copy's ControlContext replaces any Control or
// ControlContext that d has.
func DialContext(d *net.Dialer) func(ctx context.Context, network, address string) (net.Conn, error) {
	return dialContext(d, RefusePrivate)
}

// dialContext is [DialContext] with the address check as a parameter, so a
// test can let a loopback listener stand in for a public address.
func dialContext(d *net.Dialer, check func(network, address string, c syscall.RawConn) error) func(ctx context.Context, network, address string) (net.Conn, error) {
	guarded := *d
	guarded.Control = nil
	guarded.ControlContext = func(ctx context.Context, network, address string, c syscall.RawConn) error {
		err := check(network, address, c)
		if err != nil {
			if r, ok := ctx.Value(refusalsKey{}).(*refusals); ok {
				r.record(err)
			}
		}
		return err
	}
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		r := &refusals{}
		conn, err := guarded.DialContext(context.WithValue(ctx, refusalsKey{}, r), network, address)
		if err == nil {
			return conn, nil
		}
		if refusal := r.first(); refusal != nil && !errors.Is(err, ErrPrivateAddress) {
			return nil, fmt.Errorf("%w; dial failed: %w", refusal, err)
		}
		return nil, err
	}
}
