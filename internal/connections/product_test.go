package connections

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
)

type fakeSink struct {
	ref string
	err error
	got GmailCredential
}

func (s *fakeSink) SaveGmailCredential(_ context.Context, cred GmailCredential) (string, error) {
	s.got = cred
	if s.err != nil {
		return "", s.err
	}
	if s.ref == "" {
		return "vault://email/acct-1", nil
	}
	return s.ref, nil
}

// gmailTokenServer returns a fake Google token endpoint that echoes a granted
// scope set (space-separated) alongside the tokens.
func gmailTokenServer(t *testing.T, scope string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := map[string]any{"access_token": "at", "token_type": "Bearer", "refresh_token": "rt", "expires_in": 3600, "id_token": "fake"}
		if scope != "" {
			resp["scope"] = scope
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func seedIdentity(t *testing.T, store *Store, subject, email string) {
	t.Helper()
	if err := store.Save(&Connection{ID: "c1", Provider: ProviderGoogle, Subject: subject, Email: email, VaultID: "v1"}); err != nil {
		t.Fatalf("seed identity: %v", err)
	}
}

func TestEnableGmail_Success(t *testing.T) {
	srv := gmailTokenServer(t, "openid email profile "+GmailReadonlyScope)
	flow, store := newTestFlow(t, srv.URL, fakeVerifier{id: Identity{Subject: "sub-1", Email: "jane@x.com", Name: "Jane"}})
	sink := &fakeSink{ref: "vault://email/acct-9"}
	flow.WithCredentialSink(sink)
	seedIdentity(t, store, "sub-1", "jane@x.com")

	begin, err := flow.BeginEnableGmail(BeginConnectParams{RedirectURL: testRedirect})
	if err != nil {
		t.Fatalf("BeginEnableGmail: %v", err)
	}
	conn, err := flow.CompleteEnableGmail(context.Background(), CompleteConnectParams{State: begin.State, Code: "c"})
	if err != nil {
		t.Fatalf("CompleteEnableGmail: %v", err)
	}
	g, ok := conn.Grant(ProductGmail)
	if !ok || g.Health != HealthHealthy || g.CredentialRef != "vault://email/acct-9" || g.Transport != TransportNative {
		t.Fatalf("gmail grant = %+v", g)
	}
	if !slices.Contains(g.GrantedScopes, GmailReadonlyScope) {
		t.Fatalf("granted scopes missing gmail.readonly: %v", g.GrantedScopes)
	}
	// The sink received the refresh token + vault id, never the browser.
	if sink.got.RefreshToken != "rt" || sink.got.VaultID != "v1" || sink.got.Email != "jane@x.com" {
		t.Fatalf("sink credential = %+v", sink.got)
	}
}

func TestEnableGmail_ScopeDeselectedRequiresUpgrade(t *testing.T) {
	srv := gmailTokenServer(t, "openid email profile") // user unchecked mail access
	flow, store := newTestFlow(t, srv.URL, fakeVerifier{id: Identity{Subject: "sub-1"}})
	flow.WithCredentialSink(&fakeSink{})
	seedIdentity(t, store, "sub-1", "")

	begin, _ := flow.BeginEnableGmail(BeginConnectParams{RedirectURL: testRedirect})
	conn, err := flow.CompleteEnableGmail(context.Background(), CompleteConnectParams{State: begin.State, Code: "c"})
	if err != nil {
		t.Fatalf("CompleteEnableGmail: %v", err)
	}
	if g, _ := conn.Grant(ProductGmail); g.Health != HealthScopeUpgradeRequired {
		t.Fatalf("want scope_upgrade_required, got %v", g.Health)
	}
}

func TestEnableGmail_NoIdentity(t *testing.T) {
	srv := gmailTokenServer(t, "")
	flow, _ := newTestFlow(t, srv.URL, fakeVerifier{id: Identity{Subject: "sub-1"}})
	flow.WithCredentialSink(&fakeSink{})
	if _, err := flow.BeginEnableGmail(BeginConnectParams{RedirectURL: testRedirect}); err != ErrNoActiveIdentity {
		t.Fatalf("want ErrNoActiveIdentity, got %v", err)
	}
}

func TestEnableGmail_NoSink(t *testing.T) {
	srv := gmailTokenServer(t, "")
	flow, store := newTestFlow(t, srv.URL, fakeVerifier{id: Identity{Subject: "sub-1"}})
	seedIdentity(t, store, "sub-1", "")
	if _, err := flow.BeginEnableGmail(BeginConnectParams{RedirectURL: testRedirect}); err != ErrNoCredentialSink {
		t.Fatalf("want ErrNoCredentialSink, got %v", err)
	}
}

func TestEnableGmail_DifferentSubjectRejected(t *testing.T) {
	srv := gmailTokenServer(t, "openid email profile "+GmailReadonlyScope)
	flow, store := newTestFlow(t, srv.URL, fakeVerifier{id: Identity{Subject: "sub-OTHER"}})
	flow.WithCredentialSink(&fakeSink{})
	seedIdentity(t, store, "sub-1", "")

	begin, _ := flow.BeginEnableGmail(BeginConnectParams{RedirectURL: testRedirect})
	if _, err := flow.CompleteEnableGmail(context.Background(), CompleteConnectParams{State: begin.State, Code: "c"}); err != ErrDifferentAccountActive {
		t.Fatalf("want ErrDifferentAccountActive, got %v", err)
	}
}

func TestBeginEnableGmail_AuthorizeURL(t *testing.T) {
	flow, store := newTestFlow(t, "https://token.example", fakeVerifier{id: Identity{Subject: "sub-1"}})
	flow.WithCredentialSink(&fakeSink{})
	seedIdentity(t, store, "sub-1", "jane@x.com")

	res, err := flow.BeginEnableGmail(BeginConnectParams{RedirectURL: testRedirect})
	if err != nil {
		t.Fatalf("BeginEnableGmail: %v", err)
	}
	u, _ := url.Parse(res.AuthorizeURL)
	scope := u.Query().Get("scope")
	if !strings.Contains(scope, GmailReadonlyScope) || !strings.Contains(scope, "openid") {
		t.Fatalf("authorize scope missing gmail.readonly/openid: %q", scope)
	}
	if u.Query().Get("login_hint") != "jane@x.com" {
		t.Fatalf("login_hint = %q, want jane@x.com", u.Query().Get("login_hint"))
	}
}

func TestBeginEnableGmailSend_AuthorizeURL(t *testing.T) {
	flow, store := newTestFlow(t, "https://token.example", fakeVerifier{id: Identity{Subject: "sub-1"}})
	flow.WithCredentialSink(&fakeSink{})
	seedIdentity(t, store, "sub-1", "jane@x.com")

	res, err := flow.BeginEnableGmailSend(BeginConnectParams{RedirectURL: testRedirect})
	if err != nil {
		t.Fatalf("BeginEnableGmailSend: %v", err)
	}
	u, _ := url.Parse(res.AuthorizeURL)
	scope := u.Query().Get("scope")
	if !strings.Contains(scope, GmailSendScope) || !strings.Contains(scope, GmailReadonlyScope) {
		t.Fatalf("send-upgrade scope should include send + readonly: %q", scope)
	}
}

func TestEnableGmail_ReAuthUpdatesInPlace(t *testing.T) {
	srv := gmailTokenServer(t, "openid email profile "+GmailReadonlyScope+" "+GmailSendScope)
	sink := &fakeSink{ref: "vault://email/global"}
	flow, store := newTestFlow(t, srv.URL, fakeVerifier{id: Identity{Subject: "sub-1"}})
	flow.WithCredentialSink(sink)
	// Gmail already enabled (grant has a ref): the send upgrade must UPDATE it, not duplicate.
	_ = store.Save(&Connection{
		ID: "c1", Provider: ProviderGoogle, Subject: "sub-1", VaultID: "v1",
		Grants: map[ProductKey]*ProductGrant{
			ProductGmail: {ConnectionID: "c1", Product: ProductGmail, Health: HealthHealthy, CredentialRef: "vault://email/global"},
		},
	})

	begin, _ := flow.BeginEnableGmailSend(BeginConnectParams{RedirectURL: testRedirect})
	conn, err := flow.CompleteEnableGmail(context.Background(), CompleteConnectParams{State: begin.State, Code: "c"})
	if err != nil {
		t.Fatalf("CompleteEnableGmail: %v", err)
	}
	if sink.got.ExistingRef != "vault://email/global" {
		t.Fatalf("re-auth should pass ExistingRef for in-place update, got %q", sink.got.ExistingRef)
	}
	g, _ := conn.Grant(ProductGmail)
	if !slices.Contains(g.GrantedScopes, GmailSendScope) || g.CredentialRef != "vault://email/global" {
		t.Fatalf("grant should include send scope and keep its ref: %+v", g)
	}
}
