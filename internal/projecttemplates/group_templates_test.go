package projecttemplates

import (
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func groupTemplateProgramSource(pluginID, templateID string) Template {
	return Template{
		ID: templateID, Name: "Research Project", Revision: strings.Repeat("a", 64),
		PluginOwner: &workspace.PluginTemplateOwner{PluginID: pluginID, PluginVersion: "1.0.0", BlueprintID: "research-project", BlueprintVersion: 1},
		AssistantProgram: &workspace.AssistantProgramDeclaration{
			SchemaVersion: workspace.AssistantProgramSchemaVersion, ID: "research-program",
			StationName: "Research Program Home", StationDescription: "Coordinates research projects.",
			Roles: []workspace.AssistantProgramRoleSpec{
				{ID: "coordinator", Label: "Portfolio Coordinator", Scope: workspace.AssistantRoleScopeHome, Required: true, Primary: true, SystemPrompt: "Coordinate."},
				{ID: "lead", Label: "Research Lead", Scope: workspace.AssistantRoleScopeProject, Required: true, Primary: true, SystemPrompt: "Lead."},
				{ID: "curator", Label: "Archive Curator", Scope: workspace.AssistantRoleScopeHome, SystemPrompt: "Curate."},
			},
		},
		GroupRequirement: &GroupRequirement{
			SchemaVersion: GroupRequirementSchemaVersion, Policy: GroupPolicyRequired, AssistantProgramID: "research-program",
			MissingHome: MissingHomeOfferCreate, DefaultHomeName: "Research Program Home",
		},
	}
}

func usable(template Template) GroupTemplateCandidate {
	return GroupTemplateCandidate{Template: template, Usable: true}
}

func managedGroupTemplates(t *testing.T, candidates ...GroupTemplateCandidate) []GroupTemplate {
	t.Helper()
	projected := ProjectGroupTemplates(candidates)
	if len(projected) == 0 || projected[0].ID != GroupTemplateGeneralID || projected[0].Kind != GroupTemplateKindOrdinary ||
		projected[0].Representative != nil || projected[0].Provider != nil {
		t.Fatalf("first entry must be the ordinary General template: %#v", projected)
	}
	return projected[1:]
}

func TestProjectGroupTemplates_ManagedHomeUsesOnlyHomeDeclaration(t *testing.T) {
	managed := managedGroupTemplates(t, usable(groupTemplateProgramSource("fixture", "plugin:fixture:research-project")))
	if len(managed) != 1 {
		t.Fatalf("managed entries = %#v, want one", managed)
	}
	entry := managed[0]
	if entry.Kind != GroupTemplateKindManaged || entry.Name != "Research Program Home" || entry.SourceState != GroupTemplateSourceReady ||
		entry.ProposedGroupName != "Research Program Home" || !strings.HasPrefix(entry.ID, "group-template:") || entry.Revision == "" {
		t.Fatalf("managed entry = %#v", entry)
	}
	if entry.Provider == nil || entry.Provider.Kind != GroupTemplateSourcePlugin || entry.Provider.PluginID != "fixture" || entry.Provider.PluginVersion != "1.0.0" {
		t.Fatalf("provider = %#v", entry.Provider)
	}
	if len(entry.HomeRoles) != 2 || entry.HomeRoles[0].RoleID != "coordinator" || !entry.HomeRoles[0].Required ||
		entry.HomeRoles[1].RoleID != "curator" || entry.HomeRoles[1].Required {
		t.Fatalf("Home roles = %#v, want required coordinator then optional curator", entry.HomeRoles)
	}
	if len(entry.ProjectRoles) != 1 || entry.ProjectRoles[0] != "Research Lead" {
		t.Fatalf("project-local role note = %#v", entry.ProjectRoles)
	}
	if strings.Contains(entry.ID, "fixture") || strings.Contains(entry.ID, "research-program") {
		t.Fatalf("selection ID exposes source identity: %q", entry.ID)
	}
}

func TestProjectGroupTemplates_EligibilityNeverInventsGroupedPolicy(t *testing.T) {
	legacy := groupTemplateProgramSource("legacy", "plugin:legacy:project")
	legacy.GroupRequirement = nil
	none := groupTemplateProgramSource("none", "plugin:none:project")
	none.GroupRequirement = &GroupRequirement{SchemaVersion: GroupRequirementSchemaVersion, Policy: GroupPolicyNone}
	invalid := groupTemplateProgramSource("invalid", "plugin:invalid:project")
	invalid.GroupRequirementError = "invalid group requirement"
	schemaOne := groupTemplateProgramSource("schema-one", "plugin:schema-one:project")
	schemaOne.AssistantProgram.SchemaVersion = workspace.AssistantProgramLegacySchemaVersion
	mismatched := groupTemplateProgramSource("mismatch", "plugin:mismatch:project")
	mismatched.GroupRequirement.AssistantProgramID = "other-program"
	programless := groupTemplateProgramSource("programless", "plugin:programless:project")
	programless.AssistantProgram = nil
	localCopy := groupTemplateProgramSource("", "local-copy")
	localCopy.PluginOwner = nil
	staleVariant := groupTemplateProgramSource("", "stale-variant")
	staleVariant.PluginOwner = nil
	staleVariant.TemplateVariant = &TemplateVariant{Source: TemplateVariantSource{PluginID: "fixture"}}
	staleVariant.VariantSourceState = VariantSourceChanged

	managed := managedGroupTemplates(t,
		usable(legacy), usable(none), usable(invalid), usable(schemaOne), usable(mismatched),
		usable(programless), usable(localCopy), usable(staleVariant),
	)
	if len(managed) != 0 {
		t.Fatalf("ineligible sources produced managed entries: %#v", managed)
	}

	recommended := groupTemplateProgramSource("recommended", "plugin:recommended:project")
	recommended.GroupRequirement.Policy = GroupPolicyRecommended
	if managed := managedGroupTemplates(t, usable(recommended)); len(managed) != 1 {
		t.Fatalf("explicit Recommended policy is grouped and eligible: %#v", managed)
	}
}

func TestProjectGroupTemplates_DeduplicatesVariantsAndKeepsSourceNamespaces(t *testing.T) {
	plugin := groupTemplateProgramSource("fixture", "plugin:fixture:research-project")
	variant := groupTemplateProgramSource("", "my-research-variant")
	variant.PluginOwner = nil
	variant.TemplateVariant = &TemplateVariant{Source: TemplateVariantSource{PluginID: "fixture", PluginVersion: "1.0.0"}}
	variant.GroupRequirement.Policy = GroupPolicyRecommended
	variant.GroupRequirement.DefaultHomeName = "Variant Name Does Not Win"
	sibling := groupTemplateProgramSource("fixture", "plugin:fixture:other-blueprint")
	sibling.AssistantProgram.Roles[1] = workspace.AssistantProgramRoleSpec{ID: "analyst", Label: "Analyst", Scope: workspace.AssistantRoleScopeProject, SystemPrompt: "Analyze."}
	otherPlugin := groupTemplateProgramSource("other-fixture", "plugin:other-fixture:research-project")
	attachment := groupTemplateProgramSource("", "local-research")
	attachment.PluginOwner = nil
	attachment.UserSetupQuest = &UserSetupQuest{AttachmentID: "attachment-1"}

	managed := managedGroupTemplates(t, usable(variant), usable(sibling), usable(plugin), usable(otherPlugin), usable(attachment))
	if len(managed) != 3 {
		t.Fatalf("managed entries = %#v, want fixture plugin, other plugin, and user attachment", managed)
	}
	seen := map[string]GroupTemplate{}
	for _, entry := range managed {
		if _, duplicate := seen[entry.ID]; duplicate {
			t.Fatalf("duplicate selection ID %q", entry.ID)
		}
		seen[entry.ID] = entry
		if entry.SourceState != GroupTemplateSourceReady {
			t.Fatalf("project-role differences must not conflict: %#v", entry)
		}
	}
	var fixture *GroupTemplate
	for index := range managed {
		if managed[index].Provider.Kind == GroupTemplateSourcePlugin && managed[index].Provider.PluginID == "fixture" {
			fixture = &managed[index]
		}
	}
	if fixture == nil || fixture.Representative == nil || fixture.Representative.ID != "plugin:fixture:other-blueprint" ||
		fixture.ProposedGroupName != "Research Program Home" {
		t.Fatalf("fixture representative = %#v, want lowest-ID active plugin blueprint and its declared name", fixture)
	}

	again := managedGroupTemplates(t, usable(attachment), usable(otherPlugin), usable(plugin), usable(sibling), usable(variant))
	for index := range managed {
		if managed[index].ID != again[index].ID || managed[index].Revision != again[index].Revision {
			t.Fatalf("projection is order dependent: %#v / %#v", managed[index], again[index])
		}
	}
}

func TestProjectGroupTemplates_HomeDeclarationConflictFailsClosed(t *testing.T) {
	plugin := groupTemplateProgramSource("fixture", "plugin:fixture:research-project")
	conflicting := groupTemplateProgramSource("fixture", "plugin:fixture:second-blueprint")
	conflicting.AssistantProgram.Roles[0].Required = false

	managed := managedGroupTemplates(t, usable(plugin), usable(conflicting))
	if len(managed) != 1 {
		t.Fatalf("managed entries = %#v", managed)
	}
	entry := managed[0]
	if entry.SourceState != GroupTemplateSourceConflict || entry.Representative != nil || entry.Revision != "" || entry.ProposedGroupName != "" {
		t.Fatalf("conflicting Home declarations = %#v, want non-creatable conflict with no representative", entry)
	}
}

func TestProjectGroupTemplates_UnusableSourcesStayVisibleWithoutCreation(t *testing.T) {
	disabled := GroupTemplateCandidate{Template: groupTemplateProgramSource("fixture", "plugin:fixture:research-project"), UnavailableReason: "plugin_enable_required"}
	managed := managedGroupTemplates(t, disabled)
	if len(managed) != 1 || managed[0].SourceState != GroupTemplateSourceUnavailable || managed[0].SourceReason != "plugin_enable_required" ||
		managed[0].ProposedGroupName != "" || managed[0].Representative == nil {
		t.Fatalf("unusable source = %#v, want visible unavailable entry with a lookup representative", managed)
	}

	existingOnly := groupTemplateProgramSource("existing", "plugin:existing:project")
	existingOnly.GroupRequirement.MissingHome = MissingHomeExistingOnly
	existingOnly.GroupRequirement.DefaultHomeName = ""
	managed = managedGroupTemplates(t, usable(existingOnly))
	if len(managed) != 1 || managed[0].MissingHome != MissingHomeExistingOnly || managed[0].ProposedGroupName != "" {
		t.Fatalf("existing-only source = %#v, want no proposed creation", managed)
	}
}

func TestProjectGroupTemplates_RevisionTracksSourceNotDisplayState(t *testing.T) {
	base := groupTemplateProgramSource("fixture", "plugin:fixture:research-project")
	first := managedGroupTemplates(t, usable(base))[0]

	unusable := managedGroupTemplates(t, GroupTemplateCandidate{Template: base, UnavailableReason: "plugin_enable_required"})[0]
	if unusable.ID != first.ID || unusable.Revision != first.Revision {
		t.Fatalf("availability changed source revision: %#v / %#v", first, unusable)
	}

	updated := groupTemplateProgramSource("fixture", "plugin:fixture:research-project")
	updated.PluginOwner.PluginVersion = "1.1.0"
	if next := managedGroupTemplates(t, usable(updated))[0]; next.ID != first.ID || next.Revision == first.Revision {
		t.Fatalf("plugin update must keep identity and change revision: %#v / %#v", first, next)
	}

	home := groupTemplateProgramSource("fixture", "plugin:fixture:research-project")
	home.AssistantProgram.StationDescription = "Changed Home copy."
	if next := managedGroupTemplates(t, usable(home))[0]; next.Revision == first.Revision || next.HomeDigest == first.HomeDigest {
		t.Fatalf("Home declaration change must change digest and revision: %#v / %#v", first, next)
	}

	projectOnly := groupTemplateProgramSource("fixture", "plugin:fixture:research-project")
	projectOnly.AssistantProgram.Roles[1].SystemPrompt = "Lead differently."
	if next := managedGroupTemplates(t, usable(projectOnly))[0]; next.HomeDigest != first.HomeDigest {
		t.Fatalf("project-role change altered the Home digest: %#v / %#v", first, next)
	}
}

func TestFindGroupTemplateResolvesOnlyProjectedIDs(t *testing.T) {
	projected := ProjectGroupTemplates([]GroupTemplateCandidate{usable(groupTemplateProgramSource("fixture", "plugin:fixture:research-project"))})
	if _, ok := FindGroupTemplate(projected, projected[1].ID); !ok {
		t.Fatal("projected managed ID was not resolved")
	}
	if general, ok := FindGroupTemplate(projected, " general "); !ok || general.Kind != GroupTemplateKindOrdinary {
		t.Fatalf("General lookup = %#v, %v", general, ok)
	}
	for _, forged := range []string{"plugin:fixture:research-project", "group-template:" + strings.Repeat("0", 32), ""} {
		if _, ok := FindGroupTemplate(projected, forged); ok {
			t.Fatalf("forged selection %q resolved", forged)
		}
	}
}
