//go:build darwin || linux

package backuprecovery

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func monitoringReviewFixture(t *testing.T, s *Service) MonitoringReview {
	t.Helper()
	settings, err := s.GetSettings(t.Context(), "local", localFixtureWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	review, err := s.ReviewMonitoring(t.Context(), "local", localFixtureWorkspace, MonitoringReviewRequest{settings.Generation, settings.Revision, settings.Monitoring.Revision, settings.Monitoring.Policy})
	if err != nil {
		t.Fatal(err)
	}
	return review
}
func enableMonitoringFixture(t *testing.T, s *Service) (MonitoringChangeRequest, OperationResult) {
	t.Helper()
	review := monitoringReviewFixture(t, s)
	request := MonitoringChangeRequest{review.Generation, review.Revision, review.MonitoringRevision, uuid.NewString(), "active", review.Token, true}
	result, err := s.SetMonitoring(t.Context(), "local", localFixtureWorkspace, request)
	if err != nil {
		t.Fatal(err)
	}
	return request, result
}
func TestMonitoringReviewAndConsentNeverProbeOrRepeatConnectionRead(t *testing.T) {
	s, provider, credentials := newBackupServiceFixture(t)
	connectBackupServiceFixture(t, s)
	before, err := s.store.load(t.Context(), "local", localFixtureWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	review := monitoringReviewFixture(t, s)
	after, err := s.store.load(t.Context(), "local", localFixtureWorkspace)
	if err != nil || localDigest(before.state) != localDigest(after.state) || localDigest(before.receipt) != localDigest(after.receipt) {
		t.Fatal("review changed persistent consent/evidence", err)
	}
	request := MonitoringChangeRequest{review.Generation, review.Revision, review.MonitoringRevision, uuid.NewString(), "active", review.Token, false}
	if _, err := s.SetMonitoring(t.Context(), "local", localFixtureWorkspace, request); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal("missing explicit periodic consent accepted", err)
	}
	request.AllowPeriodicMetadata = true
	first, err := s.SetMonitoring(t.Context(), "local", localFixtureWorkspace, request)
	if err != nil {
		t.Fatal(err)
	}
	after, err = s.store.load(t.Context(), "local", localFixtureWorkspace)
	if err != nil || !monitoringConsentValid(after) || after.state.Generation != before.state.Generation || after.state.Revision != before.state.Revision+1 || after.state.Monitoring.Revision != before.state.Monitoring.Revision+1 || after.receipt.GrantID != before.receipt.GrantID || localDigest(after.state.Observation) != localDigest(before.state.Observation) || localDigest(after.state.Attempts) != localDigest(before.state.Attempts) {
		t.Fatal("consent widened connection authority or changed observation", err)
	}
	if after.state.Monitoring.NextDueAt == nil {
		t.Fatal("consent did not retain one bounded due slot")
	}
	retry, err := s.SetMonitoring(t.Context(), "local", localFixtureWorkspace, request)
	if err != nil || retry != first {
		t.Fatal("identical retry changed operation", retry, err)
	}
	altered := request
	altered.Intent = "paused"
	altered.AllowPeriodicMetadata = false
	altered.ReviewToken = ""
	if _, err := s.SetMonitoring(t.Context(), "local", localFixtureWorkspace, altered); !errors.Is(err, ErrConfigurationChanged) {
		t.Fatal("same UUID changed consent intent", err)
	}
	status, err := s.GetStatus(t.Context(), "local", localFixtureWorkspace)
	if err != nil || status.Monitoring != "unavailable" {
		t.Fatal("unbound scheduler was presented as working", status, err)
	}
	if provider.calls.Load() != 1 || credentials.uses.Load() != 1 {
		t.Fatal("review/consent/retry caused an external read")
	}
}
func TestMonitoringReviewTokensCannotBeConnectionAuthorityAndPauseNeedsFreshResume(t *testing.T) {
	s, provider, credentials := newBackupServiceFixture(t)
	connectBackupServiceFixture(t, s)
	review := monitoringReviewFixture(t, s)
	if _, err := s.Connect(t.Context(), "local", localFixtureWorkspace, ConnectRequest{review.Generation, review.Revision, uuid.NewString(), review.Token, true}); !errors.Is(err, ErrReviewExpired) {
		t.Fatal("monitoring token became a connection review", err)
	}
	request, _ := enableMonitoringFixture(t, s)
	settings, _ := s.GetSettings(t.Context(), "local", localFixtureWorkspace)
	paused := MonitoringChangeRequest{settings.Generation, settings.Revision, settings.Monitoring.Revision, uuid.NewString(), "paused", "", false}
	if _, err := s.SetMonitoring(t.Context(), "local", localFixtureWorkspace, paused); err != nil {
		t.Fatal(err)
	}
	record, err := s.store.load(t.Context(), "local", localFixtureWorkspace)
	if err != nil || !validLocalReceipt(record) || record.receipt.MonitoringDigest != "" || record.state.Monitoring.Intent != "paused" || record.state.Monitoring.NextDueAt != nil || record.state.Monitoring.RetryAt != nil {
		t.Fatal("pause removed manual authority or kept periodic admission", err)
	}
	settings, _ = s.GetSettings(t.Context(), "local", localFixtureWorkspace)
	request.Generation = settings.Generation
	request.Revision = settings.Revision
	request.MonitoringRevision = settings.Monitoring.Revision
	request.OperationID = uuid.NewString()
	if _, err := s.SetMonitoring(t.Context(), "local", localFixtureWorkspace, request); !errors.Is(err, ErrReviewExpired) {
		t.Fatal("old approval resumed monitoring", err)
	}
	enableMonitoringFixture(t, s)
	if provider.calls.Load() != 1 || credentials.uses.Load() != 1 {
		t.Fatal("pause/resume checked the destination")
	}
}
func TestMonitoringPolicyAndUnsavedReviewRespectBoundsAndLifecycle(t *testing.T) {
	s, provider, credentials := newBackupServiceFixture(t)
	connectBackupServiceFixture(t, s)
	settings, _ := s.GetSettings(t.Context(), "local", localFixtureWorkspace)
	request := MonitoringReviewRequest{settings.Generation, settings.Revision, settings.Monitoring.Revision, settings.Monitoring.Policy}
	for _, change := range []func(*MonitoringPolicy){func(p *MonitoringPolicy) { p.CadenceSeconds = 1 }, func(p *MonitoringPolicy) { p.FreshnessSeconds = 900 }, func(p *MonitoringPolicy) { p.ExpectedAgeSeconds = 31 * 86400 }, func(p *MonitoringPolicy) { p.DisconnectedGraceSeconds = -1 }, func(p *MonitoringPolicy) { p.LowSpacePercent = 0 }} {
		bad := request
		change(&bad.Policy)
		if _, err := s.ReviewMonitoring(t.Context(), "local", localFixtureWorkspace, bad); !errors.Is(err, ErrInvalidRequest) {
			t.Fatal("invalid policy created review", bad.Policy, err)
		}
	}
	review := monitoringReviewFixture(t, s)
	if _, err := s.store.db.ExecContext(t.Context(), `UPDATE workspaces SET status='archived';UPDATE workspaces SET status='active'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetMonitoring(t.Context(), "local", localFixtureWorkspace, MonitoringChangeRequest{review.Generation, review.Revision, review.MonitoringRevision, uuid.NewString(), "active", review.Token, true}); !errors.Is(err, ErrLocalReviewRequired) {
		t.Fatal("lifecycle round trip activated an old monitoring review", err)
	}
	if provider.calls.Load() != 1 || credentials.uses.Load() != 1 {
		t.Fatal("invalid consent/lifecycle caused a read")
	}
}
func TestMonitoringProposalsNeedAnExistingLocalConnection(t *testing.T) {
	s, provider, credentials := newBackupServiceFixture(t)
	settings, err := s.GetSettings(t.Context(), "local", localFixtureWorkspace)
	if err != nil || settings.Configured || settings.Monitoring.Intent != "manual_only" || settings.Monitoring.ConsentValid || !validMonitoringPolicy(settings.Monitoring.Policy) {
		t.Fatal("proposed defaults became permission", settings, err)
	}
	if _, err := s.ReviewMonitoring(t.Context(), "local", localFixtureWorkspace, MonitoringReviewRequest{Policy: settings.Monitoring.Policy}); !errors.Is(err, ErrLocalReviewRequired) {
		t.Fatal("unconfigured workspace obtained monitoring review", err)
	}
	record, err := s.store.load(t.Context(), "local", localFixtureWorkspace)
	if err != nil || record.state != nil || provider.calls.Load() != 0 || credentials.uses.Load() != 0 {
		t.Fatal("viewing/reviewing proposals saved state or read data", err)
	}
}

func TestMonitoringReviewExpiresAndForeignActorCannotConsumeIt(t *testing.T) {
	s, provider, credentials := newBackupServiceFixture(t)
	connectBackupServiceFixture(t, s)
	review := monitoringReviewFixture(t, s)
	request := MonitoringChangeRequest{review.Generation, review.Revision, review.MonitoringRevision, uuid.NewString(), "active", review.Token, true}
	stale := request
	stale.MonitoringRevision++
	if _, err := s.SetMonitoring(t.Context(), "local", localFixtureWorkspace, stale); !errors.Is(err, ErrConfigurationChanged) {
		t.Fatal("wrong independent monitoring revision accepted", err)
	}
	if _, err := s.SetMonitoring(t.Context(), "LOCAL", localFixtureWorkspace, request); !errors.Is(err, ErrWorkspaceUnavailable) {
		t.Fatal("folded actor consumed approval", err)
	}
	s.clock = func() time.Time { return review.ExpiresAt.Add(time.Second) }
	if _, err := s.SetMonitoring(t.Context(), "local", localFixtureWorkspace, request); !errors.Is(err, ErrReviewExpired) {
		t.Fatal("expired monitoring approval accepted", err)
	}
	if provider.calls.Load() != 1 || credentials.uses.Load() != 1 {
		t.Fatal("foreign/expired approval performed a read")
	}
}
