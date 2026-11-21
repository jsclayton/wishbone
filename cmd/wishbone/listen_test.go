package main

import (
	"net"
	"testing"
	"time"

	"github.com/alecthomas/kong"
	"github.com/stretchr/testify/require"

	"jsclayton.dev/wishbone/internal/config"
)

// otherAddress returns an IPv4 address of this machine that is not loopback,
// the address a device on the local network would use.
func otherAddress(t *testing.T) net.IP {
	t.Helper()
	addrs, err := net.InterfaceAddrs()
	require.NoError(t, err)
	for _, a := range addrs {
		n, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		if ip := n.IP.To4(); ip != nil && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() {
			return ip
		}
	}
	t.Skip("this machine has no address other than loopback")
	return nil
}

// reachable reports whether a TCP connection to ip on the listener's port opens.
func reachable(t *testing.T, ln net.Listener, ip net.IP) bool {
	t.Helper()
	port := ln.Addr().(*net.TCPAddr).Port
	conn, err := net.DialTimeout("tcp", (&net.TCPAddr{IP: ip, Port: port}).String(), time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func listenFor(t *testing.T, address string) net.Listener {
	t.Helper()
	ln, err := listen(t.Context(), config.Config{ListenAddress: address, Port: 0})
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	// Accept and drop connections, so a dial that reaches the socket completes.
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	return ln
}

func TestServeListensOnlyOnTheListenAddress(t *testing.T) {
	other := otherAddress(t)
	ln := listenFor(t, "127.0.0.1")

	require.True(t, ln.Addr().(*net.TCPAddr).IP.IsLoopback(), ln.Addr().String())
	require.True(t, reachable(t, ln, net.IPv4(127, 0, 0, 1)))
	require.False(t, reachable(t, ln, other), "reached on %s", other)
}

func TestServeListensOnEveryAddressWithoutTheSetting(t *testing.T) {
	other := otherAddress(t)
	ln := listenFor(t, "")

	require.True(t, ln.Addr().(*net.TCPAddr).IP.IsUnspecified(), ln.Addr().String())
	require.True(t, reachable(t, ln, net.IPv4(127, 0, 0, 1)))
	require.True(t, reachable(t, ln, other), "not reached on %s", other)
}

func TestServeRefusesAListenAddressThatIsNotAnIP(t *testing.T) {
	t.Setenv("WISHBONE_LISTEN_ADDRESS", "")
	var cli CLI
	k, err := kong.New(&cli)
	require.NoError(t, err)
	_, err = k.Parse([]string{"serve", "--public-url", "http://localhost", "--listen-address", "localhost"})
	require.EqualError(t, err, `serve: WISHBONE_LISTEN_ADDRESS: "localhost" is not an IP address`)
}
