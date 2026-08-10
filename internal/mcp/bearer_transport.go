package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// ErrCredentialStoreLocked reports that a credential exists but cannot be
// read right now because its store is locked.
//
// It is deliberately distinct from "no credential": a locked store means the
// user has a connection and needs to unlock it, whereas a missing credential
// means they need to create one. Collapsing the two sends users to the wrong
// remedy. The mcp package stays storage-agnostic, so the vault-backed adapter
// in server wiring maps the concrete lock error onto this sentinel.
var ErrCredentialStoreLocked = errors.New("mcp: credential store is locked")

// StaticBearerTokenType is the TokenType recorded on a RemoteCredential that
// holds a long-lived token rather than OAuth client material. A static-bearer
// credential sets AccessToken and TokenType and leaves ClientID,
// ClientSecret, RefreshToken, TokenEndpoint, and Expiry empty -- the existing
// RemoteCredential shape covers it, so no second credential type or parallel
// store is needed (this resolves the storage-shape question the PRD raised).
const StaticBearerTokenType = "Bearer"

// SaveStaticBearerToken stores token as cfg's static bearer credential,
// replacing any credential already recorded for its AuthRef. The token goes
// only to the vault-backed credential store; it is never written to
// mcp_registry.json, which carries just the opaque AuthRef.
func SaveStaticBearerToken(ctx context.Context, cfg ServerConfig, token string) error {
	trimmed := strings.TrimSpace(token)
	if trimmed == "" {
		return fmt.Errorf("mcp: a bearer token is required")
	}
	store := credentialStore()
	if store == nil {
		return fmt.Errorf("mcp: remote credential store is not configured")
	}
	// Deliberately not merged with any existing record: a static-bearer
	// credential must not inherit stale OAuth client fields, which would
	// make the stored shape ambiguous to a later reader.
	return store.SaveCredential(ctx, RemoteCredential{
		AuthRef:     NormalizedAuthRef(cfg),
		ServerName:  cfg.Name,
		AccessToken: trimmed,
		TokenType:   StaticBearerTokenType,
	})
}

// LoadStaticBearerToken returns the stored bearer token for cfg, with
// ok == false when no credential has been recorded yet.
func LoadStaticBearerToken(ctx context.Context, cfg ServerConfig) (token string, ok bool, err error) {
	store := credentialStore()
	if store == nil {
		return "", false, fmt.Errorf("mcp: remote credential store is not configured")
	}
	cred, found, err := store.LoadCredential(ctx, NormalizedAuthRef(cfg))
	if err != nil || !found {
		return "", false, err
	}
	trimmed := strings.TrimSpace(cred.AccessToken)
	if trimmed == "" {
		return "", false, nil
	}
	return trimmed, true, nil
}

// DeleteStaticBearerToken removes cfg's stored bearer credential. Deleting a
// credential that does not exist is not an error.
func DeleteStaticBearerToken(ctx context.Context, cfg ServerConfig) error {
	store := credentialStore()
	if store == nil {
		return nil
	}
	return store.DeleteCredential(ctx, NormalizedAuthRef(cfg))
}

// HasRemoteCredentials reports whether cfg has whatever credential material
// its declared auth mode needs before a connect attempt is worth making.
//
// Callers should prefer this over HasOAuthCredentials: the answer for a
// static-bearer server is "is a token stored", which has nothing to do with
// an OAuth ClientID. Asking the OAuth question about a bearer server always
// answers "not configured" and turns a perfectly good connection into a
// spurious credentials-required error.
func HasRemoteCredentials(ctx context.Context, cfg ServerConfig) (bool, error) {
	if NormalizedAuthMode(cfg) == AuthModeStaticBearer {
		_, ok, err := LoadStaticBearerToken(ctx, cfg)
		return ok, err
	}
	return HasOAuthCredentials(ctx, cfg)
}

// buildBearerClient resolves the static token for one remote connect attempt
// and returns a client that presents it. It returns an
// ErrOAuthCredentialsRequired-wrapped error when no token is stored, so
// startRemote's existing reconnect classification surfaces the server as
// "auth required" rather than a hard error -- the same treatment an
// unconfigured OAuth server gets.
func (s *Server) buildBearerClient(ctx context.Context, httpClient *http.Client, endpoint *url.URL) (*http.Client, error) {
	token, ok, err := LoadStaticBearerToken(ctx, s.config)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("%w: connect this server with an access token first", ErrOAuthCredentialsRequired)
	}
	return newBearerHTTPClient(httpClient, endpoint, token)
}

// bearerRoundTripper attaches a static `Authorization: Bearer <token>` header
// to outbound requests for endpoints that authenticate with a long-lived
// token (a personal access token, typically) rather than an OAuth client.
//
// It deliberately wraps the hardened transport built by newRemoteHTTPClient
// rather than replacing it, so the SSRF/private-IP dial guard, the redirect
// validation, and the JSON response-size cap all stay in force underneath.
type bearerRoundTripper struct {
	next http.RoundTripper
	// token is the secret. It is never logged, never formatted into an
	// error, and never read back out of this struct by anything but
	// RoundTrip.
	token string
	// allowedOrigin scopes the credential to the endpoint it was issued
	// for. It is a canonical scheme://host:port string, so a service on a
	// different port of the same host counts as a different destination --
	// which it is.
	//
	// This is a deliberate hardening choice, not an accident of
	// implementation. Injecting the header inside a RoundTripper means it
	// is applied on *every* hop the http.Client makes, including redirect
	// targets -- and Go's usual protection here does not apply: the
	// stdlib only strips a *caller-set* Authorization header on a
	// cross-host redirect, and this header is set below that layer. The
	// hardened client's CheckRedirect re-validates a redirect target's
	// scheme and rejects private hosts, but it does not require the
	// target to be the same host, so a compromised or hostile MCP
	// endpoint could 302 to a server it controls. Scoping by host means
	// same-host hops still carry the credential (which is what keeps a
	// legitimately-redirecting endpoint working) while a cross-host hop
	// silently travels unauthenticated instead of handing over the token.
	allowedOrigin string
}

// RoundTrip implements http.RoundTripper.
func (t *bearerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.next == nil {
		return nil, fmt.Errorf("remote mcp: bearer transport has no underlying transport")
	}

	// RoundTrippers must not modify the request they are handed, so the
	// header goes onto a shallow clone with its own Header map.
	outbound := req.Clone(req.Context())
	if outbound.Header == nil {
		outbound.Header = make(http.Header)
	}
	if t.token != "" && t.allowedOrigin != "" && canonicalOrigin(outbound.URL) == t.allowedOrigin {
		outbound.Header.Set("Authorization", "Bearer "+t.token)
	} else {
		// Defensive: never let a credential ride along to a host it was
		// not issued for, even one an earlier hop already set.
		outbound.Header.Del("Authorization")
	}

	resp, err := t.next.RoundTrip(outbound)
	if err != nil {
		return resp, t.redact(err)
	}
	return resp, nil
}

// redact guarantees the token cannot escape through an error string. Nothing
// in this package formats the token into an error today; this exists so that
// a future change to the wrapped transport (or the stdlib) that echoes the
// request headers back in an error cannot turn into a credential leak.
func (t *bearerRoundTripper) redact(err error) error {
	if err == nil || t.token == "" {
		return err
	}
	msg := err.Error()
	if !strings.Contains(msg, t.token) {
		return err
	}
	return fmt.Errorf("%s", strings.ReplaceAll(msg, t.token, "[redacted]"))
}

// canonicalOrigin renders u as a comparable scheme://host:port string,
// lower-casing the scheme and hostname and filling in the scheme's default
// port when the URL omits it, so that https://example.com and
// https://example.com:443 compare equal while https://example.com:8443 does
// not. Returns "" for a URL too incomplete to compare, which callers treat as
// "not the allowed origin".
func canonicalOrigin(u *url.URL) string {
	if u == nil {
		return ""
	}
	scheme := strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Hostname())
	if scheme == "" || host == "" {
		return ""
	}
	port := u.Port()
	if port == "" {
		switch scheme {
		case "https":
			port = "443"
		case "http":
			port = "80"
		default:
			return ""
		}
	}
	return scheme + "://" + host + ":" + port
}

// newBearerHTTPClient returns a copy of base whose transport attaches the
// static bearer token for requests to endpoint's host. base is left
// untouched, and its CheckRedirect (which enforces the redirect cap and
// re-validates every target) is preserved.
func newBearerHTTPClient(base *http.Client, endpoint *url.URL, token string) (*http.Client, error) {
	if base == nil {
		return nil, fmt.Errorf("remote mcp: bearer client requires a base http client")
	}
	if endpoint == nil {
		return nil, fmt.Errorf("remote mcp: bearer client requires an endpoint")
	}
	if strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("remote mcp: bearer client requires a token")
	}
	origin := canonicalOrigin(endpoint)
	if origin == "" {
		return nil, fmt.Errorf("remote mcp: bearer client requires an absolute https endpoint")
	}

	next := base.Transport
	if next == nil {
		next = http.DefaultTransport
	}

	wrapped := *base
	wrapped.Transport = &bearerRoundTripper{
		next:          next,
		token:         strings.TrimSpace(token),
		allowedOrigin: origin,
	}
	return &wrapped, nil
}
