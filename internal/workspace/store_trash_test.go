package workspace

import (
	"os"
	"runtime"
	"testing"
)

// TestFileStore_TrashAndRestore verifies the soft-delete round trip: Trash moves
// a workspace's folder to the system trash and unregisters it, and
// RestoreFromTrash brings it back with its identity intact.
func TestFileStore_TrashAndRestore(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skipf("system trash only implemented for darwin, not %s", runtime.GOOS)
	}

	// Create the store under the home dir so the trash move (a rename into
	// ~/.Trash) stays on a single volume.
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home dir: %v", err)
	}
	dir, err := os.MkdirTemp(home, ".ws-trash-test-*")
	if err != nil {
		t.Fatalf("mkdir temp: %v", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	st, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	ws := newTestWorkspace("ws-trash-1", "Trash Me")
	if err := st.Save(ws); err != nil {
		t.Fatalf("Save: %v", err)
	}

	folder, err := st.GetFolderPath(ws.ID)
	if err != nil {
		t.Fatalf("GetFolderPath: %v", err)
	}
	if _, err := os.Stat(folder); err != nil {
		t.Fatalf("workspace folder should exist before trashing: %v", err)
	}

	// Trash.
	originalPath, trashedPath, err := st.Trash(ws.ID)
	if err != nil {
		t.Fatalf("Trash: %v", err)
	}
	defer func() { _ = os.RemoveAll(trashedPath) }() // clean up if restore fails

	if originalPath != folder {
		t.Errorf("originalPath = %q, want %q", originalPath, folder)
	}
	if trashedPath == "" {
		t.Fatal("expected a trashed path for an in-root workspace")
	}
	if _, err := os.Stat(folder); !os.IsNotExist(err) {
		t.Errorf("workspace folder should be gone after trashing, stat err = %v", err)
	}
	if _, err := os.Stat(trashedPath); err != nil {
		t.Errorf("trashed folder should exist at %q: %v", trashedPath, err)
	}
	if _, err := st.GetFolderPath(ws.ID); err == nil {
		t.Error("workspace should be unregistered from the store after trashing")
	}

	// Restore.
	restored, err := st.RestoreFromTrash(originalPath, trashedPath)
	if err != nil {
		t.Fatalf("RestoreFromTrash: %v", err)
	}
	if restored.ID != ws.ID {
		t.Errorf("restored ID = %q, want %q (identity should be preserved)", restored.ID, ws.ID)
	}
	if _, err := os.Stat(folder); err != nil {
		t.Errorf("workspace folder should be back after restore: %v", err)
	}
	if _, err := st.GetFolderPath(ws.ID); err != nil {
		t.Errorf("workspace should be re-registered after restore: %v", err)
	}
}
