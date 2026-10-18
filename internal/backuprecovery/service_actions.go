package backuprecovery

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/vault"
)

type ConnectRequest struct {
	Generation        string `json:"generation"`
	Revision          uint64 `json:"revision"`
	OperationID       string `json:"operation_id"`
	ReviewToken       string `json:"review_token"`
	AllowMetadataRead bool   `json:"allow_metadata_read"`
}
type RefreshRequest struct {
	Generation        string    `json:"generation"`
	Revision          uint64    `json:"revision"`
	Sequence          uint64    `json:"sequence"`
	OperationID       string    `json:"operation_id"`
	RequestedAt       time.Time `json:"requested_at"`
	AllowMetadataRead bool      `json:"allow_metadata_read"`
}
type ControlRequest struct {
	Generation  string `json:"generation"`
	Revision    uint64 `json:"revision"`
	OperationID string `json:"operation_id"`
}
type OperationResult struct {
	OperationID string `json:"operation_id"`
	Generation  string `json:"generation"`
	Revision    uint64 `json:"revision"`
	State       string `json:"state"`
	Outcome     string `json:"outcome,omitempty"`
}
type activeMetadataOperation struct {
	id, digest         string
	monitoringRevision uint64
	cancel             context.CancelFunc
	done               chan struct{}
}

func operationResult(operation localOperation) OperationResult {
	return OperationResult{operation.ID, operation.Generation, operation.Revision, operation.Status, operation.Outcome}
}
func findOperation(record localRecord, id string) (localOperation, bool) {
	if record.state != nil {
		for _, operation := range record.state.Operations {
			if operation.ID == id {
				return operation, true
			}
		}
	}
	return localOperation{}, false
}
func appendOperation(state *localState, operation localOperation, now time.Time) {
	operations := []localOperation{operation}
	for _, prior := range state.Operations {
		if len(operations) < maxOperationOutcomes && prior.ExpiresAt.After(now) {
			operations = append(operations, prior)
		}
	}
	state.Operations = operations
}
func completeOperation(state *localState, id, status, outcome string) error {
	for i := range state.Operations {
		if state.Operations[i].ID == id {
			state.Operations[i].Status = status
			state.Operations[i].Outcome = outcome
			state.Operations[i].Revision = state.Revision
			return nil
		}
	}
	return errLocalConflict
}
func pendingOperation(state *localState, id, action, digest string, now time.Time) localOperation {
	return localOperation{ID: id, Generation: state.Generation, Action: action, RequestDigest: digest, Revision: state.Revision, Status: "pending", CreatedAt: now, ExpiresAt: now.Add(24 * time.Hour)}
}
func (s *Service) replay(ctx context.Context, record localRecord, slot *workspaceOperationSlot, id, action, digest string) (OperationResult, bool, error) {
	operation, ok := findOperation(record, id)
	if !ok {
		return OperationResult{}, false, nil
	}
	if operation.Action != action || operation.RequestDigest != digest {
		return OperationResult{}, true, ErrConfigurationChanged
	}
	if !operation.ExpiresAt.After(s.clock().UTC()) {
		return OperationResult{}, true, ErrReviewExpired
	}
	if operation.Status == "pending" {
		s.mu.Lock()
		active := slot.active
		s.mu.Unlock()
		if active != nil && (operation.Action != "monitoring" || record.state == nil || (active.monitoringRevision != 0 && active.monitoringRevision != record.state.Monitoring.Revision)) {
			return operationResult(operation), true, nil
		}
		// Restart/interruption is never permission to repeat the external read.
		updated, err := s.store.update(ctx, record.authority.Owner, record.authority.Workspace, record.expected(), changeJournal, func(current *localRecord) error {
			if operation.Action == "revoke" || operation.Action == "delete" || operation.Action == "monitoring" {
				return completeOperation(current.state, id, "complete", "applied")
			}
			return completeOperation(current.state, id, "interrupted", "cancelled")
		})
		if err != nil {
			return OperationResult{}, true, err
		}
		operation, _ = findOperation(updated, id)
	}
	return operationResult(operation), true, nil
}
func validOperationID(id string) bool { return validStorageUUID(id) }
func (s *Service) clearReviews(workspace string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for token, review := range s.reviews {
		if review.expected.authority.Workspace == workspace {
			delete(s.reviews, token)
		}
	}
}

// Connect consumes one process-local review, persists an inactive candidate and
// fences old authority BEFORE cancellation. Only a successful final transaction
// may publish both the observation and a new receipt. Monitoring stays off.
func (s *Service) Connect(ctx context.Context, owner, workspace string, request ConnectRequest) (OperationResult, error) {
	release, err := s.enter()
	if err != nil {
		return OperationResult{}, err
	}
	defer release()
	if _, err := s.record(ctx, owner, workspace); err != nil {
		return OperationResult{}, err
	}
	if !validOperationID(request.OperationID) || !request.AllowMetadataRead {
		return OperationResult{}, ErrInvalidRequest
	}
	slot := s.slot(workspace)
	unlock, err := lockSlot(ctx, slot)
	if err != nil {
		return OperationResult{}, err
	}
	defer func() {
		if unlock != nil {
			unlock()
		}
	}()
	record, err := s.record(ctx, owner, workspace)
	if err != nil {
		return OperationResult{}, err
	}
	if err := metadataAdmission(record); err != nil {
		return OperationResult{}, err
	}
	digest := localDigest([]any{owner, workspace, "connect", request})
	if result, replayed, err := s.replay(ctx, record, slot, request.OperationID, "connect", digest); replayed || err != nil {
		return result, err
	}
	if !expectedRequest(record, request.Generation, request.Revision) {
		return OperationResult{}, ErrConfigurationChanged
	}
	s.mu.Lock()
	review, found := s.reviews[request.ReviewToken]
	if found {
		delete(s.reviews, request.ReviewToken)
	}
	s.mu.Unlock()
	if !found || review.monitoring != nil || !review.expires.After(s.clock().UTC()) || review.expected != record.expected() {
		return OperationResult{}, ErrReviewExpired
	}
	now := s.clock().UTC()
	prepared, err := s.store.update(ctx, owner, workspace, record.expected(), changeUser, func(current *localRecord) error {
		current.state.Generation = uuid.NewString()
		configuration := review.configuration
		current.state.Configuration = &configuration
		current.state.Monitoring = localMonitoring{Intent: "manual_only", Revision: current.state.Monitoring.Revision + 1, Policy: current.state.Monitoring.Policy}
		appendOperation(current.state, pendingOperation(current.state, request.OperationID, "connect", digest, now), now)
		return nil
	})
	if err != nil {
		return OperationResult{}, err
	}
	s.clearReviews(workspace)
	s.mu.Lock()
	old := slot.active
	if old != nil {
		old.cancel()
	}
	s.mu.Unlock()
	if old != nil {
		if err := s.waitDrain(ctx, old.done); err != nil {
			return OperationResult{}, err
		}
	}
	return s.runMetadata(ctx, prepared, slot, request.OperationID, digest, true, 0, func() { unlock(); unlock = nil })
}

// Refresh uses receipt authority plus an explicit user-control request. Reserve
// sequence before starting: even an interrupted/evicted operation cannot replay
// the identical old request into another probe. This does NOT update freshness,
// fabricate an attempt/observation, or invalidate the user's settings revision.
func (s *Service) Refresh(ctx context.Context, owner, workspace string, request RefreshRequest) (OperationResult, error) {
	release, err := s.enter()
	if err != nil {
		return OperationResult{}, err
	}
	defer release()
	if _, err := s.record(ctx, owner, workspace); err != nil {
		return OperationResult{}, err
	}
	if !validOperationID(request.OperationID) || !request.AllowMetadataRead {
		return OperationResult{}, ErrInvalidRequest
	}
	slot := s.slot(workspace)
	unlock, err := lockSlot(ctx, slot)
	if err != nil {
		return OperationResult{}, err
	}
	defer func() {
		if unlock != nil {
			unlock()
		}
	}()
	record, err := s.record(ctx, owner, workspace)
	if err != nil {
		return OperationResult{}, err
	}
	if err := metadataAdmission(record); err != nil {
		return OperationResult{}, err
	}
	digest := localDigest([]any{owner, workspace, "refresh", request})
	if result, replayed, err := s.replay(ctx, record, slot, request.OperationID, "refresh", digest); replayed || err != nil {
		return result, err
	}
	if !expectedRequest(record, request.Generation, request.Revision) || record.state == nil || request.Sequence != record.state.Sequence {
		return OperationResult{}, ErrConfigurationChanged
	}
	if !validLocalReceipt(record) || !record.state.Configuration.valid(true) {
		return OperationResult{}, ErrLocalReviewRequired
	}
	now := s.clock().UTC()
	if !localTime(request.RequestedAt) || request.RequestedAt.After(now.Add(5*time.Minute)) || now.Sub(request.RequestedAt) > 24*time.Hour {
		return OperationResult{}, ErrReviewExpired
	}
	s.mu.Lock()
	active := slot.active
	s.mu.Unlock()
	if active != nil {
		return OperationResult{}, ErrBusy
	}
	prepared, err := s.store.update(ctx, owner, workspace, record.expected(), changeAdmission, func(current *localRecord) error {
		appendOperation(current.state, pendingOperation(current.state, request.OperationID, "refresh", digest, now), now)
		return nil
	})
	if err != nil {
		return OperationResult{}, err
	}
	return s.runMetadata(ctx, prepared, slot, request.OperationID, digest, false, 0, func() { unlock(); unlock = nil })
}

func (s *Service) runMetadata(ctx context.Context, record localRecord, slot *workspaceOperationSlot, id, digest string, activate bool, monitoringRevision uint64, unlock func()) (OperationResult, error) {
	started := s.clock().UTC()
	if s.provider == nil {
		unlock()
		return s.publishRead(record, id, started, providerReadResult{}, errProviderUnavailable, activate, monitoringRevision)
	}
	select {
	case s.capacity <- struct{}{}:
	default:
		unlock()
		return s.publishRead(record, id, started, providerReadResult{}, errReadProcess, activate, monitoringRevision)
	}
	readCtx, cancel := context.WithTimeout(s.ctx, completeReadLimit)
	stopRequest := context.AfterFunc(ctx, cancel)
	operation := &activeMetadataOperation{id: id, digest: digest, monitoringRevision: monitoringRevision, cancel: cancel, done: make(chan struct{})}
	s.mu.Lock()
	if s.closing || slot.active != nil {
		s.mu.Unlock()
		stopRequest()
		cancel()
		<-s.capacity
		unlock()
		return s.publishRead(record, id, started, providerReadResult{}, context.Canceled, activate, monitoringRevision)
	}
	slot.active = operation
	s.mu.Unlock()
	defer func() {
		cancel()
		stopRequest()
		s.mu.Lock()
		if slot.active == operation {
			slot.active = nil
		}
		s.mu.Unlock()
		<-s.capacity
		close(operation.done)
	}()
	unlock()
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-readCtx.Done():
				return
			case <-ticker.C:
				if s.authorizeMetadata(readCtx, record, monitoringRevision) != nil {
					cancel()
					return
				}
			}
		}
	}()
	configuration := *record.state.Configuration
	var result providerReadResult
	readErr := s.authorizeMetadata(readCtx, record, monitoringRevision)
	if readErr == nil {
		result, readErr = s.provider.read(readCtx, configuration, func(secretCtx context.Context, use func([]byte) error) error {
			if s.credentials == nil || secretCtx == nil || secretCtx.Err() != nil || readCtx.Err() != nil {
				return errCredentialUnavailable
			}
			ref := vault.BackupSecretReference{VaultID: configuration.Credential.VaultID, RecordID: configuration.Credential.RecordID, WorkspaceID: record.authority.Workspace, Revision: configuration.Credential.Revision}
			if err := s.credentials.UseBackupSecret(secretCtx, ref, func(authCtx context.Context) error { return s.authorizeMetadata(authCtx, record, monitoringRevision) }, use); err != nil {
				return errCredentialUnavailable
			}
			return nil
		})
	}
	if readCtx.Err() != nil || ctx.Err() != nil {
		readErr = context.Canceled
	}
	cancel()
	<-watchDone
	return s.publishRead(record, id, started, result, readErr, activate, monitoringRevision)
}
func (s *Service) waitDrain(ctx context.Context, done <-chan struct{}) error {
	timer := time.NewTimer(2 * childDrainLimit)
	defer timer.Stop()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ErrBusy
	case <-s.ctx.Done():
		return ErrUnavailable
	case <-timer.C:
		return ErrBusy
	}
}
func outcomeForReadError(err error) string {
	switch {
	case err == nil:
		return "read_ok"
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case errors.Is(err, errDestinationAbsent):
		return "destination_absent"
	case errors.Is(err, errIdentityMismatch), errors.Is(err, errStorageConflict):
		return "identity_mismatch"
	case errors.Is(err, errCredentialUnavailable):
		return "credential_unavailable"
	case errors.Is(err, errProviderUnavailable), errors.Is(err, errTrustedExecutable), errors.Is(err, errProviderImage), errors.Is(err, errSandboxProfile):
		return "provider_unavailable"
	case errors.Is(err, errMetadataLimit), errors.Is(err, errReadProcessLimit):
		return "limits_exceeded"
	default:
		return "read_unavailable"
	}
}
func (s *Service) publishRead(scope localRecord, id string, started time.Time, result providerReadResult, readErr error, activate bool, monitoringRevision uint64) (OperationResult, error) {
	// Final bookkeeping survives a disconnected HTTP client, but remains inside
	// this operation's WorkGate/lifetime ownership until publication or rollback.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(s.ctx), 2*time.Second)
	defer cancel()
	finished := s.clock().UTC()
	clockSuspect := finished.Before(started)
	if clockSuspect {
		readErr = errReadProcess
	}
	if readErr == nil && (!result.configuration.valid(true) || configurationIntentDigest(&result.configuration) != configurationIntentDigest(scope.state.Configuration) || !compatibleValidationEvidence(scope.state.Configuration, &result.configuration) || !localTime(result.observedAt) || result.observedAt.Before(started) || result.observedAt.After(finished)) {
		readErr = errReadProcess
	}
	outcome := outcomeForReadError(readErr)
	if clockSuspect {
		outcome = "clock_suspect"
	}
	for range 2 {
		current, err := s.store.load(ctx, scope.authority.Owner, scope.authority.Workspace)
		if err != nil {
			return OperationResult{}, err
		}
		if current.authority != scope.authority || current.state == nil || current.state.Generation != scope.state.Generation || configurationIntentDigest(current.state.Configuration) != configurationIntentDigest(scope.state.Configuration) {
			return OperationResult{}, ErrConfigurationChanged
		}
		if monitoringRevision != 0 && (!monitoringConsentValid(current) || current.state.Monitoring.Revision != monitoringRevision) {
			return OperationResult{}, ErrConfigurationChanged
		}
		kind := changeObservation
		if activate && readErr == nil {
			kind = changeActivation
		}
		updated, err := s.store.update(ctx, current.authority.Owner, current.authority.Workspace, current.expected(), kind, func(record *localRecord) error {
			state := record.state
			if monitoringRevision != 0 && (!monitoringConsentValid(*record) || state.Monitoring.Revision != monitoringRevision) {
				return ErrConfigurationChanged
			}
			attempt := localAttempt{Generation: state.Generation, Sequence: state.Sequence, StartedAt: started, FinishedAt: finished, Outcome: outcome}
			retained := state.Attempts
			if len(retained) >= maxAttemptHistory {
				retained = retained[:maxAttemptHistory-1]
			}
			state.Attempts = append([]localAttempt{attempt}, retained...)
			if readErr == nil {
				configuration := result.configuration
				state.Configuration = &configuration
				state.Observation = &localObservation{Generation: state.Generation, Sequence: state.Sequence, ObservedAt: finished, Snapshots: result.snapshots, Capacity: result.capacity}
				if activate {
					record.activate(finished)
				}
			}
			return completeOperation(state, id, "complete", outcome)
		})
		if errors.Is(err, errLocalConflict) {
			continue
		}
		if err != nil {
			return OperationResult{}, err
		}
		operation, _ := findOperation(updated, id)
		return operationResult(operation), nil
	}
	return OperationResult{}, ErrConfigurationChanged
}

// Revoke fences before cancellation, retains bounded historical facts, and
// never removes or modifies any external backup. Clear additionally discards
// the saved selection/reference, not the external repository or history.
func (s *Service) Revoke(ctx context.Context, owner, workspace string, request ControlRequest, clear bool) (OperationResult, error) {
	release, err := s.enter()
	if err != nil {
		return OperationResult{}, err
	}
	defer release()
	if _, err := s.record(ctx, owner, workspace); err != nil {
		return OperationResult{}, err
	}
	if !validOperationID(request.OperationID) {
		return OperationResult{}, ErrInvalidRequest
	}
	slot := s.slot(workspace)
	unlock, err := lockSlot(ctx, slot)
	if err != nil {
		return OperationResult{}, err
	}
	defer unlock()
	record, err := s.record(ctx, owner, workspace)
	if err != nil {
		return OperationResult{}, err
	}
	action := "revoke"
	if clear {
		action = "delete"
	}
	if err := metadataAdmission(record); err != nil {
		return OperationResult{}, err
	}
	digest := localDigest([]any{owner, workspace, action, request})
	if result, replayed, err := s.replay(ctx, record, slot, request.OperationID, action, digest); replayed || err != nil {
		return result, err
	}
	if !expectedRequest(record, request.Generation, request.Revision) {
		return OperationResult{}, ErrConfigurationChanged
	}
	now := s.clock().UTC()
	updated, err := s.store.update(ctx, owner, workspace, record.expected(), changeUser, func(current *localRecord) error {
		current.state.Generation = uuid.NewString()
		if clear {
			current.state.Configuration = nil
		}
		current.state.Monitoring = localMonitoring{Intent: "manual_only", Revision: current.state.Monitoring.Revision + 1, Policy: current.state.Monitoring.Policy}
		operation := pendingOperation(current.state, request.OperationID, action, digest, now)
		appendOperation(current.state, operation, now)
		return nil
	})
	if err != nil {
		return OperationResult{}, err
	}
	s.clearReviews(workspace)
	s.mu.Lock()
	active := slot.active
	if active != nil {
		active.cancel()
	}
	s.mu.Unlock()
	if active != nil {
		if err := s.waitDrain(ctx, active.done); err != nil {
			return OperationResult{}, err
		}
	}
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	updated, err = s.store.update(finishCtx, owner, workspace, updated.expected(), changeJournal, func(current *localRecord) error {
		return completeOperation(current.state, request.OperationID, "complete", "applied")
	})
	if err != nil {
		return OperationResult{}, err
	}
	operation, _ := findOperation(updated, request.OperationID)
	return operationResult(operation), nil
}
