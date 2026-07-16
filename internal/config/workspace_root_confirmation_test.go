package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspaceRootConfirmation_NewInstallStartsUnconfirmed(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("ORI_DATA_DIR", dataDir)

	manager := NewManager(filepath.Join(dataDir, "settings.json"))
	if err := manager.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if manager.IsWorkspaceRootConfirmed() {
		t.Fatal("new install must not assume the built-in workspace root is approved")
	}
	if got, want := UnconfirmedWorkspaceRoot(), filepath.Join(dataDir, "workspace-staging"); got != want {
		t.Fatalf("UnconfirmedWorkspaceRoot() = %q, want %q", got, want)
	}

	if err := manager.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	reloaded := NewManager(filepath.Join(dataDir, "settings.json"))
	if err := reloaded.Load(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.IsWorkspaceRootConfirmed() {
		t.Fatal("explicit false confirmation must survive a save and reload")
	}
}

func TestWorkspaceRootConfirmation_ExistingSettingsAreGrandfathered(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte(`{"speech_provider":"auto"}`), 0o600); err != nil {
		t.Fatalf("write legacy settings: %v", err)
	}

	manager := NewManager(path)
	if err := manager.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !manager.IsWorkspaceRootConfirmed() {
		t.Fatal("settings written before the consent flag should be grandfathered")
	}
}

func TestSetWorkspaceRootConfirmsCustomAndBuiltInLocations(t *testing.T) {
	manager := NewManager(filepath.Join(t.TempDir(), "settings.json"))
	if err := manager.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	if err := manager.SetWorkspaceRoot(filepath.Join(t.TempDir(), "custom")); err != nil {
		t.Fatalf("SetWorkspaceRoot(custom): %v", err)
	}
	if !manager.IsWorkspaceRootConfirmed() {
		t.Fatal("saving a custom root must record confirmation")
	}

	if err := manager.SetWorkspaceRoot(""); err != nil {
		t.Fatalf("SetWorkspaceRoot(default): %v", err)
	}
	if !manager.IsWorkspaceRootConfirmed() {
		t.Fatal("explicitly choosing the built-in default must stay confirmed")
	}
}
