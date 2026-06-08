package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func writeWorkspaceConfig(t *testing.T, dir, id, name string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	data, err := newTestWorkspace(id, name).ToJSON()
	if err != nil {
		t.Fatalf("ToJSON: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, WorkspaceConfigFile), data, 0o644); err != nil {
		t.Fatalf("write %s: %v", WorkspaceConfigFile, err)
	}
}

func TestReadWorkspaceFolderMeta(t *testing.T) {
	dir := t.TempDir()
	writeWorkspaceConfig(t, filepath.Join(dir, "ws"), "ws-1", "A Workspace")

	id, name, ok := readWorkspaceFolderMeta(filepath.Join(dir, "ws"))
	if !ok || id != "ws-1" || name != "A Workspace" {
		t.Fatalf("readWorkspaceFolderMeta(ws) = (%q, %q, %v), want (ws-1, A Workspace, true)", id, name, ok)
	}

	if err := os.MkdirAll(filepath.Join(dir, "plain"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := readWorkspaceFolderMeta(filepath.Join(dir, "plain")); ok {
		t.Fatalf("plain folder (no %s) reported as a workspace", WorkspaceConfigFile)
	}
}

// TestAnnotateWorkspaceEntries covers registered-only detection: a registered
// workspace folder is annotated (with the registered name), while an
// unregistered workspace.json folder, a plain folder, and files are left alone.
func TestAnnotateWorkspaceEntries(t *testing.T) {
	linked := t.TempDir()
	// member/ is a registered workspace folder; its on-disk name differs from the
	// registered name to prove we surface the authoritative registered name.
	writeWorkspaceConfig(t, filepath.Join(linked, "member"), "reg-1", "Member On Disk")
	// stray/ has a workspace.json but the id is not registered.
	writeWorkspaceConfig(t, filepath.Join(linked, "stray"), "unreg-1", "Stray")
	if err := os.MkdirAll(filepath.Join(linked, "plain"), 0o755); err != nil {
		t.Fatal(err)
	}

	store := newTestWorkspaceStore(t, newTestWorkspace("reg-1", "Registered WS"))

	files := []FileInfo{
		{Name: "member", RelativePath: "member", IsDir: true},
		{Name: "stray", RelativePath: "stray", IsDir: true},
		{Name: "plain", RelativePath: "plain", IsDir: true},
		{Name: "notes.txt", RelativePath: "notes.txt", IsDir: false},
	}
	annotateWorkspaceEntries(store, linked, files)

	if !files[0].IsWorkspace || files[0].WorkspaceID != "reg-1" {
		t.Fatalf("member should be annotated as registered workspace reg-1, got %+v", files[0])
	}
	if files[0].WorkspaceName != "Registered WS" {
		t.Fatalf("member WorkspaceName = %q, want registered name %q", files[0].WorkspaceName, "Registered WS")
	}
	if files[1].IsWorkspace {
		t.Fatalf("unregistered stray folder must not be annotated, got %+v", files[1])
	}
	if files[2].IsWorkspace {
		t.Fatalf("plain folder must not be annotated")
	}
	if files[3].IsWorkspace {
		t.Fatalf("non-directory entry must not be annotated")
	}
}
