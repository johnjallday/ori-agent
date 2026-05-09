package workspace

import "fmt"

// legalStepTransitions defines the permitted WorkflowStep status transitions.
// Rows are the "from" state, values are the set of legal "to" states.
//
// The empty status ("") represents a freshly-constructed step whose Status
// field has not yet been assigned — newly added steps should land at Pending.
//
// Reset transitions (going from a terminal state back to Pending or Ready
// for a re-run) are listed explicitly. Cancelled is terminal; reactivation
// goes through Pending.
//
// To add a new StepStatus, update both the constants in workflow_step.go
// and this map. SetStatus rejects transitions not present here; sites that
// legitimately bypass the table (orphan cleanup at server restart, schema
// migrations, tests) call ForceStatus.
var legalStepTransitions = map[StepStatus]map[StepStatus]struct{}{
	"": {
		StepStatusPending:    {},
		StepStatusWaiting:    {},
		StepStatusReady:      {},
		StepStatusInProgress: {},
		StepStatusCompleted:  {}, // re-hydrating a completed step
		StepStatusSkipped:    {}, // re-hydrating a skipped step
	},
	StepStatusPending: {
		StepStatusWaiting:   {},
		StepStatusReady:     {},
		StepStatusSkipped:   {}, // condition false at scheduling time
		StepStatusCancelled: {},
	},
	StepStatusWaiting: {
		StepStatusReady:     {},
		StepStatusSkipped:   {}, // upstream failed or condition false
		StepStatusCancelled: {},
		StepStatusPending:   {}, // reset
	},
	StepStatusReady: {
		StepStatusInProgress: {},
		StepStatusSkipped:    {}, // condition flipped before exec started
		StepStatusCancelled:  {},
		StepStatusPending:    {}, // reset
	},
	StepStatusInProgress: {
		StepStatusCompleted: {},
		StepStatusFailed:    {},
		StepStatusCancelled: {},
		StepStatusPending:   {}, // server-restart orphan cleanup
	},
	StepStatusCompleted: {
		// Re-run paths.
		StepStatusPending:    {},
		StepStatusReady:      {},
		StepStatusInProgress: {},
		StepStatusCancelled:  {},
	},
	StepStatusFailed: {
		// Retry paths.
		StepStatusPending:    {},
		StepStatusReady:      {},
		StepStatusInProgress: {},
		StepStatusCancelled:  {},
	},
	StepStatusSkipped: {
		// Skipped is reactivatable: condition state may change between runs.
		StepStatusPending:   {},
		StepStatusReady:     {},
		StepStatusCancelled: {},
	},
	StepStatusCancelled: {
		StepStatusPending: {},
	},
}

// IllegalStepTransitionError describes a refused step status transition.
// Returned by WorkflowStep.SetStatus when the requested transition is not in
// the legal table.
type IllegalStepTransitionError struct {
	StepID string
	From   StepStatus
	To     StepStatus
}

func (e *IllegalStepTransitionError) Error() string {
	return fmt.Sprintf("illegal step status transition for %q: %q → %q", e.StepID, e.From, e.To)
}

// SetStatus transitions the step to next, rejecting transitions that aren't
// in the legal table. Same-state assignments (X → X) always return an error
// so no-op flips are surfaced. The step is left untouched on error.
//
// For recovery paths that legitimately bypass the table (orphan cleanup,
// schema migrations, test fixtures), use ForceStatus and document the reason.
func (s *WorkflowStep) SetStatus(next StepStatus) error {
	if s.Status == next {
		return &IllegalStepTransitionError{StepID: s.ID, From: s.Status, To: next}
	}
	allowed, ok := legalStepTransitions[s.Status]
	if !ok {
		return &IllegalStepTransitionError{StepID: s.ID, From: s.Status, To: next}
	}
	if _, ok := allowed[next]; !ok {
		return &IllegalStepTransitionError{StepID: s.ID, From: s.Status, To: next}
	}
	s.Status = next
	return nil
}

// ForceStatus assigns next without checking the transition table. Reserved
// for paths that legitimately bypass the state machine — orphan cleanup at
// server restart, JSON migration, test fixtures. Include a comment at the
// call site explaining why; using ForceStatus where SetStatus would do is a
// smell.
func (s *WorkflowStep) ForceStatus(next StepStatus) {
	s.Status = next
}
