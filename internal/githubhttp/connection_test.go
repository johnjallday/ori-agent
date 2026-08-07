package githubhttp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/mcp"
)

const testToken = "github_pat_supersecrettoken"

// --- fake credential store ---------------------------------------------------

type fakeCredentialStore struct {
	mu    sync.Mutex
	byRef map[string]mcp.RemoteCredential
	// failLoad and failSave simulate a broken vault; lockedLoad simulates a
	// present-but-locked one.
	failLoad   bool
	failSave   bool
	lockedLoad bool
}

func (f *fakeCredentialStore) LoadCredential(_ context.Context, authRef string) (mcp.RemoteCredential, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.lockedLoad {
		return mcp.RemoteCredential{}, false, fmt.Errorf("%w: vault locked", mcp.ErrCredentialStoreLocked)
	}
	if f.failLoad {
		return mcp.RemoteCredential{}, false, errors.New("vault unavailable: token=" + testToken)
	}
	cred, ok := f.byRef[authRef]
	return cred, ok, nil
}

func (f *fakeCredentialStore) SaveCredential(_ context.Context, cred mcp.RemoteCredential) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failSave {
		return errors.New("vault unavailable: token=" + testToken)
	}
	f.byRef[cred.AuthRef] = cred
	return nil
}

func (f *fakeCredentialStore) DeleteCredential(_ context.Context, authRef string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.byRef, authRef)
	return nil
}

func withFakeStore(t *testing.T) *fakeCredentialStore {
	t.Helper()
	store := &fakeCredentialStore{byRef: make(map[string]mcp.RemoteCredential)}
	mcp.ConfigureRemoteOAuth(store, func(context.Context) (string, error) { return "local", nil })
	t.Cleanup(func() { mcp.ConfigureRemoteOAuth(nil, nil) })
	return store
}

// --- fake GitHub API ---------------------------------------------------------

type fakeGitHub struct {
	server *http.ServeMux
	// handler lets a test override /user's behavior.
	handler http.HandlerFunc

	mu       sync.Mutex
	sawAuth  []string
	requests int
}

func newFakeGitHub(t *testing.T, handler http.HandlerFunc) (*Connection, *fakeGitHub) {
	t.Helper()
	f := &fakeGitHub{handler: handler}

	mux := http.NewServeMux()
	mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.sawAuth = append(f.sawAuth, r.Header.Get("Authorization"))
		f.requests++
		f.mu.Unlock()
		f.handler(w, r)
	})
	f.server = mux

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	// Point the connection at the fake by rewriting the request host in a
	// custom transport, which keeps apiBaseURL a real constant in
	// production code rather than a test-injectable variable.
	client := &http.Client{Transport: rewriteHostTransport{target: srv.URL, next: http.DefaultTransport}}
	return NewConnection(client), f
}

type rewriteHostTransport struct {
	target string
	next   http.RoundTripper
}

func (t rewriteHostTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if strings.HasPrefix(req.URL.String(), apiBaseURL) {
		rewritten := req.Clone(req.Context())
		trimmed := strings.TrimPrefix(req.URL.String(), apiBaseURL)
		parsed, err := req.URL.Parse(t.target + trimmed)
		if err != nil {
			return nil, err
		}
		rewritten.URL = parsed
		rewritten.Host = parsed.Host
		return t.next.RoundTrip(rewritten)
	}
	return t.next.RoundTrip(req)
}

func (f *fakeGitHub) snapshot() ([]string, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.sawAuth...), f.requests
}

func okUser(login string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"login":"` + login + `"}`))
	}
}

func status(code int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(code) }
}

// --- tests -------------------------------------------------------------------

func TestConnect_StoresTokenOnlyAfterSuccessfulValidation(t *testing.T) {
	store := withFakeStore(t)
	conn, fake := newFakeGitHub(t, okUser("octocat"))

	identity, err := conn.Connect(context.Background(), "  "+testToken+"  ")
	if err != nil {
		t.Fatalf("Connect error: %v", err)
	}
	if identity.Login != "octocat" {
		t.Fatalf("login = %q, want octocat", identity.Login)
	}
	if identity.TokenType != "fine_grained" {
		t.Fatalf("token type = %q, want fine_grained", identity.TokenType)
	}

	authRef := mcp.NormalizedAuthRef(MCPServerConfig())
	stored := store.byRef[authRef]
	if stored.AccessToken != testToken {
		t.Fatalf("stored token = %q, want the trimmed token", stored.AccessToken)
	}
	if stored.TokenType != mcp.StaticBearerTokenType {
		t.Fatalf("stored TokenType = %q, want %q", stored.TokenType, mcp.StaticBearerTokenType)
	}

	auths, _ := fake.snapshot()
	if len(auths) != 1 || auths[0] != "Bearer "+testToken {
		t.Fatalf("unexpected Authorization headers: %v", auths)
	}
}

// A failed connect must not clobber a connection that already works.
func TestConnect_DoesNotStoreOrReplaceOnValidationFailure(t *testing.T) {
	store := withFakeStore(t)
	authRef := mcp.NormalizedAuthRef(MCPServerConfig())
	store.byRef[authRef] = mcp.RemoteCredential{
		AuthRef:     authRef,
		AccessToken: "previously-working-token",
		TokenType:   mcp.StaticBearerTokenType,
	}

	conn, _ := newFakeGitHub(t, status(http.StatusUnauthorized))

	if _, err := conn.Connect(context.Background(), testToken); err == nil {
		t.Fatal("expected Connect to fail on a rejected token")
	}

	if got := store.byRef[authRef].AccessToken; got != "previously-working-token" {
		t.Fatalf("a failed connect replaced the working token with %q", got)
	}
}

func TestConnect_RejectsEmptyToken(t *testing.T) {
	withFakeStore(t)
	conn, fake := newFakeGitHub(t, okUser("octocat"))

	_, err := conn.Connect(context.Background(), "   ")
	if err == nil {
		t.Fatal("expected an error for an empty token")
	}
	var connErr *ConnectionError
	if !errors.As(err, &connErr) || connErr.Category != ErrorCategoryInvalidToken {
		t.Fatalf("expected invalid_token category, got %v", err)
	}
	if _, requests := fake.snapshot(); requests != 0 {
		t.Fatalf("expected no GitHub call for an empty token, got %d", requests)
	}
}

func TestConnect_StorageFailureDoesNotLeakToken(t *testing.T) {
	store := withFakeStore(t)
	store.failSave = true
	conn, _ := newFakeGitHub(t, okUser("octocat"))

	_, err := conn.Connect(context.Background(), testToken)
	if err == nil {
		t.Fatal("expected Connect to fail when storage fails")
	}
	if strings.Contains(err.Error(), testToken) {
		t.Fatalf("token leaked through a storage error: %v", err)
	}
}

func TestTestConnection_NotConnected(t *testing.T) {
	withFakeStore(t)
	conn, fake := newFakeGitHub(t, okUser("octocat"))

	_, err := conn.TestConnection(context.Background())
	var connErr *ConnectionError
	if !errors.As(err, &connErr) || connErr.Category != ErrorCategoryNotConnected {
		t.Fatalf("expected not_connected, got %v", err)
	}
	if _, requests := fake.snapshot(); requests != 0 {
		t.Fatalf("expected no GitHub call when nothing is stored, got %d", requests)
	}
}

func TestTestConnection_ClassifiesFailures(t *testing.T) {
	future := strconv.FormatInt(time.Now().Add(10*time.Minute).Unix(), 10)

	cases := []struct {
		name    string
		handler http.HandlerFunc
		want    string
	}{
		{"revoked token", status(http.StatusUnauthorized), ErrorCategoryInvalidToken},
		{"forbidden without rate headers", status(http.StatusForbidden), ErrorCategoryInsufficientScope},
		{
			name: "exhausted primary rate limit",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-RateLimit-Remaining", "0")
				w.Header().Set("X-RateLimit-Reset", future)
				w.WriteHeader(http.StatusForbidden)
			},
			want: ErrorCategoryRateLimited,
		},
		{
			name: "secondary rate limit via Retry-After",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Retry-After", "60")
				w.WriteHeader(http.StatusForbidden)
			},
			want: ErrorCategoryRateLimited,
		},
		{"too many requests", status(http.StatusTooManyRequests), ErrorCategoryRateLimited},
		{"not found means invisible to this token", status(http.StatusNotFound), ErrorCategoryInsufficientScope},
		{"server error", status(http.StatusInternalServerError), ErrorCategoryUnavailable},
		{
			name: "unreadable success body",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"login":""}`))
			},
			want: ErrorCategoryUnavailable,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := withFakeStore(t)
			authRef := mcp.NormalizedAuthRef(MCPServerConfig())
			store.byRef[authRef] = mcp.RemoteCredential{
				AuthRef:     authRef,
				AccessToken: testToken,
				TokenType:   mcp.StaticBearerTokenType,
			}
			conn, _ := newFakeGitHub(t, tc.handler)

			_, err := conn.TestConnection(context.Background())
			var connErr *ConnectionError
			if !errors.As(err, &connErr) {
				t.Fatalf("expected a *ConnectionError, got %v", err)
			}
			if connErr.Category != tc.want {
				t.Fatalf("category = %q, want %q (message: %s)", connErr.Category, tc.want, connErr.Message)
			}
			if strings.Contains(connErr.Message, testToken) {
				t.Fatalf("token leaked into user-facing message: %s", connErr.Message)
			}
			if connErr.Message == "" {
				t.Fatal("expected plain-language repair copy, got an empty message")
			}
		})
	}
}

// A locked vault means "unlock this", not "connect something". Reporting it
// as not_connected would send the user off to generate a replacement token
// they do not need.
func TestTestConnection_LockedVaultIsItsOwnState(t *testing.T) {
	store := withFakeStore(t)
	store.lockedLoad = true
	conn, fake := newFakeGitHub(t, okUser("octocat"))

	_, err := conn.TestConnection(context.Background())
	var connErr *ConnectionError
	if !errors.As(err, &connErr) {
		t.Fatalf("expected a *ConnectionError, got %v", err)
	}
	if connErr.Category != ErrorCategoryVaultLocked {
		t.Fatalf("category = %q, want %q", connErr.Category, ErrorCategoryVaultLocked)
	}
	if _, requests := fake.snapshot(); requests != 0 {
		t.Fatalf("expected no GitHub call when the vault is locked, got %d", requests)
	}

	st := conn.Status(context.Background())
	if st.Connected || st.ErrorCategory != ErrorCategoryVaultLocked {
		t.Fatalf("status did not report the locked vault: %+v", st)
	}
}

// A broken vault must not surface its raw error, which may quote the token.
func TestTestConnection_StorageFailureDoesNotLeakToken(t *testing.T) {
	store := withFakeStore(t)
	store.failLoad = true
	conn, _ := newFakeGitHub(t, okUser("octocat"))

	_, err := conn.TestConnection(context.Background())
	if err == nil {
		t.Fatal("expected an error when the credential store fails")
	}
	if strings.Contains(err.Error(), testToken) {
		t.Fatalf("token leaked through a load error: %v", err)
	}
}

func TestStatus_ReflectsLiveTokenStateNotSavedState(t *testing.T) {
	store := withFakeStore(t)
	authRef := mcp.NormalizedAuthRef(MCPServerConfig())
	store.byRef[authRef] = mcp.RemoteCredential{
		AuthRef:     authRef,
		AccessToken: testToken,
		TokenType:   mcp.StaticBearerTokenType,
	}

	revoked := false
	conn, _ := newFakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		if revoked {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("X-OAuth-Scopes", "repo, read:org")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"login":"octocat"}`))
	})

	st := conn.Status(context.Background())
	if !st.Connected || st.Login != "octocat" {
		t.Fatalf("expected a connected status, got %+v", st)
	}
	if len(st.Scopes) != 2 || st.Scopes[0] != "repo" || st.Scopes[1] != "read:org" {
		t.Fatalf("scopes = %v, want [repo read:org]", st.Scopes)
	}

	// Revoking the token outside Ori must flip status immediately: nothing
	// about "connected" is cached, so it cannot outlive the credential.
	revoked = true
	st = conn.Status(context.Background())
	if st.Connected {
		t.Fatal("status still reported connected after the token was revoked server-side")
	}
	if st.ErrorCategory != ErrorCategoryInvalidToken {
		t.Fatalf("error category = %q, want %q", st.ErrorCategory, ErrorCategoryInvalidToken)
	}
	if st.Login != "" {
		t.Fatalf("a failed status leaked a login: %q", st.Login)
	}
}

func TestDisconnect_RemovesTokenAndIsIdempotent(t *testing.T) {
	store := withFakeStore(t)
	conn, _ := newFakeGitHub(t, okUser("octocat"))

	if _, err := conn.Connect(context.Background(), testToken); err != nil {
		t.Fatalf("Connect error: %v", err)
	}
	if connected, err := conn.IsConnected(context.Background()); err != nil || !connected {
		t.Fatalf("expected connected, got %v (err %v)", connected, err)
	}

	if err := conn.Disconnect(context.Background()); err != nil {
		t.Fatalf("Disconnect error: %v", err)
	}
	if len(store.byRef) != 0 {
		t.Fatalf("expected the credential removed, got %+v", store.byRef)
	}
	if connected, err := conn.IsConnected(context.Background()); err != nil || connected {
		t.Fatalf("expected disconnected, got %v (err %v)", connected, err)
	}
	// Disconnecting twice is not an error.
	if err := conn.Disconnect(context.Background()); err != nil {
		t.Fatalf("second Disconnect error: %v", err)
	}
}

func TestMCPServerConfig_IsValidStaticBearerRemote(t *testing.T) {
	cfg := MCPServerConfig()
	if err := mcp.ValidateServerConfig(cfg); err != nil {
		t.Fatalf("MCPServerConfig is not a valid server config: %v", err)
	}
	if mcp.NormalizedAuthMode(cfg) != mcp.AuthModeStaticBearer {
		t.Fatalf("auth mode = %q, want static_bearer", mcp.NormalizedAuthMode(cfg))
	}
	if !mcp.IsRemoteTransport(cfg) {
		t.Fatal("expected a remote transport")
	}
	if cfg.Enabled {
		t.Fatal("the GitHub server must ship disabled until a connection exists")
	}
	if cfg.Command != "" || len(cfg.Args) != 0 {
		t.Fatal("the hosted server must not declare a local command")
	}
}

func TestParseScopes(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"   ", nil},
		{",", nil},
		{"repo", []string{"repo"}},
		{"repo, read:org", []string{"repo", "read:org"}},
		{" repo ,, read:org ", []string{"repo", "read:org"}},
	}
	for _, tc := range cases {
		got := parseScopes(tc.in)
		if len(got) != len(tc.want) {
			t.Fatalf("parseScopes(%q) = %v, want %v", tc.in, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Fatalf("parseScopes(%q) = %v, want %v", tc.in, got, tc.want)
			}
		}
	}
}

func TestTokenKind(t *testing.T) {
	cases := map[string]string{
		"github_pat_abc": "fine_grained",
		"ghp_abc":        "classic",
		"something-else": "",
	}
	for token, want := range cases {
		if got := tokenKind(token); got != want {
			t.Fatalf("tokenKind(%q) = %q, want %q", token, got, want)
		}
	}
}
