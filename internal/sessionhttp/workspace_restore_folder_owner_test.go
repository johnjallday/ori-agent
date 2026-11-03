package sessionhttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/session"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// A trashed workspace no longer owns its project folder, so the folder can be
// set up again. Restoring the trashed one afterwards must refuse rather than
// bring back a second owner, and must refuse before anything leaves the Trash.
func TestRestoreRefusesWhenProjectFolderWasTakenWhileTrashed(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	fs, err := agentworkspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	handler.SetWorkspaceStore(fs)
	store := agentworkspace.NewSyncStore(session.NewWorkspaceStoreAdapter(handler.store), fs)
	handler.SetWorkspaceTaskStore(store)

	project := t.TempDir()
	trashedPath := filepath.Join(t.TempDir(), "never-touched")
	old := agentworkspace.NewWorkspace(agentworkspace.CreateWorkspaceParams{Name: "Album-2"})
	old.Status = agentworkspace.StatusTrashed
	old.SharedData = map[string]any{
		"folder_import": map[string]any{"enabled": true, "path": project},
		agentworkspace.TrashSharedDataKey: map[string]any{
			"original_path": filepath.Join(t.TempDir(), "album-2"),
			"trashed_path":  trashedPath,
		},
	}
	if err := store.Save(old); err != nil {
		t.Fatal(err)
	}
	again := agentworkspace.NewWorkspace(agentworkspace.CreateWorkspaceParams{Name: "Album-2 Again"})
	again.SharedData = map[string]any{"folder_import": map[string]any{"enabled": true, "path": project}}
	if err := store.Save(again); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	handler.HandleWorkspaces(w, httptest.NewRequest(http.MethodPost, "/api/workspaces/"+old.ID+"/restore", nil))
	if w.Code != http.StatusConflict {
		t.Fatalf("restore: got %d, want 409: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "Album-2 Again") {
		t.Fatalf("conflict does not name the new owner: %s", w.Body.String())
	}
	row, err := handler.store.GetWorkspace(context.Background(), old.ID)
	if err != nil || row.Status != session.WorkspaceStatusTrashed {
		t.Fatalf("refused restore changed the row: status=%v err=%v", row, err)
	}
}
