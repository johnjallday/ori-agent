// Package publicread owns bounded, credential-free public HTTP reads. A read
// caller must supply its own exact user authorization; this is not an HTTP API
// or an authorization mechanism.
package publicread

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/internal/sensitive"
	"github.com/johnjallday/ori-agent/internal/urlsafety"
)

const (
	MaxURLBytes      = 2000
	MaxResponseBytes = 1 << 20
	MaxRedirects     = 3
	Timeout          = 10 * time.Second
)

var ErrUnsafeURL = errors.New("public destination is not permitted")
var ErrUnavailable = errors.New("public source unavailable")

var credentialKey = regexp.MustCompile(`(?i)^(?:key|api[_-]?key|access[_-]?(?:key|token)|token|secret|client[_-]?secret|password|auth|authorization|cookie|session(?:id)?|jwt|sig(?:nature)?|code|x-amz-.*|x-goog-.*)$`)
var credentialAssignment = regexp.MustCompile(`(?i)\b(?:api[_ -]?key|access[_-]?token|token|secret|password|authorization|cookie)\s*[:=]\s*\S+`)
var numericHost = regexp.MustCompile(`(?i)^(?:0x[0-9a-f]+|[0-9]+)(?:\.(?:0x[0-9a-f]+|[0-9]+))*$`)

var telegramCredential = regexp.MustCompile(`\b[0-9]{5,}:[A-Za-z0-9_-]{12,}\b`)

// ContainsCredentialMaterial is a conservative projection guard, not a claim
// that arbitrary secrets can be recognized. Exact review is still mandatory.
func ContainsCredentialMaterial(text string) bool {
	return sensitive.ContainsSecretLikeText(text) || credentialAssignment.MatchString(text) || telegramCredential.MatchString(text)
}

// ValidateURL is lexical only. The transport also validates all resolved IPs
// and dials exactly those addresses so DNS rebinding cannot broaden the read.
func ValidateURL(raw string) (string, error) {
	if raw == "" || len(raw) > MaxURLBytes || !utf8.ValidString(raw) || strings.TrimSpace(raw) != raw || ContainsCredentialMaterial(raw) {
		return "", ErrUnsafeURL
	}
	for _, ch := range raw {
		if unicode.IsControl(ch) || unicode.IsSpace(ch) || unicode.Is(unicode.Cf, ch) {
			return "", ErrUnsafeURL
		}
	}
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() || u.Opaque != "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return "", ErrUnsafeURL
	}
	port := map[string]string{"http": "80", "https": "443"}[u.Scheme]
	if u.Port() != "" && u.Port() != port || strings.HasSuffix(u.Host, ":") {
		return "", ErrUnsafeURL
	}
	host := strings.ToLower(u.Hostname())
	if numericHost.MatchString(host) {
		if _, err := netip.ParseAddr(host); err != nil {
			return "", ErrUnsafeURL
		}
	}
	for _, part := range []string{u.Path, u.RawQuery, u.Fragment} {
		decoded, err := url.PathUnescape(part)
		if err != nil {
			return "", ErrUnsafeURL
		}
		if ContainsCredentialMaterial(decoded) {
			return "", ErrUnsafeURL
		}
		for _, ch := range decoded {
			if unicode.IsControl(ch) || unicode.Is(unicode.Cf, ch) {
				return "", ErrUnsafeURL
			}
		}
	}
	for _, suffix := range []string{".localhost", ".internal", ".lan", ".home", ".test", ".invalid"} {
		if strings.HasSuffix(host, suffix) {
			return "", ErrUnsafeURL
		}
	}
	if _, err := urlsafety.Parse(raw, urlsafety.Policy{BlockPrivateHosts: true}); err != nil {
		return "", ErrUnsafeURL
	}
	if urlsafety.IsNonPublicLiteral(host) {
		return "", ErrUnsafeURL
	}
	if ContainsCredentialMaterial(u.Path) {
		return "", ErrUnsafeURL
	}
	for key, values := range u.Query() {
		if credentialKey.MatchString(key) {
			return "", ErrUnsafeURL
		}
		for _, value := range values {
			if ContainsCredentialMaterial(value) {
				return "", ErrUnsafeURL
			}
		}
	}
	return raw, nil
}

type Request struct {
	URL          string
	ContentTypes []string // host-compiled allowlist, not caller-selected headers
	// Only public documents may permit three same-origin redirects. Catalog
	// queries use no redirects, keeping the exact query/destination binding.
	SameOriginRedirects bool
}

type Response struct {
	URL         string
	Body        []byte
	ContentType string
	Hash        string
	ETag        string
	ReadAt      time.Time
}

type Reader interface {
	Read(context.Context, Request) (Response, error)
}

type Client struct{ transport http.RoundTripper }

// NewClient never adopts a caller's CookieJar, default transport/proxies or auth
// headers. Tests inject a controlled transport inside this package, not a
// production policy override for loopback fixtures.
func NewClient() *Client { return &Client{transport: urlsafety.NewPublicTransport()} }

func (c *Client) Read(parent context.Context, request Request) (Response, error) {
	if c == nil || c.transport == nil {
		return Response{}, ErrUnavailable
	}
	if _, err := ValidateURL(request.URL); err != nil {
		return Response{}, err
	}
	ctx, cancel := context.WithTimeout(parent, Timeout)
	defer cancel()
	client := &http.Client{Transport: c.transport, Timeout: Timeout, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if !request.SameOriginRedirects || len(via) > MaxRedirects {
			return ErrUnsafeURL
		}
		if _, err := ValidateURL(req.URL.String()); err != nil {
			return err
		}
		original := via[0].URL
		if req.URL.Scheme != original.Scheme || !strings.EqualFold(req.URL.Host, original.Host) {
			return ErrUnsafeURL
		}
		// No cookies, Authorization or Referer survive a redirect, even within
		// the reviewed origin. No caller-controlled header interface exists.
		req.Header.Del("Authorization")
		req.Header.Del("Cookie")
		req.Header.Del("Proxy-Authorization")
		req.Header.Del("Referer")
		clear(req.Header)
		req.Header.Set("User-Agent", "ori-agent/public-research")
		return nil
	}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, request.URL, nil)
	if err != nil {
		return Response{}, ErrUnsafeURL
	}
	req.Header.Set("User-Agent", "ori-agent/public-research")
	resp, err := client.Do(req)
	if err != nil {
		return Response{}, ErrUnavailable
	} // never echo private DNS/path/server diagnostics
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK || resp.ContentLength > MaxResponseBytes {
		return Response{}, ErrUnavailable
	}
	contentType, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil {
		return Response{}, ErrUnavailable
	}
	allowed := false
	for _, expected := range request.ContentTypes {
		allowed = allowed || contentType == expected
	}
	if !allowed {
		return Response{}, ErrUnavailable
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBytes+1))
	if err != nil || len(body) > MaxResponseBytes || ctx.Err() != nil {
		return Response{}, ErrUnavailable
	}
	digest := sha256.Sum256(body)
	etag := resp.Header.Get("ETag")
	if len(etag) > 128 || !utf8.ValidString(etag) || ContainsCredentialMaterial(etag) || strings.IndexFunc(etag, func(ch rune) bool { return unicode.IsControl(ch) || unicode.Is(unicode.Cf, ch) }) >= 0 {
		etag = ""
	}
	return Response{URL: resp.Request.URL.String(), Body: body, ContentType: contentType, Hash: hex.EncodeToString(digest[:]), ETag: etag, ReadAt: time.Now().UTC()}, nil
}
