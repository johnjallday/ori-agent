package agenthttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/cliagent"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
)

type dashboardCLIStubAdapter struct {
	backend string
	models  []string
}

func (a dashboardCLIStubAdapter) Backend() string { return a.backend }
func (a dashboardCLIStubAdapter) IsAvailable() bool {
	return true
}
func (a dashboardCLIStubAdapter) AvailableModels() []string {
	return a.models
}
func (a dashboardCLIStubAdapter) Capabilities() cliagent.Capabilities {
	return cliagent.Capabilities{}
}
func (a dashboardCLIStubAdapter) ExecuteStep(context.Context, cliagent.StepRequest) (*cliagent.StepResult, error) {
	return nil, nil
}

func resetCLIAgentStatusStateForTest() {
	cliAgentStatusState.Lock()
	cliAgentStatusState.statuses = make(map[string]types.AgentStatus)
	cliAgentStatusState.Unlock()
}

func TestDashboardUpdateAgentStatusSupportsCLIAgents(t *testing.T) {
	resetCLIAgentStatusStateForTest()
	t.Cleanup(resetCLIAgentStatusStateForTest)

	st, err := store.NewFileStore(filepath.Join(t.TempDir(), "agents.json"), types.Settings{})
	if err != nil {
		t.Fatalf("NewFileStore() error = %v", err)
	}
	registry := cliagent.NewRegistry(dashboardCLIStubAdapter{
		backend: cliagent.BackendClaude,
		models:  []string{"opus"},
	})

	dashboard := NewDashboardHandler(st)
	dashboard.SetCLIAgentRegistry(registry)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/agents/Claude%20Code/status",
		bytes.NewBufferString(`{"status":"disabled"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	dashboard.UpdateAgentStatus(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("UpdateAgentStatus() status = %d body=%s", rr.Code, rr.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/agents/dashboard/list", nil)
	listRR := httptest.NewRecorder()
	dashboard.ListAgentsWithStats(listRR, listReq)

	if listRR.Code != http.StatusOK {
		t.Fatalf("ListAgentsWithStats() status = %d body=%s", listRR.Code, listRR.Body.String())
	}

	var listBody struct {
		Agents []AgentListItem `json:"agents"`
	}
	if err := json.Unmarshal(listRR.Body.Bytes(), &listBody); err != nil {
		t.Fatalf("decode dashboard list: %v", err)
	}
	if len(listBody.Agents) != 1 {
		t.Fatalf("expected one CLI agent, got %+v", listBody.Agents)
	}
	if got := listBody.Agents[0].Status; got != types.AgentStatusDisabled {
		t.Fatalf("dashboard CLI status = %q, want %q", got, types.AgentStatusDisabled)
	}

	agentsHandler := New(st)
	agentsHandler.SetCLIAgentRegistry(registry)
	agentsReq := httptest.NewRequest(http.MethodGet, "/api/agents", nil)
	agentsRR := httptest.NewRecorder()
	agentsHandler.ServeHTTP(agentsRR, agentsReq)

	if agentsRR.Code != http.StatusOK {
		t.Fatalf("agents list status = %d body=%s", agentsRR.Code, agentsRR.Body.String())
	}

	var agentsBody struct {
		Agents []struct {
			Name   string            `json:"name"`
			Status types.AgentStatus `json:"status"`
		} `json:"agents"`
	}
	if err := json.Unmarshal(agentsRR.Body.Bytes(), &agentsBody); err != nil {
		t.Fatalf("decode agents list: %v", err)
	}
	if len(agentsBody.Agents) != 1 {
		t.Fatalf("expected one CLI agent in /api/agents, got %+v", agentsBody.Agents)
	}
	if got := agentsBody.Agents[0].Status; got != types.AgentStatusDisabled {
		t.Fatalf("/api/agents CLI status = %q, want %q", got, types.AgentStatusDisabled)
	}
}
