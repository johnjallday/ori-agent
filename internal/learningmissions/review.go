package learningmissions

import (
	"context"
	"slices"
	"strings"

	"github.com/google/uuid"
)

type ReviewRequest struct {
	MutationRequest
	InstanceRevision int64  `json:"instance_revision"`
	Action           string `json:"action"`
	ReviewID         string `json:"review_id,omitempty"`
	Decision         string `json:"decision,omitempty"`
	Reason           string `json:"reason,omitempty"`
	Note             string `json:"note,omitempty"`
	Change           string `json:"change,omitempty"`
}

// ReviewResult is only an owner-facing interaction. There is deliberately no
// complete, actor, user, permission, Run/result override or model grading field.
func (s *Service) ReviewResult(ctx context.Context, owner, id string, req ReviewRequest) (Outcome, error) {
	if !validID(id) || req.InstanceRevision < 1 || (req.Action != "prepare" && req.Action != "submit") ||
		!boundedText(req.Note, 500, false) || !boundedText(req.Change, 500, false) {
		return Outcome{}, ErrInvalid
	}
	if req.Action == "prepare" && (req.ReviewID != "" || req.Decision != "" || req.Reason != "" || req.Note != "" || req.Change != "") {
		return Outcome{}, ErrInvalid
	}
	if req.Action == "submit" && (!validID(req.ReviewID) ||
		!slices.Contains([]string{"use", "revise", "reject"}, req.Decision) ||
		!slices.Contains([]string{"meets_scope", "missing_detail", "incorrect", "not_useful", "other"}, req.Reason) ||
		(req.Reason == "other" && strings.TrimSpace(req.Note) == "")) {
		return Outcome{}, ErrInvalid
	}
	payload := struct {
		ID      string        `json:"id"`
		Request ReviewRequest `json:"request"`
	}{id, req}
	var proof ResultProof
	out, err := s.apply(ctx, owner, req.MutationRequest, "review", payload, func(Owner) error {
		i, err := s.store.Instance(ctx, owner, id)
		if err != nil {
			return err
		}
		if i.Revision != req.InstanceRevision {
			return ErrConflict
		}
		if i.Definition.Structure != ImproveResult && req.Change != "" {
			return ErrInvalid
		}
		if i.Definition.Structure != DelegateInspect {
			prior, err := s.original(ctx, owner, i.Definition)
			if err != nil || i.Progress.Original == nil || *i.Progress.Original != prior.Result.Ref ||
				i.Progress.OriginalInputDigest != normalizedInputDigest(prior.Result.InputText) {
				return ErrUnavailable
			}
		}
		reader, ok := s.source.(ResultSource)
		if !ok || s.validateSource(ctx, owner, i.Definition) != nil {
			return ErrUnavailable
		}
		proof, err = reader.Result(ctx, owner, i)
		if err != nil || proof.FinishedAt > s.now().UnixMilli() {
			return ErrUnavailable
		}
		return VerifyResult(owner, i, proof)
	}, func(m *Mutation, op *Operation) error {
		i, err := m.Instance(id)
		if err != nil {
			return err
		}
		if i.Revision != req.InstanceRevision || i.Epoch != m.owner.Epoch {
			return ErrConflict
		}
		if err := m.CheckSource(i.Definition); err != nil {
			return err
		}
		if err := VerifyResult(owner, i, proof); err != nil {
			return err
		}
		if proof.FinishedAt > op.CreatedAt {
			return ErrUnavailable
		}
		op.InstanceID = i.ID
		if req.Action == "prepare" {
			i.Progress.Result = &proof.Ref
			i.Revision++
			i.UpdatedAt = op.CreatedAt
			r := Review{ID: uuid.NewString(), InstanceID: id, Revision: i.Revision, Epoch: i.Epoch,
				Structure: i.Definition.Structure, Version: i.Definition.Version, Result: proof.Ref,
				Original: i.Progress.Original, OriginalReceipt: i.Definition.OriginalReceipt,
				Guidance: i.Definition.Guidance, SupportVersion: i.Definition.SupportVersion, HintsShown: i.Progress.HintsShown, HintsUsed: i.Progress.HintsUsed,
				InputDigest: i.Progress.InputDigest, CreatedAt: op.CreatedAt, ExpiresAt: s.now().Add(ReviewLifetime).UnixMilli()}
			if err := m.InsertReview(r); err != nil {
				return err
			}
			op.ReviewID = r.ID
			return m.UpdateInstance(i, req.InstanceRevision)
		}
		r, err := m.Review(req.ReviewID)
		if err != nil {
			return err
		}
		if r.Decision != "" || r.ExpiresAt < op.CreatedAt {
			return ErrConflict
		}
		r.Decision, r.Reason, r.Note, r.Change, r.DecidedAt = req.Decision, req.Reason, strings.TrimSpace(req.Note), strings.TrimSpace(req.Change), op.CreatedAt
		evidence, err := PracticeEvidence(owner, i, r)
		if err != nil {
			return err
		}
		if err := m.ConsumeReview(r); err != nil {
			return err
		}
		milestoneKey := ""
		for _, e := range evidence {
			added, err := m.Credit(e)
			if err != nil {
				return err
			}
			if added && milestoneKey == "" {
				milestoneKey = e.Key
			}
		}
		i.State, i.Revision, i.UpdatedAt, i.Progress.ReviewID = Completed, i.Revision+1, op.CreatedAt, r.ID
		if i.Definition.Structure == RepeatSuccess && r.Decision != "use" {
			i.State, i.Progress.Blocker = Blocked, "result_not_useful"
		}
		op.ReviewID = r.ID
		if err := m.UpdateInstance(i, req.InstanceRevision); err != nil {
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
	// Display the same exact bytes the nonce binds, including after a lost
	// response. Do not copy result bodies into receipts, status or analytics.
	reader, ok := s.source.(ResultSource)
	if !ok || out.Instance == nil || out.Review == nil || s.validateSource(ctx, owner, out.Instance.Definition) != nil {
		return Outcome{}, ErrUnavailable
	}
	proof, err = reader.Result(ctx, owner, *out.Instance)
	if err != nil || VerifyResult(owner, *out.Instance, proof) != nil || proof.Ref != out.Review.Result {
		return Outcome{}, ErrUnavailable
	}
	if out.Review.ExpiresAt <= s.now().UnixMilli() || out.Review.Decision != "" {
		return Outcome{}, ErrConflict
	}
	if out.Instance.Definition.Structure != DelegateInspect {
		prior, err := s.original(ctx, owner, out.Instance.Definition)
		if err != nil || out.Review.Original == nil || *out.Review.Original != prior.Result.Ref {
			return Outcome{}, ErrUnavailable
		}
		out.OriginalText, out.OriginalInput = prior.Result.Text, prior.Result.InputText
	}
	out.ResultText = proof.Text
	return out, nil
}
