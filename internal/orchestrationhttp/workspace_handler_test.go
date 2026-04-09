package orchestrationhttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestHandleGetWorkspaceIncludesSkillBindings(t *testing.T) {
	store := workspace.NewInMemoryStore()
	now := time.Now().UTC().Round(time.Second)

	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{
		Name: "Workspace",
	})
	ws.ID = "workspace-1"
	ws.CreatedAt = now
	ws.UpdatedAt = now
	ws.SkillBindings = []workspace.WorkspaceSkillBinding{
		{
			ID:        "binding-1",
			SkillName: "workspace-planning",
			Enabled:   true,
			Trusted:   true,
			CreatedAt: now,
			UpdatedAt: now,
		},
	}
	ws.AgentSkillAccess = []workspace.WorkspaceAgentSkillAccess{
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

	var response map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	skillBindings, ok := response["skill_bindings"].([]interface{})
	if !ok || len(skillBindings) != 1 {
		t.Fatalf("expected one skill binding in response, got %#v", response["skill_bindings"])
	}

	agentSkillAccess, ok := response["agent_skill_access"].([]interface{})
	if !ok || len(agentSkillAccess) != 1 {
		t.Fatalf("expected one agent skill access rule in response, got %#v", response["agent_skill_access"])
	}

	settings, ok := response["workspace_settings"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected workspace_settings object in response, got %#v", response["workspace_settings"])
	}
	if got := settings["preset"]; got != "guided" {
		t.Fatalf("expected default guided settings preset, got %#v", got)
	}

	effective, ok := response["workspace_settings_effective_behavior"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected workspace_settings_effective_behavior object, got %#v", response["workspace_settings_effective_behavior"])
	}
	if _, ok := effective["summary"].([]interface{}); !ok {
		t.Fatalf("expected summary array in effective behavior, got %#v", effective["summary"])
	}
}
