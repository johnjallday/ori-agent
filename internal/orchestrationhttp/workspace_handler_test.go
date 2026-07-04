package orchestrationhttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
