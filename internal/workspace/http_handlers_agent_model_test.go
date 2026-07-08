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
		ID:             "ws-prof",
		Name:           "Profiles",
		Status:         StatusActive,
		AgentInstances: AgentInstancesFromNames("Manager", "Helper"),
		SharedData:     map[string]any{"entry_agent_name": "Manager"},
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
		ID:             "ws-upd",
		Name:           "Update",
		Status:         StatusActive,
		AgentInstances: AgentInstancesFromNames("ReaperDAW Manager"),
		SharedData:     map[string]any{"entry_agent_name": "ReaperDAW Manager"},
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

func TestHTTPHandler_WorkspaceAgentSystemPrompt_RoundTrip(t *testing.T) {
	store := NewInMemoryStore()
	handler := NewHTTPHandler(store, nil, nil)

	ws := &Workspace{
		ID:             "ws-sp",
		Name:           "Prompt",
		Status:         StatusActive,
		AgentInstances: AgentInstancesFromNames("Chief of Staff"),
		SharedData:     map[string]any{"entry_agent_name": "Chief of Staff"},
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := store.SaveWorkspaceAgent(ws.ID, "Chief of Staff", &agent.Agent{
		Type: "orchestration",
		Role: "orchestrator",
		Settings: types.Settings{
			Model:        "gpt-5.5",
			Provider:     "codex",
			SystemPrompt: "old prompt",
			Temperature:  0.3,
		},
	}); err != nil {
		t.Fatalf("save snapshot: %v", err)
	}

	// GET returns the current prompt.
	getReq := httptest.NewRequest(http.MethodGet, "/api/workspaces/ws-sp/agents/Chief%20of%20Staff/system-prompt", nil)
	getReq.SetPathValue("workspaceID", "ws-sp")
	getReq.SetPathValue("name", "Chief of Staff")
	getRec := httptest.NewRecorder()
	handler.GetWorkspaceAgentSystemPrompt(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get status=%d body=%s", getRec.Code, getRec.Body.String())
	}
	var getResp struct {
		SystemPrompt string `json:"system_prompt"`
	}
	if err := json.Unmarshal(getRec.Body.Bytes(), &getResp); err != nil {
		t.Fatalf("decode get: %v", err)
	}
	if getResp.SystemPrompt != "old prompt" {
		t.Fatalf("get system_prompt=%q, want %q", getResp.SystemPrompt, "old prompt")
	}

	// PATCH updates the prompt (and trims outer whitespace).
	body, _ := json.Marshal(map[string]string{"system_prompt": "  You are the chief of staff.  "})
	patchReq := httptest.NewRequest(http.MethodPatch, "/api/workspaces/ws-sp/agents/Chief%20of%20Staff/system-prompt", bytes.NewReader(body))
	patchReq.SetPathValue("workspaceID", "ws-sp")
	patchReq.SetPathValue("name", "Chief of Staff")
	patchRec := httptest.NewRecorder()
	handler.UpdateWorkspaceAgentSystemPrompt(patchRec, patchReq)
	if patchRec.Code != http.StatusOK {
		t.Fatalf("patch status=%d body=%s", patchRec.Code, patchRec.Body.String())
	}

	ag, ok, err := store.GetWorkspaceAgent(ws.ID, "Chief of Staff")
	if err != nil || !ok || ag == nil {
		t.Fatalf("reload agent: ok=%v err=%v", ok, err)
	}
	if ag.Settings.SystemPrompt != "You are the chief of staff." {
		t.Fatalf("system_prompt=%q, want trimmed value", ag.Settings.SystemPrompt)
	}
	// Model/provider/other settings must be preserved (prompt only).
	if ag.Settings.Model != "gpt-5.5" || ag.Settings.Provider != "codex" || ag.Settings.Temperature != 0.3 {
		t.Fatalf("other settings clobbered: %+v", ag.Settings)
	}
}

func TestHTTPHandler_UpdateWorkspaceAgentSystemPrompt_Validation(t *testing.T) {
	store := NewInMemoryStore()
	handler := NewHTTPHandler(store, nil, nil)

	ws := &Workspace{
		ID:             "ws-spval",
		Name:           "PromptVal",
		Status:         StatusActive,
		AgentInstances: AgentInstancesFromNames("Manager"),
		SharedData:     map[string]any{"entry_agent_name": "Manager"},
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

	// Missing system_prompt field -> 400.
	req := httptest.NewRequest(http.MethodPatch, "/api/workspaces/ws-spval/agents/Manager/system-prompt", bytes.NewReader([]byte(`{}`)))
	req.SetPathValue("workspaceID", "ws-spval")
	req.SetPathValue("name", "Manager")
	rec := httptest.NewRecorder()
	handler.UpdateWorkspaceAgentSystemPrompt(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing system_prompt: status=%d, want 400", rec.Code)
	}

	// Unknown agent -> 404.
	body, _ := json.Marshal(map[string]string{"system_prompt": "hi"})
	req = httptest.NewRequest(http.MethodPatch, "/api/workspaces/ws-spval/agents/Ghost/system-prompt", bytes.NewReader(body))
	req.SetPathValue("workspaceID", "ws-spval")
	req.SetPathValue("name", "Ghost")
	rec = httptest.NewRecorder()
	handler.UpdateWorkspaceAgentSystemPrompt(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown agent: status=%d, want 404", rec.Code)
	}
}

func TestHTTPHandler_UpdateWorkspaceAgentModel_Validation(t *testing.T) {
	store := NewInMemoryStore()
	handler := NewHTTPHandler(store, nil, nil)

	ws := &Workspace{
		ID:             "ws-val",
		Name:           "Val",
		Status:         StatusActive,
		AgentInstances: AgentInstancesFromNames("Manager"),
		SharedData:     map[string]any{"entry_agent_name": "Manager"},
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
