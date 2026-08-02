package workspace

import (
	"testing"
	"time"
)

// SyncStore's Goal healing: refill a legacy record, never resurrect a cleared
// one.
//
// These two cases look identical in the data — both have an empty Mission — and
// they must produce opposite behavior. Getting it backwards either loses a
// user's Goal or brings back one they deliberately turned off, so the
// distinction gets its own test.

func newMissionSyncFixture(t *testing.T) (*SyncStore, *FileStore, string) {
	t.Helper()
	disk, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore() error = %v", err)
	}
	primary := NewInMemoryStore()

	next := time.Now().Add(time.Hour)
	seeded := &Workspace{
		ID:               "ws-heal",
		Name:             "Heal",
		Mission:          "Watch the release notes",
		MissionEnabled:   true,
		AutonomyPolicy:   AutonomyPropose,
		Cadence:          &ScheduleConfig{Type: ScheduleDaily, TimeOfDay: "09:00"},
		NextMissionRunAt: &next,
	}
	if err := disk.Save(seeded); err != nil {
		t.Fatalf("seed disk: %v", err)
	}
	if err := primary.Save(&Workspace{ID: "ws-heal", Name: "Heal"}); err != nil {
		t.Fatalf("seed primary: %v", err)
	}
	return NewSyncStore(primary, disk), disk, seeded.ID
}

// A record that predates the mission column keeps the Goal that is on disk.
func TestSyncStore_HealsALegacyRecordsGoal(t *testing.T) {
	sync, disk, id := newMissionSyncFixture(t)

	// A workspace loaded from a store that never carried the Goal — exactly
	// what a pre-migration SQLite row produces.
	legacy := &Workspace{ID: id, Name: "Heal", Description: "an unrelated edit"}
	if err := sync.Save(legacy); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	healed, err := disk.Get(id)
	if err != nil {
		t.Fatalf("disk.Get() error = %v", err)
	}
	if healed.Mission != "Watch the release notes" {
		t.Fatalf("expected the goal to survive an unrelated save, got %q", healed.Mission)
	}
	if !healed.MissionEnabled || healed.Cadence == nil || healed.NextMissionRunAt == nil {
		t.Fatalf("expected the whole goal configuration to survive, got %+v", healed)
	}
	// The unrelated edit still landed.
	if healed.Description != "an unrelated edit" {
		t.Fatalf("expected the actual edit to be saved, got %q", healed.Description)
	}
}

// A Goal the user cleared stays cleared. This is the case that would break if
// healing keyed on "Mission is empty" instead of "the record carried no Goal".
func TestSyncStore_DoesNotResurrectAClearedGoal(t *testing.T) {
	sync, disk, id := newMissionSyncFixture(t)

	cleared := &Workspace{ID: id, Name: "Heal"}
	// The store that loaded it DID carry a goal envelope — the user emptied it.
	cleared.MarkMissionLoaded()

	if err := sync.Save(cleared); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	after, err := disk.Get(id)
	if err != nil {
		t.Fatalf("disk.Get() error = %v", err)
	}
	if after.Mission != "" || after.MissionEnabled || after.Cadence != nil {
		t.Fatalf("expected a deliberately cleared goal to stay cleared, got %+v", after)
	}
}

// Disabling a Goal while keeping its text is also a real edit, not an absence.
func TestSyncStore_DoesNotResurrectADisabledGoal(t *testing.T) {
	sync, disk, id := newMissionSyncFixture(t)

	disabled := &Workspace{ID: id, Name: "Heal", Mission: "Watch the release notes"}
	disabled.MarkMissionLoaded()

	if err := sync.Save(disabled); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	after, err := disk.Get(id)
	if err != nil {
		t.Fatalf("disk.Get() error = %v", err)
	}
	if after.MissionEnabled {
		t.Fatalf("expected the goal to stay disabled")
	}
	if after.Mission != "Watch the release notes" {
		t.Fatalf("expected the goal text to be kept, got %q", after.Mission)
	}
}
