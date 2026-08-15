package workspacesettings

import (
	"testing"
)

// Switching a workspace's profile picks up that profile's default preset
// (FR-131, FR-133, FR-134).
//
// Found by the end-to-end demo, not by a unit test: every workspace is created
// with `preset: guided` stored explicitly, so switching one to Software Project
// left it on Guided. "Software Project retains Planner as its default" was then
// only true for workspaces created that way — which is not what a default
// means.
//
// The rule has two halves and the second matters as much as the first: a preset
// the user actually picked is never re-derived, because silently undoing
// somebody's choice when they edit a different field is worse than the bug.

func TestSwitchingToSoftwareProjectAdoptsPlanner(t *testing.T) {
	// A freshly created workspace: general profile, guided preset stored.
	shared := Store(map[string]any{}, DefaultSettings())

	_, settings := ApplyPatch(shared, map[string]any{"profile": "software_project"})

	if settings.Preset != "planner" {
		t.Errorf("preset = %q, want planner after switching to software_project", settings.Preset)
	}
	if !settings.Planning.Enabled {
		t.Error("switching to software_project left structured planning disabled")
	}
}

func TestSwitchingToResearchAdoptsItsDefaults(t *testing.T) {
	shared := Store(map[string]any{}, DefaultSettings())

	_, settings := ApplyPatch(shared, map[string]any{"profile": "research"})

	if settings.Preset != "guided" {
		t.Errorf("preset = %q, want guided for research", settings.Preset)
	}
	// Research keeps planning off by default, and its style is investigative
	// when it is enabled.
	if settings.Planning.Enabled {
		t.Error("research enabled structured planning by default")
	}
	if settings.Planning.Mode != "investigation" {
		t.Errorf("planning mode = %q, want investigation", settings.Planning.Mode)
	}
	if settings.Planning.RequireBranch {
		t.Error("research required a branch")
	}
}

// A preset the user chose survives a profile change. Re-deriving it would undo
// their decision because they edited an unrelated field.
func TestAnExplicitlyChosenPresetSurvivesAProfileSwitch(t *testing.T) {
	// The user deliberately selected Autonomous while on general.
	shared, chosen := ApplyPatch(Store(map[string]any{}, DefaultSettings()),
		map[string]any{"preset": "autonomous"})
	if chosen.Preset != "autonomous" {
		t.Fatalf("fixture preset = %q, want autonomous", chosen.Preset)
	}

	_, settings := ApplyPatch(shared, map[string]any{"profile": "software_project"})

	if settings.Preset != "autonomous" {
		t.Errorf("preset = %q; switching profile overwrote a chosen preset", settings.Preset)
	}
}

// Naming both in one patch is what the settings screen does, and it wins.
func TestAnExplicitPresetInThePatchAlwaysWins(t *testing.T) {
	shared := Store(map[string]any{}, DefaultSettings())

	_, settings := ApplyPatch(shared, map[string]any{
		"profile": "software_project",
		"preset":  "minimal",
	})

	if settings.Preset != "minimal" {
		t.Errorf("preset = %q, want the explicitly patched minimal", settings.Preset)
	}
	if settings.Planning.Enabled {
		t.Error("minimal enabled structured planning")
	}
}

// Patching an unrelated field does not disturb the preset.
func TestPatchingSomethingElseLeavesThePresetAlone(t *testing.T) {
	shared, before := ApplyPatch(Store(map[string]any{}, DefaultSettings()),
		map[string]any{"profile": "software_project"})

	_, after := ApplyPatch(shared, map[string]any{
		"task_markdown": map[string]any{"enabled": true},
	})

	if after.Preset != before.Preset {
		t.Errorf("preset changed from %q to %q on an unrelated patch", before.Preset, after.Preset)
	}
}
