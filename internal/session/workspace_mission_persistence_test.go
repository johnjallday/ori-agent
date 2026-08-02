package session

import (
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Workspace Goal (mission) persistence through the SQLite-primary store.
//
// Every mission field except Opportunities was dropped by the adapter, so on an
// install where SQLite is the primary store — which is the default whenever a
// session store exists — a workspace Goal did not survive a read. Worse, the
// usual load → mutate → save cycle then wrote the emptied value back over the
// canonical workspace.json, so an unrelated edit could erase a Goal that had
// been saved correctly.
//
// This is the same class as the earlier Designation and Toolbox gaps: the
// SQLite conversion carries an explicit field list, and anything not on it is
// silently lost.

func missionWorkspace(now time.Time) *workspace.Workspace {
	next := now.Add(time.Hour)
	last := now.Add(-time.Hour)
	return &workspace.Workspace{
		ID:        "ws-mission",
		Name:      "Mission Workspace",
		CreatedAt: now,
		UpdatedAt: now,

		Mission:        "Watch the release notes and summarize what changed",
		MissionEnabled: true,
		AutonomyPolicy: workspace.AutonomyPropose,
		Cadence: &workspace.ScheduleConfig{
			Type:      workspace.ScheduleDaily,
			TimeOfDay: "09:00",
		},
		NotificationPolicy: &workspace.NotificationPolicy{
			MinPriority: "medium",
			OnFindings:  "if_any",
		},
		LastMissionRunAt:        &last,
		NextMissionRunAt:        &next,
		MissionExecutionCount:   7,
		MissionFailureCount:     2,
		MissionCadenceHeartbeat: true,
	}
}

// The whole Goal configuration must survive workspace → session → workspace.
func TestWorkspaceStoreAdapter_MissionRoundTrips(t *testing.T) {
	adapter := &WorkspaceStoreAdapter{}
	now := time.Now().UTC().Round(time.Second)
	input := missionWorkspace(now)

	got := adapter.toAgentWorkspace(adapter.toSessionWorkspace(input))

	if got.Mission != input.Mission {
		t.Fatalf("mission text = %q, want %q", got.Mission, input.Mission)
	}
	if !got.MissionEnabled {
		t.Fatalf("expected the goal to stay enabled")
	}
	if got.AutonomyPolicy != input.AutonomyPolicy {
		t.Fatalf("autonomy policy = %q, want %q", got.AutonomyPolicy, input.AutonomyPolicy)
	}
	if got.Cadence == nil || got.Cadence.Type != workspace.ScheduleDaily || got.Cadence.TimeOfDay != "09:00" {
		t.Fatalf("cadence = %+v, want a daily 09:00 schedule", got.Cadence)
	}
	if got.NotificationPolicy == nil || got.NotificationPolicy.MinPriority != "medium" {
		t.Fatalf("notification policy = %+v, want min priority medium", got.NotificationPolicy)
	}
	if got.MissionCadenceHeartbeat != true {
		t.Fatalf("expected the cadence heartbeat flag to survive")
	}
}

// The counters and timestamps are what stop a cadence from re-firing every
// tick, so losing them is not cosmetic: a workspace that forgot NextMissionRunAt
// would look permanently due.
func TestWorkspaceStoreAdapter_MissionCountersAndTimestampsRoundTrip(t *testing.T) {
	adapter := &WorkspaceStoreAdapter{}
	now := time.Now().UTC().Round(time.Second)
	input := missionWorkspace(now)

	got := adapter.toAgentWorkspace(adapter.toSessionWorkspace(input))

	if got.MissionExecutionCount != 7 || got.MissionFailureCount != 2 {
		t.Fatalf("counters = %d/%d, want 7/2", got.MissionExecutionCount, got.MissionFailureCount)
	}
	if got.NextMissionRunAt == nil || !got.NextMissionRunAt.Equal(*input.NextMissionRunAt) {
		t.Fatalf("next run = %v, want %v", got.NextMissionRunAt, input.NextMissionRunAt)
	}
	if got.LastMissionRunAt == nil || !got.LastMissionRunAt.Equal(*input.LastMissionRunAt) {
		t.Fatalf("last run = %v, want %v", got.LastMissionRunAt, input.LastMissionRunAt)
	}
}

// A workspace with no Goal must round-trip as having no Goal — not as one with
// an empty mission that reads as configured-but-blank.
func TestWorkspaceStoreAdapter_NoMissionStaysAbsent(t *testing.T) {
	adapter := &WorkspaceStoreAdapter{}
	now := time.Now().UTC().Round(time.Second)

	got := adapter.toAgentWorkspace(adapter.toSessionWorkspace(&workspace.Workspace{
		ID: "ws-plain", Name: "Plain", CreatedAt: now, UpdatedAt: now,
	}))

	if got.Mission != "" || got.MissionEnabled || got.Cadence != nil ||
		got.NotificationPolicy != nil || got.NextMissionRunAt != nil {
		t.Fatalf("expected no goal configuration, got %+v", got)
	}
}
