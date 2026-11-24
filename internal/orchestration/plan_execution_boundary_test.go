package orchestration

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/agentcomm"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func newBoundaryOrchestrator(t *testing.T) (*Orchestrator, store.Store, workspace.Store) {
	t.Helper()
	baseDir := t.TempDir()
	agentStore, err := store.NewFileStore(filepath.Join(baseDir, "agents.json"), types.Settings{
		Model: "gpt-4o-mini", Temperature: 1.0,
	})
	if err != nil {
		t.Fatalf("agent store: %v", err)
	}
	workspaceStore, err := workspace.NewFileStore(filepath.Join(baseDir, "workspaces"))
	if err != nil {
		t.Fatalf("workspace store: %v", err)
	}
	orch := NewOrchestrator(agentStore, workspaceStore, nil, agentcomm.NewCommunicator(workspaceStore), nil, nil, nil)
	return orch, agentStore, workspaceStore
}

func plannedWorkNeedingANewAgent() (*types.PlannerOutput, types.PlannerDecision) {
	return &types.PlannerOutput{
		ComplexityScore: 8,
		Rationale:       "needs a specialized agent",
		Tasks: []types.PlannerTask{{
			ID:             "step-1",
			Description:    "Fetch weather for New York",
			RequiredRole:   types.RoleSpecialist,
			SuggestedAgent: "weather-agent",
		}},
		DynamicAgents: []types.PlannerAgent{{
			Name: "weather-agent", Role: types.RoleSpecialist, Description: "Weather lookup specialist",
		}},
	}, types.PlannerDecision{
		ComplexityScore: 8, Threshold: 6, Mode: "auto", MultiAgent: true, CreatedAt: time.Now(),
	}
}

func workspaceCount(t *testing.T, store workspace.Store) int {
	t.Helper()
	ids, err := store.List()
	if err != nil {
		t.Fatalf("list workspaces: %v", err)
	}
	return len(ids)
}

// Work that needs an agent which does not exist yet is not run, not proposed and
// not queued. It used to open a durable Plan and queue agent requests for an
// inline approval; now the decision is made before any side effect, so there is
// no workspace, no stash, no request, no event, no new agent and no run to
// clean up if anything later fails.
func TestExecutePlannedTask_WorkNeedingANewAgentStartsNothing(t *testing.T) {
	orch, agentStore, workspaceStore := newBoundaryOrchestrator(t)
	plan, decision := plannedWorkNeedingANewAgent()
	agentsBefore := len(agentStore.ListAgents())

	result, err := orch.ExecutePlannedTask(context.Background(), "default", "Get weather", plan, decision, time.Minute)
	if err != nil {
		t.Fatalf("ExecutePlannedTask: %v", err)
	}
	if result.Status != StatusNeedsManualWork {
		t.Fatalf("status = %q, want %q", result.Status, StatusNeedsManualWork)
	}
	if result.FinalOutput != "" || len(result.SubResults) != 0 {
		t.Fatalf("no step may have run: output=%q results=%v", result.FinalOutput, result.SubResults)
	}
	if result.WorkspaceID != "" || result.PendingPlanID != "" || result.PlanID != "" ||
		len(result.DynamicAgentRequests) != 0 {
		t.Fatalf("nothing may be proposed or queued: %+v", result)
	}
	if result.PlannerDecision == nil || !result.PlannerDecision.MultiAgent {
		t.Fatalf("the planner's decision is still reported: %+v", result.PlannerDecision)
	}
	if got := workspaceCount(t, workspaceStore); got != 0 {
		t.Fatalf("a workspace was created for work that was not started (%d)", got)
	}
	if _, exists := agentStore.GetAgent("weather-agent"); exists {
		t.Fatalf("the proposed agent must not be created")
	}
	if got := len(agentStore.ListAgents()); got != agentsBefore {
		t.Fatalf("agent roster changed: %d -> %d", agentsBefore, got)
	}
}

// The decision is about missing agents, not about multi-agent work in general:
// work for agents that exist takes the same path it always did (a workspace is
// opened and the steps are delegated), so this change cannot have turned every
// multi-agent request into advice.
func TestExecutePlannedTask_WorkForExistingAgentsStillTakesTheNormalPath(t *testing.T) {
	orch, _, workspaceStore := newBoundaryOrchestrator(t)
	plan := &types.PlannerOutput{
		ComplexityScore: 8,
		Rationale:       "one general step",
		Tasks: []types.PlannerTask{{
			ID:           "step-1",
			Description:  "Summarize the notes",
			RequiredRole: types.RoleGeneral,
		}},
	}
	decision := types.PlannerDecision{ComplexityScore: 8, Threshold: 6, Mode: "auto", MultiAgent: true}

	// Nobody answers the delegated step, so the run times out; that it got that
	// far (a workspace, a delegation) is the point.
	result, _ := orch.ExecutePlannedTask(context.Background(), "default", "Summarize", plan, decision, 150*time.Millisecond)
	if result != nil && result.Status == StatusNeedsManualWork {
		t.Fatalf("work for existing agents must not be turned into advice")
	}
	if got := workspaceCount(t, workspaceStore); got != 1 {
		t.Fatalf("the normal path opens one workspace for the run, got %d", got)
	}
}
