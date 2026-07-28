package connectionshttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/connections"
)

// HTTP surface for the credential-vault preflight: the blocked-enable payload
// (FR 5-11), the read-only preflight endpoint, and the callback result pages
// that must distinguish a Google failure from a local one (FR 13-16).

type stubVaultCatalog struct {
	vaults []connections.VaultRef
}

func (s *stubVaultCatalog) ListVaults(context.Context) ([]connections.VaultRef, error) {
	return s.vaults, nil
}

func (s *stubVaultCatalog) VaultAvailability(_ context.Context, vaultID string) (connections.VaultAvailability, error) {
	for _, v := range s.vaults {
		if v.ID == vaultID {
			return v.Availability, nil
		}
	}
	return connections.VaultMissing, nil
}

func vaultRef(id, name string, a connections.VaultAvailability) connections.VaultRef {
	return connections.VaultRef{ID: id, Name: name, Availability: a}
}

// newVaultTestServer wires the handler with a vault catalog and a connected
// identity, which is the state Gmail enablement runs in.
func newVaultTestServer(t *testing.T, savedVaultID string, vaults ...connections.VaultRef) (*http.ServeMux, *connections.Store) {
	t.Helper()
	tok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "at", "token_type": "Bearer", "refresh_token": "rt", "expires_in": 3600,
			"id_token": "fake", "scope": "openid email profile " + connections.GmailReadonlyScope,
		})
	}))
	t.Cleanup(tok.Close)

	catalog := &stubVaultCatalog{vaults: vaults}
	cfg := connections.OAuthConfig{ClientID: "ori-desktop", AuthURL: "https://accounts.google.com/o/oauth2/v2/auth", TokenURL: tok.URL}
	store := connections.NewStore(t.TempDir())
	flow := connections.NewIdentityFlow(cfg, connections.NewStateStore(time.Minute), store,
		fakeVerifier{id: connections.Identity{Subject: "sub-1", Email: "jane@example.com"}}).
		WithCredentialSink(fakeSink{}).
		WithVaultCatalog(catalog)

	if err := store.Save(&connections.Connection{
		ID: "c1", Provider: connections.ProviderGoogle, Subject: "sub-1",
		Email: "jane@example.com", VaultID: savedVaultID,
	}); err != nil {
		t.Fatalf("seed connection: %v", err)
	}

	h := NewHandler(Deps{
		Flow: flow, Store: store, Guard: NewOriginGuard(), Vaults: catalog,
		BuildRedirectURL: func(*http.Request) string { return "http://localhost/api/connections/google/callback" },
	})
	mux := http.NewServeMux()
	h.Register(mux)
	return mux, store
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return out
}

func TestGmailEnable_BlockedByVault(t *testing.T) {
	cases := []struct {
		name        string
		saved       string
		vaults      []connections.VaultRef
		wantAction  string
		wantOptions int
		wantVaultID string
	}{
		{
			name:       "no vault asks to create",
			wantAction: "create",
		},
		{
			name:        "several vaults ask to choose",
			vaults:      []connections.VaultRef{vaultRef("v-1", "Personal", connections.VaultAvailable), vaultRef("v-2", "Work", connections.VaultAvailable)},
			wantAction:  "choose",
			wantOptions: 2,
		},
		{
			name:        "locked vault asks to unlock",
			saved:       "v-1",
			vaults:      []connections.VaultRef{vaultRef("v-1", "Personal", connections.VaultLocked)},
			wantAction:  "unlock",
			wantVaultID: "v-1",
		},
		{
			name:        "vanished vault asks for repair",
			saved:       "v-gone",
			vaults:      []connections.VaultRef{vaultRef("v-1", "Personal", connections.VaultAvailable)},
			wantAction:  "repair",
			wantOptions: 1,
			wantVaultID: "v-gone",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mux, _ := newVaultTestServer(t, tc.saved, tc.vaults...)
			rec := do(mux, http.MethodPost, "http://localhost/api/connections/google/gmail/enable", "http://localhost")

			if rec.Code != http.StatusConflict {
				t.Fatalf("status = %d, want 409; body %s", rec.Code, rec.Body.String())
			}
			body := decode(t, rec)
			if body["error"] != "vault_action_required" {
				t.Fatalf("error = %v, want vault_action_required", body["error"])
			}
			if body["action"] != tc.wantAction {
				t.Fatalf("action = %v, want %q", body["action"], tc.wantAction)
			}
			if msg, _ := body["message"].(string); strings.TrimSpace(msg) == "" {
				t.Fatal("a blocked enable must explain the next step")
			}
			if _, ok := body["authorize_url"]; ok {
				t.Fatal("a blocked enable must not hand back an authorize URL")
			}
			options, _ := body["vaults"].([]any)
			if len(options) != tc.wantOptions {
				t.Fatalf("options = %d, want %d", len(options), tc.wantOptions)
			}
			if tc.wantVaultID != "" && body["vault_id"] != tc.wantVaultID {
				t.Fatalf("vault_id = %v, want %q", body["vault_id"], tc.wantVaultID)
			}
		})
	}
}

// The blocked payload must be enough to render the prompt and no more: no
// vault paths, passwords, or record counts.
func TestGmailEnable_BlockedPayloadIsMinimal(t *testing.T) {
	mux, _ := newVaultTestServer(t, "",
		vaultRef("v-1", "Personal", connections.VaultAvailable),
		vaultRef("v-2", "Work", connections.VaultLocked),
	)
	rec := do(mux, http.MethodPost, "http://localhost/api/connections/google/gmail/enable", "http://localhost")
	body := decode(t, rec)

	options, _ := body["vaults"].([]any)
	if len(options) != 2 {
		t.Fatalf("options = %d, want 2", len(options))
	}
	for _, raw := range options {
		opt, _ := raw.(map[string]any)
		for key := range opt {
			switch key {
			case "id", "name", "locked":
			default:
				t.Fatalf("vault option exposed %q; only id/name/locked may cross to the browser", key)
			}
		}
	}
	second, _ := options[1].(map[string]any)
	if second["locked"] != true {
		t.Fatal("a locked option must be marked so the client can route through unlock")
	}
}

// FR 6, 7: an explicit choice is accepted, recorded, and lets authorization run.
func TestGmailEnable_ExplicitVaultChoiceProceeds(t *testing.T) {
	mux, store := newVaultTestServer(t, "",
		vaultRef("v-1", "Personal", connections.VaultAvailable),
		vaultRef("v-2", "Work", connections.VaultAvailable),
	)
	rec := do(mux, http.MethodPost, "http://localhost/api/connections/google/gmail/enable?vault_id=v-2", "http://localhost")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	if url, _ := decode(t, rec)["authorize_url"].(string); !strings.Contains(url, "gmail.readonly") {
		t.Fatalf("authorize URL = %q, want the read-only Gmail scope", url)
	}
	conn, err := store.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if conn.VaultID != "v-2" {
		t.Fatalf("recorded vault = %q, want v-2", conn.VaultID)
	}
}

// The preflight endpoint is read-only: it reports the needed step and starts
// nothing, so re-opening a prompt never re-contacts Google.
func TestVaultPreflightEndpoint(t *testing.T) {
	mux, store := newVaultTestServer(t, "",
		vaultRef("v-1", "Personal", connections.VaultAvailable),
		vaultRef("v-2", "Work", connections.VaultAvailable),
	)
	rec := do(mux, http.MethodGet, "http://localhost/api/connections/google/vault", "http://localhost")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := decode(t, rec)
	if body["action"] != "choose" {
		t.Fatalf("action = %v, want choose", body["action"])
	}
	conn, _ := store.Load()
	if conn.VaultID != "" {
		t.Fatalf("preflight recorded a vault (%q); it must have no side effects", conn.VaultID)
	}
}

func TestVaultPreflightEndpoint_ReadyIsReported(t *testing.T) {
	mux, _ := newVaultTestServer(t, "v-1", vaultRef("v-1", "Personal", connections.VaultAvailable))
	rec := do(mux, http.MethodGet, "http://localhost/api/connections/google/vault", "http://localhost")
	body := decode(t, rec)
	if body["action"] != "ready" || body["vault_id"] != "v-1" {
		t.Fatalf("body = %v, want ready on v-1", body)
	}
}

// --- Callback result pages (FR 13-16) ---------------------------------------

// completeGmailCallback runs enable → callback with the vault switching to the
// given state while the user is "at Google".
func completeGmailCallback(t *testing.T, atCallback connections.VaultAvailability) *httptest.ResponseRecorder {
	t.Helper()
	catalog := &stubVaultCatalog{vaults: []connections.VaultRef{vaultRef("v-1", "Personal", connections.VaultAvailable)}}
	tok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "at", "token_type": "Bearer", "expires_in": 3600, "id_token": "fake",
			"scope": connections.GmailReadonlyScope,
		})
	}))
	t.Cleanup(tok.Close)

	cfg := connections.OAuthConfig{ClientID: "ori-desktop", TokenURL: tok.URL}
	store := connections.NewStore(t.TempDir())
	flow := connections.NewIdentityFlow(cfg, connections.NewStateStore(time.Minute), store,
		fakeVerifier{id: connections.Identity{Subject: "sub-1", Email: "jane@example.com"}}).
		WithCredentialSink(fakeSink{}).
		WithVaultCatalog(catalog)
	if err := store.Save(&connections.Connection{ID: "c1", Provider: connections.ProviderGoogle, Subject: "sub-1", VaultID: "v-1"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	h := NewHandler(Deps{
		Flow: flow, Store: store, Guard: NewOriginGuard(), Vaults: catalog,
		BuildRedirectURL: func(*http.Request) string { return "http://localhost/api/connections/google/callback" },
	})
	mux := http.NewServeMux()
	h.Register(mux)

	rec := do(mux, http.MethodPost, "http://localhost/api/connections/google/gmail/enable", "http://localhost")
	if rec.Code != http.StatusOK {
		t.Fatalf("enable status = %d: %s", rec.Code, rec.Body.String())
	}
	state := stateFromAuthorizeURL(t, decode(t, rec)["authorize_url"].(string))

	catalog.vaults = []connections.VaultRef{vaultRef("v-1", "Personal", atCallback)}
	return do(mux, http.MethodGet, "http://localhost/api/connections/google/callback?state="+state+"&code=abc123", "")
}

func stateFromAuthorizeURL(t *testing.T, raw string) string {
	t.Helper()
	_, after, ok := strings.Cut(raw, "state=")
	if !ok {
		t.Fatalf("no state in authorize URL %q", raw)
	}
	state, _, _ := strings.Cut(after, "&")
	return state
}

func TestCallbackPage_LockedVaultOffersUnlock(t *testing.T) {
	rec := completeGmailCallback(t, connections.VaultLocked)
	page := rec.Body.String()

	if !strings.Contains(page, "signed in with Google successfully") {
		t.Fatalf("page must credit the successful sign-in:\n%s", page)
	}
	if !strings.Contains(page, "Unlock the vault and try again") {
		t.Fatalf("page must offer the unlock action:\n%s", page)
	}
	if !strings.Contains(page, "gc_action=unlock") || !strings.Contains(page, "gc_vault=v-1") {
		t.Fatalf("return link must carry the repair hint:\n%s", page)
	}
	assertNoSecrets(t, page)
}

func TestCallbackPage_MissingVaultOffersChoose(t *testing.T) {
	rec := completeGmailCallback(t, connections.VaultMissing)
	page := rec.Body.String()

	if !strings.Contains(page, "Choose a vault and try again") {
		t.Fatalf("page must offer the choose action:\n%s", page)
	}
	if !strings.Contains(page, "gc_action=choose") {
		t.Fatalf("return link must carry the choose hint:\n%s", page)
	}
	assertNoSecrets(t, page)
}

// FR 18: a successful callback returns to the Google Account card.
func TestCallbackPage_SuccessReturnsToSettings(t *testing.T) {
	rec := completeGmailCallback(t, connections.VaultAvailable)
	page := rec.Body.String()

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, page)
	}
	if !strings.Contains(page, "/settings#google-account") {
		t.Fatalf("success page must return to the Google Account card:\n%s", page)
	}
	if !strings.Contains(page, "http-equiv=\"refresh\"") {
		t.Fatalf("success page must return automatically:\n%s", page)
	}
	assertNoSecrets(t, page)
}

// FR 15: a failure at Google reads as a Google failure, not a local one.
func TestCallbackPage_DeniedDoesNotClaimSignIn(t *testing.T) {
	mux, _ := newVaultTestServer(t, "v-1", vaultRef("v-1", "Personal", connections.VaultAvailable))
	rec := do(mux, http.MethodPost, "http://localhost/api/connections/google/gmail/enable", "http://localhost")
	state := stateFromAuthorizeURL(t, decode(t, rec)["authorize_url"].(string))

	page := do(mux, http.MethodGet,
		"http://localhost/api/connections/google/callback?state="+state+"&error=access_denied", "").Body.String()

	if !strings.Contains(page, "Sign-in canceled") {
		t.Fatalf("cancellation must be named as such:\n%s", page)
	}
	if strings.Contains(page, "signed in with Google successfully") {
		t.Fatalf("a denied sign-in must not claim success:\n%s", page)
	}
	if strings.Contains(page, "access_denied") {
		t.Fatalf("provider internals must not reach the page:\n%s", page)
	}
}

// FR 19: nothing token-bearing may appear in a browser response.
func assertNoSecrets(t *testing.T, page string) {
	t.Helper()
	for _, forbidden := range []string{"abc123", "vault://email/acct-test", "at", "rt", "id_token", "code="} {
		// "at"/"rt" are the fake tokens; match them as whole words to avoid
		// false positives on ordinary prose.
		needle := forbidden
		if len(forbidden) <= 2 {
			needle = ">" + forbidden + "<"
		}
		if strings.Contains(page, needle) {
			t.Fatalf("result page leaked %q:\n%s", forbidden, page)
		}
	}
}
