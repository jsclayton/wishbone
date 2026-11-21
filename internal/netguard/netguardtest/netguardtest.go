// Package netguardtest is test support for code that dials through netguard.
package netguardtest

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"golang.org/x/net/dns/dnsmessage"
)

// Resolver answers A queries from an in-process UDP server and returns a
// resolver that talks only to it. It answers A records only, so every address
// must be IPv4; Resolver fails the test on any other address. answers maps a
// host name to its addresses, in the order the dialer should try them. Other
// names get NXDOMAIN, so a lookup of them fails with a [net.DNSError] whose
// IsNotFound is true. Pass a nil map to answer "no such host" for every name.
func Resolver(t *testing.T, answers map[string][]netip.Addr) *net.Resolver {
	t.Helper()
	for name, ips := range answers {
		for _, ip := range ips {
			if !ip.Is4() {
				t.Fatalf("netguardtest.Resolver: %s has address %s; only IPv4 addresses are supported", name, ip)
			}
		}
	}
	pc, err := (&net.ListenConfig{}).ListenPacket(t.Context(), "udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	go func() {
		buf := make([]byte, 512)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			var req dnsmessage.Message
			if req.Unpack(buf[:n]) != nil || len(req.Questions) != 1 {
				continue
			}
			q := req.Questions[0]
			resp := dnsmessage.Message{
				Header:    dnsmessage.Header{ID: req.ID, Response: true, Authoritative: true, RecursionAvailable: true},
				Questions: req.Questions,
			}
			ips, known := answers[strings.TrimSuffix(q.Name.String(), ".")]
			if !known {
				resp.RCode = dnsmessage.RCodeNameError
			}
			if q.Type == dnsmessage.TypeA {
				for _, ip := range ips {
					resp.Answers = append(resp.Answers, dnsmessage.Resource{
						Header: dnsmessage.ResourceHeader{Name: q.Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 60},
						Body:   &dnsmessage.AResource{A: ip.As4()},
					})
				}
			}
			out, err := resp.Pack()
			if err == nil {
				_, _ = pc.WriteTo(out, from)
			}
		}
	}()
	return &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, pc.LocalAddr().String())
	}}
}

// RunWithoutSystemDNS runs the tests with a [net.DefaultResolver] that sends
// no query to the machine's DNS. Call it from TestMain. A lookup that would
// reach the machine's DNS fails, and the run fails after the tests with the
// number of such lookups. Give the code under test a [Resolver] instead. Names
// in the hosts file, such as localhost, still resolve.
func RunWithoutSystemDNS(m *testing.M) int {
	var reached atomic.Int64
	net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: func(context.Context, string, string) (net.Conn, error) {
		reached.Add(1)
		return nil, errors.New("netguardtest: a test reached the machine's DNS")
	}}
	code := m.Run()
	if n := reached.Load(); n > 0 {
		fmt.Fprintf(os.Stderr, "netguardtest: tests sent %d DNS queries to the machine's resolver; give the code a netguardtest.Resolver\n", n)
		if code == 0 {
			code = 1
		}
	}
	return code
}
