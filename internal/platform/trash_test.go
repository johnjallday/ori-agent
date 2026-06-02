package platform

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestMoveToTrash_EmptyPath(t *testing.T) {
	if _, err := MoveToTrash(""); err == nil {
		t.Fatal("expected error for empty path")
	}
}

func TestRestoreFromTrash_EmptyOriginal(t *testing.T) {
	if err := RestoreFromTrash("", "token"); err == nil {
		t.Fatal("expected error for empty original path")
	}
}

// TestTrashRoundTrip exercises MoveToTrash + RestoreFromTrash on platforms whose
// trash exposes a restorable path (macOS ~/.Trash, the FreeDesktop trash on
// Linux). Windows uses the Recycle Bin (no stable path), which can't be
// round-tripped deterministically in a unit test, so it is skipped.
func TestTrashRoundTrip(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skipf("path-based trash round trip not supported on %s", runtime.GOOS)
	}

	// Create the source under the home dir so the trash move (a rename into the
	// per-user trash) stays on a single volume.
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home dir: %v", err)
	}
	src, err := os.MkdirTemp(home, ".trash-test-*")
	if err != nil {
		t.Fatalf("mkdir temp: %v", err)
	}
	defer func() { _ = os.RemoveAll(src) }() // safety net if trashing failed

	if err := os.WriteFile(filepath.Join(src, "note.md"), []byte("hello"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	token, err := MoveToTrash(src)
	if err != nil {
		t.Fatalf("MoveToTrash: %v", err)
	}
	defer func() { _ = os.RemoveAll(token) }() // clean up the trashed copy

	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("source should no longer exist after trashing, stat err = %v", err)
	}
	if token == "" {
		t.Fatal("expected a restorable token (trashed path) on this platform")
	}
	if _, err := os.Stat(filepath.Join(token, "note.md")); err != nil {
		t.Errorf("trashed contents should be intact at %q, got err = %v", token, err)
	}

	// Restore it back to the original location.
	if err := RestoreFromTrash(src, token); err != nil {
		t.Fatalf("RestoreFromTrash: %v", err)
	}
	if _, err := os.Stat(filepath.Join(src, "note.md")); err != nil {
		t.Errorf("restored contents should be back at %q, got err = %v", src, err)
	}
}
