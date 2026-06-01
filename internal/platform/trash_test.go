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

func TestMoveToTrash_MovesDirectoryOffDisk(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skipf("system trash only implemented for darwin, not %s", runtime.GOOS)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home dir: %v", err)
	}

	// Create the source on the same volume as ~/.Trash so os.Rename succeeds.
	src, err := os.MkdirTemp(home, ".trash-test-*")
	if err != nil {
		t.Fatalf("mkdir temp: %v", err)
	}
	defer func() { _ = os.RemoveAll(src) }() // safety net if trashing failed

	if err := os.WriteFile(filepath.Join(src, "note.md"), []byte("hello"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	dest, err := MoveToTrash(src)
	if err != nil {
		t.Fatalf("MoveToTrash: %v", err)
	}
	// Clean up the trashed copy so the test doesn't litter the user's Trash.
	defer func() { _ = os.RemoveAll(dest) }()

	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("source should no longer exist after trashing, stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "note.md")); err != nil {
		t.Errorf("trashed contents should be intact at %q, got err = %v", dest, err)
	}
	if filepath.Dir(dest) != filepath.Join(home, ".Trash") {
		t.Errorf("expected item inside ~/.Trash, got %q", dest)
	}
}
