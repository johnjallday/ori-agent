package workspacesettings

import (
	"testing"
)

// Legacy planning bindings have zero effect on policy (FR-183, FR-184, FR-188).
//
// A workspace upgraded from the old design may still carry a
// `workspace-planning` skill binding with config in it: mode, require_branch,
// tasks_dir. Those values are not migrated and are not consulted. There is no
// fallback path and no import step — the clean break is the point, because a
// half-migrated policy is one nobody can reason about.
//
// The property is structural: BuildEffectivePolicy takes Settings and
// WorkspaceCapabilities and nothing else. A skill binding cannot reach it
// because there is no parameter it could arrive through. These tests hold that
// signature honest.

// A workspace carrying a legacy planning binding produces exactly the same
// policy as one without it.
//
// The binding below contradicts every setting — planning off, no branch, auto
// execution, a different tasks dir. If any of it were still consulted, the two
// policies would diverge.
func TestLegacyBindingValuesCannotChangePolicy(t *testing.T) {
	caps := WorkspaceCapabilities{HasFolder: true, IsRepository: true, CurrentBranch: "feature/x"}

	stored := map[string]any{
		SharedDataKey: map[string]any{
			"profile": "software_project",
			"preset":  "planner",
		},
	}
	upgraded := map[string]any{
		SharedDataKey: map[string]any{
			"profile": "software_project",
			"preset":  "planner",
		},
		// The remnant an upgraded workspace still carries on disk.
		"skill_bindings": []any{map[string]any{
			"id":         "sb-legacy",
			"skill_name": "workspace-planning",
			"enabled":    true,
			"config": map[string]any{
				"profile_type":           "workspace_planning",
				"mode":                   "investigation",
				"require_branch":         false,
				"default_execution_mode": "auto",
				"tasks_dir":              "somewhere-else",
				"write_prd":              false,
			},
		}},
	}

	clean := BuildEffectivePolicy(Extract(stored), caps)
	afterUpgrade := BuildEffectivePolicy(Extract(upgraded), caps)

	if len(clean.Enforced) != len(afterUpgrade.Enforced) {
		t.Fatalf("control counts differ: %d vs %d", len(clean.Enforced), len(afterUpgrade.Enforced))
	}
	for index := range clean.Enforced {
		if clean.Enforced[index] != afterUpgrade.Enforced[index] {
			t.Errorf("legacy binding changed %s: %+v vs %+v",
				clean.Enforced[index].Key, clean.Enforced[index], afterUpgrade.Enforced[index])
		}
	}
	if clean.Guidance.Style != afterUpgrade.Guidance.Style {
		t.Errorf("legacy binding changed the planning style: %q vs %q",
			clean.Guidance.Style, afterUpgrade.Guidance.Style)
	}
	if clean.PlanningEnabled != afterUpgrade.PlanningEnabled {
		t.Errorf("legacy binding changed whether planning is enabled: %t vs %t",
			clean.PlanningEnabled, afterUpgrade.PlanningEnabled)
	}
}

// The settings themselves are the only source of policy (FR-123). This asserts
// the seam at the level that matters: change a SETTING and the policy moves;
// there is no other lever.
func TestOnlySettingsMovePolicy(t *testing.T) {
	caps := WorkspaceCapabilities{HasFolder: true, IsRepository: true, CurrentBranch: "feature/x"}

	withBranch := PresetDefaultsForProfile("software_project", "planner")
	withoutBranch := PresetDefaultsForProfile("software_project", "planner")
	withoutBranch.Planning.RequireBranch = false

	on, found := BuildEffectivePolicy(withBranch, caps).Control(ControlSafeBranch)
	if !found || !on.Active() {
		t.Fatal("the branch control is not active with the setting on")
	}
	off, found := BuildEffectivePolicy(withoutBranch, caps).Control(ControlSafeBranch)
	if !found {
		t.Fatal("the branch control vanished")
	}
	if off.Active() {
		t.Error("the branch control stayed active with the setting off")
	}
}

// Normalizing settings never invents planning values from anywhere else. A
// workspace whose stored settings are empty gets defaults, not remnants.
func TestEmptySettingsNormalizeToDefaultsNotRemnants(t *testing.T) {
	normalized := Normalize(Settings{})
	defaults := DefaultSettings()

	if normalized.Planning.Mode != defaults.Planning.Mode {
		t.Errorf("mode = %q, want the default %q", normalized.Planning.Mode, defaults.Planning.Mode)
	}
	if normalized.Planning.Enabled != defaults.Planning.Enabled {
		t.Errorf("enabled = %t, want the default %t",
			normalized.Planning.Enabled, defaults.Planning.Enabled)
	}
}
