package workspaceplan

import (
	"context"
	"slices"
	"time"
)

// Store is the persistence contract for the Workspace Planning Workflow.
//
// Every method takes the owning workspace ID and scopes its query by it, so a
// Plan ID from one workspace can never read or mutate another's record. A
// mismatch returns ErrPlanNotFound rather than a permission error, so one
// workspace cannot probe another's ID space (FR-163, FR-167).
//
// Implementations return clones. A caller mutating what it received changes
// nothing that is persisted, and a caller cannot reach into another caller's
// view of the same record.
//
// Storage errors never escape: implementations translate them into the typed
// sentinels in errors.go, which is what lets the memory and SQLite stores share
// one contract test.
type Store interface {
	// CreatePlan persists a new Plan. It fails with ErrPlanExists if the ID is
	// taken. The Plan's initiating request is stored verbatim (FR-21).
	CreatePlan(ctx context.Context, plan *Plan) error
	// GetPlan returns one Plan with its clarifications, Task links, and Run
	// links hydrated. It does not compute Progress: that is derived by the
	// service from live Task and Run state (FR-12).
	GetPlan(ctx context.Context, workspaceID, planID string) (*Plan, error)
	// ListPlans returns the workspace's Plans matching the filter, newest
	// activity first.
	ListPlans(ctx context.Context, workspaceID string, filter ListFilter) ([]*Plan, error)

	// UpdatePlanDraft replaces the working draft and its record-level title and
	// objective. expectedRevision is the optimistic-concurrency token the
	// caller last read; a stale value fails with ErrStaleDraft rather than
	// overwriting a concurrent editor (FR-30). It returns the new revision.
	//
	// Clarification answers are never written by this path. The draft body a
	// model regenerated cannot overwrite what a user authored (FR-25).
	UpdatePlanDraft(ctx context.Context, workspaceID, planID string, expectedRevision int64, draft DraftUpdate) (int64, error)
	// SetPlanStatus records a validated transition and appends its activity
	// entry atomically, so a status can never move without leaving a record
	// (FR-14, FR-15).
	SetPlanStatus(ctx context.Context, workspaceID, planID string, to Status, activity Activity) error
	// ArchivePlan moves a Plan to History. It never deletes versions,
	// approvals, Tasks, Runs, or artifacts (FR-16).
	ArchivePlan(ctx context.Context, workspaceID, planID, reason string, at time.Time) error
	// ReopenPlan returns an archived Plan to the Active list. It is refused for
	// a Plan whose status is terminal in a way that cannot be resumed.
	ReopenPlan(ctx context.Context, workspaceID, planID string) error
	// DeletePlan hard-deletes a Plan. It is refused with ErrPlanNotDeletable
	// unless the Plan was never approved and has no linked Tasks, Runs, or
	// artifacts; every other removal archives instead (FR-17).
	DeletePlan(ctx context.Context, workspaceID, planID string) error

	// PutClarifications upserts the question set for a Plan. Existing rows keep
	// their authored answer, answer status, and answer timestamps: only the
	// question text, options, requiredness, and ordering may be rewritten by a
	// regenerated draft (FR-25).
	PutClarifications(ctx context.Context, workspaceID, planID string, questions []Clarification) error
	// AnswerClarification persists one user-authored answer, or records a skip
	// when answered is false (FR-25, FR-28).
	AnswerClarification(ctx context.Context, workspaceID, planID, clarificationID string, answer ClarificationAnswer) error

	// CreateVersion writes an immutable review snapshot and returns it with its
	// assigned number. Numbers are monotonic per Plan and never reused
	// (FR-31).
	CreateVersion(ctx context.Context, version *Version) (*Version, error)
	// GetVersion returns one immutable version.
	GetVersion(ctx context.Context, workspaceID, planID string, number int) (*Version, error)
	// ListVersions returns every retained version, oldest first, so comparison
	// and history views read in one call (FR-35).
	ListVersions(ctx context.Context, workspaceID, planID string) ([]*Version, error)
	// SetVersionDecision records a review outcome once. It writes only the
	// decision columns; snapshot content and its hash stay immutable (FR-31).
	SetVersionDecision(ctx context.Context, workspaceID, planID string, number int, status VersionStatus, decidedBy, reason string, at time.Time) error

	// CreateApproval persists a user's approval of one exact version. A repeat
	// of the same plan and idempotency key returns the original record rather
	// than creating a second one (FR-70, FR-73).
	CreateApproval(ctx context.Context, approval *Approval) (*Approval, error)
	// GetApproval returns one approval record.
	GetApproval(ctx context.Context, workspaceID, planID, approvalID string) (*Approval, error)
	// ListApprovals returns the Plan's approval history, newest first (FR-79).
	ListApprovals(ctx context.Context, workspaceID, planID string) ([]*Approval, error)
	// ConsumeApproval atomically marks an unconsumed approval as spent and
	// records its result. A second attempt fails with ErrApprovalConsumed and
	// leaves the original result intact, which is what makes a retried or
	// raced materialization return one answer (FR-72, FR-178).
	ConsumeApproval(ctx context.Context, workspaceID, planID, approvalID string, result ApprovalResult, at time.Time) error
	// InvalidateApprovals marks every unconsumed approval for a Plan version
	// as invalidated after an approval-relevant edit (FR-68).
	InvalidateApprovals(ctx context.Context, workspaceID, planID string, version int, reason string, at time.Time) error

	// LinkTasks records Plan-to-Task provenance for a materialization. It is
	// idempotent for the same Plan, version, and item: a retry writes nothing
	// new rather than a duplicate Task tree (FR-91).
	LinkTasks(ctx context.Context, workspaceID, planID string, links []TaskLink) error
	// LinkRun records Plan-to-Run provenance for one linked Run.
	LinkRun(ctx context.Context, workspaceID, planID string, link RunLink) error
	// RetireTaskLink marks a link as replaced by a corrective revision. It
	// never deletes the link or the Task (FR-77, FR-78).
	RetireTaskLink(ctx context.Context, workspaceID, planID, taskID, replacedByTaskID, reason string, at time.Time) error
	// PlanForTask and PlanForRun are the reverse lookups that let Task and Run
	// detail resolve their originating Plan (FR-10, FR-148).
	PlanForTask(ctx context.Context, workspaceID, taskID string) (*TaskLink, error)
	PlanForRun(ctx context.Context, workspaceID, runID string) (*RunLink, error)

	// RecordReconciliation stores a user's confirmation of one exact
	// reconciliation preview. Re-confirming the same token returns the
	// original record rather than a second one, so a retried click cannot
	// authorize a second reconciliation (FR-77).
	RecordReconciliation(ctx context.Context, reconciliation *Reconciliation) (*Reconciliation, error)
	// GetReconciliation returns a confirmation by its preview token, or
	// ErrReconciliationNotFound.
	GetReconciliation(ctx context.Context, workspaceID, planID, token string) (*Reconciliation, error)
	// ConsumeReconciliation marks a confirmation as spent. It fails with
	// ErrReconciliationConsumed if it was already applied, which is what makes
	// a confirmation single-use under a concurrent retry.
	ConsumeReconciliation(ctx context.Context, workspaceID, planID, token string, at time.Time) error
	// ListReconciliations returns a Plan's confirmation history, newest first.
	// Applied and unapplied records both stay: the record of what was agreed
	// is history, not bookkeeping to clean up (FR-116).
	ListReconciliations(ctx context.Context, workspaceID, planID string) ([]*Reconciliation, error)

	// AppendActivity writes one append-only history entry and returns it with
	// its assigned sequence (FR-15, FR-80).
	AppendActivity(ctx context.Context, activity Activity) (Activity, error)
	// ListActivity returns a Plan's history in sequence order.
	ListActivity(ctx context.Context, workspaceID, planID string, limit int) ([]Activity, error)

	// PutDraftSnapshot records one autosave recovery point and prunes the Plan
	// to the newest keep snapshots. Snapshots are never review versions
	// (FR-30).
	PutDraftSnapshot(ctx context.Context, snapshot *DraftSnapshot, keep int) error
	// ListDraftSnapshots returns recovery points newest first.
	ListDraftSnapshots(ctx context.Context, workspaceID, planID string) ([]*DraftSnapshot, error)
	// PruneDraftSnapshots drops recovery points older than the cutoff and any
	// beyond the newest keep. It never touches immutable versions (FR-30).
	PruneDraftSnapshots(ctx context.Context, workspaceID, planID string, keep int, olderThan time.Time) (int, error)
}

// ListFilter selects which of a workspace's Plans to return. The zero value
// returns active Plans, which is what the Plans destination opens on (FR-146).
type ListFilter struct {
	// Scope selects Active, History, or both.
	Scope ListScope
	// Statuses optionally narrows to specific lifecycle states.
	Statuses []Status
	// Limit caps the result. Zero means no cap.
	Limit int
}

// ListScope is the Active/History split shown on the Plans destination
// (FR-146).
type ListScope string

const (
	// ScopeActive returns Plans that are not archived.
	ScopeActive ListScope = "active"
	// ScopeHistory returns archived Plans.
	ScopeHistory ListScope = "history"
	// ScopeAll returns both.
	ScopeAll ListScope = "all"
)

// Normalized returns the filter with defaults applied.
func (f ListFilter) Normalized() ListFilter {
	out := f
	switch out.Scope {
	case ScopeActive, ScopeHistory, ScopeAll:
	default:
		out.Scope = ScopeActive
	}
	if out.Limit < 0 {
		out.Limit = 0
	}
	return out
}

// matches reports whether a Plan belongs in this filter's result. Both stores
// share it so Active/History membership cannot drift between them.
func (f ListFilter) matches(plan *Plan) bool {
	archived := plan.ArchivedAt != nil
	switch f.Scope {
	case ScopeActive:
		if archived {
			return false
		}
	case ScopeHistory:
		if !archived {
			return false
		}
	}
	if len(f.Statuses) == 0 {
		return true
	}
	return slices.Contains(f.Statuses, plan.Status)
}

// DraftUpdate is one accepted write to the working draft. Clarifications inside
// Content are treated as the question set only: their answers are ignored here
// and can be changed solely through AnswerClarification (FR-25).
type DraftUpdate struct {
	Title     string
	Objective string
	Content   PlanContent
	// Intent classifies a draft derived from approved work (FR-39). It is
	// empty for a Plan that has never been approved.
	Intent RevisionIntent
	// UpdatedAt is the activity timestamp for retention (FR-16).
	UpdatedAt time.Time
}

// ClarificationAnswer is one user-authored response to a clarification question.
type ClarificationAnswer struct {
	// Answered distinguishes an answer from a skip. A skip on an optional
	// question records an assumption in the draft instead (FR-28).
	Answered bool
	Answer   string
	// SkipReason explains a skip when the user gave one.
	SkipReason string
	// AnsweredBy is the user who authored the response.
	AnsweredBy string
	At         time.Time
}
