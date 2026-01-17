package chathttp

import "github.com/johnjallday/ori-agent/internal/types"

func attachPlannerDecision(response map[string]any, decision *types.PlannerDecision) map[string]any {
	if decision != nil {
		response["planner_decision"] = decision
	}
	return response
}
