package workspaceplan

import (
	"fmt"
	"slices"
)

// Status is the user-facing Plan lifecycle state (FR-13). It is the only
// lifecycle authority for a Plan: it is never derived from linked Task status,
// Run status, the presence of an approval, or anything a model returned.
type Status string

const (
	// StatusDraft means Plan content is being prepared and nothing has been
	// committed to review.
	StatusDraft Status = "draft"
	// StatusNeedsInput means at least one required clarification question is
	// unanswered (FR-23, FR-26).
	StatusNeedsInput Status = "needs_input"
	// StatusInReview means an immutable version exists and is awaiting an
	// approve, request-changes, or reject decision (FR-31).
	StatusInReview Status = "in_review"
	// StatusApproved means an approval was consumed and Tasks (plus any
	// enabled artifacts) were materialized, but no work has started (FR-94).
	StatusApproved Status = "approved"
	// StatusExecuting means this Plan owns the workspace execution slot and
	// has active or eligible linked Runs (FR-106).
	StatusExecuting Status = "executing"
	// StatusPaused means dispatch stopped at a user action or a policy gate
	// and the execution slot has been or is being released safely (FR-108).
	StatusPaused Status = "paused"
	// StatusCompleted means every required approved item reached a successful
	// or explicitly accepted terminal outcome and required validations passed
	// (FR-119).
	StatusCompleted Status = "completed"
	// StatusFailed means execution hit a terminal failure that cannot continue
	// without revision or user intervention. Ordinary retryable errors pause
	// instead (FR-120).
	StatusFailed Status = "failed"
	// StatusCancelled means the user intentionally stopped the Plan. Cancelled
	// Plans move to History immediately and keep all completed history
	// (FR-16, FR-112).
	StatusCancelled Status = "cancelled"
	// StatusSuperseded means a newer approved Plan replaced this direction.
	StatusSuperseded Status = "superseded"
)

// AllStatuses lists every supported status in lifecycle order. Presentation
// code should read this rather than hard-coding its own list.
func AllStatuses() []Status {
	return []Status{
		StatusDraft,
		StatusNeedsInput,
		StatusInReview,
		StatusApproved,
		StatusExecuting,
		StatusPaused,
		StatusCompleted,
		StatusFailed,
		StatusCancelled,
		StatusSuperseded,
	}
}

// Valid reports whether the value is one of the ten supported statuses. An
// unsupported status is rejected rather than stored (FR-42).
func (s Status) Valid() bool {
	_, ok := statusLabels[s]
	return ok
}

// Terminal reports whether a Plan in this status has finished moving on its
// own. Terminal Plans belong in History and can only leave through an explicit
// user action the transition table allows.
func (s Status) Terminal() bool {
	switch s {
	case StatusCompleted, StatusFailed, StatusCancelled, StatusSuperseded:
		return true
	default:
		return false
	}
}

// Active reports whether a Plan in this status belongs in the Active section
// of the Plans list rather than History (FR-146).
func (s Status) Active() bool { return !s.Terminal() }

// Executing reports whether this status requires holding the workspace
// execution slot (FR-106).
func (s Status) Executing() bool { return s == StatusExecuting }

// String returns the raw status value.
func (s Status) String() string { return string(s) }

// Label returns the human-readable name for this status. Every status has both
// a label and an icon so state is never communicated by color alone (FR-162).
func (s Status) Label() string {
	if label, ok := statusLabels[s]; ok {
		return label
	}
	return string(s)
}

var statusLabels = map[Status]string{
	StatusDraft:      "Draft",
	StatusNeedsInput: "Needs input",
	StatusInReview:   "In review",
	StatusApproved:   "Approved",
	StatusExecuting:  "Executing",
	StatusPaused:     "Paused",
	StatusCompleted:  "Completed",
	StatusFailed:     "Failed",
	StatusCancelled:  "Cancelled",
	StatusSuperseded: "Superseded",
}

// TransitionSource records which subsystem drove a transition. It is audit
// metadata: no source value grants authority a caller does not already have,
// and in particular SourceModel can never reach an approval transition
// (FR-59, FR-60).
type TransitionSource string

const (
	// SourceUser is an explicit action taken by a user through an authorized
	// Ori client. Approval transitions accept only this source (FR-59).
	SourceUser TransitionSource = "user"
	// SourceService is compiled Ori application logic — materialization,
	// dispatch, completion detection, and retention.
	SourceService TransitionSource = "service"
	// SourceExecution is the Task and Run execution path reporting an outcome.
	SourceExecution TransitionSource = "execution"
	// SourceModel is model-generated planning output. It may move a draft into
	// needs_input and back, and nothing else.
	SourceModel TransitionSource = "model"
	// SourceRetention is the scheduled retention sweep (FR-16).
	SourceRetention TransitionSource = "retention"
)

// transition is one allowed edge in the lifecycle graph.
type transition struct {
	from Status
	to   Status
}

// allowedTransitions is the explicit server-side transition table (FR-14).
// Every status change is validated against it, so no handler, model output, or
// browser request can invent an edge that is not listed here.
//
// The table is intentionally written as data rather than as branching code:
// the set of legal moves is a product decision that must be readable and
// testable on its own.
var allowedTransitions = map[transition]struct{}{
	// Drafting and clarification (FR-22, FR-23, FR-26, FR-31).
	{StatusDraft, StatusNeedsInput}:      {},
	{StatusDraft, StatusInReview}:        {},
	{StatusDraft, StatusCancelled}:       {},
	{StatusDraft, StatusSuperseded}:      {},
	{StatusNeedsInput, StatusDraft}:      {},
	{StatusNeedsInput, StatusCancelled}:  {},
	{StatusNeedsInput, StatusSuperseded}: {},

	// Review outcomes. Request-changes and rejection both return to draft and
	// retain the reviewed version (FR-37, FR-66, FR-67).
	{StatusInReview, StatusDraft}:      {},
	{StatusInReview, StatusNeedsInput}: {},
	{StatusInReview, StatusApproved}:   {},
	{StatusInReview, StatusCancelled}:  {},
	{StatusInReview, StatusSuperseded}: {},

	// After approval. A Plan reaches approved only once Tasks and artifacts
	// have committed, and only then may execution begin (FR-94).
	{StatusApproved, StatusExecuting}:  {},
	{StatusApproved, StatusPaused}:     {},
	{StatusApproved, StatusDraft}:      {},
	{StatusApproved, StatusCompleted}:  {},
	{StatusApproved, StatusCancelled}:  {},
	{StatusApproved, StatusSuperseded}: {},

	// Execution (FR-108, FR-110, FR-119, FR-120).
	{StatusExecuting, StatusPaused}:    {},
	{StatusExecuting, StatusCompleted}: {},
	{StatusExecuting, StatusFailed}:    {},
	{StatusExecuting, StatusCancelled}: {},
	{StatusPaused, StatusExecuting}:    {},
	{StatusPaused, StatusApproved}:     {},
	{StatusPaused, StatusDraft}:        {},
	{StatusPaused, StatusCompleted}:    {},
	{StatusPaused, StatusFailed}:       {},
	{StatusPaused, StatusCancelled}:    {},
	{StatusPaused, StatusSuperseded}:   {},

	// A failed Plan is revisable and cancellable but never silently resumes:
	// returning to execution requires a fresh draft, version, and approval
	// (FR-38, FR-116, FR-120).
	{StatusFailed, StatusDraft}:      {},
	{StatusFailed, StatusPaused}:     {},
	{StatusFailed, StatusCancelled}:  {},
	{StatusFailed, StatusSuperseded}: {},

	// A completed Plan may be superseded by a newer direction, and may start a
	// follow-up draft, but never returns to execution on its old approval.
	{StatusCompleted, StatusDraft}:      {},
	{StatusCompleted, StatusSuperseded}: {},
}

// approvalOnlyTransitions lists the edges that represent granting approval.
// They accept only an explicit user action: agent output, workspace files,
// tool results, skill instructions, and chat text are never approval, and no
// preset or confirmation mode removes this gate (FR-59, FR-60, FR-75).
var approvalOnlyTransitions = map[transition]struct{}{
	{StatusInReview, StatusApproved}: {},
}

// CanTransition reports whether moving from one status to another is allowed.
// A no-op move (from equal to to) is not a transition and returns false, so
// callers cannot record a change that did not happen.
func CanTransition(from, to Status) bool {
	if !from.Valid() || !to.Valid() {
		return false
	}
	_, ok := allowedTransitions[transition{from, to}]
	return ok
}

// RequiresUserApproval reports whether this edge may only be driven by an
// explicit user action (FR-59).
func RequiresUserApproval(from, to Status) bool {
	_, ok := approvalOnlyTransitions[transition{from, to}]
	return ok
}

// NextStatuses returns the statuses reachable from the given status, sorted so
// the result is deterministic for tests and API responses.
func NextStatuses(from Status) []Status {
	var next []Status
	for edge := range allowedTransitions {
		if edge.from == from {
			next = append(next, edge.to)
		}
	}
	slices.Sort(next)
	return next
}

// ValidateApprovalTransition authorizes an approval transition with a real,
// consumed approval record rather than with a claimed source.
//
// This exists because materialization is compiled service code that acts ON a
// user's decision: the user approved, and moving the Plan to approved is the
// consequence of spending that approval. Letting the materializer simply claim
// SourceUser would weaken the guarantee to "any code that says it is a user".
// Requiring the approval record itself makes it stronger — the transition needs
// evidence, not an assertion (FR-59, FR-94).
func ValidateApprovalTransition(from, to Status, approval *Approval, planID string, version int) error {
	if !RequiresUserApproval(from, to) {
		return ValidateTransition(from, to, SourceService)
	}
	if approval == nil {
		return fmt.Errorf("%w: %s to %s requires an approval record", ErrApprovalAuthority, from, to)
	}
	if approval.PlanID != planID || approval.Version != version {
		return fmt.Errorf("%w: the approval does not belong to this plan version", ErrApprovalMismatch)
	}
	// Only a spent approval authorizes the move. An approval that exists but
	// has not been consumed has not yet caused anything.
	if !approval.Consumed() {
		return fmt.Errorf("%w: the approval has not been consumed", ErrApprovalAuthority)
	}
	if approval.Invalidated() {
		return fmt.Errorf("%w: the approval was invalidated", ErrApprovalMismatch)
	}
	if !CanTransition(from, to) {
		return fmt.Errorf("%w: %s cannot move to %s", ErrInvalidTransition, from, to)
	}
	return nil
}

// ValidateTransition checks one proposed status change against the transition
// table and the approval-source invariant. It is the single gate every
// lifecycle mutation passes through (FR-14).
func ValidateTransition(from, to Status, source TransitionSource) error {
	if !from.Valid() {
		return fmt.Errorf("%w: unsupported current status %q", ErrInvalidTransition, from)
	}
	if !to.Valid() {
		return fmt.Errorf("%w: unsupported target status %q", ErrInvalidTransition, to)
	}
	if from == to {
		return fmt.Errorf("%w: plan is already %s", ErrInvalidTransition, from)
	}
	if !CanTransition(from, to) {
		return fmt.Errorf("%w: %s cannot move to %s", ErrInvalidTransition, from, to)
	}
	if RequiresUserApproval(from, to) && source != SourceUser {
		return fmt.Errorf("%w: %s to %s requires an explicit user action, not %q",
			ErrApprovalAuthority, from, to, source)
	}
	return nil
}
