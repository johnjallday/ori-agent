package projecttemplates

import (
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func independentHomeFixture() AssistantProgramHome {
	return AssistantProgramHome{
		SchemaVersion: 1, Version: 1, ID: "music-producer-assistant",
		StationName: "Music Production Home", StationDescription: "Portfolio coordination.",
		DefaultPrimaryName: "Portfolio Manager", HireTitle: "Staff this Home",
		Roles: []AssistantProgramHomeRole{
			{ID: "portfolio_manager", Label: "Portfolio Manager", Required: true, Primary: true, SystemPrompt: "Coordinate reviewed work.", Skills: []string{"music-project-management"}},
			{ID: "sample_library_manager", Label: "Sample Library Manager", CapabilityID: "sample-library", SystemPrompt: "Use reviewed sample operations."},
		},
		Stages:     []workspace.AssistantProgramStageSpec{{ID: "foundation", Label: "Foundation", AcceptedCompletionThreshold: 0}},
		Reflection: workspace.AssistantReflectionConfig{MinimumProjects: 3, CadenceHours: 168, MaxProjects: 8, MaxEventsPerProject: 8, MaxCandidates: 8, MaxEvidence: 8, Rubric: "Use approved evidence."},
		AllowedProjectAttachments: []AssistantProgramAllowedProjectAttachment{{
			ProviderPluginID: "reaper-plugin", BlueprintID: "reaper-song", ProjectTeamID: "reaper-song-team",
			ProjectTeamSchemaVersion: 1, MinProjectTeamVersion: 1, MaxProjectTeamVersion: 1,
		}},
	}
}

func independentHomeOwner() workspace.AssistantProgramHomeOwner {
	return workspace.AssistantProgramHomeOwner{
		PluginID: "music-project-management", PluginVersion: "0.1.0", ProgramID: "music-producer-assistant",
		HomeSchemaVersion: 1, HomeVersion: 1, DeclarationDigest: strings.Repeat("1", 64),
		PluginGeneration: 1, ComponentFingerprint: strings.Repeat("2", 64),
	}
}

func TestAssistantProgramHomeNormalizesHomeRolesWithoutProjectRole(t *testing.T) {
	home := independentHomeFixture()
	if err := NormalizeAssistantProgramHome(&home); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	declaration := home.AssistantProgram()
	if len(declaration.Roles) != 2 {
		t.Fatalf("roles = %+v", declaration.Roles)
	}
	for _, role := range declaration.Roles {
		if role.Scope != workspace.AssistantRoleScopeHome {
			t.Fatalf("role %q scope = %q", role.ID, role.Scope)
		}
	}
}

func TestAssistantProgramHomeAcceptsMaximumRoleCount(t *testing.T) {
	home := independentHomeFixture()
	for index := len(home.Roles); index < workspace.AssistantProgramMaxRoles; index++ {
		home.Roles = append(home.Roles, AssistantProgramHomeRole{
			ID: "home_role_" + string(rune('a'+index)), Label: "Home Role", Required: true, SystemPrompt: "Coordinate reviewed work.",
		})
	}
	if err := NormalizeAssistantProgramHome(&home); err != nil {
		t.Fatalf("maximum Home roles rejected: %v", err)
	}
	if len(home.Roles) != workspace.AssistantProgramMaxRoles {
		t.Fatalf("role count = %d", len(home.Roles))
	}
}

func TestAssistantProgramHomeProjectsManagedGroupWithoutBlueprint(t *testing.T) {
	home := independentHomeFixture()
	if err := NormalizeAssistantProgramHome(&home); err != nil {
		t.Fatal(err)
	}
	owner := independentHomeOwner()
	owner.DeclarationDigest = AssistantProgramHomeDigest(home)
	template := AssistantProgramHomeTemplate(home, owner)
	if template.PluginOwner != nil || template.HasSkeleton || template.ProjectConnection != nil || len(template.Agents) != 0 || len(template.Capabilities) != 0 {
		t.Fatalf("Home carrier gained project authority: %+v", template)
	}
	projected := ProjectGroupTemplates([]GroupTemplateCandidate{{Template: template, Usable: true}})
	if len(projected) != 2 {
		t.Fatalf("projected = %+v", projected)
	}
	managed := projected[1]
	if managed.Name != home.StationName || managed.Provider == nil || managed.Provider.PluginID != owner.PluginID || managed.Representative == nil || len(managed.ProjectRoles) != 0 ||
		len(managed.HomeRoles) != 2 || len(managed.HomeRoles[0].Skills) != 1 || managed.HomeRoles[0].Skills[0] != "music-project-management" {
		t.Fatalf("managed Home = %+v", managed)
	}
	key := workspace.AssistantProgramKey{OwnerUserID: "owner", PluginID: owner.PluginID, ProgramID: home.ID}
	if managed.ID != GroupTemplateIDForKey(key) {
		t.Fatalf("group template id %q does not match key", managed.ID)
	}
}

func TestAssistantProgramHomeRejectsDuplicateAuthorizationAndSkillIdentifiers(t *testing.T) {
	home := independentHomeFixture()
	home.AllowedProjectAttachments = append(home.AllowedProjectAttachments, home.AllowedProjectAttachments[0])
	if err := NormalizeAssistantProgramHome(&home); err == nil {
		t.Fatal("expected duplicate attachment rejection")
	}
	home = independentHomeFixture()
	home.Roles[0].Skills = append(home.Roles[0].Skills, home.Roles[0].Skills[0])
	if err := NormalizeAssistantProgramHome(&home); err == nil {
		t.Fatal("expected duplicate skill rejection")
	}
}
