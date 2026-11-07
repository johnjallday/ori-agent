package learningmissions

import (
	"context"
	"slices"
	"strings"

	"github.com/google/uuid"
)

type ObservedReviewRequest struct {
	MutationRequest
	ObservedRevision int64  `json:"observed_revision"`
	Action           string `json:"action"`
	ReviewID         string `json:"review_id,omitempty"`
	Decision         string `json:"decision,omitempty"`
	Reason           string `json:"reason,omitempty"`
	Note             string `json:"note,omitempty"`
}

func (s *Service) ReviewObserved(ctx context.Context, owner, id string, req ObservedReviewRequest) (Outcome, error) {
	if !validID(id) || req.ObservedRevision < 1 || !slices.Contains([]string{"prepare", "submit"}, req.Action) || !boundedText(req.Note, 500, false) {
		return Outcome{}, ErrInvalid
	}
	if req.Action == "prepare" && (req.ReviewID != "" || req.Decision != "" || req.Reason != "" || req.Note != "") {
		return Outcome{}, ErrInvalid
	}
	if req.Action == "submit" && (!validID(req.ReviewID) || !slices.Contains([]string{"use", "revise", "reject"}, req.Decision) ||
		!slices.Contains([]string{"meets_scope", "missing_detail", "incorrect", "not_useful", "other"}, req.Reason) || (req.Reason == "other" && strings.TrimSpace(req.Note) == "")) {
		return Outcome{}, ErrInvalid
	}
	payload := struct {
		ID      string
		Request ObservedReviewRequest
	}{id, req}
	var proof ResultProof
	out, err := s.apply(ctx, owner, req.MutationRequest, "observe_review", payload, func(current Owner) error {
		claim, err := s.store.Operation(ctx, owner, id)
		if err != nil || claim.Kind != "observe" || claim.Status != "pending" || claim.Observation == nil || !claim.Observation.allowed(current) {
			return ErrUnavailable
		}
		if claim.Observation.Revision != req.ObservedRevision {
			return ErrConflict
		}
		reader, ok := s.source.(ObservedSource)
		if !ok {
			return ErrUnavailable
		}
		proof, err = reader.ObservedResult(ctx, owner, *claim.Observation)
		if err != nil || proof.FinishedAt > s.now().UnixMilli() {
			return ErrUnavailable
		}
		return VerifyObservedResult(owner, *claim.Observation, proof)
	}, func(m *Mutation, op *Operation) error {
		claim, err := readOperation(ctx, m.tx, owner, id)
		if err != nil || claim.Observation == nil || claim.Status != "pending" || !claim.Observation.allowed(m.owner) || claim.Observation.Revision != req.ObservedRevision {
			return ErrConflict
		}
		work := claim.Observation
		if err := m.checkScope(work.WorkspaceID, work.Incarnation); err != nil {
			return err
		}
		if VerifyObservedResult(owner, *work, proof) != nil || proof.FinishedAt > op.CreatedAt {
			return ErrUnavailable
		}
		op.ObservationID = work.ID
		if req.Action == "prepare" {
			work.Revision++
			r := Review{ID: uuid.NewString(), ObservationID: work.ID, Revision: work.Revision, Epoch: work.Epoch, Structure: DelegateInspect, Version: 1,
				Result: proof.Ref, Guidance: GuidanceUnknown, InputDigest: work.InputDigest, CreatedAt: op.CreatedAt, ExpiresAt: s.now().Add(ReviewLifetime).UnixMilli()}
			work.ReviewID, op.ReviewID = r.ID, r.ID
			if err := m.InsertReview(r); err != nil {
				return err
			}
			return m.UpdateOperation(claim)
		}
		r, err := m.Review(req.ReviewID)
		if err != nil || r.Decision != "" || r.ExpiresAt < op.CreatedAt {
			return ErrConflict
		}
		r.Decision, r.Reason, r.Note, r.DecidedAt = req.Decision, req.Reason, strings.TrimSpace(req.Note), op.CreatedAt
		credits, err := ObservedPracticeEvidence(owner, *work, r, proof)
		if err != nil {
			return err
		}
		if err := m.ConsumeReview(r); err != nil {
			return err
		}
		milestoneKey := ""
		for _, credit := range credits {
			added, err := m.Credit(credit)
			if err != nil {
				return err
			}
			if added && milestoneKey == "" {
				milestoneKey = credit.Key
			}
		}
		work.Revision++
		claim.Status, op.ReviewID = "succeeded", r.ID
		if err := m.UpdateOperation(claim); err != nil {
			return err
		}
		op.MilestoneID, err = m.queueMilestone(r, milestoneKey)
		return err
	})
	if err == nil && out.fresh && out.Operation.MilestoneID != "" && s.milestoneWake != nil {
		s.milestoneWake(owner, out.Operation.MilestoneID)
	}
	if err != nil || req.Action != "prepare" {
		return out, err
	}
	// A recovered response reads the same saved challenge and exact result;
	// it never issues a new nonce or borrows another attempt's output.
	claim, err := s.store.Operation(ctx, owner, id)
	current, ownerErr := s.store.Owner(ctx, owner)
	reader, ok := s.source.(ObservedSource)
	if err != nil || ownerErr != nil || !ok || claim.Observation == nil || !claim.Observation.allowed(current) || out.Review == nil ||
		out.Review.Revision != claim.Observation.Revision || out.Review.Decision != "" || out.Review.ExpiresAt <= s.now().UnixMilli() {
		return Outcome{}, ErrUnavailable
	}
	proof, err = reader.ObservedResult(ctx, owner, *claim.Observation)
	if err != nil || VerifyObservedResult(owner, *claim.Observation, proof) != nil || proof.Ref != out.Review.Result {
		return Outcome{}, ErrUnavailable
	}
	out.ResultText, out.ObservedInput = proof.Text, proof.InputText
	return out, nil
}
