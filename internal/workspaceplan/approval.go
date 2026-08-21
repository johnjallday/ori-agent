package workspaceplan

import "time"

// Approval is the durable record of a user's decision to authorize one exact
// Plan version and its declared effects (FR-70). It is the only thing that can
// cause Tasks, artifacts, or Runs to exist, and it survives server restart
// (FR-71).
//
// Nothing else grants it. Agent output, workspace files, tool results, skill
// instructions, chat text, and browser-supplied flags are not approval, and no
// preset or confirmation mode removes the requirement (FR-59, FR-60, FR-75,
// FR-168).
type Approval struct {
	ID          string `json:"id"`
	PlanID      string `json:"plan_id"`
	WorkspaceID string `json:"studio_id"`
	// Version is the exact immutable version approved, and ContentHash is that
	// version's hash. The server rejects an approval whose version, hash,
	// workspace, or effect no longer matches current state (FR-69).
	Version     int    `json:"plan_version"`
	ContentHash string `json:"content_hash"`
	// Effect is what the user was told approval would do, and is what the
	// approval authorizes — no more (FR-63).
	Effect ApprovalEffect `json:"effect"`

	// UserID and UserName identify the approving user for Task assignment
	// provenance and the approval history view (FR-79, FR-87).
	UserID   string `json:"user_id,omitempty"`
	UserName string `json:"user_name,omitempty"`

	// IdempotencyKey makes a retried approval request return the original
	// result instead of creating a second Task tree, artifact set, or Run
	// (FR-73, FR-178).
	IdempotencyKey string    `json:"idempotency_key"`
	CreatedAt      time.Time `json:"created_at"`

	// ConsumedAt marks the approval as spent. An approval is consumable
	// exactly once for its declared materialization and execution effect
	// (FR-72); a second attempt replays ConsumedResult rather than acting.
	ConsumedAt     *time.Time      `json:"consumed_at,omitempty"`
	ConsumedResult *ApprovalResult `json:"consumed_result,omitempty"`

	// InvalidatedAt marks an approval attempt that was superseded by an
	// approval-relevant edit before it was consumed (FR-68).
	InvalidatedAt     *time.Time `json:"invalidated_at,omitempty"`
	InvalidatedReason string     `json:"invalidated_reason,omitempty"`
}

// ApprovalEffect is the declared consequence of approving. The primary action
// label is derived from it, so a side-effecting approval can never be presented
// behind a generic "Approve" button (FR-64, FR-65).
type ApprovalEffect string

const (
	// EffectCreateTasks creates Tasks and any enabled artifacts, and starts
	// nothing. Its action label is "Approve and Create Tasks" (FR-65).
	EffectCreateTasks ApprovalEffect = "create_tasks"
	// EffectCreateTasksAndStart additionally authorizes automatic dispatch of
	// eligible Tasks after successful materialization. Its action label is
	// "Approve and Start" (FR-64, FR-103).
	EffectCreateTasksAndStart ApprovalEffect = "create_tasks_and_start"
)

// Valid reports whether the effect is one Ori supports.
func (e ApprovalEffect) Valid() bool {
	return e == EffectCreateTasks || e == EffectCreateTasksAndStart
}

// StartsExecution reports whether consuming this approval authorizes automatic
// dispatch (FR-103).
func (e ApprovalEffect) StartsExecution() bool { return e == EffectCreateTasksAndStart }

// ActionLabel returns the exact primary-action wording the approval view must
// use for this effect (FR-64, FR-65).
func (e ApprovalEffect) ActionLabel() string {
	if e.StartsExecution() {
		return "Approve and Start"
	}
	return "Approve and Create Tasks"
}

// ApprovalResult is the outcome recorded when an approval is consumed. Storing
// it is what lets a retried request return the original result rather than
// materializing a second time (FR-73).
type ApprovalResult struct {
	// TaskIDs are the Workspace Tasks created, in Plan order.
	TaskIDs []string `json:"task_ids,omitempty"`
	// ArtifactPaths are the workspace-relative paths written.
	ArtifactPaths []string `json:"artifact_paths,omitempty"`
	// Handoff is the repository-native implementation entry point recognized
	// when the approval was consumed. Persisting it keeps retries identical.
	Handoff         *ImplementationHandoff `json:"handoff,omitempty"`
	HandoffResolved bool                   `json:"handoff_resolved,omitempty"`
	// Started reports whether automatic dispatch was authorized and begun.
	Started     bool      `json:"started"`
	CompletedAt time.Time `json:"completed_at"`
}

// NewApprovalID returns a stable ID for one approval record.
func NewApprovalID() string { return approvalIDPrefix + newUUID() }

const approvalIDPrefix = "apr_"

// Consumed reports whether this approval has already been spent (FR-72).
func (a *Approval) Consumed() bool { return a != nil && a.ConsumedAt != nil }

// Invalidated reports whether this approval attempt was invalidated by a later
// approval-relevant edit (FR-68).
func (a *Approval) Invalidated() bool { return a != nil && a.InvalidatedAt != nil }

// Usable reports whether this approval may still be consumed for its effect.
func (a *Approval) Usable() bool {
	return a != nil && !a.Consumed() && !a.Invalidated()
}

// Clone returns a deep copy of the approval record.
func (a *Approval) Clone() *Approval {
	if a == nil {
		return nil
	}
	out := *a
	if a.ConsumedAt != nil {
		consumedAt := *a.ConsumedAt
		out.ConsumedAt = &consumedAt
	}
	if a.InvalidatedAt != nil {
		invalidatedAt := *a.InvalidatedAt
		out.InvalidatedAt = &invalidatedAt
	}
	if a.ConsumedResult != nil {
		result := *a.ConsumedResult
		result.TaskIDs = cloneStrings(a.ConsumedResult.TaskIDs)
		result.ArtifactPaths = cloneStrings(a.ConsumedResult.ArtifactPaths)
		out.ConsumedResult = &result
	}
	return &out
}
