package server

import (
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// unreadableStore lists its workspaces but cannot read one.
type unreadableStore struct{ *workspace.InMemoryStore }

func (unreadableStore) Get(string) (*workspace.Workspace, error) {
	return nil, errors.New("database is locked")
}

// A receipt's workspace is gone only when its record says it was trashed or the
// store lists every workspace without it; a record that cannot be read is
// unknown.
func TestFolderOutcomeWorkspace_SaysGoneOnlyWhenItKnows(t *testing.T) {
	store := workspace.NewInMemoryStore()
	for id, status := range map[string]workspace.WorkspaceStatus{"ws-kept": workspace.StatusActive, "ws-deleted": workspace.StatusTrashed} {
		ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: id})
		ws.ID, ws.FolderSlug, ws.Status = id, id+"-now", status
		if err := store.Save(ws); err != nil {
			t.Fatal(err)
		}
	}
	for name, tc := range map[string]struct {
		store workspace.Store
		id    string
		want  personalassistant.FolderOutcomeWorkspace
		read  bool
	}{
		"active":     {store, "ws-kept", personalassistant.FolderOutcomeWorkspace{Slug: "ws-kept-now"}, true},
		"trashed":    {store, "ws-deleted", personalassistant.FolderOutcomeWorkspace{Gone: personalassistant.FolderWorkspaceTrashed}, true},
		"removed":    {store, "ws-never", personalassistant.FolderOutcomeWorkspace{Gone: personalassistant.FolderWorkspaceRemoved}, true},
		"unreadable": {unreadableStore{store}, "ws-kept", personalassistant.FolderOutcomeWorkspace{}, false},
		"no store":   {nil, "ws-kept", personalassistant.FolderOutcomeWorkspace{}, false},
	} {
		got, read := folderOutcomeWorkspace(tc.store, tc.id)
		if got != tc.want || read != tc.read {
			t.Fatalf("%s: got %+v read %t, want %+v read %t", name, got, read, tc.want, tc.read)
		}
	}
}
