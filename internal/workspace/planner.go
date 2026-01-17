package workspace

import (
	"time"

	"github.com/johnjallday/ori-agent/internal/types"
)

// SetPlannerDecision stores the latest planner routing decision.
func (w *Workspace) SetPlannerDecision(decision *types.PlannerDecision) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.PlannerDecision = decision
	w.UpdatedAt = time.Now()
}

// SetPendingPlan stores a pending plan awaiting user approval.
func (w *Workspace) SetPendingPlan(plan *types.PendingPlan) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.PendingPlan = plan
	w.UpdatedAt = time.Now()
}

// ClearPendingPlan removes any pending plan.
func (w *Workspace) ClearPendingPlan() {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.PendingPlan = nil
	w.UpdatedAt = time.Now()
}
