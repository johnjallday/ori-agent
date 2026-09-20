package assistantsetup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

func monitoringReviewRevision(ownerUserID string, run *Run, facts MonitoringFacts) string {
	if run == nil {
		return ""
	}
	canonical := struct {
		Owner, Run, Workspace, Root, Settings, Privacy string
		RunRevision                                    int64
		Events                                         []string
		Debounce, Settling                             int
		Time, Timezone                                 string
		ExcludesFiled                                  bool
		WasPaused, WasApproved, WasActive              bool
	}{
		ownerUserID, run.ID, run.TargetWorkspaceID, facts.RootGenerationID,
		facts.SettingsRevision, facts.PrivacyMode, run.Revision,
		append([]string(nil), facts.WatchEvents...), facts.DebounceSeconds,
		facts.SettlingSeconds, facts.DailyScanLocalTime, facts.Timezone,
		facts.ExcludesFiled, facts.WasPaused, facts.WasApproved, facts.WasMonitoringActive,
	}
	data, err := json.Marshal(canonical)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func (s *Service) monitoringReview(ctx context.Context, ownerUserID string, run *Run) (*MonitoringReview, error) {
	if s == nil || s.progressor == nil || run == nil {
		return nil, ErrUnavailable
	}
	facts, err := s.progressor.ReviewMonitoring(ctx, ownerUserID, run.TargetWorkspaceID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(facts.RootGenerationID) == "" || strings.TrimSpace(facts.SettingsRevision) == "" ||
		strings.TrimSpace(facts.AuthorityRevision) == "" || strings.TrimSpace(facts.PrivacyMode) == "" || strings.TrimSpace(facts.DailyScanLocalTime) == "" ||
		strings.TrimSpace(facts.Timezone) == "" || facts.DebounceSeconds < 1 || facts.SettlingSeconds < 1 {
		return nil, ErrUnavailable
	}
	review := &MonitoringReview{
		RootGenerationID: facts.RootGenerationID, SettingsRevision: facts.SettingsRevision,
		AuthorityRevision: facts.AuthorityRevision,
		RetryRunID:        facts.RetryRunID, RetryOperationID: facts.RetryOperationID,
		RetryReviewRevision: facts.RetryReviewRevision, RetryAuthorityRevision: facts.RetryAuthorityRevision,
		RetryState:  facts.RetryState,
		PrivacyMode: facts.PrivacyMode, WatchEvents: append([]string(nil), facts.WatchEvents...),
		DebounceSeconds: facts.DebounceSeconds, SettlingSeconds: facts.SettlingSeconds,
		DailyScanLocalTime: facts.DailyScanLocalTime, Timezone: facts.Timezone,
		ExcludesFiled: facts.ExcludesFiled, NothingMoves: true,
		WasPaused: facts.WasPaused, WasApproved: facts.WasApproved, WasMonitoringActive: facts.WasMonitoringActive,
	}
	review.Revision = monitoringReviewRevision(ownerUserID, run, facts)
	if review.Revision == "" {
		return nil, ErrUnavailable
	}
	return review, nil
}

// PrepareReview commits the separately reviewed monitoring consent and one
// operation-specific metadata scan while holding reset admission throughout.
// RetryDue repeats only a previously accepted monitoring/scan operation whose
// exact reviewed authority is still current. Scope, consent, deferral, or
// lifecycle drift clears the timer and waits for an explicit review instead.
func (s *Service) RetryDue(ctx context.Context, now time.Time, limit int) (int, error) {
	if s == nil || s.store == nil || s.progressor == nil {
		return 0, ErrUnavailable
	}
	runs, err := s.store.ListDueRetries(ctx, now, limit)
	if err != nil {
		return 0, err
	}
	attempted := 0
	for i := range runs {
		run := &runs[i]
		if _, relationshipErr := s.relationship(ctx, run.OwnerUserID); relationshipErr != nil {
			_ = s.store.ClearRetry(ctx, run.OwnerUserID, run.ID, run.Revision, "retry_requires_review")
			continue
		}
		if s.resolver == nil {
			_ = s.store.ClearRetry(ctx, run.OwnerUserID, run.ID, run.Revision, "retry_requires_review")
			continue
		}
		target, targetErr := s.resolver.ResolveOne(ctx, run.OwnerUserID, run.TargetWorkspaceID)
		if targetErr != nil || target == nil || !target.Supported {
			_ = s.store.ClearRetry(ctx, run.OwnerUserID, run.ID, run.Revision, "retry_requires_review")
			continue
		}
		operations, listErr := s.store.ListOperations(ctx, run.OwnerUserID, run.ID)
		if listErr != nil {
			return attempted, listErr
		}
		var monitoring, scan *Operation
		for operationIndex := range operations {
			operation := &operations[operationIndex]
			switch operation.Kind {
			case OperationMonitoring:
				monitoring = operation
			case OperationInitialScan:
				scan = operation
			}
		}
		if monitoring == nil || scan == nil || monitoring.AttemptCount >= 3 || scan.AttemptCount >= 3 {
			_ = s.store.ClearRetry(ctx, run.OwnerUserID, run.ID, run.Revision, "retry_exhausted")
			continue
		}
		review, reviewErr := s.monitoringReview(ctx, run.OwnerUserID, run)
		if reviewErr != nil || monitoring.ExpectedRunRevision != run.Revision || scan.ExpectedRunRevision != run.Revision {
			_ = s.store.ClearRetry(ctx, run.OwnerUserID, run.ID, run.Revision, "retry_requires_review")
			continue
		}
		directAuthority := monitoring.ReviewDigest == review.Revision && scan.ReviewDigest == review.Revision
		ownedRollback := scan.ReviewDigest == monitoring.ReviewDigest && review.RetryRunID == run.ID && review.RetryOperationID == monitoring.ID &&
			review.RetryReviewRevision == monitoring.ReviewDigest && review.RetryAuthorityRevision == review.AuthorityRevision &&
			review.RetryState == "failed_paused" && review.WasApproved && review.WasPaused && !review.WasMonitoringActive
		authorized := directAuthority || ownedRollback
		if monitoring.SafeOutcomeCode == "monitoring_retry_bound" {
			authorized = ownedRollback
		}
		if !authorized {
			_ = s.store.ClearRetry(ctx, run.OwnerUserID, run.ID, run.Revision, "retry_requires_review")
			continue
		}
		attempted++
		_, _ = s.PrepareReview(ctx, run.OwnerUserID, run.ID, run.Revision, review.Revision)
	}
	return attempted, nil
}

func (s *Service) PrepareReview(ctx context.Context, ownerUserID, runID string, ifVersion int64, reviewRevision string) (*Projection, error) {
	if s == nil || s.store == nil || s.progressor == nil {
		return nil, ErrUnavailable
	}
	release, err := s.gate.Enter()
	if err != nil {
		return nil, err
	}
	defer release()
	if _, err := s.relationship(ctx, ownerUserID); err != nil {
		return nil, err
	}
	run, err := s.store.GetRun(ctx, ownerUserID, runID)
	if err != nil {
		return nil, err
	}
	if run.Status == RunFirstResult && run.CurrentStep == StepResult {
		operations, listErr := s.store.ListOperations(ctx, ownerUserID, run.ID)
		if listErr != nil {
			return nil, ErrUnavailable
		}
		matched := 0
		for _, operation := range operations {
			if operation.Kind != OperationMonitoring && operation.Kind != OperationInitialScan {
				continue
			}
			if operation.Status != OperationSucceeded || operation.ExpectedRunRevision != ifVersion ||
				operation.ReviewDigest != strings.TrimSpace(reviewRevision) {
				return nil, ErrConflict
			}
			matched++
		}
		if matched != 2 {
			return nil, ErrConflict
		}
		base, baseErr := s.baseProjection(ctx, ownerUserID)
		if baseErr != nil {
			return nil, baseErr
		}
		return s.projectRun(ctx, base, run)
	}
	if run.Status != RunActive || run.CurrentStep != StepMonitoring {
		return nil, ErrInvalidAction
	}
	if run.Revision != ifVersion {
		return nil, ErrStaleRun
	}
	review, err := s.monitoringReview(ctx, ownerUserID, run)
	if err != nil {
		return nil, err
	}
	if review.Revision != strings.TrimSpace(reviewRevision) {
		base, baseErr := s.baseProjection(ctx, ownerUserID)
		if baseErr != nil {
			return nil, ErrStaleRun
		}
		fresh, _ := s.projectRun(ctx, base, run)
		return fresh, ErrStaleRun
	}
	run, monitoring, scan, err := s.store.ClaimPrepareReview(ctx, ownerUserID, runID, ifVersion, review.Revision)
	if err != nil {
		return nil, err
	}
	request := PrepareReviewRequest{
		OwnerUserID: ownerUserID, RunID: run.ID, WorkspaceID: run.TargetWorkspaceID,
		RunRevision: run.Revision, MonitoringOperation: monitoring.ID,
		ScanOperation: scan.ID, Review: *review,
	}
	result, prepareErr := s.progressor.PrepareFirstReview(ctx, request)
	if prepareErr != nil {
		safeCode := "prepare_review_failed"
		switch {
		case errors.Is(prepareErr, ErrPrivacyReview):
			safeCode = "privacy_review_required"
		case errors.Is(prepareErr, ErrFolderChanged):
			safeCode = "folder_changed"
		case errors.Is(prepareErr, ErrNoLongerAvailable):
			safeCode = "no_longer_available"
		}
		updated, _ := s.store.RecordPrepareReviewFailure(ctx, request, result, safeCode)
		if updated != nil {
			base, baseErr := s.baseProjection(ctx, ownerUserID)
			if baseErr == nil {
				projection, _ := s.projectRun(ctx, base, updated)
				return projection, prepareErr
			}
		}
		return nil, prepareErr
	}
	updated, err := s.store.CompletePrepareReview(ctx, request, result)
	if err != nil {
		return nil, err
	}
	base, err := s.baseProjection(ctx, ownerUserID)
	if err != nil {
		return nil, err
	}
	return s.projectRun(ctx, base, updated)
}
