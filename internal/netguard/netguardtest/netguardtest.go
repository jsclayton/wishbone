// Package netguardtest is test support for code that dials through netguard.
package netguardtest

import (
	"context"
	"net"
	"net/netip"
	"strings"
	"testing"

	"golang.org/x/net/dns/dnsmessage"
)

// Resolver answers A queries from an in-process UDP server and returns a
// resolver that talks only to it. It answers A records only, so every address
// must be IPv4; Resolver fails the test on any other address. answers maps a
// host name to its addresses, in the order the dialer should try them. Other
// names get an empty answer.
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
			if q.Type == dnsmessage.TypeA {
				for _, ip := range answers[strings.TrimSuffix(q.Name.String(), ".")] {
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
