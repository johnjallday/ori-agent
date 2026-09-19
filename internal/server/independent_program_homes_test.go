package server

import (
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func splitProgramFixture() ([]plugin.InstalledPlugin, projecttemplates.Template) {
	project := &projecttemplates.AssistantProjectDeclaration{
		SchemaVersion: projecttemplates.AssistantProjectSchemaVersion, Version: 3, ID: "reaper_music_team",
		Home: projecttemplates.AssistantProjectHomeReference{
			ProviderPluginID: "music-project-management", ProgramID: "music_production", HomeSchemaVersion: 1,
			MinHomeVersion: 2, MaxHomeVersion: 2,
		},
		Roles: []projecttemplates.AssistantProjectRole{{
			ID: "reaper_engineer", Label: "REAPER Engineer", Required: true, Primary: true,
			SystemPrompt: "Operate the exact linked REAPER project.", Skills: []string{"reaper-project"},
		}},
	}
	template := projecttemplates.Template{
		ID: "plugin:reaper:reaper-song", Name: "REAPER Song", AssistantProject: project,
		PluginOwner: &workspace.PluginTemplateOwner{
			PluginID: "reaper", PluginVersion: "4.0.0", BlueprintID: "reaper-song", BlueprintVersion: 7,
		},
		GroupRequirement: &projecttemplates.GroupRequirement{
			SchemaVersion: projecttemplates.GroupRequirementSchemaVersion, Policy: projecttemplates.GroupPolicyRequired,
		},
	}
	home := projecttemplates.AssistantProgramHome{
		SchemaVersion: 1, Version: 2, ID: "music_production", StationName: "Music Production Home",
		StationDescription: "Portfolio planning.", DefaultPrimaryName: "Music Producer", HireTitle: "Hire producer",
		Roles: []projecttemplates.AssistantProgramHomeRole{{
			ID: "producer", Label: "Producer", Required: true, Primary: true,
			SystemPrompt: "Coordinate the music portfolio.", Skills: []string{"music-project-management"},
		}},
		Stages: []workspace.AssistantProgramStageSpec{{ID: "foundation", Label: "Foundation"}},
		Reflection: workspace.AssistantReflectionConfig{
			MinimumProjects: 3, CadenceHours: 24, MaxProjects: 3, MaxEventsPerProject: 1, MaxCandidates: 1, MaxEvidence: 3,
			Rubric: "Review portfolio evidence.",
		},
		AllowedProjectAttachments: []projecttemplates.AssistantProgramAllowedProjectAttachment{{
			ProviderPluginID: "reaper", BlueprintID: "reaper-song", ProjectTeamID: "reaper_music_team",
			ProjectTeamSchemaVersion: 1, MinProjectTeamVersion: 3, MaxProjectTeamVersion: 3,
		}},
	}
	feature := []string{plugin.HostFeatureIndependentProgramHomesV1}
	fingerprint := strings.Repeat("a", 64)
	installed := []plugin.InstalledPlugin{
		{
			Name: "music-project-management", Version: "2.0.0", Enabled: true, Generation: 11, ContentGeneration: 11, ComponentFingerprint: fingerprint,
			Skills: []string{"music-project-management"},
			WorkspaceSurfaces: &plugin.SurfaceContribution{
				Name: "music-project-management", Version: "2.0.0", Protocol: plugin.ProtocolRange{Min: 1, Max: plugin.SurfaceProtocolVersion},
				RequiresHostFeatures: feature, AssistantProgramHomes: []projecttemplates.AssistantProgramHome{home},
			},
		},
		{
			Name: "reaper", Version: "4.0.0", Enabled: true, Generation: 19, ContentGeneration: 19, ComponentFingerprint: strings.Repeat("b", 64),
			Skills: []string{"reaper-project"},
			WorkspaceSurfaces: &plugin.SurfaceContribution{
				Name: "reaper", Version: "4.0.0", Protocol: plugin.ProtocolRange{Min: 1, Max: plugin.SurfaceProtocolVersion},
				RequiresHostFeatures: feature,
			},
			ResolvedBlueprints: []plugin.ResolvedBlueprint{{ID: "reaper-song", Version: 7, Template: template}},
		},
	}
	return installed, template
}

func TestResolveIndependentProgramHomeBindsExactTwoProviderEvidence(t *testing.T) {
	installed, template := splitProgramFixture()
	resolved, err := resolveIndependentProgramHome(installed, "local", template, true)
	if err != nil {
		t.Fatalf("resolve split providers: %v", err)
	}
	if resolved.Key.PluginID != "music-project-management" || resolved.Key.ProgramID != "music_production" || resolved.Declaration == nil {
		t.Fatalf("resolved Home = %#v", resolved)
	}
	if resolved.Owner == nil || resolved.Owner.PluginGeneration != 11 || resolved.Owner.DeclarationDigest == "" ||
		resolved.ProjectOwner == nil || resolved.ProjectOwner.PluginGeneration != 19 || resolved.ProjectOwner.ProjectTeamDigest == "" {
		t.Fatalf("provider evidence = home %#v project %#v", resolved.Owner, resolved.ProjectOwner)
	}
	if len(resolved.Declaration.Roles) != 1 || resolved.Declaration.Roles[0].Scope != workspace.AssistantRoleScopeHome {
		t.Fatalf("Home roles = %#v", resolved.Declaration.Roles)
	}
	if !plugin.IndependentProviderEvidenceAvailable(installed, resolved.Owner, resolved.ProjectOwner) {
		t.Fatal("fresh split-provider evidence was unavailable")
	}
	installed[1].Enabled = false
	if plugin.IndependentProviderEvidenceAvailable(installed, resolved.Owner, resolved.ProjectOwner) ||
		plugin.IndependentProjectProviderEvidenceAvailable(installed, resolved.ProjectOwner) {
		t.Fatal("disabled project provider retained runtime availability")
	}
	if !plugin.IndependentHomeProviderEvidenceAvailable(installed, resolved.Owner) {
		t.Fatal("disabled project provider also disabled independent Home management")
	}
	installed[1].Enabled = true
	installed[0].Enabled = false
	if plugin.IndependentHomeProviderEvidenceAvailable(installed, resolved.Owner) ||
		!plugin.IndependentProjectProviderEvidenceAvailable(installed, resolved.ProjectOwner) {
		t.Fatal("disabled Home provider was not isolated from project-local availability")
	}
}

func TestIndependentManagedSkillRuntimeStaysBoundToRecordedProjectProvider(t *testing.T) {
	installed, template := splitProgramFixture()
	resolved, err := resolveIndependentProgramHome(installed, "local", template, true)
	if err != nil {
		t.Fatal(err)
	}
	store := workspace.NewInMemoryStore()
	project := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Song"})
	project.AgentInstances = []workspace.AgentInstance{{ID: "engineer-1", Name: "June", RoleID: "reaper_engineer"}}
	project.SetAssistantProjectLink(&workspace.AssistantProjectLink{ProjectProvider: resolved.ProjectOwner})
	if err := store.Save(project); err != nil {
		t.Fatal(err)
	}
	if !independentSkillProviderAvailableForAgent(store, installed, "June", "reaper") {
		t.Fatal("fresh project-provider evidence hid the managed skill")
	}
	installed[1].Generation++
	if !independentSkillProviderAvailableForAgent(store, installed, "June", "reaper") {
		t.Fatal("enable/disable runtime epoch invalidated unchanged managed skill content")
	}
	installed[1].ContentGeneration++
	if independentSkillProviderAvailableForAgent(store, installed, "June", "reaper") {
		t.Fatal("changed project content generation silently rebound the managed skill")
	}
	if !independentSkillProviderAvailableForAgent(store, installed, "Unrelated", "reaper") {
		t.Fatal("unrelated agent was bound to project-provider provenance")
	}
}

func TestResolveIndependentProgramHomeFailsClosedForOneSidedOrStaleAuthorization(t *testing.T) {
	tests := []struct {
		name   string
		mutate func([]plugin.InstalledPlugin, *projecttemplates.Template)
	}{
		{name: "project points elsewhere", mutate: func(_ []plugin.InstalledPlugin, template *projecttemplates.Template) {
			template.AssistantProject.Home.ProviderPluginID = "other-home"
		}},
		{name: "Home omits reciprocal authorization", mutate: func(installed []plugin.InstalledPlugin, _ *projecttemplates.Template) {
			installed[0].WorkspaceSurfaces.AssistantProgramHomes[0].AllowedProjectAttachments = nil
		}},
		{name: "project provider generation is absent", mutate: func(installed []plugin.InstalledPlugin, _ *projecttemplates.Template) {
			installed[1].Generation = 0
			installed[1].ContentGeneration = 0
		}},
		{name: "role identities collide across owners", mutate: func(installed []plugin.InstalledPlugin, _ *projecttemplates.Template) {
			installed[0].WorkspaceSurfaces.AssistantProgramHomes[0].Roles[0].ID = "reaper_engineer"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			installed, template := splitProgramFixture()
			test.mutate(installed, &template)
			if _, err := resolveIndependentProgramHome(installed, "local", template, true); err == nil {
				t.Fatal("expected fail-closed resolution")
			}
		})
	}
}

func TestResolveIndependentProgramHomeStandaloneNeedsOnlyProjectProvider(t *testing.T) {
	installed, template := splitProgramFixture()
	installed = installed[1:]
	resolved, err := resolveIndependentProgramHome(installed, "local", template, false)
	if err != nil {
		t.Fatalf("resolve standalone provider: %v", err)
	}
	if resolved.ProjectOwner == nil || resolved.Owner != nil || resolved.Declaration != nil || resolved.Key.Valid() {
		t.Fatalf("standalone resolution = %#v", resolved)
	}
}
