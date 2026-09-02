package sessionhttp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacesettings"
)

// seedMapSummaryFixture loads the given workspace from the folder-based file
// store, overwrites it with a fixed roster/tasks/tool-binding shape, and saves
// it back. This is the enrichment source hydrateWorkspaceMetadataInto reads
// from, independent of whatever the SQLite side already holds.
func seedMapSummaryFixture(t *testing.T, fileStore *agentworkspace.FileStore, workspaceID string) {
	t.Helper()

	ws, err := fileStore.Get(workspaceID)
	if err != nil {
		t.Fatalf("failed to load workspace %q from file store: %v", workspaceID, err)
	}

	settings := workspacesettings.DefaultSettings()
	settings.Workflow.Mode = "direct"

	ws.AgentInstances = []agentworkspace.AgentInstance{
		{Name: "Research Lead", EntryPoint: true},
		{Name: "Source Scout"},
	}
	ws.Tasks = []agentworkspace.Task{
		{ID: "t1", Status: agentworkspace.TaskStatusPending},
		{ID: "t2", Status: agentworkspace.TaskStatusInProgress},
		{ID: "t3", Status: agentworkspace.TaskStatusCompleted},
	}
	ws.MCPBindings = []agentworkspace.MCPBinding{{}, {}}
	ws.SkillBindings = []agentworkspace.SkillBinding{{}}
	ws.SharedData = workspacesettings.Store(map[string]any{}, settings)
	ws.SetTemplateProvenance(&agentworkspace.TemplateProvenance{
		TemplateID: "email-ops",
		Builtin:    true,
	})

	if err := fileStore.Save(ws); err != nil {
		t.Fatalf("failed to save fixture for workspace %q: %v", workspaceID, err)
	}
}

func assertMapSummaryFields(t *testing.T, entry map[string]any) {
	t.Helper()

	if got := entry["entry_agent_name"]; got != "Research Lead" {
		t.Errorf("entry_agent_name = %v, want %q", got, "Research Lead")
	}
	if got, ok := entry["agent_count"].(float64); !ok || got != 2 {
		t.Errorf("agent_count = %v, want 2", entry["agent_count"])
	}
	agents, ok := entry["agents"].([]any)
	if !ok || len(agents) != 2 {
		t.Errorf("agents = %v, want 2 entries", entry["agents"])
	}
	if got, ok := entry["open_task_count"].(float64); !ok || got != 2 {
		t.Errorf("open_task_count = %v, want 2 (pending + in_progress, excludes completed)", entry["open_task_count"])
	}
	if got, ok := entry["mcp_count"].(float64); !ok || got != 2 {
		t.Errorf("mcp_count = %v, want 2", entry["mcp_count"])
	}
	if got, ok := entry["skill_count"].(float64); !ok || got != 1 {
		t.Errorf("skill_count = %v, want 1", entry["skill_count"])
	}
	if got := entry["ops_mode"]; got != "direct" {
		t.Errorf("ops_mode = %v, want %q", got, "direct")
	}
	if got, ok := entry["active"].(bool); !ok || !got {
		t.Errorf("active = %v, want true (workspace has an in-progress task)", entry["active"])
	}
	if got := entry["blueprint_id"]; got != "email-ops" {
		t.Errorf("blueprint_id = %v, want %q", got, "email-ops")
	}
	if got, ok := entry["blueprint_builtin"].(bool); !ok || !got {
		t.Errorf("blueprint_builtin = %v, want true", entry["blueprint_builtin"])
	}
}

func TestListWorkspacesFlatIncludesMapSummaryFields(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	baseDir := t.TempDir()
	fileStore, err := agentworkspace.NewFileStore(baseDir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer func() { _ = fileStore.Close() }()
	handler.SetWorkspaceStore(fileStore)

	workspaceID := createTestWorkspace(t, handler, "Flat Summary")
	seedMapSummaryFixture(t, fileStore, workspaceID)

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces", nil)
	w := httptest.NewRecorder()
	handler.HandleWorkspaces(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Workspaces []map[string]any `json:"workspaces"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	var found map[string]any
	for _, entry := range resp.Workspaces {
		if entry["id"] == workspaceID {
			found = entry
			break
		}
	}
	if found == nil {
		t.Fatalf("workspace %q not found in flat list", workspaceID)
	}
	assertMapSummaryFields(t, found)
}

func TestListWorkspacesTreeIncludesMapSummaryFields(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	baseDir := t.TempDir()
	fileStore, err := agentworkspace.NewFileStore(baseDir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer func() { _ = fileStore.Close() }()
	handler.SetWorkspaceStore(fileStore)

	workspaceID := createTestWorkspace(t, handler, "Tree Summary")
	seedMapSummaryFixture(t, fileStore, workspaceID)

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces?tree=true", nil)
	w := httptest.NewRecorder()
	handler.HandleWorkspaces(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Workspaces []map[string]any `json:"workspaces"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	var found map[string]any
	for _, entry := range resp.Workspaces {
		if entry["id"] == workspaceID {
			found = entry
			break
		}
	}
	if found == nil {
		t.Fatalf("workspace %q not found in tree list", workspaceID)
	}
	assertMapSummaryFields(t, found)
}

func TestListWorkspacesGroupIncludesMapSummaryFields(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	baseDir := t.TempDir()
	fileStore, err := agentworkspace.NewFileStore(baseDir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer func() { _ = fileStore.Close() }()
	handler.SetWorkspaceStore(fileStore)

	groupID := createTestGroup(t, handler, "Group Summary")
	seedMapSummaryFixture(t, fileStore, groupID)

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces", nil)
	w := httptest.NewRecorder()
	handler.HandleWorkspaces(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Workspaces []map[string]any `json:"workspaces"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	var found map[string]any
	for _, entry := range resp.Workspaces {
		if entry["id"] == groupID {
			found = entry
			break
		}
	}
	if found == nil {
		t.Fatalf("group %q not found in flat list", groupID)
	}
	if got := found["kind"]; got != "group" {
		t.Fatalf("expected kind group, got %v", got)
	}
	assertMapSummaryFields(t, found)
}

func TestListWorkspacesTreeNestedChildIncludesMapSummaryFields(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	baseDir := t.TempDir()
	fileStore, err := agentworkspace.NewFileStore(baseDir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer func() { _ = fileStore.Close() }()
	handler.SetWorkspaceStore(fileStore)

	groupID := createTestGroup(t, handler, "Nested Parent")

	childBody := `{"name":"Nested Child","parent_id":"` + groupID + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces", bytes.NewBufferString(childBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.HandleWorkspaces(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("failed to create nested child: %d - %s", w.Code, w.Body.String())
	}
	var createResp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &createResp); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	childPayload := createResp["folder"].(map[string]any)
	childID := childPayload["id"].(string)

	seedMapSummaryFixture(t, fileStore, childID)

	treeReq := httptest.NewRequest(http.MethodGet, "/api/workspaces?tree=true", nil)
	treeW := httptest.NewRecorder()
	handler.HandleWorkspaces(treeW, treeReq)
	if treeW.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", treeW.Code, treeW.Body.String())
	}

	var treeResp struct {
		Workspaces []map[string]any `json:"workspaces"`
	}
	if err := json.Unmarshal(treeW.Body.Bytes(), &treeResp); err != nil {
		t.Fatalf("decode tree response: %v", err)
	}

	var parent map[string]any
	for _, entry := range treeResp.Workspaces {
		if entry["id"] == groupID {
			parent = entry
			break
		}
	}
	if parent == nil {
		t.Fatalf("parent group %q not found in tree", groupID)
	}

	children, ok := parent["children"].([]any)
	if !ok {
		t.Fatalf("expected children array on parent, got %#v", parent["children"])
	}

	var child map[string]any
	for _, c := range children {
		childMap, ok := c.(map[string]any)
		if ok && childMap["id"] == childID {
			child = childMap
			break
		}
	}
	if child == nil {
		t.Fatalf("nested child %q not found under parent %q", childID, groupID)
	}
	assertMapSummaryFields(t, child)
}
