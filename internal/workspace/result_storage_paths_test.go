package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveWorkspaceFolderBaseDirUsesTheWorkspaceFilesRoot(t *testing.T) {
	store, ws := newResultStorageTestWorkspace(t, "ws-storage-folder", "Storage Folder")

	baseDir, folder, err := ResolveWorkspaceFolderBaseDir(store, ws.ID, filepath.Join("reports", "daily"))
	if err != nil {
		t.Fatalf("ResolveWorkspaceFolderBaseDir: %v", err)
	}
	wantBase := filepath.Join(store.GetFilesPath(ws.ID), "reports", "daily")
	if baseDir != wantBase {
		t.Fatalf("base dir = %q, want %q", baseDir, wantBase)
	}
	if folder != filepath.Join("reports", "daily") {
		t.Fatalf("folder = %q, want the cleaned relative folder", folder)
	}

	finalPath, err := BuildFinalPath(baseDir, filepath.Join("runs", "summary.txt"))
	if err != nil {
		t.Fatalf("BuildFinalPath: %v", err)
	}
	if want := filepath.Join(wantBase, "runs", "summary.txt"); finalPath != want {
		t.Fatalf("final path = %q, want %q", finalPath, want)
	}
}

func TestResolveWorkspaceFolderBaseDirRejectsAFolderOutsideTheWorkspace(t *testing.T) {
	store, ws := newResultStorageTestWorkspace(t, "ws-storage-escape", "Storage Escape")

	for _, folder := range []string{"../outside", filepath.Join("reports", "..", "..", "outside")} {
		if _, _, err := ResolveWorkspaceFolderBaseDir(store, ws.ID, folder); err == nil {
			t.Fatalf("expected folder %q to be rejected", folder)
		}
	}
	if _, _, err := ResolveWorkspaceFolderBaseDir(nil, ws.ID, "reports"); err == nil {
		t.Fatal("expected a missing workspace store to be an error")
	}
}

func TestBuildFinalPathPreservesAnExternalBaseDir(t *testing.T) {
	baseDir := t.TempDir()

	finalPath, err := BuildFinalPath(baseDir, "summary.txt")
	if err != nil {
		t.Fatalf("BuildFinalPath: %v", err)
	}
	if want := filepath.Join(baseDir, "summary.txt"); finalPath != want {
		t.Fatalf("final path = %q, want %q", finalPath, want)
	}
}

func TestBuildFinalPathRejectsTraversalAndSymlinkEscape(t *testing.T) {
	store, ws := newResultStorageTestWorkspace(t, "ws-storage-secure", "Storage Secure")
	baseDir, _, err := ResolveWorkspaceFolderBaseDir(store, ws.ID, "")
	if err != nil {
		t.Fatalf("ResolveWorkspaceFolderBaseDir: %v", err)
	}

	if _, err := BuildFinalPath(baseDir, "../outside.txt"); err == nil {
		t.Fatal("expected traversal file path to be rejected")
	}

	outsideDir := t.TempDir()
	linkPath := filepath.Join(store.GetFilesPath(ws.ID), "escape")
	if err := os.Symlink(outsideDir, linkPath); err != nil {
		t.Skipf("symlink creation not supported: %v", err)
	}

	if _, err := BuildFinalPath(baseDir, filepath.Join("escape", "leak.txt")); err == nil {
		t.Fatal("expected symlink escape to be rejected")
	}
	if _, statErr := os.Stat(filepath.Join(outsideDir, "leak.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("expected no file outside the workspace, stat err=%v", statErr)
	}
}

func newResultStorageTestWorkspace(t *testing.T, id, name string) (*FileStore, *Workspace) {
	t.Helper()
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ws := newTestWorkspace(id, name)
	if err := store.Save(ws); err != nil {
		t.Fatalf("Save workspace: %v", err)
	}
	return store, ws
}
