package learningmissions

import (
	"context"
	"encoding/json"
)

// EvidenceView is explicit owner inspection, never a status/model projection.
// It issues no challenge, decision, operation or new practice credit.
type EvidenceView struct {
	Evidence   Evidence      `json:"evidence"`
	Review     Review        `json:"review"`
	ResultText string        `json:"result_text"`
	InputText  string        `json:"input_text"`
	TicketHref string        `json:"ticket_href,omitempty"`
	Original   *OriginalView `json:"original,omitempty"`
}

func (s *SQLiteStore) Evidence(ctx context.Context, owner, key string) (Evidence, error) {
	if s == nil || s.db == nil || !validID(owner) || !validDigest(key) {
		return Evidence{}, ErrUnavailable
	}
	var data string
	if err := s.db.QueryRowContext(ctx, `SELECT record FROM learning_evidence WHERE owner_id=? AND dedup_key=?`, owner, key).Scan(&data); err != nil || len(data) > MaxResponseBytes {
		return Evidence{}, ErrUnavailable
	}
	var evidence Evidence
	if json.Unmarshal([]byte(data), &evidence) != nil || evidence.Key != key || evidence.Validate() != nil {
		return Evidence{}, ErrUnavailable
	}
	return evidence, nil
}

func (s *Service) EvidenceView(ctx context.Context, owner, key string) (EvidenceView, error) {
	if s == nil || s.store == nil {
		return EvidenceView{}, ErrUnavailable
	}
	evidence, err := s.store.Evidence(ctx, owner, key)
	if err != nil || evidence.Unavailable {
		return EvidenceView{}, ErrUnavailable
	}
	review, err := s.store.Review(ctx, owner, evidence.ReviewID)
	if err != nil || review.Decision == "" || review.DecidedAt != evidence.CreatedAt {
		return EvidenceView{}, ErrUnavailable
	}
	if review.ObservationID != "" {
		return s.observedEvidenceView(ctx, owner, evidence, review)
	}
	if review.InstanceID == "" {
		return EvidenceView{}, ErrUnavailable
	}
	instance, err := s.store.Instance(ctx, owner, review.InstanceID)
	if err != nil || s.validateSource(ctx, owner, instance.Definition) != nil || instance.Progress.ReviewID != review.ID ||
		review.Revision >= instance.Revision || review.Epoch != instance.Epoch || review.Structure != instance.Definition.Structure ||
		review.Version != instance.Definition.Version || review.InputDigest != instance.Progress.InputDigest ||
		review.SupportVersion != instance.Definition.SupportVersion || review.Guidance != instance.Definition.Guidance {
		return EvidenceView{}, ErrUnavailable
	}
	reader, ok := s.source.(ResultSource)
	if !ok {
		return EvidenceView{}, ErrUnavailable
	}
	proof, err := reader.Result(ctx, owner, instance)
	if err != nil || verifyResultIdentity(owner, instance, proof) != nil || review.Result != proof.Ref || review.DecidedAt < proof.FinishedAt ||
		!boundedText(proof.InputText, MaxDraftBytes, true) || len(proof.InputText) > MaxDraftBytes {
		return EvidenceView{}, ErrUnavailable
	}
	// Reconstruct the already-consumed review boundary only to check the credit
	// identity. This is the existing saved instance, not new enrollment/review.
	atReview := instance
	atReview.Revision = review.Revision
	credits, err := PracticeEvidence(owner, atReview, review)
	if err != nil {
		return EvidenceView{}, ErrUnavailable
	}
	matched := false
	for _, credit := range credits {
		if credit.Key == evidence.Key && credit.Competency == evidence.Competency {
			matched = true
		}
	}
	if !matched {
		return EvidenceView{}, ErrUnavailable
	}
	out := EvidenceView{Evidence: evidence, Review: review, ResultText: proof.Text, InputText: proof.InputText}
	if review.Original != nil {
		original, err := s.OriginalView(ctx, owner, instance.ID)
		if err != nil || original.Result != *review.Original {
			return EvidenceView{}, ErrUnavailable
		}
		out.Original = &original
	}
	if links, ok := s.source.(interface {
		TicketLink(context.Context, string, Instance) string
	}); ok {
		out.TicketHref = links.TicketLink(ctx, owner, instance)
	}
	return out, nil
}
