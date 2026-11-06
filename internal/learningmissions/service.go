package learningmissions

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/resetstate"
)

// Candidate contains only host-authorized identity and bounded display data.
// It contains no paths, tools, permission grants or executable model fields.
type Candidate struct {
	Ref         string
	WorkspaceID string
	Incarnation string
	ScopeDigest string
	Name        string
}

// Source is the narrow canonical-owner adapter. Metadata is not an execution
// grant. A missing source/ownership/readiness check fails closed.
type Source interface {
	Candidate(context.Context, string, string) (Candidate, error)
	Validate(context.Context, string, Definition) error
}

type Service struct {
	// One server-owned service coordinates cross-store draft writes with
	// stop/clear. Durable epochs still fence callbacks/restarts; this lock
	// prevents a local clear from interleaving the admitted workspace write.
	effects sync.RWMutex
	store   *SQLiteStore
	gate    *resetstate.WorkGate
	source  Source
	now     func() time.Time
}

func NewService(store *SQLiteStore, gate *resetstate.WorkGate, source Source) *Service {
	return &Service{store: store, gate: gate, source: source, now: time.Now}
}

type MutationRequest struct {
	OperationID      string `json:"operation_id"`
	ExpectedRevision int64  `json:"expected_revision"`
}

type ProposalRequest struct {
	MutationRequest
	WorkspaceID string `json:"studio_id"`
}

type DecisionRequest struct {
	MutationRequest
	InstanceRevision int64  `json:"instance_revision"`
	Decision         string `json:"decision"`
}

type Outcome struct {
	fresh      bool      // only this call committed the new operation; never serialized
	Operation  Operation `json:"operation"`
	Instance   *Instance `json:"instance,omitempty"`
	Review     *Review   `json:"review,omitempty"`
	ResultText string    `json:"result_text,omitempty"` // explicit review only; never persisted here
}

type Status struct {
	Owner     Owner      `json:"owner"`
	Instances []Instance `json:"instances"`
}

func (s *Service) Status(ctx context.Context, owner string) (Status, error) {
	if s == nil || s.store == nil {
		return Status{}, ErrUnavailable
	}
	o, err := s.store.Owner(ctx, owner)
	if err != nil {
		return Status{}, err
	}
	instances, err := s.store.Instances(ctx, owner)
	for n := range instances {
		instances[n] = s.visibleInstance(ctx, owner, instances[n])
	}
	return Status{Owner: o, Instances: instances}, err
}

func (s *Service) Operation(ctx context.Context, owner, id string) (Outcome, error) {
	op, err := s.store.Operation(ctx, owner, id)
	if err != nil {
		return Outcome{}, err
	}
	return s.outcome(ctx, owner, op), nil
}

func (s *Service) outcome(ctx context.Context, owner string, op Operation) Outcome {
	out := Outcome{Operation: op}
	if op.InstanceID != "" {
		if i, err := s.store.Instance(ctx, owner, op.InstanceID); err == nil {
			out.Instance = &i
		}
	}
	if op.ReviewID != "" {
		if review, err := s.store.Review(ctx, owner, op.ReviewID); err == nil {
			out.Review = &review
		}
	}
	return out
}

func (s *Service) permit() (func(), error) {
	if s == nil || s.store == nil || s.gate == nil {
		return nil, resetstate.ErrWorkUntracked
	}
	return s.gate.Enter()
}

// apply atomically saves the effect and its idempotency receipt. preflight runs
// only for new operations; a retry reads the durable receipt, never replays an
// external effect. It runs outside the SQLite transaction to avoid owner/store
// lock inversion. Cross-store effects require a separately persisted claim.
func (s *Service) apply(ctx context.Context, owner string, req MutationRequest, kind string, payload any,
	preflight func(Owner) error, mutate func(*Mutation, *Operation) error,
) (Outcome, error) {
	release, err := s.permit()
	if err != nil {
		return Outcome{}, err
	}
	defer release()
	if !validID(owner) || !validID(req.OperationID) || req.ExpectedRevision < 0 {
		return Outcome{}, ErrInvalid
	}
	data, err := encode(struct {
		Kind    string `json:"kind"`
		Payload any    `json:"payload"`
	}{kind, payload}, MaxRecordBytes)
	if err != nil {
		return Outcome{}, err
	}
	hash := digest([]byte(data))
	current, err := s.store.Owner(ctx, owner)
	if err != nil {
		return Outcome{}, err
	}
	if old, err := s.store.Operation(ctx, owner, req.OperationID); err == nil {
		if old.Epoch != current.Epoch || old.Kind != kind || old.RequestDigest != hash {
			return Outcome{}, ErrConflict
		}
		return s.outcome(ctx, owner, old), nil
	}
	if current.Revision != req.ExpectedRevision {
		return Outcome{}, ErrConflict
	}
	if preflight != nil {
		if err := preflight(current); err != nil {
			return Outcome{}, err
		}
	}
	op := Operation{ID: req.OperationID, Kind: kind, Status: "succeeded", RequestDigest: hash, CreatedAt: s.now().UTC().UnixMilli()}
	_, err = s.store.Change(ctx, owner, current.Revision, current.Epoch, current.Revision == 0, func(m *Mutation) error {
		op.Epoch = m.owner.Epoch
		if err := mutate(m, &op); err != nil {
			return err
		}
		return m.InsertOperation(op)
	})
	if err != nil {
		// A concurrent identical request or uncertain commit can already have
		// produced the exact receipt. Reconcile it, never repeat the mutation.
		after, readErr := s.store.Owner(ctx, owner)
		if readErr == nil {
			if old, readErr := s.store.Operation(ctx, owner, req.OperationID); readErr == nil &&
				old.Epoch == after.Epoch && old.Kind == kind && old.RequestDigest == hash {
				return s.outcome(ctx, owner, old), nil
			}
		}
		return Outcome{}, err
	}
	out := s.outcome(ctx, owner, op)
	out.fresh = true
	return out, nil
}

// Propose is the deterministic, explicit fallback. It calls no model and never
// creates/starts a Ticket. The chosen workspace is not silently opted into
// ongoing observation or automatic suggestions.
func (s *Service) Propose(ctx context.Context, owner string, req ProposalRequest) (Outcome, error) {
	var candidate Candidate
	return s.apply(ctx, owner, req.MutationRequest, "propose", req, func(o Owner) error {
		if o.Preferences.Mode == "off" || s.source == nil || !validID(req.WorkspaceID) {
			return ErrUnavailable
		}
		var err error
		candidate, err = s.source.Candidate(ctx, owner, req.WorkspaceID)
		if err != nil || candidate.WorkspaceID != req.WorkspaceID || !boundedText(candidate.Name, 120, true) {
			return ErrUnavailable
		}
		return nil
	}, func(m *Mutation, op *Operation) error {
		// Active work takes priority; no recommendation feed alongside it.
		var occupied int
		if err := m.tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM learning_instances WHERE owner_id=? AND state IN ('active','blocked','offered')`, owner).Scan(&occupied); err != nil {
			return ErrUnavailable
		}
		if occupied != 0 {
			return ErrConflict
		}
		if err := m.BindSource(candidate.WorkspaceID, candidate.Incarnation); err != nil {
			return err
		}
		structure, _ := Lookup(DelegateInspect, 1)
		i := Instance{ID: uuid.NewString(), OwnerID: owner, Epoch: m.owner.Epoch, Revision: 1, State: Offered,
			Definition: Definition{Structure: DelegateInspect, Version: 1, WorkspaceID: candidate.WorkspaceID,
				Incarnation: candidate.Incarnation, CandidateRef: candidate.Ref, ScopeDigest: candidate.ScopeDigest,
				Goal: "Delegate a small draft and inspect the result", Scope: "Use only text you explicitly supply in this workspace. Nothing is sent or run until you review and start a Ticket.",
				Why: "You chose this workspace to practice reviewing useful work.", Steps: structure.Steps,
				Guidance: m.owner.Preferences.Guidance, ExpiresAt: s.now().Add(OfferLifetime).UnixMilli()},
			CreatedAt: op.CreatedAt, UpdatedAt: op.CreatedAt}
		op.InstanceID = i.ID
		return m.InsertInstance(i)
	})
}

func (s *Service) Decide(ctx context.Context, owner, id string, req DecisionRequest) (Outcome, error) {
	if s == nil {
		return Outcome{}, ErrUnavailable
	}
	s.effects.Lock()
	defer s.effects.Unlock()
	if !validID(id) || req.InstanceRevision < 1 || !slices.Contains([]string{"accept", "stop", "defer", "decline"}, req.Decision) {
		return Outcome{}, ErrInvalid
	}
	payload := struct {
		ID      string          `json:"id"`
		Request DecisionRequest `json:"request"`
	}{id, req}
	return s.apply(ctx, owner, req.MutationRequest, "decide", payload, func(o Owner) error {
		if req.Decision != "accept" {
			return nil // stopping never needs the old source to remain available
		}
		i, err := s.store.Instance(ctx, owner, id)
		if err != nil || s.source == nil || o.Preferences.Mode == "off" {
			return ErrUnavailable
		}
		if i.Definition.ExpiresAt <= s.now().UnixMilli() {
			return ErrConflict
		}
		candidate, err := s.source.Candidate(ctx, owner, i.Definition.WorkspaceID)
		if err != nil || candidate.Ref != i.Definition.CandidateRef || candidate.ScopeDigest != i.Definition.ScopeDigest {
			return ErrUnavailable
		}
		return s.validateSource(ctx, owner, i.Definition)
	}, func(m *Mutation, op *Operation) error {
		i, err := m.Instance(id)
		if err != nil {
			return err
		}
		if i.Revision != req.InstanceRevision {
			return ErrConflict
		}
		if req.Decision == "accept" {
			if err := m.CheckSource(i.Definition); err != nil {
				return err
			}
		}
		next := map[string]State{"accept": Active, "stop": Stopped, "defer": Deferred, "decline": Declined}[req.Decision]
		if !CanTransition(i.State, next) {
			return ErrConflict
		}
		i.State, i.Revision, i.UpdatedAt = next, i.Revision+1, op.CreatedAt
		if next == Active {
			i.Progress.AcceptedAt = op.CreatedAt
		}
		op.InstanceID = i.ID
		return m.UpdateInstance(i, req.InstanceRevision)
	})
}

// ClearHistory fences pending callbacks by advancing the durable epoch in the
// same transaction as deletion. It never deletes/cancels a Ticket or Run.
func (s *Service) ClearHistory(ctx context.Context, owner string, req MutationRequest) (Outcome, error) {
	if s == nil {
		return Outcome{}, ErrUnavailable
	}
	s.effects.Lock()
	defer s.effects.Unlock()
	return s.apply(ctx, owner, req, "clear_history", req, nil, func(m *Mutation, op *Operation) error {
		for _, query := range []string{
			`DELETE FROM learning_evidence WHERE owner_id=?`,
			`DELETE FROM learning_reviews WHERE owner_id=?`,
			`DELETE FROM learning_instances WHERE owner_id=?`,
			`DELETE FROM learning_sources WHERE owner_id=?`,
			`UPDATE learning_operations SET status='invalidated',record=json_set(record,'$.status','invalidated') WHERE owner_id=? AND status='pending'`,
		} {
			if _, err := m.tx.ExecContext(ctx, query, owner); err != nil {
				return ErrUnavailable
			}
		}
		m.owner.Epoch++
		op.Epoch = m.owner.Epoch
		return nil
	})
}

func (s *Service) validateSource(ctx context.Context, owner string, d Definition) error {
	incarnation, err := s.store.SourceIncarnation(ctx, owner, d.WorkspaceID)
	if err != nil || incarnation != d.Incarnation || s.source == nil {
		return ErrUnavailable
	}
	return s.source.Validate(ctx, owner, d)
}

// IsUnavailable groups only evidence absence, not malformed commands/conflicts.
func IsUnavailable(err error) bool { return errors.Is(err, ErrUnavailable) }
