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

func TestMonitoringLifecycleFenceDeniesCredentialAndLatePublication(t *testing.T) {
	for _, change := range []string{"archive", "trash", "delete", "owner"} {
		t.Run(change, func(t *testing.T) {
			s, provider, credentials, clock := newMonitoringFixture(t)
			entered, finish := make(chan struct{}), make(chan struct{})
			var once sync.Once
			release := func() { once.Do(func() { close(finish) }) }
			defer release()
			provider.readFn = func(ctx context.Context, cfg localConfiguration, use backupPasswordUse) (providerReadResult, error) {
				close(entered)
				<-finish
				if err := use(ctx, func([]byte) error { return nil }); err != nil {
					return providerReadResult{}, err
				}
				result := inventedServiceRead(cfg)
				result.observedAt = clock.now().UTC()
				return result, nil
			}
			done := make(chan error, 1)
			go func() { _, err := s.monitorWorkspace(t.Context(), localFixtureWorkspace); done <- err }()
			<-entered
			sql := ""
			switch change {
			case "archive":
				sql = `UPDATE workspaces SET status='archived'`
			case "trash":
				sql = `UPDATE workspaces SET status='trashed'`
			case "delete":
				sql = `DELETE FROM workspaces`
			case "owner":
				sql = `INSERT INTO users(id,created_at,updated_at) VALUES('other',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP);UPDATE workspaces SET owner_user_id='other'`
			}
			if _, err := s.store.db.ExecContext(t.Context(), sql); err != nil {
				t.Fatal(err)
			}
			release()
			if err := <-done; err == nil {
				t.Fatal("obsolete automatic result published")
			}
			if credentials.uses.Load() != 1 || provider.calls.Load() != 2 {
				t.Fatal("fenced read received a password or retried")
			}
			if change != "delete" {
				if _, err := s.store.db.ExecContext(t.Context(), `UPDATE workspaces SET status='active',owner_user_id='local'`); err != nil {
					t.Fatal(err)
				}
				if _, err := s.monitorWorkspace(t.Context(), localFixtureWorkspace); !errors.Is(err, ErrLocalReviewRequired) {
					t.Fatal("restore/owner return resumed automatic access", err)
				}
				status, err := s.GetStatus(t.Context(), "local", localFixtureWorkspace)
				if err != nil || status.EvidenceCurrent || status.Connection != "review_required" {
					t.Fatal("lifecycle resurrected current evidence", status, err)
				}
			} else {
				var count int
				if err := s.store.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM backup_recovery_local`).Scan(&count); err != nil || count != 0 {
					t.Fatal("deleted workspace state was recreated", count, err)
				}
			}
		})
	}
}
func TestSameNamedReplacementCannotInheritCopiedPeriodicState(t *testing.T) {
	s, provider, _, _ := newMonitoringFixture(t)
	if _, err := s.store.db.ExecContext(t.Context(), `UPDATE workspaces SET name='Invented retained backup name'`); err != nil {
		t.Fatal(err)
	}
	record, err := s.store.load(t.Context(), "local", localFixtureWorkspace)
	if err != nil || !monitoringConsentValid(record) {
		t.Fatal("rename unexpectedly changed consent", err)
	}
	newID := uuid.NewString()
	if _, err := s.store.db.ExecContext(t.Context(), `INSERT INTO workspaces(id,name,folder_slug,owner_user_id,kind,status,installed_capabilities_json,created_at,updated_at)
	 SELECT ?,'Invented retained backup name',?,'local','workspace','active',installed_capabilities_json,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP FROM workspaces WHERE id=?`, newID, "replacement-"+newID, localFixtureWorkspace); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.db.ExecContext(t.Context(), `INSERT INTO backup_recovery_local SELECT ?,owner_user_id,schema_version,generation,revision,sequence,authority_epoch,ciphertext,updated_at FROM backup_recovery_local WHERE workspace_id=?`, newID, localFixtureWorkspace); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.db.ExecContext(t.Context(), `INSERT INTO backup_recovery_receipts SELECT ?,owner_user_id,generation,grant_id,ciphertext FROM backup_recovery_receipts WHERE workspace_id=?`, newID, localFixtureWorkspace); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.db.ExecContext(t.Context(), `DELETE FROM workspaces WHERE id=?`, localFixtureWorkspace); err != nil {
		t.Fatal(err)
	}
	if _, err := s.monitorWorkspace(t.Context(), newID); err == nil || provider.calls.Load() != 1 {
		t.Fatal("same name/copied enabled fields recreated periodic authority", err)
	}
}

func TestManualIdentityFailureFencesPeriodicReadAndOldReview(t *testing.T) {
	s, provider, _, _ := newMonitoringFixture(t)
	review := monitoringReviewFixture(t, s)
	provider.readFn = func(context.Context, localConfiguration, backupPasswordUse) (providerReadResult, error) {
		return providerReadResult{}, errIdentityMismatch
	}
	request := refreshBackupServiceFixture(t, s)
	if _, err := s.Refresh(t.Context(), "local", localFixtureWorkspace, request); err != nil {
		t.Fatal(err)
	}
	if _, err := s.monitorWorkspace(t.Context(), localFixtureWorkspace); !errors.Is(err, ErrLocalReviewRequired) {
		t.Fatal("manual identity failure left periodic work admissible", err)
	}
	if _, err := s.SetMonitoring(t.Context(), "local", localFixtureWorkspace, MonitoringChangeRequest{review.Generation, review.Revision, review.MonitoringRevision, uuid.NewString(), "active", review.Token, true}); !errors.Is(err, ErrLocalReviewRequired) {
		t.Fatal("old policy review bypassed new identity failure", err)
	}
	status, _ := s.GetStatus(t.Context(), "local", localFixtureWorkspace)
	if status.Monitoring != "review_required" || provider.calls.Load() != 2 {
		t.Fatal("bad evidence was retried or presented as active", status)
	}
}
func TestReplacingConfigurationDrainsOldPeriodicReadAndReturnsToManualOnly(t *testing.T) {
	s, provider, credentials, clock := newMonitoringFixture(t)
	entered, finish := make(chan struct{}), make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(finish) }) }
	defer release()
	provider.readFn = func(ctx context.Context, cfg localConfiguration, use backupPasswordUse) (providerReadResult, error) {
		if provider.calls.Load() == 2 {
			close(entered)
			<-finish
		} else if err := use(ctx, func([]byte) error { return nil }); err != nil {
			return providerReadResult{}, err
		}
		result := inventedServiceRead(cfg)
		result.observedAt = clock.now().UTC()
		return result, nil
	}
	oldDone := make(chan error, 1)
	go func() { _, err := s.monitorWorkspace(t.Context(), localFixtureWorkspace); oldDone <- err }()
	<-entered
	review := reviewBackupServiceFixture(t, s)
	connected := make(chan error, 1)
	go func() {
		_, err := s.Connect(t.Context(), "local", localFixtureWorkspace, ConnectRequest{review.Generation, review.Revision, uuid.NewString(), review.Token, true})
		connected <- err
	}()
	waitServiceCondition(t, func() bool {
		record, err := s.store.load(t.Context(), "local", localFixtureWorkspace)
		return err == nil && record.state.Generation != review.Generation && record.receipt == nil && record.state.Monitoring.Intent == "manual_only"
	})
	select {
	case err := <-connected:
		t.Fatal("replacement released old periodic ownership", err)
	default:
	}
	release()
	if err := <-oldDone; !errors.Is(err, ErrConfigurationChanged) {
		t.Fatal(err)
	}
	if err := <-connected; err != nil {
		t.Fatal(err)
	}
	record, _ := s.store.load(t.Context(), "local", localFixtureWorkspace)
	if !validLocalReceipt(record) || record.receipt.MonitoringDigest != "" || record.state.Monitoring.Intent != "manual_only" || provider.calls.Load() != 3 || credentials.uses.Load() != 2 {
		t.Fatal("replacement inherited periodic permission or repeated execution")
	}
}

func TestMonitoringPauseTimeoutRemainsFencedAndRetryable(t *testing.T) {
	s, provider, _, clock := newMonitoringFixture(t)
	entered, finish := make(chan struct{}), make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(finish) }) }
	defer release()
	provider.readFn = func(_ context.Context, cfg localConfiguration, _ backupPasswordUse) (providerReadResult, error) {
		close(entered)
		<-finish
		result := inventedServiceRead(cfg)
		result.observedAt = clock.now().UTC()
		return result, nil
	}
	readDone := make(chan error, 1)
	go func() { _, err := s.monitorWorkspace(t.Context(), localFixtureWorkspace); readDone <- err }()
	<-entered
	settings, _ := s.GetSettings(t.Context(), "local", localFixtureWorkspace)
	request := MonitoringChangeRequest{settings.Generation, settings.Revision, settings.Monitoring.Revision, uuid.NewString(), "paused", "", false}
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	if _, err := s.SetMonitoring(ctx, "local", localFixtureWorkspace, request); !errors.Is(err, ErrBusy) {
		t.Fatal("ambiguous pause was marked complete", err)
	}
	pending, err := s.SetMonitoring(t.Context(), "local", localFixtureWorkspace, request)
	if err != nil || pending.State != "pending" {
		t.Fatal("retry released still-owned work", pending, err)
	}
	record, _ := s.store.load(t.Context(), "local", localFixtureWorkspace)
	if record.state.Monitoring.Intent != "paused" || record.receipt.MonitoringDigest != "" {
		t.Fatal("timeout lost its persisted fence")
	}
	release()
	if err := <-readDone; !errors.Is(err, ErrConfigurationChanged) {
		t.Fatal(err)
	}
	completed, err := s.SetMonitoring(t.Context(), "local", localFixtureWorkspace, request)
	if err != nil || completed.State != "complete" || completed.Outcome != "applied" || provider.calls.Load() != 2 {
		t.Fatal("drained retry did not finish safely", completed, err)
	}
}

func TestCapabilityRemovalDrainsAutomaticReaderBeforeCompleting(t *testing.T) {
	s, provider, _, clock := newMonitoringFixture(t)
	entered, finish := make(chan struct{}), make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(finish) }) }
	defer release()
	provider.readFn = func(_ context.Context, cfg localConfiguration, _ backupPasswordUse) (providerReadResult, error) {
		close(entered)
		<-finish
		result := inventedServiceRead(cfg)
		result.observedAt = clock.now().UTC()
		return result, nil
	}
	readDone := make(chan error, 1)
	go func() { _, err := s.monitorWorkspace(t.Context(), localFixtureWorkspace); readDone <- err }()
	<-entered
	settings, _ := s.GetSettings(t.Context(), "local", localFixtureWorkspace)
	runtime := NewCapabilityRuntime(s)
	removed := make(chan error, 1)
	go func() {
		removed <- runtime.PrepareCapabilityRemoval(t.Context(), "local", localFixtureWorkspace, ControlRequest{settings.Generation, settings.Revision, uuid.NewString()})
	}()
	waitServiceCondition(t, func() bool {
		record, err := s.store.load(t.Context(), "local", localFixtureWorkspace)
		return err == nil && pendingRemoval(record) && record.receipt == nil
	})
	select {
	case err := <-removed:
		t.Fatal("uninstall released a live automatic read", err)
	default:
	}
	release()
	if err := <-readDone; !errors.Is(err, ErrConfigurationChanged) {
		t.Fatal(err)
	}
	if err := <-removed; err != nil {
		t.Fatal(err)
	}
	if err := runtime.OnCapabilityRemove(localFixtureWorkspace); err != nil {
		t.Fatal("drained removal was not retryable", err)
	}
	if _, err := s.monitorWorkspace(t.Context(), localFixtureWorkspace); !errors.Is(err, ErrLocalReviewRequired) {
		t.Fatal("pending uninstall admitted a periodic check", err)
	}
}
