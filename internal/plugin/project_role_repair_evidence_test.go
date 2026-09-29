package plugin

import (
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestExactIndependentProjectRolesRequiresOriginalReciprocalProviderPins(t *testing.T) {
	homeSurface, err := ParseSurfaceContribution(independentHomeContributionJSON(t))
	if err != nil {
		t.Fatal(err)
	}
	home := homeSurface.AssistantProgramHomes[0]
	project := &projecttemplates.AssistantProjectDeclaration{
		ID: "reaper-song-team", SchemaVersion: 1, Version: 1,
		Home: projecttemplates.AssistantProjectHomeReference{
			ProviderPluginID: homeSurface.Name, ProgramID: home.ID, HomeSchemaVersion: 1,
			MinHomeVersion: 1, MaxHomeVersion: 1,
		},
		Roles: []projecttemplates.AssistantProjectRole{{ID: "reaper-assistant", Label: "REAPER Assistant", Primary: true,
			SystemPrompt: "Work in the exact project only.", Skills: []string{"reaper-skill"}}},
	}
	projectSurface := &SurfaceContribution{Protocol: ProtocolRange{Min: 1, Max: 1},
		RequiresHostFeatures: []string{HostFeatureIndependentProgramHomesV1}}
	installed := []InstalledPlugin{
		{Name: homeSurface.Name, Version: homeSurface.Version, Enabled: true, ContentGeneration: 1,
			ComponentFingerprint: strings.Repeat("a", 64), WorkspaceSurfaces: homeSurface,
			Skills: []string{"music-project-management"}},
		{Name: "reaper-plugin", Version: "0.9.0", Enabled: true, ContentGeneration: 2,
			ComponentFingerprint: strings.Repeat("b", 64), WorkspaceSurfaces: projectSurface,
			ResolvedBlueprints: []ResolvedBlueprint{{ID: "reaper-song", Version: 10,
				Template: projecttemplates.Template{AssistantProject: project}}}, Skills: []string{"reaper-skill"}},
	}
	homeOwner := &workspace.AssistantProgramHomeOwner{PluginID: installed[0].Name, PluginVersion: installed[0].Version,
		ProgramID: home.ID, HomeSchemaVersion: home.SchemaVersion, HomeVersion: home.Version,
		DeclarationDigest: projecttemplates.AssistantProgramHomeDigest(home),
		PluginGeneration:  installed[0].EvidenceGeneration(), ComponentFingerprint: installed[0].ComponentFingerprint}
	projectOwner := &workspace.AssistantProjectProviderOwner{PluginID: installed[1].Name, PluginVersion: installed[1].Version,
		BlueprintID: "reaper-song", BlueprintVersion: 10, ProjectTeamID: project.ID,
		ProjectTeamSchema: project.SchemaVersion, ProjectTeamVersion: project.Version,
		ProjectTeamDigest: projecttemplates.AssistantProjectDigest(project),
		PluginGeneration:  installed[1].EvidenceGeneration(), ComponentFingerprint: installed[1].ComponentFingerprint}
	roles, ok := ExactIndependentProjectRoles(installed, homeOwner, projectOwner)
	if !ok || len(roles) != 1 || roles[0].ID != "reaper-assistant" || roles[0].Scope != workspace.AssistantRoleScopeProject || !roles[0].Required {
		t.Fatalf("exact pinned project declaration unavailable: %+v ok=%t", roles, ok)
	}
	roles[0].Skills[0] = "browser-supplied"
	if fresh, ok := ExactIndependentProjectRoles(installed, homeOwner, projectOwner); !ok || fresh[0].Skills[0] != "reaper-skill" {
		t.Fatalf("returned roles aliased installed blueprint: %+v ok=%t", fresh, ok)
	}

	refuse := func(name string, mutate func([]InstalledPlugin, *workspace.AssistantProgramHomeOwner, *workspace.AssistantProjectProviderOwner)) {
		t.Helper()
		t.Run(name, func(t *testing.T) {
			copyOfInstalled := append([]InstalledPlugin(nil), installed...)
			pinnedHome, pinnedProject := *homeOwner, *projectOwner
			mutate(copyOfInstalled, &pinnedHome, &pinnedProject)
			if roles, ok := ExactIndependentProjectRoles(copyOfInstalled, &pinnedHome, &pinnedProject); ok || len(roles) != 0 {
				t.Fatalf("replacement or ambiguous provider supplied roles: %+v ok=%t", roles, ok)
			}
		})
	}
	refuse("changed project fingerprint", func(rows []InstalledPlugin, _ *workspace.AssistantProgramHomeOwner, _ *workspace.AssistantProjectProviderOwner) {
		rows[1].ComponentFingerprint = strings.Repeat("c", 64)
	})
	refuse("changed project generation", func(rows []InstalledPlugin, _ *workspace.AssistantProgramHomeOwner, _ *workspace.AssistantProjectProviderOwner) {
		rows[1].ContentGeneration++
	})
	refuse("changed project version", func(rows []InstalledPlugin, _ *workspace.AssistantProgramHomeOwner, _ *workspace.AssistantProjectProviderOwner) {
		rows[1].Version = "1.0.0"
	})
	refuse("changed Home version", func(rows []InstalledPlugin, _ *workspace.AssistantProgramHomeOwner, _ *workspace.AssistantProjectProviderOwner) {
		rows[0].Version = "0.1.1"
	})
	refuse("changed Home generation", func(rows []InstalledPlugin, _ *workspace.AssistantProgramHomeOwner, _ *workspace.AssistantProjectProviderOwner) {
		rows[0].ContentGeneration++
	})
	refuse("changed Home fingerprint", func(rows []InstalledPlugin, _ *workspace.AssistantProgramHomeOwner, _ *workspace.AssistantProjectProviderOwner) {
		rows[0].ComponentFingerprint = strings.Repeat("d", 64)
	})
	refuse("changed saved Home declaration", func(_ []InstalledPlugin, pin *workspace.AssistantProgramHomeOwner, _ *workspace.AssistantProjectProviderOwner) {
		pin.DeclarationDigest = strings.Repeat("e", 64)
	})
	refuse("different project blueprint", func(_ []InstalledPlugin, _ *workspace.AssistantProgramHomeOwner, pin *workspace.AssistantProjectProviderOwner) {
		pin.BlueprintVersion++
	})
	refuse("ambiguous project blueprint", func(rows []InstalledPlugin, _ *workspace.AssistantProgramHomeOwner, _ *workspace.AssistantProjectProviderOwner) {
		rows[1].ResolvedBlueprints = append(append([]ResolvedBlueprint(nil), rows[1].ResolvedBlueprints...), rows[1].ResolvedBlueprints[0])
	})
	refuse("changed declared project role", func(rows []InstalledPlugin, _ *workspace.AssistantProgramHomeOwner, _ *workspace.AssistantProjectProviderOwner) {
		declaration := *rows[1].ResolvedBlueprints[0].Template.AssistantProject
		declaration.Roles = append([]projecttemplates.AssistantProjectRole(nil), declaration.Roles...)
		declaration.Roles[0].SystemPrompt = "Replacement role prompt"
		rows[1].ResolvedBlueprints = append([]ResolvedBlueprint(nil), rows[1].ResolvedBlueprints...)
		rows[1].ResolvedBlueprints[0].Template.AssistantProject = &declaration
	})
	refuse("changed reciprocal Home reference", func(rows []InstalledPlugin, _ *workspace.AssistantProgramHomeOwner, pin *workspace.AssistantProjectProviderOwner) {
		declaration := *rows[1].ResolvedBlueprints[0].Template.AssistantProject
		declaration.Home.ProgramID = "different-home"
		rows[1].ResolvedBlueprints = append([]ResolvedBlueprint(nil), rows[1].ResolvedBlueprints...)
		rows[1].ResolvedBlueprints[0].Template.AssistantProject = &declaration
		pin.ProjectTeamDigest = projecttemplates.AssistantProjectDigest(&declaration)
	})
	refuse("disabled project", func(rows []InstalledPlugin, _ *workspace.AssistantProgramHomeOwner, _ *workspace.AssistantProjectProviderOwner) {
		rows[1].Enabled = false
	})
	refuse("disabled Home", func(rows []InstalledPlugin, _ *workspace.AssistantProgramHomeOwner, _ *workspace.AssistantProjectProviderOwner) {
		rows[0].Enabled = false
	})
	refuse("project role skill missing", func(rows []InstalledPlugin, _ *workspace.AssistantProgramHomeOwner, _ *workspace.AssistantProjectProviderOwner) {
		rows[1].Skills = nil
	})
	refuse("Home provider missing", func(rows []InstalledPlugin, _ *workspace.AssistantProgramHomeOwner, _ *workspace.AssistantProjectProviderOwner) {
		rows[0] = InstalledPlugin{}
	})
	refuse("project provider missing", func(rows []InstalledPlugin, _ *workspace.AssistantProgramHomeOwner, _ *workspace.AssistantProjectProviderOwner) {
		rows[1] = InstalledPlugin{}
	})
}
