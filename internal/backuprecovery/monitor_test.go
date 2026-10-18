//go:build darwin || linux

package backuprecovery

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

type monitoringClock struct {
	mu sync.Mutex
	at time.Time
}

func (c *monitoringClock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.at }
func (c *monitoringClock) advance(by time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = c.at.Add(by)
}
func newMonitoringFixture(t *testing.T) (*Service, *serviceFixtureProvider, *serviceFixtureCredentials, *monitoringClock) {
	t.Helper()
	s, provider, credentials := newBackupServiceFixture(t)
	connectBackupServiceFixture(t, s)
	clock := &monitoringClock{at: time.Now().UTC()}
	s.clock = clock.now
	provider.readFn = func(ctx context.Context, cfg localConfiguration, use backupPasswordUse) (providerReadResult, error) {
		if err := use(ctx, func([]byte) error { return nil }); err != nil {
			return providerReadResult{}, err
		}
		result := inventedServiceRead(cfg)
		result.observedAt = clock.now().UTC()
		return result, nil
	}
	enableMonitoringFixture(t, s)
	return s, provider, credentials, clock
}
func TestMonitorCoalescesSleepAndReservesRestartSlotBeforeRead(t *testing.T) {
	s, provider, _, clock := newMonitoringFixture(t)
	if result, err := s.monitorWorkspace(t.Context(), localFixtureWorkspace); err != nil || result.Outcome != "read_ok" {
		t.Fatal(result, err)
	}
	clock.mu.Lock()
	clock.at = clock.at.In(time.FixedZone("invented DST offset", 3600))
	clock.mu.Unlock()
	if _, err := s.monitorWorkspace(t.Context(), localFixtureWorkspace); err != nil {
		t.Fatal(err)
	}
	if provider.calls.Load() != 2 {
		t.Fatal("same due slot read twice")
	}
	clock.advance(12 * time.Hour)
	if _, err := s.monitorWorkspace(t.Context(), localFixtureWorkspace); err != nil {
		t.Fatal(err)
	}
	if provider.calls.Load() != 3 {
		t.Fatal("sleep replayed missed intervals")
	}
	// Model the durable admission left by interruption, without claiming a real
	// process crash. The existing slot/due record is not replay permission.
	record, err := s.store.load(t.Context(), "local", localFixtureWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.update(t.Context(), "local", localFixtureWorkspace, record.expected(), changeAdmission, func(current *localRecord) error {
		now := clock.now().UTC()
		next := now.Add(time.Hour)
		current.state.Monitoring.NextDueAt = &next
		current.state.Monitoring.LastSlotAt = &now
		appendOperation(current.state, pendingOperation(current.state, uuid.NewString(), "monitor", localDigest("invented interrupted slot"), now), now)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	restartedProvider := &serviceFixtureProvider{readFn: provider.readFn}
	restarted, err := newService(ServiceOptions{Database: s.store.db, Secrets: s.store.secrets, DataDir: s.store.root.path, WorkGate: s.work, Credentials: s.credentials, Selections: s.selections}, restartedProvider, clock.now)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := restarted.Close(t.Context()); err != nil {
			t.Error(err)
		}
	}()
	if _, err := restarted.monitorWorkspace(t.Context(), localFixtureWorkspace); err != nil {
		t.Fatal(err)
	}
	if restartedProvider.calls.Load() != 0 {
		t.Fatal("restart replayed an admitted interrupted read")
	}
	clock.advance(-2 * time.Hour)
	if _, err := restarted.monitorWorkspace(t.Context(), localFixtureWorkspace); err != nil {
		t.Fatal(err)
	}
	if restartedProvider.calls.Load() != 0 {
		t.Fatal("clock rollback admitted external work")
	}
}
func TestMonitorUsesOnlyOneTransientRetryAndPreservesSuccessfulTime(t *testing.T) {
	s, provider, _, clock := newMonitoringFixture(t)
	before, _ := s.GetStatus(t.Context(), "local", localFixtureWorkspace)
	provider.readFn = func(context.Context, localConfiguration, backupPasswordUse) (providerReadResult, error) {
		return providerReadResult{}, errReadProcess
	}
	if _, err := s.monitorWorkspace(t.Context(), localFixtureWorkspace); err != nil {
		t.Fatal(err)
	}
	record, _ := s.store.load(t.Context(), "local", localFixtureWorkspace)
	if record.state.Monitoring.RetryAt == nil || record.state.Monitoring.RetryUsed {
		t.Fatal("missing bounded retry")
	}
	clock.advance(monitorRetryDelay)
	if _, err := s.monitorWorkspace(t.Context(), localFixtureWorkspace); err != nil {
		t.Fatal(err)
	}
	clock.advance(monitorRetryDelay)
	if _, err := s.monitorWorkspace(t.Context(), localFixtureWorkspace); err != nil {
		t.Fatal(err)
	}
	record, _ = s.store.load(t.Context(), "local", localFixtureWorkspace)
	if provider.calls.Load() != 3 || !record.state.Monitoring.RetryUsed || record.state.Monitoring.RetryAt != nil {
		t.Fatal("transient retry became a storm")
	}
	after, _ := s.GetStatus(t.Context(), "local", localFixtureWorkspace)
	if after.LastSuccessfulRead == nil || !after.LastSuccessfulRead.Equal(*before.LastSuccessfulRead) {
		t.Fatal("failed automatic check advanced success")
	}
}
func TestMonitorIdentityOrCredentialFailureRequiresFreshLocalEvidence(t *testing.T) {
	for _, failure := range []error{errIdentityMismatch, errCredentialUnavailable} {
		t.Run(failure.Error(), func(t *testing.T) {
			s, provider, _, clock := newMonitoringFixture(t)
			provider.readFn = func(context.Context, localConfiguration, backupPasswordUse) (providerReadResult, error) {
				return providerReadResult{}, failure
			}
			if _, err := s.monitorWorkspace(t.Context(), localFixtureWorkspace); err != nil {
				t.Fatal(err)
			}
			record, _ := s.store.load(t.Context(), "local", localFixtureWorkspace)
			if record.state.Monitoring.Intent != "paused" || record.receipt.MonitoringDigest != "" || record.state.Monitoring.RetryAt != nil {
				t.Fatal("permanent failure retained automatic permission")
			}
			clock.advance(48 * time.Hour)
			if _, err := s.monitorWorkspace(t.Context(), localFixtureWorkspace); !errors.Is(err, ErrLocalReviewRequired) {
				t.Fatal("permanent failure retried", err)
			}
			settings, _ := s.GetSettings(t.Context(), "local", localFixtureWorkspace)
			if _, err := s.ReviewMonitoring(t.Context(), "local", localFixtureWorkspace, MonitoringReviewRequest{settings.Generation, settings.Revision, settings.Monitoring.Revision, settings.Monitoring.Policy}); !errors.Is(err, ErrLocalReviewRequired) {
				t.Fatal("old failed evidence resumed periodic access", err)
			}
			if provider.calls.Load() != 2 {
				t.Fatal("identity/credential failure repeated a read")
			}
		})
	}
}
func TestMonitorDisconnectedDestinationWaitsForRegularCadence(t *testing.T) {
	s, provider, _, clock := newMonitoringFixture(t)
	working := provider.readFn
	provider.readFn = func(context.Context, localConfiguration, backupPasswordUse) (providerReadResult, error) {
		return providerReadResult{}, errDestinationAbsent
	}
	if result, err := s.monitorWorkspace(t.Context(), localFixtureWorkspace); err != nil || result.Outcome != "destination_absent" {
		t.Fatal(result, err)
	}
	record, _ := s.store.load(t.Context(), "local", localFixtureWorkspace)
	if !monitoringConsentValid(record) || record.state.Monitoring.RetryAt != nil {
		t.Fatal("disconnection became lost authority or a retry storm")
	}
	clock.advance(monitorRetryDelay)
	if _, err := s.monitorWorkspace(t.Context(), localFixtureWorkspace); err != nil {
		t.Fatal(err)
	}
	if provider.calls.Load() != 2 {
		t.Fatal("offline destination retried before the cadence")
	}
	provider.readFn = working
	clock.advance(time.Hour)
	if result, err := s.monitorWorkspace(t.Context(), localFixtureWorkspace); err != nil || result.Outcome != "read_ok" {
		t.Fatal(result, err)
	}
}
func TestMonitorFullWorkerPoolDoesNotSkipUnadmittedPage(t *testing.T) {
	s, provider, _, _ := newMonitoringFixture(t)
	for range monitorPageLimit {
		id := uuid.NewString()
		if _, err := s.store.db.ExecContext(t.Context(), `INSERT INTO workspaces(id,name,folder_slug,owner_user_id,kind,status,created_at,updated_at) VALUES (?,?,?,'local','workspace','active',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, id, "Invented scan "+id, "scan-"+id); err != nil {
			t.Fatal(err)
		}
		// Intentionally inert invented envelopes, never activation receipts.
		if _, err := s.store.db.ExecContext(t.Context(), `INSERT INTO backup_recovery_local VALUES (?,'local',1,?,1,0,0,zeroblob(28),CURRENT_TIMESTAMP)`, id, uuid.NewString()); err != nil {
			t.Fatal(err)
		}
	}
	s.monitorJobs <- struct{}{}
	s.monitorJobs <- struct{}{}
	defer func() { <-s.monitorJobs; <-s.monitorJobs }()
	s.monitorTick(t.Context())
	s.mu.Lock()
	cursor := s.monitorCursor
	s.mu.Unlock()
	if cursor != "" || provider.calls.Load() != 1 {
		t.Fatal("full pool paged past work it never admitted", cursor)
	}
}

func TestMonitorPausedDuringOwnedReadCannotPublishLateFacts(t *testing.T) {
	s, provider, _, clock := newMonitoringFixture(t)
	entered, finish := make(chan struct{}), make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(finish) }) }
	defer release()
	provider.readFn = func(_ context.Context, cfg localConfiguration, _ backupPasswordUse) (providerReadResult, error) {
		close(entered)
		<-finish
		result := inventedServiceRead(cfg)
		result.observedAt = clock.now()
		return result, nil
	}
	before, _ := s.GetStatus(t.Context(), "local", localFixtureWorkspace)
	done := make(chan error, 1)
	go func() { _, err := s.monitorWorkspace(t.Context(), localFixtureWorkspace); done <- err }()
	<-entered
	record, _ := s.store.load(t.Context(), "local", localFixtureWorkspace)
	if record.state.Monitoring.NextDueAt == nil || !record.state.Monitoring.NextDueAt.After(clock.now()) || len(record.state.Operations) == 0 || record.state.Operations[0].Action != "monitor" || record.state.Operations[0].Status != "pending" {
		t.Fatal("read started before durable slot admission")
	}
	if _, err := s.monitorWorkspace(t.Context(), localFixtureWorkspace); !errors.Is(err, ErrBusy) {
		t.Fatal("overlapping automatic read admitted", err)
	}
	settings, _ := s.GetSettings(t.Context(), "local", localFixtureWorkspace)
	paused := make(chan error, 1)
	go func() {
		_, err := s.SetMonitoring(t.Context(), "local", localFixtureWorkspace, MonitoringChangeRequest{settings.Generation, settings.Revision, settings.Monitoring.Revision, uuid.NewString(), "paused", "", false})
		paused <- err
	}()
	waitServiceCondition(t, func() bool {
		record, err := s.store.load(t.Context(), "local", localFixtureWorkspace)
		return err == nil && record.state.Monitoring.Intent == "paused" && record.receipt.MonitoringDigest == ""
	})
	select {
	case err := <-paused:
		t.Fatal("pause completed before owned work drained", err)
	default:
	}
	release()
	if err := <-paused; err != nil {
		t.Fatal("pause did not complete after drain", err)
	}
	if err := <-done; !errors.Is(err, ErrConfigurationChanged) {
		t.Fatal("obsolete monitoring result published", err)
	}
	after, _ := s.GetStatus(t.Context(), "local", localFixtureWorkspace)
	if after.Monitoring != "paused" || after.LastSuccessfulRead == nil || !after.LastSuccessfulRead.Equal(*before.LastSuccessfulRead) {
		t.Fatal("late read resurrected monitoring/evidence", after)
	}
}
func TestMonitorLoopIsExplicitSingleRegistrationAndServiceOwned(t *testing.T) {
	s, provider, _, _ := newMonitoringFixture(t)
	if provider.calls.Load() != 1 {
		t.Fatal("construction/consent ran a check")
	}
	if err := s.StartMonitoring(); err != nil {
		t.Fatal(err)
	}
	if err := s.StartMonitoring(); err != nil {
		t.Fatal(err)
	}
	waitServiceCondition(t, func() bool {
		status, err := s.GetStatus(t.Context(), "local", localFixtureWorkspace)
		return err == nil && !status.InProgress && status.Monitoring == "active" && status.LastAttempt != nil && provider.calls.Load() == 2
	})
	if err := s.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if provider.closes.Load() != 1 || provider.calls.Load() != 2 {
		t.Fatal("duplicate loop or early/repeated provider close")
	}
}
