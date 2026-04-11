package workspacesettings

import "testing"

func TestExtractReturnsDefaultsWhenMissing(t *testing.T) {
	settings := Extract(nil)
	if settings.Profile != "general" {
		t.Fatalf("expected general profile, got %q", settings.Profile)
	}
	if settings.Preset != "guided" {
		t.Fatalf("expected guided preset, got %q", settings.Preset)
	}
	if settings.Workflow.Mode != "guided" {
		t.Fatalf("expected guided workflow mode, got %q", settings.Workflow.Mode)
	}
	if settings.Planning.Enabled {
		t.Fatal("expected planning to be disabled by default")
	}
}

func TestProfileDefaultsForSoftwareProjectUsePlannerPreset(t *testing.T) {
	settings := ProfileDefaults("software_project")
	if settings.Profile != "software_project" {
		t.Fatalf("expected software_project profile, got %q", settings.Profile)
	}
	if settings.Preset != "planner" {
		t.Fatalf("expected planner preset, got %q", settings.Preset)
	}
	if !settings.Planning.Enabled {
		t.Fatal("expected software_project profile to enable planning by default")
	}
}

func TestApplyPatchUsesPresetAsNewBase(t *testing.T) {
	sharedData := map[string]interface{}{
		SharedDataKey: map[string]interface{}{
			"profile": "software_project",
			"preset":  "guided",
			"workflow": map[string]interface{}{
				"require_repo_scan": false,
			},
		},
	}

	updatedSharedData, settings := ApplyPatch(sharedData, map[string]interface{}{
		"preset": "planner",
		"workflow": map[string]interface{}{
			"save_outputs_as_notes": false,
		},
	})

	if settings.Preset != "planner" {
		t.Fatalf("expected planner preset, got %q", settings.Preset)
	}
	if settings.Profile != "software_project" {
		t.Fatalf("expected software_project profile to be preserved, got %q", settings.Profile)
	}
	if !settings.Workflow.RequireRepoScan {
		t.Fatal("expected planner preset to enable repo scan")
	}
	if settings.Workflow.SaveOutputsAsNotes {
		t.Fatal("expected patch to override planner preset save_outputs_as_notes")
	}

	raw := ExtractRaw(updatedSharedData)
	if rawPreset := raw["preset"]; rawPreset != "planner" {
		t.Fatalf("expected raw preset planner, got %#v", rawPreset)
	}
}

func TestBuildEffectiveBehaviorIncludesPlanningManagedSkill(t *testing.T) {
	settings := PresetDefaultsForProfile("software_project", "planner")
	effective := BuildEffectiveBehavior(settings)
	if len(effective.ManagedSkills) != 1 {
		t.Fatalf("expected one managed skill, got %#v", effective.ManagedSkills)
	}
	if effective.ManagedSkills[0].SkillName != "workspace-planning" {
		t.Fatalf("expected workspace-planning managed skill, got %#v", effective.ManagedSkills[0])
	}
	if effective.ManagedSkills[0].Config["sync_workspace_tasks"] != true {
		t.Fatalf("expected sync_workspace_tasks true, got %#v", effective.ManagedSkills[0].Config["sync_workspace_tasks"])
	}
}
