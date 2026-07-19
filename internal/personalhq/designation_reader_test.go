package personalhq

import (
	"context"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestIsWorkspaceDesignatedPersonalHQ_ReadsFolderStoreProjection(t *testing.T) {
	svc, _, _ := newTestHarness(t)
	folderStore, err := workspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	// Do not create a profile designation or a primary-store workspace. This
	// mirrors the SQLite-primary gap: the designation exists only in the
	// canonical workspace.json record.
	if err := folderStore.Save(&workspace.Workspace{
		ID:          "hq-folder-only",
		Name:        "HQ from disk",
		FolderSlug:  "hq-from-disk",
		Designation: "personal_hq",
	}); err != nil {
		t.Fatalf("save folder workspace: %v", err)
	}
	svc.SetDesignationReader(folderStore)

	got, err := svc.IsWorkspaceDesignatedPersonalHQ(context.Background(), "local", "hq-folder-only")
	if err != nil {
		t.Fatalf("IsWorkspaceDesignatedPersonalHQ: %v", err)
	}
	if !got {
		t.Fatal("expected designation resolved only from the folder store to be honored")
	}
}
