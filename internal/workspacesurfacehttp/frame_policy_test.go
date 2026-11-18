package workspacesurfacehttp

import (
	"crypto/tls"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFrameAssetPolicyPinsOnlyCurrentHostAndVersionedPath(t *testing.T) {
	for _, host := range []string{"localhost:8948", "127.0.0.1:8765", "ori.example", "[::1]:8765"} {
		t.Run(host, func(t *testing.T) {
			policy, err := frameAssetPolicy(host, "http", "opaque-token", "version-digest")
			if err != nil {
				t.Fatal(err)
			}
			prefix := "http://" + host + "/api/workspace-surfaces/frames/opaque-token/version-digest/"
			for _, directive := range []string{"script-src " + prefix, "style-src " + prefix, "font-src " + prefix, "img-src " + prefix + " data:", "connect-src 'none'", "object-src 'none'", "base-uri 'none'", "form-action 'none'", "frame-ancestors 'self'"} {
				if !strings.Contains(policy, directive) {
					t.Errorf("missing pinned directive %q", directive)
				}
			}
			for _, unsafe := range []string{"unsafe-inline", "unsafe-eval", "*", "script-src 'self'", "style-src 'self'"} {
				if strings.Contains(policy, unsafe) {
					t.Errorf("policy permits %q", unsafe)
				}
			}
		})
	}
}

func TestFrameAssetPolicyRejectsHostInjectionAndMalformedPorts(t *testing.T) {
	for _, host := range []string{"", "bad; script-src *", "evil.example/path", "evil.example?query", "evil@example.org", "*.example.org", "example.org\r\nInjected: yes", "example.org:0", "example.org:65536", "example.org:", "example.org:invalid", "-bad.example", "bad..example", "example.org%2fother", "[not-ip]:80"} {
		t.Run(host, func(t *testing.T) {
			if policy, err := frameAssetPolicy(host, "http", "token", "version"); err == nil || policy != "" {
				t.Errorf("accepted unsafe host %q", host)
			}
		})
	}
}

func TestFrameAssetPolicyEscapesPathDelimiters(t *testing.T) {
	policy, err := frameAssetPolicy("localhost:8948", "http", "token'; *", "version/other")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(policy, "token';") || strings.Contains(policy, "version/other") || strings.Contains(policy, " *") {
		t.Fatal("path escaped the pinned CSP source")
	}
}

func TestFrameRequestSchemeBindsOnlySameHostParentOrigin(t *testing.T) {
	for _, item := range []struct {
		origin string
		secure bool
		want   string
	}{
		{"", false, "http"}, {"", true, "https"},
		{"http://ori.example:8765", false, "http"}, {"https://ori.example:8765", false, "https"},
	} {
		r := httptest.NewRequest("POST", "http://ori.example:8765/open", nil)
		if item.secure {
			r.TLS = &tls.ConnectionState{}
		}
		r.Header.Set("Origin", item.origin)
		r.Header.Set("X-Forwarded-Proto", "https") // Not a source of authority.
		got, err := frameRequestScheme(r)
		if err != nil || got != item.want {
			t.Fatalf("origin %q: %q %v", item.origin, got, err)
		}
	}
	for _, authority := range []struct{ host, origin string }{
		{"ori.example:443", "https://ori.example"}, {"ori.example", "https://ori.example:443"},
		{"ori.example:80", "http://ori.example"}, {"ori.example", "http://ori.example:80"},
	} {
		r := httptest.NewRequest("POST", "http://"+authority.host+"/open", nil)
		r.Header.Set("Origin", authority.origin)
		if _, err := frameRequestScheme(r); err != nil {
			t.Errorf("rejected equivalent default port: %v", err)
		}
	}
	for _, origin := range []string{"null", "https://other.example:8765", "https://ori.example:443", "https://ori.example:8765/path", "https://ori.example:8765?query", "https://user@ori.example:8765", "javascript:alert(1)"} {
		r := httptest.NewRequest("POST", "http://ori.example:8765/open", nil)
		r.Header.Set("Origin", origin)
		if _, err := frameRequestScheme(r); err == nil {
			t.Errorf("accepted mismatched parent origin %q", origin)
		}
	}
}
