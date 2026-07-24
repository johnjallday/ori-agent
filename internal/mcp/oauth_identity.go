package mcp

import (
	"net/url"
	"strings"
)

// Google Workspace MCP servers (Calendar, Drive) authorize against
// accounts.google.com — Google's OIDC provider. That lets Ori obtain a
// verifiable account subject for the grant by requesting the OIDC scopes
// alongside the product scopes, then reading the ID token from the token
// response. This file holds the targeted, Google-only helpers for that; every
// other MCP server is left exactly as before.
var googleMCPHosts = map[string]bool{
	"calendarmcp.googleapis.com": true,
	"drivemcp.googleapis.com":    true,
}

// isGoogleMCPEndpoint reports whether an MCP endpoint URL is a Google Workspace
// MCP server (whose OAuth authorization server is accounts.google.com).
func isGoogleMCPEndpoint(endpoint string) bool {
	u, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil {
		return false
	}
	return googleMCPHosts[strings.ToLower(u.Hostname())]
}

// googleIdentityScopes are merged into a Google MCP authorization so the token
// response carries an ID token Ori can verify to bind the grant to an account
// subject (FR 23). `openid` triggers the ID token; `email` populates it.
var googleIdentityScopes = []string{"openid", "email"}

// injectGoogleIdentityScopes returns authorizeURL with the OIDC identity scopes
// merged into its `scope` parameter (deduplicated, order-stable, product scopes
// first). Every other parameter — state, PKCE challenge, redirect_uri, nonce —
// is left untouched, so it rides the SDK's existing flow. An unparseable URL is
// returned unchanged with the error, so callers can fall back to the original.
func injectGoogleIdentityScopes(authorizeURL string) (string, error) {
	u, err := url.Parse(authorizeURL)
	if err != nil {
		return authorizeURL, err
	}
	q := u.Query()

	merged := strings.Fields(q.Get("scope"))
	seen := make(map[string]bool, len(merged))
	for _, s := range merged {
		seen[s] = true
	}
	for _, s := range googleIdentityScopes {
		if !seen[s] {
			merged = append(merged, s)
			seen[s] = true
		}
	}

	q.Set("scope", strings.Join(merged, " "))
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// googleMCPIdentityHook, when set by the server layer, receives the raw ID token
// from a Google MCP authorization so it can verify the subject and attach the
// grant to the active connection (FR 23). Decoupled via a package var so
// internal/mcp never imports the connection domain.
var googleMCPIdentityHook func(serverName, endpoint, rawIDToken, clientID string)

// SetGoogleMCPIdentityHook installs (or clears, with nil) the identity hook.
func SetGoogleMCPIdentityHook(fn func(serverName, endpoint, rawIDToken, clientID string)) {
	googleMCPIdentityHook = fn
}
