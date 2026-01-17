package orchestration

import (
	"testing"

	"github.com/johnjallday/ori-agent/internal/types"
)

func TestDecideMultiAgent(t *testing.T) {
	orch := &Orchestrator{}
	plan := &types.PlannerOutput{ComplexityScore: 7.5, Rationale: "test"}

	decision := orch.DecideMultiAgent(plan, types.MultiAgentModeAuto, 6.0)
	if !decision.MultiAgent {
		t.Fatalf("expected multi-agent to be enabled for auto mode")
	}

	decision = orch.DecideMultiAgent(plan, types.MultiAgentModeOff, 6.0)
	if decision.MultiAgent {
		t.Fatalf("expected multi-agent to be disabled for off mode")
	}

	decision = orch.DecideMultiAgent(plan, types.MultiAgentModeForce, 9.0)
	if !decision.MultiAgent {
		t.Fatalf("expected multi-agent to be enabled for force mode")
	}
}
