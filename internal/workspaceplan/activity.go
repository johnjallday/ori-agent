package workspaceplan

import "time"

// Activity is one append-only record in a Plan's lifecycle history. Entries are
// never rewritten or removed, so a clarified-reviewed-rejected-revised-approved
// Plan keeps the full account of how it got there (FR-15, FR-80).
//
// One stream carries both status transitions and the audit events that do not
// change status — an approval being invalidated by a later edit, or an approval
// being consumed by materialization. Splitting those into a second table would
// make "what happened to this Plan, in order" a merge rather than a read.
type Activity struct {
	ID     string `json:"id"`
	PlanID string `json:"plan_id"`
	// WorkspaceID is carried on every row so an activity read can be scoped to
	// the owning workspace without joining back to the Plan (FR-163).
	WorkspaceID string `json:"studio_id"`
	// Sequence orders entries within one Plan, starting at 1.
	Sequence int64        `json:"sequence"`
	Kind     ActivityKind `json:"kind"`
	// From and To are set for a status transition (FR-15). From is empty for
	// the record that created the Plan, and both are empty for a
	// non-transition audit event.
	From Status `json:"from,omitempty"`
	To   Status `json:"to,omitempty"`
	// Source records which subsystem drove the entry.
	Source TransitionSource `json:"source"`
	// Actor is the user or system principal responsible.
	Actor   string `json:"actor,omitempty"`
	ActorID string `json:"actor_id,omitempty"`
	// Reason is optional explanatory text. It is user-visible and stored, so
	// it passes through the existing redaction utilities first (FR-171).
	Reason string `json:"reason,omitempty"`
	// Version references the Plan version the entry is about, when it is about
	// one (review, approval, supersession).
	Version int `json:"plan_version,omitempty"`
	// ApprovalID references the approval record for approval-related entries.
	ApprovalID string `json:"approval_id,omitempty"`
	// TaskID and RunID reference the record that triggered an execution-driven
	// entry. Only the reference is stored, never a copy of its state (FR-173).
	TaskID    string    `json:"task_id,omitempty"`
	RunID     string    `json:"run_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// ActivityKind classifies a lifecycle history entry.
type ActivityKind string

const (
	// ActivityCreated is the entry written when the Plan record is created.
	ActivityCreated ActivityKind = "created"
	// ActivityStatusChange is a validated status transition (FR-15).
	ActivityStatusChange ActivityKind = "status_change"
	// ActivityReviewRequested records a draft becoming an immutable review
	// version (FR-80).
	ActivityReviewRequested ActivityKind = "review_requested"
	// ActivityChangesRequested records a reviewer sending a version back to
	// draft while retaining it (FR-67, FR-80).
	ActivityChangesRequested ActivityKind = "changes_requested"
	// ActivityRejected records an explicit rejection with an optional reason
	// (FR-66, FR-80).
	ActivityRejected ActivityKind = "rejected"
	// ActivityApproved records an approval being granted (FR-80).
	ActivityApproved ActivityKind = "approved"
	// ActivityApprovalInvalidated records an outstanding approval attempt
	// being invalidated by an approval-relevant edit (FR-68, FR-80).
	ActivityApprovalInvalidated ActivityKind = "approval_invalidated"
	// ActivityApprovalConsumed records an approval being spent on its declared
	// materialization and execution effect (FR-72, FR-80).
	ActivityApprovalConsumed ActivityKind = "approval_consumed"
	// ActivityMaterialized records Tasks and artifacts being created.
	ActivityMaterialized ActivityKind = "materialized"
	// ActivityTaskLinked and ActivityRunLinked record provenance links.
	ActivityTaskLinked ActivityKind = "task_linked"
	ActivityRunLinked  ActivityKind = "run_linked"
	// ActivityArchived and ActivityReopened record History placement.
	ActivityArchived ActivityKind = "archived"
	ActivityReopened ActivityKind = "reopened"
	// ActivityClarificationAsked and ActivityClarificationAnswered record one
	// round of questions and the answers a user authored (FR-23, FR-25).
	ActivityClarificationAsked    ActivityKind = "clarification_asked"
	ActivityClarificationAnswered ActivityKind = "clarification_answered"
	// ActivityDraftRecovered records an autosave recovery point being restored
	// into the working draft (FR-30).
	ActivityDraftRecovered ActivityKind = "draft_recovered"
	// ActivityTaskSkipped records a user's decision to proceed without an
	// approved task, with the reason they gave (FR-115).
	ActivityTaskSkipped ActivityKind = "task_skipped"
	// ActivityCompleted records a plan finishing, with or without exceptions
	// (FR-119, FR-121).
	ActivityCompleted ActivityKind = "completed"
)

// NewActivityID returns a stable ID for one lifecycle history entry.
func NewActivityID() string { return activityIDPrefix + newUUID() }

const activityIDPrefix = "act_"

// NewStatusChange builds the append-only record for a validated transition
// (FR-15). Callers pass it to the store, which assigns Sequence.
func NewStatusChange(plan *Plan, to Status, source TransitionSource, actor, reason string) Activity {
	return Activity{
		ID:          NewActivityID(),
		PlanID:      plan.ID,
		WorkspaceID: plan.WorkspaceID,
		Kind:        ActivityStatusChange,
		From:        plan.Status,
		To:          to,
		Source:      source,
		Actor:       actor,
		// The reason is prose somebody wrote about what happened. Refusing it
		// would block a legitimate state change over its explanation, so it is
		// redacted on the way in instead (FR-171).
		Reason:    RedactCredentials(reason),
		CreatedAt: time.Now().UTC(),
	}
}

// NewActivity builds a non-transition history entry.
func NewActivity(plan *Plan, kind ActivityKind, source TransitionSource, actor, reason string) Activity {
	return Activity{
		ID:          NewActivityID(),
		PlanID:      plan.ID,
		WorkspaceID: plan.WorkspaceID,
		Kind:        kind,
		Source:      source,
		Actor:       actor,
		// The reason is prose somebody wrote about what happened. Refusing it
		// would block a legitimate state change over its explanation, so it is
		// redacted on the way in instead (FR-171).
		Reason:    RedactCredentials(reason),
		CreatedAt: time.Now().UTC(),
	}
}

// IsStatusChange reports whether this entry recorded a lifecycle transition.
func (a Activity) IsStatusChange() bool {
	return a.Kind == ActivityStatusChange || a.Kind == ActivityCreated
}
