package server

import (
	"context"
	"errors"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/personalassistanthttp"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// hireLookRunning reports whether a hired agent's workspace has a goal run in
// flight. The scheduler is read on each call: it is built in another phase.
func (b *ServerBuilder) hireLookRunning(workspaceID string) bool {
	if b == nil || b.taskScheduler == nil {
		return false
	}
	_, running := b.taskScheduler.MissionRunning(workspaceID)
	return running
}

// hireLookActionsHost serves Start this, Not now and Retry on the report of an
// agent hired from the Agents page. Like the folder report's host, its seams
// are read when a click arrives, not when the route is wired.
type hireLookActionsHost struct{ builder *ServerBuilder }

var _ personalassistanthttp.HireLookActioner = hireLookActionsHost{}

func (h hireLookActionsHost) actions() *personalassistant.HireLookActions {
	b := h.builder
	actions := &personalassistant.HireLookActions{
		Workspaces: b.workspaceStore,
		Running:    b.hireLookRunning,
		// The same manual-execution path the folder report's Start this uses.
		Start: func(workspaceID, taskID string) error {
			if b.orchestrationHandler == nil {
				return errors.New("task execution is unavailable")
			}
			return b.orchestrationHandler.StartTaskAsync(workspaceID, taskID)
		},
		Look: func(workspaceID string) (string, error) {
			if b.taskScheduler == nil {
				return "", errors.New("goal runs are not available on this server")
			}
			return b.taskScheduler.StartMissionRun(workspaceID, workspace.MissionOriginHireFirstLook)
		},
	}
	if b.sessionStore != nil {
		actions.Records = personalassistant.NewSQLiteHireRecords(b.sessionStore.DB())
	}
	return actions
}

func (h hireLookActionsHost) StartSuggestion(ctx context.Context, userID, hireID string) (personalassistant.FirstLookStarted, error) {
	return h.actions().StartSuggestion(ctx, userID, hireID)
}

func (h hireLookActionsHost) Dismiss(ctx context.Context, userID, hireID string) error {
	return h.actions().Dismiss(ctx, userID, hireID)
}

func (h hireLookActionsHost) Retry(ctx context.Context, userID, hireID string) (personalassistant.HireRecord, error) {
	return h.actions().Retry(ctx, userID, hireID)
}
