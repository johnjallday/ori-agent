package workspaceplan

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

// MemoryStore is a deterministic in-memory Store. It is the store unit tests
// run against and the fallback for callers with no database, so it implements
// the same invariants as the SQLite store rather than a looser approximation:
// optimistic draft concurrency, one-shot approval consumption, idempotent Task
// linkage, and authored answers that a draft write cannot overwrite.
type MemoryStore struct {
	mu    sync.Mutex
	plans map[string]*planRecord
}

// planRecord holds one Plan and everything stored alongside it. Keeping the
// dependent records here rather than in parallel maps is what makes deleting a
// Plan, and scoping every read by workspace, a single operation.
type planRecord struct {
	plan           *Plan
	clarifications []Clarification
	versions       []*Version
	approvals      []*Approval
	taskLinks      []TaskLink
	runLinks       []RunLink
	activity       []Activity
	snapshots      []*DraftSnapshot
	reconciles     []*Reconciliation
	sequence       int64
}

// NewMemoryStore returns an empty in-memory Plan store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{plans: make(map[string]*planRecord)}
}

var _ Store = (*MemoryStore)(nil)

// lookup returns the record for a Plan owned by the given workspace. A Plan ID
// belonging to another workspace is reported as missing, never as forbidden
// (FR-163, FR-167).
func (s *MemoryStore) lookup(workspaceID, planID string) (*planRecord, error) {
	record, ok := s.plans[planID]
	if !ok || record.plan.WorkspaceID != workspaceID {
		return nil, fmt.Errorf("%w: %s", ErrPlanNotFound, planID)
	}
	return record, nil
}

func (s *MemoryStore) CreatePlan(_ context.Context, plan *Plan) error {
	if plan == nil {
		return fmt.Errorf("%w: plan is nil", ErrValidation)
	}
	if plan.ID == "" || plan.WorkspaceID == "" {
		return fmt.Errorf("%w: plan requires an ID and an owning workspace", ErrValidation)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.plans[plan.ID]; exists {
		return fmt.Errorf("%w: %s", ErrPlanExists, plan.ID)
	}
	stored := plan.Clone()
	// Progress is derived on read and is never persisted (FR-12).
	stored.Progress = nil
	// Clarifications live in their own collection so a later draft write
	// cannot carry an answer with it (FR-25).
	clarifications := append([]Clarification(nil), stored.Draft.Clarifications...)
	stored.Draft.Clarifications = nil
	stored.TaskLinks = nil
	stored.RunLinks = nil

	s.plans[plan.ID] = &planRecord{plan: stored, clarifications: clarifications}
	return nil
}

func (s *MemoryStore) GetPlan(_ context.Context, workspaceID, planID string) (*Plan, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	record, err := s.lookup(workspaceID, planID)
	if err != nil {
		return nil, err
	}
	return record.hydrated(), nil
}

// hydrated returns the Plan with its separately stored clarifications and
// provenance links folded back in.
func (r *planRecord) hydrated() *Plan {
	out := r.plan.Clone()
	out.Draft.Clarifications = append([]Clarification(nil), r.clarifications...)
	out.TaskLinks = cloneTaskLinks(r.taskLinks)
	out.RunLinks = cloneRunLinks(r.runLinks)
	return out
}

func (s *MemoryStore) ListPlans(_ context.Context, workspaceID string, filter ListFilter) ([]*Plan, error) {
	filter = filter.Normalized()

	s.mu.Lock()
	defer s.mu.Unlock()

	var out []*Plan
	for _, record := range s.plans {
		if record.plan.WorkspaceID != workspaceID {
			continue
		}
		if !filter.matches(record.plan) {
			continue
		}
		out = append(out, record.hydrated())
	}
	sortPlansByActivity(out)
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

// sortPlansByActivity orders newest activity first, breaking ties on ID so the
// order is stable for tests and pagination.
func sortPlansByActivity(plans []*Plan) {
	sort.Slice(plans, func(i, j int) bool {
		if !plans[i].LastActivityAt.Equal(plans[j].LastActivityAt) {
			return plans[i].LastActivityAt.After(plans[j].LastActivityAt)
		}
		return plans[i].ID < plans[j].ID
	})
}

func (s *MemoryStore) UpdatePlanDraft(_ context.Context, workspaceID, planID string, expectedRevision int64, draft DraftUpdate) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	record, err := s.lookup(workspaceID, planID)
	if err != nil {
		return 0, err
	}
	if record.plan.DraftRevision != expectedRevision {
		return 0, fmt.Errorf("%w: plan is at revision %d, write carried %d",
			ErrStaleDraft, record.plan.DraftRevision, expectedRevision)
	}

	content := draft.Content.Clone()
	// The question set may be rewritten by a regenerated draft; the answers may
	// not. PutClarifications is the only path that touches the stored
	// questions, and AnswerClarification the only path that touches an answer
	// (FR-25).
	content.Clarifications = nil

	record.plan.Title = draft.Title
	record.plan.Objective = draft.Objective
	record.plan.Draft = content
	record.plan.DraftIntent = draft.Intent
	record.plan.DraftRevision++
	record.plan.UpdatedAt = draft.UpdatedAt
	record.plan.LastActivityAt = draft.UpdatedAt
	return record.plan.DraftRevision, nil
}

func (s *MemoryStore) SetPlanStatus(_ context.Context, workspaceID, planID string, to Status, activity Activity) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	record, err := s.lookup(workspaceID, planID)
	if err != nil {
		return err
	}
	record.plan.Status = to
	record.plan.UpdatedAt = activity.CreatedAt
	record.plan.LastActivityAt = activity.CreatedAt
	record.appendActivity(activity)
	return nil
}

// appendActivity assigns the next sequence and stores the entry. The caller
// holds the lock.
func (r *planRecord) appendActivity(activity Activity) Activity {
	r.sequence++
	activity.Sequence = r.sequence
	if activity.ID == "" {
		activity.ID = NewActivityID()
	}
	if activity.PlanID == "" {
		activity.PlanID = r.plan.ID
	}
	if activity.WorkspaceID == "" {
		activity.WorkspaceID = r.plan.WorkspaceID
	}
	r.activity = append(r.activity, activity)
	return activity
}

func (s *MemoryStore) ArchivePlan(_ context.Context, workspaceID, planID, reason string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	record, err := s.lookup(workspaceID, planID)
	if err != nil {
		return err
	}
	archivedAt := at
	record.plan.ArchivedAt = &archivedAt
	record.plan.ArchiveReason = reason
	record.plan.UpdatedAt = at
	return nil
}

func (s *MemoryStore) ReopenPlan(_ context.Context, workspaceID, planID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	record, err := s.lookup(workspaceID, planID)
	if err != nil {
		return err
	}
	record.plan.ArchivedAt = nil
	record.plan.ArchiveReason = ""
	record.plan.UpdatedAt = time.Now().UTC()
	record.plan.LastActivityAt = record.plan.UpdatedAt
	return nil
}

func (s *MemoryStore) DeletePlan(_ context.Context, workspaceID, planID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	record, err := s.lookup(workspaceID, planID)
	if err != nil {
		return err
	}
	// Only a Plan that never produced anything may be hard-deleted. Everything
	// else archives, so materialized work is never silently removed (FR-17).
	if err := record.deletable(); err != nil {
		return err
	}
	delete(s.plans, planID)
	return nil
}

// deletable reports whether this Plan has no effects worth preserving.
func (r *planRecord) deletable() error {
	if r.plan.ApprovedVersion > 0 {
		return fmt.Errorf("%w: plan has an approved version", ErrPlanNotDeletable)
	}
	if len(r.approvals) > 0 {
		return fmt.Errorf("%w: plan has approval records", ErrPlanNotDeletable)
	}
	if len(r.taskLinks) > 0 {
		return fmt.Errorf("%w: plan has %d linked tasks", ErrPlanNotDeletable, len(r.taskLinks))
	}
	if len(r.runLinks) > 0 {
		return fmt.Errorf("%w: plan has %d linked runs", ErrPlanNotDeletable, len(r.runLinks))
	}
	return nil
}

func (s *MemoryStore) PutClarifications(_ context.Context, workspaceID, planID string, questions []Clarification) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	record, err := s.lookup(workspaceID, planID)
	if err != nil {
		return err
	}

	existing := make(map[string]Clarification, len(record.clarifications))
	for _, question := range record.clarifications {
		existing[question.ID] = question
	}

	merged := make([]Clarification, 0, len(questions))
	for _, question := range questions {
		if prior, ok := existing[question.ID]; ok {
			// Keep every authored field. A regenerated question may reword its
			// prompt, but the user's answer is theirs (FR-25).
			question.Status = prior.Status
			question.Answer = prior.Answer
			question.AnsweredBy = prior.AnsweredBy
			question.AnsweredAt = prior.AnsweredAt
			question.SkipReason = prior.SkipReason
			question.CreatedAt = prior.CreatedAt
		}
		if question.Status == "" {
			question.Status = ClarificationOpen
		}
		merged = append(merged, question)
	}
	record.clarifications = merged
	return nil
}

func (s *MemoryStore) AnswerClarification(_ context.Context, workspaceID, planID, clarificationID string, answer ClarificationAnswer) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	record, err := s.lookup(workspaceID, planID)
	if err != nil {
		return err
	}
	for i, question := range record.clarifications {
		if question.ID != clarificationID {
			continue
		}
		answeredAt := answer.At
		if answer.Answered {
			question.Status = ClarificationAnswered
			question.Answer = answer.Answer
			question.SkipReason = ""
		} else {
			question.Status = ClarificationSkipped
			question.SkipReason = answer.SkipReason
		}
		question.AnsweredBy = answer.AnsweredBy
		question.AnsweredAt = &answeredAt
		record.clarifications[i] = question
		record.plan.LastActivityAt = answer.At
		record.plan.UpdatedAt = answer.At
		return nil
	}
	return fmt.Errorf("%w: clarification %s", ErrPlanNotFound, clarificationID)
}

func (s *MemoryStore) CreateVersion(_ context.Context, version *Version) (*Version, error) {
	if version == nil {
		return nil, fmt.Errorf("%w: version is nil", ErrValidation)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	record, err := s.lookup(version.WorkspaceID, version.PlanID)
	if err != nil {
		return nil, err
	}
	stored := version.Clone()
	// Numbers are assigned by the store so two concurrent review requests can
	// never claim the same one (FR-31).
	stored.Number = len(record.versions) + 1
	if stored.Status == "" {
		stored.Status = VersionInReview
	}
	record.versions = append(record.versions, stored)
	record.plan.CurrentVersion = stored.Number
	record.plan.UpdatedAt = stored.CreatedAt
	record.plan.LastActivityAt = stored.CreatedAt
	return stored.Clone(), nil
}

func (s *MemoryStore) GetVersion(_ context.Context, workspaceID, planID string, number int) (*Version, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	record, err := s.lookup(workspaceID, planID)
	if err != nil {
		return nil, err
	}
	for _, version := range record.versions {
		if version.Number == number {
			return version.Clone(), nil
		}
	}
	return nil, fmt.Errorf("%w: plan %s version %d", ErrVersionNotFound, planID, number)
}

func (s *MemoryStore) ListVersions(_ context.Context, workspaceID, planID string) ([]*Version, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	record, err := s.lookup(workspaceID, planID)
	if err != nil {
		return nil, err
	}
	out := make([]*Version, 0, len(record.versions))
	for _, version := range record.versions {
		out = append(out, version.Clone())
	}
	return out, nil
}

func (s *MemoryStore) SetVersionDecision(_ context.Context, workspaceID, planID string, number int, status VersionStatus, decidedBy, reason string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	record, err := s.lookup(workspaceID, planID)
	if err != nil {
		return err
	}
	for _, version := range record.versions {
		if version.Number != number {
			continue
		}
		// Only the decision is written. Content and its hash stay exactly as
		// they were reviewed (FR-31, FR-32).
		decidedAt := at
		version.Status = status
		version.DecidedAt = &decidedAt
		version.DecidedBy = decidedBy
		version.DecisionReason = reason
		if status == VersionApproved {
			record.plan.ApprovedVersion = number
		}
		record.plan.UpdatedAt = at
		record.plan.LastActivityAt = at
		return nil
	}
	return fmt.Errorf("%w: plan %s version %d", ErrVersionNotFound, planID, number)
}

func (s *MemoryStore) CreateApproval(_ context.Context, approval *Approval) (*Approval, error) {
	if approval == nil {
		return nil, fmt.Errorf("%w: approval is nil", ErrValidation)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	record, err := s.lookup(approval.WorkspaceID, approval.PlanID)
	if err != nil {
		return nil, err
	}
	// A retried approval request returns what the first one produced instead of
	// creating a second authorization (FR-73).
	for _, existing := range record.approvals {
		if existing.IdempotencyKey == approval.IdempotencyKey {
			return existing.Clone(), nil
		}
	}
	stored := approval.Clone()
	if stored.ID == "" {
		stored.ID = NewApprovalID()
	}
	record.approvals = append(record.approvals, stored)
	return stored.Clone(), nil
}

func (s *MemoryStore) GetApproval(_ context.Context, workspaceID, planID, approvalID string) (*Approval, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	record, err := s.lookup(workspaceID, planID)
	if err != nil {
		return nil, err
	}
	for _, approval := range record.approvals {
		if approval.ID == approvalID {
			return approval.Clone(), nil
		}
	}
	return nil, fmt.Errorf("%w: %s", ErrApprovalNotFound, approvalID)
}

func (s *MemoryStore) ListApprovals(_ context.Context, workspaceID, planID string) ([]*Approval, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	record, err := s.lookup(workspaceID, planID)
	if err != nil {
		return nil, err
	}
	out := make([]*Approval, 0, len(record.approvals))
	for _, approval := range record.approvals {
		out = append(out, approval.Clone())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (s *MemoryStore) ConsumeApproval(_ context.Context, workspaceID, planID, approvalID string, result ApprovalResult, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	record, err := s.lookup(workspaceID, planID)
	if err != nil {
		return err
	}
	for _, approval := range record.approvals {
		if approval.ID != approvalID {
			continue
		}
		if approval.Invalidated() {
			return fmt.Errorf("%w: approval was invalidated by a later edit", ErrApprovalMismatch)
		}
		// The check and the write happen under one lock, which is what makes
		// two concurrent materializations resolve to one (FR-72, FR-178).
		if approval.Consumed() {
			return fmt.Errorf("%w: %s", ErrApprovalConsumed, approvalID)
		}
		consumedAt := at
		stored := result
		stored.TaskIDs = cloneStrings(result.TaskIDs)
		stored.ArtifactPaths = cloneStrings(result.ArtifactPaths)
		approval.ConsumedAt = &consumedAt
		approval.ConsumedResult = &stored
		return nil
	}
	return fmt.Errorf("%w: %s", ErrApprovalNotFound, approvalID)
}

func (s *MemoryStore) InvalidateApprovals(_ context.Context, workspaceID, planID string, version int, reason string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	record, err := s.lookup(workspaceID, planID)
	if err != nil {
		return err
	}
	for _, approval := range record.approvals {
		if approval.Version != version || !approval.Usable() {
			continue
		}
		invalidatedAt := at
		approval.InvalidatedAt = &invalidatedAt
		approval.InvalidatedReason = reason
	}
	return nil
}

func (s *MemoryStore) LinkTasks(_ context.Context, workspaceID, planID string, links []TaskLink) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	record, err := s.lookup(workspaceID, planID)
	if err != nil {
		return err
	}
	for _, link := range links {
		if record.hasMaterializedLink(link) {
			// A retried materialization must not produce a second Task for an
			// approved item (FR-91).
			continue
		}
		record.taskLinks = append(record.taskLinks, link)
	}
	return nil
}

// hasMaterializedLink mirrors the SQLite partial unique index: one Task per
// Plan item per approved version, with follow-ups exempt (FR-78, FR-91).
func (r *planRecord) hasMaterializedLink(candidate TaskLink) bool {
	if candidate.Role == LinkRoleFollowUp {
		return false
	}
	for _, link := range r.taskLinks {
		if link.TaskID == candidate.TaskID {
			return true
		}
		if link.Role == candidate.Role &&
			link.Version == candidate.Version &&
			link.GroupID == candidate.GroupID &&
			link.ItemID == candidate.ItemID {
			return true
		}
	}
	return false
}

func (s *MemoryStore) LinkRun(_ context.Context, workspaceID, planID string, link RunLink) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	record, err := s.lookup(workspaceID, planID)
	if err != nil {
		return err
	}
	for _, existing := range record.runLinks {
		if existing.RunID == link.RunID {
			return nil
		}
	}
	record.runLinks = append(record.runLinks, link)
	return nil
}

func (s *MemoryStore) RetireTaskLink(_ context.Context, workspaceID, planID, taskID, replacedByTaskID, reason string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	record, err := s.lookup(workspaceID, planID)
	if err != nil {
		return err
	}
	for i, link := range record.taskLinks {
		if link.TaskID != taskID {
			continue
		}
		retiredAt := at
		link.RetiredAt = &retiredAt
		link.RetiredReason = reason
		link.ReplacedByTaskID = replacedByTaskID
		record.taskLinks[i] = link
		return nil
	}
	return fmt.Errorf("%w: task link %s", ErrPlanNotFound, taskID)
}

func (s *MemoryStore) RecordReconciliation(_ context.Context, reconciliation *Reconciliation) (*Reconciliation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	record, err := s.lookup(reconciliation.WorkspaceID, reconciliation.PlanID)
	if err != nil {
		return nil, err
	}
	// Re-confirming the same preview returns the original decision. A double
	// click is one confirmation, not two.
	for _, existing := range record.reconciles {
		if existing.Token == reconciliation.Token {
			return cloneReconciliation(existing), nil
		}
	}

	stored := cloneReconciliation(reconciliation)
	if stored.ID == "" {
		stored.ID = newUUID()
	}
	record.reconciles = append(record.reconciles, stored)
	return cloneReconciliation(stored), nil
}

func (s *MemoryStore) GetReconciliation(_ context.Context, workspaceID, planID, token string) (*Reconciliation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	record, err := s.lookup(workspaceID, planID)
	if err != nil {
		return nil, err
	}
	for _, existing := range record.reconciles {
		if existing.Token == token {
			return cloneReconciliation(existing), nil
		}
	}
	return nil, ErrReconciliationNotFound
}

func (s *MemoryStore) ConsumeReconciliation(_ context.Context, workspaceID, planID, token string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	record, err := s.lookup(workspaceID, planID)
	if err != nil {
		return err
	}
	for _, existing := range record.reconciles {
		if existing.Token != token {
			continue
		}
		if existing.AppliedAt != nil {
			return ErrReconciliationConsumed
		}
		applied := at
		existing.AppliedAt = &applied
		return nil
	}
	return ErrReconciliationNotFound
}

func (s *MemoryStore) ListReconciliations(_ context.Context, workspaceID, planID string) ([]*Reconciliation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	record, err := s.lookup(workspaceID, planID)
	if err != nil {
		return nil, err
	}
	out := make([]*Reconciliation, 0, len(record.reconciles))
	for i := len(record.reconciles) - 1; i >= 0; i-- {
		out = append(out, cloneReconciliation(record.reconciles[i]))
	}
	return out, nil
}

// cloneReconciliation copies a record so a caller cannot reach into the store's
// state by holding the pointer it was handed.
func cloneReconciliation(in *Reconciliation) *Reconciliation {
	if in == nil {
		return nil
	}
	out := *in
	out.Entries = append([]ReconcileEntry(nil), in.Entries...)
	if in.AppliedAt != nil {
		applied := *in.AppliedAt
		out.AppliedAt = &applied
	}
	return &out
}

func (s *MemoryStore) PlanForTask(_ context.Context, workspaceID, taskID string) (*TaskLink, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, record := range s.plans {
		if record.plan.WorkspaceID != workspaceID {
			continue
		}
		for _, link := range record.taskLinks {
			if link.TaskID == taskID {
				cloned := cloneTaskLinks([]TaskLink{link})
				return &cloned[0], nil
			}
		}
	}
	return nil, fmt.Errorf("%w: no plan links task %s", ErrPlanNotFound, taskID)
}

func (s *MemoryStore) PlanForRun(_ context.Context, workspaceID, runID string) (*RunLink, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, record := range s.plans {
		if record.plan.WorkspaceID != workspaceID {
			continue
		}
		for _, link := range record.runLinks {
			if link.RunID == runID {
				cloned := link
				return &cloned, nil
			}
		}
	}
	return nil, fmt.Errorf("%w: no plan links run %s", ErrPlanNotFound, runID)
}

func (s *MemoryStore) AppendActivity(_ context.Context, activity Activity) (Activity, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	record, err := s.lookup(activity.WorkspaceID, activity.PlanID)
	if err != nil {
		return Activity{}, err
	}
	return record.appendActivity(activity), nil
}

func (s *MemoryStore) ListActivity(_ context.Context, workspaceID, planID string, limit int) ([]Activity, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	record, err := s.lookup(workspaceID, planID)
	if err != nil {
		return nil, err
	}
	out := append([]Activity(nil), record.activity...)
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}

func (s *MemoryStore) PutDraftSnapshot(_ context.Context, snapshot *DraftSnapshot, keep int) error {
	if snapshot == nil {
		return fmt.Errorf("%w: snapshot is nil", ErrValidation)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	record, err := s.lookup(snapshot.WorkspaceID, snapshot.PlanID)
	if err != nil {
		return err
	}
	stored := snapshot.Clone()
	if stored.ID == "" {
		stored.ID = NewDraftSnapshotID()
	}
	record.snapshots = append(record.snapshots, stored)
	record.pruneSnapshots(keep, time.Time{})
	return nil
}

func (s *MemoryStore) ListDraftSnapshots(_ context.Context, workspaceID, planID string) ([]*DraftSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	record, err := s.lookup(workspaceID, planID)
	if err != nil {
		return nil, err
	}
	out := make([]*DraftSnapshot, 0, len(record.snapshots))
	for _, snapshot := range record.snapshots {
		out = append(out, snapshot.Clone())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (s *MemoryStore) PruneDraftSnapshots(_ context.Context, workspaceID, planID string, keep int, olderThan time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	record, err := s.lookup(workspaceID, planID)
	if err != nil {
		return 0, err
	}
	return record.pruneSnapshots(keep, olderThan), nil
}

// pruneSnapshots drops recovery points beyond the newest keep and any older
// than the cutoff. It never touches versions, so recovery pruning can never
// erase review history (FR-30, FR-31).
func (r *planRecord) pruneSnapshots(keep int, olderThan time.Time) int {
	sort.Slice(r.snapshots, func(i, j int) bool {
		return r.snapshots[i].CreatedAt.After(r.snapshots[j].CreatedAt)
	})
	kept := make([]*DraftSnapshot, 0, len(r.snapshots))
	removed := 0
	for i, snapshot := range r.snapshots {
		tooMany := keep > 0 && i >= keep
		tooOld := !olderThan.IsZero() && snapshot.CreatedAt.Before(olderThan)
		if tooMany || tooOld {
			removed++
			continue
		}
		kept = append(kept, snapshot)
	}
	r.snapshots = kept
	return removed
}
