package workspaceplan

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Review and approval.
//
// This is the file the whole feature exists to make trustworthy. Its job is to
// guarantee one sentence: the version a user read is exactly the version that
// creates work.
//
// Three mechanisms hold that together, and none of them trusts the caller:
//
//   - An immutable version is snapshotted at review time and never edited
//     afterwards (FR-31).
//   - An approval binds to that version's content hash. Any approval-relevant
//     edit produces a different hash, and the server refuses an approval whose
//     hash no longer matches (FR-32, FR-69).
//   - Approval is a user action. No preset, confirmation mode, agent, tool, or
//     workspace file can reach it (FR-59, FR-60, FR-75).

// ReviewInput requests that the current draft become an immutable version.
type ReviewInput struct {
	Actor string
	// Policy is the effective enforced policy this version is reviewed under.
	// It is snapshotted with the version so a later Settings change cannot
	// rewrite what an approval meant (FR-143, FR-144).
	Policy PolicySnapshot
	// Validation carries the agents and capabilities that actually exist, so a
	// plan naming an agent that has since disappeared cannot reach review
	// (FR-48).
	Validation ValidationContext
	// Intent classifies a version derived from already-approved work (FR-39).
	Intent RevisionIntent
}

// RequestReview snapshots the working draft as an immutable version and moves
// the Plan to in_review (FR-31).
//
// The draft must be fully valid here — unlike an in-progress save, which may be
// incomplete. A version is what a user will be asked to approve, so it cannot
// contain an empty group or a dangling dependency.
func (s *Service) RequestReview(ctx context.Context, workspaceID, planID string, input ReviewInput) (*Version, error) {
	plan, err := s.store.GetPlan(ctx, workspaceID, planID)
	if err != nil {
		return nil, err
	}
	if err := ValidateTransition(plan.Status, StatusInReview, SourceUser); err != nil {
		return nil, err
	}
	if open := plan.Draft.UnansweredRequired(); len(open) > 0 {
		return nil, fmt.Errorf("%w: %d required question(s) are unanswered",
			ErrValidation, len(open))
	}

	if result := ValidatePlanContent(plan.Objective, plan.Draft, input.Validation); !result.OK() {
		return nil, result.Error()
	}

	// The version cap offers split or supersession rather than deleting
	// history or truncating the draft (FR-31).
	existing, err := s.store.ListVersions(ctx, workspaceID, planID)
	if err != nil {
		return nil, err
	}
	if len(existing) >= MaxReviewVersions {
		return nil, fmt.Errorf(
			"%w: this plan already has %d review versions, the maximum. "+
				"Split the remaining work into a new plan, or supersede this one — "+
				"no version will be deleted to make room",
			ErrLimitExceeded, len(existing))
	}

	hash, err := ContentHash(plan.Objective, plan.Draft, input.Policy)
	if err != nil {
		return nil, err
	}

	now := s.now()
	version, err := s.store.CreateVersion(ctx, &Version{
		PlanID:         plan.ID,
		WorkspaceID:    plan.WorkspaceID,
		Title:          plan.Title,
		Objective:      plan.Objective,
		Content:        plan.Draft,
		ContentHash:    hash,
		PolicySnapshot: input.Policy,
		Intent:         orDefaultIntent(input.Intent, plan.DraftIntent),
		Status:         VersionInReview,
		CreatedAt:      now,
		CreatedBy:      Origin{Kind: OriginUser, Actor: input.Actor},
	})
	if err != nil {
		return nil, err
	}

	change := NewStatusChange(plan, StatusInReview, SourceUser, input.Actor, "")
	change.Kind = ActivityReviewRequested
	change.Version = version.Number
	change.CreatedAt = now
	if err := s.setStatus(ctx, workspaceID, planID, StatusInReview, change); err != nil {
		return nil, err
	}
	return version, nil
}

// DecisionInput records a reviewer's rejection or request for changes.
type DecisionInput struct {
	Actor string
	// Version is the version being decided on. Deciding on a version that is
	// no longer current is refused, so two reviewers cannot act on different
	// snapshots and both believe they decided the same thing.
	Version int
	Reason  string
}

// RequestChanges returns a reviewed version to editing while retaining it
// (FR-37, FR-67).
func (s *Service) RequestChanges(ctx context.Context, workspaceID, planID string, input DecisionInput) (*Plan, error) {
	return s.decide(ctx, workspaceID, planID, input,
		VersionChangesRequested, ActivityChangesRequested)
}

// Reject records an explicit rejection with an optional reason (FR-66).
//
// Like request-changes, it retains the version: the record of what was proposed
// and turned down is part of the Plan's history (FR-37).
func (s *Service) Reject(ctx context.Context, workspaceID, planID string, input DecisionInput) (*Plan, error) {
	return s.decide(ctx, workspaceID, planID, input, VersionRejected, ActivityRejected)
}

func (s *Service) decide(ctx context.Context, workspaceID, planID string, input DecisionInput,
	outcome VersionStatus, kind ActivityKind) (*Plan, error) {
	plan, err := s.store.GetPlan(ctx, workspaceID, planID)
	if err != nil {
		return nil, err
	}
	if plan.Status != StatusInReview {
		return nil, fmt.Errorf("%w: only a plan in review can be decided on, this one is %s",
			ErrInvalidTransition, plan.Status)
	}
	if input.Version != 0 && input.Version != plan.CurrentVersion {
		return nil, fmt.Errorf("%w: version %d is not the version under review (%d)",
			ErrStaleVersion, input.Version, plan.CurrentVersion)
	}

	now := s.now()
	if err := s.store.SetVersionDecision(ctx, workspaceID, planID, plan.CurrentVersion,
		outcome, input.Actor, input.Reason, now); err != nil {
		return nil, err
	}
	// A decision invalidates any approval attempt outstanding against that
	// version: it is no longer approvable (FR-68, FR-74).
	if err := s.store.InvalidateApprovals(ctx, workspaceID, planID, plan.CurrentVersion,
		"the reviewed version was "+string(outcome), now); err != nil {
		return nil, err
	}

	change := NewStatusChange(plan, StatusDraft, SourceUser, input.Actor, input.Reason)
	change.Kind = kind
	change.Version = plan.CurrentVersion
	change.CreatedAt = now
	if err := s.store.SetPlanStatus(ctx, workspaceID, planID, StatusDraft, change); err != nil {
		return nil, err
	}
	return s.Get(ctx, workspaceID, planID)
}

// ApprovalRequest is a user's decision to approve one exact version.
type ApprovalRequest struct {
	// Version and ContentHash identify exactly what the user read. Both are
	// checked against current state; either mismatching refuses the approval
	// (FR-61, FR-69).
	Version     int
	ContentHash string
	// Effect is what the user was told approval would do. It is checked
	// against what the version actually declares, so a client cannot ask for
	// "create tasks" and receive "create tasks and start" — or the reverse
	// (FR-63, FR-69).
	Effect ApprovalEffect
	// UserID and UserName identify the approver (FR-70, FR-87).
	UserID   string
	UserName string
	// IdempotencyKey makes a retried request return the original approval
	// rather than creating a second one (FR-73).
	IdempotencyKey string
}

// Approve records a user's approval of one exact Plan version (FR-70).
//
// It creates no Tasks and starts nothing. Approval is the authorization;
// materialization consumes it separately, which is what lets a retry replay the
// original result instead of doing the work twice (FR-72, FR-73).
func (s *Service) Approve(ctx context.Context, workspaceID, planID string, req ApprovalRequest) (*Approval, error) {
	plan, err := s.store.GetPlan(ctx, workspaceID, planID)
	if err != nil {
		return nil, err
	}

	// Approval is a user action, and the transition table is where that is
	// enforced. It holds under every preset and confirmation mode: Autonomous
	// changes what happens after approval, never whether approval happened
	// (FR-59, FR-75).
	if err := ValidateTransition(plan.Status, StatusApproved, SourceUser); err != nil {
		return nil, err
	}
	if !req.Effect.Valid() {
		return nil, fmt.Errorf("%w: unsupported approval effect %q", ErrValidation, req.Effect)
	}
	if strings.TrimSpace(req.IdempotencyKey) == "" {
		return nil, fmt.Errorf("%w: an approval requires an idempotency key", ErrValidation)
	}

	version, err := s.store.GetVersion(ctx, workspaceID, planID, req.Version)
	if err != nil {
		return nil, err
	}
	// A stale, rejected, or superseded version is not approvable, whatever the
	// request says (FR-74).
	if !version.Status.Approvable() {
		return nil, fmt.Errorf("%w: version %d is %s and cannot be approved",
			ErrStaleVersion, version.Number, version.Status)
	}
	if version.Number != plan.CurrentVersion {
		return nil, fmt.Errorf("%w: version %d is not the current version (%d)",
			ErrStaleVersion, version.Number, plan.CurrentVersion)
	}
	// The hash check is what makes "the version you read is the version you
	// approved" true. Any approval-relevant edit since the view loaded
	// produces a different hash and lands here (FR-68, FR-69).
	if req.ContentHash != version.ContentHash {
		return nil, fmt.Errorf(
			"%w: this plan changed since you reviewed it. Re-read version %d before approving",
			ErrApprovalMismatch, version.Number)
	}
	// The declared effect must match what the version actually asks for, so
	// the label the user clicked is the behavior they get (FR-63, FR-64).
	if expected := EffectFor(version.Content.Execution.Mode); expected != req.Effect {
		return nil, fmt.Errorf(
			"%w: this version's execution mode is %q, which means %q, not %q",
			ErrApprovalMismatch, version.Content.Execution.Mode,
			expected.ActionLabel(), req.Effect.ActionLabel())
	}

	now := s.now()
	approval, err := s.store.CreateApproval(ctx, &Approval{
		PlanID:         planID,
		WorkspaceID:    workspaceID,
		Version:        version.Number,
		ContentHash:    version.ContentHash,
		Effect:         req.Effect,
		UserID:         req.UserID,
		UserName:       req.UserName,
		IdempotencyKey: req.IdempotencyKey,
		CreatedAt:      now,
	})
	if err != nil {
		return nil, err
	}

	entry := NewActivity(plan, ActivityApproved, SourceUser, req.UserName, "")
	entry.Version = version.Number
	entry.ApprovalID = approval.ID
	entry.CreatedAt = now
	if _, err := s.store.AppendActivity(ctx, entry); err != nil {
		return nil, err
	}
	return approval, nil
}

// EffectFor maps an execution mode to the approval effect it implies, and
// therefore to the label the primary action must carry (FR-64, FR-65).
func EffectFor(mode ExecutionMode) ApprovalEffect {
	if mode == ExecutionAuto {
		return EffectCreateTasksAndStart
	}
	return EffectCreateTasks
}

// InvalidateOutstandingApprovals marks every unconsumed approval for a Plan's
// current version as invalid after an approval-relevant edit (FR-68).
//
// It is called by the edit path rather than left to approval time so the UI can
// tell a user their pending approval died, instead of only discovering it when
// they click.
func (s *Service) InvalidateOutstandingApprovals(ctx context.Context, workspaceID, planID, reason string) error {
	plan, err := s.store.GetPlan(ctx, workspaceID, planID)
	if err != nil {
		return err
	}
	if plan.CurrentVersion == 0 {
		return nil
	}
	now := s.now()
	if err := s.store.InvalidateApprovals(ctx, workspaceID, planID, plan.CurrentVersion, reason, now); err != nil {
		return err
	}
	entry := NewActivity(plan, ActivityApprovalInvalidated, SourceService, "", reason)
	entry.Version = plan.CurrentVersion
	entry.CreatedAt = now
	_, err = s.store.AppendActivity(ctx, entry)
	return err
}

// ReviewContract is everything a user must see before approving (FR-62, FR-63).
//
// It is assembled server-side rather than composed by the client so the summary
// of effects cannot drift from what approval will actually do.
type ReviewContract struct {
	PlanID      string `json:"plan_id"`
	WorkspaceID string `json:"studio_id"`
	Version     int    `json:"version"`
	// ContentHash is what the approval will bind to. The client returns it
	// unchanged, which is how a stale view is caught (FR-61).
	ContentHash string `json:"content_hash"`

	Title     string `json:"title"`
	Objective string `json:"objective"`
	// OriginalRequest is shown alongside the objective so a reviewer can see
	// whether the plan answers what was actually asked (FR-21).
	OriginalRequest string   `json:"original_request"`
	InScope         []string `json:"in_scope"`
	NonGoals        []string `json:"non_goals"`

	Assumptions []Assumption           `json:"assumptions,omitempty"`
	Risks       []Risk                 `json:"risks,omitempty"`
	Groups      []TaskGroup            `json:"groups"`
	Validations []ValidationCheckpoint `json:"validations,omitempty"`

	// TaskCount, Assignees, Capabilities, and Dependencies summarize what the
	// task hierarchy commits to, so the numbers are not left to the reader to
	// count (FR-62).
	TaskCount    int      `json:"task_count"`
	GroupCount   int      `json:"group_count"`
	Assignees    []string `json:"assignees"`
	Unassigned   int      `json:"unassigned"`
	Capabilities []string `json:"required_capabilities"`
	Dependencies int      `json:"dependency_count"`

	// Effects is the plain list of what approval authorizes (FR-63).
	Effects []string `json:"effects"`
	// Effect and ActionLabel are the declared consequence and the exact
	// wording the primary action must use (FR-64, FR-65).
	Effect      ApprovalEffect `json:"effect"`
	ActionLabel string         `json:"action_label"`
	// StartsExecution says plainly whether work begins on approval.
	StartsExecution bool `json:"starts_execution"`

	ExecutionMode ExecutionMode  `json:"execution_mode"`
	Preconditions []string       `json:"enforced_preconditions"`
	Policy        PolicySnapshot `json:"policy_snapshot"`
	Artifacts     []ArtifactPlan `json:"artifacts,omitempty"`

	// Blockers are the reasons this version cannot be approved right now. A
	// non-empty list means the approval action must be disabled rather than
	// failing on click (FR-48).
	Blockers []string `json:"blockers,omitempty"`
	// Approvable is false when anything blocks approval.
	Approvable bool `json:"approvable"`
}

// ArtifactPlan is one file approval would authorize writing (FR-63, FR-98).
type ArtifactPlan struct {
	ID      string       `json:"id"`
	Kind    ArtifactKind `json:"kind"`
	Path    string       `json:"path"`
	Enabled bool         `json:"enabled"`
}

// BuildReviewContract assembles the approval view for one version.
//
// Everything a reviewer is shown, and the effect they will authorize, is
// derived here from the version itself — so the button's label and the
// behaviour behind it cannot disagree.
func (s *Service) BuildReviewContract(ctx context.Context, workspaceID, planID string, number int, vctx ValidationContext) (*ReviewContract, error) {
	plan, err := s.store.GetPlan(ctx, workspaceID, planID)
	if err != nil {
		return nil, err
	}
	version, err := s.store.GetVersion(ctx, workspaceID, planID, number)
	if err != nil {
		return nil, err
	}

	effect := EffectFor(version.Content.Execution.Mode)
	contract := &ReviewContract{
		PlanID:          plan.ID,
		WorkspaceID:     plan.WorkspaceID,
		Version:         version.Number,
		ContentHash:     version.ContentHash,
		Title:           version.Title,
		Objective:       version.Objective,
		OriginalRequest: plan.OriginalRequest,
		InScope:         version.Content.InScope,
		NonGoals:        version.Content.NonGoals,
		Assumptions:     version.Content.Assumptions,
		Risks:           version.Content.Risks,
		Groups:          version.Content.Groups,
		Validations:     version.Content.Validations,
		GroupCount:      len(version.Content.Groups),
		TaskCount:       version.Content.ActionableItemCount(),
		Effect:          effect,
		ActionLabel:     effect.ActionLabel(),
		StartsExecution: effect.StartsExecution(),
		ExecutionMode:   version.Content.Execution.Mode,
		Preconditions:   version.Content.Execution.Preconditions,
		Policy:          version.PolicySnapshot,
	}

	assignees := map[string]struct{}{}
	capabilities := map[string]struct{}{}
	version.Content.EachItem(func(_ TaskGroup, item TaskItem) bool {
		if item.Assignee == "" {
			contract.Unassigned++
		} else {
			assignees[item.Assignee] = struct{}{}
		}
		for _, capability := range item.RequiredCapabilities {
			capabilities[capability] = struct{}{}
		}
		contract.Dependencies += len(item.DependsOn)
		return true
	})
	contract.Assignees = sortedKeys(assignees)
	contract.Capabilities = sortedKeys(capabilities)

	for _, artifact := range version.Content.Artifacts {
		contract.Artifacts = append(contract.Artifacts, ArtifactPlan{
			ID: artifact.ID, Kind: artifact.Kind,
			Path: artifact.Path, Enabled: artifact.Enabled,
		})
	}

	contract.Effects = describeEffects(version, effect)
	contract.Blockers = approvalBlockers(plan, version, vctx)
	contract.Approvable = len(contract.Blockers) == 0 && version.Status.Approvable()
	return contract, nil
}

// describeEffects states every side effect approval authorizes, in plain
// sentences. A user should not have to infer "and it will start running" from
// an execution-mode enum (FR-63).
func describeEffects(version *Version, effect ApprovalEffect) []string {
	effects := []string{
		fmt.Sprintf("Create %d workspace task(s) in %d group(s)",
			version.Content.ActionableItemCount(), len(version.Content.Groups)),
	}
	for _, artifact := range version.Content.EnabledArtifacts() {
		effects = append(effects, fmt.Sprintf("Write %s to %s", artifact.Kind, artifact.Path))
	}
	for _, precondition := range version.Content.Execution.Preconditions {
		effects = append(effects, fmt.Sprintf("Run the %s check before code work begins", precondition))
	}
	if effect.StartsExecution() {
		effects = append(effects,
			"Start running eligible tasks automatically once they are created")
	} else {
		effects = append(effects,
			"Nothing starts running until you start it")
	}
	return effects
}

// approvalBlockers lists what stands between this version and approval, so the
// action can be disabled with a reason rather than failing on click.
func approvalBlockers(plan *Plan, version *Version, vctx ValidationContext) []string {
	var blockers []string

	if version.Number != plan.CurrentVersion {
		blockers = append(blockers, fmt.Sprintf(
			"Version %d is no longer the current version (%d)", version.Number, plan.CurrentVersion))
	}
	if !version.Status.Approvable() {
		blockers = append(blockers, fmt.Sprintf("This version was %s", version.Status))
	}
	if plan.Status != StatusInReview {
		blockers = append(blockers, fmt.Sprintf("This plan is %s, not in review", plan.Status))
	}

	// An assignee that disappeared since review blocks approval until it is
	// removed, replaced, or left unassigned (FR-48).
	if vctx.AvailableAgents != nil {
		available := toSet(vctx.AvailableAgents)
		missing := map[string]struct{}{}
		version.Content.EachItem(func(_ TaskGroup, item TaskItem) bool {
			if item.Assignee == "" {
				return true
			}
			if _, ok := available[item.Assignee]; !ok {
				missing[item.Assignee] = struct{}{}
			}
			return true
		})
		for _, name := range sortedKeys(missing) {
			blockers = append(blockers, fmt.Sprintf(
				"Agent %q is no longer available; reassign that task or leave it unassigned", name))
		}
	}
	return blockers
}

func sortedKeys(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sortStrings(out)
	return out
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

// Versions returns the Plan's retained review versions, oldest first (FR-35).
func (s *Service) Versions(ctx context.Context, workspaceID, planID string) ([]*Version, error) {
	return s.store.ListVersions(ctx, workspaceID, planID)
}

// Compare returns the difference between two retained versions (FR-35, FR-36).
func (s *Service) Compare(ctx context.Context, workspaceID, planID string, from, to int) (VersionDiff, error) {
	first, err := s.store.GetVersion(ctx, workspaceID, planID, from)
	if err != nil {
		return VersionDiff{}, err
	}
	second, err := s.store.GetVersion(ctx, workspaceID, planID, to)
	if err != nil {
		return VersionDiff{}, err
	}
	return CompareVersions(first, second), nil
}

// Approvals returns the Plan's approval history, newest first (FR-79).
func (s *Service) Approvals(ctx context.Context, workspaceID, planID string) ([]*Approval, error) {
	return s.store.ListApprovals(ctx, workspaceID, planID)
}

// EditApproved starts a new working draft from an approved Plan (FR-38).
//
// It never mutates the approved version. The new draft must declare whether it
// is additive, corrective, or superseding, because that decides what
// reconciliation may do to work the earlier approval already created (FR-39).
func (s *Service) EditApproved(ctx context.Context, workspaceID, planID string, intent RevisionIntent, actor string) (*Plan, error) {
	plan, err := s.store.GetPlan(ctx, workspaceID, planID)
	if err != nil {
		return nil, err
	}
	if plan.ApprovedVersion == 0 {
		return nil, fmt.Errorf("%w: this plan has no approved version to revise", ErrInvalidTransition)
	}
	switch intent {
	case RevisionAdditive, RevisionCorrective, RevisionSuperseding:
	default:
		return nil, fmt.Errorf(
			"%w: a revision of approved work must declare whether it is additive, corrective, or superseding",
			ErrValidation)
	}
	if err := ValidateTransition(plan.Status, StatusDraft, SourceUser); err != nil {
		return nil, err
	}

	now := s.now()
	// The draft starts from the approved version's content, so a revision
	// begins from what was agreed rather than from whatever the draft happened
	// to hold.
	approved, err := s.store.GetVersion(ctx, workspaceID, planID, plan.ApprovedVersion)
	if err != nil {
		return nil, err
	}
	if _, err := s.store.UpdatePlanDraft(ctx, workspaceID, planID, plan.DraftRevision, DraftUpdate{
		Title:     approved.Title,
		Objective: approved.Objective,
		Content:   approved.Content,
		Intent:    intent,
		UpdatedAt: now,
	}); err != nil {
		return nil, err
	}

	change := NewStatusChange(plan, StatusDraft, SourceUser, actor,
		fmt.Sprintf("started a %s revision of version %d", intent, plan.ApprovedVersion))
	change.Version = plan.ApprovedVersion
	change.CreatedAt = now
	if err := s.store.SetPlanStatus(ctx, workspaceID, planID, StatusDraft, change); err != nil {
		return nil, err
	}
	return s.Get(ctx, workspaceID, planID)
}

// consumeApprovalWindow is how long a created-but-unconsumed approval stays
// usable. It exists so an approval that was never materialized (a crash between
// approve and materialize) does not sit indefinitely as a live authorization.
const consumeApprovalWindow = 24 * time.Hour

// UsableApproval returns the approval that may still be consumed for a Plan
// version, or nil when there is none.
func (s *Service) UsableApproval(ctx context.Context, workspaceID, planID string, version int) (*Approval, error) {
	approvals, err := s.store.ListApprovals(ctx, workspaceID, planID)
	if err != nil {
		return nil, err
	}
	cutoff := s.now().Add(-consumeApprovalWindow)
	for _, approval := range approvals {
		if approval.Version != version || !approval.Usable() {
			continue
		}
		if approval.CreatedAt.Before(cutoff) {
			continue
		}
		return approval, nil
	}
	return nil, nil
}
