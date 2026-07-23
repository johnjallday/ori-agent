package connectionshttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/connections"
)

type fakeVerifier struct{ id connections.Identity }

func (f fakeVerifier) Verify(_ context.Context, _, _ string) (connections.Identity, error) {
	return f.id, nil
}

type fakeSink struct{}

func (fakeSink) SaveGmailCredential(_ context.Context, _ connections.GmailCredential) (string, error) {
	return "vault://email/acct-test", nil
}

func newTestServer(t *testing.T) (*http.ServeMux, *connections.Store) {
	t.Helper()
	// Fake Google token endpoint (returns a granted scope set incl. gmail.readonly).
	tok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "at", "token_type": "Bearer", "refresh_token": "rt", "expires_in": 3600, "id_token": "fake",
			"scope": "openid email profile " + connections.GmailReadonlyScope,
		})
	}))
	t.Cleanup(tok.Close)

	cfg := connections.OAuthConfig{ClientID: "ori-desktop", AuthURL: "https://accounts.google.com/o/oauth2/v2/auth", TokenURL: tok.URL}
	store := connections.NewStore(t.TempDir())
	flow := connections.NewIdentityFlow(cfg, connections.NewStateStore(time.Minute), store,
		fakeVerifier{id: connections.Identity{Subject: "sub-1", Email: "jane@example.com", Name: "Jane"}}).
		WithCredentialSink(fakeSink{})

	h := NewHandler(Deps{
		Flow: flow, Store: store, Guard: NewOriginGuard(),
		BuildRedirectURL: func(*http.Request) string { return "http://localhost/api/connections/google/callback" },
	})
	mux := http.NewServeMux()
	h.Register(mux)
	return mux, store
}

func do(mux *http.ServeMux, method, target, origin string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, nil)
	r.Host = "localhost"
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, r)
	return rec
}

func TestHandler_ConnectCallbackStatus(t *testing.T) {
	mux, _ := newTestServer(t)

	// 1. Connect -> authorize URL.
	rec := do(mux, http.MethodPost, "http://localhost/api/connections/google/connect", "http://localhost")
	if rec.Code != http.StatusOK {
		t.Fatalf("connect = %d, body %s", rec.Code, rec.Body.String())
	}
	var connectResp struct {
		AuthorizeURL string `json:"authorize_url"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &connectResp); err != nil {
		t.Fatalf("decode connect: %v", err)
	}
	u, _ := url.Parse(connectResp.AuthorizeURL)
	state := u.Query().Get("state")
	if state == "" {
		t.Fatal("no state in authorize url")
	}

	// 2. Callback completes the flow.
	rec = do(mux, http.MethodGet, "http://localhost/api/connections/google/callback?state="+state+"&code=abc", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "jane@example.com") {
		t.Fatalf("callback = %d, body %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "fake") || strings.Contains(rec.Body.String(), "abc") {
		t.Fatal("callback page must not echo the id_token or auth code")
	}

	// 3. Status reflects the connected account.
	rec = do(mux, http.MethodGet, "http://localhost/api/connections/google/status", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var pub connections.PublicConnection
	if err := json.Unmarshal(rec.Body.Bytes(), &pub); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if pub.State != connections.StateConnected || pub.Email != "jane@example.com" || len(pub.Grants) != 3 {
		t.Fatalf("status projection = %+v", pub)
	}
}

func TestHandler_CallbackBadStateRendersError(t *testing.T) {
	mux, _ := newTestServer(t)
	rec := do(mux, http.MethodGet, "http://localhost/api/connections/google/callback?state=nope&code=x", "")
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "expired") {
		t.Fatalf("bad-state callback = %d, body %s", rec.Code, rec.Body.String())
	}
}

func TestHandler_OriginGuardBlocksCrossOrigin(t *testing.T) {
	mux, _ := newTestServer(t)
	// Cross-origin POST to connect is rejected.
	if rec := do(mux, http.MethodPost, "http://localhost/api/connections/google/connect", "http://evil.example"); rec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin connect = %d, want 403", rec.Code)
	}
	// Cross-origin read of status is rejected (identity metadata must not leak).
	if rec := do(mux, http.MethodGet, "http://localhost/api/connections/google/status", "http://evil.example"); rec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin status = %d, want 403", rec.Code)
	}
}

func TestHandler_Disconnect(t *testing.T) {
	mux, store := newTestServer(t)
	_ = store.Save(&connections.Connection{ID: "c", Provider: connections.ProviderGoogle, Subject: "sub-1", Email: "j@x.com"})
	rec := do(mux, http.MethodPost, "http://localhost/api/connections/google/disconnect", "http://localhost")
	if rec.Code != http.StatusOK {
		t.Fatalf("disconnect = %d", rec.Code)
	}
	if got, _ := store.Load(); got != nil {
		t.Fatal("disconnect should clear the stored connection")
	}
}

func TestHandler_GmailEnable_RequiresIdentity(t *testing.T) {
	mux, _ := newTestServer(t)
	rec := do(mux, http.MethodPost, "http://localhost/api/connections/google/gmail/enable", "http://localhost")
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "no_identity") {
		t.Fatalf("gmail enable without identity = %d, body %s", rec.Code, rec.Body.String())
	}
}

func TestHandler_GmailEnable_EndToEnd(t *testing.T) {
	mux, store := newTestServer(t)
	_ = store.Save(&connections.Connection{ID: "c", Provider: connections.ProviderGoogle, Subject: "sub-1", Email: "jane@example.com", VaultID: "v1"})

	// Begin enable -> authorize URL carrying gmail.readonly.
	rec := do(mux, http.MethodPost, "http://localhost/api/connections/google/gmail/enable", "http://localhost")
	if rec.Code != http.StatusOK {
		t.Fatalf("gmail enable = %d, body %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		AuthorizeURL string `json:"authorize_url"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	u, _ := url.Parse(resp.AuthorizeURL)
	if !strings.Contains(u.Query().Get("scope"), connections.GmailReadonlyScope) {
		t.Fatalf("authorize scope missing gmail.readonly: %q", u.Query().Get("scope"))
	}
	state := u.Query().Get("state")

	// The shared callback route dispatches to the Gmail-enable completion.
	rec = do(mux, http.MethodGet, "http://localhost/api/connections/google/callback?state="+state+"&code=abc", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("gmail callback = %d, body %s", rec.Code, rec.Body.String())
	}

	// Status now shows Gmail Healthy + enabled.
	rec = do(mux, http.MethodGet, "http://localhost/api/connections/google/status", "")
	var pub connections.PublicConnection
	_ = json.Unmarshal(rec.Body.Bytes(), &pub)
	var gmail connections.PublicGrant
	for _, g := range pub.Grants {
		if g.Product == connections.ProductGmail {
			gmail = g
		}
	}
	if gmail.Health != connections.HealthHealthy || !gmail.Enabled {
		t.Fatalf("gmail grant after enable = %+v", gmail)
	}
}
