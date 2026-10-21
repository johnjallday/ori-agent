package sessionhttp

import (
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func resourceSummaryHomeDeclaration() projecttemplates.AssistantProgramHome {
	return projecttemplates.AssistantProgramHome{
		SchemaVersion: 1, Version: 1, ID: "music-producer-assistant", StationName: "Music Production Home",
		AllowedProjectAttachments: []projecttemplates.AssistantProgramAllowedProjectAttachment{
			{ProviderPluginID: "reaper-plugin", BlueprintID: "reaper-song", ProjectTeamID: "reaper-song-team",
				ProjectTeamSchemaVersion: 1, MinProjectTeamVersion: 1, MaxProjectTeamVersion: 1},
			{ProviderPluginID: "reaper-plugin", BlueprintID: "reaper-studio", ProjectTeamID: "reaper-studio-team",
				ProjectTeamSchemaVersion: 1, MinProjectTeamVersion: 1, MaxProjectTeamVersion: 1,
				WorkspaceKind: workspace.AssistantWorkspaceKindResource, Label: "REAPER Studio", Description: "Your studio catalog."},
		},
	}
}

func resourceSummaryStudioBlueprint() plugin.ResolvedBlueprint {
	return plugin.ResolvedBlueprint{ID: "reaper-studio", Version: 1, Template: projecttemplates.Template{
		ID: "plugin:reaper-plugin:reaper-studio",
		AssistantProject: &projecttemplates.AssistantProjectDeclaration{
			SchemaVersion: 1, Version: 1, ID: "reaper-studio-team", WorkspaceKind: workspace.AssistantWorkspaceKindResource,
		},
	}}
}

type resourceSummaryFixture struct {
	store     *workspace.FileStore
	programs  *workspace.AssistantProgramStore
	home      *workspace.Workspace
	installed []plugin.InstalledPlugin
}

func newResourceSummaryFixture(t *testing.T) *resourceSummaryFixture {
	t.Helper()
	store, err := workspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	declaration := resourceSummaryHomeDeclaration()
	owner := workspace.AssistantProgramHomeOwner{
		PluginID: "music-project-management", PluginVersion: "0.4.0", ProgramID: declaration.ID, HomeSchemaVersion: 1, HomeVersion: 1,
		DeclarationDigest: projecttemplates.AssistantProgramHomeDigest(declaration), PluginGeneration: 3, ComponentFingerprint: strings.Repeat("b", 64),
	}
	programs := workspace.NewAssistantProgramStore(store)
	key := workspace.AssistantProgramKey{OwnerUserID: "local", PluginID: owner.PluginID, ProgramID: owner.ProgramID}
	home, _, err := programs.EnsureNamedIndependentStation(key, declaration.AssistantProgram(), "Music Production Home", owner)
	if err != nil {
		t.Fatal(err)
	}
	return &resourceSummaryFixture{store: store, programs: programs, home: home, installed: []plugin.InstalledPlugin{
		{Name: "music-project-management", Version: "0.4.0", Enabled: true,
			WorkspaceSurfaces: &plugin.SurfaceContribution{AssistantProgramHomes: []projecttemplates.AssistantProgramHome{declaration}}},
		{Name: "reaper-plugin", Version: "0.11.0", Enabled: true, ResolvedBlueprints: []plugin.ResolvedBlueprint{resourceSummaryStudioBlueprint()}},
	}}
}

func (f *resourceSummaryFixture) summaries(t *testing.T) []resourceWorkspaceSummary {
	t.Helper()
	home, err := f.store.Get(f.home.ID)
	if err != nil {
		t.Fatal(err)
	}
	summaries, err := resourceWorkspaceSummaries(f.programs, f.installed, home, home.GetAssistantProgramState())
	if err != nil {
		t.Fatal(err)
	}
	return summaries
}

func (f *resourceSummaryFixture) linkStudio(t *testing.T) *workspace.Workspace {
	t.Helper()
	homeState := f.home.GetAssistantProgramState()
	key := homeState.Key.Normalize()
	project := &workspace.AssistantProjectProviderOwner{
		PluginID: "reaper-plugin", PluginVersion: "0.11.0", BlueprintID: "reaper-studio", BlueprintVersion: 1,
		ProjectTeamID: "reaper-studio-team", ProjectTeamSchema: 1, ProjectTeamVersion: 1, ProjectTeamDigest: strings.Repeat("c", 64),
		PluginGeneration: 5, ComponentFingerprint: strings.Repeat("d", 64), WorkspaceKind: workspace.AssistantWorkspaceKindResource,
	}
	studio := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "REAPER Studio"})
	studio.ID, studio.OwnerUserID = "studio-one", "local"
	studio.SetTemplateProvenance(&workspace.TemplateProvenance{
		TemplateID: "plugin:reaper-plugin:reaper-studio", AssistantProgram: homeState.Declaration,
		AssistantProjectRoles: []workspace.AssistantProgramRoleSpec{{ID: "reaper-studio-assistant", Label: "REAPER Studio Assistant",
			Scope: workspace.AssistantRoleScopeProject, Required: true, Primary: true, SystemPrompt: "Explain the catalog."}},
		GroupRequirement: &workspace.GroupRequirementSnapshot{
			SchemaVersion: workspace.GroupRequirementSnapshotSchemaVersion, Policy: "required", SelectedComposition: workspace.GroupRequirementCompositionGrouped,
			TemplateID: "plugin:reaper-plugin:reaper-studio", DefinitionDigest: strings.Repeat("e", 64), ReviewDigest: strings.Repeat("f", 64),
			OperationDigest: strings.Repeat("1", 64), AppliedAt: time.Now().UTC(), ProgramKey: &key,
			HomeWorkspaceID: f.home.ID, ProjectLinkID: workspace.AssistantProjectLinkID(f.home.ID, studio.ID),
			HomeProvider: homeState.HomeProvider, ProjectProvider: project,
		},
	})
	if err := f.store.Save(studio); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.programs.EnsureProjectStation(studio.ID); err != nil {
		t.Fatalf("link the Studio: %v", err)
	}
	return studio
}

func TestResourceWorkspaceSummary_OffersSetUpThenOpen(t *testing.T) {
	fixture := newResourceSummaryFixture(t)
	summaries := fixture.summaries(t)
	if len(summaries) != 1 || summaries[0].State != resourceWorkspaceNotCreated ||
		summaries[0].TemplateID != "plugin:reaper-plugin:reaper-studio" || summaries[0].Workspace != nil ||
		summaries[0].Attachment.Label != "REAPER Studio" {
		t.Fatalf("before setup = %+v", summaries)
	}
	studio := fixture.linkStudio(t)
	summaries = fixture.summaries(t)
	if len(summaries) != 1 || summaries[0].State != resourceWorkspaceAvailable || summaries[0].TemplateID != "" ||
		summaries[0].Workspace == nil || summaries[0].Workspace.ID != studio.ID || !strings.HasPrefix(summaries[0].Workspace.URL, "/workspaces/") {
		t.Fatalf("after setup = %+v", summaries)
	}
}

func TestResourceWorkspaceSummary_NamesWhatTheProviderLacks(t *testing.T) {
	cases := map[string]struct {
		mutate func(f *resourceSummaryFixture)
		want   string
	}{
		"plugin missing":  {func(f *resourceSummaryFixture) { f.installed = f.installed[:1] }, resourceWorkspaceProviderMissing},
		"plugin disabled": {func(f *resourceSummaryFixture) { f.installed[1].Enabled = false }, resourceWorkspaceProviderDisabled},
		"older plugin without the blueprint": {func(f *resourceSummaryFixture) {
			f.installed[1].ResolvedBlueprints = nil
		}, resourceWorkspaceProviderIncompatible},
		"blueprint with a project team": {func(f *resourceSummaryFixture) {
			f.installed[1].ResolvedBlueprints[0].Template.AssistantProject.WorkspaceKind = ""
		}, resourceWorkspaceProviderIncompatible},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			fixture := newResourceSummaryFixture(t)
			test.mutate(fixture)
			summaries := fixture.summaries(t)
			if len(summaries) != 1 || summaries[0].State != test.want || summaries[0].TemplateID != "" {
				t.Fatalf("summaries = %+v, want %s", summaries, test.want)
			}
		})
	}
}

func TestResourceWorkspaceSummary_KeepsAnExistingStudioOpenableWhenTheHomePinIsGone(t *testing.T) {
	fixture := newResourceSummaryFixture(t)
	studio := fixture.linkStudio(t)
	fixture.installed[0].Enabled = false
	fixture.installed[0].WorkspaceSurfaces = nil // the pinned Home declaration is no longer installed
	summaries := fixture.summaries(t)
	if len(summaries) != 1 || summaries[0].State != resourceWorkspaceHomeUnavailable || summaries[0].Workspace == nil ||
		summaries[0].Workspace.ID != studio.ID || summaries[0].TemplateID != "" {
		t.Fatalf("summaries = %+v", summaries)
	}
}
