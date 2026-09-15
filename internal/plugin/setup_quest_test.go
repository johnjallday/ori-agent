package plugin

import (
	"encoding/json"
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
	entry, _ := specialist.Get("music_production")
	q, err := specialist.NormalizeSetupJourney(*entry.SetupJourney)
	if err != nil {
		t.Fatal(err)
	}
	q.ExpectedBlueprintID = c.Blueprints[0].ID
	c.RequiresHostFeatures = append(c.RequiresHostFeatures, HostFeatureSetupQuestsV1)
	c.SetupQuests = []SetupQuest{*q}
	return c
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

// FR 4: host account-link quests are a valid host shape, but a plugin may
// author only the five-step specialist shape.
func TestSetupQuestContributionRejectsHostAccountLinkShape(t *testing.T) {
	c := questContribution(t)
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

// The published plugin schema stays exactly the five fixed specialist steps.
func TestSetupQuestSchemaKeepsTheFiveFixedSteps(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("schema", "setup-quest-v1.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties struct {
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
	steps := schema.Properties.Steps
	if steps.MinItems != specialist.SetupJourneyRequiredSteps || steps.MaxItems != specialist.SetupJourneyRequiredSteps ||
		steps.Items == nil || *steps.Items {
		t.Fatalf("schema step bounds changed: %+v", steps)
	}
	want := specialist.SetupJourneyShapeSteps(specialist.SetupJourneyShapeSpecialist)
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
