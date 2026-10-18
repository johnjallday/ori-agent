//go:build darwin || linux

package backuprecovery

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/resetstate"
)

func TestMonitorResetFenceAndClosePreventAnyLaterWriter(t *testing.T) {
	s, provider, _, _ := newMonitoringFixture(t)
	if err := s.StartMonitoring(); err != nil {
		t.Fatal(err)
	}
	waitServiceCondition(t, func() bool { return provider.calls.Load() == 2 && s.work.Snapshot().Active == 0 })
	if err := s.work.TryFence(t.Context()); err != nil {
		t.Fatal("idle monitor could not enter reset lifecycle", err)
	}
	if err := s.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := s.StartMonitoring(); !errors.Is(err, ErrUnavailable) {
		t.Fatal("closed/reset monitor restarted", err)
	}
	if _, err := s.monitorWorkspace(t.Context(), localFixtureWorkspace); !errors.Is(err, ErrUnavailable) {
		t.Fatal("fenced monitor wrote after reset", err)
	}
	if provider.calls.Load() != 2 || provider.closes.Load() != 1 {
		t.Fatal("reset repeated work or released provider twice")
	}
}
func TestMonitorShutdownTimeoutRetainsOwnedWorkAndLifetime(t *testing.T) {
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
	if err := s.StartMonitoring(); err != nil {
		t.Fatal(err)
	}
	<-entered
	if err := s.work.TryFence(t.Context()); !errors.Is(err, resetstate.ErrWorkActive) {
		t.Fatal("reset passed an owned automatic reader", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	if err := s.Close(ctx); !errors.Is(err, ErrBusy) {
		t.Fatal("ambiguous automatic cleanup released ownership", err)
	}
	if provider.closes.Load() != 0 || s.work.Snapshot().Active == 0 {
		t.Fatal("live child/provider ownership was released")
	}
	release()
	if err := s.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if provider.closes.Load() != 1 || s.work.Snapshot().Active != 0 {
		t.Fatal("drained shutdown retained finite work or double closed")
	}
}
func TestImportedEnabledIntentAndCopiedRootCannotStartPeriodicRead(t *testing.T) {
	s, provider, _, clock := newMonitoringFixture(t)
	foreignProvider := &serviceFixtureProvider{}
	foreign, err := newService(ServiceOptions{Database: s.store.db, Secrets: s.store.secrets, DataDir: t.TempDir(), WorkGate: s.work, Credentials: s.credentials}, foreignProvider, clock.now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := foreign.monitorWorkspace(t.Context(), localFixtureWorkspace); err == nil || foreignProvider.calls.Load() != 0 {
		t.Fatal("copied installation root reused periodic authority", err)
	}
	if err := foreign.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.db.ExecContext(t.Context(), `INSERT INTO continuity_operations(id,tree_digest,destination_digest,owner_user_id,action,status,created_at,updated_at)
	 VALUES ('invented-monitor-import','invented-tree','invented-destination','local','continue','complete',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP);
	 UPDATE continuity_attachments SET state='imported_inactive',operation_id='invented-monitor-import',source_generation=?,source_digest=?`, localUUID(200), strings.Repeat("d", 64)); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"imported_inactive", "imported_active"} {
		if _, err := s.store.db.ExecContext(t.Context(), `UPDATE continuity_attachments SET state=?`, state); err != nil {
			t.Fatal(err)
		}
		if _, err := s.monitorWorkspace(t.Context(), localFixtureWorkspace); !errors.Is(err, ErrLocalReviewRequired) {
			t.Fatal("general import activation became backup consent", state, err)
		}
	}
	if provider.calls.Load() != 1 {
		t.Fatal("imported enabled flag probed a destination")
	}
}
