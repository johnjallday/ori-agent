package connections

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// Gmail enablement must resolve — and record — its destination vault before the
// browser ever leaves for Google. These tests cover the full vault matrix at
// begin time (FR 1, 3-9), the resume/cancel contract (FR 10, 11), and the
// callback-time re-verification that used to surface as a generic "we couldn't
// complete the Google sign-in" (FR 12-16).

func seedFlowWithVaults(t *testing.T, savedVaultID string, vaults ...VaultRef) (*IdentityFlow, *Store, *fakeVaultCatalog) {
	t.Helper()
	flow, store := newTestFlow(t, "https://token.example", fakeVerifier{id: Identity{Subject: "sub-1"}})
	flow.WithCredentialSink(&fakeSink{})
	catalog := &fakeVaultCatalog{vaults: vaults}
	flow.WithVaultCatalog(catalog)
	if err := store.Save(&Connection{ID: "c1", Provider: ProviderGoogle, Subject: "sub-1", Email: "jane@x.com", VaultID: savedVaultID}); err != nil {
		t.Fatalf("seed identity: %v", err)
	}
	return flow, store, catalog
}

func beginGmailPreflight(t *testing.T, flow *IdentityFlow) (BeginConnectResult, *VaultPreflightError) {
	t.Helper()
	res, err := flow.BeginEnableGmail(context.Background(), BeginConnectParams{RedirectURL: testRedirect})
	if err == nil {
		return res, nil
	}
	var pf *VaultPreflightError
	if !errors.As(err, &pf) {
		t.Fatalf("BeginEnableGmail: %v", err)
	}
	return BeginConnectResult{}, pf
}

func TestBeginEnableGmail_VaultMatrix(t *testing.T) {
	cases := []struct {
		name        string
		saved       string
		vaults      []VaultRef
		wantOutcome VaultOutcome // empty means authorization is allowed to start
		wantRecords string       // vault id expected on the connection afterward
	}{
		{
			name:        "saved unlocked vault is reused without prompting",
			saved:       "v-1",
			vaults:      []VaultRef{vault("v-1", "Personal", VaultAvailable), vault("v-2", "Work", VaultAvailable)},
			wantRecords: "v-1",
		},
		{
			name:        "sole vault is auto-selected and recorded",
			saved:       "",
			vaults:      []VaultRef{vault("only", "Personal", VaultAvailable)},
			wantRecords: "only",
		},
		{
			name:        "no vault blocks with create",
			saved:       "",
			wantOutcome: VaultOutcomeCreate,
		},
		{
			name:        "several vaults block with choose",
			saved:       "",
			vaults:      []VaultRef{vault("v-1", "Personal", VaultAvailable), vault("v-2", "Work", VaultAvailable)},
			wantOutcome: VaultOutcomeChoose,
		},
		{
			name:        "locked vault blocks with unlock",
			saved:       "v-1",
			vaults:      []VaultRef{vault("v-1", "Personal", VaultLocked)},
			wantOutcome: VaultOutcomeUnlock,
		},
		{
			name:        "vanished saved vault blocks with repair",
			saved:       "v-gone",
			vaults:      []VaultRef{vault("v-1", "Personal", VaultAvailable)},
			wantOutcome: VaultOutcomeRepair,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			flow, store, _ := seedFlowWithVaults(t, tc.saved, tc.vaults...)
			res, pf := beginGmailPreflight(t, flow)

			if tc.wantOutcome != "" {
				if pf == nil {
					t.Fatalf("authorization started despite needing %q", tc.wantOutcome)
				}
				if pf.Preflight.Outcome != tc.wantOutcome {
					t.Fatalf("outcome = %q, want %q", pf.Preflight.Outcome, tc.wantOutcome)
				}
				if res.AuthorizeURL != "" {
					t.Fatal("a blocked preflight must not produce an authorize URL")
				}
				return
			}

			if pf != nil {
				t.Fatalf("unexpected block: %+v", pf.Preflight)
			}
			if res.AuthorizeURL == "" {
				t.Fatal("expected an authorize URL")
			}
			// FR 7: the choice is durable BEFORE the browser leaves for Google.
			conn, err := store.Load()
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if conn.VaultID != tc.wantRecords {
				t.Fatalf("recorded vault = %q, want %q", conn.VaultID, tc.wantRecords)
			}
		})
	}
}

// FR 10: the user's answer to a prompt resumes the same enable action.
func TestBeginEnableGmail_ExplicitChoiceIsRecordedAndUsed(t *testing.T) {
	flow, store, _ := seedFlowWithVaults(t, "",
		vault("v-1", "Personal", VaultAvailable),
		vault("v-2", "Work", VaultAvailable),
	)
	if _, pf := beginGmailPreflight(t, flow); pf == nil || pf.Preflight.Outcome != VaultOutcomeChoose {
		t.Fatal("expected a choose prompt first")
	}

	res, err := flow.BeginEnableGmail(context.Background(), BeginConnectParams{RedirectURL: testRedirect, VaultID: "v-2"})
	if err != nil {
		t.Fatalf("resume with explicit choice: %v", err)
	}
	if res.AuthorizeURL == "" {
		t.Fatal("expected an authorize URL after the choice")
	}
	conn, _ := store.Load()
	if conn.VaultID != "v-2" {
		t.Fatalf("recorded vault = %q, want v-2", conn.VaultID)
	}
}

// FR 9: repair must be able to move OFF a vault that no longer exists.
func TestBeginEnableGmail_ExplicitChoiceReplacesAMissingVault(t *testing.T) {
	flow, store, _ := seedFlowWithVaults(t, "v-gone", vault("v-1", "Personal", VaultAvailable))
	if _, err := flow.BeginEnableGmail(context.Background(), BeginConnectParams{RedirectURL: testRedirect, VaultID: "v-1"}); err != nil {
		t.Fatalf("repair: %v", err)
	}
	conn, _ := store.Load()
	if conn.VaultID != "v-1" {
		t.Fatalf("recorded vault = %q, want v-1", conn.VaultID)
	}
}

// FR 11: a blocked preflight leaves no in-flight authorization behind, so
// cancelling costs nothing and Gmail stays disabled.
func TestBeginEnableGmail_BlockedPreflightStartsNoFlow(t *testing.T) {
	flow, store, _ := seedFlowWithVaults(t, "v-1", vault("v-1", "Personal", VaultLocked))
	if _, pf := beginGmailPreflight(t, flow); pf == nil {
		t.Fatal("expected the locked vault to block")
	}
	conn, _ := store.Load()
	if _, ok := conn.Grant(ProductGmail); ok {
		t.Fatal("no Gmail grant may exist after a blocked preflight")
	}
	if len(flow.states.entries) != 0 {
		t.Fatalf("expected no pending authorization, got %d", len(flow.states.entries))
	}
}

// FR 2, 17, 67: Connect Google stays identity-only; Gmail scopes are requested
// only by the separate enable action, and send only by the explicit upgrade.
func TestScopeSeparation(t *testing.T) {
	flow, _, _ := seedFlowWithVaults(t, "v-1", vault("v-1", "Personal", VaultAvailable))

	connect, err := flow.BeginConnect(BeginConnectParams{RedirectURL: testRedirect})
	if err != nil {
		t.Fatalf("BeginConnect: %v", err)
	}
	if strings.Contains(connect.AuthorizeURL, "gmail") {
		t.Fatalf("identity connect must request no Gmail scope: %s", connect.AuthorizeURL)
	}

	enable, err := flow.BeginEnableGmail(context.Background(), BeginConnectParams{RedirectURL: testRedirect})
	if err != nil {
		t.Fatalf("BeginEnableGmail: %v", err)
	}
	if !strings.Contains(enable.AuthorizeURL, "gmail.readonly") {
		t.Fatal("enable must request gmail.readonly")
	}
	if strings.Contains(enable.AuthorizeURL, "gmail.send") {
		t.Fatal("enable must NOT bundle the send scope")
	}

	send, err := flow.BeginEnableGmailSend(context.Background(), BeginConnectParams{RedirectURL: testRedirect})
	if err != nil {
		t.Fatalf("BeginEnableGmailSend: %v", err)
	}
	if !strings.Contains(send.AuthorizeURL, "gmail.send") {
		t.Fatal("the explicit upgrade must request gmail.send")
	}
}

// --- Callback-time vault verification (FR 12-14) ----------------------------

// completeWithVaultState runs a full begin+callback with the vault healthy at
// begin time and in the given state by callback time.
func completeWithVaultState(t *testing.T, atCallback VaultAvailability) error {
	t.Helper()
	srv := gmailTokenServer(t, "openid email profile "+GmailReadonlyScope)
	flow, store := newTestFlow(t, srv.URL, fakeVerifier{id: Identity{Subject: "sub-1"}})
	flow.WithCredentialSink(&fakeSink{})
	catalog := &fakeVaultCatalog{vaults: []VaultRef{vault("v-1", "Personal", VaultAvailable)}}
	flow.WithVaultCatalog(catalog)
	if err := store.Save(&Connection{ID: "c1", Provider: ProviderGoogle, Subject: "sub-1", VaultID: "v-1"}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	begin, err := flow.BeginEnableGmail(context.Background(), BeginConnectParams{RedirectURL: testRedirect})
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	// The vault changes state while the user is away at Google.
	catalog.vaults = []VaultRef{vault("v-1", "Personal", atCallback)}

	_, err = flow.CompleteEnableGmail(context.Background(), CompleteConnectParams{State: begin.State, Code: "c"})
	return err
}

func TestCompleteEnableGmail_VaultLockedAtCallback(t *testing.T) {
	err := completeWithVaultState(t, VaultLocked)
	cb := ClassifyCallback(err)
	if cb == nil || cb.Category != CategoryVaultLocked {
		t.Fatalf("category = %+v, want vault_locked", cb)
	}
	if !cb.SignedIn {
		t.Fatal("the Google sign-in succeeded; the page must not blame Google")
	}
	if cb.VaultID != "v-1" {
		t.Fatalf("vault id = %q, want v-1 so the page can offer unlock", cb.VaultID)
	}
	if cb.Stage != StageVault {
		t.Fatalf("stage = %q, want vault", cb.Stage)
	}
}

func TestCompleteEnableGmail_VaultGoneAtCallback(t *testing.T) {
	err := completeWithVaultState(t, VaultMissing)
	cb := ClassifyCallback(err)
	if cb == nil || cb.Category != CategoryVaultSelectionRequired {
		t.Fatalf("category = %+v, want vault_selection_required", cb)
	}
	if !cb.SignedIn {
		t.Fatal("the Google sign-in succeeded")
	}
}

// FR 12: with no vault recorded there is nothing to guess at, even if exactly
// one vault happens to exist.
func TestCompleteEnableGmail_NoRecordedVaultNeverGuesses(t *testing.T) {
	srv := gmailTokenServer(t, GmailReadonlyScope)
	flow, store := newTestFlow(t, srv.URL, fakeVerifier{id: Identity{Subject: "sub-1"}})
	sink := &fakeSink{}
	flow.WithCredentialSink(sink)
	flow.WithVaultCatalog(&fakeVaultCatalog{vaults: []VaultRef{vault("v-1", "Personal", VaultAvailable)}})
	if err := store.Save(&Connection{ID: "c1", Provider: ProviderGoogle, Subject: "sub-1", VaultID: "v-1"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	begin, err := flow.BeginEnableGmail(context.Background(), BeginConnectParams{RedirectURL: testRedirect})
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	// The recorded choice is lost between begin and callback.
	conn, _ := store.Load()
	conn.VaultID = ""
	if err := store.Save(conn); err != nil {
		t.Fatalf("save: %v", err)
	}

	_, err = flow.CompleteEnableGmail(context.Background(), CompleteConnectParams{State: begin.State, Code: "c"})
	cb := ClassifyCallback(err)
	if cb == nil || cb.Category != CategoryVaultSelectionRequired {
		t.Fatalf("category = %+v, want vault_selection_required", cb)
	}
	if sink.got.AccessToken != "" {
		t.Fatal("no credential may be written when the destination is unknown")
	}
}

// A vault that locks between the callback's check and the actual write still
// reaches the user as unlock-and-retry (FR 13, 15).
func TestCompleteEnableGmail_LockedWriteStaysActionable(t *testing.T) {
	srv := gmailTokenServer(t, GmailReadonlyScope)
	flow, store := newTestFlow(t, srv.URL, fakeVerifier{id: Identity{Subject: "sub-1"}})
	flow.WithCredentialSink(&fakeSink{err: ErrVaultLockedWrite})
	flow.WithVaultCatalog(&fakeVaultCatalog{vaults: []VaultRef{vault("v-1", "Personal", VaultAvailable)}})
	if err := store.Save(&Connection{ID: "c1", Provider: ProviderGoogle, Subject: "sub-1", VaultID: "v-1"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	begin, _ := flow.BeginEnableGmail(context.Background(), BeginConnectParams{RedirectURL: testRedirect})

	_, err := flow.CompleteEnableGmail(context.Background(), CompleteConnectParams{State: begin.State, Code: "c"})
	cb := ClassifyCallback(err)
	if cb == nil || cb.Category != CategoryVaultLocked {
		t.Fatalf("category = %+v, want vault_locked", cb)
	}
}

// FR 15: each callback failure gets a distinct category, and the SignedIn flag
// separates "Google refused" from "this machine failed".
func TestCallbackCategories(t *testing.T) {
	cases := []struct {
		name         string
		err          error
		wantCategory CallbackCategory
		wantSignedIn bool
	}{
		{"cancel", ErrAuthorizationDenied, CategoryDenied, false},
		{"expired state", ErrExpiredFlow, CategoryExpiredState, false},
		{"wrong account", ErrDifferentAccountActive, CategoryAccountMismatch, true},
		{"no id token", ErrNoIDToken, CategoryIdentityUnverified, false},
		{"bad id token", ErrIDTokenInvalid, CategoryIdentityUnverified, false},
		{"nonce mismatch", ErrNonceMismatch, CategoryIdentityUnverified, false},
		{"not configured", ErrOAuthNotConfigured, CategoryNotConfigured, false},
		{"no identity", ErrNoActiveIdentity, CategoryNoIdentity, false},
		{"unclassified", errors.New("disk on fire"), CategoryUnknown, true},
	}
	seen := map[CallbackCategory]string{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cb := ClassifyCallback(tc.err)
			if cb.Category != tc.wantCategory {
				t.Fatalf("category = %q, want %q", cb.Category, tc.wantCategory)
			}
			if cb.SignedIn != tc.wantSignedIn {
				t.Fatalf("signed in = %v, want %v", cb.SignedIn, tc.wantSignedIn)
			}
			if prev, dup := seen[cb.Category]; dup && tc.wantCategory != CategoryIdentityUnverified {
				t.Fatalf("category %q collides with %q", cb.Category, prev)
			}
			seen[cb.Category] = tc.name
		})
	}
	if ClassifyCallback(nil) != nil {
		t.Fatal("nil error must classify to nil")
	}
}

// FR 19, 20: the diagnostic surface carries a correlation id and nothing secret.
func TestCallbackError_CarriesCorrelationNotSecrets(t *testing.T) {
	flow, store, _ := seedFlowWithVaults(t, "v-1", vault("v-1", "Personal", VaultAvailable))
	begin, err := flow.BeginEnableGmail(context.Background(), BeginConnectParams{RedirectURL: testRedirect})
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if begin.CorrelationID == "" {
		t.Fatal("begin must expose a correlation id for the log line")
	}
	if strings.Contains(begin.CorrelationID, begin.State) {
		t.Fatal("the correlation id must not be derived from the replay-sensitive state")
	}

	conn, _ := store.Load()
	conn.VaultID = ""
	_ = store.Save(conn)
	_, err = flow.CompleteEnableGmail(context.Background(), CompleteConnectParams{State: begin.State, Code: "secret-code"})
	cb := ClassifyCallback(err)
	if cb.CorrelationID != begin.CorrelationID {
		t.Fatalf("correlation id = %q, want %q", cb.CorrelationID, begin.CorrelationID)
	}
	if strings.Contains(cb.Error(), "secret-code") {
		t.Fatalf("callback error leaked the authorization code: %s", cb.Error())
	}
}
