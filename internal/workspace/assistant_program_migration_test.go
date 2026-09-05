package workspace

import (
	"errors"
	"testing"
	"time"
)

func legacyMigrationFixture(t *testing.T) (*InMemoryStore, *AssistantProgramStore, *Workspace, *Workspace) {
	t.Helper()
	store := NewInMemoryStore()
	programs := NewAssistantProgramStore(store)
	target := neutralAssistantDeclaration()
	key := AssistantProgramKey{OwnerUserID: "local", PluginID: "neutral", ProgramID: target.ID}.Normalize()
	station := NewWorkspace(CreateWorkspaceParams{Name: "Plain legacy station"})
	station.Kind = "workspace"
	station.OwnerUserID = "local"
	station.AgentInstances = []AgentInstance{{ID: "legacy-producer-instance", Name: "Legacy Producer"}}
	legacyDeclaration := CloneAssistantProgramDeclaration(target)
	legacyDeclaration.SchemaVersion = AssistantProgramLegacySchemaVersion
	station.SetAssistantProgramState(&AssistantProgramState{
		SchemaVersion: AssistantProgramLegacyStateSchemaVersion, StateRevision: 7,
		Key: key, Declaration: legacyDeclaration, Hired: true, PrimaryName: "Legacy Producer",
		Roster: []AssistantRoleBinding{{RoleID: "producer", AgentInstanceID: "legacy-producer-instance", AgentName: "Legacy Producer"}},
	})
	if err := store.Save(station); err != nil {
		t.Fatal(err)
	}
	project := NewWorkspace(CreateWorkspaceParams{Name: "Compatible project"})
	project.OwnerUserID = "local"
	project.AgentInstances = []AgentInstance{{ID: "legacy-producer-instance", Name: "Legacy Producer"}}
	project.SetTemplateProvenance(&TemplateProvenance{
		TemplateID:       "plugin:neutral:project",
		PluginOwner:      &PluginTemplateOwner{PluginID: "neutral", PluginVersion: "2.0.0", BlueprintID: "project", BlueprintVersion: 2},
		AssistantProgram: target,
	})
	project.SetAssistantProjectLink(&AssistantProjectLink{
		ID: AssistantProjectLinkID(station.ID, project.ID), SchemaVersion: AssistantProjectLinkLegacySchemaVersion,
		StationWorkspaceID: station.ID, Key: key, DeclarationVersion: 1, LinkedAt: time.Now(), StateRevision: 3,
	})
	if err := store.Save(project); err != nil {
		t.Fatal(err)
	}
	state := station.GetAssistantProgramState()
	state.LinkedProjectIDs = []string{project.ID}
	station.SetAssistantProgramState(state)
	if err := store.Save(station); err != nil {
		t.Fatal(err)
	}
	return store, programs, station, project
}

func TestAssistantLegacyEnsureLeavesPlainStationAndParentProjectionUntouched(t *testing.T) {
	store, programs, station, project := legacyMigrationFixture(t)
	if _, _, err := programs.EnsureProjectStation(project.ID); err != nil {
		t.Fatal(err)
	}
	unchangedHome, _ := store.Get(station.ID)
	unchangedProject, _ := store.Get(project.ID)
	if unchangedHome.Kind != "workspace" || unchangedProject.ParentID != "" || unchangedProject.GetAssistantProjectLink().SchemaVersion != AssistantProjectLinkLegacySchemaVersion {
		t.Fatalf("legacy ensure changed topology: Home=%+v project=%+v", unchangedHome, unchangedProject)
	}
}

func TestAssistantLegacyMigrationRequiresReviewAndPreservesSharedRoster(t *testing.T) {
	store, programs, station, project := legacyMigrationFixture(t)
	state := station.GetAssistantProgramState()
	review, err := programs.ReviewLegacyMigration(station.ID, state.StateRevision)
	if err != nil || review.LegacyRosterCount != 1 || len(review.ProjectWorkspaceIDs) != 1 || len(review.Impact) != 3 {
		t.Fatalf("migration review = %+v, %v", review, err)
	}
	receipt, err := programs.CommitLegacyMigration(station.ID, review.Token)
	if err != nil || receipt.MigratedProjects != 1 || receipt.LegacyRosterCount != 1 {
		t.Fatalf("migration receipt = %+v, %v", receipt, err)
	}
	replay, err := programs.CommitLegacyMigration(station.ID, review.Token)
	if err != nil || !replay.Replayed {
		t.Fatalf("migration replay = %+v, %v", replay, err)
	}
	migratedHome, _ := store.Get(station.ID)
	migratedState := migratedHome.GetAssistantProgramState()
	if migratedHome.Kind != "group" || migratedState.SchemaVersion != AssistantProgramStateSchemaVersion || len(migratedState.Roster) != 1 || len(migratedState.HomeBindings.Bindings) != 0 {
		t.Fatalf("migrated Home = %+v", migratedState)
	}
	migratedProject, _ := store.Get(project.ID)
	link := migratedProject.GetAssistantProjectLink()
	if link.SchemaVersion != AssistantProjectLinkSchemaVersion || len(link.ProjectBindings.Bindings) != 0 || len(migratedProject.AgentInstances) != 1 || migratedProject.AgentInstances[0].Name != "Legacy Producer" {
		t.Fatalf("migrated project = link %+v agents %+v", link, migratedProject.AgentInstances)
	}
}

func TestAssistantLegacyMigrationLeavesBuiltinAndAmbiguousTopologyUntouched(t *testing.T) {
	store, programs, station, project := legacyMigrationFixture(t)
	project.SetTemplateProvenance(&TemplateProvenance{TemplateID: "legacy-builtin", Builtin: true, Version: 1, AssistantProgram: neutralAssistantDeclaration()})
	if err := store.Save(project); err != nil {
		t.Fatal(err)
	}
	if _, err := programs.ReviewLegacyMigration(station.ID, station.GetAssistantProgramState().StateRevision); !errors.Is(err, ErrAssistantMigrationAmbiguous) {
		t.Fatalf("ambiguous migration error = %v", err)
	}
	unchanged, _ := store.Get(station.ID)
	if unchanged.Kind != "workspace" || unchanged.GetAssistantProgramState().SchemaVersion != AssistantProgramLegacyStateSchemaVersion || len(unchanged.GetAssistantProgramState().Roster) != 1 {
		t.Fatalf("ambiguous topology changed: %+v", unchanged)
	}
}
