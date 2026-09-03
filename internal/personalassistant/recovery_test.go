package personalassistant

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/personalhq"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/types"
)

type recoveryProfilesStub struct{ profiles []RecoveryProfile }

func (s recoveryProfilesStub) PersonalAssistantRecoveryProfiles() []RecoveryProfile {
	out := make([]RecoveryProfile, len(s.profiles))
	copy(out, s.profiles)
	return out
}

type recoveryWorkspacesStub struct {
	workspaces []RecoveryWorkspace
	err        error
}

func (s recoveryWorkspacesStub) PersonalAssistantRecoveryWorkspaces(context.Context) ([]RecoveryWorkspace, error) {
	out := make([]RecoveryWorkspace, len(s.workspaces))
	copy(out, s.workspaces)
	return out, s.err
}

type recoveryHQStub struct {
	status *personalhq.Status
	err    error
}

func (s recoveryHQStub) Status(context.Context, string) (*personalhq.Status, error) {
	return s.status, s.err
}

type recoveryBriefStub struct {
	config *dailybrief.Config
	err    error
}

func (s recoveryBriefStub) GetConfig(context.Context, string) (*dailybrief.Config, error) {
	return s.config, s.err
}

func newRecoveryStore(t *testing.T) *SQLiteStore {
	t.Helper()
	db, err := database.Open(context.Background(), &database.Config{InMemory: true, WALMode: false})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewSQLiteStore(db)
}

func recoveryFixture(t *testing.T) (*RecoveryCoordinator, *SQLiteStore, *recoveryProfilesStub, *recoveryWorkspacesStub, *recoveryHQStub, *recoveryBriefStub) {
	t.Helper()
	createdAt := time.Date(2026, time.September, 3, 13, 9, 38, 0, time.UTC)
	profiles := &recoveryProfilesStub{profiles: []RecoveryProfile{{
		Name: "Assistant", AssistantID: "assistant-a", HireRequestID: "hire-a",
		Role: types.RoleOrchestrator, Appearance: types.NewAgentAppearance(), CreatedAt: createdAt,
	}}}
	workspace := &session.Workspace{
		ID: "hq-a", OwnerUserID: "local",
		AgentInstances: []session.AgentInstance{{
			ID: "instance-a", Name: "Assistant", EntryPoint: true,
		}},
		SharedData: map[string]any{"personal_assistant_presentation": map[string]any{
			"assistant_id": "assistant-a", "request_id": "hq-request-a", "version": 1,
		}},
	}
	workspaces := &recoveryWorkspacesStub{workspaces: []RecoveryWorkspace{{
		ID: "hq-a", OwnerUserID: "local", AssistantID: "assistant-a", HQRequestID: "hq-request-a",
		PresentationValid: true, EntryAgents: []RecoveryEntryAgent{{ID: "instance-a", Name: "Assistant"}},
	}}}
	hq := &recoveryHQStub{status: &personalhq.Status{
		UserID: "local", WorkspaceID: "hq-a", Workspace: workspace, Valid: true,
		EntryAgentInstanceID: "instance-a", EntryAgentName: "Assistant",
	}}
	briefs := &recoveryBriefStub{config: &dailybrief.Config{
		WorkspaceID: "hq-a", UserID: "local", Timezone: "UTC",
		ScheduleDays: []string{"mon"}, ScheduleTime: "08:00", ConfigRevision: 1,
	}}
	store := newRecoveryStore(t)
	coordinator := NewRecoveryCoordinator(store, profiles, workspaces, hq, briefs)
	return coordinator, store, profiles, workspaces, hq, briefs
}

func TestRecoveryInspectFindsExactOrphanWithoutMutation(t *testing.T) {
	coordinator, store, _, _, _, _ := recoveryFixture(t)

	candidate, err := coordinator.Inspect(context.Background(), "local")
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if candidate.AssistantID != "assistant-a" || candidate.HQWorkspaceID != "hq-a" ||
		candidate.HQEntryAgentInstanceID != "instance-a" || candidate.Status != StatusPaused {
		t.Fatalf("candidate = %#v", candidate)
	}
	if candidate.HireRequestID != "hire-a" || candidate.HQRequestID != "hq-request-a" {
		t.Fatalf("operation provenance missing: %#v", candidate)
	}
	if _, err := store.GetState(context.Background(), "local"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("inspection mutated relationship state: %v", err)
	}
}

func TestRecoveryRepairRecreatesOnlyPausedRelationship(t *testing.T) {
	coordinator, store, _, _, _, _ := recoveryFixture(t)

	state, err := coordinator.Repair(context.Background(), "local", 0)
	if err != nil {
		t.Fatalf("Repair: %v", err)
	}
	if state.Status != StatusPaused || state.StateVersion != 1 {
		t.Fatalf("state = %#v", state)
	}
	if state.AssistantID != "assistant-a" || state.GlobalAgentProfileName != "Assistant" ||
		state.HQWorkspaceID != "hq-a" || state.HQEntryAgentInstanceID != "instance-a" {
		t.Fatalf("stable identity changed: %#v", state)
	}
	if state.Mandate != "" || len(state.FocusAreas) != 0 {
		t.Fatalf("recovery invented a working agreement: %#v", state)
	}
	persisted, err := store.GetState(context.Background(), "local")
	if err != nil || persisted.AssistantID != state.AssistantID {
		t.Fatalf("persisted relationship = %#v, %v", persisted, err)
	}
	if _, err := coordinator.Repair(context.Background(), "local", 0); !errors.Is(err, ErrConflict) {
		t.Fatalf("second repair error = %v; want conflict", err)
	}
}

func TestRecoveryRepairRestoresProfileOnlyHireAsAwaitingHQ(t *testing.T) {
	coordinator, _, _, workspaces, hq, briefs := recoveryFixture(t)
	workspaces.workspaces = nil
	hq.status = &personalhq.Status{UserID: "local"}
	briefs.config = nil

	state, err := coordinator.Repair(context.Background(), "local", 0)
	if err != nil {
		t.Fatalf("Repair: %v", err)
	}
	if state.Status != StatusAwaitingHQ || state.HQWorkspaceID != "" || state.HQEntryAgentInstanceID != "" {
		t.Fatalf("pre-HQ recovery = %#v", state)
	}
}

func TestRecoveryInspectBlocksWorkspaceAmbiguityAndIdentityMismatch(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*recoveryWorkspacesStub, *recoveryHQStub, *recoveryBriefStub)
	}{
		{
			name: "multiple marked hq workspaces",
			mutate: func(workspaces *recoveryWorkspacesStub, _ *recoveryHQStub, _ *recoveryBriefStub) {
				workspaces.workspaces = append(workspaces.workspaces, RecoveryWorkspace{
					ID: "hq-b", OwnerUserID: "local", AssistantID: "assistant-a", HQRequestID: "hq-request-b",
					PresentationValid: true, EntryAgents: []RecoveryEntryAgent{{ID: "instance-b", Name: "Assistant"}},
				})
			},
		},
		{
			name: "malformed hq presentation",
			mutate: func(workspaces *recoveryWorkspacesStub, _ *recoveryHQStub, _ *recoveryBriefStub) {
				workspaces.workspaces[0].PresentationValid = false
			},
		},
		{
			name: "workspace evidence has foreign owner",
			mutate: func(workspaces *recoveryWorkspacesStub, _ *recoveryHQStub, _ *recoveryBriefStub) {
				workspaces.workspaces[0].OwnerUserID = "other-user"
			},
		},
		{
			name: "workspace evidence has foreign assistant",
			mutate: func(workspaces *recoveryWorkspacesStub, _ *recoveryHQStub, _ *recoveryBriefStub) {
				workspaces.workspaces[0].AssistantID = "assistant-b"
			},
		},
		{
			name: "hq is not designated",
			mutate: func(_ *recoveryWorkspacesStub, hq *recoveryHQStub, _ *recoveryBriefStub) {
				hq.status = &personalhq.Status{UserID: "local"}
			},
		},
		{
			name: "designation points to another workspace",
			mutate: func(_ *recoveryWorkspacesStub, hq *recoveryHQStub, _ *recoveryBriefStub) {
				hq.status.WorkspaceID = "hq-b"
			},
		},
		{
			name: "designation has foreign user",
			mutate: func(_ *recoveryWorkspacesStub, hq *recoveryHQStub, _ *recoveryBriefStub) {
				hq.status.UserID = "other-user"
			},
		},
		{
			name: "designation entry instance differs",
			mutate: func(_ *recoveryWorkspacesStub, hq *recoveryHQStub, _ *recoveryBriefStub) {
				hq.status.EntryAgentInstanceID = "instance-b"
			},
		},
		{
			name: "daily brief has foreign user",
			mutate: func(_ *recoveryWorkspacesStub, _ *recoveryHQStub, briefs *recoveryBriefStub) {
				briefs.config.UserID = "other-user"
			},
		},
		{
			name: "daily brief belongs to another workspace",
			mutate: func(_ *recoveryWorkspacesStub, _ *recoveryHQStub, briefs *recoveryBriefStub) {
				briefs.config.WorkspaceID = "hq-b"
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			coordinator, store, _, workspaces, hq, briefs := recoveryFixture(t)
			test.mutate(workspaces, hq, briefs)
			if _, err := coordinator.Inspect(context.Background(), "local"); !errors.Is(err, ErrRepairNeeded) {
				t.Fatalf("Inspect error = %v; want ErrRepairNeeded", err)
			}
			if _, err := store.GetState(context.Background(), "local"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("blocked inspection mutated state: %v", err)
			}
		})
	}
}

func TestRecoveryProfileProvenanceBlocksPartialOrDuplicateMarkers(t *testing.T) {
	tests := []struct {
		name    string
		tags    []string
		marked  bool
		validID bool
	}{
		{name: "unmarked", tags: []string{"assistant"}, marked: false},
		{name: "exact pair", tags: []string{
			ProfileAssistantMarker("assistant-a"), ProfileHireMarker("hire-a"),
		}, marked: true, validID: true},
		{name: "partial pair", tags: []string{ProfileAssistantMarker("assistant-a")}, marked: true},
		{name: "empty marker", tags: []string{ProfileAssistantMarker(""), ProfileHireMarker("hire-a")}, marked: true},
		{name: "duplicate assistant identities", tags: []string{
			ProfileAssistantMarker("assistant-a"), ProfileAssistantMarker("assistant-b"), ProfileHireMarker("hire-a"),
		}, marked: true},
		{name: "duplicate hire identities", tags: []string{
			ProfileAssistantMarker("assistant-a"), ProfileHireMarker("hire-a"), ProfileHireMarker("hire-b"),
		}, marked: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			provenance, marked := recoveryProfileProvenance("Assistant", test.tags)
			if marked != test.marked {
				t.Fatalf("marked = %v; want %v", marked, test.marked)
			}
			validID := provenance.AssistantID != "" && provenance.HireRequestID != ""
			if validID != test.validID {
				t.Fatalf("provenance = %#v; valid identity want %v", provenance, test.validID)
			}
		})
	}
}

func TestRecoveryInspectDistinguishesFreshFromBlockedEvidence(t *testing.T) {
	t.Run("fresh", func(t *testing.T) {
		coordinator, _, profiles, workspaces, _, _ := recoveryFixture(t)
		profiles.profiles = nil
		workspaces.workspaces = nil
		if _, err := coordinator.Inspect(context.Background(), "local"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("Inspect error = %v; want ErrNotFound", err)
		}
	})

	tests := []struct {
		name   string
		mutate func(*recoveryProfilesStub, *recoveryHQStub, *recoveryBriefStub)
	}{
		{
			name: "multiple marked profiles",
			mutate: func(profiles *recoveryProfilesStub, _ *recoveryHQStub, _ *recoveryBriefStub) {
				profiles.profiles = append(profiles.profiles, RecoveryProfile{
					Name: "Other", AssistantID: "assistant-b", HireRequestID: "hire-b",
					Role: types.RoleOrchestrator,
				})
			},
		},
		{
			name: "partial profile marker",
			mutate: func(profiles *recoveryProfilesStub, _ *recoveryHQStub, _ *recoveryBriefStub) {
				profiles.profiles[0].HireRequestID = ""
			},
		},
		{
			name: "wrong profile role",
			mutate: func(profiles *recoveryProfilesStub, _ *recoveryHQStub, _ *recoveryBriefStub) {
				profiles.profiles[0].Role = types.RoleResearcher
			},
		},
		{
			name: "workspace assistant mismatch",
			mutate: func(_ *recoveryProfilesStub, hq *recoveryHQStub, _ *recoveryBriefStub) {
				hq.status.Workspace.SharedData["personal_assistant_presentation"] = map[string]any{
					"assistant_id": "foreign", "request_id": "hq-request-a",
				}
			},
		},
		{
			name: "entry agent mismatch",
			mutate: func(_ *recoveryProfilesStub, hq *recoveryHQStub, _ *recoveryBriefStub) {
				hq.status.Workspace.AgentInstances[0].Name = "Someone else"
			},
		},
		{
			name: "foreign workspace",
			mutate: func(_ *recoveryProfilesStub, hq *recoveryHQStub, _ *recoveryBriefStub) {
				hq.status.Workspace.OwnerUserID = "other-user"
			},
		},
		{
			name: "daily brief missing",
			mutate: func(_ *recoveryProfilesStub, _ *recoveryHQStub, briefs *recoveryBriefStub) {
				briefs.config = nil
				briefs.err = dailybrief.ErrConfigNotFound
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			coordinator, store, profiles, _, hq, briefs := recoveryFixture(t)
			test.mutate(profiles, hq, briefs)
			if _, err := coordinator.Inspect(context.Background(), "local"); !errors.Is(err, ErrRepairNeeded) {
				t.Fatalf("Inspect error = %v; want ErrRepairNeeded", err)
			}
			if _, err := store.GetState(context.Background(), "local"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("blocked inspection mutated state: %v", err)
			}
		})
	}
}

type recoveryInspectorStub struct {
	candidate *RecoveryCandidate
	err       error
	reads     int
}

func (s *recoveryInspectorStub) Inspect(context.Context, string) (*RecoveryCandidate, error) {
	s.reads++
	return s.candidate.clone(), s.err
}

func TestServiceGetProjectsExplicitRecoveryWithoutWriting(t *testing.T) {
	store := &readTrackingStore{err: ErrNotFound}
	inspector := &recoveryInspectorStub{candidate: &RecoveryCandidate{
		AssistantID: "assistant-a", DisplayName: "Assistant",
		Appearance: types.NewAgentAppearance(), GlobalAgentProfileName: "Assistant",
		HQWorkspaceID: "hq-a", HQEntryAgentInstanceID: "instance-a",
	}}
	service := NewService(store, nil, nil, fakeModelReader{
		availability: SourceAvailability{Available: true, Status: AvailabilityAvailable},
	}).WithRecoveryInspector(inspector)

	projection, err := service.Get(context.Background(), "local")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if projection.State != APIStateRepairNeeded || projection.NextAction != NextActionRepair ||
		projection.RepairStep != RepairRelationshipRecovery {
		t.Fatalf("projection = %#v", projection)
	}
	if projection.AssistantID != "assistant-a" || projection.HQWorkspaceID != "hq-a" {
		t.Fatalf("validated recovery identity missing: %#v", projection)
	}
	if store.mutationHit || inspector.reads != 1 {
		t.Fatalf("recovery read mutated=%v reads=%d", store.mutationHit, inspector.reads)
	}
}

func TestServiceGetBlocksContradictoryRecoveryEvidence(t *testing.T) {
	store := &readTrackingStore{err: ErrNotFound}
	service := NewService(store, nil, nil, nil).WithRecoveryInspector(
		&recoveryInspectorStub{err: ErrRepairNeeded},
	)
	projection, err := service.Get(context.Background(), "local")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if projection.State != APIStateRepairNeeded || projection.RepairStep != RepairRelationshipRecoveryBlocked {
		t.Fatalf("projection = %#v", projection)
	}
	if projection.AssistantID != "" || projection.HQWorkspaceID != "" {
		t.Fatalf("blocked recovery leaked identity: %#v", projection)
	}
}
