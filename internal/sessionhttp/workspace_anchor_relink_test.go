package sessionhttp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func relinkAnchorTest(t *testing.T, h *Handler, ws *workspace.Workspace, path string) *httptest.ResponseRecorder {
	t.Helper()
	ref, err := ws.GetDirectoryReference(ws.AnchorDirectoryReferenceID())
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"path": path, "directory_reference_id": ref.ID, "expected_path": ref.Path})
	recorder := httptest.NewRecorder()
	h.HandleWorkspaces(recorder, httptest.NewRequest(http.MethodPut, "/api/workspaces/"+ws.ID+"/anchor", bytes.NewReader(body)))
	return recorder
}

func TestAnchorDetailAndRelinkPreserveExplicitAuthority(t *testing.T) {
	for _, mode := range []string{"default", "removed", "disabled"} {
		t.Run(mode, func(t *testing.T) {
			h, _, store, cleanup := reviewedFileJanitorHandler(t)
			defer cleanup()
			root := canonicalAnchorTestDir(t)
			old := filepath.Join(root, "Original")
			next := filepath.Join(root, "Moved")
			if err := os.Mkdir(old, 0750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(old, "source.txt"), []byte("unchanged"), 0600); err != nil {
				t.Fatal(err)
			}
			body, _ := json.Marshal(map[string]string{"name": "Anchor", "anchor_path": old})
			res, payload := postCreateWorkspace(t, h, string(body))
			if res.Code != http.StatusCreated {
				t.Fatal(res.Body.String())
			}
			id := payload["folder"].(map[string]any)["id"].(string)
			if err := store.Update(id, func(ws *workspace.Workspace) error {
				if mode == "removed" {
					ws.MCPBindings = nil
				}
				if mode == "disabled" {
					ws.MCPBindings[0].Enabled = false
				}
				return ws.UpsertMCPBinding(workspace.MCPBinding{ID: "user", ServerName: "filesystem", Alias: "explicit-user-grant", Config: map[string]any{"roots": []string{old}}, AllowedTools: []string{"read_file"}})
			}); err != nil {
				t.Fatal(err)
			}
			before, _ := store.Get(id)
			if err := os.Rename(old, next); err != nil {
				t.Fatal(err)
			}
			row, _ := h.store.GetWorkspace(t.Context(), id)
			detail := h.buildWorkspaceDetailResponse(row)
			if detail["anchor"].(map[string]any)["missing"] != true || detail["sidecar_path"] == "" {
				t.Fatalf("missing projection: %+v", detail)
			}
			res = relinkAnchorTest(t, h, before, next)
			if res.Code != http.StatusOK {
				t.Fatalf("relink: %d %s", res.Code, res.Body.String())
			}
			after, _ := store.Get(id)
			if len(after.DirectoryReferences) != 1 || after.DirectoryReferences[0].Path != next || after.AnchorDirectoryReferenceID() != before.AnchorDirectoryReferenceID() || after.SharedData[workspace.PrimaryDirectoryIDKey] != before.AnchorDirectoryReferenceID() {
				t.Fatal("anchor identity changed")
			}
			if len(after.MCPBindings) != len(before.MCPBindings) {
				t.Fatal("relink restored removed access")
			}
			for i, binding := range after.MCPBindings {
				prior := before.MCPBindings[i]
				if binding.ID != prior.ID || binding.Enabled != prior.Enabled || !reflect.DeepEqual(binding.AllowedTools, prior.AllowedTools) {
					t.Fatal("relink widened authority")
				}
				want := old
				if binding.Alias == anchorFolderMCPAlias {
					want = next
				}
				if !reflect.DeepEqual(workspaceMCPBindingRoots(binding), []string{want}) {
					t.Fatalf("wrong roots: %+v", binding)
				}
			}
			if stale := relinkAnchorTest(t, h, before, next); stale.Code != http.StatusConflict {
				t.Fatal("accepted stale relink")
			}
			if data, err := os.ReadFile(filepath.Join(next, "source.txt")); err != nil || string(data) != "unchanged" {
				t.Fatal("source changed")
			}
			if _, err := os.Stat(old); !os.IsNotExist(err) {
				t.Fatal("missing anchor recreated")
			}
		})
	}
}

func TestJanitorAnchorRelinkKeepsOneReferenceAndInvalidatesOldConsent(t *testing.T) {
	f := newBuildPreparationFixture(t)
	res := f.call("janitor-prepare", f.review(t))
	if res.Code != http.StatusOK {
		t.Fatal(res.Body.String())
	}
	var result struct {
		WorkspaceID string `json:"workspace_id"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	before, _ := f.workspaces.Get(result.WorkspaceID)
	next := canonicalAnchorTestDir(t)
	res = relinkAnchorTest(t, f.h, before, next)
	if res.Code != http.StatusOK {
		t.Fatalf("relink: %d %s", res.Code, res.Body.String())
	}
	after, _ := f.workspaces.Get(result.WorkspaceID)
	assertAnchoredJanitorWorkspace(t, after, next)
	if before.AnchorDirectoryReferenceID() != after.AnchorDirectoryReferenceID() || before.MCPBindings[0].ID != after.MCPBindings[0].ID {
		t.Fatal("relink replaced stable resource IDs")
	}
	status, err := f.janitor.Status(result.WorkspaceID)
	if err != nil || status.Settings.RootPath != next || status.Settings.SetupDecision != nil || !status.Settings.AutomationApprovedAt.IsZero() {
		t.Fatalf("old authority survived: %+v %v", status.Settings, err)
	}
	if data, err := os.ReadFile(filepath.Join(f.root, "invoice.pdf")); err != nil || string(data) != "unchanged synthetic source" {
		t.Fatal("old source changed")
	}
}

func TestAnchorRelinkRefusesManagedSidecarAndOtherOwners(t *testing.T) {
	h, _, store, cleanup := reviewedFileJanitorHandler(t)
	defer cleanup()
	var workspaces []*workspace.Workspace
	for _, name := range []string{"First", "Second"} {
		body, _ := json.Marshal(map[string]string{"name": name, "anchor_path": canonicalAnchorTestDir(t)})
		res, payload := postCreateWorkspace(t, h, string(body))
		if res.Code != http.StatusCreated {
			t.Fatal(res.Body.String())
		}
		ws, _ := store.Get(payload["folder"].(map[string]any)["id"].(string))
		workspaces = append(workspaces, ws)
	}
	sidecar, _ := h.workspaceStore.GetFolderPath(workspaces[0].ID)
	for _, path := range []string{sidecar, workspaces[1].DirectoryReferences[0].Path} {
		res := relinkAnchorTest(t, h, workspaces[0], path)
		if res.Code != http.StatusBadRequest && res.Code != http.StatusConflict {
			t.Fatalf("accepted invalid root: %s", res.Body.String())
		}
		after, _ := store.Get(workspaces[0].ID)
		if !reflect.DeepEqual(workspaces[0].DirectoryReferences, after.DirectoryReferences) || !reflect.DeepEqual(workspaces[0].MCPBindings, after.MCPBindings) {
			t.Fatal("refusal changed authority")
		}
	}
}
