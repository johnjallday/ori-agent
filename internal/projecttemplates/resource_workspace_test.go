package projecttemplates

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func resourceContractFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "resourcecatalog", "testdata", "contract", name))
	if err != nil {
		t.Fatalf("read contract fixture %s: %v", name, err)
	}
	return data
}

func songAttachment() AssistantProgramAllowedProjectAttachment {
	return AssistantProgramAllowedProjectAttachment{
		ProviderPluginID: "reaper-plugin", BlueprintID: "reaper-song", ProjectTeamID: "reaper-song-team",
		ProjectTeamSchemaVersion: 1, MinProjectTeamVersion: 1, MaxProjectTeamVersion: 1,
	}
}

func studioAttachment(t *testing.T) AssistantProgramAllowedProjectAttachment {
	t.Helper()
	var attachment AssistantProgramAllowedProjectAttachment
	if err := decodeStrictDeclaration(resourceContractFixture(t, "home-attachment.valid.json"), &attachment); err != nil {
		t.Fatalf("decode attachment fixture: %v", err)
	}
	return attachment
}

func resourceTestHome(attachments ...AssistantProgramAllowedProjectAttachment) AssistantProgramHome {
	return AssistantProgramHome{
		SchemaVersion: 1, Version: 1, ID: "music-producer-assistant", StationName: "Music Production Home",
		DefaultPrimaryName: "Portfolio Manager", HireTitle: "Staff your Home",
		Roles:  []AssistantProgramHomeRole{{ID: "portfolio_manager", Label: "Portfolio Manager", Required: true, Primary: true, Role: "orchestrator", SystemPrompt: "Coordinate reviewed Home work."}},
		Stages: []workspace.AssistantProgramStageSpec{{ID: "foundation", Label: "Foundation"}},
		Reflection: workspace.AssistantReflectionConfig{MinimumProjects: 3, CadenceHours: 24, MaxProjects: 3, MaxEventsPerProject: 1,
			MaxCandidates: 1, MaxEvidence: 3, Rubric: "Find stable preferences."},
		AllowedProjectAttachments: attachments,
	}
}

func TestResourceAttachment_NormalizesTheStudioEntryAfterTheSongEntry(t *testing.T) {
	home := resourceTestHome(songAttachment(), studioAttachment(t))
	if err := NormalizeAssistantProgramHome(&home); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	studio, ok := home.ResourceAttachment()
	if !ok || studio.BlueprintID != "reaper-studio" || studio.Label != "REAPER Studio" || studio.Description == "" {
		t.Fatalf("resource attachment = %+v, %v", studio, ok)
	}
	if home.AllowedProjectAttachments[0].IsResource() {
		t.Fatal("the song entry became a resource entry")
	}
}

func TestResourceAttachment_LeavesAnExistingHomeDigestUnchanged(t *testing.T) {
	plain := resourceTestHome(songAttachment())
	if err := NormalizeAssistantProgramHome(&plain); err != nil {
		t.Fatal(err)
	}
	explicit := resourceTestHome(songAttachment())
	explicit.AllowedProjectAttachments[0].WorkspaceKind = "project"
	if err := NormalizeAssistantProgramHome(&explicit); err != nil {
		t.Fatal(err)
	}
	if AssistantProgramHomeDigest(plain) != AssistantProgramHomeDigest(explicit) {
		t.Fatal("an explicit project kind changed the Home digest")
	}
	encoded, err := json.Marshal(plain.AllowedProjectAttachments[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"workspace_kind", "label", "description"} {
		if strings.Contains(string(encoded), key) {
			t.Fatalf("a project entry serializes %q: %s", key, encoded)
		}
	}
}

func TestResourceAttachment_RefusesEveryMalformedEntry(t *testing.T) {
	cases := map[string]func(*AssistantProgramAllowedProjectAttachment, *AssistantProgramHome){
		"unknown kind": func(a *AssistantProgramAllowedProjectAttachment, _ *AssistantProgramHome) {
			a.WorkspaceKind = "library"
		},
		"resource without a label":     func(a *AssistantProgramAllowedProjectAttachment, _ *AssistantProgramHome) { a.Label = " " },
		"resource without description": func(a *AssistantProgramAllowedProjectAttachment, _ *AssistantProgramHome) { a.Description = "" },
		"label too long": func(a *AssistantProgramAllowedProjectAttachment, _ *AssistantProgramHome) {
			a.Label = strings.Repeat("Studio ", 10)
		},
		"text-reordering mark": func(a *AssistantProgramAllowedProjectAttachment, _ *AssistantProgramHome) {
			a.Description = "Your studio " + string(rune(0x202e)) + "catalog"
		},
		"multi-line description": func(a *AssistantProgramAllowedProjectAttachment, _ *AssistantProgramHome) { a.Description = "One\nTwo" },
		"URL in description": func(a *AssistantProgramAllowedProjectAttachment, _ *AssistantProgramHome) {
			a.Description = "See https://example.com"
		},
		"project entry with a label": func(_ *AssistantProgramAllowedProjectAttachment, home *AssistantProgramHome) {
			home.AllowedProjectAttachments[0].Label = "Songs"
		},
		"two resource entries": func(_ *AssistantProgramAllowedProjectAttachment, home *AssistantProgramHome) {
			second := home.AllowedProjectAttachments[1]
			second.BlueprintID, second.ProjectTeamID = "reaper-vault", "reaper-vault-team"
			home.AllowedProjectAttachments = append(home.AllowedProjectAttachments, second)
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			home := resourceTestHome(songAttachment(), studioAttachment(t))
			mutate(&home.AllowedProjectAttachments[1], &home)
			if err := NormalizeAssistantProgramHome(&home); !errors.Is(err, ErrInvalidAssistantProgram) {
				t.Fatalf("err = %v, want ErrInvalidAssistantProgram", err)
			}
		})
	}
}

func TestResourceAttachment_AcceptsOnlyAMatchingKind(t *testing.T) {
	studio := studioAttachment(t)
	studio.WorkspaceKind, _ = normalizeWorkspaceKind(studio.WorkspaceKind)
	if !studio.Accepts("reaper-plugin", "reaper-studio", "reaper-studio-team", 1, 1, workspace.AssistantWorkspaceKindResource) {
		t.Fatal("the resource entry refused its own resource team")
	}
	if studio.Accepts("reaper-plugin", "reaper-studio", "reaper-studio-team", 1, 1, "") {
		t.Fatal("the resource entry accepted a project team with the same identity")
	}
	if songAttachment().Accepts("reaper-plugin", "reaper-song", "reaper-song-team", 1, 1, workspace.AssistantWorkspaceKindResource) {
		t.Fatal("the song entry accepted a resource team")
	}
}

func TestResourceWorkspaceTemplate_LoadsTheStudioBlueprint(t *testing.T) {
	manifestPath, skeleton := writePluginBlueprint(t, string(resourceContractFixture(t, "studio-template.valid.json")))
	template, _, err := LoadPluginBlueprint(manifestPath, skeleton, defaultRuntimeCatalog())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	project := template.AssistantProject
	if !project.IsResource() || project.ID != "reaper-studio-team" || len(project.Roles) != 1 {
		t.Fatalf("assistant project = %+v", project)
	}
	if template.ProjectEntry != nil || template.ProjectConnection != nil || len(template.StarterTasks) != 0 {
		t.Fatalf("resource template has song components: %+v", template)
	}
}

func TestResourceWorkspaceTemplate_RefusesSongComponentsAndStandalonePlacement(t *testing.T) {
	var base map[string]json.RawMessage
	if err := json.Unmarshal(resourceContractFixture(t, "studio-template.valid.json"), &base); err != nil {
		t.Fatal(err)
	}
	cases := map[string]map[string]string{
		"project file":      {"project_entry": `{"relative_path":"{{name}}.rpp","open_after_create_default":true}`},
		"song inputs":       {"inputs": `{"schema_version":1,"apply_to":[],"fields":[]}`},
		"starter tasks":     {"starter_tasks": `[{"title":"Start a song","description":"Pick a tempo."}]`},
		"setup quest":       {"setup_quest": `"reaper_setup"`},
		"runtime":           {"runtime_requirements": `{"schema_version":1}`},
		"standalone":        {"standalone_composition": `{"schema_version":2,"project_roles":["reaper-studio-assistant"]}`},
		"recommended Home":  {"group_requirement": `{"schema_version":2,"policy":"recommended","assistant_project_id":"reaper-studio-team","missing_home":"existing_only"}`},
		"Home created here": {"group_requirement": `{"schema_version":2,"policy":"required","assistant_project_id":"reaper-studio-team","missing_home":"offer_create","default_home_name":"Music Production Home"}`},
	}
	for name, overrides := range cases {
		t.Run(name, func(t *testing.T) {
			manifest := make(map[string]json.RawMessage, len(base)+len(overrides))
			for key, value := range base {
				manifest[key] = value
			}
			for key, value := range overrides {
				manifest[key] = json.RawMessage(value)
			}
			encoded, err := json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			manifestPath, skeleton := writePluginBlueprint(t, string(encoded))
			if _, _, err := LoadPluginBlueprint(manifestPath, skeleton, defaultRuntimeCatalog()); err == nil {
				t.Fatal("a resource blueprint with a song component or standalone placement loaded")
			}
		})
	}
}
