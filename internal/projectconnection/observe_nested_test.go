package projectconnection

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/pathselection"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// reparentedView reads the store as if some workspaces had other parents. The
// guarded store refuses to move a linked song, so this is how a test shows
// Observe a song that sits inside a folder group inside its Home.
type reparentedView struct {
	*workspace.SyncStore
	parents map[string]string
}

func (v reparentedView) Get(id string) (*workspace.Workspace, error) {
	ws, err := v.SyncStore.Get(id)
	if err == nil && ws != nil {
		if parent, ok := v.parents[id]; ok {
			ws.ParentID = parent
		}
	}
	return ws, err
}

func TestObserve_AcceptsASongInsideAFolderGroupInsideTheHome(t *testing.T) {
	service, store, _, _ := splitConnectionService(t)
	scope := Scope{OwnerUserID: "owner-1", RunID: "nested-song-run", Template: splitConnectionTemplate(t)}
	home, err := service.CreateHome(scope, "My Production")
	if err != nil || home.HomeID == "" {
		t.Fatalf("prepare Home: %+v %v", home, err)
	}
	folder := t.TempDir()
	if err := os.WriteFile(filepath.Join(folder, "Song.rpp"), []byte("project"), 0o600); err != nil {
		t.Fatal(err)
	}
	token, err := service.selections.(*pathselection.Store).Issue(folder)
	if err != nil {
		t.Fatal(err)
	}
	request := Request{ModeID: projecttemplates.ProjectConnectionExistingProject, SelectionToken: token, WorkspaceName: "Album-1"}
	preview, err := service.Preview(context.Background(), scope, request)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Commit(context.Background(), scope, request, preview.InputDigest, preview.OwnerDigest)
	if err != nil || result.HomeWorkspaceID != home.HomeID {
		t.Fatalf("connect: %+v %v", result, err)
	}
	albums := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Albums"})
	albums.Kind, albums.OwnerUserID, albums.ParentID = "group", "owner-1", home.HomeID
	if err := store.Save(albums); err != nil {
		t.Fatal(err)
	}
	mode := projecttemplates.ProjectConnectionExistingProject
	nested := NewService(reparentedView{SyncStore: store, parents: map[string]string{result.ProjectWorkspaceID: albums.ID}}, service.selections)
	if !nested.Observe(scope, home.HomeID, result.ProjectWorkspaceID, mode) {
		t.Fatal("a song inside a folder group inside its Home was not observed as connected")
	}
	strayed := NewService(reparentedView{SyncStore: store, parents: map[string]string{
		result.ProjectWorkspaceID: albums.ID, albums.ID: "",
	}}, service.selections)
	if strayed.Observe(scope, home.HomeID, result.ProjectWorkspaceID, mode) {
		t.Fatal("a song whose folder group left the Home was still observed as connected")
	}
}
