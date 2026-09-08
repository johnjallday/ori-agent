package workspace

import (
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func neutralAssistantDeclaration() *AssistantProgramDeclaration {
	return &AssistantProgramDeclaration{
		SchemaVersion: AssistantProgramSchemaVersion,
		ID:            "project-guide", StationName: "Project Guide Home", StationDescription: "Shared project guidance.",
		DefaultPrimaryName: "Guide", HireTitle: "Hire your guide",
		Roles: []AssistantProgramRoleSpec{
			{ID: "guide", Label: "Guide", Scope: AssistantRoleScopeHome, Required: true, Primary: true, Role: "orchestrator", SystemPrompt: "Coordinate confirmed work."},
			{ID: "reviewer", Label: "Reviewer", Scope: AssistantRoleScopeProject, Required: true, Primary: true, Role: "specialist", SystemPrompt: "Review bounded questions."},
		},
		Stages: []AssistantProgramStageSpec{
			{ID: "helper", Label: "Helper", AcceptedCompletionThreshold: 0},
			{ID: "collaborator", Label: "Collaborator", AcceptedCompletionThreshold: 5},
		},
		Reflection: AssistantReflectionConfig{MinimumProjects: 3, CadenceHours: 24, MaxProjects: 12, MaxEventsPerProject: 32, MaxCandidates: 6, MaxEvidence: 8, Rubric: "Find repeated preferences."},
	}
}

func assistantProject(t *testing.T, store Store, name string) *Workspace {
	t.Helper()
	project := NewWorkspace(CreateWorkspaceParams{Name: name})
	project.OwnerUserID = "owner-1"
	project.SetTemplateProvenance(&TemplateProvenance{
		TemplateID: "plugin:neutral:project", PluginOwner: &PluginTemplateOwner{PluginID: "neutral", PluginVersion: "1.0.0", BlueprintID: "project", BlueprintVersion: 2},
		AssistantProgram: neutralAssistantDeclaration(),
	})
	if err := store.Save(project); err != nil {
		t.Fatalf("save project: %v", err)
	}
	return project
}

type assistantPortableMetadataStore struct {
	Store
	portable map[string]*Workspace
}

func (store *assistantPortableMetadataStore) GetFolderWorkspace(id string) (*Workspace, error) {
	workspace, ok := store.portable[id]
	if !ok {
		return nil, fmt.Errorf("portable workspace %s not found", id)
	}
	return cloneWorkspaceForRebind(workspace)
}

func TestAssistantProgramStore_ReadsCanonicalDeclarationWhenPrimaryOmitsProvenance(t *testing.T) {
	primary := NewInMemoryStore()
	project := assistantProject(t, primary, "Portable declaration")
	portable, err := cloneWorkspaceForRebind(project)
	if err != nil {
		t.Fatal(err)
	}
	if err := primary.Update(project.ID, func(current *Workspace) error {
		current.TemplateProvenance = nil
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	store := &assistantPortableMetadataStore{Store: primary, portable: map[string]*Workspace{project.ID: portable}}
	station, created, err := NewAssistantProgramStore(store).EnsureProjectStation(project.ID)
	if err != nil || !created || station.GetAssistantProgramState() == nil {
		t.Fatalf("portable declaration provisioning = (%+v, %v, %v)", station, created, err)
	}
}

func TestAssistantProgramStore_EnsuresGroupHomeBeforeAnyProjectExists(t *testing.T) {
	store := NewInMemoryStore()
	service := NewAssistantProgramStore(store)
	key := AssistantProgramKey{OwnerUserID: "owner-1", PluginID: "neutral", ProgramID: "project-guide"}
	station, created, err := service.EnsureStation(key, neutralAssistantDeclaration())
	if err != nil || !created {
		t.Fatalf("ensure Home = (%+v, %v, %v)", station, created, err)
	}
	if station.Kind != "group" || station.OwnerUserID != "owner-1" {
		t.Fatalf("Home identity = kind %q owner %q", station.Kind, station.OwnerUserID)
	}
	state := station.GetAssistantProgramState()
	if state == nil || len(state.LinkedProjectIDs) != 0 || state.Hired || len(state.Roster) != 0 {
		t.Fatalf("new Home acquired premature consequences: %+v", state)
	}
	again, created, err := service.EnsureStation(key, neutralAssistantDeclaration())
	if err != nil || created || again.ID != station.ID {
		t.Fatalf("repeat ensure Home = (%+v, %v, %v)", again, created, err)
	}
}

func TestAssistantProgramStore_NestsLinkedProjectAsNonCanonicalProjection(t *testing.T) {
	primary := NewInMemoryStore()
	folders, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = folders.Close() })
	store := NewSyncStore(primary, folders)
	project := assistantProject(t, store, "Nested")
	station, _, err := NewAssistantProgramStore(store).EnsureProjectStation(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	linked, err := store.Get(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if linked.ParentID != station.ID || linked.GetAssistantProjectLink() == nil {
		t.Fatalf("linked project projection = parent %q link %#v", linked.ParentID, linked.GetAssistantProjectLink())
	}
	projectPath, _ := folders.GetFolderPath(project.ID)
	stationPath, _ := folders.GetFolderPath(station.ID)
	if !isPathWithin(projectPath, filepath.Join(stationPath, SubWorkspacesDir)) {
		t.Fatalf("project folder %q is not nested under Home %q", projectPath, stationPath)
	}
}

func TestAssistantProgramStore_CreatesOneStationAndStableLinks(t *testing.T) {
	store := NewInMemoryStore()
	service := NewAssistantProgramStore(store)
	first := assistantProject(t, store, "First")
	second := assistantProject(t, store, "Second")

	station, created, err := service.EnsureProjectStation(first.ID)
	if err != nil || !created {
		t.Fatalf("first link = (%+v, %v, %v)", station, created, err)
	}
	stationAgain, createdAgain, err := service.EnsureProjectStation(first.ID)
	if err != nil || createdAgain || stationAgain.ID != station.ID {
		t.Fatalf("repeat link = (%+v, %v, %v)", stationAgain, createdAgain, err)
	}
	secondStation, createdSecond, err := service.EnsureProjectStation(second.ID)
	if err != nil || createdSecond || secondStation.ID != station.ID {
		t.Fatalf("second link = (%+v, %v, %v)", secondStation, createdSecond, err)
	}
	state := secondStation.GetAssistantProgramState()
	if len(state.LinkedProjectIDs) != 2 || !containsString(state.LinkedProjectIDs, first.ID) || !containsString(state.LinkedProjectIDs, second.ID) {
		t.Fatalf("linked projects = %#v", state.LinkedProjectIDs)
	}
	if station.Kind != "group" {
		t.Fatalf("assistant Home kind = %q, want group", station.Kind)
	}
	if state.Hired || len(state.Roster) != 0 || state.Reflection.ScheduleTaskID != "" {
		t.Fatalf("station shell created consequences before hire: %+v", state)
	}
	storedFirst, _ := store.Get(first.ID)
	if storedFirst.GetAssistantProjectLink().StationWorkspaceID != station.ID {
		t.Fatal("project did not persist stable station ID")
	}
}

func TestAssistantProgramStore_StationIdentityDoesNotDependOnDisplaySlug(t *testing.T) {
	store := NewInMemoryStore()
	ordinary := NewWorkspace(CreateWorkspaceParams{Name: "Project Guide Home"})
	if err := store.Save(ordinary); err != nil {
		t.Fatal(err)
	}
	project := assistantProject(t, store, "Song With Shared Title")
	station, _, err := NewAssistantProgramStore(store).EnsureProjectStation(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if station.FolderSlug == ordinary.FolderSlug || station.FolderSlug != assistantStationFolderSlug(station.GetAssistantProgramState().Key) {
		t.Fatalf("station slug %q depends on display title %q", station.FolderSlug, ordinary.FolderSlug)
	}
}

func TestAssistantProgramStore_ScopedProgramDoesNotCopyHomeRosterIntoLaterProject(t *testing.T) {
	store := NewInMemoryStore()
	service := NewAssistantProgramStore(store)
	first := assistantProject(t, store, "First Hired Project")
	station, _, err := service.EnsureProjectStation(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	roster := []AgentInstance{{ID: "producer-id", Name: "June"}, {ID: "engineer-id", Name: "Engineer"}, {ID: "writer-id", Name: "Writer"}}
	if err := store.Update(station.ID, func(current *Workspace) error {
		state := current.GetAssistantProgramState()
		state.Hired = true
		state.PrimaryName = "June"
		state.StageID = "helper"
		state.Level = 1
		current.SetAssistantProgramState(state)
		current.AgentInstances = append([]AgentInstance(nil), roster...)
		return current.SetEntryAgentName("June")
	}); err != nil {
		t.Fatal(err)
	}
	second := assistantProject(t, store, "Later Project")
	if _, _, err := service.EnsureProjectStation(second.ID); err != nil {
		t.Fatal(err)
	}
	linked, _ := store.Get(second.ID)
	instances := linked.GetAgentInstances()
	if len(instances) != 0 || linked.EntryAgentName() != "" {
		t.Fatalf("schema-v2 project inherited shared Home roster = %+v entry=%q", instances, linked.EntryAgentName())
	}
}

func TestAssistantProgramStore_ScopedBindingsPersistAndReviseIndependently(t *testing.T) {
	store := NewInMemoryStore()
	service := NewAssistantProgramStore(store)
	first := assistantProject(t, store, "First Scoped Project")
	second := assistantProject(t, store, "Second Scoped Project")
	station, _, err := service.EnsureProjectStation(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.EnsureProjectStation(second.ID); err != nil {
		t.Fatal(err)
	}
	homeAgent := AgentInstance{ID: "home-manager", Name: "Home Manager"}
	firstAgent := AgentInstance{ID: "first-reviewer", Name: "First Reviewer"}
	secondAgent := AgentInstance{ID: "second-reviewer", Name: "Second Reviewer"}
	for id, instance := range map[string]AgentInstance{station.ID: homeAgent, first.ID: firstAgent, second.ID: secondAgent} {
		if err := store.Update(id, func(current *Workspace) error {
			current.AgentInstances = append(current.GetAgentInstances(), instance)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := service.SetHomeRoleBindings(station.ID, 0, []AssistantRoleBinding{{RoleID: "guide", AgentInstanceID: homeAgent.ID, AgentName: homeAgent.Name}}); err != nil {
		t.Fatalf("set Home binding: %v", err)
	}
	if err := service.SetProjectRoleBindings(first.ID, 0, []AssistantRoleBinding{{RoleID: "reviewer", AgentInstanceID: firstAgent.ID, AgentName: firstAgent.Name}}); err != nil {
		t.Fatalf("set first binding: %v", err)
	}
	if err := service.SetProjectRoleBindings(second.ID, 0, []AssistantRoleBinding{{RoleID: "reviewer", AgentInstanceID: secondAgent.ID, AgentName: secondAgent.Name}}); err != nil {
		t.Fatalf("set second binding: %v", err)
	}
	if err := service.SetProjectRoleBindings(first.ID, 1, nil); err != nil {
		t.Fatalf("revise first binding: %v", err)
	}
	storedHome, _ := store.Get(station.ID)
	storedFirst, _ := store.Get(first.ID)
	storedSecond, _ := store.Get(second.ID)
	if storedHome.GetAssistantProgramState().HomeBindings.StateRevision != 1 || len(storedHome.GetAssistantProgramState().HomeBindings.Bindings) != 1 {
		t.Fatalf("Home bindings = %#v", storedHome.GetAssistantProgramState().HomeBindings)
	}
	if storedFirst.GetAssistantProjectLink().ProjectBindings.StateRevision != 2 || len(storedFirst.GetAssistantProjectLink().ProjectBindings.Bindings) != 0 {
		t.Fatalf("first bindings = %#v", storedFirst.GetAssistantProjectLink().ProjectBindings)
	}
	if storedSecond.GetAssistantProjectLink().ProjectBindings.StateRevision != 1 || storedSecond.GetAssistantProjectLink().ProjectBindings.Bindings[0].AgentInstanceID != secondAgent.ID {
		t.Fatalf("second bindings changed with first = %#v", storedSecond.GetAssistantProjectLink().ProjectBindings)
	}
	if err := service.SetHomeRoleBindings(station.ID, 0, nil); !errors.Is(err, ErrAssistantBindingVersionConflict) {
		t.Fatalf("stale Home binding error = %v", err)
	}
	if err := service.SetProjectRoleBindings(second.ID, 1, []AssistantRoleBinding{{RoleID: "guide", AgentInstanceID: secondAgent.ID, AgentName: secondAgent.Name}}); !errors.Is(err, ErrAssistantBindingInvalid) {
		t.Fatalf("cross-scope binding error = %v", err)
	}
}

func TestAssistantProgramStore_PreservesLegacySharedRosterWithoutInferringScopedBindings(t *testing.T) {
	store := NewInMemoryStore()
	station := NewWorkspace(CreateWorkspaceParams{Name: "Legacy Home"})
	station.Kind = "group"
	station.SetAssistantProgramState(&AssistantProgramState{
		SchemaVersion: AssistantProgramLegacyStateSchemaVersion,
		StateRevision: 4,
		Key:           AssistantProgramKey{OwnerUserID: "owner-1", PluginID: "neutral", ProgramID: "project-guide"},
		Declaration:   neutralAssistantDeclaration(),
		Hired:         true,
		Roster:        []AssistantRoleBinding{{RoleID: "guide", AgentInstanceID: "legacy-agent", AgentName: "Legacy Guide"}},
	})
	if err := store.Save(station); err != nil {
		t.Fatal(err)
	}
	stored, err := store.Get(station.ID)
	if err != nil {
		t.Fatal(err)
	}
	state := stored.GetAssistantProgramState()
	if len(state.Roster) != 1 || state.Roster[0].AgentInstanceID != "legacy-agent" {
		t.Fatalf("legacy roster not readable: %#v", state.Roster)
	}
	if state.HomeBindings.StateRevision != 0 || len(state.HomeBindings.Bindings) != 0 {
		t.Fatalf("legacy roster was inferred into Home scope: %#v", state.HomeBindings)
	}
}

func TestAssistantProgramStore_ConcurrentProjectCreationKeepsOneStation(t *testing.T) {
	store := NewInMemoryStore()
	service := NewAssistantProgramStore(store)
	projects := make([]*Workspace, 12)
	for index := range projects {
		projects[index] = assistantProject(t, store, fmt.Sprintf("Concurrent %d Project", index+1))
		if err := store.Update(projects[index].ID, func(current *Workspace) error {
			current.Name = "Concurrent Song"
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	stationIDs := make(chan string, len(projects))
	errorsSeen := make(chan error, len(projects))
	var wait sync.WaitGroup
	for _, project := range projects {
		wait.Add(1)
		go func(projectID string) {
			defer wait.Done()
			station, _, err := service.EnsureProjectStation(projectID)
			if err != nil {
				errorsSeen <- err
				return
			}
			stationIDs <- station.ID
		}(project.ID)
	}
	wait.Wait()
	close(stationIDs)
	close(errorsSeen)
	for err := range errorsSeen {
		t.Errorf("concurrent ensure: %v", err)
	}
	oneID := ""
	for stationID := range stationIDs {
		if oneID == "" {
			oneID = stationID
		}
		if stationID != oneID {
			t.Fatalf("multiple station IDs: %q and %q", oneID, stationID)
		}
	}
	station, err := store.Get(oneID)
	if err != nil || len(station.GetAssistantProgramState().LinkedProjectIDs) != len(projects) {
		t.Fatalf("station links = (%+v, %v)", station.GetAssistantProgramState(), err)
	}
}

func TestAssistantProgramStore_RenameAndPluginDisablePreserveIdentity(t *testing.T) {
	store := NewInMemoryStore()
	service := NewAssistantProgramStore(store)
	project := assistantProject(t, store, "Before")
	station, _, err := service.EnsureProjectStation(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(project.ID, func(current *Workspace) error { current.Name = "After"; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := store.Update(station.ID, func(current *Workspace) error { current.Name = "Renamed Home"; return nil }); err != nil {
		t.Fatal(err)
	}
	found, err := service.FindStation(AssistantProgramKey{OwnerUserID: "owner-1", PluginID: "neutral", ProgramID: "project-guide"})
	if err != nil || found.ID != station.ID {
		t.Fatalf("find after rename = (%+v, %v)", found, err)
	}
	if err := service.SetPluginAvailable(station.ID, false); err != nil {
		t.Fatal(err)
	}
	disabled, _ := store.Get(station.ID)
	if disabled.GetAssistantProgramState().PluginAvailable {
		t.Fatal("plugin disable state not persisted")
	}
	projects, err := service.LinkedProjects(station.ID)
	if err != nil || len(projects) != 1 || projects[0].Name != "After" {
		t.Fatalf("linked projects remain readable = (%+v, %v)", projects, err)
	}
}

func TestAssistantProgramState_RoundTripAndDefensiveCopies(t *testing.T) {
	ws := NewWorkspace(CreateWorkspaceParams{Name: "Station"})
	now := time.Now().UTC().Truncate(time.Second)
	ws.SetAssistantProgramState(&AssistantProgramState{
		SchemaVersion: 1, StateRevision: 4,
		Key:         AssistantProgramKey{OwnerUserID: "owner", PluginID: "plugin", ProgramID: "program"},
		Declaration: neutralAssistantDeclaration(), LinkedProjectIDs: []string{"b", "a", "a"},
		StageID: "helper", Level: 1, StageEnteredAt: map[string]time.Time{"helper": now},
	})
	ws.SetAssistantProjectLink(&AssistantProjectLink{SchemaVersion: 1, StationWorkspaceID: "station", LinkedAt: now, StateRevision: 1})
	encoded, err := ws.ToJSON()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := FromJSON(encoded)
	if err != nil {
		t.Fatal(err)
	}
	state := decoded.GetAssistantProgramState()
	state.Declaration.Roles[0].Label = "Changed"
	state.StageEnteredAt["helper"] = time.Time{}
	if decoded.GetAssistantProgramState().Declaration.Roles[0].Label != "Guide" || decoded.GetAssistantProgramState().StageEnteredAt["helper"].IsZero() {
		t.Fatal("assistant program getter exposed workspace-owned references")
	}
	if decoded.GetAssistantProjectLink().StationWorkspaceID != "station" {
		t.Fatal("project link did not round trip")
	}
}

func TestSubscribeAssistantProgressionAwardsCanonicalCompletionOnce(t *testing.T) {
	store := NewInMemoryStore()
	project := assistantProject(t, store, "Event Song")
	service := NewAssistantProgramStore(store)
	station, _, err := service.EnsureProjectStation(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(station.ID, func(current *Workspace) error {
		state := current.GetAssistantProgramState()
		state.Hired = true
		state.StageID = "helper"
		state.Level = 1
		current.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Update(project.ID, func(current *Workspace) error {
		return current.AddTask(Task{ID: "done", Description: "Accepted work", Status: TaskStatusCompleted})
	}); err != nil {
		t.Fatal(err)
	}
	bus := DefaultEventBus()
	defer bus.Shutdown()
	SubscribeAssistantProgression(bus, store)
	bus.Publish(Event{Type: EventTaskCompleted, WorkspaceID: project.ID, Data: map[string]any{"task_id": "done"}})
	time.Sleep(20 * time.Millisecond)
	current, _ := store.Get(station.ID)
	if current.GetAssistantProgramState().AcceptedCompletions != 0 {
		t.Fatal("execution completion advanced progression before user acceptance")
	}
	event := Event{Type: EventTaskCompleted, WorkspaceID: project.ID, Data: map[string]any{"task_id": "done", "accepted": true}}
	bus.Publish(event)
	bus.Publish(event)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		current, _ := store.Get(station.ID)
		if current.GetAssistantProgramState().AcceptedCompletions == 1 {
			time.Sleep(20 * time.Millisecond)
			current, _ = store.Get(station.ID)
			if current.GetAssistantProgramState().AcceptedCompletions != 1 {
				t.Fatal("duplicate event awarded twice")
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("completion event was not awarded")
}

func TestAssistantProgramStore_RecordAcceptedCompletionIsIdempotentAndPromotes(t *testing.T) {
	store := NewInMemoryStore()
	project := assistantProject(t, store, "Song A")
	service := NewAssistantProgramStore(store)
	station, _, err := service.EnsureProjectStation(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(station.ID, func(current *Workspace) error {
		state := current.GetAssistantProgramState()
		state.Hired = true
		state.StageID = "helper"
		state.Level = 1
		state.Declaration.Stages[1].AcceptedCompletionThreshold = 2
		current.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Update(project.ID, func(current *Workspace) error {
		if err := current.AddTask(Task{ID: "one", Description: "First", Status: TaskStatusCompleted}); err != nil {
			return err
		}
		return current.AddTask(Task{ID: "two", Description: "Second", Status: TaskStatusCompleted})
	}); err != nil {
		t.Fatal(err)
	}

	state, promoted, err := service.RecordAcceptedCompletion(project.ID, "task:one")
	if err != nil || promoted || state.AcceptedCompletions != 1 || state.StageID != "helper" {
		t.Fatalf("first completion state=%+v promoted=%v err=%v", state, promoted, err)
	}
	state, promoted, err = service.RecordAcceptedCompletion(project.ID, "task:one")
	if err != nil || promoted || state.AcceptedCompletions != 1 {
		t.Fatalf("retry was not idempotent: state=%+v promoted=%v err=%v", state, promoted, err)
	}
	state, promoted, err = service.RecordAcceptedCompletion(project.ID, "task:two")
	if err != nil || !promoted || state.AcceptedCompletions != 2 || state.StageID != "collaborator" || state.Level != 2 {
		t.Fatalf("promotion state=%+v promoted=%v err=%v", state, promoted, err)
	}
	if state.PromotionReceipt == nil || state.PromotionReceipt.StageID != "collaborator" || state.PromotionReceipt.AcknowledgedAt != nil {
		t.Fatalf("promotion receipt = %+v", state.PromotionReceipt)
	}
}

func TestAssistantProgramStore_CompletionLedgerIsBoundedWithoutReplayAfterEviction(t *testing.T) {
	store := NewInMemoryStore()
	service := NewAssistantProgramStore(store)
	project := assistantProject(t, store, "Bounded Ledger")
	station, _, err := service.EnsureProjectStation(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(station.ID, func(current *Workspace) error {
		state := current.GetAssistantProgramState()
		state.Hired = true
		state.StageID = "helper"
		state.Level = 1
		current.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Update(project.ID, func(current *Workspace) error {
		for index := 0; index <= assistantCompletionReceiptLimit; index++ {
			if err := current.AddTask(Task{ID: fmt.Sprintf("task-%03d", index), Description: "Accepted", Status: TaskStatusCompleted}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for index := 0; index <= assistantCompletionReceiptLimit; index++ {
		if _, _, err := service.RecordAcceptedCompletion(project.ID, fmt.Sprintf("task:task-%03d", index)); err != nil {
			t.Fatal(err)
		}
	}
	current, _ := store.Get(station.ID)
	state := current.GetAssistantProgramState()
	if len(state.CompletionReceipts) != assistantCompletionReceiptLimit || state.AcceptedCompletions != assistantCompletionReceiptLimit+1 {
		t.Fatalf("bounded state = receipts %d completions %d", len(state.CompletionReceipts), state.AcceptedCompletions)
	}
	if _, _, err := service.RecordAcceptedCompletion(project.ID, "task:task-000"); err != nil {
		t.Fatal(err)
	}
	current, _ = store.Get(station.ID)
	if got := current.GetAssistantProgramState().AcceptedCompletions; got != assistantCompletionReceiptLimit+1 {
		t.Fatalf("evicted receipt replay incremented completions to %d", got)
	}
}

func TestAssistantProgramStore_OrdinaryWorkspaceIsNoOp(t *testing.T) {
	store := NewInMemoryStore()
	plain := NewWorkspace(CreateWorkspaceParams{Name: "Plain"})
	if err := store.Save(plain); err != nil {
		t.Fatal(err)
	}
	_, _, err := NewAssistantProgramStore(store).EnsureProjectStation(plain.ID)
	if !errors.Is(err, ErrAssistantProgramUnavailable) {
		t.Fatalf("error = %v", err)
	}
}
