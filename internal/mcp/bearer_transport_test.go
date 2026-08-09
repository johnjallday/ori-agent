package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

const testBearerToken = "ghp_supersecrettoken"

// recordingRoundTripper captures the Authorization header of every request it
// sees, which is how the redirect-hop assertions below observe each hop.
type recordingRoundTripper struct {
	mu     sync.Mutex
	authBy []string
	next   http.RoundTripper
}

func (r *recordingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	r.authBy = append(r.authBy, req.Header.Get("Authorization"))
	r.mu.Unlock()
	return r.next.RoundTrip(req)
}

func (r *recordingRoundTripper) seen() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.authBy...)
}

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return u
}

func TestBearerRoundTripper_SetsHeaderOnMatchingHost(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	endpoint := mustParseURL(t, srv.URL+"/mcp")
	client, err := newBearerHTTPClient(&http.Client{}, endpoint, testBearerToken)
	if err != nil {
		t.Fatalf("newBearerHTTPClient error: %v", err)
	}

	resp, err := client.Get(endpoint.String())
	if err != nil {
		t.Fatalf("GET error: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if want := "Bearer " + testBearerToken; gotAuth != want {
		t.Fatalf("Authorization = %q, want %q", gotAuth, want)
	}
}

// The header is injected below the layer where Go strips caller-set
// Authorization headers, so same-host redirect hops must still carry it --
// otherwise a legitimately-redirecting endpoint would 401 on the second hop.
func TestBearerRoundTripper_SetsHeaderOnSameHostRedirectHops(t *testing.T) {
	var mu sync.Mutex
	var authByPath = map[string]string{}

	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		authByPath["/mcp"] = r.Header.Get("Authorization")
		mu.Unlock()
		http.Redirect(w, r, "/mcp-moved", http.StatusFound)
	})
	mux.HandleFunc("/mcp-moved", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		authByPath["/mcp-moved"] = r.Header.Get("Authorization")
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	endpoint := mustParseURL(t, srv.URL+"/mcp")
	client, err := newBearerHTTPClient(&http.Client{}, endpoint, testBearerToken)
	if err != nil {
		t.Fatalf("newBearerHTTPClient error: %v", err)
	}

	resp, err := client.Get(endpoint.String())
	if err != nil {
		t.Fatalf("GET error: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	want := "Bearer " + testBearerToken
	mu.Lock()
	defer mu.Unlock()
	for _, path := range []string{"/mcp", "/mcp-moved"} {
		if authByPath[path] != want {
			t.Fatalf("hop %s Authorization = %q, want %q", path, authByPath[path], want)
		}
	}
}

// A hostile or compromised endpoint must not be able to redirect the
// credential to a host it controls.
func TestBearerRoundTripper_DoesNotLeakTokenCrossHost(t *testing.T) {
	var attackerAuth string
	attacker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attackerAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer attacker.Close()

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, attacker.URL+"/steal", http.StatusFound)
	}))
	defer origin.Close()

	endpoint := mustParseURL(t, origin.URL+"/mcp")
	client, err := newBearerHTTPClient(&http.Client{}, endpoint, testBearerToken)
	if err != nil {
		t.Fatalf("newBearerHTTPClient error: %v", err)
	}

	resp, err := client.Get(endpoint.String())
	if err != nil {
		t.Fatalf("GET error: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if attackerAuth != "" {
		t.Fatalf("token leaked to cross-host redirect target: Authorization = %q", attackerAuth)
	}
}

// An Authorization header a caller already placed on the request must not
// survive a cross-host hop either.
func TestBearerRoundTripper_StripsPreexistingAuthOnForeignHost(t *testing.T) {
	rec := &recordingRoundTripper{next: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody, Header: make(http.Header)}, nil
	})}
	rt := &bearerRoundTripper{next: rec, token: testBearerToken, allowedOrigin: "https://api.example.com:443"}

	req, err := http.NewRequest(http.MethodGet, "https://evil.example.net/steal", nil)
	if err != nil {
		t.Fatalf("NewRequest error: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+testBearerToken)

	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip error: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	seen := rec.seen()
	if len(seen) != 1 {
		t.Fatalf("expected exactly one downstream request, got %d", len(seen))
	}
	if seen[0] != "" {
		t.Fatalf("expected Authorization stripped for foreign host, got %q", seen[0])
	}
	// The caller's own request object must be left untouched.
	if req.Header.Get("Authorization") == "" {
		t.Fatal("RoundTrip mutated the caller's request headers")
	}
}

func TestCanonicalOrigin(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{"https://api.example.com/mcp", "https://api.example.com:443"},
		{"https://API.Example.COM/mcp", "https://api.example.com:443"},
		{"https://api.example.com:443/mcp", "https://api.example.com:443"},
		{"https://api.example.com:8443/mcp", "https://api.example.com:8443"},
		{"http://api.example.com/mcp", "http://api.example.com:80"},
		{"ftp://api.example.com/mcp", ""},
		{"/relative/path", ""},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			if got := canonicalOrigin(mustParseURL(t, tc.raw)); got != tc.want {
				t.Fatalf("canonicalOrigin(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
	if got := canonicalOrigin(nil); got != "" {
		t.Fatalf("canonicalOrigin(nil) = %q, want empty", got)
	}
}

// Same hostname, different port is a different destination -- the case that
// makes hostname-only scoping unsafe.
func TestBearerRoundTripper_ScopesByPortNotJustHostname(t *testing.T) {
	rec := &recordingRoundTripper{next: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody, Header: make(http.Header)}, nil
	})}
	rt := &bearerRoundTripper{next: rec, token: testBearerToken, allowedOrigin: "https://127.0.0.1:8443"}

	for _, target := range []string{"https://127.0.0.1:8443/mcp", "https://127.0.0.1:9999/mcp"} {
		req, err := http.NewRequest(http.MethodGet, target, nil)
		if err != nil {
			t.Fatalf("NewRequest error: %v", err)
		}
		resp, err := rt.RoundTrip(req)
		if err != nil {
			t.Fatalf("RoundTrip error: %v", err)
		}
		_ = resp.Body.Close()
	}

	seen := rec.seen()
	if len(seen) != 2 {
		t.Fatalf("expected 2 downstream requests, got %d", len(seen))
	}
	if seen[0] != "Bearer "+testBearerToken {
		t.Fatalf("matching-port request lost its token: %q", seen[0])
	}
	if seen[1] != "" {
		t.Fatalf("token sent to a different port on the same host: %q", seen[1])
	}
}

func TestBearerRoundTripper_RedactsTokenFromErrors(t *testing.T) {
	rt := &bearerRoundTripper{
		next: roundTripFunc(func(*http.Request) (*http.Response, error) {
			// Simulate a transport that echoes the request headers back
			// in its error, which is exactly the leak redact() defends
			// against.
			return nil, fmt.Errorf("dial failed sending Authorization: Bearer %s", testBearerToken)
		}),
		token:         testBearerToken,
		allowedOrigin: "https://api.example.com:443",
	}

	req, err := http.NewRequest(http.MethodGet, "https://api.example.com/mcp", nil)
	if err != nil {
		t.Fatalf("NewRequest error: %v", err)
	}

	_, err = rt.RoundTrip(req)
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), testBearerToken) {
		t.Fatalf("token leaked in error string: %v", err)
	}
	if !strings.Contains(err.Error(), "[redacted]") {
		t.Fatalf("expected redaction marker in error, got: %v", err)
	}
}

// The bearer wrapper must not weaken any protection newRemoteHTTPClient
// installs -- the private-IP dial guard and the redirect validator in
// particular.
func TestBearerHTTPClient_PreservesHardenedClientGuards(t *testing.T) {
	endpoint := mustParseURL(t, "https://api.example.com/mcp")
	client, err := newBearerHTTPClient(newRemoteHTTPClient(), endpoint, testBearerToken)
	if err != nil {
		t.Fatalf("newBearerHTTPClient error: %v", err)
	}

	t.Run("private ip dial still blocked", func(t *testing.T) {
		if _, err := client.Get("https://127.0.0.1:1/mcp"); err == nil {
			t.Fatal("expected loopback dial to be blocked")
		}
	})

	t.Run("redirect validator still installed", func(t *testing.T) {
		if client.CheckRedirect == nil {
			t.Fatal("CheckRedirect was dropped by the bearer wrapper")
		}
		req, err := http.NewRequest(http.MethodGet, "http://example.com/mcp", nil)
		if err != nil {
			t.Fatalf("NewRequest error: %v", err)
		}
		if err := client.CheckRedirect(req, nil); err == nil {
			t.Fatal("expected non-https redirect target to be rejected")
		}
	})

	t.Run("base client not mutated", func(t *testing.T) {
		base := newRemoteHTTPClient()
		baseTransport := base.Transport
		if _, err := newBearerHTTPClient(base, endpoint, testBearerToken); err != nil {
			t.Fatalf("newBearerHTTPClient error: %v", err)
		}
		if base.Transport != baseTransport {
			t.Fatal("newBearerHTTPClient mutated the base client's transport")
		}
	})
}

func TestNewBearerHTTPClient_RejectsMissingInputs(t *testing.T) {
	endpoint := mustParseURL(t, "https://api.example.com/mcp")
	cases := []struct {
		name     string
		base     *http.Client
		endpoint *url.URL
		token    string
	}{
		{"nil base", nil, endpoint, testBearerToken},
		{"nil endpoint", &http.Client{}, nil, testBearerToken},
		{"empty token", &http.Client{}, endpoint, "   "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := newBearerHTTPClient(tc.base, tc.endpoint, tc.token); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

// --- credential storage ------------------------------------------------------

func TestStaticBearerToken_SaveLoadDeleteRoundTrip(t *testing.T) {
	store := withFakeCredentialStore(t)
	ctx := context.Background()
	cfg := ServerConfig{
		Name:      "github",
		Transport: TransportStreamableHTTP,
		URL:       "https://api.githubcopilot.com/mcp/",
		AuthMode:  AuthModeStaticBearer,
	}

	if _, ok, err := LoadStaticBearerToken(ctx, cfg); err != nil || ok {
		t.Fatalf("expected no stored token initially, got ok=%v err=%v", ok, err)
	}

	if err := SaveStaticBearerToken(ctx, cfg, "  "+testBearerToken+"  "); err != nil {
		t.Fatalf("SaveStaticBearerToken error: %v", err)
	}

	got, ok, err := LoadStaticBearerToken(ctx, cfg)
	if err != nil || !ok {
		t.Fatalf("LoadStaticBearerToken ok=%v err=%v", ok, err)
	}
	if got != testBearerToken {
		t.Fatalf("token = %q, want %q (whitespace should be trimmed)", got, testBearerToken)
	}

	// The stored record must be a clean static-token shape, not something a
	// later reader could mistake for OAuth client material.
	cred := store.byRef[NormalizedAuthRef(cfg)]
	if cred.TokenType != StaticBearerTokenType {
		t.Fatalf("TokenType = %q, want %q", cred.TokenType, StaticBearerTokenType)
	}
	if cred.ClientID != "" || cred.ClientSecret != "" || cred.RefreshToken != "" || cred.TokenEndpoint != "" {
		t.Fatalf("static bearer credential carried OAuth client fields: %+v", cred)
	}

	if err := DeleteStaticBearerToken(ctx, cfg); err != nil {
		t.Fatalf("DeleteStaticBearerToken error: %v", err)
	}
	if _, ok, err := LoadStaticBearerToken(ctx, cfg); err != nil || ok {
		t.Fatalf("expected token gone after delete, got ok=%v err=%v", ok, err)
	}
	// Deleting again is not an error.
	if err := DeleteStaticBearerToken(ctx, cfg); err != nil {
		t.Fatalf("second DeleteStaticBearerToken error: %v", err)
	}
}

func TestSaveStaticBearerToken_ReplacesOAuthClientFields(t *testing.T) {
	store := withFakeCredentialStore(t)
	ctx := context.Background()
	cfg := ServerConfig{Name: "github", Transport: TransportStreamableHTTP, URL: "https://api.githubcopilot.com/mcp/", AuthMode: AuthModeStaticBearer}
	authRef := NormalizedAuthRef(cfg)
	store.byRef[authRef] = RemoteCredential{
		AuthRef:       authRef,
		ClientID:      "stale-client",
		ClientSecret:  "stale-secret",
		RefreshToken:  "stale-refresh",
		TokenEndpoint: "https://stale.example.com/token",
	}

	if err := SaveStaticBearerToken(ctx, cfg, testBearerToken); err != nil {
		t.Fatalf("SaveStaticBearerToken error: %v", err)
	}

	cred := store.byRef[authRef]
	if cred.ClientID != "" || cred.ClientSecret != "" || cred.RefreshToken != "" || cred.TokenEndpoint != "" {
		t.Fatalf("stale OAuth fields survived the static-bearer save: %+v", cred)
	}
}

func TestSaveStaticBearerToken_RejectsEmpty(t *testing.T) {
	withFakeCredentialStore(t)
	cfg := ServerConfig{Name: "github", Transport: TransportStreamableHTTP, URL: "https://api.githubcopilot.com/mcp/", AuthMode: AuthModeStaticBearer}
	if err := SaveStaticBearerToken(context.Background(), cfg, "   "); err == nil {
		t.Fatal("expected an error for an empty token")
	}
}

func TestBuildBearerClient_RequiresStoredToken(t *testing.T) {
	withFakeCredentialStore(t)
	cfg := ServerConfig{Name: "github", Transport: TransportStreamableHTTP, URL: "https://api.githubcopilot.com/mcp/", AuthMode: AuthModeStaticBearer}
	s := NewServer(cfg)

	_, err := s.buildBearerClient(context.Background(), newRemoteHTTPClient(), mustParseURL(t, cfg.URL))
	if err == nil || !isOAuthReconnectError(err) {
		t.Fatalf("expected ErrOAuthCredentialsRequired so the server reports auth-required, got: %v", err)
	}
	if strings.Contains(fmt.Sprint(err), testBearerToken) {
		t.Fatalf("token leaked into error: %v", err)
	}
}

// A static-bearer server's readiness must be judged by "is a token stored",
// not by the OAuth ClientID question -- asking the wrong one turned a working
// connection into a spurious credentials_required error in the HTTP layer.
func TestHasRemoteCredentials_UsesTheAuthModesOwnQuestion(t *testing.T) {
	store := withFakeCredentialStore(t)
	ctx := context.Background()

	bearerCfg := ServerConfig{Name: "github", Transport: TransportStreamableHTTP, URL: "https://api.githubcopilot.com/mcp/", AuthMode: AuthModeStaticBearer}
	oauthCfg := ServerConfig{Name: "gcal", Transport: TransportStreamableHTTP, URL: "https://example.com/mcp"}

	if ok, err := HasRemoteCredentials(ctx, bearerCfg); err != nil || ok {
		t.Fatalf("expected no bearer credentials yet, got ok=%v err=%v", ok, err)
	}

	if err := SaveStaticBearerToken(ctx, bearerCfg, testBearerToken); err != nil {
		t.Fatalf("SaveStaticBearerToken error: %v", err)
	}
	ok, err := HasRemoteCredentials(ctx, bearerCfg)
	if err != nil {
		t.Fatalf("HasRemoteCredentials error: %v", err)
	}
	if !ok {
		t.Fatal("a stored bearer token must count as configured credentials")
	}
	// The OAuth question would (wrongly) say "not configured" here, since a
	// static-bearer credential deliberately carries no ClientID.
	if oauthAnswer, _ := HasOAuthCredentials(ctx, bearerCfg); oauthAnswer {
		t.Fatal("a static-bearer credential must not look like OAuth client material")
	}

	// The OAuth path keeps its original semantics.
	authRef := NormalizedAuthRef(oauthCfg)
	store.byRef[authRef] = RemoteCredential{AuthRef: authRef, ClientID: "client-id", ClientSecret: "secret"}
	if ok, err := HasRemoteCredentials(ctx, oauthCfg); err != nil || !ok {
		t.Fatalf("expected oauth credentials to count as configured, got ok=%v err=%v", ok, err)
	}
}

// Binding GitHub to a workspace materializes a per-workspace copy under a
// different NAME. Without a pinned AuthRef each copy would derive its own
// credential key, find nothing, and report auth_required with zero tools --
// while Settings still showed a healthy connection.
func TestGitHubServerConfig_SharesOneCredentialAcrossWorkspaceCopies(t *testing.T) {
	global := GitHubServerConfig()
	if global.AuthRef == "" {
		t.Fatal("the GitHub server must pin an AuthRef so workspace copies share one token")
	}

	// What materializeRuntimeBinding does: clone the template, rename it.
	workspaceCopy := global
	workspaceCopy.Name = "ws:ws-1:mcp:github:binding-1"

	if NormalizedAuthRef(workspaceCopy) != NormalizedAuthRef(global) {
		t.Fatalf("a workspace copy resolved to %q, want the global %q",
			NormalizedAuthRef(workspaceCopy), NormalizedAuthRef(global))
	}

	// And a token stored once is readable through the renamed copy.
	withFakeCredentialStore(t)
	ctx := context.Background()
	if err := SaveStaticBearerToken(ctx, global, testBearerToken); err != nil {
		t.Fatalf("SaveStaticBearerToken error: %v", err)
	}
	got, ok, err := LoadStaticBearerToken(ctx, workspaceCopy)
	if err != nil || !ok {
		t.Fatalf("the workspace copy could not read the shared token: ok=%v err=%v", ok, err)
	}
	if got != testBearerToken {
		t.Fatalf("token = %q, want the shared one", got)
	}
}

// An install that already had a github entry before AuthRef was pinned keeps
// that entry forever, because adding defaults never updates what is already
// stored. The result is a connection that looks healthy in Settings while
// every workspace reports "connect this server with an access token first".
func TestInitializeDefaultServers_RepairsStoredGitHubAuthFields(t *testing.T) {
	baseDir := t.TempDir()
	cm := NewConfigManager(baseDir)

	// A github entry as it was stored before the fix: no auth ref, no auth
	// mode, and a user-set enabled flag that must survive.
	if err := cm.SaveGlobalConfig(&GlobalConfig{
		Servers: []ServerConfig{{
			Name:      GitHubServerName,
			Transport: TransportStreamableHTTP,
			URL:       GitHubServerURL,
			Enabled:   true,
		}},
	}); err != nil {
		t.Fatalf("SaveGlobalConfig: %v", err)
	}

	if err := cm.InitializeDefaultServers(); err != nil {
		t.Fatalf("InitializeDefaultServers: %v", err)
	}

	cfg, err := cm.LoadGlobalConfig()
	if err != nil {
		t.Fatalf("LoadGlobalConfig: %v", err)
	}
	github, ok := findServerByName(cfg.Servers, GitHubServerName)
	if !ok {
		t.Fatal("expected the github server to still be present")
	}
	if github.AuthRef != GitHubAuthRef {
		t.Fatalf("AuthRef = %q, want the pinned %q", github.AuthRef, GitHubAuthRef)
	}
	if NormalizedAuthMode(github) != AuthModeStaticBearer {
		t.Fatalf("AuthMode = %q, want static_bearer", github.AuthMode)
	}
	// What the user set is theirs.
	if !github.Enabled {
		t.Fatal("the repair must not reset the user's enabled flag")
	}

	// And a workspace copy of the repaired entry resolves to the same
	// credential, which is the whole point.
	workspaceCopy := github
	workspaceCopy.Name = "ws:ws-1:mcp:github:binding-1"
	if NormalizedAuthRef(workspaceCopy) != NormalizedAuthRef(github) {
		t.Fatalf("a workspace copy still resolves elsewhere: %q vs %q",
			NormalizedAuthRef(workspaceCopy), NormalizedAuthRef(github))
	}
}

// The repair must not overwrite an AuthRef someone set deliberately.
func TestRepairRemoteAuthDefaults_LeavesAnExplicitAuthRefAlone(t *testing.T) {
	servers := []ServerConfig{{
		Name:      GitHubServerName,
		Transport: TransportStreamableHTTP,
		URL:       GitHubServerURL,
		AuthRef:   "mcp:my-own-reference",
		AuthMode:  AuthModeStaticBearer,
	}}
	if repaired := repairRemoteAuthDefaults(servers); repaired {
		t.Fatal("nothing needed repairing")
	}
	if servers[0].AuthRef != "mcp:my-own-reference" {
		t.Fatalf("AuthRef was overwritten: %q", servers[0].AuthRef)
	}
}

// --- auth mode ---------------------------------------------------------------

func TestNormalizedAuthMode(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", AuthModeOAuth},
		{"  ", AuthModeOAuth},
		{"oauth", AuthModeOAuth},
		{"STATIC_BEARER", AuthModeStaticBearer},
		{" static_bearer ", AuthModeStaticBearer},
		{"nonsense", "nonsense"},
	}
	for _, tc := range cases {
		if got := NormalizedAuthMode(ServerConfig{AuthMode: tc.in}); got != tc.want {
			t.Fatalf("NormalizedAuthMode(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestValidateServerConfig_AuthMode(t *testing.T) {
	remote := func(mode string) ServerConfig {
		return ServerConfig{Name: "github", Transport: TransportStreamableHTTP, URL: "https://api.githubcopilot.com/mcp/", AuthMode: mode}
	}
	cases := []struct {
		name    string
		cfg     ServerConfig
		wantErr bool
	}{
		{"remote default mode ok", remote(""), false},
		{"remote oauth ok", remote(AuthModeOAuth), false},
		{"remote static bearer ok", remote(AuthModeStaticBearer), false},
		{"remote unknown mode rejected", remote("magic"), true},
		{"stdio with auth mode rejected", ServerConfig{Name: "fs", Command: "npx", AuthMode: AuthModeStaticBearer}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateServerConfig(tc.cfg)
			if tc.wantErr && err == nil {
				t.Fatal("expected an error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

// --- end-to-end: startRemote over a bearer-gated fake MCP server -------------

// fakeBearerMCPServer is a Streamable HTTP MCP server that accepts exactly one
// static bearer token and 401s everything else. It is the static-token analogue
// of fakeOAuthMCPServer, minus all the authorization-server machinery -- which
// is the point: this path must never touch OAuth discovery.
type fakeBearerMCPServer struct {
	server *httptest.Server

	mu           sync.Mutex
	sawAuth      []string
	oauthProbes  int
	acceptToken  string
	toolCallName string
}

func newFakeBearerMCPServer(t *testing.T, acceptToken string) *fakeBearerMCPServer {
	t.Helper()
	f := &fakeBearerMCPServer{acceptToken: acceptToken}

	mcpServer := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "fake-github", Version: "1.0.0"}, nil)
	sdkmcp.AddTool(mcpServer, &sdkmcp.Tool{Name: "list_issues", Description: "lists issues"},
		func(ctx context.Context, req *sdkmcp.CallToolRequest, args map[string]any) (*sdkmcp.CallToolResult, any, error) {
			f.mu.Lock()
			f.toolCallName = "list_issues"
			f.mu.Unlock()
			return &sdkmcp.CallToolResult{Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "no open issues"}}}, nil, nil
		})
	mcpHandler := sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server { return mcpServer }, nil)

	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		f.mu.Lock()
		f.sawAuth = append(f.sawAuth, auth)
		f.mu.Unlock()

		if auth != "Bearer "+f.acceptToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		mcpHandler.ServeHTTP(w, r)
	})
	// Any hit here means the static-bearer path fell through to OAuth
	// discovery, which it must never do.
	mux.HandleFunc("/.well-known/", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.oauthProbes++
		f.mu.Unlock()
		w.WriteHeader(http.StatusNotFound)
	})

	f.server = httptest.NewTLSServer(mux)
	return f
}

func (f *fakeBearerMCPServer) mcpURL() string { return f.server.URL + "/mcp" }
func (f *fakeBearerMCPServer) close()         { f.server.Close() }

func (f *fakeBearerMCPServer) snapshot() (auths []string, probes int, tool string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.sawAuth...), f.oauthProbes, f.toolCallName
}

func TestServer_StartRemote_StaticBearerConnectsAndListsTools(t *testing.T) {
	allowPrivateRemoteHostsForTests.Store(true)
	t.Cleanup(func() { allowPrivateRemoteHostsForTests.Store(false) })

	fake := newFakeBearerMCPServer(t, testBearerToken)
	t.Cleanup(fake.close)

	withFakeCredentialStore(t)

	cfg := ServerConfig{
		Name:      "github",
		Transport: TransportStreamableHTTP,
		URL:       fake.mcpURL(),
		AuthMode:  AuthModeStaticBearer,
	}
	if err := SaveStaticBearerToken(context.Background(), cfg, testBearerToken); err != nil {
		t.Fatalf("SaveStaticBearerToken error: %v", err)
	}

	s := NewServer(cfg)
	if err := s.Start(); err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	t.Cleanup(func() { _ = s.Stop() })

	if got := s.GetStatus(); got != StatusRunning {
		t.Fatalf("status = %s, want %s", got, StatusRunning)
	}

	auths, probes, _ := fake.snapshot()
	if len(auths) == 0 {
		t.Fatal("server saw no requests")
	}
	for i, a := range auths {
		if a != "Bearer "+testBearerToken {
			t.Fatalf("request %d Authorization = %q, want the bearer token", i, a)
		}
	}
	if probes != 0 {
		t.Fatalf("static-bearer path performed %d OAuth discovery request(s); it must perform none", probes)
	}
}

func TestServer_StartRemote_StaticBearerWithoutTokenReportsAuthRequired(t *testing.T) {
	allowPrivateRemoteHostsForTests.Store(true)
	t.Cleanup(func() { allowPrivateRemoteHostsForTests.Store(false) })

	fake := newFakeBearerMCPServer(t, testBearerToken)
	t.Cleanup(fake.close)

	withFakeCredentialStore(t)

	s := NewServer(ServerConfig{
		Name:      "github",
		Transport: TransportStreamableHTTP,
		URL:       fake.mcpURL(),
		AuthMode:  AuthModeStaticBearer,
	})

	err := s.Start()
	if err == nil {
		t.Fatal("expected Start() to fail without a stored token")
	}
	if got := s.GetStatus(); got != StatusAuthRequired {
		t.Fatalf("status = %s, want %s", got, StatusAuthRequired)
	}
	if _, probes, _ := fake.snapshot(); probes != 0 {
		t.Fatalf("expected no OAuth discovery probes, got %d", probes)
	}
}

// The OAuth path must be untouched by the auth-mode branch: a remote server
// with no auth mode declared still resolves through buildOAuthHandler and
// still fails the same way when its client credentials are missing.
func TestServer_StartRemote_OAuthPathUnaffectedByAuthModeBranch(t *testing.T) {
	withFakeCredentialStore(t)
	cfg := ServerConfig{Name: "gcal", Transport: TransportStreamableHTTP, URL: "https://example.com/mcp"}

	if got := NormalizedAuthMode(cfg); got != AuthModeOAuth {
		t.Fatalf("a config with no auth mode resolved to %q, want %q", got, AuthModeOAuth)
	}

	s := NewServer(cfg)
	_, err := s.buildOAuthHandler(context.Background(), newRemoteHTTPClient())
	if !errors.Is(err, ErrOAuthCredentialsRequired) {
		t.Fatalf("expected ErrOAuthCredentialsRequired from the untouched OAuth path, got: %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }
