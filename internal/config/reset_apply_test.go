package config

import (
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/vault"
)

func TestResetPreferencesKeepsOnlyReviewedCrossCategoryRoots(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	manager := NewManagerWithSecretStore(path, vault.NewMemorySecretStore())
	if err := manager.Load(); err != nil {
		t.Fatal(err)
	}
	if err := manager.SetWorkspaceRoot(filepath.Join(t.TempDir(), "projects")); err != nil {
		t.Fatal(err)
	}
	if err := manager.SetVaultRoot(filepath.Join(t.TempDir(), "vaults")); err != nil {
		t.Fatal(err)
	}
	if err := manager.SetTemplatesRoot(filepath.Join(t.TempDir(), "templates")); err != nil {
		t.Fatal(err)
	}
	manager.SetMultiAgentDefaults("force", 9)
	beforeWorkspace, beforeVault := manager.GetWorkspaceRoot(), manager.GetVaultRoot()

	options := ResetPreferencesOptions{PreserveWorkspaceRoot: true, PreserveVaultRoot: true}
	if err := manager.ResetPreferences(options); err != nil {
		t.Fatal(err)
	}
	if !manager.ResetPreferencesVerified(options) {
		t.Fatal("canonical persisted defaults were not verified")
	}
	reloaded := NewManagerWithSecretStore(path, vault.NewMemorySecretStore())
	if err := reloaded.Load(); err != nil {
		t.Fatal(err)
	}
	if reloaded.GetWorkspaceRoot() != beforeWorkspace || !reloaded.IsWorkspaceRootConfirmed() {
		t.Fatal("workspace ownership root was not retained")
	}
	if reloaded.GetVaultRoot() != beforeVault {
		t.Fatal("vault root was not retained")
	}
	if reloaded.GetTemplatesRoot() != "" {
		t.Fatal("unselected template preference was not reset")
	}
	mode, threshold := reloaded.GetMultiAgentDefaults()
	if mode == "force" || threshold == 9 {
		t.Fatal("ordinary preferences were retained")
	}
}

func TestClearWorkspaceRegistrationPreservesUnrelatedSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	manager := NewManagerWithSecretStore(path, vault.NewMemorySecretStore())
	if err := manager.Load(); err != nil {
		t.Fatal(err)
	}
	if err := manager.SetWorkspaceRoot(filepath.Join(t.TempDir(), "projects")); err != nil {
		t.Fatal(err)
	}
	if err := manager.SetVaultRoot(filepath.Join(t.TempDir(), "vaults")); err != nil {
		t.Fatal(err)
	}
	manager.SetMultiAgentDefaults("force", 9)
	vaultRoot := manager.GetVaultRoot()

	if err := manager.ClearWorkspaceRegistration(); err != nil {
		t.Fatal(err)
	}
	if manager.GetWorkspaceRoot() != "" || manager.IsWorkspaceRootConfirmed() {
		t.Fatal("workspace registration remained")
	}
	if manager.GetVaultRoot() != vaultRoot {
		t.Fatal("vault root changed")
	}
	mode, threshold := manager.GetMultiAgentDefaults()
	if mode != "force" || threshold != 9 {
		t.Fatal("unrelated preferences changed")
	}
}
