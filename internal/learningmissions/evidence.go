package learningmissions

import (
	"context"
	"strings"
	"unicode/utf8"
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
	InputText   string // bounded canonical execution input; never design/analytics context
	Process     string // compiled recipe shape, not a generated process label
}

type ResultSource interface {
	Result(context.Context, string, Instance) (ResultProof, error)
}

// VerifyResult requires a locally pinned attempt, not a Ticket's mutable latest
// pointer. Missing history is unavailable, never evidence that practice failed.
func VerifyResult(owner string, i Instance, proof ResultProof) error {
	if i.State != Active {
		return ErrUnavailable
	}
	return verifyResultIdentity(owner, i, proof)
}

type resultExpectation struct {
	owner, workspace, incarnation, ticket, run, attempt, inputDigest string
	admittedAt                                                       int64
}

// All practice paths use the same exact canonical proof boundary; none adopt
// latest pointers, actor labels, timestamps alone or imported result snippets.
func verifyResultProof(want resultExpectation, proof ResultProof) error {
	if proof.OwnerID != want.owner || want.admittedAt <= 0 || !validID(want.attempt) || !validID(want.run) ||
		proof.Ref.Validate() != nil || proof.Ref.WorkspaceID != want.workspace || proof.Ref.Incarnation != want.incarnation ||
		proof.Ref.TicketID != want.ticket || proof.Ref.RunID != want.run || proof.AttemptID != want.attempt ||
		!validDigest(want.inputDigest) || proof.InputDigest != want.inputDigest || proof.Status != "succeeded" ||
		proof.StartedAt < want.admittedAt || proof.FinishedAt < proof.StartedAt ||
		!utf8.ValidString(proof.Text) || strings.ContainsRune(proof.Text, 0) || strings.TrimSpace(proof.Text) == "" || len(proof.Text) > MaxResultBytes || digest([]byte(proof.Text)) != proof.Ref.Digest {
		return ErrUnavailable
	}
	return nil
}

func verifyResultIdentity(owner string, i Instance, proof ResultProof) error {
	if i.Validate() != nil || i.OwnerID != owner {
		return ErrUnavailable
	}
	if err := verifyResultProof(resultExpectation{owner: owner, workspace: i.Definition.WorkspaceID, incarnation: i.Definition.Incarnation,
		ticket: i.Progress.TicketID, run: i.Progress.RunID, attempt: i.Progress.AttemptID, inputDigest: i.Progress.InputDigest, admittedAt: i.Progress.AcceptedAt}, proof); err != nil {
		return err
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
		review.InputDigest != i.Progress.InputDigest || review.Guidance != i.Definition.Guidance || review.SupportVersion != i.Definition.SupportVersion ||
		review.HintsShown != i.Progress.HintsShown || review.HintsUsed != i.Progress.HintsUsed {
		return nil, ErrConflict
	}
	competencies := []string{"delegate", "evaluate"}
	reused := false
	if i.Definition.Structure != DelegateInspect {
		if i.Progress.Original == nil || review.Original == nil || *review.Original != *i.Progress.Original ||
			review.OriginalReceipt != i.Definition.OriginalReceipt || review.Result.WorkspaceID != review.Original.WorkspaceID ||
			review.Result.Incarnation != review.Original.Incarnation || review.Result.TicketID == review.Original.TicketID ||
			review.Result.RunID == review.Original.RunID || review.Result.ArtifactID == review.Original.ArtifactID {
			return nil, ErrConflict
		}
		switch i.Definition.Structure {
		case ImproveResult:
			if strings.TrimSpace(i.Progress.Input) == "" || strings.TrimSpace(review.Change) == "" {
				return nil, ErrInvalid
			}
			competencies = []string{"evaluate", "improve"}
		case RepeatSuccess:
			if !validDigest(i.Progress.OriginalInputDigest) || strings.TrimSpace(i.Progress.InputChange) == "" ||
				normalizedInputDigest(i.Progress.Input) == i.Progress.OriginalInputDigest {
				return nil, ErrInvalid
			}
			competencies = []string{"evaluate"}
			if review.Decision == "use" {
				competencies = append(competencies, "reuse")
				reused = true
			}
		default:
			return nil, ErrUnsupported
		}
	}
	support := review.Guidance
	if review.HintsShown || review.HintsUsed {
		support = GuidanceGuided
	}
	if review.SupportVersion != GuidanceProtocolVersion {
		support = GuidanceUnknown
	}
	inputDigest := i.Progress.InputDigest
	if i.Definition.Structure != ImproveResult && strings.TrimSpace(i.Progress.Input) != "" {
		inputDigest = normalizedInputDigest(i.Progress.Input)
	}
	return creditsForReview(owner, review, competencies, support, reused, inputDigest)
}

func creditsForReview(owner string, review Review, competencies []string, support Guidance, reused bool, inputDigest string) ([]Evidence, error) {
	out := make([]Evidence, 0, len(competencies))
	for _, competency := range competencies {
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
		attempt, err := encode(struct{ Owner, Workspace, Incarnation, Ticket, Input, Competency string }{owner, review.Result.WorkspaceID, review.Result.Incarnation, review.Result.TicketID, inputDigest, competency}, MaxResponseBytes)
		if err != nil {
			return nil, err
		}
		out = append(out, Evidence{Key: digest([]byte(key)), ReviewID: review.ID, Structure: review.Structure, Version: review.Version,
			Competency: competency, Guidance: support, SupportVersion: review.SupportVersion, AttemptDigest: digest([]byte(attempt)), Reused: reused && competency == "reuse", CreatedAt: review.DecidedAt})
	}
	return out, nil
}
