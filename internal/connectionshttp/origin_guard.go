// Package connectionshttp holds the HTTP layer for the Google connection
// domain. For now it ships only the origin/host guard that protects the
// connection-mutating endpoints; the endpoint handlers themselves land in a
// later group and mount behind this guard.
package connectionshttp

import (
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// OriginGuard defends Ori's local connection-mutating endpoints against
// cross-origin and DNS-rebinding attacks (FR 34). Ori's server is a local,
// browser-driven app: without this guard a malicious page could point a
// hostname at 127.0.0.1 (DNS rebinding) or issue a cross-origin request to
// drive connect/enable/disconnect/revoke. The guard requires the request's Host
// — and, when present, its Origin — to be one of Ori's own local origins.
type OriginGuard struct {
	allowedHosts map[string]struct{} // hostnames only (no port), lowercased
}

// ErrCrossOrigin is returned by Check when a request fails the origin/host test.
var ErrCrossOrigin = errors.New("connectionshttp: cross-origin or rebinding request rejected")

// DefaultLocalHosts are the loopback hostnames Ori serves on.
func DefaultLocalHosts() []string { return []string{"localhost", "127.0.0.1", "::1"} }

// NewOriginGuard builds a guard allowing the loopback defaults plus any extra
// hostnames. Ports are ignored — only the hostname is matched.
func NewOriginGuard(extraHosts ...string) *OriginGuard {
	g := &OriginGuard{allowedHosts: make(map[string]struct{})}
	for _, h := range append(DefaultLocalHosts(), extraHosts...) {
		if h = strings.ToLower(strings.TrimSpace(h)); h != "" {
			g.allowedHosts[h] = struct{}{}
		}
	}
	return g
}

// Check validates a request. It returns ErrCrossOrigin when the Host header is
// not a known local host, or when an Origin header is present and is not a known
// local origin (the DNS-rebinding case: the browser still sends the attacker's
// Origin even though the request reaches 127.0.0.1).
func (g *OriginGuard) Check(r *http.Request) error {
	if !g.hostAllowed(r.Host) {
		return ErrCrossOrigin
	}
	if origin := strings.TrimSpace(r.Header.Get("Origin")); origin != "" {
		u, err := url.Parse(origin)
		if err != nil || !g.hostAllowed(u.Host) {
			return ErrCrossOrigin
		}
	}
	return nil
}

// Wrap returns middleware that 403s any request failing Check before it reaches
// next.
func (g *OriginGuard) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := g.Check(r); err != nil {
			http.Error(w, "cross-origin request rejected", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (g *OriginGuard) hostAllowed(hostport string) bool {
	host := strings.TrimSpace(hostport)
	if host == "" {
		return false
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(strings.ToLower(host), "[]") // normalize IPv6 brackets
	_, ok := g.allowedHosts[host]
	return ok
}
