package sessionhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/session"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

type anchorChangedOnCreateStore struct {
	session.HybridStore
	afterCreate func()
}

func (s anchorChangedOnCreateStore) CreateWorkspace(ctx context.Context, ws *session.Workspace) error {
	if err := s.HybridStore.CreateWorkspace(ctx, ws); err != nil {
		return err
	}
	s.afterCreate()
	return nil
}

func TestAnchorFinalValidationRollsBackWorkspaceAndSeededAgents(t *testing.T) {
	h, _, _, cleanup := templateTestEnv(t)
	defer cleanup()
	anchor := canonicalAnchorTestDir(t)
	writeRosterTemplate(t, h, "anchor-roster", `{"name":"Anchor roster","agents":[{"name":"Anchor Lead","role":"orchestrator"}]}`)
	h.store = anchorChangedOnCreateStore{HybridStore: h.store, afterCreate: func() {
		if err := os.WriteFile(filepath.Join(anchor, "workspace.json"), []byte(`{"id":"claimed","name":"Claimed in another window"}`), 0600); err != nil {
			t.Fatal(err)
		}
	}}
	body, err := json.Marshal(map[string]any{"name": "Race loser", "template_id": "anchor-roster", "anchor_path": anchor})
	if err != nil {
		t.Fatal(err)
	}
	response, payload := postCreateWorkspace(t, h, string(body))
	if response.Code != http.StatusConflict || payload["code"] != "anchor_contains_workspace" {
		t.Fatalf("late refusal: %d %s", response.Code, response.Body.String())
	}
	rows, err := h.store.ListWorkspaces(t.Context())
	if err != nil || len(rows) != 0 {
		t.Fatalf("left records: %+v / %v", rows, err)
	}
	assertAgentAbsent(t, h, "Anchor Lead")
	if _, err := os.Stat(filepath.Join(anchor, "workspace.json")); err != nil {
		t.Fatal("rollback touched anchor")
	}
}

func canonicalAnchorTestDir(t *testing.T) string {
	t.Helper()
	path, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCreateAnchoredWorkspace(t *testing.T) {
	h, _, _, cleanup := templateTestEnv(t)
	defer cleanup()
	anchor := canonicalAnchorTestDir(t)
	body, err := json.Marshal(map[string]any{"name": "Anchored inbox", "anchor_path": anchor})
	if err != nil {
		t.Fatal(err)
	}
	response, payload := postCreateWorkspace(t, h, string(body))
	if response.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", response.Code, response.Body.String())
	}
	created := payload["folder"].(map[string]any)
	if created["shape"] != "anchored" {
		t.Fatalf("shape: %v", created["shape"])
	}
	id := created["id"].(string)
	ws, err := h.workspaceStore.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(ws.DirectoryReferences) != 1 || ws.DirectoryReferences[0].WorkspaceID != id || ws.DirectoryReferences[0].Path != anchor || ws.AnchorDirectoryReferenceID() != ws.DirectoryReferences[0].ID || ws.SharedData[agentworkspace.PrimaryDirectoryIDKey] != ws.AnchorDirectoryReferenceID() {
		t.Fatalf("anchor reference: %+v / %+v", ws.DirectoryReferences, ws.SharedData)
	}
	if len(ws.MCPBindings) != 1 {
		t.Fatalf("bindings: %+v", ws.MCPBindings)
	}
	binding := ws.MCPBindings[0]
	if binding.Alias != "anchor-folder" || binding.ServerName != "filesystem" || binding.DefaultSideEffect != agentworkspace.SideEffectRead || !binding.Enabled {
		t.Fatalf("binding: %+v", binding)
	}
	want := []string{"directory_tree", "get_file_info", "list_allowed_directories", "list_directory", "list_directory_with_sizes", "read_file", "read_media_file", "read_multiple_files", "read_text_file", "search_files"}
	got := slices.Clone(binding.AllowedTools)
	slices.Sort(got)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tools = %v, want %v", got, want)
	}
	for _, forbidden := range []string{"write_file", "edit_file", "create_directory", "move_file", "delete_file", "copy_file"} {
		if slices.Contains(got, forbidden) {
			t.Fatalf("mutation tool granted: %s", forbidden)
		}
	}
	roots := binding.Config["roots"].([]any)
	if len(roots) != 1 || roots[0] != anchor {
		t.Fatalf("roots = %v", roots)
	}
	folder, err := h.workspaceStore.GetFolderPath(id)
	if err != nil {
		t.Fatal(err)
	}
	// Repeated saves and a rescan must not backfill managed scaffolding.
	if err := h.workspaceStore.Save(ws); err != nil {
		t.Fatal(err)
	}
	if err := h.workspaceStore.Reload(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"files", "notes"} {
		if _, err := os.Stat(filepath.Join(folder, name)); !os.IsNotExist(err) {
			t.Fatalf("%s exists or stat failed: %v", name, err)
		}
	}
	entries, err := os.ReadDir(anchor)
	if err != nil || len(entries) != 0 {
		t.Fatalf("anchor was written: %v / %v", entries, err)
	}
	// A second create cannot claim the same root and leaves no extra record.
	body, _ = json.Marshal(map[string]any{"name": "Duplicate", "anchor_path": anchor})
	response, payload = postCreateWorkspace(t, h, string(body))
	if response.Code != http.StatusConflict || payload["code"] != "anchor_already_owned" {
		t.Fatalf("duplicate: %d %s", response.Code, response.Body.String())
	}
	if payload["duplicate"].(map[string]any)["workspace_id"] != id {
		t.Fatalf("duplicate owner: %v", payload)
	}
	rows, err := h.store.ListWorkspaces(context.Background())
	if err != nil || len(rows) != 1 {
		t.Fatalf("duplicate left records: %d / %v", len(rows), err)
	}
}

func TestAnchoredTemplateDoesNotScaffoldASecondWorkFolder(t *testing.T) {
	h, _, _, cleanup := templateTestEnv(t)
	defer cleanup()
	path := canonicalAnchorTestDir(t)
	body, err := json.Marshal(map[string]any{"name": "Existing client", "anchor_path": path, "template_id": "demo-template"})
	if err != nil {
		t.Fatal(err)
	}
	response, payload := postCreateWorkspace(t, h, string(body))
	if response.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", response.Code, response.Body.String())
	}
	id := payload["folder"].(map[string]any)["id"].(string)
	ws, err := h.workspaceStore.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(ws.DirectoryReferences) != 1 || ws.DirectoryReferences[0].Path != path || ws.ProjectPath != "" || ws.SharedData[agentworkspace.PrimaryDirectoryIDKey] != ws.AnchorDirectoryReferenceID() {
		t.Fatalf("template replaced the anchor: %+v / %+v", ws.DirectoryReferences, ws.SharedData)
	}
}

func TestAnchorChooserCheckIsReadOnly(t *testing.T) {
	h, _, _, cleanup := templateTestEnv(t)
	defer cleanup()
	path := canonicalAnchorTestDir(t)
	response := httptest.NewRecorder()
	h.HandleWorkspaces(response, httptest.NewRequest(http.MethodGet, "/api/workspaces/anchor/check?path="+url.QueryEscape(path), nil))
	if response.Code != http.StatusOK {
		t.Fatalf("check: %d %s", response.Code, response.Body.String())
	}
	var payload struct {
		Success bool   `json:"success"`
		Path    string `json:"normalized_path"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.Success || payload.Path != path {
		t.Fatalf("check result: %+v", payload)
	}
	rows, err := h.store.ListWorkspaces(t.Context())
	if err != nil || len(rows) != 0 {
		t.Fatalf("check created a workspace: %+v / %v", rows, err)
	}
	entries, err := os.ReadDir(path)
	if err != nil || len(entries) != 0 {
		t.Fatalf("check wrote to folder: %v / %v", entries, err)
	}
}

func TestCreateAnchorRejections(t *testing.T) {
	for _, tc := range []struct {
		name, code string
		setup      func(*testing.T, *Handler) string
	}{
		{"missing", "anchor_not_directory", func(t *testing.T, _ *Handler) string { return filepath.Join(canonicalAnchorTestDir(t), "missing") }},
		{"file", "anchor_not_directory", func(t *testing.T, _ *Handler) string {
			path := filepath.Join(canonicalAnchorTestDir(t), "file")
			if err := os.WriteFile(path, nil, 0600); err != nil {
				t.Fatal(err)
			}
			return path
		}},
		{"workspace-root", "anchor_inside_workspace_root", func(t *testing.T, h *Handler) string {
			path, err := filepath.EvalSymlinks(h.workspaceStore.BasePath())
			if err != nil {
				t.Fatal(err)
			}
			return path
		}},
		{"workspace-child", "anchor_inside_workspace_root", func(t *testing.T, h *Handler) string {
			root, err := filepath.EvalSymlinks(h.workspaceStore.BasePath())
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "child")
			if err := os.Mkdir(path, 0750); err != nil {
				t.Fatal(err)
			}
			return path
		}},
		{"workspace-config", "anchor_contains_workspace", func(t *testing.T, _ *Handler) string {
			path := canonicalAnchorTestDir(t)
			if err := os.WriteFile(filepath.Join(path, "workspace.json"), []byte(`{"id":"owned","name":"Existing"}`), 0600); err != nil {
				t.Fatal(err)
			}
			return path
		}},
		{"symlink", "anchor_symlink", func(t *testing.T, _ *Handler) string {
			path := filepath.Join(canonicalAnchorTestDir(t), "link")
			if err := os.Symlink(canonicalAnchorTestDir(t), path); err != nil {
				t.Fatal(err)
			}
			return path
		}},
		{"project-owner", "anchor_already_owned", func(t *testing.T, h *Handler) string {
			path := canonicalAnchorTestDir(t)
			ws := &agentworkspace.Workspace{ID: "owner", Name: "Existing project", SharedData: map[string]any{}, DirectoryReferences: []agentworkspace.DirectoryReference{{ID: "project", Path: path}}}
			if err := agentworkspace.SetProjectEntryLocator(ws.SharedData, agentworkspace.ProjectEntryLocator{SchemaVersion: agentworkspace.ProjectEntryLocatorSchemaVersion, Kind: agentworkspace.ProjectEntryDirectoryReference, DirectoryReferenceID: "project", RelativePath: "project.txt"}); err != nil {
				t.Fatal(err)
			}
			if err := h.workspaceStore.Save(ws); err != nil {
				t.Fatal(err)
			}
			return path
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, _, _, cleanup := templateTestEnv(t)
			defer cleanup()
			path := tc.setup(t, h)
			body, err := json.Marshal(map[string]any{"name": "Refused", "anchor_path": path})
			if err != nil {
				t.Fatal(err)
			}
			response, payload := postCreateWorkspace(t, h, string(body))
			if response.Code != http.StatusBadRequest && response.Code != http.StatusConflict {
				t.Fatalf("status: %d %s", response.Code, response.Body.String())
			}
			if payload["code"] != tc.code {
				t.Fatalf("code: %v want %s", payload, tc.code)
			}
			rows, err := h.store.ListWorkspaces(context.Background())
			if err != nil || len(rows) != 0 {
				t.Fatalf("refusal left records: %v / %v", rows, err)
			}
		})
	}
}

func TestAnchorRebaseAndBackfillPreserveExternalAccess(t *testing.T) {
	h, _, _, cleanup := templateTestEnv(t)
	defer cleanup()
	ws := &session.Workspace{ID: "anchored", Name: "Inbox", FolderSlug: "inbox"}
	if err := provisionAnchoredScaffolding(ws, canonicalAnchorTestDir(t)); err != nil {
		t.Fatal(err)
	}
	// Same names used to trigger the name-keyed rebind to the sidecar.
	refs, err := decodeDirectoryReferences(ws.DirectoryReferencesJSON)
	if err != nil {
		t.Fatal(err)
	}
	refs[0].Name = "inbox"
	ws.DirectoryReferencesJSON, err = json.Marshal(refs)
	if err != nil {
		t.Fatal(err)
	}
	beforeRefs, beforeBindings := string(ws.DirectoryReferencesJSON), string(ws.MCPBindingsJSON)
	if err := updateManagedWorkspaceReferences(ws, "/workspaces/inbox", "/workspaces/home/sub-workspaces/inbox"); err != nil {
		t.Fatal(err)
	}
	if string(ws.DirectoryReferencesJSON) != beforeRefs || string(ws.MCPBindingsJSON) != beforeBindings {
		t.Fatal("rebase changed anchor references/bindings")
	}
	changed, err := h.backfillGroupScaffolding(context.Background(), ws)
	if err != nil || changed {
		t.Fatalf("backfill changed non-group anchor: %v / %v", changed, err)
	}
	bindings, err := decodeWorkspaceMCPBindings(ws.MCPBindingsJSON)
	if err != nil {
		t.Fatal(err)
	}
	_, rebound, _ := rebaseWorkspaceFolderReferences(folderRebaseIdentity{isAnchored: true}, refs, bindings[:0], "/old", "/new", time.Now())
	if len(rebound) != 0 {
		t.Fatalf("rebase restored removed bindings: %+v", rebound)
	}
}

func TestAnchorPersistenceRoundTrip(t *testing.T) {
	h, cleanup := createTestHandler(t)
	defer cleanup()
	files, err := agentworkspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ws := &session.Workspace{ID: "anchor-round-trip", Name: "Inbox", Status: session.WorkspaceStatusActive}
	setWorkspaceAnchorDirectoryID(ws, "external-folder")
	ws.DirectoryReferencesJSON = json.RawMessage(`[{"id":"external-folder","path":"/missing-on-this-machine"}]`)
	folder, err := buildFileStoreWorkspace(ws)
	if err != nil {
		t.Fatal(err)
	}
	if err := files.Save(folder); err != nil {
		t.Fatal(err)
	}
	if err := files.Reload(); err != nil {
		t.Fatal(err)
	}
	loaded, err := files.Get(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.IsAnchored() || loaded.AnchorDirectoryReferenceID() != "external-folder" {
		t.Fatalf("anchor lost on disk: %+v", loaded.SharedData)
	}
	converted := session.ConvertAgentWorkspace(loaded)
	// Use the actual SQLite cache, not only the in-memory hybrid layer.
	sqlite := session.NewSQLiteStore(h.store.DB())
	if err := sqlite.CreateWorkspace(context.Background(), converted); err != nil {
		t.Fatal(err)
	}
	cached, err := sqlite.GetWorkspace(context.Background(), ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	listed, err := sqlite.ListWorkspaces(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].Shape() != "anchored" || listed[0].SharedData != nil {
		t.Fatalf("SQLite summary lost shape or loaded heavy data: %+v", listed)
	}
	if workspaceAnchorDirectoryID(cached) != "external-folder" || workspacePrimaryDirectoryID(cached) != "external-folder" {
		t.Fatalf("anchor/primary lost in SQLite: %+v", cached.SharedData)
	}
	rebuilt, err := buildFileStoreWorkspace(cached)
	if err != nil {
		t.Fatal(err)
	}
	if !rebuilt.IsAnchored() {
		t.Fatal("anchor lost on SQLite -> file conversion")
	}
}

func TestWorkspaceShapeResponses(t *testing.T) {
	h := &Handler{}
	for _, tc := range []struct {
		name string
		data map[string]any
		want string
	}{
		{"legacy", nil, "managed"},
		{"legacy-primary", map[string]any{"primary_directory_id": "folder"}, "managed"},
		{"anchored", map[string]any{agentworkspace.AnchorDirectoryReferenceIDKey: "folder"}, "anchored"},
		{"imported", map[string]any{"folder_import": map[string]any{"enabled": true}}, "imported"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ws := &session.Workspace{ID: tc.name, SharedData: tc.data}
			if got := h.buildWorkspaceDetailResponse(ws)["shape"]; got != tc.want {
				t.Fatalf("detail shape = %v", got)
			}
			data, err := json.Marshal([]session.Workspace{*ws})
			if err != nil {
				t.Fatal(err)
			}
			var listed []map[string]any
			if err := json.Unmarshal(data, &listed); err != nil {
				t.Fatal(err)
			}
			if got := listed[0]["shape"]; got != tc.want {
				t.Fatalf("list shape = %v", got)
			}
		})
	}
}
