package backuprecovery

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
)

const monitorPollInterval = 30 * time.Second
const monitorRetryDelay = 5 * time.Minute
const monitorPageLimit = 32

// StartMonitoring is a composition-only lifecycle hook, never an HTTP/model
// operation. Construction and stored readers cannot start this loop. The server
// binds it only after the separate monitor lifecycle/reset gate is complete.
func (s *Service) StartMonitoring() error {
	release, err := s.enter()
	if err != nil {
		return err
	}
	defer release()
	if s.provider == nil {
		return ErrUnavailable
	}
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		return ErrUnavailable
	}
	if s.monitoringBound {
		s.mu.Unlock()
		return nil
	}
	s.monitoringBound = true
	s.operations.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.operations.Done()
		ticker := time.NewTicker(monitorPollInterval)
		defer ticker.Stop()
		for {
			s.monitorTick(s.ctx)
			select {
			case <-s.ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return nil
}

// A bounded round-robin page avoids both an unbounded decrypted-state scan and
// permanent starvation of workspaces after the first page. Jobs never queue.
func (s *Service) monitorTick(ctx context.Context) {
	release, err := s.enter()
	if err != nil {
		return
	}
	defer release()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	s.mu.Lock()
	cursor := s.monitorCursor
	s.mu.Unlock()
	ids := []string{}
	err = s.store.transaction(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT workspace_id FROM backup_recovery_local WHERE owner_user_id='local' AND workspace_id>? ORDER BY workspace_id LIMIT ?`, cursor, monitorPageLimit)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return err
			}
			ids = append(ids, id)
		}
		return rows.Err()
	})
	if err != nil {
		return
	}
	saveCursor := func(value string) { s.mu.Lock(); s.monitorCursor = value; s.mu.Unlock() }
	for _, id := range ids {
		select {
		case s.monitorJobs <- struct{}{}:
		default:
			// Resume after the last admitted ID, not after the entire queried
			// page: otherwise a full worker pool could starve its later rows.
			saveCursor(cursor)
			return
		}
		owned, err := s.enter()
		if err != nil {
			<-s.monitorJobs
			return
		}
		cursor = id
		go func(workspace string) {
			defer owned()
			defer func() { <-s.monitorJobs }()
			_, _ = s.monitorWorkspaceEntered(s.ctx, workspace)
		}(id)
	}
	if len(ids) < monitorPageLimit {
		cursor = ""
	}
	saveCursor(cursor)
}

func (s *Service) authorizeMetadata(ctx context.Context, scope localRecord, monitoringRevision uint64) error {
	if monitoringRevision == 0 {
		return s.authorize(ctx, scope.expected(), false)
	}
	current, err := s.record(ctx, scope.authority.Owner, scope.authority.Workspace)
	if err != nil {
		return err
	}
	if current.authority != scope.authority || current.state == nil || current.state.Generation != scope.state.Generation || !periodicReadAdmissible(current) || current.state.Monitoring.Revision != monitoringRevision {
		return ErrLocalReviewRequired
	}
	return nil
}
func currentAttemptNeedsReview(state *localState) bool {
	return state != nil && len(state.Attempts) > 0 && state.Attempts[0].Generation == state.Generation && monitorNeedsReview(state.Attempts[0].Outcome)
}
func periodicReadAdmissible(record localRecord) bool {
	return monitoringConsentValid(record) && !currentAttemptNeedsReview(record.state)
}

func monitorNeedsReview(outcome string) bool {
	switch outcome {
	case "identity_mismatch", "credential_unavailable", "provider_unavailable", "limits_exceeded", "clock_suspect":
		return true
	}
	return false
}

// monitorWorkspace is the fake-clock/direct-check seam, not a public action.
// Both it and the loop retain finite ownership before any read or publication.
func (s *Service) monitorWorkspace(ctx context.Context, workspace string) (OperationResult, error) {
	release, err := s.enter()
	if err != nil {
		return OperationResult{}, err
	}
	defer release()
	return s.monitorWorkspaceEntered(ctx, workspace)
}
func (s *Service) monitorWorkspaceEntered(ctx context.Context, workspace string) (OperationResult, error) {
	if s.provider == nil {
		return OperationResult{}, ErrUnavailable
	}
	record, err := s.record(ctx, "local", workspace)
	if err != nil {
		return OperationResult{}, err
	}
	if !periodicReadAdmissible(record) {
		return OperationResult{}, ErrLocalReviewRequired
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
	record, err = s.record(ctx, "local", workspace)
	if err != nil {
		return OperationResult{}, err
	}
	if !periodicReadAdmissible(record) {
		return OperationResult{}, ErrLocalReviewRequired
	}
	s.mu.Lock()
	active := slot.active != nil
	s.mu.Unlock()
	if active || len(s.capacity) == cap(s.capacity) {
		return OperationResult{}, ErrBusy
	}
	monitoring := record.state.Monitoring
	now := s.clock().UTC()
	if !localTime(now) || monitoring.LastSlotAt != nil && now.Before(*monitoring.LastSlotAt) || record.state.Observation != nil && record.state.Observation.ObservedAt.After(now.Add(5*time.Minute)) {
		return OperationResult{}, nil
	}
	if monitoring.NextDueAt == nil {
		return OperationResult{}, ErrLocalReviewRequired
	}
	due := *monitoring.NextDueAt
	retry := false
	if due.After(now) {
		if monitoring.RetryAt == nil || monitoring.RetryAt.After(now) || monitoring.RetryUsed {
			return OperationResult{}, nil
		}
		due = *monitoring.RetryAt
		retry = true
	}
	key := localDigest([]any{"periodic-slot-v1", workspace, record.state.Generation, monitoring.Revision, due.UTC().Format(time.RFC3339Nano), retry})
	id := uuid.NewSHA1(uuid.NameSpaceOID, []byte(key)).String()
	if _, found := findOperation(record, id); found {
		return OperationResult{}, nil
	}
	prepared, err := s.store.update(ctx, "local", workspace, record.expected(), changeAdmission, func(current *localRecord) error {
		if !periodicReadAdmissible(*current) || current.state.Monitoring.Revision != monitoring.Revision {
			return ErrConfigurationChanged
		}
		m := &current.state.Monitoring
		if retry {
			m.RetryUsed = true
			m.RetryAt = nil
		} else {
			next := now.Add(time.Duration(m.Policy.CadenceSeconds) * time.Second)
			m.NextDueAt = &next
			m.LastSlotAt = &now
			m.RetryAt = nil
			m.RetryUsed = false
		}
		appendOperation(current.state, pendingOperation(current.state, id, "monitor", key, now), now)
		return nil
	})
	if err != nil {
		return OperationResult{}, err
	}
	result, err := s.runMetadata(ctx, prepared, slot, id, key, false, monitoring.Revision, func() { unlock(); unlock = nil })
	if err != nil {
		return result, err
	}
	if err := s.finishMonitorSlot(prepared, result, retry); err != nil {
		return result, err
	}
	return result, nil
}

// Due time and a deterministic slot ID were persisted before the external read.
// Restart never replays pending work. Only one transient retry is proposed; an
// authority/identity/credential/containment error fences periodic consent.
func (s *Service) finishMonitorSlot(scope localRecord, result OperationResult, retry bool) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(s.ctx), 2*time.Second)
	defer cancel()
	current, err := s.record(ctx, "local", scope.authority.Workspace)
	if err != nil {
		return err
	}
	if current.authority != scope.authority || !monitoringConsentValid(current) || current.state.Generation != scope.state.Generation || current.state.Monitoring.Revision != scope.state.Monitoring.Revision || current.state.Sequence != scope.state.Sequence+1 {
		return nil
	}
	kind := changeJournal
	if monitorNeedsReview(result.Outcome) {
		kind = changeUser
	}
	_, err = s.store.update(ctx, "local", scope.authority.Workspace, current.expected(), kind, func(record *localRecord) error {
		if !monitoringConsentValid(*record) || record.state.Monitoring.Revision != scope.state.Monitoring.Revision || record.state.Sequence != scope.state.Sequence+1 {
			return ErrConfigurationChanged
		}
		m := &record.state.Monitoring
		if monitorNeedsReview(result.Outcome) {
			m.Intent = "paused"
			m.Revision++
			m.NextDueAt = nil
			m.RetryAt = nil
			m.RetryUsed = false
			record.receipt.MonitoringDigest = ""
		} else if result.Outcome == "read_unavailable" && !retry && !m.RetryUsed {
			at := s.clock().UTC().Add(monitorRetryDelay)
			if m.NextDueAt != nil && at.Before(*m.NextDueAt) {
				m.RetryAt = &at
			}
		}
		return nil
	})
	return err
}
