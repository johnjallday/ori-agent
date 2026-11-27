package projectlibrary

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestMapFolderRows_NestedMapMembersCountOnlyOnce(t *testing.T) {
	root := t.TempDir()
	scope := Scope{HomeID: "home"}
	state := &workspace.AssistantProgramState{LinkedProjectIDs: []string{"a", "b", "c"}}
	folder := func(id, parent, path string) *workspace.Workspace {
		ws := &workspace.Workspace{ID: id, Name: id, Kind: "group", ParentID: parent, SharedData: map[string]any{workspace.AnchorDirectoryReferenceIDKey: "ref"}}
		ws.DirectoryReferences = []workspace.DirectoryReference{{ID: "ref", Name: id, Path: path}}
		return ws
	}
	song := func(id, parent string) *workspace.Workspace {
		return &workspace.Workspace{ID: id, ParentID: parent, AssistantProjectLink: &workspace.AssistantProjectLink{StationWorkspaceID: "home"}}
	}
	members := map[string]*workspace.Workspace{"albums": folder("albums", "home", root), "drafts": folder("drafts", "albums", filepath.Join(root, "absent")), "a": song("a", "albums"), "b": song("b", "drafts"), "c": song("c", "home")}
	view := mapFolderRows(scope, members, state)
	if len(view.Folders) != 2 || view.Outside != 1 || view.Folders[0].Songs != 1 || view.Folders[1].Songs != 1 || !view.Folders[1].Missing {
		t.Fatalf("wrong folder totals: %+v", view)
	}
	encoded, err := json.Marshal(view)
	if err != nil || strings.Contains(string(encoded), root) {
		t.Fatalf("path in projection: %s %v", encoded, err)
	}
}
