package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/oauth2"
)

// --- OAuthSessions unit tests ------------------------------------------------

func TestOAuthSessions_DeliverConsumesExactlyOnce(t *testing.T) {
	sessions := newOAuthSessions(time.Minute)
	session := sessions.open("state-1", oauthSessionInfo{ServerName: "gcal"})

	info, ok := sessions.deliver("state-1", oauthCallbackResult{code: "abc"})
	if !ok {
		t.Fatal("expected first delivery to succeed")
	}
	if info.ServerName != "gcal" {
		t.Fatalf("unexpected session info: %+v", info)
	}
	select {
	case result := <-session.resultCh:
		if result.code != "abc" {
			t.Fatalf("unexpected code: %q", result.code)
		}
	default:
		t.Fatal("expected result to be delivered to resultCh")
	}

	// Replay: state was removed on first delivery.
	if _, ok := sessions.deliver("state-1", oauthCallbackResult{code: "replay"}); ok {
		t.Fatal("expected replayed delivery to fail")
	}
}

func TestOAuthSessions_UnknownStateNotDelivered(t *testing.T) {
	sessions := newOAuthSessions(time.Minute)
	if _, ok := sessions.deliver("never-opened", oauthCallbackResult{code: "x"}); ok {
		t.Fatal("expected delivery to unknown state to fail")
	}
}

func TestOAuthSessions_ExpiredSessionNotDelivered(t *testing.T) {
	now := time.Now()
	sessions := newOAuthSessions(time.Minute)
	sessions.now = func() time.Time { return now }
	sessions.open("state-1", oauthSessionInfo{ServerName: "gcal"})

	now = now.Add(2 * time.Minute)
	if _, ok := sessions.deliver("state-1", oauthCallbackResult{code: "late"}); ok {
		t.Fatal("expected delivery to an expired session to fail")
	}
}

func TestOAuthSessions_ExpireDropsUndelivered(t *testing.T) {
	sessions := newOAuthSessions(time.Minute)
	sessions.open("state-1", oauthSessionInfo{ServerName: "gcal"})
	sessions.expire("state-1")
	if _, ok := sessions.deliver("state-1", oauthCallbackResult{code: "late"}); ok {
		t.Fatal("expected expired-and-abandoned session not to be deliverable")
	}
}

func TestStateFromAuthorizeURL(t *testing.T) {
	if _, err := stateFromAuthorizeURL("https://auth.example.com/authorize?client_id=x"); err == nil {
		t.Fatal("expected error for missing state")
	}
	if _, err := stateFromAuthorizeURL("://not a url"); err == nil {
		t.Fatal("expected error for malformed url")
	}
	state, err := stateFromAuthorizeURL("https://auth.example.com/authorize?state=abc123&client_id=x")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state != "abc123" {
		t.Fatalf("unexpected state: %q", state)
	}
}

func TestDeliverOAuthCallback(t *testing.T) {
	t.Cleanup(func() { globalOAuthSessions = newOAuthSessions(defaultMCPOAuthTimeout + time.Minute) })
	globalOAuthSessions = newOAuthSessions(time.Minute)
	session := globalOAuthSessions.open("state-1", oauthSessionInfo{ServerName: "gcal"})

	name, ok := DeliverOAuthCallback("state-1", "code-abc", "", "")
	if !ok || name != "gcal" {
		t.Fatalf("expected successful delivery bound to gcal, got name=%q ok=%v", name, ok)
	}
	result := <-session.resultCh
	if result.err != nil || result.code != "code-abc" {
		t.Fatalf("unexpected result: %+v", result)
	}

	if _, ok := DeliverOAuthCallback("unknown-state", "code", "", ""); ok {
		t.Fatal("expected unknown state to fail delivery")
	}
}

func TestDeliverOAuthCallback_Denied(t *testing.T) {
	t.Cleanup(func() { globalOAuthSessions = newOAuthSessions(defaultMCPOAuthTimeout + time.Minute) })
	globalOAuthSessions = newOAuthSessions(time.Minute)
	session := globalOAuthSessions.open("state-1", oauthSessionInfo{ServerName: "gcal"})

	if _, ok := DeliverOAuthCallback("state-1", "", "access_denied", "user said no"); !ok {
		t.Fatal("expected denial to still be delivered")
	}
	result := <-session.resultCh
	if result.err == nil || !strings.Contains(result.err.Error(), "user said no") {
		t.Fatalf("expected denial error, got: %+v", result)
	}
}

// --- persistingTokenSource ---------------------------------------------------

type fakeTokenSource struct {
	mu     sync.Mutex
	tokens []*oauth2.Token
	i      int
	err    error
}

func (f *fakeTokenSource) Token() (*oauth2.Token, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	tok := f.tokens[f.i]
	if f.i < len(f.tokens)-1 {
		f.i++
	}
	return tok, nil
}

func TestPersistingTokenSource_PersistsOnlyOnChange(t *testing.T) {
	inner := &fakeTokenSource{tokens: []*oauth2.Token{
		{AccessToken: "tok-1"},
		{AccessToken: "tok-1"}, // unchanged; ReuseTokenSource-style callers may re-Token() the same value
		{AccessToken: "tok-2"}, // refreshed
	}}

	var saved []string
	var mu sync.Mutex
	ts := newPersistingTokenSource(inner, func(_ context.Context, tok *oauth2.Token) {
		mu.Lock()
		saved = append(saved, tok.AccessToken)
		mu.Unlock()
	})

	for range 3 {
		if _, err := ts.Token(); err != nil {
			t.Fatalf("Token() error: %v", err)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if len(saved) != 2 || saved[0] != "tok-1" || saved[1] != "tok-2" {
		t.Fatalf("expected persistence only on change, got: %v", saved)
	}
}

func TestPersistingTokenSource_PropagatesError(t *testing.T) {
	inner := &fakeTokenSource{err: fmt.Errorf("refresh failed: invalid_grant")}
	saveCalled := false
	ts := newPersistingTokenSource(inner, func(context.Context, *oauth2.Token) { saveCalled = true })

	if _, err := ts.Token(); err == nil {
		t.Fatal("expected error to propagate")
	}
	if saveCalled {
		t.Fatal("expected save not to be called on error")
	}
}

// --- fakeRemoteCredentialStore for buildOAuthHandler tests -------------------

type fakeRemoteCredentialStore struct {
	mu    sync.Mutex
	byRef map[string]RemoteCredential
}

func newFakeRemoteCredentialStore() *fakeRemoteCredentialStore {
	return &fakeRemoteCredentialStore{byRef: make(map[string]RemoteCredential)}
}

func (f *fakeRemoteCredentialStore) LoadCredential(_ context.Context, authRef string) (RemoteCredential, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cred, ok := f.byRef[authRef]
	return cred, ok, nil
}

func (f *fakeRemoteCredentialStore) SaveCredential(_ context.Context, cred RemoteCredential) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.byRef[cred.AuthRef] = cred
	return nil
}

func (f *fakeRemoteCredentialStore) DeleteCredential(_ context.Context, authRef string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.byRef, authRef)
	return nil
}

func withFakeCredentialStore(t *testing.T) *fakeRemoteCredentialStore {
	t.Helper()
	store := newFakeRemoteCredentialStore()
	prevStore := credentialStore()
	prevResolver := resolveOAuthUserID
	ConfigureRemoteOAuth(store, func(context.Context) (string, error) { return "local", nil })
	t.Cleanup(func() {
		ConfigureRemoteOAuth(prevStore, prevResolver)
	})
	return store
}

func TestBuildOAuthHandler_RequiresCredentials(t *testing.T) {
	withFakeCredentialStore(t)
	s := NewServer(ServerConfig{Name: "gcal", Transport: TransportStreamableHTTP, URL: "https://example.com/mcp"})

	_, err := s.buildOAuthHandler(context.Background(), newRemoteHTTPClient())
	if err == nil || !isOAuthReconnectError(err) {
		t.Fatalf("expected ErrOAuthCredentialsRequired, got: %v", err)
	}
}

func TestBuildOAuthHandler_LoadsStoredTokenWithoutInteractiveAuth(t *testing.T) {
	store := withFakeCredentialStore(t)
	cfg := ServerConfig{Name: "gcal", Transport: TransportStreamableHTTP, URL: "https://example.com/mcp"}
	authRef := NormalizedAuthRef(cfg)
	store.byRef[authRef] = RemoteCredential{
		AuthRef:       authRef,
		ClientID:      "client-id",
		ClientSecret:  "client-secret",
		AccessToken:   "stored-access-token",
		RefreshToken:  "stored-refresh-token",
		TokenEndpoint: "https://example.com/token",
		Expiry:        time.Now().Add(time.Hour),
	}

	s := NewServer(cfg)
	handler, err := s.buildOAuthHandler(context.Background(), newRemoteHTTPClient())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ts, err := handler.TokenSource(context.Background())
	if err != nil {
		t.Fatalf("TokenSource error: %v", err)
	}
	if ts == nil {
		t.Fatal("expected a ready token source from stored credentials, got nil (would force interactive auth)")
	}
}

// --- End-to-end: fake remote MCP + OAuth server ------------------------------

// fakeOAuthMCPServer is a minimal, in-process authorization server + bearer-
// gated Streamable HTTP MCP server, used to exercise Server.startRemote's
// full OAuth handshake (discovery, PKCE via the SDK, code exchange, token
// persistence, and silent refresh on reconnect) without any real network
// dependency. PKCE correctness itself is the SDK's concern (already covered
// by its own test suite); this fixture focuses on exercising Ori's glue code.
type fakeOAuthMCPServer struct {
	mu           sync.Mutex
	codes        map[string]struct{}
	accessTokens map[string]struct{}
	refreshUses  int

	server *httptest.Server
}

func newFakeOAuthMCPServer(t *testing.T) *fakeOAuthMCPServer {
	t.Helper()
	f := &fakeOAuthMCPServer{
		codes:        make(map[string]struct{}),
		accessTokens: make(map[string]struct{}),
	}

	mcpServer := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "fake-calendar", Version: "1.0.0"}, nil)
	sdkmcp.AddTool(mcpServer, &sdkmcp.Tool{Name: "echo", Description: "echoes input"}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args map[string]any) (*sdkmcp.CallToolResult, any, error) {
		return &sdkmcp.CallToolResult{Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "echo:ok"}}}, nil, nil
	})
	mcpHandler := sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server { return mcpServer }, nil)

	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		f.mu.Lock()
		_, ok := f.accessTokens[token]
		f.mu.Unlock()
		if token == "" || !ok {
			w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer resource_metadata="%s/.well-known/oauth-protected-resource/mcp"`, f.baseURL()))
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		mcpHandler.ServeHTTP(w, r)
	})
	mux.HandleFunc("/.well-known/oauth-protected-resource/mcp", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"resource":              f.baseURL() + "/mcp",
			"authorization_servers": []string{f.baseURL()},
		})
	})
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"issuer":                                f.baseURL(),
			"authorization_endpoint":                f.baseURL() + "/authorize",
			"token_endpoint":                        f.baseURL() + "/token",
			"response_types_supported":              []string{"code"},
			"token_endpoint_auth_methods_supported": []string{"client_secret_post"},
			"code_challenge_methods_supported":      []string{"S256"},
		})
	})
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		redirectURI := q.Get("redirect_uri")
		state := q.Get("state")
		if redirectURI == "" || state == "" || q.Get("code_challenge") == "" {
			http.Error(w, "missing required authorize params", http.StatusBadRequest)
			return
		}
		code := "code-" + state
		f.mu.Lock()
		f.codes[code] = struct{}{}
		f.mu.Unlock()

		redirect, _ := url.Parse(redirectURI)
		rq := redirect.Query()
		rq.Set("code", code)
		rq.Set("state", state)
		redirect.RawQuery = rq.Encode()
		http.Redirect(w, r, redirect.String(), http.StatusFound)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		grantType := r.FormValue("grant_type")

		switch grantType {
		case "authorization_code":
			code := r.FormValue("code")
			f.mu.Lock()
			_, ok := f.codes[code]
			delete(f.codes, code)
			f.mu.Unlock()
			if !ok {
				http.Error(w, "invalid_grant", http.StatusBadRequest)
				return
			}
			f.issueToken(w)
		case "refresh_token":
			if r.FormValue("refresh_token") != "test-refresh-token" {
				http.Error(w, "invalid_grant", http.StatusBadRequest)
				return
			}
			f.mu.Lock()
			f.refreshUses++
			f.mu.Unlock()
			f.issueToken(w)
		default:
			http.Error(w, "unsupported_grant_type", http.StatusBadRequest)
		}
	})

	f.server = httptest.NewTLSServer(mux)
	return f
}

func (f *fakeOAuthMCPServer) issueToken(w http.ResponseWriter) {
	access := "test-access-token"
	f.mu.Lock()
	f.accessTokens[access] = struct{}{}
	f.mu.Unlock()

	writeJSON(w, map[string]any{
		"access_token":  access,
		"refresh_token": "test-refresh-token",
		"token_type":    "Bearer",
		"expires_in":    3600,
	})
}

func (f *fakeOAuthMCPServer) baseURL() string { return f.server.URL }
func (f *fakeOAuthMCPServer) mcpURL() string  { return f.server.URL + "/mcp" }
func (f *fakeOAuthMCPServer) close()          { f.server.Close() }

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// simulateBrowserAuthorize acts as "the user's browser": it GETs the
// authorize URL (auto-approved by the fake authorization server), captures
// the redirect's code+state, and delivers it exactly the way
// OAuthCallbackHandler would.
func simulateBrowserAuthorize(t *testing.T, client *http.Client, authorizeURL string) {
	t.Helper()
	noRedirectClient := &http.Client{
		Transport: client.Transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := noRedirectClient.Get(authorizeURL)
	if err != nil {
		t.Fatalf("simulated browser GET authorize url failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("expected 302 from fake authorize endpoint, got %d", resp.StatusCode)
	}
	location, err := resp.Location()
	if err != nil {
		t.Fatalf("missing Location header: %v", err)
	}
	code := location.Query().Get("code")
	state := location.Query().Get("state")
	if code == "" || state == "" {
		t.Fatalf("redirect missing code/state: %s", location)
	}
	if _, ok := DeliverOAuthCallback(state, code, "", ""); !ok {
		t.Fatal("DeliverOAuthCallback rejected the simulated browser callback")
	}
}

func TestServer_StartRemote_FullOAuthHandshakeAndReconnect(t *testing.T) {
	allowPrivateRemoteHostsForTests.Store(true)
	t.Cleanup(func() { allowPrivateRemoteHostsForTests.Store(false) })

	fake := newFakeOAuthMCPServer(t)
	t.Cleanup(fake.close)

	store := withFakeCredentialStore(t)

	cfg := ServerConfig{Name: "fake-calendar", Transport: TransportStreamableHTTP, URL: fake.mcpURL()}
	if err := SaveOAuthClientCredentials(context.Background(), cfg, "test-client-id", "test-client-secret"); err != nil {
		t.Fatalf("SaveOAuthClientCredentials error: %v", err)
	}

	s := NewServer(cfg)

	startErrCh := make(chan error, 1)
	go func() { startErrCh <- s.Start() }()

	// Wait for the fetcher to publish the authorize URL.
	deadline := time.Now().Add(10 * time.Second)
	for s.GetStatus() != StatusAuthRequired {
		select {
		case err := <-startErrCh:
			t.Fatalf("Start() returned before reaching StatusAuthRequired: %v (status=%s)", err, s.GetStatus())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for StatusAuthRequired, status=%s", s.GetStatus())
		}
		time.Sleep(10 * time.Millisecond)
	}
	authorizeURL := s.GetAuthorizeURL()
	if authorizeURL == "" {
		t.Fatal("expected a non-empty authorize URL")
	}
	t.Logf("authorize URL published: %s", authorizeURL)

	simulateBrowserAuthorize(t, fake.server.Client(), authorizeURL)
	t.Logf("simulated browser authorize done")

	select {
	case err := <-startErrCh:
		if err != nil {
			t.Fatalf("Start() returned error after simulated authorization: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for Start() to complete")
	}
	t.Logf("Start() completed")

	if got := s.GetStatus(); got != StatusRunning {
		t.Fatalf("expected StatusRunning after authorization, got %s", got)
	}

	tools := s.GetTools()
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("expected the fake server's echo tool to be discovered, got: %+v", tools)
	}

	result, err := s.CallTool(context.Background(), "echo", map[string]any{"text": "hi"})
	if err != nil {
		t.Fatalf("CallTool error: %v", err)
	}
	if len(result.Content) == 0 {
		t.Fatal("expected non-empty tool result")
	}

	// A credential should now be persisted, including a discovered token
	// endpoint -- required for the reconnect path below to refresh silently.
	authRef := NormalizedAuthRef(cfg)
	persisted, ok, err := store.LoadCredential(context.Background(), authRef)
	if err != nil || !ok {
		t.Fatalf("expected persisted credential, ok=%v err=%v", ok, err)
	}
	if persisted.RefreshToken == "" || persisted.TokenEndpoint == "" {
		t.Fatalf("expected refresh token and discovered token endpoint to be persisted, got: %+v", persisted)
	}

	// --- Reconnect: simulate a process restart with an already-expired
	// stored access token. oauth2 checks Expiry client-side before ever
	// making a request, so this forces a refresh_token grant pre-flight --
	// zero browser interaction and (unlike a 401 mid-request, which the
	// OAuthHandler contract maps to a fresh interactive Authorize() rather
	// than a silent refresh) no risk of tripping the interactive path.
	if err := s.Stop(); err != nil {
		t.Fatalf("Stop() error: %v", err)
	}
	persisted.Expiry = time.Now().Add(-time.Hour)
	if err := store.SaveCredential(context.Background(), persisted); err != nil {
		t.Fatalf("failed to age the stored credential: %v", err)
	}

	s2 := NewServer(cfg) // fresh Server instance, as a restart would create
	if err := s2.Start(); err != nil {
		t.Fatalf("reconnect Start() should succeed via silent refresh, got error: %v", err)
	}
	if got := s2.GetStatus(); got != StatusRunning {
		t.Fatalf("expected StatusRunning after silent refresh reconnect, got %s", got)
	}
	if got := s2.GetAuthorizeURL(); got != "" {
		t.Fatalf("reconnect must not require interactive authorization, got authorize url: %q", got)
	}

	fake.mu.Lock()
	refreshUses := fake.refreshUses
	fake.mu.Unlock()
	if refreshUses < 1 {
		t.Fatal("expected the refresh_token grant to be used on reconnect")
	}

	_ = s2.Stop()
}
