package learningmissions

import "context"

type ObservedView struct {
	ID          string `json:"id"`
	Revision    int64  `json:"revision"`
	WorkspaceID string `json:"studio_id"`
	TicketID    string `json:"ticket_id"`
	Ready       bool   `json:"ready"`
	TicketHref  string `json:"ticket_href,omitempty"`
}

func (s *SQLiteStore) pendingObservations(ctx context.Context, owner string) ([]ObservedWork, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM learning_operations WHERE owner_id=? AND kind='observe' AND status='pending' ORDER BY created_at_ms DESC,id LIMIT ?`, owner, MaxPendingObservations+1)
	if err != nil {
		return nil, ErrUnavailable
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, ErrUnavailable
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil || len(ids) > MaxPendingObservations {
		return nil, ErrUnavailable
	}
	out := []ObservedWork{}
	for _, id := range ids {
		op, err := s.Operation(ctx, owner, id)
		if err != nil || op.Observation == nil {
			return nil, ErrUnavailable
		}
		out = append(out, *op.Observation)
	}
	return out, nil
}

func (s *Service) visibleObservedOutcome(ctx context.Context, owner string, out Outcome) Outcome {
	redacted := out
	redacted.Observation, redacted.Review = nil, nil
	redacted.ResultText, redacted.ObservedInput = "", ""
	redacted.Operation.ReviewID = ""
	current, err := s.store.Owner(ctx, owner)
	if err != nil {
		return redacted
	}
	claim, err := s.store.Operation(ctx, owner, out.Operation.ObservationID)
	reader, ok := s.source.(ObservedSource)
	if err != nil || !ok || claim.Observation == nil || !claim.Observation.allowed(current) {
		return redacted
	}
	work := claim.Observation
	link := reader.ObservedTicketLink(ctx, owner, *work)
	if link == "" {
		return redacted
	}
	if out.Review != nil && out.Review.Decision == "" && (out.Review.Revision != work.Revision || out.Review.ExpiresAt <= s.now().UnixMilli()) {
		return redacted
	}
	ready, _ := reader.ObservedReady(ctx, owner, *work)
	if out.Review != nil {
		proof, err := reader.ObservedResult(ctx, owner, *work)
		if err != nil || VerifyObservedResult(owner, *work, proof) != nil || proof.Ref != out.Review.Result {
			return redacted
		}
		if out.Review.Decision == "" {
			out.ResultText, out.ObservedInput = proof.Text, proof.InputText
		}
	}
	out.Observation = &ObservedView{ID: work.ID, Revision: work.Revision, WorkspaceID: work.WorkspaceID, TicketID: work.TicketID, TicketHref: link, Ready: ready}
	return out
}

func (s *Service) observationViews(ctx context.Context, owner Owner) ([]ObservedView, error) {
	out := []ObservedView{}
	if !owner.Preferences.Observe || owner.Preferences.Mode == "off" || owner.Preferences.ObservationRevision < 1 {
		return out, nil
	}
	reader, ok := s.source.(ObservedSource)
	if !ok {
		return out, ErrUnavailable
	}
	works, err := s.store.pendingObservations(ctx, owner.ID)
	if err != nil {
		return out, err
	}
	for _, work := range works {
		if !work.allowed(owner) {
			continue
		}
		link := reader.ObservedTicketLink(ctx, owner.ID, work)
		if link == "" {
			continue
		}
		ready, _ := reader.ObservedReady(ctx, owner.ID, work)
		out = append(out, ObservedView{ID: work.ID, Revision: work.Revision, WorkspaceID: work.WorkspaceID, TicketID: work.TicketID, Ready: ready, TicketHref: link})
	}
	return out, nil
}
