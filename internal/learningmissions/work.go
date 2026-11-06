package learningmissions

import (
	"context"
	"strings"

	"github.com/google/uuid"
)

// WorkHost delegates to canonical Ticket owners. Neither method starts work.
// MatchTicket must validate identity/binding, not just the existence of an ID.
type WorkHost interface {
	CheckWork(context.Context, string, Instance, string) error
	CreateTicket(context.Context, string, Instance) error
	MatchTicket(context.Context, string, Instance) error
}

type WorkRequest struct {
	MutationRequest
	InstanceRevision int64  `json:"instance_revision"`
	Input            string `json:"input"`
	Assignee         string `json:"assignee"`
}

// PrepareWork first commits the intended Ticket identity and exact input. Only
// the call that inserted that operation may attempt creation. Every replay is
// a lookup; unknown outcomes never silently create another Ticket.
func (s *Service) PrepareWork(ctx context.Context, owner, id string, req WorkRequest) (Outcome, error) {
	release, err := s.permit()
	if err != nil {
		return Outcome{}, err
	}
	defer release() // includes cross-store effect and its final receipt
	s.effects.RLock()
	defer s.effects.RUnlock()
	req.Input, req.Assignee = strings.TrimSpace(req.Input), strings.TrimSpace(req.Assignee)
	if !validID(id) || req.InstanceRevision < 1 || req.Input == "" || len(req.Input) > MaxDraftBytes ||
		!boundedText(req.Input, MaxDraftBytes, true) || !boundedText(req.Assignee, 128, true) {
		return Outcome{}, ErrInvalid
	}
	host, ok := s.source.(WorkHost)
	if !ok {
		return Outcome{}, ErrUnavailable
	}
	payload := struct {
		ID      string      `json:"id"`
		Request WorkRequest `json:"request"`
	}{id, req}
	out, err := s.apply(ctx, owner, req.MutationRequest, "prepare_work", payload, func(Owner) error {
		i, err := s.store.Instance(ctx, owner, id)
		if err != nil || s.validateSource(ctx, owner, i.Definition) != nil {
			return ErrUnavailable
		}
		if i.State != Active || i.Revision != req.InstanceRevision || i.Progress.OperationID != "" {
			return ErrConflict
		}
		return host.CheckWork(ctx, owner, i, req.Assignee)
	}, func(m *Mutation, op *Operation) error {
		i, err := m.Instance(id)
		if err != nil {
			return err
		}
		if i.State != Active || i.Revision != req.InstanceRevision || i.Progress.OperationID != "" {
			return ErrConflict
		}
		if err := m.CheckSource(i.Definition); err != nil {
			return err
		}
		i.Progress.Input, i.Progress.Assignee, i.Progress.InputDigest = req.Input, req.Assignee, digest([]byte(req.Input))
		i.Progress.TicketID, i.Progress.AttemptID, i.Progress.OperationID = uuid.NewString(), uuid.NewString(), op.ID
		i.Revision++
		i.UpdatedAt = op.CreatedAt
		op.Status, op.InstanceID, op.TicketID, op.AttemptID = "pending", i.ID, i.Progress.TicketID, i.Progress.AttemptID
		return m.UpdateInstance(i, req.InstanceRevision)
	})
	if err != nil || out.Operation.Status != "pending" || out.Instance == nil {
		return out, err
	}
	if out.fresh {
		// An error can mean the target committed and the reply was lost. Never
		// retry creation: read the reserved identity below in both cases.
		_ = host.CreateTicket(ctx, owner, *out.Instance)
	}
	if host.MatchTicket(ctx, owner, *out.Instance) != nil {
		return out, nil // visibly pending/unknown; only reconcile, never replay
	}
	current, err := s.store.Owner(ctx, owner)
	if err != nil {
		return out, err
	}
	_, err = s.store.Change(ctx, owner, current.Revision, out.Operation.Epoch, false, func(m *Mutation) error {
		i, err := m.Instance(id)
		if err != nil || i.State != Active || i.Progress.OperationID != out.Operation.ID || i.Progress.TicketID != out.Operation.TicketID {
			return ErrConflict
		}
		if err := m.CheckSource(i.Definition); err != nil {
			return err
		}
		op, err := readOperation(ctx, m.tx, owner, out.Operation.ID)
		if err != nil {
			return err
		}
		if op.Status == "succeeded" {
			return nil
		}
		op.Status = "succeeded"
		return m.UpdateOperation(op)
	})
	if err != nil {
		return out, err
	}
	return s.Operation(ctx, owner, out.Operation.ID)
}

// ReserveRun binds the one explicit existing Ticket start to a fresh canonical
// identity before Run creation. A late/unrelated retry cannot replace it. This
// compiled callback is not an HTTP handler or a mission-design tool.
func (s *Service) ReserveRun(ctx context.Context, owner, instanceID, ticketID, runID string, epoch int64) error {
	release, err := s.permit()
	if err != nil {
		return err
	}
	defer release()
	s.effects.RLock()
	defer s.effects.RUnlock()
	if _, err := uuid.Parse(runID); err != nil {
		return ErrInvalid
	}
	i, err := s.store.Instance(ctx, owner, instanceID)
	if err != nil || s.validateSource(ctx, owner, i.Definition) != nil {
		return ErrUnavailable
	}
	current, err := s.store.Owner(ctx, owner)
	if err != nil {
		return err
	}
	_, err = s.store.Change(ctx, owner, current.Revision, epoch, false, func(m *Mutation) error {
		i, err := m.Instance(instanceID)
		if err != nil || i.State != Active || i.Epoch != epoch || i.Progress.TicketID != ticketID ||
			i.Progress.OperationID == "" || i.Progress.AttemptID == "" || i.Progress.RunID != "" {
			return ErrConflict
		}
		if err := m.CheckSource(i.Definition); err != nil {
			return err
		}
		work, err := readOperation(ctx, m.tx, owner, i.Progress.OperationID)
		if err != nil || work.Status != "succeeded" || work.TicketID != ticketID || work.AttemptID != i.Progress.AttemptID {
			return ErrConflict
		}
		expected := i.Revision
		i.Progress.RunID, i.Revision, i.UpdatedAt = runID, i.Revision+1, s.now().UnixMilli()
		return m.UpdateInstance(i, expected)
	})
	return err
}
