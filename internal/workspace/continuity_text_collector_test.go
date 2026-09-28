package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

func TestCollectContinuityTextFilesInventoriesAuthoredBytes(t *testing.T) {
	root := t.TempDir()
	if files, err := CollectContinuityTextFiles(t.Context(), root); err != nil || len(files) != 0 {
		t.Fatal("absent text owner was not empty", files, err)
	}
	memory, note := []byte("private remembered text"), []byte("private authored note")
	if err := os.WriteFile(filepath.Join(root, "MEMORY.md"), memory, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, NotesDir), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, NotesDir, "note.md"), note, 0600); err != nil {
		t.Fatal(err)
	}
	files, err := CollectContinuityTextFiles(t.Context(), root)
	if err != nil || len(files) != 2 {
		t.Fatal("authored text omitted", files, err)
	}
	if files[0] != (workspacecontinuity.Fingerprint{Path: "MEMORY.md", Digest: workspacecontinuity.Digest(memory), Bytes: int64(len(memory))}) ||
		files[1] != (workspacecontinuity.Fingerprint{Path: NotesDir + "/note.md", Digest: workspacecontinuity.Digest(note), Bytes: int64(len(note))}) {
		t.Fatal("source fingerprints changed", files)
	}
	if err := CheckContinuityTextCoverage(t.Context(), root, files); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, NotesDir, "unrepresentable"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := CollectContinuityTextFiles(t.Context(), root); !errors.Is(err, workspacecontinuity.ErrUnsafe) {
		t.Fatal("nested notes accepted", err)
	}
	if err := os.Remove(filepath.Join(root, NotesDir, "unrepresentable")); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		link := filepath.Join(root, NotesDir, "linked.md")
		if err := os.Symlink(filepath.Join(root, "MEMORY.md"), link); err != nil {
			t.Fatal(err)
		}
		if _, err := CollectContinuityTextFiles(t.Context(), root); !errors.Is(err, workspacecontinuity.ErrUnsafe) {
			t.Fatal("linked notes accepted", err)
		}
		if err := os.Remove(link); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(root, "MEMORY.md")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(root, NotesDir, "note.md"), filepath.Join(root, "MEMORY.md")); err != nil {
			t.Fatal(err)
		}
		if _, err := CollectContinuityTextFiles(t.Context(), root); !errors.Is(err, workspacecontinuity.ErrUnsafe) {
			t.Fatal("linked memory accepted", err)
		}
		if err := os.Remove(filepath.Join(root, "MEMORY.md")); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Truncate(filepath.Join(root, NotesDir, "note.md"), int64(workspacecontinuity.MaxChunkBytes)+1); err != nil {
		t.Fatal(err)
	}
	if _, err := CollectContinuityTextFiles(t.Context(), root); !errors.Is(err, workspacecontinuity.ErrLimit) {
		t.Fatal("oversized note accepted", err)
	}
}
