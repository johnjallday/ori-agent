package learningmissions

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"slices"
	"time"

	"github.com/google/uuid"
)

// One process-wide slot, in addition to the durable per-owner unique claim.
// Contention never queues an unreviewed later call or starts a repair attempt.
var generationSlot = make(chan struct{}, 1)

// SetDesigner is startup wiring only, before requests or subscriptions run.
func (s *Service) SetDesigner(designer Designer) { s.designer = designer }

func generationOwner(owner Owner, workspaceID string) Owner {
	// An explicit request authorizes this scope for this one design call. It
	// does not persist observation consent or borrow it from another workspace.
	owner.Preferences.Observe = owner.Preferences.Observe && slices.Contains(owner.Preferences.WorkspaceIDs, workspaceID)
	owner.Preferences.WorkspaceIDs = []string{workspaceID}
	owner.Preferences.Mode = "on_request" // this explicit one-call consent is not an automatic trigger
	return owner
}

func (s *Service) generate(ctx context.Context, owner string, req ProposalRequest) (Outcome, error) {
	return s.generateWithMilestone(ctx, owner, req, nil)
}

func proposalOwner(owner Owner, workspace string, automatic bool) Owner {
	owner = generationOwner(owner, workspace)
	if automatic {
		owner.Preferences.Mode = "automatic"
	}
	return owner
}

func (s *Service) generateWithMilestone(ctx context.Context, owner string, req ProposalRequest, milestone *Milestone) (Outcome, error) {
	if milestone != nil && (req.WorkspaceID != milestone.WorkspaceID || req.ModelFingerprint != milestone.ModelFingerprint) {
		return Outcome{}, ErrInvalid
	}
	if !validID(req.WorkspaceID) || !validDigest(req.ModelFingerprint) || req.ConsentVersion != DisclosureVersion {
		return Outcome{}, ErrInvalid
	}
	release, err := s.permit()
	if err != nil {
		return Outcome{}, err
	}
	defer release()
	var snapshot ContextSnapshot
	var config ModelConfiguration
	var claimedOwner Owner
	slotHeld := false
	defer func() {
		if slotHeld {
			<-generationSlot
		}
	}()
	var payload any = req
	if milestone != nil {
		payload = struct {
			Request     ProposalRequest
			MilestoneID string
		}{req, milestone.ID}
	}
	s.effects.RLock()
	out, err := s.apply(ctx, owner, req.MutationRequest, "generate", payload, func(current Owner) error {
		if s.designer == nil || current.Preferences.Mode == "off" {
			return ErrUnavailable
		}
		if milestone != nil {
			if err := s.validateMilestone(ctx, current, *milestone); err != nil {
				return err
			}
		}
		var err error
		config, err = s.designer.Configuration(ctx)
		if err != nil || config.Validate() != nil {
			return ErrUnavailable
		}
		if config.Fingerprint != req.ModelFingerprint {
			return ErrConflict
		}
		claimedOwner = proposalOwner(current, req.WorkspaceID, milestone != nil)
		snapshot, err = BuildContext(ctx, s.source, claimedOwner, s.now())
		if err == nil {
			snapshot, err = filterContext(snapshot, req.Structure, req.CandidateRef)
		}
		if err == nil {
			snapshot, err = s.rankContext(ctx, claimedOwner, snapshot)
		}
		if err != nil {
			return err
		}
		if len(snapshot.Data.Candidates) == 0 {
			return ErrUnavailable
		}
		select {
		case generationSlot <- struct{}{}:
			slotHeld = true
			return nil
		default:
			return ErrConflict
		}
	}, func(m *Mutation, op *Operation) error {
		if err := m.expireGeneration(s.now()); err != nil {
			return err
		}
		var occupied, calls int
		var last sql.NullInt64
		if err := m.tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM learning_instances WHERE owner_id=? AND state IN ('active','blocked','offered')`, owner).Scan(&occupied); err != nil {
			return ErrUnavailable
		}
		if occupied != 0 {
			return ErrConflict
		}
		if err := m.tx.QueryRowContext(ctx, `SELECT COUNT(*),MAX(created_at_ms) FROM learning_operations WHERE owner_id=? AND kind='generate' AND created_at_ms>=?`, owner, s.now().Add(-24*time.Hour).UnixMilli()).Scan(&calls, &last); err != nil {
			return ErrUnavailable
		}
		if calls >= DailyCalls || (last.Valid && op.CreatedAt-last.Int64 < ExplicitSpacing.Milliseconds()) {
			return ErrLimit
		}
		if milestone != nil {
			if err := m.claimMilestone(*milestone, snapshot, config, op); err != nil {
				return err
			}
		}
		for _, candidate := range snapshot.Bindings {
			if err := m.BindSource(candidate.WorkspaceID, candidate.Incarnation); err != nil {
				return err
			}
		}
		op.Status, op.ReservedTokens, op.ModelFingerprint = "pending", GenerationReservedTokens, config.Fingerprint
		return nil
	})
	s.effects.RUnlock()
	if err != nil || !out.fresh {
		return out, err // persisted retry/unknown commit never invokes the model again
	}
	claimedOwner.Epoch = out.Operation.Epoch
	claimedOwner.Revision = req.ExpectedRevision + 1
	callCtx, cancel := context.WithTimeout(ctx, GenerationTimeout)
	reply, callErr := s.designer.Generate(callCtx, config, snapshot)
	if callCtx.Err() != nil {
		callErr = callCtx.Err()
	}
	cancel()
	// A disconnected browser must not leave a known outcome pending forever.
	// The reset permit remains held; epoch/revision checks still fence clear.
	finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer finishCancel()
	return s.finishGeneration(finishCtx, claimedOwner, config, snapshot, out.Operation, reply, callErr)
}

func generationFailure(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, ErrConflict):
		return "state_changed"
	case errors.Is(err, ErrInvalid), errors.Is(err, ErrLimit):
		return "invalid_proposal"
	default:
		return "personalization_unavailable"
	}
}

func (s *Service) finishGeneration(ctx context.Context, claimed Owner, config ModelConfiguration, snapshot ContextSnapshot, op Operation, reply GenerationReply, callErr error) (Outcome, error) {
	s.effects.RLock()
	defer s.effects.RUnlock()
	current, err := s.store.Owner(ctx, claimed.ID)
	if err != nil || current.Epoch != claimed.Epoch {
		return Outcome{}, ErrConflict
	}
	old, err := s.store.Operation(ctx, claimed.ID, op.ID)
	if err != nil {
		return Outcome{}, err
	}
	if old.Status != "pending" {
		return s.outcome(ctx, claimed.ID, old), nil
	}
	if current.Revision != claimed.Revision {
		callErr = ErrConflict
	}
	var definition Definition
	if callErr == nil {
		definition, callErr = ValidateProposal(reply.Text, snapshot, config, s.now())
	}
	if callErr == nil && op.Automatic {
		event, err := s.store.Operation(ctx, claimed.ID, op.MilestoneID)
		if err != nil || event.Milestone == nil || !event.Milestone.allowed(current) || event.Milestone.ExpiresAt <= s.now().UnixMilli() {
			callErr = ErrConflict
		}
	}
	if callErr == nil {
		freshModel, modelErr := s.designer.Configuration(ctx)
		fresh, sourceErr := BuildContext(ctx, s.source, proposalOwner(current, definition.WorkspaceID, op.Automatic), s.now())
		if sourceErr == nil {
			fresh, sourceErr = filterContext(fresh, snapshot.StructureFilter, snapshot.CandidateFilter)
		}
		if sourceErr == nil {
			fresh, sourceErr = s.rankContext(ctx, proposalOwner(current, definition.WorkspaceID, op.Automatic), fresh)
		}
		before, beforeErr := snapshot.ModelJSON()
		after, afterErr := fresh.ModelJSON()
		if modelErr != nil || freshModel != config || sourceErr != nil || beforeErr != nil || afterErr != nil || !bytes.Equal(before, after) || s.validateSource(ctx, claimed.ID, definition) != nil {
			callErr = ErrConflict
		}
	}
	op.ActualTokens = reply.ActualTokens
	op.Status = "failed"
	if callErr != nil {
		op.Failure = generationFailure(callErr)
		if errors.Is(callErr, ErrConflict) {
			op.Status = "invalidated"
		}
	}
	_, err = s.store.Change(ctx, claimed.ID, current.Revision, claimed.Epoch, false, func(m *Mutation) error {
		if callErr == nil {
			if err := m.CheckSource(definition); err != nil {
				return err
			}
			now := s.now().UnixMilli()
			definition.Automatic = op.Automatic
			i := Instance{ID: uuid.NewString(), OwnerID: claimed.ID, Epoch: claimed.Epoch, Revision: 1, State: Offered,
				Definition: definition, CreatedAt: now, UpdatedAt: now}
			if err := m.InsertInstance(i); err != nil {
				return err
			}
			op.Status, op.InstanceID = "succeeded", i.ID
		}
		return m.UpdateOperation(op)
	})
	if err != nil {
		return Outcome{}, err // recover the same operation; never replay generation
	}
	return s.outcome(ctx, claimed.ID, op), nil
}

func (m *Mutation) expireGeneration(now time.Time) error {
	var id string
	err := m.tx.QueryRowContext(m.ctx, `SELECT id FROM learning_operations WHERE owner_id=? AND kind='generate' AND status='pending' AND created_at_ms<=? LIMIT 1`, m.owner.ID, now.Add(-GenerationTimeout).UnixMilli()).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return ErrUnavailable
	}
	op, err := readOperation(m.ctx, m.tx, m.owner.ID, id)
	if err != nil {
		return err
	}
	op.Status, op.Failure = "unknown", "outcome_unknown"
	return m.UpdateOperation(op)
}

// recoverGeneration expires only a previously admitted claim whose deadline
// passed. Startup/events may retire it, never replay it or catch up suggestions.
// GETs never call this. The caller holds the service effects lock and WorkGate.
func (s *Service) recoverGeneration(ctx context.Context, owner string) error {
	if s.store == nil || s.store.db == nil {
		return ErrUnavailable
	}
	var id string
	err := s.store.db.QueryRowContext(ctx, `SELECT id FROM learning_operations WHERE owner_id=? AND kind='generate' AND status='pending' AND created_at_ms<=? LIMIT 1`, owner, s.now().Add(-GenerationTimeout).UnixMilli()).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return ErrUnavailable
	}
	current, err := s.store.Owner(ctx, owner)
	if err != nil {
		return err
	}
	_, err = s.store.Change(ctx, owner, current.Revision, current.Epoch, false, func(m *Mutation) error {
		op, err := readOperation(ctx, m.tx, owner, id)
		if err != nil || op.Status != "pending" {
			return ErrConflict
		}
		op.Status, op.Failure = "unknown", "outcome_unknown"
		return m.UpdateOperation(op)
	})
	return err
}
