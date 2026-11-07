package learningmissions

import (
	"context"
	"slices"
	"strings"
)

const PlainDraftProcess = "supplied_text_v1"
const RevisionDraftProcess = "revision_v1"

// OriginalProof is read through canonical owners plus a locally consumed
// decision. Portable histories and caller-authored actor labels cannot create it.
type OriginalProof struct {
	Instance    Instance
	Observation *Operation
	Review      Review
	Result      ResultProof
}

type OriginalSource interface {
	Original(context.Context, string, Definition) (OriginalProof, error)
}

// WorkInputBinder lets the existing recipe owner define the exact execution
// input digest; learning must not duplicate the executor's input encoding.
type WorkInputBinder interface {
	InputDigest(context.Context, string, Instance, string) (string, error)
}

func normalizedInputDigest(input string) string {
	return digest([]byte(strings.Join(strings.Fields(input), " ")))
}

func VerifyOriginal(owner string, definition Definition, prior OriginalProof) error {
	i, r, proof := prior.Instance, prior.Review, prior.Result
	if !slices.Contains([]StructureID{ImproveResult, RepeatSuccess}, definition.Structure) || r.Validate() != nil || r.Decision == "" ||
		r.ID != definition.OriginalReceipt || r.Result != proof.Ref || r.Result.WorkspaceID != definition.WorkspaceID || r.Result.Incarnation != definition.Incarnation ||
		proof.FinishedAt > r.DecidedAt || !boundedText(proof.InputText, MaxDraftBytes, true) || len(proof.InputText) > MaxDraftBytes {
		return ErrUnavailable
	}
	if prior.Observation != nil {
		op := prior.Observation
		if i.ID != "" || op.Validate() != nil || op.Kind != "observe" || op.Status != "succeeded" || op.Observation == nil || r.Revision >= op.Observation.Revision {
			return ErrUnavailable
		}
		atReview := *op.Observation
		atReview.Revision = r.Revision
		if _, err := ObservedPracticeEvidence(owner, atReview, r, proof); err != nil {
			return ErrUnavailable
		}
	} else {
		if !slices.Contains([]State{Completed, Stopped, Blocked}, i.State) || i.Progress.ReviewID != r.ID || r.InstanceID != i.ID ||
			r.Epoch != i.Epoch || r.Revision >= i.Revision || r.Structure != i.Definition.Structure || r.Version != i.Definition.Version ||
			r.InputDigest != i.Progress.InputDigest || i.Progress.Result == nil || *i.Progress.Result != r.Result {
			return ErrUnavailable
		}
		if err := verifyResultIdentity(owner, i, proof); err != nil {
			return err
		}
	}
	if definition.Structure == RepeatSuccess && (r.Decision != "use" || proof.Process != PlainDraftProcess ||
		digest([]byte(strings.TrimSpace(proof.InputText))) != proof.InputDigest) {
		return ErrUnavailable
	}
	return nil
}

func (s *Service) original(ctx context.Context, owner string, definition Definition) (OriginalProof, error) {
	reader, ok := s.source.(OriginalSource)
	if !ok {
		return OriginalProof{}, ErrUnavailable
	}
	prior, err := reader.Original(ctx, owner, definition)
	if err != nil {
		return OriginalProof{}, ErrUnavailable
	}
	if err := VerifyOriginal(owner, definition, prior); err != nil {
		return OriginalProof{}, err
	}
	return prior, nil
}
