package learningmissions

import (
	"context"
	"fmt"
	"slices"

	"github.com/google/uuid"
)

type AdjustmentRequest struct {
	MutationRequest
	InstanceRevision int64  `json:"instance_revision"`
	Kind             string `json:"kind"`
	WorkspaceID      string `json:"studio_id,omitempty"`
}

func definitionInputLimit(d Definition) int {
	if d.InputLimit > 0 {
		return d.InputLimit
	}
	return MaxDraftBytes
}

type adjustmentContext struct {
	target        Instance
	active, offer *Instance
}

func (s *Service) adjustmentContext(ctx context.Context, owner, id string, revision int64) (adjustmentContext, error) {
	target, err := s.store.Instance(ctx, owner, id)
	if err != nil {
		return adjustmentContext{}, err
	}
	if target.Revision != revision || !slices.Contains([]State{Offered, Active, Blocked}, target.State) {
		return adjustmentContext{}, ErrConflict
	}
	out := adjustmentContext{target: target}
	rows, err := s.store.Instances(ctx, owner)
	if err != nil {
		return out, err
	}
	for _, row := range rows {
		i := row
		switch i.State {
		case Active, Blocked:
			if (target.State == Offered && (target.Definition.ReplacesID != i.ID || target.Definition.ReplacesRevision != i.Revision)) || (target.State != Offered && target.ID != i.ID) {
				return out, ErrConflict
			}
			out.active = &i
		case Offered:
			if (target.State == Offered && target.ID != i.ID) || (target.State != Offered && i.Definition.ReplacesID != target.ID) {
				return out, ErrConflict
			}
			out.offer = &i
		}
	}
	if target.Definition.ReplacesID != "" && target.State == Offered && out.active == nil {
		return out, ErrConflict
	}
	return out, nil
}

func (s *Service) adjustmentCandidate(ctx context.Context, owner Owner, target Instance, workspace string) (rankedCandidate, error) {
	var candidates []EligibleCandidate
	if reader, ok := s.source.(CandidateSource); ok {
		var err error
		candidates, err = reader.Candidates(ctx, owner.ID, workspace)
		if err != nil {
			return rankedCandidate{}, err
		}
	} else {
		candidate, err := s.source.Candidate(ctx, owner.ID, workspace)
		if err != nil {
			return rankedCandidate{}, err
		}
		candidates = []EligibleCandidate{{Candidate: candidate, Structure: DelegateInspect}}
	}
	policy, err := s.recommendationContext(ctx, generationOwner(owner, workspace), candidates, "")
	if err != nil {
		return rankedCandidate{}, err
	}
	// Only this explicitly requested preview may coexist with the single
	// validated current contract. The transaction rechecks both revisions.
	policy.Active, policy.ReviewableWork, policy.AvoidRef = false, false, target.Definition.CandidateRef
	ranked := rankCandidates(candidates, policy)
	if len(ranked) == 0 {
		return rankedCandidate{}, ErrUnavailable
	}
	return ranked[0], nil
}

// Adjust is model-free and creates only a new proposal. A saved active contract
// and its real work remain unchanged until explicit replacement acceptance.
func (s *Service) Adjust(ctx context.Context, owner, id string, req AdjustmentRequest) (Outcome, error) {
	if s == nil || !validID(id) || req.InstanceRevision < 1 || !slices.Contains([]string{"smaller", "alternative"}, req.Kind) ||
		(req.WorkspaceID != "" && !validID(req.WorkspaceID)) {
		return Outcome{}, ErrInvalid
	}
	s.effects.RLock()
	defer s.effects.RUnlock()
	payload := struct {
		ID      string
		Request AdjustmentRequest
	}{id, req}
	var selection adjustmentContext
	var definition Definition
	return s.apply(ctx, owner, req.MutationRequest, "adjust", payload, func(current Owner) error {
		if current.Preferences.Mode == "off" || s.source == nil {
			return ErrUnavailable
		}
		var err error
		selection, err = s.adjustmentContext(ctx, owner, id, req.InstanceRevision)
		if err != nil {
			return err
		}
		target := selection.target
		workspace := req.WorkspaceID
		if workspace == "" {
			workspace = target.Definition.WorkspaceID
		}
		if req.Kind == "smaller" {
			if workspace != target.Definition.WorkspaceID {
				return ErrInvalid
			}
			if s.validateSource(ctx, owner, target.Definition) != nil {
				return ErrUnavailable
			}
			for _, exclusion := range current.Preferences.Exclusions {
				if exclusion.WorkspaceID == workspace && exclusion.Structure == target.Definition.Structure &&
					(exclusion.Topic == "" || exclusion.Topic == target.Definition.FocusTopic) && (exclusion.Until == 0 || exclusion.Until > s.now().UnixMilli()) {
					return ErrUnavailable
				}
			}
			limit := definitionInputLimit(target.Definition)
			if limit <= 256 {
				return ErrLimit
			}
			definition = target.Definition
			definition.InputLimit = max(256, limit/2)
			definition.Goal, definition.Scope, definition.Why = genericCopy(definition.Structure)
			definition.Scope = fmt.Sprintf("Supply at most %d bytes for one focused draft or correction. The same explicit execution and exact-result review criteria still apply.", definition.InputLimit)
			definition.Why = "You requested a smaller input boundary. This does not lower the evidence requirements or promise a lower provider charge."
		} else {
			ranked, err := s.adjustmentCandidate(ctx, current, target, workspace)
			if err != nil {
				return err
			}
			candidate := ranked.Candidate
			structure, err := Lookup(candidate.Structure, DefinitionVersion)
			if err != nil {
				return err
			}
			goal, scope, why := genericCopy(candidate.Structure)
			definition = Definition{Structure: candidate.Structure, Version: DefinitionVersion, WorkspaceID: candidate.WorkspaceID, Incarnation: candidate.Incarnation,
				CandidateRef: candidate.Ref, ScopeDigest: candidate.ScopeDigest, OriginalReceipt: candidate.OriginalReceipt, Goal: goal, Scope: scope, Why: why, Steps: structure.Steps,
				FocusTopic: recommendationTopic(current.Preferences.Focus), Signals: slices.Clone(ranked.Signals)}
		}
		definition.Personalized, definition.Automatic, definition.ModelFingerprint, definition.Hints = false, false, "", nil
		definition.Guidance, definition.SupportVersion = current.Preferences.Guidance, GuidanceProtocolVersion
		definition.Adjustment, definition.ExpiresAt, definition.CoachingDepth = req.Kind, s.now().Add(OfferLifetime).UnixMilli(), ""
		definition.ReplacesID, definition.ReplacesRevision = "", 0
		if selection.active != nil {
			definition.ReplacesID, definition.ReplacesRevision = selection.active.ID, selection.active.Revision
		}
		// A newly explicitly selected workspace may have no learning source
		// row yet. Bind its host candidate transactionally, as Propose does;
		// acceptance separately rechecks the exact canonical contract.
		return definition.Validate()
	}, func(m *Mutation, op *Operation) error {
		current, err := m.Instance(id)
		if err != nil || current.Revision != selection.target.Revision || current.State != selection.target.State {
			return ErrConflict
		}
		if selection.active != nil {
			active, err := m.Instance(selection.active.ID)
			if err != nil || active.Revision != selection.active.Revision || !slices.Contains([]State{Active, Blocked}, active.State) {
				return ErrConflict
			}
		}
		if selection.offer != nil {
			offer, err := m.Instance(selection.offer.ID)
			if err != nil || offer.Revision != selection.offer.Revision || offer.State != Offered {
				return ErrConflict
			}
			oldRevision := offer.Revision
			offer.State, offer.Revision, offer.UpdatedAt, offer.Progress.Blocker = Invalidated, offer.Revision+1, op.CreatedAt, "replaced_offer"
			if err := m.UpdateInstance(offer, oldRevision); err != nil {
				return err
			}
		}
		if err := m.BindSource(definition.WorkspaceID, definition.Incarnation); err != nil {
			return err
		}
		i := Instance{ID: uuid.NewString(), OwnerID: owner, Epoch: m.owner.Epoch, Revision: 1, State: Offered, Definition: definition, CreatedAt: op.CreatedAt, UpdatedAt: op.CreatedAt}
		op.InstanceID = i.ID
		return m.InsertInstance(i)
	})
}

func (m *Mutation) acceptReplacement(i Instance, now int64) error {
	if i.Definition.ReplacesID == "" {
		return nil
	}
	old, err := m.Instance(i.Definition.ReplacesID)
	if err != nil || old.Revision != i.Definition.ReplacesRevision || !slices.Contains([]State{Active, Blocked}, old.State) {
		return ErrConflict
	}
	revision := old.Revision
	old.State, old.Revision, old.UpdatedAt, old.Progress.Blocker = Stopped, old.Revision+1, now, "replaced"
	return m.UpdateInstance(old, revision)
}

func (m *Mutation) invalidateReplacementOffers(id string, now int64) error {
	_, err := m.tx.ExecContext(m.ctx, `UPDATE learning_instances SET state='invalidated',revision=revision+1,updated_at_ms=?,progress=json_set(progress,'$.blocker','replacement_changed')
 WHERE owner_id=? AND state='offered' AND json_extract(definition,'$.replaces_id')=?`, now, m.owner.ID, id)
	if err != nil {
		return ErrUnavailable
	}
	return nil
}
