package plugin

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/specialist"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func questContribution(t *testing.T) *SurfaceContribution {
	t.Helper()
	c, err := ParseSurfaceContribution(canonicalSurfaceFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	q, err := specialist.NormalizeSetupJourney(projectSetupQuestFixture(c.Blueprints[0].ID))
	if err != nil {
		t.Fatal(err)
	}
	c.RequiresHostFeatures = append(c.RequiresHostFeatures, HostFeatureSetupQuestsV2)
	c.SetupQuests = []SetupQuest{*q}
	return c
}

// projectSetupQuestFixture is a valid four-step plugin quest for one blueprint.
func projectSetupQuestFixture(blueprintID string) specialist.SetupJourney {
	return specialist.SetupJourney{
		SchemaVersion: specialist.SetupJourneySchemaVersion, Version: 2, ID: "demo_setup",
		Title: "Set up the demo", Description: "Connect a demo project and choose how Ori can help.",
		IntegrationKey: "demo_integration", ExpectedBlueprintID: blueprintID, ExpectedAssistantProgramID: "demo-assistant",
		Steps: []specialist.SetupJourneyStep{
			{ID: "project", Kind: specialist.SetupStepProjectConnect, Title: "Connect a project", Description: "Connect a project."},
			{ID: "workspace", Kind: specialist.SetupStepWorkspaceSetup, Title: "Choose how Ori works", Description: "Choose a mode."},
			{ID: "staffing", Kind: specialist.SetupStepAssistantProgramStaffing, Title: "Add your team", Description: "Add roles."},
			{ID: "summary", Kind: specialist.SetupStepSummary, Title: "Review setup", Description: "Review setup."},
		},
		WorkspaceLaunch: &specialist.WorkspaceLaunchCopy{GroupTitle: "Build your group", GroupName: "Demo group"},
	}
}

// FR 7: a v2 four-step quest is accepted; the retired five-step layout and the
// runtime launch copy are rejected, and the error names setup_quests_v2.
func TestSetupQuestContributionAcceptsOnlyTheFourStepV2Shape(t *testing.T) {
	if err := questContribution(t).Validate(); err != nil {
		t.Fatalf("four-step v2 quest rejected: %v", err)
	}

	fiveStep := questContribution(t)
	fiveStep.SetupQuests[0].Steps = append([]specialist.SetupJourneyStep{{
		ID: "integration", Kind: specialist.SetupStepIntegrationInstall, Title: "Install", Description: "Install the plugin.",
	}}, fiveStep.SetupQuests[0].Steps...)
	err := fiveStep.Validate()
	if !ContributionErrorIs(err, CodeContributionInvalid) || !strings.Contains(errors.Unwrap(err).Error(), HostFeatureSetupQuestsV2) {
		t.Fatalf("five-step v2 quest error = %v (cause %v)", err, errors.Unwrap(err))
	}

	data, err := json.Marshal(questContribution(t))
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	launch := document["setup_quests"].([]any)[0].(map[string]any)["workspace_launch"].(map[string]any)
	launch["runtime_title"] = "Set up the app"
	withRuntime, _ := json.Marshal(document)
	if _, err := ParseSurfaceContribution(withRuntime); err == nil {
		t.Fatal("manifest with runtime launch copy was accepted")
	}
}

func TestSetupQuestContributionRequiresHostFeatureAndRejectsExecutableFields(t *testing.T) {
	c := questContribution(t)
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseSurfaceContribution(data)
	if err != nil || len(parsed.SetupQuests) != 1 {
		t.Fatalf("parse=%#v err=%v", parsed, err)
	}
	if err := parsed.ValidateForHost(1, []string{HostFeatureAssistantProgramV1, HostFeatureSpecialistSetupJourneyV1}); !ContributionErrorIs(err, CodeHostFeatureUnsupported) {
		t.Fatalf("older host accepted quest: %v", err)
	}
	for _, field := range []string{"command", "url", "adapter", "owner_plugin_id"} {
		t.Run(field, func(t *testing.T) {
			var document map[string]any
			if err := json.Unmarshal(data, &document); err != nil {
				t.Fatal(err)
			}
			document["setup_quests"].([]any)[0].(map[string]any)[field] = "untrusted"
			malformed, err := json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ParseSurfaceContribution(malformed); err == nil {
				t.Fatalf("accepted %s", field)
			}
		})
	}
	cases := map[string]func(*SurfaceContribution){
		"missing host feature": func(c *SurfaceContribution) { c.RequiresHostFeatures = nil },
		"unowned blueprint":    func(c *SurfaceContribution) { c.SetupQuests[0].ExpectedBlueprintID = "other" },
		"duplicate":            func(c *SurfaceContribution) { c.SetupQuests = append(c.SetupQuests, c.SetupQuests[0]) },
		"too many": func(c *SurfaceContribution) {
			for len(c.SetupQuests) < 9 {
				c.SetupQuests = append(c.SetupQuests, c.SetupQuests[0])
			}
		},
		"no launch":        func(c *SurfaceContribution) { c.SetupQuests[0].WorkspaceLaunch = nil },
		"unsupported step": func(c *SurfaceContribution) { c.SetupQuests[0].Steps[0].Kind = "execute" },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			c := questContribution(t)
			change(c)
			if err := c.Validate(); err == nil {
				t.Fatal("invalid declaration accepted")
			}
		})
	}
}

// Host account-link and install quests are valid host shapes, but a plugin may
// author only the four-step project_setup shape.
func TestSetupQuestContributionRejectsHostShapes(t *testing.T) {
	c := questContribution(t)
	install, err := specialist.NormalizeSetupJourney(specialist.SetupJourney{
		SchemaVersion: specialist.SetupJourneySchemaVersion, Version: 1, ID: "install_demo_integration",
		Title: "Install the demo plugin", Description: "Install the demo plugin.",
		IntegrationKey: "demo_integration", ExpectedBlueprintID: c.Blueprints[0].ID, ExpectedAssistantProgramID: "demo-assistant",
		Steps: []specialist.SetupJourneyStep{
			{ID: "integration", Kind: specialist.SetupStepIntegrationInstall, Title: "Install", Description: "Install the plugin."},
			{ID: "summary", Kind: specialist.SetupStepSummary, Title: "Ready", Description: "Continue setup."},
		},
	})
	if err != nil {
		t.Fatalf("host normalizer rejected the install fixture: %v", err)
	}
	c.SetupQuests = []SetupQuest{*install}
	if err := c.Validate(); err == nil {
		t.Fatal("plugin contribution accepted an install-shape setup quest")
	}

	c = questContribution(t)
	accountLink, err := specialist.NormalizeSetupJourney(specialist.SetupJourney{
		SchemaVersion: specialist.SetupJourneySchemaVersion, Version: 1, ID: "plugin_account_setup",
		Title: "Set up an account", Description: "Create a workspace and link an account.",
		ExpectedBlueprintID: c.Blueprints[0].ID,
		Steps: []specialist.SetupJourneyStep{
			{ID: "team", Kind: specialist.SetupStepWorkspaceCreate, Title: "Team", Description: "Create the workspace."},
			{ID: "connect", Kind: specialist.SetupStepAccountConnect, Title: "Connect", Description: "Connect the account."},
			{ID: "link", Kind: specialist.SetupStepAccountLink, Title: "Link", Description: "Link the account."},
			{ID: "summary", Kind: specialist.SetupStepSummary, Title: "Summary", Description: "Review completion."},
		},
	})
	if err != nil {
		t.Fatalf("host normalizer rejected the account-link fixture: %v", err)
	}
	c.SetupQuests = []SetupQuest{*accountLink}
	if err := c.Validate(); err == nil {
		t.Fatal("plugin contribution accepted an account-link-shape setup quest")
	}
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseSurfaceContribution(data); err == nil {
		t.Fatal("plugin manifest parse accepted an account-link-shape setup quest")
	}
}

// FR 8: the published plugin schema is exactly the four fixed project_setup
// steps and a group-only launch object; the v1 schema is gone.
func TestSetupQuestSchemaIsTheFourFixedSteps(t *testing.T) {
	if _, err := os.Stat(filepath.Join("schema", "setup-quest-v1.schema.json")); !os.IsNotExist(err) {
		t.Fatalf("retired v1 schema still exists: %v", err)
	}
	data, err := os.ReadFile(filepath.Join("schema", "setup-quest-v2.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties struct {
			WorkspaceLaunch struct {
				Required   []string                   `json:"required"`
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"workspace_launch"`
			Steps struct {
				MinItems    int `json:"minItems"`
				MaxItems    int `json:"maxItems"`
				PrefixItems []struct {
					Properties struct {
						Kind struct {
							Const string `json:"const"`
						} `json:"kind"`
					} `json:"properties"`
				} `json:"prefixItems"`
				Items *bool `json:"items"`
			} `json:"steps"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	want := specialist.SetupJourneyShapeSteps(specialist.SetupJourneyShapeProjectSetup)
	steps := schema.Properties.Steps
	if steps.MinItems != len(want) || steps.MaxItems != len(want) || steps.Items == nil || *steps.Items {
		t.Fatalf("schema step bounds changed: %+v", steps)
	}
	launch := schema.Properties.WorkspaceLaunch
	if len(launch.Properties) != 2 || launch.Properties["group_title"] == nil || launch.Properties["group_name"] == nil || len(launch.Required) != 2 {
		t.Fatalf("schema launch copy = %+v", launch)
	}
	surface, err := os.ReadFile(filepath.Join("schema", "workspace-surface-v1.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(surface), `"setup-quest-v2.schema.json"`) || !strings.Contains(string(surface), `"setup_quests_v2"`) ||
		strings.Contains(string(surface), "setup_quests_v1") {
		t.Fatal("workspace surface schema does not reference the v2 quest contract")
	}
	if len(steps.PrefixItems) != len(want) {
		t.Fatalf("schema declares %d steps, want %d", len(steps.PrefixItems), len(want))
	}
	for index, item := range steps.PrefixItems {
		if specialist.SetupStepKind(item.Properties.Kind.Const) != want[index] {
			t.Fatalf("schema step %d kind = %q, want %q", index, item.Properties.Kind.Const, want[index])
		}
	}
}

func TestQuestBlueprintReferencesStayWithinTheirOwnerAndProgram(t *testing.T) {
	c := questContribution(t)
	q := c.SetupQuests[0]
	makeBlueprint := func() []ResolvedBlueprint {
		return []ResolvedBlueprint{{ID: q.ExpectedBlueprintID, Template: projecttemplates.Template{
			SetupQuestID: q.ID, AssistantProgram: &workspace.AssistantProgramDeclaration{ID: q.ExpectedAssistantProgramID},
			ProjectConnection: &projecttemplates.ProjectConnectionDeclaration{SchemaVersion: 1, SupportedModes: []projecttemplates.ProjectConnectionMode{projecttemplates.ProjectConnectionNewProject}},
		}}}
	}
	if err := validateQuestBlueprints(c, makeBlueprint()); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func([]ResolvedBlueprint){
		"foreign quest":          func(b []ResolvedBlueprint) { b[0].Template.SetupQuestID = "other" },
		"missing reference":      func(b []ResolvedBlueprint) { b[0].Template.SetupQuestID = "" },
		"wrong blueprint":        func(b []ResolvedBlueprint) { b[0].ID = "other" },
		"wrong program":          func(b []ResolvedBlueprint) { b[0].Template.AssistantProgram.ID = "other" },
		"no program":             func(b []ResolvedBlueprint) { b[0].Template.AssistantProgram = nil },
		"no project declaration": func(b []ResolvedBlueprint) { b[0].Template.ProjectConnection = nil },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			b := makeBlueprint()
			change(b)
			if err := validateQuestBlueprints(c, b); err == nil {
				t.Fatal("invalid reference accepted")
			}
		})
	}
	// Exercise the actual strict manifest -> template -> resolution path too.
	descriptor := blueprintDescriptorFixture(t)
	path := filepath.Join(descriptor.InstallDir, "blueprints", "demo", "template.json")
	data, err := os.ReadFile(path) // #nosec G304 -- fixture path under t.TempDir
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, strings.Replace(string(data), `"name":"Demo Workspace"`, `"name":"Demo Workspace", "setup_quest":"unowned"`, 1))
	if _, err := ResolvePluginBlueprints(descriptor); err == nil {
		t.Fatal("resolver accepted a template's foreign quest")
	}
}
