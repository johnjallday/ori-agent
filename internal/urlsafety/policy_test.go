package urlsafety

import (
	"net/netip"
	"testing"
)

func TestParseAllowsOnlyHTTPPublicHosts(t *testing.T) {
	for _, raw := range []string{"http://127.0.0.1/x", "http://localhost/x", "http://10.0.0.1/x", "file:///tmp/x", "javascript:alert(1)"} {
		if _, err := Parse(raw, Policy{BlockPrivateHosts: true}); err == nil {
			t.Fatalf("unsafe URL accepted: %s", raw)
		}
	}
	parsed, err := Parse("https://example.com/path", Policy{BlockPrivateHosts: true})
	if err != nil || parsed.Hostname() != "example.com" {
		t.Fatalf("public URL = %v, %v", parsed, err)
	}
}

func TestPrivateIPUnmapsIPv4MappedAddresses(t *testing.T) {
	if !IsPrivateIP(netip.MustParseAddr("::ffff:127.0.0.1")) {
		t.Fatal("IPv4-mapped loopback was not private")
	}
}
