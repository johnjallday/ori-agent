package vault

import (
	"context"
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/database"
)

func newKeyringFixture(t *testing.T) (*database.DB, string) {
	t.Helper()
	db, err := database.Open(context.Background(), &database.Config{InMemory: true})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, t.TempDir()
}

func appPasswordAccount(vaultID string) EmailAccountInput {
	return EmailAccountInput{
		VaultID: vaultID, Provider: EmailProviderIMAPSMTP, AuthType: EmailAuthTypeAppPassword,
		EmailAddress: "me@fastmail.example", IMAPHost: "imap.fastmail.example", SMTPHost: "smtp.fastmail.example",
		Credentials: EmailAccountCredentials{Password: "app-password"},
	}
}

func TestKeyringCreatesTheVaultOnceAndReusesIt(t *testing.T) {
	ctx := context.Background()
	db, dir := newKeyringFixture(t)
	store := NewStore(db, StoreOptions{VaultFilesBaseDir: dir})
	keyring := NewKeyring(store, NewMemorySecretStore())

	first, err := keyring.EnsureVault(ctx, "Email")
	if err != nil {
		t.Fatalf("EnsureVault: %v", err)
	}
	second, err := keyring.EnsureVault(ctx, "Email")
	if err != nil || second != first {
		t.Fatalf("second EnsureVault = %q, %v; want the same vault %q", second, err, first)
	}
	vaults, err := store.ListVaults(ctx)
	if err != nil || len(vaults) != 1 || vaults[0].Name != "Email" {
		t.Fatalf("vaults = %+v, %v; want one vault named Email", vaults, err)
	}
	if keyring.RememberedVaultID() != first {
		t.Fatalf("remembered = %q, want %q", keyring.RememberedVaultID(), first)
	}
}

// The point of the keyring: a restart locks every vault, and the remembered one
// must open again with nobody typing a password.
func TestKeyringUnlocksTheVaultAfterARestart(t *testing.T) {
	ctx := context.Background()
	db, dir := newKeyringFixture(t)
	secrets := NewMemorySecretStore()

	before := NewStore(db, StoreOptions{VaultFilesBaseDir: dir})
	vaultID, err := NewKeyring(before, secrets).EnsureVault(ctx, "Email")
	if err != nil {
		t.Fatalf("EnsureVault: %v", err)
	}
	account, err := before.CreateEmailAccount(ctx, appPasswordAccount(vaultID))
	if err != nil {
		t.Fatalf("CreateEmailAccount: %v", err)
	}

	// A new store over the same files is what a restart looks like: no key in memory.
	after := NewStore(db, StoreOptions{VaultFilesBaseDir: dir})
	if _, err := after.GetEmailAccount(ctx, account.ID); !errors.Is(err, ErrVaultLocked) {
		t.Fatalf("before unlock err = %v, want ErrVaultLocked", err)
	}
	if err := NewKeyring(after, secrets).UnlockRemembered(ctx); err != nil {
		t.Fatalf("UnlockRemembered: %v", err)
	}
	login, err := after.RevealEmailLoginCredentials(ctx, account.ID, AccessContext{})
	if err != nil || login.Password != "app-password" {
		t.Fatalf("login after restart = %+v, %v", login, err)
	}
}

func TestKeyringNeverTakesAUsersVaultName(t *testing.T) {
	ctx := context.Background()
	db, dir := newKeyringFixture(t)
	store := NewStore(db, StoreOptions{VaultFilesBaseDir: dir})
	mine := createTestVault(t, ctx, store, "Email")

	id, err := NewKeyring(store, NewMemorySecretStore()).EnsureVault(ctx, "Email")
	if err != nil {
		t.Fatalf("EnsureVault: %v", err)
	}
	if id == mine.ID {
		t.Fatal("the keyring adopted the user's own vault, whose password it does not have")
	}
	created, err := store.getVault(ctx, id)
	if err != nil || created.Name != "Email 2" {
		t.Fatalf("created vault = %+v, %v; want it named Email 2", created, err)
	}
}

func TestKeyringReplacesADeletedVaultAndForgetsIt(t *testing.T) {
	ctx := context.Background()
	db, dir := newKeyringFixture(t)
	store := NewStore(db, StoreOptions{VaultFilesBaseDir: dir})
	secrets := NewMemorySecretStore()
	keyring := NewKeyring(store, secrets)

	first, err := keyring.EnsureVault(ctx, "Email")
	if err != nil {
		t.Fatalf("EnsureVault: %v", err)
	}
	if err := store.DeleteVault(ctx, first); err != nil {
		t.Fatalf("DeleteVault: %v", err)
	}
	if err := keyring.UnlockRemembered(ctx); err != nil {
		t.Fatalf("UnlockRemembered with the vault gone: %v", err)
	}
	if _, err := secrets.Get(SecretKeyRememberedVault); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("a deleted vault's password stayed in the secret store: %v", err)
	}

	second, err := keyring.EnsureVault(ctx, "Email")
	if err != nil || second == first {
		t.Fatalf("EnsureVault after delete = %q, %v; want a new vault", second, err)
	}
	if err := keyring.Forget("some-other-vault"); err != nil || keyring.RememberedVaultID() != second {
		t.Fatalf("Forget of another vault dropped the remembered one: %v", err)
	}
	if err := keyring.Forget(second); err != nil || keyring.RememberedVaultID() != "" {
		t.Fatalf("Forget(%s) = %v, remembered %q", second, err, keyring.RememberedVaultID())
	}
}

type failingSecretStore struct{ *MemorySecretStore }

func (failingSecretStore) Set(SecretKey, string) error { return errors.New("keychain refused") }

func TestKeyringWithoutAWritableSecretStoreCreatesNothing(t *testing.T) {
	ctx := context.Background()
	db, dir := newKeyringFixture(t)
	store := NewStore(db, StoreOptions{VaultFilesBaseDir: dir})

	if _, err := NewKeyring(store, nil).EnsureVault(ctx, "Email"); !errors.Is(err, ErrKeyringUnavailable) {
		t.Fatalf("nil secret store err = %v, want ErrKeyringUnavailable", err)
	}
	if _, err := NewKeyring(store, failingSecretStore{NewMemorySecretStore()}).EnsureVault(ctx, "Email"); !errors.Is(err, ErrKeyringUnavailable) {
		t.Fatalf("refusing secret store err = %v, want ErrKeyringUnavailable", err)
	}
	if vaults, _ := store.ListVaults(ctx); len(vaults) != 0 {
		t.Fatalf("a vault was created without its password being kept: %+v", vaults)
	}
}

// unreadableSecretStore is a machine with no secret store, or a locked keychain.
type unreadableSecretStore struct {
	*MemorySecretStore
	err error
}

func (s unreadableSecretStore) Get(SecretKey) (string, error) { return "", s.err }

func TestKeyringWithAnUnreadableSecretStore(t *testing.T) {
	ctx := context.Background()
	db, dir := newKeyringFixture(t)
	store := NewStore(db, StoreOptions{VaultFilesBaseDir: dir})

	none := NewKeyring(store, unreadableSecretStore{NewMemorySecretStore(), ErrSecretStoreUnavailable})
	if err := none.UnlockRemembered(ctx); err != nil {
		t.Fatalf("UnlockRemembered with no secret store = %v; nothing was remembered, so nothing is wrong", err)
	}
	if _, err := none.EnsureVault(ctx, "Email"); !errors.Is(err, ErrKeyringUnavailable) {
		t.Fatalf("EnsureVault with no secret store = %v, want ErrKeyringUnavailable", err)
	}

	locked := NewKeyring(store, unreadableSecretStore{NewMemorySecretStore(), errors.New("User interaction is not allowed.")})
	if err := locked.UnlockRemembered(ctx); !errors.Is(err, ErrKeyringUnavailable) {
		t.Fatalf("UnlockRemembered with a locked keychain = %v, want it reported", err)
	}
	if vaults, _ := store.ListVaults(ctx); len(vaults) != 0 {
		t.Fatalf("a vault was created though its password could not be kept: %+v", vaults)
	}
}

func TestGeneratedVaultPasswordsAreLongAndDistinct(t *testing.T) {
	a, errA := generateVaultPassword()
	b, errB := generateVaultPassword()
	if errA != nil || errB != nil || a == b || len(a) < 40 {
		t.Fatalf("passwords %q / %q, errs %v %v", a, b, errA, errB)
	}
}
