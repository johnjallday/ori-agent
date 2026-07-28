package server

import (
	"context"
	"testing"

	"github.com/johnjallday/ori-agent/internal/connections"
	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/vault"
)

const testConnVaultPassword = "test-vault-password"

func newTestVaultStore(t *testing.T) *vault.Store {
	t.Helper()
	db, err := database.Open(context.Background(), &database.Config{InMemory: true, WALMode: false})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return vault.NewStore(db, vault.StoreOptions{VaultFilesBaseDir: t.TempDir()})
}

func createTestVault(t *testing.T, store *vault.Store, name string) vault.Vault {
	t.Helper()
	item := vault.Vault{Name: name}
	if err := store.CreateVault(context.Background(), &item, testConnVaultPassword); err != nil {
		t.Fatalf("create vault %q: %v", name, err)
	}
	return item
}

// A freshly created vault is unlocked for this process, so preflight can use it
// without prompting.
func TestConnectionVaultCatalog_ListsCreatedVaultAsAvailable(t *testing.T) {
	store := newTestVaultStore(t)
	created := createTestVault(t, store, "Personal")

	refs, err := newConnectionVaultCatalog(store).ListVaults(context.Background())
	if err != nil {
		t.Fatalf("ListVaults: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("got %d vaults, want 1", len(refs))
	}
	if refs[0].ID != created.ID || refs[0].Name != "Personal" {
		t.Fatalf("ref = %+v, want id %q name Personal", refs[0], created.ID)
	}
	if refs[0].Availability != connections.VaultAvailable {
		t.Fatalf("availability = %q, want available", refs[0].Availability)
	}
}

// Locking a vault (the state after a process restart with a password-protected
// vault) must surface as locked, which is what drives the unlock prompt instead
// of a failed authorization at callback time.
func TestConnectionVaultCatalog_LockedVaultReportsLocked(t *testing.T) {
	ctx := context.Background()
	store := newTestVaultStore(t)
	created := createTestVault(t, store, "Personal")
	if err := store.Lock(ctx, created.ID); err != nil {
		t.Fatalf("lock vault: %v", err)
	}

	catalog := newConnectionVaultCatalog(store)
	availability, err := catalog.VaultAvailability(ctx, created.ID)
	if err != nil {
		t.Fatalf("VaultAvailability: %v", err)
	}
	if availability != connections.VaultLocked {
		t.Fatalf("availability = %q, want locked", availability)
	}

	refs, err := catalog.ListVaults(ctx)
	if err != nil {
		t.Fatalf("ListVaults: %v", err)
	}
	if len(refs) != 1 || refs[0].Availability != connections.VaultLocked {
		t.Fatalf("listed availability = %+v, want locked", refs)
	}
}

// A remembered vault id that no longer exists is a repair prompt, not an error:
// the user picks or creates a replacement.
func TestConnectionVaultCatalog_UnknownVaultIsMissingNotError(t *testing.T) {
	catalog := newConnectionVaultCatalog(newTestVaultStore(t))
	availability, err := catalog.VaultAvailability(context.Background(), "v-does-not-exist")
	if err != nil {
		t.Fatalf("VaultAvailability: %v", err)
	}
	if availability != connections.VaultMissing {
		t.Fatalf("availability = %q, want missing", availability)
	}
}

func TestConnectionVaultCatalog_BlankVaultIDIsMissing(t *testing.T) {
	catalog := newConnectionVaultCatalog(newTestVaultStore(t))
	availability, err := catalog.VaultAvailability(context.Background(), "  ")
	if err != nil {
		t.Fatalf("VaultAvailability: %v", err)
	}
	if availability != connections.VaultMissing {
		t.Fatalf("availability = %q, want missing", availability)
	}
}

// With no vault store there is no safe default: the adapter must report the
// failure rather than pretending zero vaults exist (which would send the user
// into an unnecessary "create a vault" flow).
func TestConnectionVaultCatalog_NoStoreErrors(t *testing.T) {
	catalog := newConnectionVaultCatalog(nil)
	if _, err := catalog.ListVaults(context.Background()); err == nil {
		t.Fatal("ListVaults must error without a store")
	}
	if _, err := catalog.VaultAvailability(context.Background(), "v-1"); err == nil {
		t.Fatal("VaultAvailability must error without a store")
	}
}

// End-to-end through the domain: multiple unlocked vaults with nothing recorded
// on the connection is the "choose" state, and locking the recorded one flips it
// to "unlock".
func TestConnectionVaultCatalog_DrivesPreflight(t *testing.T) {
	ctx := context.Background()
	store := newTestVaultStore(t)
	first := createTestVault(t, store, "Personal")
	createTestVault(t, store, "Work")
	catalog := newConnectionVaultCatalog(store)

	pf, err := connections.PreflightVault(ctx, catalog, "")
	if err != nil {
		t.Fatalf("PreflightVault: %v", err)
	}
	if pf.Outcome != connections.VaultOutcomeChoose || len(pf.Options) != 2 {
		t.Fatalf("preflight = %+v, want choose with 2 options", pf)
	}

	pf, err = connections.PreflightVault(ctx, catalog, first.ID)
	if err != nil {
		t.Fatalf("PreflightVault: %v", err)
	}
	if pf.Outcome != connections.VaultOutcomeReady || pf.VaultID != first.ID {
		t.Fatalf("preflight = %+v, want ready on %q", pf, first.ID)
	}

	if err := store.Lock(ctx, first.ID); err != nil {
		t.Fatalf("lock vault: %v", err)
	}
	pf, err = connections.PreflightVault(ctx, catalog, first.ID)
	if err != nil {
		t.Fatalf("PreflightVault: %v", err)
	}
	if pf.Outcome != connections.VaultOutcomeUnlock || pf.VaultID != first.ID {
		t.Fatalf("preflight = %+v, want unlock on %q", pf, first.ID)
	}
}
