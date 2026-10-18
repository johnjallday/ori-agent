//go:build darwin || linux

package backuprecovery

import (
	"math"
	"testing"
	"time"
)

func healthFixture() (Status, *localState, time.Time) {
	now := time.Date(2026, 10, 18, 1, 0, 0, 0, time.UTC)
	count := 1
	status := Status{StateVersion: StateVersion{Generation: localUUID(7)}, Connection: "configured", RuntimeAvailable: true, Monitoring: "manual_only", Destination: "present", EvidenceCurrent: true, LastSuccessfulRead: &now, LastCompletedAt: &now, SnapshotCount: &count, LastAttempt: &Attempt{Generation: localUUID(7), FinishedAt: now, StartedAt: now.Add(-time.Second), Outcome: "read_ok"}}
	state := &localState{Generation: status.Generation, Monitoring: localMonitoring{Intent: "manual_only", Policy: proposedMonitoringPolicy()}}
	return status, state, now
}
func hasHealthProblem(health EvidenceHealth, code string) bool {
	for _, problem := range health.Problems {
		if problem == code {
			return true
		}
	}
	return false
}
func TestHealthSeparatesFreshnessRecencyAndMetadataFailure(t *testing.T) {
	status, state, now := healthFixture()
	if health := evaluateHealth(status, state, now.Add(24*time.Hour)); health.Observation != "current" || health.Recency != "recent" {
		t.Fatal("threshold equality became stale/overdue", health)
	}
	if health := evaluateHealth(status, state, now.Add(24*time.Hour+time.Second)); health.Observation != "stale" || health.Recency != "recent" {
		t.Fatal("freshness became backup recency", health)
	}
	if health := evaluateHealth(status, state, now.Add(48*time.Hour+time.Second)); health.Recency != "overdue" || !hasHealthProblem(health, "backup_completion_overdue") {
		t.Fatal(health)
	}
	status.LastAttempt.Outcome = "read_unavailable"
	if health := evaluateHealth(status, state, now.Add(time.Hour)); health.Observation != "current" || health.Recency != "recent" || health.LatestCheck != "failed" {
		t.Fatal("failed check erased successful evidence", health)
	}
	state.Monitoring.Intent = "active"
	state.Monitoring.Revision = 1
	if health := evaluateHealth(status, state, now.Add(3*time.Hour+time.Second)); health.Observation != "stale" {
		t.Fatal("reviewed freshness policy ignored", health)
	}
}
func TestHealthDistinguishesEmptyUnknownHistoricalAndFutureEvidence(t *testing.T) {
	status, state, now := healthFixture()
	zero := 0
	status.SnapshotCount = &zero
	status.LastCompletedAt = nil
	if health := evaluateHealth(status, state, now); health.Recency != "no_completed_snapshot" {
		t.Fatal(health)
	}
	one := 1
	status.SnapshotCount = &one
	if health := evaluateHealth(status, state, now); health.Recency != "completion_unavailable" {
		t.Fatal(health)
	}
	status.SnapshotCount = nil
	if health := evaluateHealth(status, state, now); health.Recency != "unknown" {
		t.Fatal("unknown became zero", health)
	}
	status.EvidenceCurrent = false
	if health := evaluateHealth(status, state, now); health.Observation != "historical" || health.Recency == "recent" {
		t.Fatal("old generation became current", health)
	}
	status.EvidenceCurrent = true
	status.SnapshotCount = &one
	future := now.Add(6 * time.Minute)
	status.LastCompletedAt = &future
	if health := evaluateHealth(status, state, now); health.Summary != "unavailable" || health.Recency != "clock_suspect" || !hasHealthProblem(health, "clock_suspect") {
		t.Fatal("future completion became protection", health)
	}
}
func TestHealthKeepsDisconnectGraceSeparateFromBackupFailure(t *testing.T) {
	status, state, now := healthFixture()
	status.Destination = "absent"
	status.LastAttempt.Outcome = "destination_absent"
	state.Attempts = []localAttempt{{Generation: state.Generation, FinishedAt: now, StartedAt: now, Outcome: "destination_absent"}, {Generation: state.Generation, FinishedAt: now.Add(-time.Hour), StartedAt: now.Add(-time.Hour), Outcome: "read_ok"}}
	health := evaluateHealth(status, state, now.Add(time.Hour))
	if health.Disconnection != "within_grace" || health.Recency != "recent" || health.LatestCheck != "destination_absent" || hasHealthProblem(health, "metadata_check_failed") {
		t.Fatal("expected offline period became backup failure", health)
	}
	health = evaluateHealth(status, state, now.Add(24*time.Hour))
	if health.Disconnection != "grace_exceeded" || !hasHealthProblem(health, "destination_disconnected_beyond_grace") {
		t.Fatal(health)
	}
	state.Attempts = make([]localAttempt, maxAttemptHistory)
	for index := range state.Attempts {
		state.Attempts[index] = localAttempt{Generation: state.Generation, FinishedAt: now, StartedAt: now, Outcome: "destination_absent"}
	}
	if health := evaluateHealth(status, state, now.Add(time.Hour)); health.Disconnection != "unknown_duration" {
		t.Fatal("bounded history invented the outage start", health)
	}
}
func TestHealthCapacityUsesExactBoundsAndNeverPredictsFit(t *testing.T) {
	status, state, now := healthFixture()
	if health := evaluateHealth(status, state, now); health.Capacity != "unknown" {
		t.Fatal("unavailable capacity became zero", health)
	}
	status.Capacity = &CapacityEvidence{TotalBytes: 100, AvailableBytes: 0, ObservedAt: now}
	if health := evaluateHealth(status, state, now); health.Capacity != "low" {
		t.Fatal("known full destination became unknown", health)
	}
	status.Capacity.AvailableBytes = 10
	if health := evaluateHealth(status, state, now); health.Capacity != "low" {
		t.Fatal("exact warning boundary ignored", health)
	}
	status.Capacity.AvailableBytes = 11
	if health := evaluateHealth(status, state, now); health.Capacity != "not_low" {
		t.Fatal(health)
	}
	if health := evaluateHealth(status, state, now.Add(25*time.Hour)); health.Capacity != "stale" {
		t.Fatal("old capacity was presented as current", health)
	}
	status.Capacity.TotalBytes = math.MaxUint64
	state.Monitoring.Policy.LowSpacePercent = 50
	status.Capacity.AvailableBytes = math.MaxUint64 / 2
	if health := evaluateHealth(status, state, now); health.Capacity != "low" {
		t.Fatal("capacity arithmetic overflowed", health)
	}
}
func TestStoredHealthReevaluatesTimeWithoutProviderOrPasswordCalls(t *testing.T) {
	s, provider, credentials := newBackupServiceFixture(t)
	connectBackupServiceFixture(t, s)
	before, _ := s.GetStatus(t.Context(), "local", localFixtureWorkspace)
	s.clock = func() time.Time { return before.ServerTime.Add(25 * time.Hour) }
	after, err := s.GetStatus(t.Context(), "local", localFixtureWorkspace)
	if err != nil || after.Health.Observation != "stale" || after.LastSuccessfulRead == nil || !after.LastSuccessfulRead.Equal(*before.LastSuccessfulRead) {
		t.Fatal("stored health changed evidence or failed to age it", after, err)
	}
	if provider.calls.Load() != 1 || credentials.uses.Load() != 1 {
		t.Fatal("health evaluation performed IO")
	}
}
