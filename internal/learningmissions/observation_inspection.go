package learningmissions

import "context"

func (s *Service) observedEvidenceView(ctx context.Context, owner string, evidence Evidence, review Review) (EvidenceView, error) {
	claim, err := s.store.Operation(ctx, owner, review.ObservationID)
	if err != nil || claim.Kind != "observe" || claim.Status != "succeeded" || claim.Observation == nil {
		return EvidenceView{}, ErrUnavailable
	}
	work := *claim.Observation
	current, err := s.store.Owner(ctx, owner)
	if err != nil || work.Epoch != current.Epoch || work.ReviewID != review.ID || review.Revision >= work.Revision {
		return EvidenceView{}, ErrUnavailable
	}
	reader, ok := s.source.(ObservedSource)
	if !ok {
		return EvidenceView{}, ErrUnavailable
	}
	proof, err := reader.ObservedResult(ctx, owner, work)
	if err != nil {
		return EvidenceView{}, ErrUnavailable
	}
	work.Revision = review.Revision // the saved, consumed boundary; not a new claim
	credits, err := ObservedPracticeEvidence(owner, work, review, proof)
	if err != nil {
		return EvidenceView{}, ErrUnavailable
	}
	for _, credit := range credits {
		if credit.Key == evidence.Key && credit.Competency == evidence.Competency {
			return EvidenceView{Evidence: evidence, Review: review, ResultText: proof.Text, InputText: proof.InputText, TicketHref: reader.ObservedTicketLink(ctx, owner, work)}, nil
		}
	}
	return EvidenceView{}, ErrUnavailable
}
