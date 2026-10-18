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

func TestCapabilityStatusReflectsStoredMonitoringAndAttentionWithoutReadingProvider(t *testing.T) {
	for _, check := range []struct {
		monitoring string
		problems   []string
		want       string
	}{
		{"manual_only", nil, "ready"}, {"active", nil, "monitoring"}, {"paused", nil, "paused"}, {"unavailable", nil, "unavailable"}, {"review_required", nil, "needs_attention"}, {"active", []string{"backup_completion_overdue"}, "needs_attention"},
	} {
		status := Status{RuntimeAvailable: true, Connection: "configured", Monitoring: check.monitoring, Health: EvidenceHealth{Problems: check.problems}}
		if got := backupCapabilityStatus(status); string(got.State) != check.want || !got.Configured {
			t.Fatal(check, got)
		}
	}
	s, provider, credentials := newBackupServiceFixture(t)
	connectBackupServiceFixture(t, s)
	got, err := NewCapabilityRuntime(s).CapabilityStatus(localFixtureWorkspace)
	if err != nil || string(got.State) != "needs_attention" || provider.calls.Load() != 1 || credentials.uses.Load() != 1 {
		t.Fatal("empty stored evidence was called ready or status probed", got, err)
	}
}

func TestCapabilityRemovalFencesAdmissionAcrossDrainAndInstallRecordWrite(t *testing.T) {
	s, provider, credentials := newBackupServiceFixture(t)
	connectBackupServiceFixture(t, s)
	request := refreshBackupServiceFixture(t, s)
	entered, finish := make(chan struct{}), make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(finish) }) }
	defer release()
	provider.readFn = func(_ context.Context, cfg localConfiguration, _ backupPasswordUse) (providerReadResult, error) {
		close(entered)
		<-finish
		return inventedServiceRead(cfg), nil
	}
	readDone := make(chan error, 1)
	go func() { _, err := s.Refresh(t.Context(), "local", localFixtureWorkspace, request); readDone <- err }()
	<-entered
	removeDone := make(chan error, 1)
	runtime := NewCapabilityRuntime(s)
	control := ControlRequest{request.Generation, request.Revision, uuid.NewString()}
	if err := runtime.OnCapabilityRemove(localFixtureWorkspace); !errors.Is(err, ErrLocalReviewRequired) {
		t.Fatal("context-free callback initiated removal", err)
	}
	go func() {
		removeDone <- runtime.PrepareCapabilityRemoval(t.Context(), "local", localFixtureWorkspace, control)
	}()
	waitServiceCondition(t, func() bool {
		record, err := s.store.load(t.Context(), "local", localFixtureWorkspace)
		return err == nil && pendingRemoval(record) && record.receipt == nil
	})
	select {
	case err := <-removeDone:
		t.Fatal("removal released ownership before drain", err)
	default:
	}
	release()
	if err := <-readDone; !errors.Is(err, ErrConfigurationChanged) {
		t.Fatal("late read published", err)
	}
	if err := <-removeDone; err != nil {
		t.Fatal(err)
	}
	beforeCalls, beforeUses := provider.calls.Load(), credentials.uses.Load()
	// The generic capability layer has not written the install record yet.
	// Even after an outcome's TTL, this persistent barrier cannot be expired
	// or evicted by public mutations or by retrying the removal hook.
	s.clock = func() time.Time { return time.Now().Add(25 * time.Hour) }
	status, err := s.GetStatus(t.Context(), "local", localFixtureWorkspace)
	if err != nil || !status.RemovalPending || status.EvidenceCurrent || status.InProgress {
		t.Fatal(status, err)
	}
	if _, err := s.Review(t.Context(), "local", localFixtureWorkspace, ReviewRequest{Generation: status.Generation, Revision: status.Revision}); !errors.Is(err, ErrRemovalPending) {
		t.Fatal("removal admitted review", err)
	}
	if _, err := s.Connect(t.Context(), "local", localFixtureWorkspace, ConnectRequest{Generation: status.Generation, Revision: status.Revision, OperationID: uuid.NewString(), ReviewToken: "old-review", AllowMetadataRead: true}); !errors.Is(err, ErrRemovalPending) {
		t.Fatal("removal admitted connect", err)
	}
	if _, err := s.Revoke(t.Context(), "local", localFixtureWorkspace, ControlRequest{status.Generation, status.Revision, uuid.NewString()}, true); !errors.Is(err, ErrRemovalPending) {
		t.Fatal("public revoke evicted removal barrier", err)
	}
	if err := runtime.OnCapabilityRemove(localFixtureWorkspace); err != nil {
		t.Fatal("removal retry failed", err)
	}
	if err := runtime.PrepareCapabilityRemoval(t.Context(), "local", localFixtureWorkspace, control); err != nil {
		t.Fatal("identical removal control failed", err)
	}
	altered := control
	altered.Revision++
	if err := runtime.PrepareCapabilityRemoval(t.Context(), "local", localFixtureWorkspace, altered); !errors.Is(err, ErrConfigurationChanged) {
		t.Fatal("same UUID changed input", err)
	}
	if provider.calls.Load() != beforeCalls || credentials.uses.Load() != beforeUses {
		t.Fatal("removal/retry/read-only status repeated access")
	}
	s.clock = time.Now
	if _, err := s.store.db.ExecContext(t.Context(), `UPDATE workspaces SET installed_capabilities_json='[]';UPDATE workspaces SET installed_capabilities_json='`+localFixtureInstall+`'`); err != nil {
		t.Fatal(err)
	}
	if err := runtime.PrepareCapabilityRemoval(t.Context(), "local", localFixtureWorkspace, control); !errors.Is(err, ErrConfigurationChanged) {
		t.Fatal("old removal control targeted new installation", err)
	}
	if err := runtime.ValidateCapabilityRemoval(localFixtureWorkspace); !errors.Is(err, ErrConfigurationChanged) {
		t.Fatal("old removal fence targeted reinstallation", err)
	}
	reviewBackupServiceFixture(t, s) // New live lifecycle, still review only.
	if provider.calls.Load() != beforeCalls || credentials.uses.Load() != beforeUses {
		t.Fatal("reinstallation inherited read access")
	}
}
