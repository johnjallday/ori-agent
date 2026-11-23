package sessionhttp

import (
	"strings"

	"github.com/johnjallday/ori-agent/internal/logger"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// applyCreateWorkspaceGoal stores the goal a new workspace was created with as
// its Workspace.Mission, the one goal every workspace prompt reads. It writes
// through the folder-aware store so workspace.json and SQLite agree, and it is
// best-effort like starter-task seeding: a failure logs and never fails
// creation. Storing a goal never runs it. Legacy callers can arm the
// first-open behavior (FR25); ordinary assisted creation leaves it unarmed.
func (h *Handler) applyCreateWorkspaceGoal(workspaceID, goal string, armFirstOpen bool) {
	goal = strings.TrimSpace(goal)
	id := strings.TrimSpace(workspaceID)
	if h == nil || goal == "" || id == "" {
		return
	}
	store := h.taskMutationStore()
	if store == nil {
		return
	}
	if err := store.Update(id, func(ws *agentworkspace.Workspace) error {
		ws.Mission = goal
		ws.GoalFirstRunPending = armFirstOpen
		return nil
	}); err != nil {
		logger.Warn("Failed to store the workspace goal", logger.Fields{"workspace_id": id, "error": err})
	}
}
