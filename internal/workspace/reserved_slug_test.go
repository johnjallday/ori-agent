package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// <root>/Agents holds the user's agents, so no workspace may take that
// top-level folder. The check is case-insensitive because macOS file systems
// are, and it applies at the top level only.

func reservedSlugStore(t *testing.T) (*FileStore, string) {
	t.Helper()
	root := t.TempDir()
	fs, err := NewFileStore(root)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	t.Cleanup(func() { _ = fs.Close() })
	return fs, root
}

func TestCreatingATopLevelWorkspaceNamedAgentsIsRejected(t *testing.T) {
	for _, name := range []string{"Agents", "agents", "AGENTS"} {
		fs, root := reservedSlugStore(t)
		err := fs.Save(&Workspace{ID: "ws-" + name, Name: name})
		if !errors.Is(err, ErrReservedWorkspaceSlug) {
			t.Errorf("Save(%q): err = %v, want ErrReservedWorkspaceSlug", name, err)
		}
		if _, statErr := os.Stat(filepath.Join(root, "agents")); !os.IsNotExist(statErr) {
			t.Errorf("Save(%q) created the reserved folder", name)
		}
	}
}

func TestSaveAtTheRootIsRejectedToo(t *testing.T) {
	fs, root := reservedSlugStore(t)
	if err := fs.SaveAt(&Workspace{ID: "ws-1", Name: "Agents"}, root); !errors.Is(err, ErrReservedWorkspaceSlug) {
		t.Fatalf("SaveAt: err = %v, want ErrReservedWorkspaceSlug", err)
	}
}

func TestAWorkspaceNamedAgentsInsideAGroupIsAllowed(t *testing.T) {
	fs, _ := reservedSlugStore(t)
	if err := fs.Save(&Workspace{ID: "group", Name: "Team"}); err != nil {
		t.Fatalf("save group: %v", err)
	}
	if err := fs.Save(&Workspace{ID: "member", Name: "Agents", ParentID: "group"}); err != nil {
		t.Fatalf("a member named Agents must be allowed: %v", err)
	}
}

func TestRenamingAWorkspaceToAgentsIsRejected(t *testing.T) {
	fs, root := reservedSlugStore(t)
	if err := fs.Save(&Workspace{ID: "ws-1", Name: "Scouting"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := fs.RenameWithSlug("ws-1", "Agents", ""); !errors.Is(err, ErrReservedWorkspaceSlug) {
		t.Fatalf("RenameWithSlug: err = %v, want ErrReservedWorkspaceSlug", err)
	}
	if _, err := os.Stat(filepath.Join(root, "scouting", WorkspaceConfigFile)); err != nil {
		t.Errorf("the refused rename moved the workspace: %v", err)
	}
}

func TestMovingAMemberNamedAgentsToTheTopLevelIsRejected(t *testing.T) {
	fs, _ := reservedSlugStore(t)
	if err := fs.Save(&Workspace{ID: "group", Name: "Team"}); err != nil {
		t.Fatalf("save group: %v", err)
	}
	if err := fs.Save(&Workspace{ID: "member", Name: "Agents", ParentID: "group"}); err != nil {
		t.Fatalf("save member: %v", err)
	}
	if _, err := fs.MoveWorkspaceFolder("member", ""); !errors.Is(err, ErrReservedWorkspaceSlug) {
		t.Fatalf("MoveWorkspaceFolder: err = %v, want ErrReservedWorkspaceSlug", err)
	}
}

func TestImportingAWorkspaceIntoTheAgentsFolderIsRejected(t *testing.T) {
	fs, root := reservedSlugStore(t)
	outside := filepath.Join(t.TempDir(), "agents")
	if err := os.MkdirAll(outside, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outside, WorkspaceConfigFile), []byte(`{"id":"imported","name":"Agents"}`), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, _, err := fs.Import(outside); !errors.Is(err, ErrReservedWorkspaceSlug) {
		t.Fatalf("Import: err = %v, want ErrReservedWorkspaceSlug", err)
	}
	if _, err := os.Stat(filepath.Join(root, "agents")); !os.IsNotExist(err) {
		t.Error("the refused import copied into the reserved folder")
	}
}

func TestAnExistingWorkspaceAlreadyNamedAgentsKeepsSaving(t *testing.T) {
	root := t.TempDir()
	existing := filepath.Join(root, "agents")
	if err := os.MkdirAll(existing, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(existing, WorkspaceConfigFile),
		[]byte(`{"id":"old","name":"Agents","folder_slug":"agents"}`), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	fs, err := NewFileStore(root)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	t.Cleanup(func() { _ = fs.Close() })
	ws, err := fs.Get("old")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	ws.Description = "still mine"
	if err := fs.Save(ws); err != nil {
		t.Fatalf("an existing workspace in the reserved folder must keep saving: %v", err)
	}
}
