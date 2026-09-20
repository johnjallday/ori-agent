package workspace

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func splitProviderOwners() (AssistantProgramHomeOwner, AssistantProjectProviderOwner) {
	return AssistantProgramHomeOwner{
			PluginID: "music", PluginVersion: "2.0.0", ProgramID: "music_home", HomeSchemaVersion: 1, HomeVersion: 2,
			DeclarationDigest: strings.Repeat("a", 64), PluginGeneration: 11, ComponentFingerprint: strings.Repeat("b", 64),
		}, AssistantProjectProviderOwner{
			PluginID: "reaper", PluginVersion: "4.0.0", BlueprintID: "song", BlueprintVersion: 7,
			ProjectTeamID: "reaper_team", ProjectTeamSchema: 1, ProjectTeamVersion: 3,
			ProjectTeamDigest: strings.Repeat("c", 64), PluginGeneration: 19, ComponentFingerprint: strings.Repeat("d", 64),
		}
}

func splitHomeDeclaration() *AssistantProgramDeclaration {
	return &AssistantProgramDeclaration{
		SchemaVersion: AssistantProgramSchemaVersion, ID: "music_home", StationName: "Music Home",
		Roles: []AssistantProgramRoleSpec{{
			ID: "producer", Label: "Producer", Scope: AssistantRoleScopeHome, Required: true, Primary: true,
			SystemPrompt: "Coordinate the music portfolio.", Skills: []string{"music-project-management"},
		}},
	}
}

func splitProjectProvenance(homeID, projectID string, homeOwner AssistantProgramHomeOwner, projectOwner AssistantProjectProviderOwner) *TemplateProvenance {
	key := AssistantProgramKey{OwnerUserID: "owner", PluginID: "music", ProgramID: "music_home"}
	return &TemplateProvenance{
		TemplateID: "plugin:reaper:song", AssistantProgram: splitHomeDeclaration(),
		AssistantProjectRoles: []AssistantProgramRoleSpec{{
			ID: "engineer", Label: "Engineer", Scope: AssistantRoleScopeProject, Required: true, Primary: true,
			SystemPrompt: "Operate this exact project.", Skills: []string{"reaper-project"},
		}},
		GroupRequirement: &GroupRequirementSnapshot{
			SchemaVersion: GroupRequirementSnapshotSchemaVersion, Policy: "required", SelectedComposition: GroupRequirementCompositionGrouped,
			TemplateID: "plugin:reaper:song", DefinitionDigest: strings.Repeat("e", 64), ReviewDigest: strings.Repeat("f", 64),
			OperationDigest: strings.Repeat("1", 64), AppliedAt: time.Now().UTC(), ProgramKey: &key,
			HomeWorkspaceID: homeID, ProjectLinkID: AssistantProjectLinkID(homeID, projectID),
			HomeProvider: &homeOwner, ProjectProvider: &projectOwner,
		},
	}
}

func TestEnsureProjectStationUsesExistingIndependentHomeAndPersistsProjectOwner(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	programs := NewAssistantProgramStore(store)
	homeOwner, projectOwner := splitProviderOwners()
	key := AssistantProgramKey{OwnerUserID: "owner", PluginID: "music", ProgramID: "music_home"}
	home, created, err := programs.EnsureNamedIndependentStation(key, splitHomeDeclaration(), "Music Home", homeOwner)
	if err != nil || !created {
		t.Fatalf("create independent Home = %#v, %t, %v", home, created, err)
	}
	project := NewWorkspace(CreateWorkspaceParams{Name: "Song"})
	project.ID = "project-one"
	project.OwnerUserID = "owner"
	project.SetTemplateProvenance(splitProjectProvenance(home.ID, project.ID, homeOwner, projectOwner))
	if err := store.Save(project); err != nil {
		t.Fatal(err)
	}

	station, stationCreated, err := programs.EnsureProjectStation(project.ID)
	if err != nil || stationCreated || station.ID != home.ID {
		t.Fatalf("link independent Home = %#v, %t, %v", station, stationCreated, err)
	}
	linked, err := store.Get(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	link := linked.GetAssistantProjectLink()
	if link == nil || link.HomeProvider == nil || *link.HomeProvider != homeOwner || link.ProjectProvider == nil || *link.ProjectProvider != projectOwner ||
		len(link.ProjectRoles) != 1 || link.ProjectRoles[0].ID != "engineer" {
		t.Fatalf("split link = %#v", link)
	}
	state := station.GetAssistantProgramState()
	if state == nil || state.HomeProvider == nil || *state.HomeProvider != homeOwner || len(state.Declaration.Roles) != 1 || state.Declaration.Roles[0].ID != "producer" {
		t.Fatalf("independent Home state = %#v", state)
	}
}

func TestIndependentHomeDoesNotAdoptOrRewriteSameNamedLegacyProvider(t *testing.T) {
	root := t.TempDir()
	store, err := NewFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	programs := NewAssistantProgramStore(store)
	legacyKey := AssistantProgramKey{OwnerUserID: "owner", PluginID: "reaper", ProgramID: "music_home"}
	legacyDeclaration := splitHomeDeclaration()
	legacyDeclaration.Roles = append(legacyDeclaration.Roles, AssistantProgramRoleSpec{
		ID: "engineer", Label: "Engineer", Scope: AssistantRoleScopeProject, Required: true, Primary: true,
		SystemPrompt: "Operate this exact project.", Skills: []string{"reaper-project"},
	})
	legacy, created, err := programs.EnsureNamedStation(legacyKey, legacyDeclaration, "Music Home")
	if err != nil || !created {
		t.Fatalf("legacy Home = %#v, %t, %v", legacy, created, err)
	}
	legacyPath := filepath.Join(root, legacy.FolderSlug, "workspace.json")
	before, err := os.ReadFile(legacyPath) // #nosec G304 -- path is inside the test's temporary workspace root
	if err != nil {
		t.Fatal(err)
	}

	homeOwner, _ := splitProviderOwners()
	independentKey := AssistantProgramKey{OwnerUserID: "owner", PluginID: "music", ProgramID: "music_home"}
	independent, created, err := programs.EnsureNamedIndependentStation(independentKey, splitHomeDeclaration(), "Music Home", homeOwner)
	if err != nil || !created || independent.ID == legacy.ID {
		t.Fatalf("independent Home adopted legacy identity: %#v, %t, %v", independent, created, err)
	}
	ids, err := store.List()
	if err != nil || len(ids) != 2 {
		t.Fatalf("same-name Homes = %v, %v", ids, err)
	}
	legacyAfter, err := store.Get(legacy.ID)
	if err != nil {
		t.Fatal(err)
	}
	legacyState := legacyAfter.GetAssistantProgramState()
	if legacyState.Key.Normalize() != legacyKey.Normalize() || legacyState.HomeProvider != nil || len(legacyState.Declaration.Roles) != 2 {
		t.Fatalf("legacy Home was rewritten: %#v", legacyState)
	}
	after, err := os.ReadFile(legacyPath) // #nosec G304 -- path is inside the test's temporary workspace root
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("independent Home setup rewrote persisted legacy workspace bytes")
	}
}

func TestIndependentHomeReuseRequiresExactPersistedProvider(t *testing.T) {
	store := NewInMemoryStore()
	programs := NewAssistantProgramStore(store)
	key := AssistantProgramKey{OwnerUserID: "owner", PluginID: "music", ProgramID: "music_home"}
	legacy, created, err := programs.EnsureNamedStation(key, splitHomeDeclaration(), "Music Home")
	if err != nil || !created {
		t.Fatalf("unprovenanced Home = %#v, %t, %v", legacy, created, err)
	}
	homeOwner, _ := splitProviderOwners()
	if _, created, err := programs.EnsureNamedIndependentStation(key, splitHomeDeclaration(), "Music Home", homeOwner); !errors.Is(err, ErrAssistantProgramVersionConflict) || created {
		t.Fatalf("unprovenanced exact-key reuse = created %t, err %v", created, err)
	}
	unchanged, _ := store.Get(legacy.ID)
	if state := unchanged.GetAssistantProgramState(); state == nil || state.HomeProvider != nil {
		t.Fatalf("unprovenanced Home was adopted: %#v", state)
	}

	matchingStore := NewInMemoryStore()
	matchingPrograms := NewAssistantProgramStore(matchingStore)
	matching, created, err := matchingPrograms.EnsureNamedIndependentStation(key, splitHomeDeclaration(), "Music Home", homeOwner)
	if err != nil || !created {
		t.Fatalf("independent Home = %#v, %t, %v", matching, created, err)
	}
	changedOwner := homeOwner
	changedOwner.PluginGeneration++
	if _, created, err := matchingPrograms.EnsureNamedIndependentStation(key, splitHomeDeclaration(), "Music Home", changedOwner); !errors.Is(err, ErrAssistantProgramVersionConflict) || created {
		t.Fatalf("different provider evidence reuse = created %t, err %v", created, err)
	}
}

func TestEnsureProjectStationNeverCreatesMissingIndependentHome(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	homeOwner, projectOwner := splitProviderOwners()
	project := NewWorkspace(CreateWorkspaceParams{Name: "Song"})
	project.ID = "project-one"
	project.OwnerUserID = "owner"
	project.SetTemplateProvenance(splitProjectProvenance("missing-home", project.ID, homeOwner, projectOwner))
	if err := store.Save(project); err != nil {
		t.Fatal(err)
	}
	if _, created, err := NewAssistantProgramStore(store).EnsureProjectStation(project.ID); !errors.Is(err, ErrAssistantStationNotFound) || created {
		t.Fatalf("missing independent Home = created %t, err %v", created, err)
	}
	ids, err := store.List()
	if err != nil || len(ids) != 1 || ids[0] != project.ID {
		t.Fatalf("workspace ids after refusal = %v, %v", ids, err)
	}
}
