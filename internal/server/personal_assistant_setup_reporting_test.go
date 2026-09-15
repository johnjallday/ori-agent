package server

import (
	"context"
	"testing"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
	"github.com/johnjallday/ori-agent/internal/setupjourney"
	"github.com/johnjallday/ori-agent/internal/specialist"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type setupReportingRelationship struct{ state *personalassistant.State }

func (r setupReportingRelationship) GetState(context.Context, string) (*personalassistant.State, error) {
	return r.state.Clone(), nil
}

type setupReportingPlugins struct{ items []plugin.InstalledPlugin }

func (p *setupReportingPlugins) List() ([]plugin.InstalledPlugin, error) { return p.items, nil }

// installedReaperQuestPlugin is the REAPER plugin installed with its four-step
// setup_quests_v2 quest, referenced by its blueprint.
func installedReaperQuestPlugin(t *testing.T) plugin.InstalledPlugin {
	t.Helper()
	quest, err := specialist.NormalizeSetupJourney(specialist.SetupJourney{
		SchemaVersion: 1, Version: 1, ID: "reaper_setup", Title: "Set up REAPER", Description: "Connect a REAPER project.",
		IntegrationKey: "ori_reaper", ExpectedBlueprintID: "reaper-song", ExpectedAssistantProgramID: "music-producer-assistant",
		Steps: []specialist.SetupJourneyStep{
			{ID: "project", Kind: specialist.SetupStepProjectConnect, Title: "Project", Description: "Connect a project."},
			{ID: "workspace", Kind: specialist.SetupStepWorkspaceSetup, Title: "Workspace", Description: "Choose a mode."},
			{ID: "staffing", Kind: specialist.SetupStepAssistantProgramStaffing, Title: "Team", Description: "Add roles."},
			{ID: "summary", Kind: specialist.SetupStepSummary, Title: "Review", Description: "Review setup."},
		},
		WorkspaceLaunch: &specialist.WorkspaceLaunchCopy{GroupTitle: "Build Your Music Production Group", GroupName: "Music Production"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return plugin.InstalledPlugin{
		Name: "reaper-plugin", Version: "0.6.0", Enabled: true,
		WorkspaceSurfaces: &plugin.SurfaceContribution{
			RequiresHostFeatures: []string{plugin.HostFeatureSetupQuestsV2}, SetupQuests: []plugin.SetupQuest{*quest},
		},
		ResolvedBlueprints: []plugin.ResolvedBlueprint{{ID: "reaper-song", Template: projecttemplates.Template{
			SetupQuestID: quest.ID, AssistantProgram: &workspace.AssistantProgramDeclaration{ID: quest.ExpectedAssistantProgramID},
		}}},
	}
}

// FR 22: Home's specialist setup card is titled by whichever quest the alias
// resolves: the install quest before the plugin is installed, then the plugin's.
func TestPersonalAssistantSetupReportingTitleFollowsTheResolvedQuest(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, &database.Config{InMemory: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	relationship := setupReportingRelationship{state: &personalassistant.State{
		UserID: "local", AssistantID: "assistant-1", Status: personalassistant.StatusActive,
		SpecialistOfferState: personalassistant.SpecialistOfferAccepted, SpecialistSlug: "music_production",
	}}
	readers := map[specialist.SetupStepKind]setupjourney.CanonicalReader{}
	for _, kind := range specialist.SetupStepKinds() {
		readers[kind] = setupjourney.CanonicalReaderFunc(func(context.Context, setupjourney.ReadScope) (setupjourney.CanonicalStepRead, error) {
			return setupjourney.CanonicalStepRead{}, nil
		})
	}
	registry, err := setupjourney.NewReaderRegistry(readers)
	if err != nil {
		t.Fatal(err)
	}
	journeys, err := setupjourney.NewService(setupjourney.NewSQLiteStore(db), relationship, registry)
	if err != nil {
		t.Fatal(err)
	}
	plugins := &setupReportingPlugins{}
	journeys.SetQuestCatalog(setupjourney.CombineQuestCatalogs(
		setupjourney.NewIntegrationInstallQuestCatalog(reviewedintegration.All, plugins),
		setupjourney.NewInstalledQuestCatalog(plugins),
	))
	adapter := &personalAssistantSetupReportingAdapter{journeys: journeys, workspaces: workspace.NewInMemoryStore()}

	before, err := adapter.GetSpecialistSetup(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	if before.Title != "Install Ori REAPER Plugin" || before.JourneyID != "install_ori_reaper" || before.CurrentStepID != "integration" {
		t.Fatalf("pre-install card = %+v", before)
	}
	if len(before.Actions) == 0 || before.Actions[0].ID != "continue_setup" {
		t.Fatalf("pre-install card actions = %+v", before.Actions)
	}

	plugins.items = []plugin.InstalledPlugin{installedReaperQuestPlugin(t)}
	after, err := adapter.GetSpecialistSetup(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	if after.Title != "Set up REAPER" || after.JourneyID != "reaper_setup" {
		t.Fatalf("post-install card = %+v", after)
	}
}

func TestPersonalAssistantSetupReportingUsesCanonicalRunsAndExactLinks(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, &database.Config{InMemory: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	journeyStore := setupjourney.NewSQLiteStore(db)
	relationship := setupReportingRelationship{state: &personalassistant.State{
		UserID: "local", AssistantID: "assistant-1", Status: personalassistant.StatusActive,
		SpecialistOfferState: personalassistant.SpecialistOfferAccepted, SpecialistSlug: "music_production",
	}}
	readers := map[specialist.SetupStepKind]setupjourney.CanonicalReader{}
	for _, kind := range specialist.SetupStepKinds() {
		kind := kind
		readers[kind] = setupjourney.CanonicalReaderFunc(func(_ context.Context, scope setupjourney.ReadScope) (setupjourney.CanonicalStepRead, error) {
			switch kind {
			case specialist.SetupStepIntegrationInstall:
				return setupjourney.CanonicalStepRead{Complete: true, Result: setupjourney.CanonicalResult{IntegrationPluginID: "reaper-plugin", IntegrationVersion: "0.5.0"}}, nil
			case specialist.SetupStepProjectConnect:
				projectID := "project-1"
				if scope.RunKind == setupjourney.RunKindChild {
					projectID = "project-2"
				}
				return setupjourney.CanonicalStepRead{Complete: true, Result: setupjourney.CanonicalResult{HomeWorkspaceID: "home-1", ProjectWorkspaceID: projectID}}, nil
			case specialist.SetupStepWorkspaceSetup:
				return setupjourney.CanonicalStepRead{Complete: true, Result: setupjourney.CanonicalResult{SelectedModeID: "file_only"}}, nil
			case specialist.SetupStepAssistantProgramStaffing:
				return setupjourney.CanonicalStepRead{Complete: true}, nil
			default:
				return setupjourney.CanonicalStepRead{}, nil
			}
		})
	}
	registry, err := setupjourney.NewReaderRegistry(readers)
	if err != nil {
		t.Fatal(err)
	}
	journeys, err := setupjourney.NewService(journeyStore, relationship, registry)
	if err != nil {
		t.Fatal(err)
	}
	journeys.SetQuestCatalog(setupjourney.NewInstalledQuestCatalog(&setupReportingPlugins{items: []plugin.InstalledPlugin{installedReaperQuestPlugin(t)}}))
	root, err := journeys.Read(ctx, "local", "")
	if err != nil {
		t.Fatal(err)
	}
	child, _, err := journeyStore.CreateOrGetChild(ctx, root.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = journeys.Read(ctx, "local", child.ID); err != nil {
		t.Fatal(err)
	}

	workspaces := workspace.NewInMemoryStore()
	declaration := &workspace.AssistantProgramDeclaration{SchemaVersion: workspace.AssistantProgramSchemaVersion, ID: "music-producer-assistant"}
	key := workspace.AssistantProgramKey{OwnerUserID: "local", PluginID: "reaper-plugin", ProgramID: declaration.ID}.Normalize()
	home := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Music Home"})
	home.ID, home.FolderSlug, home.Kind = "home-1", "music-home", "group"
	home.SetAssistantProgramState(&workspace.AssistantProgramState{
		SchemaVersion: workspace.AssistantProgramStateSchemaVersion, Key: key,
		Declaration: declaration, LinkedProjectIDs: []string{"project-1", "project-2"},
	})
	if err = workspaces.Save(home); err != nil {
		t.Fatal(err)
	}
	for index, id := range []string{"project-1", "project-2"} {
		project := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Project"})
		project.ID, project.FolderSlug = id, id
		project.SetAssistantProjectLink(&workspace.AssistantProjectLink{
			ID: workspace.AssistantProjectLinkID(home.ID, id), SchemaVersion: workspace.AssistantProjectLinkSchemaVersion,
			StationWorkspaceID: home.ID, Key: key, DeclarationVersion: 1, StateRevision: int64(index + 1),
		})
		if err = workspaces.Save(project); err != nil {
			t.Fatal(err)
		}
	}

	adapter := &personalAssistantSetupReportingAdapter{journeys: journeys, workspaces: workspaces}
	got, err := adapter.GetSpecialistSetup(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	if got.Lifecycle != "ready" || got.ConnectedProjectCount != 2 || got.ChildRunCount != 1 || got.UnfinishedChildCount != 0 || len(got.Runs) != 2 {
		t.Fatalf("overview = %+v", got)
	}
	if got.Runs[0].ProjectWorkspaceID != "project-1" || got.Runs[1].ProjectWorkspaceID != "project-2" {
		t.Fatalf("run/project identity was inferred incorrectly: %+v", got.Runs)
	}
	actions := map[string]personalassistant.TodaySpecialistSetupAction{}
	for _, action := range got.Actions {
		actions[action.ID] = action
	}
	for _, id := range []string{"review_setup", "connect_another", "open_home", "open_project", "manage_samples", "live_setup"} {
		if _, ok := actions[id]; !ok {
			t.Errorf("missing action %q in %+v", id, got.Actions)
		}
	}
	if actions["open_home"].Route != "/workspaces/music-home/assistant" || actions["open_project"].Route != "/workspaces/project-1" {
		t.Fatalf("actions do not use canonical slugs: %+v", got.Actions)
	}
}
