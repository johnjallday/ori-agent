package learningmissions

import (
	"context"
	"errors"
)

// ProcessMilestone is called only by the owned event worker (or a controlled
// test), never GET, polling, arbitrary workspace events, or startup recovery.
func (s *Service) ProcessMilestone(ctx context.Context, owner, id string) error {
	release, err := s.permit()
	if err != nil {
		return err
	}
	defer release()
	event, err := s.store.Operation(ctx, owner, id)
	if err != nil {
		return err
	}
	if event.Kind != "milestone" || event.Milestone == nil {
		return ErrInvalid
	}
	if event.Status != "pending" {
		return nil
	}
	if s.now().UnixMilli() < event.Milestone.NotBefore {
		return nil
	}
	current, err := s.store.Owner(ctx, owner)
	if err != nil {
		return err
	}
	request := ProposalRequest{MutationRequest: MutationRequest{OperationID: "auto-" + event.ID, ExpectedRevision: current.Revision},
		WorkspaceID: event.Milestone.WorkspaceID, Personalize: true, ConsentVersion: DisclosureVersion, ModelFingerprint: event.Milestone.ModelFingerprint}
	_, err = s.generateWithMilestone(ctx, owner, request, event.Milestone)
	if err == nil {
		return nil
	}
	// Ineligibility/contention/limits are quiet terminal outcomes, not a timer
	// to retry later. An uncertain committed generation already consumed the
	// event and reserved the cost; this retirement cannot replay that call.
	reason := "milestone_unavailable"
	if errors.Is(err, ErrLimit) || errors.Is(err, ErrStorageLimit) {
		reason = "milestone_limited"
	}
	if errors.Is(err, ErrConflict) {
		reason = "milestone_changed"
	}
	return s.retireMilestone(ctx, owner, *event.Milestone, reason)
}

func (s *Service) validateMilestone(ctx context.Context, owner Owner, event Milestone) error {
	if !event.allowed(owner) || s.now().UnixMilli() < event.NotBefore || s.now().UnixMilli() >= event.ExpiresAt {
		return ErrConflict
	}
	op, err := s.store.Operation(ctx, owner.ID, event.ID)
	if err != nil || op.Status != "pending" || op.Milestone == nil || *op.Milestone != event {
		return ErrConflict
	}
	// Resolve the already-earned exact receipt again. Its bounded canonical
	// bytes are used only for host verification and never enter the design
	// projection, analytics, event record, or provider prompt.
	view, err := s.EvidenceView(ctx, owner.ID, event.EvidenceKey)
	if err != nil || view.Review.ID != event.ReviewID || view.Review.DecidedAt != event.CreatedAt ||
		view.Review.Epoch != event.Epoch || view.Review.Result.WorkspaceID != event.WorkspaceID || view.Review.Result.Incarnation != event.Incarnation {
		return ErrUnavailable
	}
	return nil
}

func automaticCandidateFingerprint(owner Owner, snapshot ContextSnapshot, config ModelConfiguration) (string, error) {
	if len(snapshot.Data.Candidates) != 1 {
		return "", ErrUnavailable
	}
	candidate, ok := snapshot.Bindings[snapshot.Data.Candidates[0].Ref]
	if !ok || !owner.Preferences.AllowsScope(candidate.WorkspaceID, candidate.Incarnation) {
		return "", ErrUnavailable
	}
	data, err := encode(struct {
		Owner                                        string
		Epoch                                        int64
		Workspace, Incarnation, Ref, Scope, Original string
		Structure                                    StructureID
		Focus, Effort, Model                         string
		Guidance                                     Guidance
		CoachingDepth                                string
	}{owner.ID, owner.Epoch, candidate.WorkspaceID, candidate.Incarnation, candidate.Ref, candidate.ScopeDigest, candidate.OriginalReceipt,
		candidate.Structure, recommendationTopic(owner.Preferences.Focus), owner.Preferences.Effort, config.Fingerprint, owner.Preferences.Guidance, snapshot.Data.CoachingDepth}, MaxRecordBytes)
	if err != nil {
		return "", err
	}
	return digest([]byte(data)), nil
}

func (m *Mutation) claimMilestone(event Milestone, snapshot ContextSnapshot, config ModelConfiguration, generation *Operation) error {
	if !event.allowed(m.owner) || generation.CreatedAt < event.NotBefore || generation.CreatedAt >= event.ExpiresAt || config.Fingerprint != event.ModelFingerprint {
		return ErrConflict
	}
	prior, err := readOperation(m.ctx, m.tx, m.owner.ID, event.ID)
	if err != nil || prior.Status != "pending" || prior.Milestone == nil || *prior.Milestone != event {
		return ErrConflict
	}
	if err := m.checkScope(event.WorkspaceID, event.Incarnation); err != nil {
		return err
	}
	fingerprint, err := automaticCandidateFingerprint(m.owner, snapshot, config)
	if err != nil {
		return err
	}
	var recent, duplicate int
	if err := m.tx.QueryRowContext(m.ctx, `SELECT COUNT(*) FROM learning_operations WHERE owner_id=? AND kind='generate' AND json_extract(record,'$.automatic')=1 AND created_at_ms>=?`, m.owner.ID, generation.CreatedAt-AutomaticSpacing.Milliseconds()).Scan(&recent); err != nil {
		return ErrUnavailable
	}
	if recent > 0 {
		return ErrLimit
	}
	if err := m.tx.QueryRowContext(m.ctx, `SELECT COUNT(*) FROM learning_operations WHERE owner_id=? AND epoch=? AND kind='generate' AND json_extract(record,'$.candidate_fingerprint')=?`, m.owner.ID, m.owner.Epoch, fingerprint).Scan(&duplicate); err != nil {
		return ErrUnavailable
	}
	if duplicate > 0 {
		return ErrConflict
	}
	prior.Status = "succeeded" // event consumed by one durable generation admission
	if err := m.UpdateOperation(prior); err != nil {
		return err
	}
	generation.Automatic, generation.MilestoneID, generation.CandidateFingerprint = true, event.ID, fingerprint
	return nil
}

func (s *Service) retireMilestone(ctx context.Context, owner string, event Milestone, reason string) error {
	s.effects.RLock()
	defer s.effects.RUnlock()
	current, err := s.store.Owner(ctx, owner)
	if err != nil {
		return err
	}
	if current.Revision == 0 || current.Epoch != event.Epoch {
		return nil
	}
	prior, err := s.store.Operation(ctx, owner, event.ID)
	if err != nil || prior.Status != "pending" {
		return err
	}
	_, err = s.store.Change(ctx, owner, current.Revision, current.Epoch, false, func(m *Mutation) error {
		op, err := readOperation(ctx, m.tx, owner, event.ID)
		if err != nil {
			return err
		}
		if op.Status != "pending" {
			return nil
		}
		op.Status, op.Failure = "invalidated", reason
		return m.UpdateOperation(op)
	})
	return err
}
