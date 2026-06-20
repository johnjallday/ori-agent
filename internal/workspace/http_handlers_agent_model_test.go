package workspace

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/types"
)

func TestHTTPHandler_ListWorkspaceAgentProfiles(t *testing.T) {
	store := NewInMemoryStore()
	handler := NewHTTPHandler(store, nil, nil)

	ws := &Workspace{
		ID:         "ws-prof",
		Name:       "Profiles",
		Status:     StatusActive,
		Agents:     []string{"Manager", "Helper"},
		SharedData: map[string]any{"entry_agent_name": "Manager"},
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("save: %v", err)
	}
	// Only Manager has an on-disk snapshot; Helper does not.
	if err := store.SaveWorkspaceAgent(ws.ID, "Manager", &agent.Agent{
		Type: "orchestration",
		Role: "orchestrator",
		Settings: types.Settings{
			Model:    "google/gemma-4-e4b",
			Provider: "lmstudio",
		},
	}); err != nil {
		t.Fatalf("save snapshot: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/ws-prof/agents", nil)
	rec := httptest.NewRecorder()
	handler.ListWorkspaceAgentProfiles(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		WorkspaceID string                  `json:"workspace_id"`
		Agents      []WorkspaceAgentProfile `json:"agents"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.WorkspaceID != "ws-prof" {
		t.Fatalf("workspace_id=%q", resp.WorkspaceID)
	}
	if len(resp.Agents) != 1 {
		t.Fatalf("agents=%v, want 1 (only Manager has a snapshot)", resp.Agents)
	}
	got := resp.Agents[0]
	if got.Name != "Manager" || got.Model != "google/gemma-4-e4b" ||
		got.Provider != "lmstudio" || got.Type != "orchestration" || got.Source != "workspace" {
		t.Fatalf("profile=%+v", got)
	}
}

func TestHTTPHandler_ListWorkspaceAgentProfiles_UnknownWorkspace(t *testing.T) {
	store := NewInMemoryStore()
	handler := NewHTTPHandler(store, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/nope/agents", nil)
	rec := httptest.NewRecorder()
	handler.ListWorkspaceAgentProfiles(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d, want 404", rec.Code)
	}
}

func TestHTTPHandler_UpdateWorkspaceAgentModel(t *testing.T) {
	store := NewInMemoryStore()
	handler := NewHTTPHandler(store, nil, nil)

	ws := &Workspace{
		ID:         "ws-upd",
		Name:       "Update",
		Status:     StatusActive,
		Agents:     []string{"ReaperDAW Manager"},
		SharedData: map[string]any{"entry_agent_name": "ReaperDAW Manager"},
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := store.SaveWorkspaceAgent(ws.ID, "ReaperDAW Manager", &agent.Agent{
		Type: "orchestration",
		Role: "orchestrator",
		Settings: types.Settings{
			Model:        "google/gemma-4-e4b",
			Provider:     "lmstudio",
			SystemPrompt: "keep me",
			Temperature:  0.2,
		},
	}); err != nil {
		t.Fatalf("save snapshot: %v", err)
	}

	body, _ := json.Marshal(map[string]string{
		"model":        "claude-opus-4",
		"llm_provider": "claude",
	})
	// Path uses %20 for the space; net/http decodes URL.Path before our handler.
	req := httptest.NewRequest(http.MethodPatch, "/api/workspaces/ws-upd/agents/ReaperDAW%20Manager", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.UpdateWorkspaceAgentModel(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	ag, ok, err := store.GetWorkspaceAgent(ws.ID, "ReaperDAW Manager")
	if err != nil || !ok || ag == nil {
		t.Fatalf("reload agent: ok=%v err=%v", ok, err)
	}
	if ag.Settings.Model != "claude-opus-4" || ag.Settings.Provider != "claude" {
		t.Fatalf("model=%q provider=%q, want claude-opus-4/claude", ag.Settings.Model, ag.Settings.Provider)
	}
	// Unrelated settings must be preserved (model+provider only).
	if ag.Settings.SystemPrompt != "keep me" || ag.Settings.Temperature != 0.2 {
		t.Fatalf("other settings clobbered: prompt=%q temp=%v", ag.Settings.SystemPrompt, ag.Settings.Temperature)
	}
}

func TestHTTPHandler_UpdateWorkspaceAgentModel_Validation(t *testing.T) {
	store := NewInMemoryStore()
	handler := NewHTTPHandler(store, nil, nil)

	ws := &Workspace{
		ID:         "ws-val",
		Name:       "Val",
		Status:     StatusActive,
		Agents:     []string{"Manager"},
		SharedData: map[string]any{"entry_agent_name": "Manager"},
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := store.SaveWorkspaceAgent(ws.ID, "Manager", &agent.Agent{
		Type:     "general",
		Settings: types.Settings{Model: "m", Provider: "p"},
	}); err != nil {
		t.Fatalf("save snapshot: %v", err)
	}

	// Missing model -> 400.
	body, _ := json.Marshal(map[string]string{"llm_provider": "claude"})
	req := httptest.NewRequest(http.MethodPatch, "/api/workspaces/ws-val/agents/Manager", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.UpdateWorkspaceAgentModel(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing model: status=%d, want 400", rec.Code)
	}

	// Unknown agent -> 404.
	body, _ = json.Marshal(map[string]string{"model": "m", "llm_provider": "p"})
	req = httptest.NewRequest(http.MethodPatch, "/api/workspaces/ws-val/agents/Ghost", bytes.NewReader(body))
	rec = httptest.NewRecorder()
	handler.UpdateWorkspaceAgentModel(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown agent: status=%d, want 404", rec.Code)
	}
}
