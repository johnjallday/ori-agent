package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestTrashReviewedManagedRefusesReplacedFolder(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("managed Trash requires a rename-based system Trash")
	}
	for _, replacement := range []string{"different workspace", "symlink"} {
		t.Run(replacement, func(t *testing.T) {
			st, err := NewFileStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = st.Close() }()
			ws := newTestWorkspace("hq", "My HQ")
			if err := st.Save(ws); err != nil {
				t.Fatal(err)
			}
			path, err := st.ReviewManagedTrash(ws.ID)
			if err != nil {
				t.Fatal(err)
			}
			original := filepath.Join(t.TempDir(), "original")
			if err := os.Rename(path, original); err != nil {
				t.Fatal(err)
			}
			if replacement == "symlink" {
				if err := os.Symlink(original, path); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Mkdir(path, 0o750); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(path, WorkspaceConfigFile), []byte(`{"id":"not-hq"}`), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if _, _, err := st.TrashReviewedManaged(ws.ID, path); !errors.Is(err, ErrManagedTrashUnavailable) {
				t.Fatalf("replacement was not refused: %v", err)
			}
			for _, kept := range []string{path, original} {
				if _, err := os.Lstat(kept); err != nil {
					t.Fatalf("refusal must leave %s untouched: %v", kept, err)
				}
			}
		})
	}
}
