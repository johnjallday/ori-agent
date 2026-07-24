package mcp

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

const (
	remoteDialTimeout           = 10 * time.Second
	remoteTLSHandshakeTimeout   = 10 * time.Second
	remoteResponseHeaderTimeout = 20 * time.Second
	remoteMaxRedirects          = 5
	remoteMaxJSONResponseBytes  = 5 << 20 // 5 MiB; applies to non-streaming (JSON) responses only
)

// ValidateRemoteEndpoint enforces the safety policy for a remote MCP server
// URL: HTTPS only, no embedded userinfo credentials or fragment, and a host
// that isn't a local/private-network address. It runs at config-save time
// and again on every dial (including redirect hops) via the hardened
// transport's DialContext, since a hostname that resolves safely at save
// time could still be re-pointed at a private address later (DNS rebinding).
func ValidateRemoteEndpoint(rawURL string) (*url.URL, error) {
	candidate := strings.TrimSpace(rawURL)
	if candidate == "" {
		return nil, fmt.Errorf("remote MCP url is required")
	}

	parsed, err := url.Parse(candidate)
	if err != nil {
		return nil, fmt.Errorf("invalid remote MCP url: %w", err)
	}
	if !strings.EqualFold(parsed.Scheme, "https") {
		return nil, fmt.Errorf("remote MCP url must use https")
	}
	if parsed.User != nil {
		return nil, fmt.Errorf("remote MCP url must not include userinfo credentials")
	}
	if parsed.Fragment != "" {
		return nil, fmt.Errorf("remote MCP url must not include a fragment")
	}

	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	if host == "" {
		return nil, fmt.Errorf("remote MCP url host is required")
	}
	if isPrivateOrLocalRemoteHost(host) {
		return nil, fmt.Errorf("remote MCP url must not target a local or private-network host")
	}

	return parsed, nil
}

// allowPrivateRemoteHostsForTests disables the private/loopback-host guard
// (and, alongside it, TLS certificate verification) so in-package tests can
// point Server.startRemote at an httptest server. It is unexported and only
// ever set from _test.go files in this package -- there is no env var or
// exported API that could flip it in a running server.
//
// It is an atomic.Bool because a test's t.Cleanup resets it on the test
// goroutine while a still-in-flight transport dial (e.g. a reconnect the test
// kicked off) may read it concurrently — a plain bool races there under -race.
var allowPrivateRemoteHostsForTests atomic.Bool

func isPrivateOrLocalRemoteHost(host string) bool {
	if allowPrivateRemoteHostsForTests.Load() {
		return false
	}
	if host == "localhost" || strings.HasSuffix(host, ".local") {
		return true
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		// Not a literal IP; DNS-resolution-time and dial-time checks below
		// still guard against the hostname resolving to a private address.
		return false
	}
	return isPrivateRemoteIP(addr)
}

func isPrivateRemoteIP(addr netip.Addr) bool {
	return addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() ||
		addr.IsLinkLocalMulticast() || addr.IsMulticast() || addr.IsUnspecified()
}

// newRemoteHTTPClient returns an HTTP client hardened for calling
// user-configured remote MCP endpoints:
//   - no implicit OS/env proxy, which could otherwise route traffic around
//     destination validation
//   - dial-time DNS resolution + private-IP rejection on every connection,
//     including redirect hops, since Go's client re-dials through the same
//     Transport for each hop
//   - a CheckRedirect hook that re-runs full URL validation (scheme,
//     userinfo, fragment) on every redirect target
//   - bounded dial/TLS/header timeouts and a response-size cap for
//     non-streaming (JSON) responses
//
// No blanket client Timeout is set: the Streamable HTTP transport keeps a
// long-lived SSE stream open for server-initiated messages, and per-call
// bounds are already enforced via context (see resolveMCPInitTimeout /
// resolveMCPOAuthTimeout and the context passed to CallTool).
func newRemoteHTTPClient() *http.Client {
	dialer := &net.Dialer{Timeout: remoteDialTimeout}

	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, fmt.Errorf("remote mcp: invalid address %q: %w", addr, err)
			}

			ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
			if err != nil {
				return nil, fmt.Errorf("remote mcp: dns lookup failed for %q: %w", host, err)
			}
			if len(ips) == 0 {
				return nil, fmt.Errorf("remote mcp: no addresses found for %q", host)
			}
			if !allowPrivateRemoteHostsForTests.Load() {
				for _, ip := range ips {
					if isPrivateRemoteIP(ip) {
						return nil, fmt.Errorf("remote mcp: %q resolved to a blocked private address %s", host, ip)
					}
				}
			}

			return dialer.DialContext(ctx, network, net.JoinHostPort(host, port))
		},
		TLSHandshakeTimeout:   remoteTLSHandshakeTimeout,
		ResponseHeaderTimeout: remoteResponseHeaderTimeout,
		ForceAttemptHTTP2:     true,
		// #nosec G402 -- InsecureSkipVerify only ever engages via the
		// unexported test-only flag above (never reachable in production).
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: allowPrivateRemoteHostsForTests.Load()},
	}

	return &http.Client{
		Transport: &sizeLimitedRoundTripper{next: transport, maxBytes: remoteMaxJSONResponseBytes},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= remoteMaxRedirects {
				return fmt.Errorf("remote mcp: too many redirects")
			}
			if _, err := ValidateRemoteEndpoint(req.URL.String()); err != nil {
				return fmt.Errorf("remote mcp: redirect target rejected: %w", err)
			}
			return nil
		},
	}
}

// sizeLimitedRoundTripper caps the body size of non-streaming JSON responses.
// Server-Sent-Events responses are deliberately left uncapped: the
// standalone SSE stream is a legitimate long-lived, low-throughput channel
// for server-initiated messages, and a byte cap there would force spurious
// reconnects rather than defend against anything. Bounding the JSON path
// (initialize, list-tools, single-shot tool-call responses) is what prevents
// a malicious or compromised server from returning an unbounded body.
type sizeLimitedRoundTripper struct {
	next     http.RoundTripper
	maxBytes int64
}

func (t *sizeLimitedRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.next.RoundTrip(req)
	if err != nil || resp == nil || resp.Body == nil {
		return resp, err
	}
	if strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
		return resp, nil
	}
	resp.Body = &limitedReadCloser{ReadCloser: resp.Body, remaining: t.maxBytes}
	return resp, nil
}

// limitedReadCloser wraps a response body and errors once more than
// remaining bytes have been read, rather than silently truncating (which
// would let a caller mistake a capped body for a complete one).
type limitedReadCloser struct {
	io.ReadCloser
	remaining int64
}

func (l *limitedReadCloser) Read(p []byte) (int, error) {
	if l.remaining <= 0 {
		return 0, fmt.Errorf("remote mcp: response exceeded size limit")
	}
	if int64(len(p)) > l.remaining {
		p = p[:l.remaining]
	}
	n, err := l.ReadCloser.Read(p)
	l.remaining -= int64(n)
	return n, err
}
