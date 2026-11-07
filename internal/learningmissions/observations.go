package learningmissions

import (
	"context"

	"github.com/google/uuid"
)

const MaxPendingObservations = 20

// ObservedWork is a local host Start claim, not a learning instance or a portable
// actor assertion. No input/result bytes are copied into this claim.
type ObservedWork struct {
	ID                  string `json:"id"`
	Epoch               int64  `json:"epoch"`
	Revision            int64  `json:"revision"`
	ObservationRevision int64  `json:"observation_revision"`
	WorkspaceID         string `json:"studio_id"`
	Incarnation         string `json:"incarnation"`
	TicketID            string `json:"ticket_id"`
	StartToken          string `json:"start_token"`
	InputDigest         string `json:"input_digest"`
	RunID               string `json:"run_id,omitempty"`
	ReviewID            string `json:"review_id,omitempty"`
	CreatedAt           int64  `json:"created_at_ms"`
}

func (o ObservedWork) Validate() error {
	if !validID(o.ID) || !validID(o.WorkspaceID) || !validID(o.Incarnation) || !validID(o.TicketID) || !validID(o.StartToken) ||
		o.Epoch < 1 || o.Revision < 1 || o.ObservationRevision < 1 || o.CreatedAt <= 0 || !validDigest(o.InputDigest) ||
		(o.RunID != "" && !validID(o.RunID)) || (o.ReviewID != "" && !validID(o.ReviewID)) {
		return ErrInvalid
	}
	return nil
}

func (o ObservedWork) allowed(owner Owner) bool {
	return o.Validate() == nil && o.Epoch == owner.Epoch && owner.Preferences.ObservationRevision == o.ObservationRevision &&
		owner.Preferences.Observes(o.WorkspaceID, o.Incarnation) && o.CreatedAt >= owner.Preferences.ObservationSince
}

// ObservedSource only supports compiled reviewed, bounded supplied-text work.
// Ordinary unrestricted tasks and imported actor/history labels are unknown.
type ObservedSource interface {
	ObservedStart(context.Context, string, string, string, string) (ObservedWork, error)
	BindObservedStart(context.Context, string, ObservedWork) error
	ObservedResult(context.Context, string, ObservedWork) (ResultProof, error)
	ObservedReady(context.Context, string, ObservedWork) (bool, error)
	ObservedTicketLink(context.Context, string, ObservedWork) string
}

// ObservationConsent is captured before the winning Start records its execution
// approval. A later opt-in (even in the same millisecond) cannot adopt that Start.
type ObservationConsent struct {
	owner, workspace, incarnation string
	epoch, revision               int64
}

func (s *Service) CaptureObservationConsent(ctx context.Context, owner, workspace string) (ObservationConsent, error) {
	if s == nil || s.store == nil {
		return ObservationConsent{}, ErrUnavailable
	}
	current, err := s.store.Owner(ctx, owner)
	if err != nil {
		return ObservationConsent{}, err
	}
	incarnation := current.Preferences.ScopeBindings[workspace]
	if current.Revision == 0 || current.Preferences.ObservationRevision < 1 || !current.Preferences.Observes(workspace, incarnation) {
		return ObservationConsent{}, nil
	}
	return ObservationConsent{owner: owner, workspace: workspace, incarnation: incarnation, epoch: current.Epoch, revision: current.Preferences.ObservationRevision}, nil
}

// ObserveStart is a compiled callback after the winning owner-facing execution
// review. Failure must not cancel actual work. There is no public claim API.
func (s *Service) ObserveStart(ctx context.Context, owner, workspace, ticket, token string, consent ObservationConsent) error {
	if s == nil || s.store == nil {
		return ErrUnavailable
	}
	if consent.owner == "" {
		return nil
	}
	if consent.owner != owner || consent.workspace != workspace {
		return ErrUnavailable
	}
	s.effects.RLock()
	defer s.effects.RUnlock()
	release, err := s.permit()
	if err != nil {
		return err
	}
	defer release()
	current, err := s.store.Owner(ctx, owner)
	if err != nil {
		return err
	}
	incarnation := current.Preferences.ScopeBindings[workspace]
	if current.Revision == 0 || current.Epoch != consent.epoch || current.Preferences.ObservationRevision != consent.revision || incarnation != consent.incarnation || !current.Preferences.Observes(workspace, incarnation) {
		return ErrUnavailable
	}
	reader, ok := s.source.(ObservedSource)
	if !ok {
		return ErrUnavailable
	}
	work, err := reader.ObservedStart(ctx, owner, workspace, ticket, token)
	if err != nil || work.WorkspaceID != workspace || work.TicketID != ticket || work.StartToken != token || work.Incarnation != incarnation {
		return ErrUnavailable
	}
	work.ID, work.Epoch, work.Revision = uuid.NewString(), current.Epoch, 1
	work.ObservationRevision, work.CreatedAt = current.Preferences.ObservationRevision, s.now().UnixMilli()
	if !work.allowed(current) || work.RunID != "" || work.ReviewID != "" {
		return ErrUnavailable
	}
	data, err := encode(work, MaxResponseBytes)
	if err != nil {
		return err
	}
	admitted := false
	_, err = s.store.Change(ctx, owner, current.Revision, current.Epoch, false, func(m *Mutation) error {
		if !work.allowed(m.owner) {
			return ErrConflict
		}
		if err := m.checkScope(work.WorkspaceID, work.Incarnation); err != nil {
			return err
		}
		var tracked int
		if err := m.tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM learning_instances WHERE owner_id=? AND epoch=? AND state IN ('active','blocked') AND json_extract(definition,'$.studio_id')=? AND json_extract(progress,'$.ticket_id')=?`, owner, current.Epoch, workspace, ticket).Scan(&tracked); err != nil {
			return ErrUnavailable
		}
		if tracked != 0 {
			return ErrConflict
		} // no parallel synthetic enrollment/credit for mission work
		var duplicate int
		if err := m.tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM learning_operations WHERE owner_id=? AND epoch=? AND kind='observe' AND json_extract(record,'$.observation.start_token')=?`, owner, current.Epoch, token).Scan(&duplicate); err != nil {
			return ErrUnavailable
		}
		if duplicate != 0 {
			return ErrConflict
		}
		var pending int
		if err := m.tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM learning_operations WHERE owner_id=? AND kind='observe' AND status='pending'`, owner).Scan(&pending); err != nil {
			return ErrUnavailable
		}
		if pending >= MaxPendingObservations {
			return ErrLimit
		}
		op := Operation{ID: work.ID, Kind: "observe", Status: "pending", Epoch: work.Epoch, RequestDigest: digest([]byte(data)),
			TicketID: work.TicketID, AttemptID: work.StartToken, CreatedAt: work.CreatedAt, Observation: &work}
		if err := m.InsertOperation(op); err != nil {
			return err
		}
		admitted = true
		return nil
	})
	if err != nil || !admitted {
		return err
	}
	// Attempt this finite cross-store correlation once. Unknown writes are never
	// replayed; the canonical token/claim can still reserve the same actual Run.
	return reader.BindObservedStart(ctx, owner, work)
}

type ObservedRun struct {
	OperationID, WorkspaceID, TicketID, StartToken, InputDigest, RunID string
}

// ReserveObservedRun pins the first canonical Run before creation. It neither
// creates work nor lets a retry/new Start replace the original attempt.
func (s *Service) ReserveObservedRun(ctx context.Context, owner string, req ObservedRun) error {
	id, token, run := req.OperationID, req.StartToken, req.RunID
	if s == nil || s.store == nil || !validID(run) {
		return ErrUnavailable
	}
	s.effects.RLock()
	defer s.effects.RUnlock()
	release, err := s.permit()
	if err != nil {
		return err
	}
	defer release()
	current, err := s.store.Owner(ctx, owner)
	if err != nil {
		return err
	}
	before, err := s.store.Operation(ctx, owner, id)
	if err != nil || before.Kind != "observe" || before.Status != "pending" || before.Observation == nil ||
		!before.Observation.allowed(current) || before.Observation.StartToken != token || before.Observation.WorkspaceID != req.WorkspaceID || before.Observation.TicketID != req.TicketID || before.Observation.InputDigest != req.InputDigest {
		return ErrUnavailable
	}
	if before.Observation.RunID != "" {
		if before.Observation.RunID == run {
			return nil
		}
		return ErrConflict
	}
	_, err = s.store.Change(ctx, owner, current.Revision, current.Epoch, false, func(m *Mutation) error {
		op, err := readOperation(ctx, m.tx, owner, id)
		if err != nil || op.Observation == nil || op.Status != "pending" || !op.Observation.allowed(m.owner) || op.Observation.StartToken != token || op.Observation.RunID != "" {
			return ErrConflict
		}
		if err := m.checkScope(op.Observation.WorkspaceID, op.Observation.Incarnation); err != nil {
			return err
		}
		op.Observation.RunID, op.Observation.Revision = run, op.Observation.Revision+1
		return m.UpdateOperation(op)
	})
	return err
}
