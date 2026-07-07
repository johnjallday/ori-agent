package sessionhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/session"
	agentstore "github.com/johnjallday/ori-agent/internal/store"
)

// patchInstanceSettings issues a PATCH to UpdateWorkspaceAgentInstanceSettings with
// the given path values and JSON body. The URL path is escaped (agent names can
// contain spaces) while SetPathValue carries the raw, already-decoded name that
// the Go 1.22 mux would supply.
func patchInstanceSettings(h *Handler, workspaceID, agentName, body string) *httptest.ResponseRecorder {
	target := "/api/workspaces/" + url.PathEscape(workspaceID) + "/agents/" + url.PathEscape(agentName) + "/instance-settings"
	req := httptest.NewRequest(http.MethodPatch, target, strings.NewReader(body))
	req.SetPathValue("workspaceID", workspaceID)
	req.SetPathValue("name", agentName)
	rr := httptest.NewRecorder()
	h.UpdateWorkspaceAgentInstanceSettings(rr, req)
	return rr
}

func seedWorkspaceWithInstance(t *testing.T, h *Handler, wsID, agentName string) {
	t.Helper()
	ws := &session.Workspace{
		ID:   wsID,
		Name: wsID,
		AgentInstances: []session.AgentInstance{
			{ID: "inst-1", Name: agentName, InstanceNumber: 1, NodeID: agentName + "-node-1", EntryPoint: true},
		},
	}
	if err := h.store.CreateWorkspace(context.Background(), ws); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
}

// TestUpdateWorkspaceAgentInstanceSettings_UpdatesInstanceOnly verifies role,
// description, and custom_instructions are persisted on the AgentInstance (and
// survive a store reload), without touching the global agent definition (FR18).
func TestUpdateWorkspaceAgentInstanceSettings_UpdatesInstanceOnly(t *testing.T) {
	h, cleanup := createTestHandler(t)
	defer cleanup()
	seedWorkspaceWithInstance(t, h, "ws-1", "Brand Copywriter")

	const custom = "Favor short-form social copy.\nKeep it punchy."
	// Raw string literal so the JSON body contains an escaped \n and padded outer
	// whitespace, which the handler trims (outer) while preserving the newline.
	body := `{"role":"Voice keeper","description":"Owns tone","custom_instructions":"  Favor short-form social copy.\nKeep it punchy.  "}`
	rr := patchInstanceSettings(h, "ws-1", "Brand Copywriter", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}

	// Reload from the store to prove persistence (real SQLite round-trip).
	ws, err := h.store.GetWorkspace(context.Background(), "ws-1")
	if err != nil {
		t.Fatalf("GetWorkspace: %v", err)
	}
	inst := ws.AgentInstances[0]
	if inst.Role != "Voice keeper" || inst.Description != "Owns tone" {
		t.Errorf("role/description not persisted: %+v", inst)
	}
	// Outer whitespace trimmed, internal newline preserved.
	if inst.CustomInstructions != custom {
		t.Errorf("custom_instructions = %q, want %q", inst.CustomInstructions, custom)
	}

	// The global definition must not have been created/mutated by this call.
	if _, ok := h.agentStore.GetAgent("Brand Copywriter"); ok {
		t.Errorf("global agent definition should not be touched by instance settings")
	}
}

// TestUpdateWorkspaceAgentInstanceSettings_RejectsOverlongCustom verifies the
// 2000-char cap (FR17).
func TestUpdateWorkspaceAgentInstanceSettings_RejectsOverlongCustom(t *testing.T) {
	h, cleanup := createTestHandler(t)
	defer cleanup()
	seedWorkspaceWithInstance(t, h, "ws-1", "Editor")

	long := strings.Repeat("x", maxCustomInstructionsLen+1)
	rr := patchInstanceSettings(h, "ws-1", "Editor", `{"custom_instructions":"`+long+`"}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for overlong custom_instructions, got %d", rr.Code)
	}
}

// TestUpdateWorkspaceAgentInstanceSettings_UnattachedAgent404 verifies an agent
// not attached to the workspace yields 404.
func TestUpdateWorkspaceAgentInstanceSettings_UnattachedAgent404(t *testing.T) {
	h, cleanup := createTestHandler(t)
	defer cleanup()
	seedWorkspaceWithInstance(t, h, "ws-1", "Editor")

	rr := patchInstanceSettings(h, "ws-1", "Nonexistent", `{"role":"x"}`)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unattached agent, got %d body=%s", rr.Code, rr.Body.String())
	}
}

// TestGetWorkspaceAgentEffectivePrompt verifies the inspector returns the shared
// base prompt layered with the per-workspace refinement (PRD FR30).
func TestGetWorkspaceAgentEffectivePrompt(t *testing.T) {
	h, cleanup := createTestHandler(t)
	defer cleanup()

	if err := h.agentStore.CreateAgent("Copywriter", &agentstore.CreateAgentConfig{SystemPrompt: "BASE PROMPT"}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	seedWorkspaceWithInstance(t, h, "ws-1", "Copywriter")
	if rr := patchInstanceSettings(h, "ws-1", "Copywriter", `{"custom_instructions":"Be concise."}`); rr.Code != http.StatusOK {
		t.Fatalf("seed custom_instructions: %d", rr.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/ws-1/agents/Copywriter/effective-prompt", nil)
	req.SetPathValue("workspaceID", "ws-1")
	req.SetPathValue("name", "Copywriter")
	rr := httptest.NewRecorder()
	h.GetWorkspaceAgentEffectivePrompt(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}

	var body struct {
		BaseSystemPrompt   string `json:"base_system_prompt"`
		CustomInstructions string `json:"custom_instructions"`
		Refinement         string `json:"refinement"`
		EffectivePrompt    string `json:"effective_prompt"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v body=%s", err, rr.Body.String())
	}
	if body.BaseSystemPrompt != "BASE PROMPT" {
		t.Errorf("base_system_prompt = %q, want BASE PROMPT", body.BaseSystemPrompt)
	}
	if body.CustomInstructions != "Be concise." || !strings.Contains(body.Refinement, "Be concise.") {
		t.Errorf("refinement not surfaced: %+v", body)
	}
	if !strings.Contains(body.EffectivePrompt, "BASE PROMPT") || !strings.Contains(body.EffectivePrompt, "Be concise.") {
		t.Errorf("effective_prompt should layer base + refinement, got %q", body.EffectivePrompt)
	}
}
