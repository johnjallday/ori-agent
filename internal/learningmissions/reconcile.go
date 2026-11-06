package learningmissions

import "context"

// Reconcile only looks up previously reserved identities. A canonical event or
// startup may wake it; neither event payloads nor the latest Ticket Run pointer
// are evidence. It never creates/starts work, decides a review or grants credit.
func (s *Service) Reconcile(ctx context.Context, owner, workspaceID string) error {
	release, err := s.permit()
	if err != nil {
		return err
	}
	defer release()
	s.effects.Lock()
	defer s.effects.Unlock()
	instances, err := s.store.Instances(ctx, owner)
	if err != nil {
		return err
	}
	for _, i := range instances {
		if i.State != Active || (workspaceID != "" && i.Definition.WorkspaceID != workspaceID) || s.validateSource(ctx, owner, i.Definition) != nil {
			continue
		}
		if err := s.reconcileInstance(ctx, owner, i); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) reconcileInstance(ctx context.Context, owner string, i Instance) error {
	var work *Operation
	if i.Progress.OperationID != "" {
		op, err := s.store.Operation(ctx, owner, i.Progress.OperationID)
		if err != nil {
			return err
		}
		if op.Status == "pending" && op.Kind == "prepare_work" && op.TicketID == i.Progress.TicketID && op.AttemptID == i.Progress.AttemptID {
			if host, ok := s.source.(WorkHost); ok && host.MatchTicket(ctx, owner, i) == nil {
				op.Status = "succeeded"
				work = &op
			}
		}
	}
	var result *ResultRef
	if i.Progress.RunID != "" && i.Progress.Result == nil {
		if reader, ok := s.source.(ResultSource); ok {
			proof, err := reader.Result(ctx, owner, i)
			if err == nil && VerifyResult(owner, i, proof) == nil {
				result = &proof.Ref
			}
		}
	}
	if work == nil && result == nil {
		return nil
	}
	current, err := s.store.Owner(ctx, owner)
	if err != nil {
		return err
	}
	_, err = s.store.Change(ctx, owner, current.Revision, i.Epoch, false, func(m *Mutation) error {
		saved, err := m.Instance(i.ID)
		if err != nil || saved.State != Active || saved.Revision != i.Revision || saved.Progress.RunID != i.Progress.RunID {
			return ErrConflict
		}
		if err := m.CheckSource(saved.Definition); err != nil {
			return err
		}
		if work != nil {
			if err := m.UpdateOperation(*work); err != nil {
				return err
			}
		}
		if result != nil {
			saved.Progress.Result = result
			saved.Revision++
			saved.UpdatedAt = s.now().UnixMilli()
			return m.UpdateInstance(saved, i.Revision)
		}
		return nil
	})
	return err
}
