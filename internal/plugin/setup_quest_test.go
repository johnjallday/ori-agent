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
