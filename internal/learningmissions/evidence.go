package learningmissions

import (
	"context"
	"strings"
)

// ResultProof is supplied only by the canonical Ticket/Run adapter. It is never
// decoded from a browser, model reply, event payload or imported history.
type ResultProof struct {
	OwnerID     string
	Ref         ResultRef
	AttemptID   string
	InputDigest string
	Status      string
	Text        string
	StartedAt   int64
	FinishedAt  int64
}

type ResultSource interface {
	Result(context.Context, string, Instance) (ResultProof, error)
}

// VerifyResult requires a locally pinned attempt, not a Ticket's mutable latest
// pointer. Missing history is unavailable, never evidence that practice failed.
func VerifyResult(owner string, i Instance, proof ResultProof) error {
	if i.Validate() != nil || i.OwnerID != owner || proof.OwnerID != owner ||
		i.State != Active || i.Progress.AcceptedAt <= 0 || i.Progress.AttemptID == "" || i.Progress.RunID == "" ||
		proof.Ref.Validate() != nil || proof.Ref.WorkspaceID != i.Definition.WorkspaceID ||
		proof.Ref.Incarnation != i.Definition.Incarnation || proof.Ref.TicketID != i.Progress.TicketID ||
		proof.Ref.RunID != i.Progress.RunID || proof.AttemptID != i.Progress.AttemptID ||
		!validDigest(i.Progress.InputDigest) || proof.InputDigest != i.Progress.InputDigest ||
		proof.Status != "succeeded" || proof.StartedAt < i.Progress.AcceptedAt || proof.FinishedAt < proof.StartedAt ||
		strings.TrimSpace(proof.Text) == "" || len(proof.Text) > MaxResultBytes || digest([]byte(proof.Text)) != proof.Ref.Digest {
		return ErrUnavailable
	}
	if i.Progress.Result != nil && *i.Progress.Result != proof.Ref {
		return ErrConflict
	}
	return nil
}

// PracticeEvidence is a deterministic projection of an already-verified
// decision. Its output never asserts objective correctness or model mastery.
func PracticeEvidence(owner string, i Instance, review Review) ([]Evidence, error) {
	if review.Validate() != nil || review.Decision == "" || review.InstanceID != i.ID || review.Revision != i.Revision ||
		review.Epoch != i.Epoch || review.Structure != i.Definition.Structure || review.Version != i.Definition.Version ||
		i.OwnerID != owner || i.Progress.Result == nil || review.Result != *i.Progress.Result ||
		review.InputDigest != i.Progress.InputDigest || review.Guidance != i.Definition.Guidance ||
		review.HintsShown != i.Progress.HintsShown || review.HintsUsed != i.Progress.HintsUsed {
		return nil, ErrConflict
	}
	if i.Definition.Structure != DelegateInspect {
		// Revision/reuse need verified original lineage as well as a distinct
		// result. Until their end-to-end adapters supply that, fail closed.
		return nil, ErrUnsupported
	}
	support := review.Guidance
	if review.HintsShown || review.HintsUsed {
		support = GuidanceGuided
	}
	out := make([]Evidence, 0, 2)
	for _, competency := range []string{"delegate", "evaluate"} {
		key, err := encode(struct {
			Owner      string
			Structure  StructureID
			Version    int
			Competency string
			Result     ResultRef
		}{owner, review.Structure, review.Version, competency, review.Result}, MaxRecordBytes)
		if err != nil {
			return nil, err
		}
		out = append(out, Evidence{Key: digest([]byte(key)), ReviewID: review.ID, Structure: review.Structure, Version: review.Version,
			Competency: competency, Guidance: support, CreatedAt: review.DecidedAt})
	}
	return out, nil
}
