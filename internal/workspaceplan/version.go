package workspaceplan

import (
	"maps"
	"time"
)

// Version is an immutable review snapshot of Plan content (FR-31). Once
// written, Title, Objective, Content, ContentHash, and PolicySnapshot never
// change: the whole point of the record is that the version a user approved is
// exactly the version that creates Tasks.
//
// The review decision fields at the bottom are written once, when a reviewer
// acts on the version. They describe what happened to the snapshot, not what is
// in it, so setting them leaves the content hash untouched.
type Version struct {
	PlanID      string `json:"plan_id"`
	WorkspaceID string `json:"studio_id"`
	// Number is monotonically increasing per Plan, starting at 1. Numbers are
	// never reused, including after a rejection (FR-31).
	Number int `json:"version"`

	Title     string      `json:"title"`
	Objective string      `json:"objective"`
	Content   PlanContent `json:"content"`
	// ContentHash is the deterministic hash over every approval-relevant field
	// (FR-32, FR-33). An approval binds to it, so any approval-relevant edit
	// produces a different hash and invalidates the outstanding approval view.
	ContentHash string `json:"content_hash"`
	// PolicySnapshot freezes the effective enforced policy this version was
	// reviewed under, so a later Settings change cannot rewrite the meaning of
	// an approval after the fact (FR-143, FR-144).
	PolicySnapshot PolicySnapshot `json:"policy_snapshot"`
	// Intent classifies a version derived from already-approved work (FR-39).
	Intent RevisionIntent `json:"intent,omitempty"`

	Status    VersionStatus `json:"status"`
	CreatedAt time.Time     `json:"created_at"`
	CreatedBy Origin        `json:"created_by"`

	// DecidedAt, DecidedBy, and DecisionReason record the review outcome once.
	DecidedAt      *time.Time `json:"decided_at,omitempty"`
	DecidedBy      string     `json:"decided_by,omitempty"`
	DecisionReason string     `json:"decision_reason,omitempty"`
}

// VersionStatus is the review outcome of one immutable version. It is separate
// from the Plan's own status: a Plan back in draft still has a retained
// rejected or changes-requested version behind it (FR-37).
type VersionStatus string

const (
	// VersionInReview is awaiting a decision.
	VersionInReview VersionStatus = "in_review"
	// VersionApproved was approved by an explicit user action.
	VersionApproved VersionStatus = "approved"
	// VersionRejected was rejected, with an optional reason (FR-66).
	VersionRejected VersionStatus = "rejected"
	// VersionChangesRequested was sent back to editing and retained (FR-67).
	VersionChangesRequested VersionStatus = "changes_requested"
	// VersionSuperseded was replaced by a later approved version.
	VersionSuperseded VersionStatus = "superseded"
)

// Approvable reports whether a version in this state may still be approved. A
// stale, rejected, cancelled, or superseded version never can (FR-74).
func (s VersionStatus) Approvable() bool { return s == VersionInReview }

// PolicySnapshot is the effective enforced policy captured with a review
// version (FR-144). Group 8 owns resolving it; the Plan store only needs to
// persist and return it verbatim so audits can explain past behavior.
type PolicySnapshot struct {
	// Profile and Preset are the workspace type and settings bundle in force.
	Profile string `json:"profile,omitempty"`
	Preset  string `json:"preset,omitempty"`
	// Enforced maps each compiled enforcement adapter key to whether it was
	// active for this version. Only adapters that actually exist appear here:
	// a control Ori cannot enforce is never recorded as enforced (FR-127).
	Enforced map[string]bool `json:"enforced,omitempty"`
	// Unavailable maps an adapter key to a machine-readable reason it could
	// not be applied in this workspace (FR-127, FR-128).
	Unavailable map[string]string `json:"unavailable,omitempty"`
	// ExecutionMode is the execution behavior the policy allowed.
	ExecutionMode ExecutionMode `json:"execution_mode,omitempty"`
	// CapturedAt is when the snapshot was taken.
	CapturedAt time.Time `json:"captured_at"`
}

// Clone returns a deep copy of the snapshot.
func (p PolicySnapshot) Clone() PolicySnapshot {
	out := p
	if p.Enforced != nil {
		out.Enforced = maps.Clone(p.Enforced)
	}
	if p.Unavailable != nil {
		out.Unavailable = maps.Clone(p.Unavailable)
	}
	return out
}

// Clone returns a deep copy of the version.
func (v *Version) Clone() *Version {
	if v == nil {
		return nil
	}
	out := *v
	out.Content = v.Content.Clone()
	out.PolicySnapshot = v.PolicySnapshot.Clone()
	if v.DecidedAt != nil {
		decidedAt := *v.DecidedAt
		out.DecidedAt = &decidedAt
	}
	return &out
}

// DraftSnapshot is an autosave recovery point for the working draft. Snapshots
// exist so a conflicted or accidentally overwritten edit can be recovered; they
// are deliberately not review versions and never count toward the 50-version
// limit (FR-30, FR-31).
type DraftSnapshot struct {
	ID          string `json:"id"`
	PlanID      string `json:"plan_id"`
	WorkspaceID string `json:"studio_id"`
	// DraftRevision is the working-draft revision this snapshot captured.
	DraftRevision int64       `json:"draft_revision"`
	Title         string      `json:"title"`
	Objective     string      `json:"objective"`
	Content       PlanContent `json:"content"`
	CreatedAt     time.Time   `json:"created_at"`
}

// NewDraftSnapshotID returns a stable ID for one autosave recovery snapshot.
func NewDraftSnapshotID() string { return snapshotIDPrefix + newUUID() }

const snapshotIDPrefix = "snap_"

// Clone returns a deep copy of the snapshot.
func (s *DraftSnapshot) Clone() *DraftSnapshot {
	if s == nil {
		return nil
	}
	out := *s
	out.Content = s.Content.Clone()
	return &out
}
