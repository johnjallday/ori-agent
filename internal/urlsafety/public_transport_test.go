package urlsafety

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
)

func TestPublicIPPolicyRejectsPrivateMappedReservedAndSharedAddressSpace(t *testing.T) {
	for _, raw := range []string{"127.0.0.1", "::1", "::ffff:127.0.0.1", "10.0.0.1", "169.254.169.254", "100.64.0.1", "192.0.2.1", "198.51.100.1", "203.0.113.1", "198.18.0.1", "240.0.0.1", "255.255.255.255", "2001:db8::1", "2002:7f00:1::", "fe80::1", "fc00::1"} {
		if !IsNonPublicIP(netip.MustParseAddr(raw)) || !IsNonPublicLiteral(raw) {
			t.Fatalf("nonpublic address allowed: %s", raw)
		}
	}
	for _, raw := range []string{"93.184.216.34", "2606:4700:4700::1111"} {
		if IsNonPublicIP(netip.MustParseAddr(raw)) {
			t.Fatalf("public address blocked: %s", raw)
		}
	}
	// The stricter research policy does not silently change legacy utility or
	// blueprint callers' existing address classification.
	if IsPrivateIP(netip.MustParseAddr("192.0.2.1")) {
		t.Fatal("legacy policy changed")
	}
}

func TestPublicTransportDialsOnlyOnceResolvedValidatedAddresses(t *testing.T) {
	lookups, dials := 0, []string{}
	lookup := func(context.Context, string, string) ([]netip.Addr, error) {
		lookups++
		if lookups > 1 {
			return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
		}
		return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
	}
	dial := func(_ context.Context, _ string, address string) (net.Conn, error) {
		dials = append(dials, address)
		left, right := net.Pipe()
		_ = right.Close()
		return left, nil
	}
	transport := newSafeTransport(IsNonPublicIP, lookup, dial)
	conn, err := transport.DialContext(context.Background(), "tcp", "example.com:443")
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	if lookups != 1 || len(dials) != 1 || dials[0] != "93.184.216.34:443" {
		t.Fatalf("DNS rebinding window: %d %v", lookups, dials)
	}
	if transport.Proxy != nil {
		t.Fatal("transport inherits environment proxies")
	}
}

func TestPublicTransportRejectsMixedAnswersBeforeAnyDial(t *testing.T) {
	for _, blocked := range []string{"127.0.0.1", "169.254.169.254", "100.64.0.1", "192.0.2.1", "::ffff:10.0.0.1"} {
		t.Run(blocked, func(t *testing.T) {
			transport := newSafeTransport(IsNonPublicIP, func(context.Context, string, string) ([]netip.Addr, error) {
				return []netip.Addr{netip.MustParseAddr("93.184.216.34"), netip.MustParseAddr(blocked)}, nil
			}, func(context.Context, string, string) (net.Conn, error) {
				t.Fatal("mixed DNS answer dialled")
				return nil, errors.New("not reached")
			})
			if _, err := transport.DialContext(context.Background(), "tcp", "example.com:443"); err == nil {
				t.Fatal("mixed DNS answer accepted")
			}
		})
	}
}
