package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func newPlacementFixture(t *testing.T) (*workspace.FileStore, *workspace.Workspace, *workspace.Workspace, string) {
	t.Helper()
	store, err := workspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	home := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Production Home"})
	home.Kind, home.OwnerUserID = "group", "local"
	if err := store.Save(home); err != nil {
		t.Fatal(err)
	}
	anchored := t.TempDir()
	folder := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Albums"})
	folder.OwnerUserID = "local"
	folder.Mission = "Keep the albums tidy"
	if err := folder.AddDirectoryReference(workspace.DirectoryReference{ID: "anchor", Name: "Albums", Path: anchored}); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(folder); err != nil {
		t.Fatal(err)
	}
	return store, home, folder, anchored
}

// FR1, FR3: the Home step places the folder workspace and promotes it in
// place. Same id, references and goal; the anchored folder is untouched.
func TestPlaceFolderWorkspaceInHome_PromotesInPlace(t *testing.T) {
	store, home, folder, anchored := newPlacementFixture(t)
	if err := os.WriteFile(filepath.Join(anchored, "Album-1.rpp"), []byte("project"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := placeFolderWorkspaceInHome(store, folder.ID, home.ID); err != nil {
		t.Fatal(err)
	}
	placed, err := store.Get(folder.ID)
	if err != nil {
		t.Fatal(err)
	}
	if placed.ParentID != home.ID || placed.Kind != "group" || placed.Mission != folder.Mission {
		t.Fatalf("placed = parent %q kind %q mission %q", placed.ParentID, placed.Kind, placed.Mission)
	}
	if ref, err := placed.GetDirectoryReference("anchor"); err != nil || ref.Path != anchored {
		t.Fatalf("the anchor changed: %+v %v", ref, err)
	}
	if entries, err := os.ReadDir(anchored); err != nil || len(entries) != 1 || entries[0].Name() != "Album-1.rpp" {
		t.Fatalf("promotion touched the anchored folder: %v %v", entries, err)
	}
	// A second run of the same step is a no-op.
	if err := placeFolderWorkspaceInHome(store, folder.ID, home.ID); err != nil {
		t.Fatalf("placing again: %v", err)
	}
}

// A refused promotion (running work) still places the workspace: the Home
// step succeeds and the folder stays a plain workspace inside the Home.
func TestPlaceFolderWorkspaceInHome_RefusedPromotionStillPlaces(t *testing.T) {
	store, home, folder, _ := newPlacementFixture(t)
	if err := store.Update(folder.ID, func(ws *workspace.Workspace) error {
		ws.Tasks = append(ws.Tasks, workspace.Task{ID: "running", Description: "Sort", Status: workspace.TaskStatusInProgress})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := placeFolderWorkspaceInHome(store, folder.ID, home.ID); err != nil {
		t.Fatalf("a refused promotion failed the Home step: %v", err)
	}
	placed, err := store.Get(folder.ID)
	if err != nil {
		t.Fatal(err)
	}
	if placed.ParentID != home.ID || placed.Kind == "group" {
		t.Fatalf("placed = parent %q kind %q, want inside the Home and still a leaf", placed.ParentID, placed.Kind)
	}
}
