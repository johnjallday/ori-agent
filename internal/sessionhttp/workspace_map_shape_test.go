package sessionhttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/johnjallday/ori-agent/internal/session"
	workspace "github.com/johnjallday/ori-agent/internal/workspace"
)

func TestWorkspaceTreeNestedFolderShapes(t *testing.T) {
	h, _, _, cleanup := templateTestEnv(t)
	defer cleanup()
	parent := "map-group"
	group := &session.Workspace{ID: parent, Name: "Map group", Kind: session.WorkspaceKindGroup}
	if err := h.store.CreateWorkspace(t.Context(), group); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		id   string
		data map[string]any
	}{
		{"managed", nil},
		{"anchored", map[string]any{workspace.AnchorDirectoryReferenceIDKey: "external"}},
		{"primary-link", map[string]any{workspace.PrimaryDirectoryIDKey: "external"}},
		{"imported", map[string]any{"folder_import": map[string]any{"enabled": true}}},
	} {
		ws := &workspace.Workspace{ID: tc.id, Name: tc.id, SharedData: tc.data,
			DirectoryReferences: []workspace.DirectoryReference{{ID: "external", Path: "/demo/" + tc.id}}}
		if err := h.workspaceStore.Save(ws); err != nil {
			t.Fatal(err)
		}
		row := session.ConvertAgentWorkspace(ws)
		row.ParentID = parent
		if err := h.store.CreateWorkspace(t.Context(), row); err != nil {
			t.Fatal(err)
		}
	}
	response := httptest.NewRecorder()
	h.HandleWorkspaces(response, httptest.NewRequest(http.MethodGet, "/api/workspaces?tree=true", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("tree: %d %s", response.Code, response.Body.String())
	}
	var body struct {
		Workspaces []struct {
			ID       string `json:"id"`
			Children []struct {
				ID    string `json:"id"`
				Shape string `json:"shape"`
			} `json:"children"`
		} `json:"workspaces"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Workspaces) != 1 || body.Workspaces[0].ID != parent || len(body.Workspaces[0].Children) != 4 {
		t.Fatalf("nested tree: %s", response.Body.String())
	}
	want := map[string]string{"managed": "managed", "anchored": "anchored", "primary-link": "managed", "imported": "imported"}
	for _, child := range body.Workspaces[0].Children {
		if child.Shape != want[child.ID] {
			t.Errorf("%s shape = %q, want %q", child.ID, child.Shape, want[child.ID])
		}
	}
}
