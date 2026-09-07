package vault_test

import (
	"bytes"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/testutil/resetfixture"
	"github.com/johnjallday/ori-agent/internal/vault"
)

type resetSecretRunner struct {
	calls [][]string
}

func (r *resetSecretRunner) Run(_ string, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, append([]string{name}, args...))
	return nil, errors.New("synthetic locked keychain")
}

func TestResetLifecycleRelativeSecretNamespaceIsSharedAcrossRoots(t *testing.T) {
	f := resetfixture.New(t)
	available := true
	runner := &resetSecretRunner{}
	for _, cwd := range []string{f.Paths().WorkDir, f.Paths().DataDir} {
		t.Chdir(cwd)
		secrets := vault.NewAutoSecretStore(vault.AutoSecretStoreOptions{
			GOOS: "darwin", Namespace: "settings.json", Runner: runner, DarwinAvailable: &available,
		})
		if !secrets.Status().Available {
			t.Fatal("expected discovery-based backend status")
		}
		if _, err := secrets.Get(vault.SecretKeyOpenAIAPIKey); err == nil || errors.Is(err, vault.ErrSecretNotFound) {
			t.Fatal("locked store must not be interpreted as an absent key")
		}
	}
	if len(runner.calls) != 2 || !reflect.DeepEqual(runner.calls[0], runner.calls[1]) {
		t.Fatal("characterization changed: relative namespace now distinguishes data roots")
	}
	// The injected runner never executes OS commands or obtains secret values.
}

func TestResetLifecycleCatalogDetachPreservesReopenableVaultFile(t *testing.T) {
	f := resetfixture.New(t)
	db, err := database.Open(t.Context(), &database.Config{Path: filepath.Join(f.Paths().DataDir, "sessions.db"), WALMode: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	opts := vault.StoreOptions{VaultFilesBaseDir: f.Paths().DataDir, ManagedVaultRoot: f.Paths().Vaults}
	owner := vault.NewStore(db, opts)
	password := rand.Text()
	item := vault.Vault{Name: "Retained Fixture"}
	if err := owner.CreateVault(t.Context(), &item, password); err != nil {
		t.Fatal(err)
	}
	record := &vault.Record{VaultID: item.ID, Type: "personal_note", Label: "Fixture", Payload: []byte(`{"synthetic":true}`)}
	if err := owner.CreateRecord(t.Context(), record, vault.AccessContext{}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(item.FilePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.DeleteVault(t.Context(), item.ID); err != nil {
		t.Fatal(err)
	}
	fresh := vault.NewStore(db, opts)
	registered, err := fresh.ListVaults(t.Context())
	if err != nil || len(registered) != 0 {
		t.Fatal("retained package automatically reattached:", err)
	}
	after, err := os.ReadFile(item.FilePath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("catalog detach changed retained vault bytes:", err)
	}
	// Characterize key independence, not a shipped attachment API: explicitly
	// restore ONLY the captured catalog metadata into this fixture DB. A real
	// file-attachment operation still needs an owner-validated API in group 4.
	_, err = db.ExecContext(t.Context(), `INSERT INTO vaults (id, name, description, file_path, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		item.ID, item.Name, item.Description, item.FilePath, item.CreatedAt, item.UpdatedAt)
	if err != nil {
		t.Fatal(err)
	}
	if err := fresh.Unlock(t.Context(), item.ID, password); err != nil {
		t.Fatal("retained package lost required encryption material:", err)
	}
	reopened, err := fresh.GetRecord(t.Context(), record.ID, vault.AccessContext{})
	if err != nil || !bytes.Equal(reopened.Payload, record.Payload) {
		t.Fatal("retained record cannot be decrypted after explicit catalog reattachment:", err)
	}
}
