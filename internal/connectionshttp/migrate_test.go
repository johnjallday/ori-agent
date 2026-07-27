package connectionshttp

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/johnjallday/ori-agent/internal/connections"
)

type fakeMigrator struct {
	legacy    []LegacyAccount
	migrated  string
	returnErr error
}

func (f *fakeMigrator) ListLegacyGmailAccounts(_ context.Context) ([]LegacyAccount, error) {
	return f.legacy, nil
}

func (f *fakeMigrator) MigrateAccount(_ context.Context, accountID, _, _, _ string) error {
	if f.returnErr != nil {
		return f.returnErr
	}
	f.migrated = accountID
	return nil
}

func migratorHandler(t *testing.T, m Migrator, gmailEnabled bool) *http.ServeMux {
	t.Helper()
	store := connections.NewStore(t.TempDir())
	conn := &connections.Connection{
		ID: "c1", Provider: connections.ProviderGoogle, Subject: "sub-1", Email: "jane@example.com", VaultID: "v1",
		Grants: map[connections.ProductKey]*connections.ProductGrant{},
	}
	if gmailEnabled {
		conn.Grants[connections.ProductGmail] = &connections.ProductGrant{
			ConnectionID: "c1", Product: connections.ProductGmail, CredentialRef: "vault://email/conn-gmail", Health: connections.HealthHealthy,
		}
	}
	if err := store.Save(conn); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(Deps{Store: store, Guard: NewOriginGuard(), Migrator: m})
	mux := http.NewServeMux()
	h.Register(mux)
	return mux
}

func TestMigratable_ComputesEmailMatch(t *testing.T) {
	m := &fakeMigrator{legacy: []LegacyAccount{
		{ID: "a1", Email: "jane@example.com", WorkspaceID: "ws1"},
		{ID: "a2", Email: "other@example.com", WorkspaceID: "ws2"},
	}}
	mux := migratorHandler(t, m, true)
	rec := do(mux, http.MethodGet, "http://localhost/api/connections/google/migratable", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	var resp struct {
		Accounts []LegacyAccount `json:"accounts"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	byID := map[string]LegacyAccount{}
	for _, a := range resp.Accounts {
		byID[a.ID] = a
	}
	if !byID["a1"].EmailMatches {
		t.Error("same-email legacy account should be email_matches=true")
	}
	if byID["a2"].EmailMatches {
		t.Error("different-email legacy account must be email_matches=false")
	}
}

func TestMigrate_MatchingSucceeds(t *testing.T) {
	m := &fakeMigrator{}
	mux := migratorHandler(t, m, true)
	rec := do(mux, http.MethodPost, "http://localhost/api/connections/google/migrate?account_id=a1", "http://localhost")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if m.migrated != "a1" {
		t.Errorf("migrated = %q, want a1", m.migrated)
	}
}

func TestMigrate_MismatchReturns409(t *testing.T) {
	m := &fakeMigrator{returnErr: connections.ErrAccountMismatch}
	mux := migratorHandler(t, m, true)
	rec := do(mux, http.MethodPost, "http://localhost/api/connections/google/migrate?account_id=a2", "http://localhost")
	if rec.Code != http.StatusConflict {
		t.Fatalf("status %d", rec.Code)
	}
	var body map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["error"] != "account_mismatch" {
		t.Errorf("error = %q, want account_mismatch", body["error"])
	}
}

func TestMigrate_GmailNotEnabledReturns409(t *testing.T) {
	mux := migratorHandler(t, &fakeMigrator{}, false)
	rec := do(mux, http.MethodPost, "http://localhost/api/connections/google/migrate?account_id=a1", "http://localhost")
	if rec.Code != http.StatusConflict {
		t.Fatalf("status %d (want 409 when Gmail isn't enabled)", rec.Code)
	}
}

func TestMigrate_MissingAccountIDReturns400(t *testing.T) {
	mux := migratorHandler(t, &fakeMigrator{}, true)
	rec := do(mux, http.MethodPost, "http://localhost/api/connections/google/migrate", "http://localhost")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d (want 400 without account_id)", rec.Code)
	}
}
