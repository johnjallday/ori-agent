package sessionfiles

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResetOwnedUploadsRemovesManifestCopiesAndPreservesLinksAndUnknownFiles(t *testing.T) {
	root := t.TempDir()
	external := filepath.Join(t.TempDir(), "linked.txt")
	if err := os.WriteFile(external, []byte("keep linked bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddFileFromReader("session-1", strings.NewReader("owned"), "owned.txt", 5); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LinkFile("session-1", external, "linked.txt"); err != nil {
		t.Fatal(err)
	}
	unknown := filepath.Join(root, "session-1", "files", "unknown.txt")
	if err := os.WriteFile(unknown, []byte("keep unknown"), 0o600); err != nil {
		t.Fatal(err)
	}

	removed, err := ResetOwnedUploads(root)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	if _, err := os.Stat(filepath.Join(root, "session-1", "files", "owned.txt")); !os.IsNotExist(err) {
		t.Fatalf("owned upload remains: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(root, "session-1", "files", "linked.txt")); !os.IsNotExist(err) {
		t.Fatalf("linked registration remains: %v", err)
	}
	for _, path := range []string{external, unknown} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("retained file %s is unavailable: %v", filepath.Base(path), err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "session-1", "manifest.json")); !os.IsNotExist(err) {
		t.Fatalf("manifest registration remains: %v", err)
	}
}
