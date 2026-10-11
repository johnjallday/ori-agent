// Package urlsafety provides the shared SSRF policy for host-owned HTTP fetchers.
package urlsafety

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

const (
	DefaultUserAgent        = "ori-agent/utility-tools"
	DefaultMaxResponseBytes = int64(1 << 20)
	DefaultTimeout          = 10 * time.Second
)

var ErrUnsafeURL = errors.New("unsafe URL")

type Policy struct {
	AllowedDomains    []string
	BlockPrivateHosts bool
}

func Parse(rawURL string, policy Policy) (*url.URL, error) {
	candidate := strings.TrimSpace(rawURL)
	if candidate == "" {
		return nil, fmt.Errorf("%w: url is required", ErrUnsafeURL)
	}
	if !strings.Contains(candidate, "://") {
		candidate = "https://" + candidate
	}
	parsed, err := url.Parse(candidate)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid url", ErrUnsafeURL)
	}
	if !strings.EqualFold(parsed.Scheme, "http") && !strings.EqualFold(parsed.Scheme, "https") {
		return nil, fmt.Errorf("%w: url must use http or https", ErrUnsafeURL)
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	if host == "" {
		return nil, fmt.Errorf("%w: url host is required", ErrUnsafeURL)
	}
	if policy.BlockPrivateHosts && IsPrivateHost(host) {
		return nil, fmt.Errorf("%w: private or local hosts are blocked", ErrUnsafeURL)
	}
	if len(policy.AllowedDomains) > 0 && !MatchesAllowedDomain(host, policy.AllowedDomains) {
		return nil, fmt.Errorf("%w: host %q is not in allowed domains", ErrUnsafeURL, host)
	}
	return parsed, nil
}

func IsPrivateHost(host string) bool {
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".local") {
		return true
	}
	addr, err := netip.ParseAddr(host)
	return err == nil && IsPrivateIP(addr)
}

func IsPrivateIP(addr netip.Addr) bool {
	addr = addr.Unmap()
	return addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() || addr.IsMulticast() || addr.IsUnspecified()
}

func NewSafeTransport() *http.Transport {
	return newSafeTransport(IsPrivateIP, net.DefaultResolver.LookupNetIP, (&net.Dialer{Timeout: DefaultTimeout}).DialContext)
}

// NewPublicTransport additionally excludes non-public/reserved address space
// for research. Existing callers keep their original NewSafeTransport policy.
func NewPublicTransport() *http.Transport {
	transport := newSafeTransport(IsNonPublicIP, net.DefaultResolver.LookupNetIP, (&net.Dialer{Timeout: DefaultTimeout}).DialContext)
	transport.MaxIdleConns, transport.MaxIdleConnsPerHost, transport.MaxConnsPerHost = 8, 2, 4
	transport.IdleConnTimeout = 30 * time.Second
	transport.TLSHandshakeTimeout, transport.ResponseHeaderTimeout = DefaultTimeout, DefaultTimeout
	transport.MaxResponseHeaderBytes = 64 << 10
	return transport
}

func newSafeTransport(blocked func(netip.Addr) bool, lookup func(context.Context, string, string) ([]netip.Addr, error), dial func(context.Context, string, string) (net.Conn, error)) *http.Transport {
	return &http.Transport{DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("ssrf check: invalid address %q: %w", addr, err)
		}
		ips, err := lookup(ctx, "ip", host)
		if err != nil {
			return nil, fmt.Errorf("ssrf check: dns lookup failed for %q: %w", host, err)
		}
		if len(ips) == 0 {
			return nil, fmt.Errorf("ssrf check: dns lookup returned no addresses for %q", host)
		}
		for _, ip := range ips {
			if blocked(ip) {
				return nil, fmt.Errorf("ssrf check: resolved to private IP %s", ip)
			}
		}
		var dialErr error
		for _, ip := range ips {
			// Dial the address that passed validation instead of resolving the
			// hostname again, which would leave a DNS-rebinding window.
			conn, err := dial(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return conn, nil
			}
			dialErr = err
		}
		return nil, fmt.Errorf("ssrf check: connect to %q: %w", host, dialErr)
	}}
}

var nonPublicRanges = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001:db8::/32"), netip.MustParsePrefix("2001::/32"), netip.MustParsePrefix("2002::/16"),
}

func IsNonPublicIP(addr netip.Addr) bool {
	addr = addr.Unmap()
	if !addr.IsGlobalUnicast() || IsPrivateIP(addr) {
		return true
	}
	for _, prefix := range nonPublicRanges {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

func IsNonPublicLiteral(host string) bool {
	addr, err := netip.ParseAddr(host)
	return err == nil && IsNonPublicIP(addr)
}

func MatchesAllowedDomain(host string, allowed []string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	for _, raw := range allowed {
		domain := strings.ToLower(strings.TrimSpace(raw))
		if domain != "" && (host == domain || strings.HasSuffix(host, "."+domain)) {
			return true
		}
	}
	return false
}
