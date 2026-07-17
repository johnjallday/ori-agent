package orchestrationhttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacesettings"
)

// TestAddWorkspaceMapFields covers the map-summary keys via addWorkspaceMapFields,
// which now delegates to workspace.ComputeMapSummaryFields (see
// TestComputeMapSummaryFields in internal/workspace for the field-derivation
// logic itself); this test guards the summary-map wiring stays intact.
func TestAddWorkspaceMapFields(t *testing.T) {
	settings := workspacesettings.DefaultSettings()
	settings.Workflow.Mode = "direct"

	ws := &workspace.Workspace{
		Kind:     "group",
		ParentID: "parent-123",
		AgentInstances: []workspace.AgentInstance{
			{Name: "Research Lead", EntryPoint: true},
			{Name: "Source Scout"},
		},
		MCPBindings:   []workspace.MCPBinding{{}, {}},
		SkillBindings: []workspace.SkillBinding{{}},
		Tasks: []workspace.Task{
			{Status: workspace.TaskStatusPending},
			{Status: workspace.TaskStatusInProgress},
			{Status: workspace.TaskStatusCompleted},
		},
		SharedData: workspacesettings.Store(map[string]any{}, settings),
	}

	summary := map[string]any{}
	addWorkspaceMapFields(ws, summary)

	cases := map[string]any{
		"entry_agent_name": "Research Lead",
		"kind":             "group",
		"parent_id":        "parent-123",
		"mcp_count":        2,
		"skill_count":      1,
		"ops_mode":         "direct",
		"open_task_count":  2, // pending + in_progress, excludes completed
		"active":           true,
	}
	for key, want := range cases {
		if got := summary[key]; got != want {
			t.Errorf("summary[%q] = %v (%T), want %v", key, got, got, want)
		}
	}
}

func TestHandleGetWorkspaceIncludesSkillBindings(t *testing.T) {
	store := workspace.NewInMemoryStore()
	now := time.Now().UTC().Round(time.Second)

	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{
		Name: "Workspace",
	})
	ws.ID = "workspace-1"
	ws.CreatedAt = now
	ws.UpdatedAt = now
	ws.Tags = []string{"music", "reaper"}
	ws.SkillBindings = []workspace.SkillBinding{
		{
			ID:        "binding-1",
			SkillName: "workspace-planning",
			Enabled:   true,
			Trusted:   true,
			CreatedAt: now,
			UpdatedAt: now,
		},
	}
	ws.AgentSkillAccess = []workspace.AgentSkillAccess{
		{
			AgentInstanceID:   "agent-1",
			EnabledBindingIDs: []string{"binding-1"},
			UpdatedAt:         now,
		},
	}

	if err := store.Save(ws); err != nil {
		t.Fatalf("Save workspace: %v", err)
	}

	handler := NewWorkspaceHandler(nil, store, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/orchestration/workspace?id=workspace-1", nil)
	w := httptest.NewRecorder()

	handler.WorkspaceHandler(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var response map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	skillBindings, ok := response["skill_bindings"].([]any)
	if !ok || len(skillBindings) != 1 {
		t.Fatalf("expected one skill binding in response, got %#v", response["skill_bindings"])
	}

	agentSkillAccess, ok := response["agent_skill_access"].([]any)
	if !ok || len(agentSkillAccess) != 1 {
		t.Fatalf("expected one agent skill access rule in response, got %#v", response["agent_skill_access"])
	}

	settings, ok := response["workspace_settings"].(map[string]any)
	if !ok {
		t.Fatalf("expected workspace_settings object in response, got %#v", response["workspace_settings"])
	}
	if got := settings["preset"]; got != "guided" {
		t.Fatalf("expected default guided settings preset, got %#v", got)
	}

	effective, ok := response["workspace_settings_effective_behavior"].(map[string]any)
	if !ok {
		t.Fatalf("expected workspace_settings_effective_behavior object, got %#v", response["workspace_settings_effective_behavior"])
	}
	if _, ok := effective["summary"].([]any); !ok {
		t.Fatalf("expected summary array in effective behavior, got %#v", effective["summary"])
	}

	tags, ok := response["tags"].([]any)
	if !ok || len(tags) != 2 || tags[0] != "music" || tags[1] != "reaper" {
		t.Fatalf("expected workspace tags in response, got %#v", response["tags"])
	}
}

// TestHandleGetWorkspaceHydratesDesignationFromFolderStore guards the fix for
// a production bug: the real workspaceStore is a SQLite-primary SyncStore, and
// Designation has no SQLite column (FR3), so a plain Get always returns "".
// This test simulates that by using an in-memory store (which, like SQLite,
// carries no Designation for this workspace) as workspaceStore, with a
// separate *workspace.FileStore standing in for the canonical folder-store
// record — mirroring how production wires SetFolderStore.
func TestHandleGetWorkspaceHydratesDesignationFromFolderStore(t *testing.T) {
	store := workspace.NewInMemoryStore()
	now := time.Now().UTC().Round(time.Second)

	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "HQ Workspace"})
	ws.ID = "hq-workspace"
	ws.CreatedAt = now
	ws.UpdatedAt = now
	// Designation is deliberately unset here: the primary/SQLite-style store
	// never carries it.
	if err := store.Save(ws); err != nil {
		t.Fatalf("Save workspace: %v", err)
	}

	folderStore, err := workspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	folderWS := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "HQ Workspace"})
	folderWS.ID = "hq-workspace"
	folderWS.Designation = "personal_hq"
	if err := folderStore.Save(folderWS); err != nil {
		t.Fatalf("Save folder workspace: %v", err)
	}

	handler := NewWorkspaceHandler(nil, store, nil, nil)
	handler.SetFolderStore(folderStore)

	req := httptest.NewRequest(http.MethodGet, "/api/orchestration/workspace?id=hq-workspace", nil)
	w := httptest.NewRecorder()
	handler.WorkspaceHandler(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var response map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got := response["designation"]; got != "personal_hq" {
		t.Fatalf(`expected designation hydrated from folder store to "personal_hq", got %#v`, got)
	}
}

// TestHandleGetWorkspaceWithoutFolderStoreOmitsDesignation covers the
// degrade-gracefully path: no folder store wired means designation just stays
// empty rather than erroring.
func TestHandleGetWorkspaceWithoutFolderStoreOmitsDesignation(t *testing.T) {
	store := workspace.NewInMemoryStore()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Plain Workspace"})
	ws.ID = "plain-workspace"
	if err := store.Save(ws); err != nil {
		t.Fatalf("Save workspace: %v", err)
	}

	handler := NewWorkspaceHandler(nil, store, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/orchestration/workspace?id=plain-workspace", nil)
	w := httptest.NewRecorder()
	handler.WorkspaceHandler(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var response map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got := response["designation"]; got != "" {
		t.Fatalf("expected empty designation with no folder store wired, got %#v", got)
	}
}

// TestHandleGetWorkspaceListHydratesDesignation covers the list-summary path
// (no id query param), which shares the same SQLite-primary gap as the detail
// path.
func TestHandleGetWorkspaceListHydratesDesignation(t *testing.T) {
	store := workspace.NewInMemoryStore()

	hqWS := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "HQ Workspace"})
	hqWS.ID = "hq-workspace"
	if err := store.Save(hqWS); err != nil {
		t.Fatalf("Save HQ workspace: %v", err)
	}
	plainWS := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Plain Workspace"})
	plainWS.ID = "plain-workspace"
	if err := store.Save(plainWS); err != nil {
		t.Fatalf("Save plain workspace: %v", err)
	}

	folderStore, err := workspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	folderHQ := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "HQ Workspace"})
	folderHQ.ID = "hq-workspace"
	folderHQ.Designation = "personal_hq"
	if err := folderStore.Save(folderHQ); err != nil {
		t.Fatalf("Save folder HQ workspace: %v", err)
	}
	folderPlain := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Plain Workspace"})
	folderPlain.ID = "plain-workspace"
	if err := folderStore.Save(folderPlain); err != nil {
		t.Fatalf("Save folder plain workspace: %v", err)
	}

	handler := NewWorkspaceHandler(nil, store, nil, nil)
	handler.SetFolderStore(folderStore)

	req := httptest.NewRequest(http.MethodGet, "/api/orchestration/workspace", nil)
	w := httptest.NewRecorder()
	handler.WorkspaceHandler(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var response struct {
		Workspaces []map[string]any `json:"workspaces"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	var hqEntry, plainEntry map[string]any
	for _, entry := range response.Workspaces {
		switch entry["id"] {
		case "hq-workspace":
			hqEntry = entry
		case "plain-workspace":
			plainEntry = entry
		}
	}
	if hqEntry == nil || hqEntry["designation"] != "personal_hq" {
		t.Fatalf("expected hq-workspace summary designation = personal_hq, got %#v", hqEntry)
	}
	if plainEntry == nil || plainEntry["designation"] != "" {
		t.Fatalf("expected plain-workspace summary designation = empty, got %#v", plainEntry)
	}
}

// TestSaveStationLayoutHandlerRoundTrip covers FR5: station positions saved
// through the scoped endpoint must arrive in the orchestration workspace GET
// payload with no extra fetch.
func TestSaveStationLayoutHandlerRoundTrip(t *testing.T) {
	store := workspace.NewInMemoryStore()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "HQ Workspace"})
	ws.ID = "hq-workspace"
	if err := store.Save(ws); err != nil {
		t.Fatalf("Save workspace: %v", err)
	}

	handler := NewWorkspaceHandler(nil, store, workspace.NewEventBus(10, 10), nil)

	body := `{"workspace_id":"hq-workspace","station_positions":{"email":{"x":0.92,"y":0.15}}}`
	req := httptest.NewRequest(http.MethodPut, "/api/orchestration/workspace/station-layout", strings.NewReader(body))
	w := httptest.NewRecorder()
	handler.SaveStationLayoutHandler(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/orchestration/workspace?id=hq-workspace", nil)
	getW := httptest.NewRecorder()
	handler.WorkspaceHandler(getW, getReq)
	if getW.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", getW.Code, getW.Body.String())
	}

	var response map[string]any
	if err := json.Unmarshal(getW.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	layout, ok := response["layout"].(map[string]any)
	if !ok {
		t.Fatalf("expected layout object in response, got %#v", response["layout"])
	}
	stationPositions, ok := layout["station_positions"].(map[string]any)
	if !ok {
		t.Fatalf("expected station_positions in layout, got %#v", layout["station_positions"])
	}
	email, ok := stationPositions["email"].(map[string]any)
	if !ok || email["x"] != 0.92 || email["y"] != 0.15 {
		t.Fatalf("expected email station position {0.92, 0.15}, got %#v", stationPositions["email"])
	}
}

// TestSaveStationLayoutHandlerMissingWorkspace covers the 404 path.
func TestSaveStationLayoutHandlerMissingWorkspace(t *testing.T) {
	store := workspace.NewInMemoryStore()
	handler := NewWorkspaceHandler(nil, store, workspace.NewEventBus(10, 10), nil)

	body := `{"workspace_id":"does-not-exist","station_positions":{"email":{"x":0.5,"y":0.5}}}`
	req := httptest.NewRequest(http.MethodPut, "/api/orchestration/workspace/station-layout", strings.NewReader(body))
	w := httptest.NewRecorder()
	handler.SaveStationLayoutHandler(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

// TestSaveStationLayoutHandlerPreservesCanvasFields covers FR3/FR4 (one
// direction of the clobber regression): saving station positions must leave
// every canvas layout field untouched.
func TestSaveStationLayoutHandlerPreservesCanvasFields(t *testing.T) {
	store := workspace.NewInMemoryStore()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "HQ Workspace"})
	ws.ID = "hq-workspace"
	ws.Layout = &workspace.CanvasLayout{
		TaskPositions: map[string]workspace.Position{
			"task-1": {X: 400, Y: 300},
		},
		Scale: 1.5,
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("Save workspace: %v", err)
	}

	handler := NewWorkspaceHandler(nil, store, workspace.NewEventBus(10, 10), nil)

	body := `{"workspace_id":"hq-workspace","station_positions":{"email":{"x":0.92,"y":0.15}}}`
	req := httptest.NewRequest(http.MethodPut, "/api/orchestration/workspace/station-layout", strings.NewReader(body))
	w := httptest.NewRecorder()
	handler.SaveStationLayoutHandler(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	saved, err := store.Get("hq-workspace")
	if err != nil {
		t.Fatalf("Get workspace: %v", err)
	}
	if saved.Layout == nil {
		t.Fatalf("expected layout to survive station save")
	}
	if got := saved.Layout.TaskPositions["task-1"]; got.X != 400 || got.Y != 300 {
		t.Fatalf("expected canvas task position preserved, got %#v", got)
	}
	if saved.Layout.Scale != 1.5 {
		t.Fatalf("expected canvas scale preserved, got %v", saved.Layout.Scale)
	}
	if got := saved.Layout.StationPositions["email"]; got.X != 0.92 || got.Y != 0.15 {
		t.Fatalf("expected station position saved, got %#v", got)
	}
}

// TestSaveLayoutHandlerPreservesStationPositions covers the other direction
// of the clobber regression (FR4): saving canvas layout must leave station
// positions untouched, because SaveLayoutHandler's request struct never
// carries station_positions and it assigns fields in-place onto ws.Layout.
func TestSaveLayoutHandlerPreservesStationPositions(t *testing.T) {
	store := workspace.NewInMemoryStore()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "HQ Workspace"})
	ws.ID = "hq-workspace"
	ws.Layout = &workspace.CanvasLayout{
		StationPositions: map[string]workspace.Position{
			"email": {X: 0.92, Y: 0.15},
		},
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("Save workspace: %v", err)
	}

	handler := NewWorkspaceHandler(nil, store, workspace.NewEventBus(10, 10), nil)

	body := `{"workspace_id":"hq-workspace","task_positions":{"task-1":{"x":400,"y":300}},"scale":1.5}`
	req := httptest.NewRequest(http.MethodPut, "/api/orchestration/workspace/layout", strings.NewReader(body))
	w := httptest.NewRecorder()
	handler.SaveLayoutHandler(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	saved, err := store.Get("hq-workspace")
	if err != nil {
		t.Fatalf("Get workspace: %v", err)
	}
	if saved.Layout == nil {
		t.Fatalf("expected layout to survive canvas save")
	}
	if got := saved.Layout.TaskPositions["task-1"]; got.X != 400 || got.Y != 300 {
		t.Fatalf("expected canvas task position saved, got %#v", got)
	}
	if got := saved.Layout.StationPositions["email"]; got.X != 0.92 || got.Y != 0.15 {
		t.Fatalf("expected station position preserved across canvas save, got %#v", got)
	}
}
