//go:build darwin || linux

package backuprecovery

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestRecoveryGuideIsPinnedNonExecutingAndOwnsItsSlices(t *testing.T) {
	guide := ExternalRecoveryGuide()
	if guide.Version != "0.19.1" || len(guide.Steps) != 6 || len(guide.Sources) != 2 {
		t.Fatal("missing vetted recovery guide")
	}
	for _, source := range guide.Sources {
		if source.URL != "https://github.com/restic/restic/blob/v0.19.1/doc/030_preparing_a_new_repo.rst" && source.URL != "https://github.com/restic/restic/blob/v0.19.1/doc/050_restore.rst" {
			t.Fatal("unvetted source")
		}
	}
	encoded, _ := json.Marshal(guide)
	for _, required := range []string{"separate empty recovery target", "Do not overwrite", "Ori does not open or compare", "Never put a password", "User reports passed", "not off-site"} {
		if !strings.Contains(string(encoded), required) {
			t.Fatal("missing recovery boundary", required)
		}
	}
	guide.Steps[0].Instructions[0] = "mutated"
	guide.Sources[0].URL = "javascript:mutated"
	again := ExternalRecoveryGuide()
	if again.Steps[0].Instructions[0] == "mutated" || again.Sources[0].URL == "javascript:mutated" {
		t.Fatal("shared mutable guide")
	}
}
func recoveryServiceFixture(t *testing.T, future bool) (*Service, *serviceFixtureProvider, *serviceFixtureCredentials) {
	t.Helper()
	s, provider, credentials := newBackupServiceFixture(t)
	provider.readFn = func(ctx context.Context, cfg localConfiguration, use backupPasswordUse) (providerReadResult, error) {
		if err := use(ctx, func([]byte) error { return nil }); err != nil {
			return providerReadResult{}, err
		}
		result := inventedServiceRead(cfg)
		started := result.observedAt.Add(-time.Minute)
		completed := result.observedAt.Add(-time.Second)
		if future {
			started = result.observedAt.Add(time.Hour)
			completed = started.Add(time.Minute)
		}
		result.snapshots = []localSnapshot{{ID: strings.Repeat("a", 64), StartedAt: started, CompletedAt: &completed, DeclaredPaths: []string{cfg.Sources[0].Path}, Exclusions: []string{"untrusted-provider-exclusion"}}}
		return result, nil
	}
	connectBackupServiceFixture(t, s)
	return s, provider, credentials
}
func TestRecoveryUsesOnlyAuthorizedStoredEvidenceAndLabelsStaleHistorical(t *testing.T) {
	s, provider, credentials := recoveryServiceFixture(t, false)
	view, err := s.GetRecovery(t.Context(), "local", localFixtureWorkspace)
	if err != nil || view.Observation != "current" || len(view.Snapshots) != 1 || view.Snapshots[0].Evidence != "stored" || len(view.Sources) != 1 {
		t.Fatal(view, err)
	}
	encoded, _ := json.Marshal(view)
	if strings.Contains(string(encoded), "untrusted-provider-exclusion") {
		t.Fatal("raw provider text crossed the guide boundary")
	}
	view.Snapshots[0].ID = "mutated"
	*view.Snapshots[0].CompletedAt = time.Time{}
	again, err := s.GetRecovery(t.Context(), "local", localFixtureWorkspace)
	if err != nil || again.Snapshots[0].ID == "mutated" || again.Snapshots[0].CompletedAt.IsZero() {
		t.Fatal("view mutated stored evidence", err)
	}
	now := again.ServerTime
	s.clock = func() time.Time { return now.Add(25 * time.Hour) }
	stale, err := s.GetRecovery(t.Context(), "local", localFixtureWorkspace)
	if err != nil || stale.Observation != "stale" || stale.Snapshots[0].Evidence != "stale" {
		t.Fatal("stale snapshot became current", stale, err)
	}
	if _, err := s.GetRecovery(t.Context(), "other", localFixtureWorkspace); !errors.Is(err, ErrWorkspaceUnavailable) {
		t.Fatal("foreign recovery view", err)
	}
	s.clock = time.Now
	if _, err := s.Revoke(t.Context(), "local", localFixtureWorkspace, ControlRequest{again.Generation, again.Revision, uuid.NewString()}, false); err != nil {
		t.Fatal(err)
	}
	historical, err := s.GetRecovery(t.Context(), "local", localFixtureWorkspace)
	if err != nil || historical.Snapshots[0].Evidence != "historical" || historical.Snapshots[0].Generation == historical.Generation {
		t.Fatal("old snapshot became new configuration evidence", historical, err)
	}
	if provider.calls.Load() != 1 || credentials.uses.Load() != 1 {
		t.Fatal("recovery guidance probed or requested a password")
	}
}
func TestRecoveryQuarantinesFutureSnapshotTimesWithoutReadingAgain(t *testing.T) {
	s, provider, credentials := recoveryServiceFixture(t, true)
	view, err := s.GetRecovery(t.Context(), "local", localFixtureWorkspace)
	if err != nil || len(view.Snapshots) != 1 || view.Snapshots[0].Evidence != "clock_suspect" || view.Snapshots[0].StartedAt != nil || view.Snapshots[0].CompletedAt != nil {
		t.Fatal(view, err)
	}
	if provider.calls.Load() != 1 || credentials.uses.Load() != 1 {
		t.Fatal("future timestamp triggered a probe")
	}
}
