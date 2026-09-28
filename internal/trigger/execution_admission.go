package trigger

import (
	"context"
	"strconv"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// runtimeTriggerKey scopes ephemeral watches, debounce windows and rate buckets
// to their canonical owner. A copied trigger ID cannot cancel native work.
func runtimeTriggerKey(workspaceID, triggerID string) string {
	return strconv.Itoa(len(workspaceID)) + ":" + workspaceID + triggerID
}

// requireExecution consults both canonical owners. A runtime-store decorator
// must not erase admission supplied by the folder owner (or vice versa). All
// wiring is fixed before Start; the local policy itself is read afresh.
func (s *Store) requireExecution(workspaceID string, automatic bool) error {
	ctx := context.Background()
	if admission, ok := s.source.(workspace.ExecutionAdmission); ok {
		if err := admission.CheckWorkspaceExecution(ctx, workspaceID, automatic); err != nil {
			return err
		}
	}
	return workspace.RequireWorkspaceExecution(ctx, s.executionOwner, workspaceID, automatic)
}
