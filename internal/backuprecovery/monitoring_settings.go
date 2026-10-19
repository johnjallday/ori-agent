package backuprecovery

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// MonitoringPolicy contains bounded metadata-check proposals, never a backup
// schedule. No policy or stored intent alone permits an external read.
type MonitoringPolicy monitoringPolicy

type MonitoringSettings struct {
	Intent       string           `json:"intent"`
	Revision     uint64           `json:"monitoring_revision"`
	Policy       MonitoringPolicy `json:"policy"`
	ConsentValid bool             `json:"consent_valid"`
}
type MonitoringReviewRequest struct {
	Generation         string           `json:"generation"`
	Revision           uint64           `json:"revision"`
	MonitoringRevision uint64           `json:"monitoring_revision"`
	Policy             MonitoringPolicy `json:"policy"`
}
type MonitoringReview struct {
	Generation         string           `json:"generation"`
	Revision           uint64           `json:"revision"`
	MonitoringRevision uint64           `json:"monitoring_revision"`
	Policy             MonitoringPolicy `json:"policy"`
	Token              string           `json:"review_token"`
	ExpiresAt          time.Time        `json:"expires_at"`
}
type MonitoringChangeRequest struct {
	Generation            string `json:"generation"`
	Revision              uint64 `json:"revision"`
	MonitoringRevision    uint64 `json:"monitoring_revision"`
	OperationID           string `json:"operation_id"`
	Intent                string `json:"intent"`
	ReviewToken           string `json:"review_token"`
	AllowPeriodicMetadata bool   `json:"allow_periodic_metadata"`
}

func monitoringConsentValid(record localRecord) bool {
	return record.state != nil && record.state.Monitoring.Intent == "active" && validLocalReceipt(record) && record.receipt.MonitoringDigest != "" && record.automatic && !pendingRemoval(record)
}
func storedMonitoring(record localRecord) MonitoringSettings {
	result := MonitoringSettings{Intent: "manual_only", Policy: MonitoringPolicy(proposedMonitoringPolicy())}
	if record.state != nil {
		result.Intent = record.state.Monitoring.Intent
		result.Revision = record.state.Monitoring.Revision
		result.Policy = MonitoringPolicy(record.state.Monitoring.Policy)
		result.ConsentValid = monitoringConsentValid(record)
	}
	return result
}
func validMonitoringPolicy(policy MonitoringPolicy) bool {
	return (localMonitoring{Intent: "manual_only", Policy: monitoringPolicy(policy)}).valid()
}

// ReviewMonitoring reads only stored authority/evidence. It creates neither a
// key nor durable consent and never checks a destination or decrypts a password.
func (s *Service) ReviewMonitoring(ctx context.Context, owner, workspace string, request MonitoringReviewRequest) (MonitoringReview, error) {
	release, err := s.enter()
	if err != nil {
		return MonitoringReview{}, err
	}
	defer release()
	record, err := s.record(ctx, owner, workspace)
	if err != nil {
		return MonitoringReview{}, err
	}
	if err := metadataAdmission(record); err != nil {
		return MonitoringReview{}, err
	}
	if !validMonitoringPolicy(request.Policy) {
		return MonitoringReview{}, ErrInvalidRequest
	}
	if !expectedRequest(record, request.Generation, request.Revision) {
		return MonitoringReview{}, ErrConfigurationChanged
	}
	if record.state == nil {
		return MonitoringReview{}, ErrLocalReviewRequired
	}
	if request.MonitoringRevision != record.state.Monitoring.Revision {
		return MonitoringReview{}, ErrConfigurationChanged
	}
	if !validLocalReceipt(record) || !record.automatic {
		return MonitoringReview{}, ErrLocalReviewRequired
	}
	if currentAttemptNeedsReview(record.state) {
		return MonitoringReview{}, ErrLocalReviewRequired
	}
	if s.providerFor(recordProvider(record)) == nil {
		return MonitoringReview{}, ErrUnavailable
	}
	now := s.clock().UTC()
	policy := monitoringPolicy(request.Policy)
	token, expires, err := s.addReview(record.authority.Workspace, pendingReview{expected: record.expected(), monitoring: &policy, monitoringRevision: request.MonitoringRevision, expires: now.Add(5 * time.Minute)}, now)
	if err != nil {
		return MonitoringReview{}, err
	}
	return MonitoringReview{record.state.Generation, record.state.Revision, request.MonitoringRevision, request.Policy, token, expires}, nil
}

// SetMonitoring changes only reviewed consent/policy. It does not itself run a
// check. Until the separately lifecycle-bound scheduler is available, status
// honestly remains unavailable even for a valid saved periodic consent.
func (s *Service) SetMonitoring(ctx context.Context, owner, workspace string, request MonitoringChangeRequest) (OperationResult, error) {
	release, err := s.enter()
	if err != nil {
		return OperationResult{}, err
	}
	defer release()
	if !validOperationID(request.OperationID) {
		return OperationResult{}, ErrInvalidRequest
	}
	switch request.Intent {
	case "active":
		if !request.AllowPeriodicMetadata || request.ReviewToken == "" {
			return OperationResult{}, ErrInvalidRequest
		}
	case "paused", "manual_only":
		if request.AllowPeriodicMetadata || request.ReviewToken != "" {
			return OperationResult{}, ErrInvalidRequest
		}
	default:
		return OperationResult{}, ErrInvalidRequest
	}
	if _, err := s.record(ctx, owner, workspace); err != nil {
		return OperationResult{}, err
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
	if err := metadataAdmission(record); err != nil {
		return OperationResult{}, err
	}
	digest := localDigest([]any{owner, workspace, "monitoring", request})
	if result, replayed, err := s.replay(ctx, record, slot, request.OperationID, "monitoring", digest); replayed || err != nil {
		return result, err
	}
	if !expectedRequest(record, request.Generation, request.Revision) || record.state == nil || record.state.Monitoring.Revision != request.MonitoringRevision {
		return OperationResult{}, ErrConfigurationChanged
	}
	policy := record.state.Monitoring.Policy
	if request.Intent == "active" {
		if s.providerFor(recordProvider(record)) == nil {
			return OperationResult{}, ErrUnavailable
		}
		if !validLocalReceipt(record) || !record.automatic || currentAttemptNeedsReview(record.state) {
			return OperationResult{}, ErrLocalReviewRequired
		}
		s.mu.Lock()
		review, found := s.reviews[request.ReviewToken]
		if found {
			delete(s.reviews, request.ReviewToken)
		}
		s.mu.Unlock()
		if !found || review.monitoring == nil || review.expected != record.expected() || review.monitoringRevision != request.MonitoringRevision || !review.expires.After(s.clock().UTC()) {
			return OperationResult{}, ErrReviewExpired
		}
		policy = *review.monitoring
	}
	now := s.clock().UTC()
	updated, err := s.store.update(ctx, owner, workspace, record.expected(), changeUser, func(current *localRecord) error {
		if request.Intent == "active" && (!validLocalReceipt(*current) || !current.automatic || currentAttemptNeedsReview(current.state) || current.state.Monitoring.Revision != request.MonitoringRevision) {
			return ErrLocalReviewRequired
		}
		current.state.Monitoring = localMonitoring{Intent: request.Intent, Revision: current.state.Monitoring.Revision + 1, Policy: policy}
		if request.Intent == "active" {
			due := now
			current.state.Monitoring.NextDueAt = &due
			current.receipt.MonitoringDigest = monitoringDigest(current.state.Monitoring)
		} else if current.receipt != nil {
			current.receipt.MonitoringDigest = ""
		}
		operation := pendingOperation(current.state, request.OperationID, "monitoring", digest, now)
		appendOperation(current.state, operation, now)
		return nil
	})
	if err != nil {
		return OperationResult{}, err
	}
	s.clearReviews(workspace)
	// Fence was persisted first. Manual one-read permission is independent;
	// only the old periodic read must cancel/drain before this control completes.
	s.mu.Lock()
	active := slot.active
	if active != nil && active.monitoringRevision != 0 && active.monitoringRevision != updated.state.Monitoring.Revision {
		active.cancel()
	} else {
		active = nil
	}
	s.mu.Unlock()
	if active != nil {
		if err := s.waitDrain(ctx, active.done); err != nil {
			return OperationResult{}, err
		}
	}
	current, err := s.record(ctx, owner, workspace)
	if err != nil {
		return OperationResult{}, err
	}
	if current.authority != updated.authority || current.state == nil || current.state.Generation != updated.state.Generation {
		return OperationResult{}, ErrConfigurationChanged
	}
	finished, err := s.store.update(ctx, owner, workspace, current.expected(), changeJournal, func(record *localRecord) error {
		return completeOperation(record.state, request.OperationID, "complete", "applied")
	})
	if err != nil {
		return OperationResult{}, err
	}
	operation, _ := findOperation(finished, request.OperationID)
	return operationResult(operation), nil
}

// addReview shares the process-local quota and expiry across connection and
// monitoring reviews. Configuration generation/revision and lifecycle authority
// are checked again on consumption, never promoted from a browser token alone.
func (s *Service) addReview(workspace string, review pendingReview, now time.Time) (string, time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		return "", time.Time{}, ErrUnavailable
	}
	count := 0
	for token, prior := range s.reviews {
		if !prior.expires.After(now) {
			delete(s.reviews, token)
		} else if prior.expected.authority.Workspace == workspace {
			count++
		}
	}
	if count >= 4 || len(s.reviews) >= 128 {
		return "", time.Time{}, ErrBusy
	}
	token := uuid.NewString()
	s.reviews[token] = review
	return token, review.expires, nil
}
