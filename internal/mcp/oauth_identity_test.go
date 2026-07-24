package mcp

import (
	"net/url"
	"strings"
	"testing"
)

func TestIsGoogleMCPEndpoint(t *testing.T) {
	cases := map[string]bool{
		"https://calendarmcp.googleapis.com/mcp/v1": true,
		"https://drivemcp.googleapis.com/mcp/v1":    true,
		"https://CalendarMCP.googleapis.com/mcp/v1": true, // case-insensitive host
		"https://mcp.example.com/v1":                false,
		"https://gmailmcp.googleapis.com/mcp/v1":    false, // Gmail is native, not MCP
		"":                                          false,
	}
	for endpoint, want := range cases {
		if got := isGoogleMCPEndpoint(endpoint); got != want {
			t.Errorf("isGoogleMCPEndpoint(%q) = %v, want %v", endpoint, got, want)
		}
	}
}

func TestInjectGoogleIdentityScopes(t *testing.T) {
	base := "https://accounts.google.com/o/oauth2/v2/auth?" +
		"client_id=abc&state=xyz&code_challenge=chal&code_challenge_method=S256&nonce=n1&scope=" +
		url.QueryEscape("https://www.googleapis.com/auth/calendar.events.readonly")

	out, err := injectGoogleIdentityScopes(base)
	if err != nil {
		t.Fatalf("inject: %v", err)
	}
	q := mustQuery(t, out)

	scope := q.Get("scope")
	for _, want := range []string{"openid", "email", "https://www.googleapis.com/auth/calendar.events.readonly"} {
		if !strings.Contains(scope, want) {
			t.Errorf("scope %q missing %q", scope, want)
		}
	}
	// State, PKCE, nonce, client_id must be untouched.
	if q.Get("state") != "xyz" || q.Get("code_challenge") != "chal" || q.Get("code_challenge_method") != "S256" || q.Get("nonce") != "n1" || q.Get("client_id") != "abc" {
		t.Errorf("non-scope params changed: %v", q)
	}
}

func TestInjectGoogleIdentityScopes_DedupesOpenID(t *testing.T) {
	base := "https://accounts.google.com/o/oauth2/v2/auth?scope=" +
		url.QueryEscape("openid https://www.googleapis.com/auth/calendar.readonly")
	out, _ := injectGoogleIdentityScopes(base)
	scope := mustQuery(t, out).Get("scope")
	if strings.Count(scope, "openid") != 1 {
		t.Errorf("openid should not duplicate: %q", scope)
	}
	if !strings.Contains(scope, "email") {
		t.Errorf("email should be added: %q", scope)
	}
}

func TestInjectGoogleIdentityScopes_NoExistingScope(t *testing.T) {
	out, err := injectGoogleIdentityScopes("https://accounts.google.com/o/oauth2/v2/auth?client_id=abc")
	if err != nil {
		t.Fatalf("inject: %v", err)
	}
	scope := mustQuery(t, out).Get("scope")
	if !strings.Contains(scope, "openid") || !strings.Contains(scope, "email") {
		t.Errorf("identity scopes not set on empty scope: %q", scope)
	}
}

func mustQuery(t *testing.T, rawURL string) url.Values {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse %q: %v", rawURL, err)
	}
	return u.Query()
}
