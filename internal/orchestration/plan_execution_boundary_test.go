package orchestration

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/agentcomm"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// failingDrafter stands in for an unavailable or refusing Plan service: the
// shape a drafter failure takes when planning is disabled for a workspace, the
// store errors, or the drafter was never wired.
type failingDrafter struct{ calls int }

func (d *failingDrafter) DraftPlan(context.Context, string, string, *types.PlannerOutput) (string, error) {
	d.calls++
	return "", errors.New("workspace planning is not enabled")
}

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

// Work that needs an agent which does not exist yet is never run, whether or not
// a durable Plan could be drafted for it. A drafter failure must not turn into
// execution or into an agent created behind the user's back.
func TestExecutePlannedTask_DynamicAgentWorkIsNeverRun(t *testing.T) {
	cases := map[string]PlanDrafter{
		"no drafter wired":    nil,
		"drafter that errors": &failingDrafter{},
	}
	for name, drafter := range cases {
		t.Run(name, func(t *testing.T) {
			orch, agentStore, workspaceStore := newBoundaryOrchestrator(t)
			if drafter != nil {
				orch.SetPlanDrafter(drafter)
			}
			plan, decision := plannedWorkNeedingANewAgent()

			result, err := orch.ExecutePlannedTask(context.Background(), "default", "Get weather", plan, decision, time.Minute)
			if err != nil {
				t.Fatalf("ExecutePlannedTask: %v", err)
			}
			if result.Status == "completed" || result.FinalOutput != "" {
				t.Fatalf("work needing a new agent must not run: status=%q output=%q", result.Status, result.FinalOutput)
			}
			if len(result.SubResults) != 0 {
				t.Fatalf("no step may have produced a result: %v", result.SubResults)
			}
			if _, exists := agentStore.GetAgent("weather-agent"); exists {
				t.Fatalf("the proposed agent must not be created without an approval")
			}
			if result.WorkspaceID != "" {
				ws, err := workspaceStore.Get(result.WorkspaceID)
				if err != nil {
					t.Fatalf("load workspace: %v", err)
				}
				if len(ws.Tasks) != 0 {
					t.Fatalf("no ticket may be delegated for the proposed work, got %d", len(ws.Tasks))
				}
			}
		})
	}
}
