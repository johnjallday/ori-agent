package server

import (
	"context"
	"encoding/json"

	"github.com/johnjallday/ori-agent/internal/learningmissions"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func learningReceiptCandidate(base learningmissions.Candidate, structure learningmissions.StructureID, review learningmissions.Review) learningmissions.EligibleCandidate {
	return learningReceiptCandidateRef(base, structure, learningmissions.ReviewedResultMetadata{ReviewID: review.ID, Result: review.Result, InputDigest: review.InputDigest})
}

func learningReceiptCandidateRef(base learningmissions.Candidate, structure learningmissions.StructureID, review learningmissions.ReviewedResultMetadata) learningmissions.EligibleCandidate {
	data, _ := json.Marshal([]any{base.ScopeDigest, structure, review.ReviewID, review.Result, review.InputDigest})
	base.ScopeDigest = learningDigest(data)
	base.Ref = learningDigest([]byte("learning-lineage-v1\x00" + base.ScopeDigest))
	return learningmissions.EligibleCandidate{Candidate: base, Structure: structure, OriginalReceipt: review.ReviewID}
}

func (s learningMissionSource) Candidates(ctx context.Context, owner, id string) ([]learningmissions.EligibleCandidate, error) {
	base, err := s.Candidate(ctx, owner, id)
	if err != nil {
		return nil, err
	}
	out := []learningmissions.EligibleCandidate{{Candidate: base, Structure: learningmissions.DelegateInspect}}
	reviews, err := s.learning.RecentReviews(ctx, owner, id, learningmissions.MaxCandidates)
	if err != nil {
		return out, nil
	} // unknown prior evidence is not a new learner label
	for _, review := range reviews {
		for _, structure := range []learningmissions.StructureID{learningmissions.ImproveResult, learningmissions.RepeatSuccess} {
			if len(out) == learningmissions.MaxCandidates {
				return out, nil
			}
			identity := learningmissions.Definition{Structure: structure, WorkspaceID: id, Incarnation: base.Incarnation, OriginalReceipt: review.ID}
			prior, err := s.originalReceipt(ctx, owner, identity)
			if err != nil {
				continue
			}
			candidate := learningReceiptCandidate(base, structure, review)
			candidate.ReviewedGoal = prior.Instance.Definition.Goal
			out = append(out, candidate)
		}
	}
	return out, nil
}

func (s learningMissionSource) originalReceipt(ctx context.Context, owner string, definition learningmissions.Definition) (learningmissions.OriginalProof, error) {
	if s.learning == nil || definition.OriginalReceipt == "" {
		return learningmissions.OriginalProof{}, learningmissions.ErrUnavailable
	}
	review, err := s.learning.Review(ctx, owner, definition.OriginalReceipt)
	if err != nil {
		return learningmissions.OriginalProof{}, learningmissions.ErrUnavailable
	}
	if review.ObservationID != "" {
		op, err := s.learning.Operation(ctx, owner, review.ObservationID)
		current, ownerErr := s.learning.Owner(ctx, owner)
		if err != nil || ownerErr != nil || op.Kind != "observe" || op.Status != "succeeded" || op.Observation == nil || op.Epoch != current.Epoch {
			return learningmissions.OriginalProof{}, learningmissions.ErrUnavailable
		}
		result, err := s.ObservedResult(ctx, owner, *op.Observation)
		if err != nil {
			return learningmissions.OriginalProof{}, learningmissions.ErrUnavailable
		}
		prior := learningmissions.OriginalProof{Observation: &op, Review: review, Result: result}
		if err := learningmissions.VerifyOriginal(owner, definition, prior); err != nil {
			return learningmissions.OriginalProof{}, err
		}
		return prior, nil
	}
	if review.InstanceID == "" {
		return learningmissions.OriginalProof{}, learningmissions.ErrUnavailable
	}
	instance, err := s.learning.Instance(ctx, owner, review.InstanceID)
	if err != nil {
		return learningmissions.OriginalProof{}, learningmissions.ErrUnavailable
	}
	result, err := s.canonicalResult(ctx, owner, instance)
	if err != nil {
		return learningmissions.OriginalProof{}, learningmissions.ErrUnavailable
	}
	prior := learningmissions.OriginalProof{Instance: instance, Review: review, Result: result}
	if err := learningmissions.VerifyOriginal(owner, definition, prior); err != nil {
		return learningmissions.OriginalProof{}, err
	}
	return prior, nil
}

func (s learningMissionSource) Original(ctx context.Context, owner string, definition learningmissions.Definition) (learningmissions.OriginalProof, error) {
	if err := s.validateDefinitionBase(ctx, owner, definition); err != nil {
		return learningmissions.OriginalProof{}, err
	}
	return s.originalReceipt(ctx, owner, definition)
}

func originalDraftReference(ref *learningmissions.ResultRef) *workspace.LearningDraftOriginal {
	if ref == nil {
		return nil
	}
	return &workspace.LearningDraftOriginal{TicketID: ref.TicketID, RunID: ref.RunID, ArtifactID: ref.ArtifactID, Digest: ref.Digest}
}

func (s learningMissionSource) InputDigest(ctx context.Context, owner string, i learningmissions.Instance, input string) (string, error) {
	if i.OwnerID != owner || s.Validate(ctx, owner, i.Definition) != nil {
		return "", learningmissions.ErrUnavailable
	}
	var original *workspace.LearningDraftOriginal
	if i.Definition.Structure == learningmissions.ImproveResult {
		prior, err := s.originalReceipt(ctx, owner, i.Definition)
		if err != nil || i.Progress.Original == nil || *i.Progress.Original != prior.Result.Ref {
			return "", learningmissions.ErrUnavailable
		}
		original = originalDraftReference(i.Progress.Original)
	}
	return workspace.LearningInputDigest(input, original)
}
