package workspace

import (
	"errors"
	"testing"
	"time"
)

func neutralAssistantDeclaration() *AssistantProgramDeclaration {
	return &AssistantProgramDeclaration{
		SchemaVersion: AssistantProgramSchemaVersion,
		ID:            "project-guide", StationName: "Project Guide Home", StationDescription: "Shared project guidance.",
		DefaultPrimaryName: "Guide", HireTitle: "Hire your guide",
		Roles: []AssistantProgramRoleSpec{
			{ID: "guide", Label: "Guide", Primary: true, Role: "orchestrator", SystemPrompt: "Coordinate confirmed work."},
			{ID: "reviewer", Label: "Reviewer", Role: "specialist", SystemPrompt: "Review bounded questions."},
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
	if state.Hired || len(state.Roster) != 0 || state.Reflection.ScheduleTaskID != "" {
		t.Fatalf("station shell created consequences before hire: %+v", state)
	}
	storedFirst, _ := store.Get(first.ID)
	if storedFirst.GetAssistantProjectLink().StationWorkspaceID != station.ID {
		t.Fatal("project did not persist stable station ID")
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
