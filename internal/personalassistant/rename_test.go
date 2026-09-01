package personalassistant

import (
	"context"
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/personalhq"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type renameHQReader struct {
	workspaces workspace.Store
}

func (r renameHQReader) Status(_ context.Context, userID string) (*personalhq.Status, error) {
	ws, err := r.workspaces.Get("hq-local")
	if err != nil {
		return nil, err
	}
	converted := &session.Workspace{ID: ws.ID, OwnerUserID: ws.OwnerUserID, FolderSlug: ws.FolderSlug}
	for _, instance := range ws.AgentInstances {
		converted.AgentInstances = append(converted.AgentInstances, session.AgentInstance{
			ID: instance.ID, Name: instance.Name, EntryPoint: instance.EntryPoint,
		})
	}
	return &personalhq.Status{UserID: userID, WorkspaceID: ws.ID, Workspace: converted, Valid: true}, nil
}

type renameSessions struct {
	oldName  string
	newName  string
	calls    int
	failNext bool
	renamed  bool
}

func (s *renameSessions) RenameSessionsByAgent(_ context.Context, oldName, newName string) (int, error) {
	s.oldName, s.newName = oldName, newName
	s.calls++
	if s.failNext {
		s.failNext = false
		return 0, errors.New("simulated session rename failure")
	}
	if s.renamed {
		return 0, nil
	}
	s.renamed = true
	return 2, nil
}

type renameStateStore struct {
	Store
	failNextStep RenameStep
	failFinal    bool
}

func (s *renameStateStore) UpdateState(ctx context.Context, state *State, expectedVersion int64) (*State, error) {
	if s.failFinal && state.RenameStep == RenameNone && state.RenameFromName == "" {
		s.failFinal = false
		return nil, errors.New("simulated final rename persistence failure")
	}
	if s.failNextStep != RenameNone && state.RenameStep == s.failNextStep {
		s.failNextStep = RenameNone
		return nil, errors.New("simulated journal persistence failure")
	}
	return s.Store.UpdateState(ctx, state, expectedVersion)
}

type renameWorkspaces struct {
	*workspace.InMemoryStore
	failNextSave bool
}

func (s *renameWorkspaces) Save(ws *workspace.Workspace) error {
	if s.failNextSave {
		s.failNextSave = false
		return errors.New("simulated workspace save failure")
	}
	return s.InMemoryStore.Save(ws)
}

type renameProfiles struct {
	agents   map[string]*agent.Agent
	failNext bool
}

func (s *renameProfiles) ListAgents() []string {
	out := make([]string, 0, len(s.agents))
	for name := range s.agents {
		out = append(out, name)
	}
	return out
}
func (s *renameProfiles) GetAgent(name string) (*agent.Agent, bool) {
	ag, ok := s.agents[name]
	return ag, ok
}
func (s *renameProfiles) RenameAgent(oldName, newName string) error {
	if s.failNext {
		s.failNext = false
		return errors.New("simulated crash boundary")
	}
	ag, ok := s.agents[oldName]
	if !ok {
		return errors.New("source missing")
	}
	if _, exists := s.agents[newName]; exists {
		return errors.New("destination exists")
	}
	delete(s.agents, oldName)
	s.agents[newName] = ag
	return nil
}

func newRenameFixture(t *testing.T) (*RenameCoordinator, *renameStateStore, *renameWorkspaces, *renameProfiles, *renameSessions) {
	t.Helper()
	ctx := context.Background()
	sqliteStore, _ := newTestStore(t)
	store := &renameStateStore{Store: sqliteStore}
	state := activeTestState("local", "assistant-stable")
	state.FirstAssignmentStatus = FirstAssignmentCompleted
	if _, err := store.CreateState(ctx, state); err != nil {
		t.Fatal(err)
	}
	workspaces := &renameWorkspaces{InMemoryStore: workspace.NewInMemoryStore()}
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Personal HQ"})
	ws.ID, ws.FolderSlug, ws.OwnerUserID = "hq-local", "personal-hq", "local"
	ws.AgentInstances = []workspace.AgentInstance{{ID: "instance-local", Name: "Ada", EntryPoint: true}}
	if err := workspaces.Save(ws); err != nil {
		t.Fatal(err)
	}
	hq := renameHQReader{workspaces: workspaces}
	briefs := &continuityBriefs{config: validBriefConfigForRename(ws.ID)}
	read := NewService(store, hq, briefs,
		fakeModelReader{availability: SourceAvailability{Available: true, Status: AvailabilityAvailable}})
	continuity := NewContinuityService(store, hq, briefs, read)
	profiles := &renameProfiles{agents: map[string]*agent.Agent{"Ada": {}}}
	sessions := &renameSessions{}
	coordinator := NewRenameCoordinator(continuity, profiles, workspaces)
	coordinator.SetSessionRenamer(sessions)
	return coordinator, store, workspaces, profiles, sessions
}

func validBriefConfigForRename(workspaceID string) dailybrief.Config {
	return dailybrief.Config{WorkspaceID: workspaceID, UserID: "local", Timezone: "UTC", ScheduleDays: []string{"mon"}, ScheduleTime: "08:00", ScheduleEnabled: true, Scope: dailybrief.ScopeAll, ConfigRevision: 1}
}

func TestRenameCoordinator_RetryKeepsStableIdentityAndHistory(t *testing.T) {
	coordinator, store, workspaces, profiles, sessions := newRenameFixture(t)
	profiles.failNext = true
	if _, err := coordinator.Rename(context.Background(), "local", "Atlas", 1); err == nil {
		t.Fatal("expected simulated profile failure")
	}
	journal, _ := store.GetState(context.Background(), "local")
	if journal.RenameStep != RenameProfilePending || journal.RenameFromName != "Ada" || journal.RenameToName != "Atlas" {
		t.Fatalf("rename journal=%+v", journal)
	}
	result, err := coordinator.Rename(context.Background(), "local", "Atlas", journal.StateVersion)
	if err != nil {
		t.Fatalf("retry rename: %v", err)
	}
	if result.DisplayName != "Atlas" || result.AssistantID != "assistant-stable" || result.HQAgentInstanceID != "instance-local" || result.FirstAssignment != FirstAssignmentCompleted {
		t.Fatalf("identity/history changed: %+v", result)
	}
	ws, _ := workspaces.Get("hq-local")
	if ws.AgentInstances[0].Name != "Atlas" {
		t.Fatalf("HQ entry was not renamed: %+v", ws.AgentInstances)
	}
	if _, ok := profiles.agents["Atlas"]; !ok || len(profiles.agents) != 1 {
		t.Fatalf("global profile was duplicated: %+v", profiles.agents)
	}
	if sessions.calls != 1 || sessions.oldName != "Ada" || sessions.newName != "Atlas" {
		t.Fatalf("sessions were not preserved under the new name: %+v", sessions)
	}
}

func TestRenameCoordinator_ResumesAfterEveryDurableBoundary(t *testing.T) {
	for _, tc := range []struct {
		name      string
		journal   RenameStep
		failHQ    bool
		failSess  bool
		failFinal bool
	}{
		{name: "profile moved before journal", journal: RenameHQPending},
		{name: "HQ save refused before mutation", failHQ: true},
		{name: "HQ moved before journal", journal: RenameSessionsPending},
		{name: "session update refused before mutation", failSess: true},
		{name: "sessions moved before journal", journal: RenameStatePending},
		{name: "final presentation write refused", failFinal: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			coordinator, store, workspaces, _, sessions := newRenameFixture(t)
			store.failNextStep = tc.journal
			store.failFinal = tc.failFinal
			workspaces.failNextSave = tc.failHQ
			sessions.failNext = tc.failSess
			if _, err := coordinator.Rename(context.Background(), "local", "Atlas", 1); err == nil {
				t.Fatal("expected injected rename failure")
			}
			journal, err := store.GetState(context.Background(), "local")
			if err != nil {
				t.Fatal(err)
			}
			result, err := coordinator.Rename(context.Background(), "local", "Atlas", journal.StateVersion)
			if err != nil || result.DisplayName != "Atlas" || result.AssistantID != "assistant-stable" {
				t.Fatalf("retry result=%+v err=%v journal=%+v", result, err, journal)
			}
			if len(sessions.oldName) == 0 || sessions.newName != "Atlas" {
				t.Fatalf("session continuity missing: %+v", sessions)
			}
		})
	}
}

func TestRenameCoordinator_RejectsOriCollisionAndAttachedProfile(t *testing.T) {
	coordinator, _, workspaces, _, _ := newRenameFixture(t)
	if _, err := coordinator.Rename(context.Background(), "local", "Ori", 1); !errors.Is(err, ErrValidation) {
		t.Fatalf("reserved name error=%v", err)
	}
	extra := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Other"})
	extra.ID, extra.OwnerUserID = "other", "local"
	extra.AgentInstances = []workspace.AgentInstance{{ID: "other-instance", Name: "Ada"}}
	if err := workspaces.Save(extra); err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.Rename(context.Background(), "local", "Atlas", 1); !errors.Is(err, ErrConflict) {
		t.Fatalf("attached profile error=%v", err)
	}
}

func TestRenameCoordinator_AllowsCaseOnlyRename(t *testing.T) {
	coordinator, _, _, profiles, _ := newRenameFixture(t)
	result, err := coordinator.Rename(context.Background(), "local", "ADA", 1)
	if err != nil || result.DisplayName != "ADA" {
		t.Fatalf("case rename result=%+v err=%v", result, err)
	}
	if _, ok := profiles.agents["ADA"]; !ok {
		t.Fatalf("case-only profile rename missing: %+v", profiles.agents)
	}
}
