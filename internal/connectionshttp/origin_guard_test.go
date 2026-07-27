package connectionshttp

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func guardReq(host, origin string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "http://placeholder/api/connections/disconnect", nil)
	r.Host = host
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	return r
}

func TestOriginGuard_Check(t *testing.T) {
	g := NewOriginGuard()
	cases := []struct {
		name   string
		host   string
		origin string
		ok     bool
	}{
		{"localhost host", "localhost:8765", "", true},
		{"loopback ip", "127.0.0.1:8765", "", true},
		{"ipv6 loopback", "[::1]:8765", "", true},
		{"local host + local origin", "localhost:8765", "http://localhost:8765", true},
		{"foreign host rejected", "evil.example", "", false},
		{"rebinding: local host + foreign origin", "localhost:8765", "http://evil.example", false},
		{"empty host rejected", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := g.Check(guardReq(tc.host, tc.origin))
			if (err == nil) != tc.ok {
				t.Fatalf("Check(host=%q origin=%q) err=%v, want ok=%v", tc.host, tc.origin, err, tc.ok)
			}
		})
	}
}

func TestOriginGuard_ExtraHost(t *testing.T) {
	g := NewOriginGuard("ori.local")
	if err := g.Check(guardReq("ori.local:9000", "")); err != nil {
		t.Fatalf("configured extra host should pass: %v", err)
	}
}

func TestOriginGuard_Wrap(t *testing.T) {
	g := NewOriginGuard()
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h := g.Wrap(next)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, guardReq("localhost:8765", ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("allowed request should pass through: %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, guardReq("evil.example", ""))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("rejected request should 403: %d", rec.Code)
	}
}
