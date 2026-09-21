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
	return &http.Transport{DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("ssrf check: invalid address %q: %w", addr, err)
		}
		ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, fmt.Errorf("ssrf check: dns lookup failed for %q: %w", host, err)
		}
		if len(ips) == 0 {
			return nil, fmt.Errorf("ssrf check: dns lookup returned no addresses for %q", host)
		}
		for _, ip := range ips {
			if IsPrivateIP(ip) {
				return nil, fmt.Errorf("ssrf check: resolved to private IP %s", ip)
			}
		}
		dialer := &net.Dialer{Timeout: DefaultTimeout}
		var dialErr error
		for _, ip := range ips {
			// Dial the address that passed validation instead of resolving the
			// hostname again, which would leave a DNS-rebinding window.
			conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return conn, nil
			}
			dialErr = err
		}
		return nil, fmt.Errorf("ssrf check: connect to %q: %w", host, dialErr)
	}}
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
