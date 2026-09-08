package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResetAgentPersistenceRemovesOwnedProfilesAndPreservesUnknownEntries(t *testing.T) {
	root := t.TempDir()
	profiles := filepath.Join(root, "agents")
	if err := os.MkdirAll(filepath.Join(profiles, "Owned Agent"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profiles, "Owned Agent", "agent_settings.json"), []byte(`{"type":"tool-calling"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profiles, "legacy.json"), []byte(`{"type":"tool-calling"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	unknown := filepath.Join(profiles, "README.txt")
	if err := os.WriteFile(unknown, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(t.TempDir(), "external")
	if err := os.Mkdir(external, 0o750); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(profiles, "external-link")
	if err := os.Symlink(external, link); err != nil {
		t.Fatal(err)
	}
	index := filepath.Join(root, "agents.json")
	if err := os.WriteFile(index, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}

	removed, err := ResetAgentPersistence(index, profiles, index)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 2 {
		t.Fatalf("removed = %d, want 2", removed)
	}
	for _, path := range []string{filepath.Join(profiles, "Owned Agent"), filepath.Join(profiles, "legacy.json"), index} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("owned path remains %s: %v", filepath.Base(path), err)
		}
	}
	for _, path := range []string{unknown, link, external} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("retained path missing %s: %v", filepath.Base(path), err)
		}
	}
}
