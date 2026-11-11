package netguard

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strings"
	"syscall"
	"testing"
	"time"

	"jsclayton.dev/wishbone/internal/netguard/netguardtest"
)

func TestRefusePrivate(t *testing.T) {
	refused := []string{
		"127.0.0.1:80", "10.0.0.1:80", "192.168.1.1:80", "169.254.169.254:80", "0.1.2.3:80",
		"100.101.102.103:80", "198.19.0.1:80", "[::1]:80", "[64:ff9b::a00:1]:80", "[::ffff:10.0.0.1]:80",
		"[::192.168.1.1]:80", "[::c0a8:101]:80", "[::2]:80", "[::ffff:ffff]:80", // ::/96
		"[::ffff:0:c0a8:101]:80",                      // IPv4-translated (SIIT)
		"[2002:c0a8:101::1]:80",                       // 6to4
		"[64:ff9b:1::1]:80", "[64:ff9b:1:ffff::1]:80", // local NAT64
		"[fec0::1]:80", "[feff::1]:80", // site-local
		"[2001::1]:80", "[2001:0:4136:e378:8000:63bf:3fff:fdd2]:80", // Teredo
		"192.0.0.1:80", "192.0.0.255:80", "[::ffff:192.0.0.1]:80", // IETF protocol assignments
		"240.0.0.1:80", "255.255.255.254:80", "255.255.255.255:80", // reserved, broadcast
		"[::ffff:ffff:ffff]:80", // mapped broadcast
		// Zoned: the zone must not hide the address from the prefix checks.
		"[::192.168.1.1%eth0]:80", "[::c0a8:101%eth0]:80", "[2002:c0a8:101::1%x]:80", "[2001::1%x]:80",
		"[fec0::1%1]:80", "[64:ff9b:1::1%x]:80", "[64:ff9b::a00:1%x]:80", "[fe80::1%eth0]:80",
		// Special-purpose IPv6 ranges: outside 2000::/3, or inside 2001::/23.
		"[2001:2::1]:80", "[2001:10::1]:80", "[2001:20::1]:80", // benchmarking, ORCHID
		"[2001:1ff:ffff::1]:80",       // last address of 2001::/23
		"[100::1]:80", "[5f00::1]:80", // discard-only, SRv6 SIDs
		"[::1:0:0:0]:80", "[::ffff:1:0:0]:80", // rest of the reserved ::/8
		"[64:ff9b:2::1]:80", "[1fff:ffff::1]:80", // just below 2000::/3
		"[4000::1]:80",              // just above 2000::/3
		"[100::1%x]:80",             // zoned, outside 2000::/3
		"[::ffff:10.0.0.1%eth0]:80", // zoned IPv4-mapped: zone dropped, then IPv4 rules
		"[fd00::1]:80",              // ULA
		":80", "not-an-ip:80", "noport",
	}
	for _, addr := range refused {
		if err := RefusePrivate("tcp", addr, nil); !errors.Is(err, ErrPrivateAddress) {
			t.Errorf("%s: got %v, want ErrPrivateAddress", addr, err)
		}
	}
	for _, addr := range []string{
		"93.184.216.34:443", "[2606:2800:220:1:248:1893:25c8:1946]:443",
		"192.0.1.1:443", "223.255.255.255:443", "[::ffff:8.8.8.8]:443", // edges of the new ranges
		"[2001:4860:4860::8888]:443", "[2001:200::1]:443", "[2003::1]:443", "[2001:db8::1]:443",
		"[2000::1]:443", "[3fff:ffff::1]:443", "[2606:4700::1]:443",
		"[2606:2800:220:1:248:1893:25c8:1946%eth0]:443", // a zone doesn't change the verdict
	} {
		if err := RefusePrivate("tcp", addr, nil); err != nil {
			t.Errorf("%s refused: %v", addr, err)
		}
	}
}

// allowLoopback lets a loopback address stand in for a public one, which a test
// can't reach. Everything else gets the server's check.
func allowLoopback(network, address string, c syscall.RawConn) error {
	if strings.HasPrefix(address, "127.0.0.1:") {
		return nil
	}
	return RefusePrivate(network, address, c)
}

// closedPort returns a loopback port with nothing listening, so a dial to it
// fails at once. A timeout would not do: the dialer gives an address that
// stalls at least two seconds, whatever Dialer.Timeout says.
func closedPort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	_ = ln.Close()
	return port
}

func TestDialContextRefusalSurvivesAnEarlierFailure(t *testing.T) {
	down, private := netip.MustParseAddr("127.0.0.1"), netip.MustParseAddr("10.0.0.1")
	port := closedPort(t)
	for name, addrs := range map[string][]netip.Addr{
		"public first":  {down, private},
		"private first": {private, down},
	} {
		t.Run(name, func(t *testing.T) {
			d := &net.Dialer{Timeout: time.Second, Resolver: netguardtest.Resolver(t, map[string][]netip.Addr{"mixed.test": addrs})}
			conn, err := dialContext(d, allowLoopback)(context.Background(), "tcp", "mixed.test:"+port)
			if err == nil {
				_ = conn.Close()
			}
			if !errors.Is(err, ErrPrivateAddress) {
				t.Fatalf("got %v, want ErrPrivateAddress", err)
			}
		})
	}
}

func TestDialContextWithoutARefusalKeepsThePlainError(t *testing.T) {
	d := &net.Dialer{Timeout: time.Second, Resolver: netguardtest.Resolver(t, map[string][]netip.Addr{"down.test": {netip.MustParseAddr("127.0.0.1")}})}
	conn, err := dialContext(d, allowLoopback)(context.Background(), "tcp", "down.test:"+closedPort(t))
	if err == nil {
		_ = conn.Close()
		t.Fatal("dial succeeded")
	}
	if errors.Is(err, ErrPrivateAddress) {
		t.Fatalf("plain failure reported as a refusal: %v", err)
	}
}

func TestDialContextRefusesPrivateOnlyName(t *testing.T) {
	d := &net.Dialer{Timeout: time.Second, Resolver: netguardtest.Resolver(t, map[string][]netip.Addr{"lan.test": {netip.MustParseAddr("192.168.1.1")}})}
	if _, err := DialContext(d)(context.Background(), "tcp", "lan.test:80"); !errors.Is(err, ErrPrivateAddress) {
		t.Fatalf("got %v, want ErrPrivateAddress", err)
	}
}

func TestDialContextConnectsToAPublicAddress(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	private := netip.MustParseAddr("10.0.0.1")
	for name, addrs := range map[string][]netip.Addr{
		"private then public": {private, netip.MustParseAddr("127.0.0.1")},
		"public then private": {netip.MustParseAddr("127.0.0.1"), private},
	} {
		t.Run(name, func(t *testing.T) {
			d := &net.Dialer{Timeout: time.Second, Resolver: netguardtest.Resolver(t, map[string][]netip.Addr{"ok.test": addrs})}
			conn, err := dialContext(d, allowLoopback)(context.Background(), "tcp", "ok.test:"+port)
			if err != nil {
				t.Fatalf("dial failed: %v", err)
			}
			_ = conn.Close()
		})
	}
}
