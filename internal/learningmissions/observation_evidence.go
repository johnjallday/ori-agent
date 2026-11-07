package learningmissions

import "strings"

func VerifyObservedResult(owner string, work ObservedWork, proof ResultProof) error {
	if !validID(owner) || work.Validate() != nil || proof.Process != PlainDraftProcess ||
		!boundedText(proof.InputText, MaxDraftBytes, true) || len(proof.InputText) > MaxDraftBytes || digest([]byte(strings.TrimSpace(proof.InputText))) != work.InputDigest {
		return ErrUnavailable
	}
	return verifyResultProof(resultExpectation{owner: owner, workspace: work.WorkspaceID, incarnation: work.Incarnation, ticket: work.TicketID,
		run: work.RunID, attempt: work.StartToken, inputDigest: work.InputDigest, admittedAt: work.CreatedAt}, proof)
}

// ObservedPracticeEvidence has no synthetic instance. The local Start claim,
// exact canonical result and consumed owner decision are the authority. Support
// outside the versioned mission path stays unknown, never inferred from work.
func ObservedPracticeEvidence(owner string, work ObservedWork, review Review, proof ResultProof) ([]Evidence, error) {
	if VerifyObservedResult(owner, work, proof) != nil || review.Validate() != nil || review.Decision == "" ||
		review.ObservationID != work.ID || review.InstanceID != "" || review.Revision != work.Revision || review.Epoch != work.Epoch ||
		review.ID != work.ReviewID || review.Structure != DelegateInspect || review.Version != 1 || review.Result != proof.Ref ||
		review.CreatedAt < proof.FinishedAt || review.InputDigest != work.InputDigest || review.Original != nil || review.OriginalReceipt != "" || review.Change != "" ||
		review.SupportVersion != 0 || review.Guidance != GuidanceUnknown || review.HintsShown || review.HintsUsed {
		return nil, ErrConflict
	}
	return creditsForReview(owner, review, []string{"delegate", "evaluate"}, GuidanceUnknown, false, normalizedInputDigest(proof.InputText))
}
