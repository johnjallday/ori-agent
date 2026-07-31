package wakeservice

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/wakeprotocol"
)

type reconcileFailure struct {
	code      wakeprotocol.Code
	uncertain bool
	message   string
}

func (e *reconcileFailure) Error() string {
	return e.message
}

func (s *Service) register(
	ctx context.Context,
	uid int,
	request wakeprotocol.Request,
	state rootState,
	now time.Time,
) wakeprotocol.Response {
	digest, err := wakeprotocol.MutationDigest(request)
	if err != nil {
		return s.refusal(request, wakeprotocol.ErrorCode(err), err.Error())
	}
	if previous := state.idempotency(request.IdempotencyKey); previous != nil {
		if previous.Digest != digest || previous.Operation != request.Operation {
			return s.refusal(request, wakeprotocol.CodeConflict, "idempotency key was already used for a different mutation")
		}
		public := state.public()
		return responseFromRecord(s.config.BuildVersion, request, *previous, &public)
	}
	if err := s.preflight(ctx, &state); err != nil {
		var failure *reconcileFailure
		if errors.As(err, &failure) {
			return s.refusal(request, failure.code, failure.message)
		}
		return s.refusal(request, wakeprotocol.CodePMSetFailed, err.Error())
	}

	before := cloneRootState(state)
	state.prune(now)
	target := wakeprotocol.Target{
		ID:      request.Candidate.ID,
		Source:  request.Candidate.Source,
		Purpose: request.Candidate.Purpose,
	}
	index, _ := state.findCandidate(target)
	candidate := *request.Candidate
	candidate.WakeAt = candidate.WakeAt.UTC()
	candidate.ExpiresAt = candidate.ExpiresAt.UTC()
	if index >= 0 {
		state.Candidates[index] = candidate
	} else {
		if len(state.Candidates) >= wakeprotocol.MaxCandidates {
			return s.refusal(request, wakeprotocol.CodeConflict, "wake candidate capacity is full")
		}
		state.Candidates = append(state.Candidates, candidate)
	}
	desired := state.winner(now)
	state.Intent = &replacementIntent{
		Previous:  cloneProgrammed(state.Programmed),
		Desired:   cloneCandidate(desired),
		StartedAt: now,
	}
	state.UpdatedAt = now
	if err := s.store.save(state); err != nil {
		return s.refusal(request, wakeprotocol.CodeStateFailed, err.Error())
	}

	if err := s.reconcile(ctx, &state, desired, now); err != nil {
		return s.finishFailedMutation(request, before, state, err)
	}
	public := state.public()
	response := s.success(request, "wake candidate registered and programmed event verified", &public, nil)
	state.remember(recordFromResponse(request, digest, response, now))
	state.UpdatedAt = now
	if err := s.store.save(state); err != nil {
		return s.uncertain(request, "pmset verification succeeded but final wake state could not be persisted")
	}
	_ = uid // recorded by the bounded audit trail added with full arbitration.
	return response
}

func (s *Service) cancel(
	ctx context.Context,
	uid int,
	request wakeprotocol.Request,
	state rootState,
	now time.Time,
) wakeprotocol.Response {
	digest, err := wakeprotocol.MutationDigest(request)
	if err != nil {
		return s.refusal(request, wakeprotocol.ErrorCode(err), err.Error())
	}
	if previous := state.idempotency(request.IdempotencyKey); previous != nil {
		if previous.Digest != digest || previous.Operation != request.Operation {
			return s.refusal(request, wakeprotocol.CodeConflict, "idempotency key was already used for a different mutation")
		}
		public := state.public()
		return responseFromRecord(s.config.BuildVersion, request, *previous, &public)
	}
	if err := s.preflight(ctx, &state); err != nil {
		var failure *reconcileFailure
		if errors.As(err, &failure) {
			return s.refusal(request, failure.code, failure.message)
		}
		return s.refusal(request, wakeprotocol.CodePMSetFailed, err.Error())
	}

	before := cloneRootState(state)
	index, _ := state.findCandidate(*request.Target)
	if index < 0 {
		return s.refusal(request, wakeprotocol.CodeNotFound, "wake candidate was not found")
	}
	state.Candidates = append(state.Candidates[:index], state.Candidates[index+1:]...)
	state.prune(now)
	desired := state.winner(now)
	state.Intent = &replacementIntent{
		Previous:  cloneProgrammed(state.Programmed),
		Desired:   cloneCandidate(desired),
		StartedAt: now,
	}
	state.UpdatedAt = now
	if err := s.store.save(state); err != nil {
		return s.refusal(request, wakeprotocol.CodeStateFailed, err.Error())
	}

	if err := s.reconcile(ctx, &state, desired, now); err != nil {
		return s.finishFailedMutation(request, before, state, err)
	}
	public := state.public()
	response := s.success(request, "wake candidate canceled and programmed event reconciled", &public, nil)
	state.remember(recordFromResponse(request, digest, response, now))
	state.UpdatedAt = now
	if err := s.store.save(state); err != nil {
		return s.uncertain(request, "pmset reconciliation succeeded but final wake state could not be persisted")
	}
	_ = uid
	return response
}

func (s *Service) verify(
	ctx context.Context,
	request wakeprotocol.Request,
	state rootState,
	now time.Time,
) wakeprotocol.Response {
	state.prune(now)
	_, candidate := state.findCandidate(*request.Target)
	if candidate == nil {
		return s.refusal(request, wakeprotocol.CodeNotFound, "wake candidate was not found or has expired")
	}
	desired := state.winner(now)
	if err := s.reconcile(ctx, &state, desired, now); err != nil {
		var failure *reconcileFailure
		if errors.As(err, &failure) && failure.uncertain {
			return s.uncertain(request, failure.message)
		}
		if errors.As(err, &failure) {
			return s.refusal(request, failure.code, failure.message)
		}
		return s.refusal(request, wakeprotocol.CodeVerificationFailed, err.Error())
	}
	if err := s.store.save(state); err != nil {
		return s.uncertain(request, "verified pmset state could not be persisted")
	}
	if state.Programmed == nil ||
		state.Programmed.ID != candidate.ID ||
		state.Programmed.Source != candidate.Source ||
		state.Programmed.Purpose != candidate.Purpose ||
		state.Programmed.WakeAt.After(candidate.WakeAt) {
		return s.refusal(request, wakeprotocol.CodeVerificationFailed, "candidate is not the directly programmed Herdr wake")
	}
	verification := &wakeprotocol.Verification{
		Target:           *request.Target,
		RequestedWakeAt:  candidate.WakeAt,
		ProgrammedWakeAt: state.Programmed.WakeAt,
		VerifiedAt:       now,
		Matched:          true,
	}
	public := state.public()
	return s.success(request, "matching Herdr pmset event verified", &public, verification)
}

func (s *Service) finishFailedMutation(
	request wakeprotocol.Request,
	before rootState,
	current rootState,
	err error,
) wakeprotocol.Response {
	var failure *reconcileFailure
	if errors.As(err, &failure) && failure.uncertain {
		_ = s.store.save(current)
		return s.uncertain(request, failure.message)
	}
	if saveErr := s.store.save(before); saveErr != nil {
		return s.uncertain(request, "wake mutation was refused but prior root state could not be restored")
	}
	if errors.As(err, &failure) {
		return s.refusal(request, failure.code, failure.message)
	}
	return s.refusal(request, wakeprotocol.CodePMSetFailed, err.Error())
}

func (s *Service) preflight(ctx context.Context, state *rootState) error {
	events, err := s.power.Events(ctx)
	if err != nil {
		return definite(wakeprotocol.CodePMSetFailed, err.Error())
	}
	owned, err := ownedEvents(events)
	if err != nil {
		return definite(wakeprotocol.CodeUnsafeHostSchedule, err.Error())
	}
	if len(owned) > 1 {
		return definite(wakeprotocol.CodeConflict, "multiple Herdr-owned pmset events require operator recovery")
	}
	if len(owned) == 1 && !knownOwnedEvent(owned[0], state) {
		return definite(wakeprotocol.CodeConflict, "an untracked Herdr-owned pmset event already exists")
	}
	return nil
}

// reconcile makes root state and the exact fixed-owner pmset event agree. It
// never issues a mutation until the complete supported schedule has parsed and
// every Herdr-owned event is provably accounted for by root state or intent.
func (s *Service) reconcile(
	ctx context.Context,
	state *rootState,
	desired *wakeprotocol.Candidate,
	now time.Time,
) error {
	events, err := s.power.Events(ctx)
	if err != nil {
		return definite(wakeprotocol.CodePMSetFailed, err.Error())
	}
	owned, err := ownedEvents(events)
	if err != nil {
		return definite(wakeprotocol.CodeUnsafeHostSchedule, err.Error())
	}
	if len(owned) > 1 {
		return definite(wakeprotocol.CodeConflict, "multiple Herdr-owned pmset events require operator recovery")
	}
	var actual *PowerEvent
	if len(owned) == 1 {
		actual = &owned[0]
		if !knownOwnedEvent(*actual, state) {
			return definite(wakeprotocol.CodeConflict, "an untracked Herdr-owned pmset event already exists")
		}
	}

	if desired == nil {
		if actual != nil {
			if err := s.power.Cancel(ctx, actual.At); err != nil {
				return uncertainFailure("exact Herdr pmset cancellation may have changed the host schedule")
			}
			remaining, err := s.power.Events(ctx)
			if err != nil {
				return uncertainFailure("exact Herdr pmset cancellation could not be verified")
			}
			ownedRemaining, parseErr := ownedEvents(remaining)
			if parseErr != nil || len(ownedRemaining) != 0 {
				return uncertainFailure("exact Herdr pmset cancellation did not produce a verifiable empty owned schedule")
			}
		}
		state.Programmed = nil
		state.Intent = nil
		state.ReconciledAt = now
		state.UpdatedAt = now
		return nil
	}

	expected := desired.WakeAt.UTC().Truncate(time.Second)
	if actual != nil && actual.At.Equal(expected) {
		state.Programmed = programmedFor(*desired, expected, now)
		state.Intent = nil
		state.ReconciledAt = now
		state.UpdatedAt = now
		return nil
	}

	if actual != nil {
		if err := s.power.Cancel(ctx, actual.At); err != nil {
			return uncertainFailure("Herdr pmset replacement may have stopped after exact cancellation")
		}
		afterCancel, err := s.power.Events(ctx)
		if err != nil {
			return uncertainFailure("Herdr pmset replacement cancellation could not be verified")
		}
		ownedAfterCancel, parseErr := ownedEvents(afterCancel)
		if parseErr != nil || len(ownedAfterCancel) != 0 {
			return uncertainFailure("Herdr pmset replacement left an unverified owned event")
		}
	}

	if err := s.power.Schedule(ctx, expected); err != nil {
		return uncertainFailure("Herdr pmset scheduling may have changed the host schedule without a response")
	}
	afterSchedule, err := s.power.Events(ctx)
	if err != nil {
		return uncertainFailure("new Herdr pmset event could not be read back")
	}
	ownedAfterSchedule, parseErr := ownedEvents(afterSchedule)
	if parseErr != nil || len(ownedAfterSchedule) != 1 || !ownedAfterSchedule[0].At.Equal(expected) {
		return uncertainFailure("new Herdr pmset event did not match the requested owner, type, and timestamp")
	}
	state.Programmed = programmedFor(*desired, expected, now)
	state.Intent = nil
	state.ReconciledAt = now
	state.UpdatedAt = now
	return nil
}

func ownedEvents(events []PowerEvent) ([]PowerEvent, error) {
	owned := make([]PowerEvent, 0, 1)
	for _, event := range events {
		if event.Owner != PMSetOwner {
			continue
		}
		if event.Type != "wake" && event.Type != PMSetEventType {
			return nil, fmt.Errorf("Herdr owner appears on an unsupported pmset event type")
		}
		owned = append(owned, event)
	}
	return owned, nil
}

func knownOwnedEvent(event PowerEvent, state *rootState) bool {
	if state.Programmed != nil && state.Programmed.Owner == PMSetOwner &&
		state.Programmed.EventType == PMSetEventType &&
		state.Programmed.WakeAt.UTC().Truncate(time.Second).Equal(event.At.UTC().Truncate(time.Second)) {
		return true
	}
	if state.Intent != nil {
		if state.Intent.Previous != nil &&
			state.Intent.Previous.WakeAt.UTC().Truncate(time.Second).Equal(event.At.UTC().Truncate(time.Second)) {
			return true
		}
		if state.Intent.Desired != nil &&
			state.Intent.Desired.WakeAt.UTC().Truncate(time.Second).Equal(event.At.UTC().Truncate(time.Second)) {
			return true
		}
	}
	return false
}

func programmedFor(candidate wakeprotocol.Candidate, wakeAt, now time.Time) *wakeprotocol.Programmed {
	return &wakeprotocol.Programmed{
		Target: wakeprotocol.Target{
			ID:      candidate.ID,
			Source:  candidate.Source,
			Purpose: candidate.Purpose,
		},
		WakeAt:       wakeAt.UTC(),
		ProgrammedAt: now.UTC(),
		Owner:        PMSetOwner,
		EventType:    PMSetEventType,
	}
}

func definite(code wakeprotocol.Code, message string) error {
	return &reconcileFailure{code: code, message: boundedMessage(message)}
}

func uncertainFailure(message string) error {
	return &reconcileFailure{
		code:      wakeprotocol.CodeUncertain,
		uncertain: true,
		message:   boundedMessage(message),
	}
}

func recordFromResponse(
	request wakeprotocol.Request,
	digest string,
	response wakeprotocol.Response,
	now time.Time,
) idempotencyRecord {
	return idempotencyRecord{
		Key:       request.IdempotencyKey,
		Digest:    digest,
		Operation: request.Operation,
		Result:    response.Result,
		Code:      response.Code,
		Message:   response.Message,
		AppliedAt: now,
	}
}

func responseFromRecord(
	build string,
	request wakeprotocol.Request,
	record idempotencyRecord,
	state *wakeprotocol.State,
) wakeprotocol.Response {
	return wakeprotocol.Response{
		ProtocolVersion: wakeprotocol.Version,
		RequestID:       request.RequestID,
		DaemonBuild:     build,
		Operation:       request.Operation,
		Result:          record.Result,
		Code:            record.Code,
		Message:         record.Message,
		State:           state,
	}
}

func cloneRootState(state rootState) rootState {
	cloned := state
	cloned.Candidates = append([]wakeprotocol.Candidate(nil), state.Candidates...)
	cloned.Idempotency = append([]idempotencyRecord(nil), state.Idempotency...)
	cloned.Programmed = cloneProgrammed(state.Programmed)
	if state.Intent != nil {
		cloned.Intent = &replacementIntent{
			Previous:  cloneProgrammed(state.Intent.Previous),
			Desired:   cloneCandidate(state.Intent.Desired),
			StartedAt: state.Intent.StartedAt,
		}
	}
	return cloned
}

func cloneCandidate(candidate *wakeprotocol.Candidate) *wakeprotocol.Candidate {
	if candidate == nil {
		return nil
	}
	copy := *candidate
	return &copy
}
