package workspace

import "fmt"

// legalTaskTransitions defines the permitted task status transitions.
// Rows are the "from" state, values are the set of legal "to" states.
//
// The empty status ("") represents a freshly-constructed Task whose Status
// field has not yet been assigned — it can transition to any non-terminal
// state (and to Completed for markdown-driven creation of already-checked
// items).
//
// Reset transitions (going from a terminal state back to Pending or Assigned
// for a re-run) are listed explicitly. Cancelled is a terminal state that can
// only be reactivated via reset.
//
// To add a new TaskStatus, update both the constants in workspace_types.go
// and this map. SetStatus rejects transitions not present here; sites that
// legitimately bypass the table (orphan cleanup at server restart, schema
// migrations, tests) call ForceStatus.
var legalTaskTransitions = map[TaskStatus]map[TaskStatus]struct{}{
	"": {
		TaskStatusPending:    {},
		TaskStatusAssigned:   {},
		TaskStatusInProgress: {},
		TaskStatusCompleted:  {}, // markdown-import: a [x] item is created already-completed
	},
	TaskStatusPending: {
		TaskStatusAssigned:   {},
		TaskStatusInProgress: {},
		TaskStatusCompleted:  {}, // markdown checkbox tick — no execution path
		TaskStatusCancelled:  {},
	},
	TaskStatusAssigned: {
		TaskStatusPending:    {}, // unassign / hold
		TaskStatusInProgress: {},
		TaskStatusCancelled:  {},
	},
	TaskStatusInProgress: {
		TaskStatusCompleted:        {},
		TaskStatusFailed:           {},
		TaskStatusWaitingForChoice: {},
		TaskStatusCancelled:        {},
		TaskStatusTimeout:          {},
		TaskStatusPending:          {}, // server-restart orphan cleanup
	},
	TaskStatusWaitingForChoice: {
		TaskStatusInProgress: {}, // user resumed
		TaskStatusFailed:     {},
		TaskStatusCancelled:  {},
		TaskStatusPending:    {}, // user reset
		TaskStatusAssigned:   {},
	},
	TaskStatusCompleted: {
		// Re-run paths. InProgress is allowed as a shortcut (skip the
		// reset-to-Pending step) for callers that immediately re-execute.
		TaskStatusPending:    {},
		TaskStatusAssigned:   {},
		TaskStatusInProgress: {},
		TaskStatusCancelled:  {},
	},
	TaskStatusFailed: {
		// Retry paths
		TaskStatusPending:    {},
		TaskStatusAssigned:   {},
		TaskStatusInProgress: {},
		TaskStatusCancelled:  {},
	},
	TaskStatusCancelled: {
		// Reactivate
		TaskStatusPending:    {},
		TaskStatusAssigned:   {},
		TaskStatusInProgress: {},
	},
	TaskStatusTimeout: {
		// Retry paths
		TaskStatusPending:    {},
		TaskStatusAssigned:   {},
		TaskStatusInProgress: {},
		TaskStatusCancelled:  {},
	},
}

// IllegalTaskTransitionError describes a refused status transition. Returned
// by Task.SetStatus when the requested transition is not in the legal table.
type IllegalTaskTransitionError struct {
	TaskID string
	From   TaskStatus
	To     TaskStatus
}

func (e *IllegalTaskTransitionError) Error() string {
	return fmt.Sprintf("illegal task status transition for %q: %q → %q", e.TaskID, e.From, e.To)
}

// SetStatus transitions the task to next, rejecting transitions that aren't in
// the legal table. Same-state assignments (X → X) always return an error so
// no-op flips are surfaced. The Task is left untouched on error.
//
// For recovery paths that legitimately bypass the table (orphan cleanup,
// schema migrations, tests that need to set up a specific state), use
// ForceStatus and document the reason.
func (t *Task) SetStatus(next TaskStatus) error {
	if t.Status == next {
		return &IllegalTaskTransitionError{TaskID: t.ID, From: t.Status, To: next}
	}
	allowed, ok := legalTaskTransitions[t.Status]
	if !ok {
		return &IllegalTaskTransitionError{TaskID: t.ID, From: t.Status, To: next}
	}
	if _, ok := allowed[next]; !ok {
		return &IllegalTaskTransitionError{TaskID: t.ID, From: t.Status, To: next}
	}
	t.Status = next
	return nil
}

// ForceStatus assigns next without checking the transition table. Reserved
// for paths that legitimately bypass the state machine — orphan cleanup at
// server restart, JSON migration, test fixtures. Include a comment at the
// call site explaining why; using ForceStatus where SetStatus would do is a
// smell.
func (t *Task) ForceStatus(next TaskStatus) {
	t.Status = next
}
