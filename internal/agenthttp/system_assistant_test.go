package agenthttp

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
)

func newSystemAssistantTestStore(t *testing.T) store.Store {
	t.Helper()
	st, err := store.NewFileStore(filepath.Join(t.TempDir(), "agents.json"), types.Settings{})
	if err != nil {
		t.Fatalf("NewFileStore() error = %v", err)
	}
	return st
}

func TestEnsureSystemAssistantAgentSetsOrchestratorRole(t *testing.T) {
	st := newSystemAssistantTestStore(t)

	if err := EnsureSystemAssistantAgent(st); err != nil {
		t.Fatalf("EnsureSystemAssistantAgent() error = %v", err)
	}

	ag, ok := st.GetAgent(systemAssistantAgentName)
	if !ok || ag == nil {
		t.Fatalf("system assistant %q not found after ensure", systemAssistantAgentName)
	}
	if ag.Role != types.RoleOrchestrator {
		t.Fatalf("system assistant role = %q, want %q", ag.Role, types.RoleOrchestrator)
	}
}

func TestEnsureSystemAssistantAgentUpgradesExistingRole(t *testing.T) {
	st := newSystemAssistantTestStore(t)

	// Simulate a pre-existing Ori agent created before the orchestrator role
	// was assigned (the historical default was the general role).
	if err := st.CreateAgent(systemAssistantAgentName, &store.CreateAgentConfig{
		Type: agent.TypeGeneral,
		Role: types.RoleGeneral,
	}); err != nil {
		t.Fatalf("CreateAgent() error = %v", err)
	}

	if err := EnsureSystemAssistantAgent(st); err != nil {
		t.Fatalf("EnsureSystemAssistantAgent() error = %v", err)
	}

	ag, ok := st.GetAgent(systemAssistantAgentName)
	if !ok || ag == nil {
		t.Fatalf("system assistant %q not found after ensure", systemAssistantAgentName)
	}
	if ag.Role != types.RoleOrchestrator {
		t.Fatalf("system assistant role = %q, want %q", ag.Role, types.RoleOrchestrator)
	}
}

func TestDashboardUpdateAgentStatusBlocksDisablingSystemAssistant(t *testing.T) {
	st := newSystemAssistantTestStore(t)
	if err := EnsureSystemAssistantAgent(st); err != nil {
		t.Fatalf("EnsureSystemAssistantAgent() error = %v", err)
	}

	dashboard := NewDashboardHandler(st)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/agents/"+url.PathEscape(systemAssistantAgentName)+"/status",
		bytes.NewBufferString(`{"status":"disabled"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	dashboard.UpdateAgentStatus(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("UpdateAgentStatus() status = %d, want %d body=%s", rr.Code, http.StatusBadRequest, rr.Body.String())
	}

	ag, ok := st.GetAgent(systemAssistantAgentName)
	if !ok || ag == nil {
		t.Fatalf("system assistant %q not found", systemAssistantAgentName)
	}
	if ag.Status == types.AgentStatusDisabled {
		t.Fatalf("system assistant was disabled despite guard; status = %q", ag.Status)
	}
}

func TestDashboardUpdateAgentStatusAllowsReenablingSystemAssistant(t *testing.T) {
	st := newSystemAssistantTestStore(t)
	if err := EnsureSystemAssistantAgent(st); err != nil {
		t.Fatalf("EnsureSystemAssistantAgent() error = %v", err)
	}

	dashboard := NewDashboardHandler(st)

	// Re-enabling (status active) must not be blocked by the disable guard.
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/agents/"+url.PathEscape(systemAssistantAgentName)+"/status",
		bytes.NewBufferString(`{"status":"active"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	dashboard.UpdateAgentStatus(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("UpdateAgentStatus() status = %d, want %d body=%s", rr.Code, http.StatusOK, rr.Body.String())
	}
}
