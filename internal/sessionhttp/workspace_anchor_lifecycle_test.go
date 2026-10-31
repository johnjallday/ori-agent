package sessionhttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestNestedAnchorIsAllowedWithParentWorkspaceHint(t *testing.T) {
	h, _, _, cleanup := reviewedFileJanitorHandler(t)
	defer cleanup()
	root := canonicalAnchorTestDir(t)
	child := filepath.Join(root, "Invoices")
	if err := os.Mkdir(child, 0750); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"name": "Client", "anchor_path": root})
	res, payload := postCreateWorkspace(t, h, string(body))
	if res.Code != http.StatusCreated {
		t.Fatal(res.Body.String())
	}
	id := payload["folder"].(map[string]any)["id"].(string)
	check := httptest.NewRecorder()
	h.HandleWorkspaces(check, httptest.NewRequest(http.MethodGet, "/api/workspaces/anchor/check?path="+url.QueryEscape(child), nil))
	var result map[string]any
	if err := json.Unmarshal(check.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if check.Code != http.StatusOK || result["related_workspace"].(map[string]any)["workspace_id"] != id {
		t.Fatalf("missing parent hint: %s", check.Body.String())
	}
	body, _ = json.Marshal(map[string]string{"name": "Invoices", "anchor_path": child})
	res, _ = postCreateWorkspace(t, h, string(body))
	if res.Code != http.StatusCreated {
		t.Fatalf("nested anchor refused: %s", res.Body.String())
	}
}

func TestAnchorRenameAndGroupMovesOnlyMoveTheSidecar(t *testing.T) {
	h, store := newUpdateRenameHandler(t, t.TempDir())
	anchor := canonicalAnchorTestDir(t)
	if err := os.WriteFile(filepath.Join(anchor, "source.txt"), []byte("original bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"name": "Client", "anchor_path": anchor})
	response, payload := postCreateWorkspace(t, h, string(body))
	if response.Code != http.StatusCreated {
		t.Fatal(response.Body.String())
	}
	id := payload["folder"].(map[string]any)["id"].(string)
	before, _ := store.Get(id)
	oldSidecar, _ := h.workspaceStore.GetFolderPath(id)
	patchWorkspaceName(t, h, id, "Renamed client", http.StatusOK)
	group := createTestGroup(t, h, "Agency")
	patchWorkspaceParent(t, h, id, group, http.StatusOK)
	groupPath, _ := h.workspaceStore.GetFolderPath(group)
	nested, _ := h.workspaceStore.GetFolderPath(id)
	if nested != filepath.Join(groupPath, workspace.SubWorkspacesDir, "renamed-client") {
		t.Fatalf("wrong nested sidecar: %s", nested)
	}
	patchWorkspaceParent(t, h, id, "", http.StatusOK)
	if err := h.workspaceStore.Reload(); err != nil {
		t.Fatal(err)
	}
	after, err := h.workspaceStore.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if after.Shape() != "anchored" || after.AnchorDirectoryReferenceID() != before.AnchorDirectoryReferenceID() || !reflect.DeepEqual(after.DirectoryReferences, before.DirectoryReferences) || !reflect.DeepEqual(after.MCPBindings, before.MCPBindings) {
		t.Fatalf("anchor authority changed: %+v %+v", after.DirectoryReferences, after.MCPBindings)
	}
	if _, err := os.Stat(oldSidecar); !os.IsNotExist(err) {
		t.Fatal("old sidecar remains")
	}
	if data, err := os.ReadFile(filepath.Join(anchor, "source.txt")); err != nil || string(data) != "original bytes" {
		t.Fatal("anchor changed")
	}
	current, _ := h.workspaceStore.GetFolderPath(id)
	for _, name := range []string{workspace.FilesDir, workspace.NotesDir} {
		if _, err := os.Stat(filepath.Join(current, name)); !os.IsNotExist(err) {
			t.Fatalf("move scaffolded %s", name)
		}
	}
}
