package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAnchoredSidecarSupportsContinuityAndBacklogWithoutFiles(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ws := NewWorkspace(CreateWorkspaceParams{Name: "Inbox"})
	ws.SharedData = map[string]any{AnchorDirectoryReferenceIDKey: "external", PrimaryDirectoryIDKey: "external"}
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}
	root, err := store.GetFolderPath(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	files, err := CollectContinuityOwnedFiles(t.Context(), root)
	if err != nil || len(files) != 0 {
		t.Fatalf("collect missing files: %v / %v", files, err)
	}
	if err := CheckContinuityFilesCoverage(t.Context(), root, files); err != nil {
		t.Fatalf("coverage: %v", err)
	}
	if _, err := NewFileBacklogSynchronizer(store).EnsureBacklogMarkdownFile(ws.ID); err != nil {
		t.Fatalf("backlog: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, BacklogMarkdownFileName)); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{FilesDir, NotesDir} {
		if _, err := os.Stat(filepath.Join(root, dir)); !os.IsNotExist(err) {
			t.Fatalf("%s scaffolded: %v", dir, err)
		}
	}
}
