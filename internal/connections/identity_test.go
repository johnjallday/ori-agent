package connections

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

type fakeVerifier struct {
	id  Identity
	err error
}

func (f fakeVerifier) Verify(_ context.Context, _, _ string) (Identity, error) {
	if f.err != nil {
		return Identity{}, f.err
	}
	return f.id, nil
}

func fakeTokenServer(t *testing.T, idToken string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := map[string]any{"access_token": "at-123", "token_type": "Bearer", "refresh_token": "rt-123", "expires_in": 3600}
		if idToken != "" {
			resp["id_token"] = idToken
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newTestFlow(t *testing.T, tokenURL string, v IDVerifier) (*IdentityFlow, *Store) {
	t.Helper()
	store := NewStore(t.TempDir())
	cfg := OAuthConfig{ClientID: "ori-desktop", AuthURL: googleAuthURL, TokenURL: tokenURL}
	return NewIdentityFlow(cfg, NewStateStore(time.Minute), store, v), store
}

const testRedirect = "http://localhost:8931/api/connections/google/callback"

func TestIdentityFlow_BeginConnect_AuthorizeURL(t *testing.T) {
	flow, _ := newTestFlow(t, "https://token.example", fakeVerifier{id: Identity{Subject: "sub-1"}})
	res, err := flow.BeginConnect(BeginConnectParams{LocalUserID: "u1", RedirectURL: testRedirect, ReturnTo: "/settings"})
	if err != nil {
		t.Fatalf("BeginConnect: %v", err)
	}
	u, err := url.Parse(res.AuthorizeURL)
	if err != nil {
		t.Fatalf("parse authorize url: %v", err)
	}
	q := u.Query()
	checks := map[string]string{
		"state":                 res.State,
		"prompt":                "select_account",
		"access_type":           "offline",
		"code_challenge_method": "S256",
		"client_id":             "ori-desktop",
		"redirect_uri":          testRedirect,
		"response_type":         "code",
	}
	for k, want := range checks {
		if got := q.Get(k); got != want {
			t.Errorf("authorize %s = %q, want %q", k, got, want)
		}
	}
	if q.Get("nonce") == "" || q.Get("code_challenge") == "" {
		t.Errorf("authorize url missing nonce/code_challenge: %s", res.AuthorizeURL)
	}
	if !strings.Contains(q.Get("scope"), "openid") {
		t.Errorf("scope missing openid: %q", q.Get("scope"))
	}
}

func TestIdentityFlow_NotConfigured(t *testing.T) {
	flow := NewIdentityFlow(OAuthConfig{}, NewStateStore(time.Minute), NewStore(t.TempDir()), fakeVerifier{})
	if _, err := flow.BeginConnect(BeginConnectParams{RedirectURL: testRedirect}); err != ErrOAuthNotConfigured {
		t.Fatalf("want ErrOAuthNotConfigured, got %v", err)
	}
}

func TestIdentityFlow_CompleteConnect_Success(t *testing.T) {
	srv := fakeTokenServer(t, "fake-id-token")
	flow, store := newTestFlow(t, srv.URL, fakeVerifier{id: Identity{Subject: "sub-1", Email: "jane@example.com", Name: "Jane", Picture: "pic"}})

	begin, err := flow.BeginConnect(BeginConnectParams{LocalUserID: "u1", RedirectURL: testRedirect})
	if err != nil {
		t.Fatalf("BeginConnect: %v", err)
	}
	conn, err := flow.CompleteConnect(context.Background(), CompleteConnectParams{State: begin.State, Code: "auth-code"})
	if err != nil {
		t.Fatalf("CompleteConnect: %v", err)
	}
	if conn.Subject != "sub-1" || conn.Email != "jane@example.com" || conn.ID == "" {
		t.Fatalf("connection = %+v", conn)
	}
	// Persisted.
	loaded, _ := store.Load()
	if loaded == nil || loaded.Subject != "sub-1" {
		t.Fatalf("not persisted: %+v", loaded)
	}
}

func TestIdentityFlow_ExpiredState(t *testing.T) {
	srv := fakeTokenServer(t, "fake-id-token")
	flow, _ := newTestFlow(t, srv.URL, fakeVerifier{id: Identity{Subject: "sub-1"}})
	if _, err := flow.CompleteConnect(context.Background(), CompleteConnectParams{State: "never-issued", Code: "x"}); err != ErrExpiredFlow {
		t.Fatalf("want ErrExpiredFlow, got %v", err)
	}
}

func TestIdentityFlow_AuthorizationDenied(t *testing.T) {
	srv := fakeTokenServer(t, "fake-id-token")
	flow, _ := newTestFlow(t, srv.URL, fakeVerifier{id: Identity{Subject: "sub-1"}})
	begin, _ := flow.BeginConnect(BeginConnectParams{RedirectURL: testRedirect})
	_, err := flow.CompleteConnect(context.Background(), CompleteConnectParams{State: begin.State, OAuthError: "access_denied"})
	if err == nil || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("want authorization-denied, got %v", err)
	}
}

func TestIdentityFlow_NoIDToken(t *testing.T) {
	srv := fakeTokenServer(t, "") // no id_token in response
	flow, _ := newTestFlow(t, srv.URL, fakeVerifier{id: Identity{Subject: "sub-1"}})
	begin, _ := flow.BeginConnect(BeginConnectParams{RedirectURL: testRedirect})
	if _, err := flow.CompleteConnect(context.Background(), CompleteConnectParams{State: begin.State, Code: "c"}); err != ErrNoIDToken {
		t.Fatalf("want ErrNoIDToken, got %v", err)
	}
}

func TestIdentityFlow_ReconnectSameSubjectPreservesGrants(t *testing.T) {
	srv := fakeTokenServer(t, "fake-id-token")
	flow, store := newTestFlow(t, srv.URL, fakeVerifier{id: Identity{Subject: "sub-1", Email: "new@example.com"}})
	// Pre-existing connection for the same subject with a Gmail grant + vault.
	_ = store.Save(&Connection{
		ID: "existing-id", Provider: ProviderGoogle, Subject: "sub-1", Email: "old@example.com", VaultID: "vault-9",
		Grants: map[ProductKey]*ProductGrant{ProductGmail: {ConnectionID: "existing-id", Product: ProductGmail, Health: HealthHealthy}},
	})

	begin, _ := flow.BeginConnect(BeginConnectParams{RedirectURL: testRedirect})
	conn, err := flow.CompleteConnect(context.Background(), CompleteConnectParams{State: begin.State, Code: "c"})
	if err != nil {
		t.Fatalf("reconnect: %v", err)
	}
	if conn.ID != "existing-id" || conn.VaultID != "vault-9" || conn.GrantHealthOf(ProductGmail) != HealthHealthy {
		t.Fatalf("reconnect should preserve id/vault/grants: %+v", conn)
	}
	if conn.Email != "new@example.com" {
		t.Fatalf("email should refresh to the latest: %q", conn.Email)
	}
}

func TestIdentityFlow_DifferentSubjectRejected(t *testing.T) {
	srv := fakeTokenServer(t, "fake-id-token")
	flow, store := newTestFlow(t, srv.URL, fakeVerifier{id: Identity{Subject: "sub-NEW"}})
	_ = store.Save(&Connection{ID: "e", Provider: ProviderGoogle, Subject: "sub-OLD"})

	begin, _ := flow.BeginConnect(BeginConnectParams{RedirectURL: testRedirect})
	if _, err := flow.CompleteConnect(context.Background(), CompleteConnectParams{State: begin.State, Code: "c"}); err != ErrDifferentAccountActive {
		t.Fatalf("want ErrDifferentAccountActive, got %v", err)
	}
}
