package server

import (
	"context"
	"errors"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/personalassistanthttp"
)

// firstLookActionsHost serves Start this and Not now on a hired agent's
// first-look report. Its seams are read when a click arrives, not when the
// route is wired, because the folder digest and the task handler are built in
// other phases.
type firstLookActionsHost struct{ builder *ServerBuilder }

var _ personalassistanthttp.FirstLookActioner = firstLookActionsHost{}

func (h firstLookActionsHost) actions() *personalassistant.FirstLookActions {
	b := h.builder
	return &personalassistant.FirstLookActions{
		Digest:     b.personalAssistantFolderDigest,
		Workspaces: b.workspaceStore,
		// The same manual-execution path Start first look uses: it checks the
		// workspace may run work here before anything starts.
		Start: func(workspaceID, taskID string) error {
			if b.orchestrationHandler == nil {
				return errors.New("task execution is unavailable")
			}
			return b.orchestrationHandler.StartTaskAsync(workspaceID, taskID)
		},
	}
}

func (h firstLookActionsHost) StartSuggestion(ctx context.Context, userID, lookTaskID string) (personalassistant.FirstLookStarted, error) {
	return h.actions().StartSuggestion(ctx, userID, lookTaskID)
}

func (h firstLookActionsHost) Dismiss(ctx context.Context, userID, lookTaskID string) error {
	return h.actions().Dismiss(ctx, userID, lookTaskID)
}
