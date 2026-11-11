package clientip

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func req(remote string, xff ...string) *http.Request {
	r := &http.Request{RemoteAddr: remote, Header: http.Header{}}
	for _, v := range xff {
		r.Header.Add("X-Forwarded-For", v)
	}
	return r
}

func resolver(t *testing.T, cidrs ...string) *Resolver {
	t.Helper()
	res, err := New(cidrs)
	require.NoError(t, err)
	return res
}

func TestTrustedProxyUsesForwardedFor(t *testing.T) {
	r := resolver(t, "10.0.0.0/8")
	require.Equal(t, "203.0.113.7", r.IP(req("10.1.2.3:5555", "203.0.113.7")))
}

func TestUntrustedPeerIgnoresForwardedFor(t *testing.T) {
	r := resolver(t, "10.0.0.0/8")
	require.Equal(t, "198.51.100.9", r.IP(req("198.51.100.9:4000", "203.0.113.7")))
}

func TestNoTrustedProxiesIgnoresForwardedFor(t *testing.T) {
	r := resolver(t)
	require.Equal(t, "198.51.100.9", r.IP(req("198.51.100.9:4000", "203.0.113.7")))
}

func TestSpoofedHeaderBehindTrustedProxy(t *testing.T) {
	// The client sent "1.2.3.4"; the proxy appended the real peer address.
	r := resolver(t, "10.0.0.0/8")
	require.Equal(t, "203.0.113.7", r.IP(req("10.1.2.3:1", "1.2.3.4, 203.0.113.7")))
}

func TestChainOfTrustedProxiesIsSkipped(t *testing.T) {
	r := resolver(t, "10.0.0.0/8")
	require.Equal(t, "203.0.113.7", r.IP(req("10.1.2.3:1", "203.0.113.7", "10.9.9.9")))
}

func TestGarbageHeaderFallsBackToPeer(t *testing.T) {
	r := resolver(t, "10.0.0.0/8")
	require.Equal(t, "10.1.2.3", r.IP(req("10.1.2.3:1", "not-an-ip")))
	require.Equal(t, "10.1.2.3", r.IP(req("10.1.2.3:1")))
}

func TestIPv6(t *testing.T) {
	r := resolver(t, "fd00::/8")
	require.Equal(t, "2001:db8::1", r.IP(req("[fd00::2]:1", "2001:db8::1")))
}

func TestInvalidCIDR(t *testing.T) {
	_, err := New([]string{"nope"})
	require.Error(t, err)
}

func TestAllHopsTrustedReturnsLeftmost(t *testing.T) {
	r := resolver(t, "10.0.0.0/8")
	require.Equal(t, "10.5.5.5", r.IP(req("10.1.2.3:1", "10.5.5.5, 10.9.9.9")))
}
