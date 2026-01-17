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

func TestExecutePlannedTask_PendingDynamicAgents(t *testing.T) {
	baseDir := t.TempDir()

	agentStore, err := store.NewFileStore(filepath.Join(baseDir, "agents.json"), types.Settings{
		Model:       "gpt-4o-mini",
		Temperature: 1.0,
	})
	if err != nil {
		t.Fatalf("failed to create agent store: %v", err)
	}

	workspaceStore, err := workspace.NewFileStore(filepath.Join(baseDir, "workspaces"))
	if err != nil {
		t.Fatalf("failed to create workspace store: %v", err)
	}

	communicator := agentcomm.NewCommunicator(workspaceStore)
	orch := NewOrchestrator(agentStore, workspaceStore, communicator, nil, nil, nil)

	plan := &types.PlannerOutput{
		ComplexityScore: 8,
		Rationale:       "needs specialized agent",
		Tasks: []types.PlannerTask{
			{
				ID:             "step-1",
				Description:    "Fetch weather for New York",
				RequiredRole:   types.RoleSpecialist,
				SuggestedAgent: "weather-agent",
			},
		},
		DynamicAgents: []types.PlannerAgent{
			{
				Name:         "weather-agent",
				Role:         types.RoleSpecialist,
				Capabilities: []string{types.CapabilityAPIIntegration},
				Description:  "Weather lookup specialist",
			},
		},
	}

	decision := types.PlannerDecision{
		ComplexityScore: 8,
		Threshold:       6,
		Mode:            "auto",
		MultiAgent:      true,
		CreatedAt:       time.Now(),
	}

	result, err := orch.ExecutePlannedTask(context.Background(), "default", "Get weather", plan, decision, time.Minute)
	if err != nil {
		t.Fatalf("expected pending plan, got error: %v", err)
	}
	if result.Status != "pending_approval" {
		t.Fatalf("expected pending_approval status, got %q", result.Status)
	}
	if result.PendingPlanID == "" {
		t.Fatalf("expected pending plan id to be set")
	}
	if len(result.DynamicAgentRequests) == 0 {
		t.Fatalf("expected dynamic agent requests")
	}

	ws, err := workspaceStore.Get(result.WorkspaceID)
	if err != nil {
		t.Fatalf("failed to load workspace: %v", err)
	}
	if ws.PendingPlan == nil {
		t.Fatalf("expected pending plan on workspace")
	}
	if len(ws.DynamicAgentRequests) == 0 {
		t.Fatalf("expected dynamic agent requests on workspace")
	}
}
