package workspacesettings

import (
	"strings"
	"testing"
)

// Upgrade behavior for a workspace carrying the old planning binding
// (FR-185–FR-188, SM-8).
//
// Four properties, and they pull against each other in a way worth being
// explicit about: the values must have no effect, the user must still be told,
// the telling must happen once, and cleaning up must not take anything else
// with it.

func legacyBinding(config map[string]any) map[string]any {
	return map[string]any{
		"id":         "sb-legacy",
		"skill_name": "workspace-planning",
		"enabled":    true,
		"config":     config,
	}
}

func plainBinding(id, name string) map[string]any {
	return map[string]any{
		"id":         id,
		"skill_name": name,
		"enabled":    true,
	}
}

// --- The notice appears, once (FR-185, FR-186) -----------------------------

func TestAnUpgradedWorkspaceIsToldOnce(t *testing.T) {
	bindings := []map[string]any{
		legacyBinding(map[string]any{"mode": "feature", "require_branch": true}),
	}

	first := DetectLegacyPlanningBinding(map[string]any{}, bindings)
	if !first.ShouldShow() {
		t.Fatal("an upgraded workspace was not told its binding is unsupported")
	}
	if first.BindingID != "sb-legacy" {
		t.Errorf("binding id = %q, want sb-legacy", first.BindingID)
	}

	// The notice says nothing was migrated and points at where policy lives.
	if !strings.Contains(first.Message, "no longer used") {
		t.Errorf("the notice does not say the settings are unused: %q", first.Message)
	}
	if !strings.Contains(first.Message, "Nothing was migrated") {
		t.Errorf("the notice does not say nothing was migrated: %q", first.Message)
	}
	if first.SettingsPath == "" {
		t.Error("the notice does not link to Workspace Settings")
	}

	// After acknowledgement it stops appearing.
	acked := AcknowledgeLegacyPlanningNotice(map[string]any{})
	second := DetectLegacyPlanningBinding(acked, bindings)
	if second.ShouldShow() {
		t.Error("the notice reappeared after being acknowledged")
	}
	// It is still detected — the binding is still there — just not shown again.
	if !second.Present {
		t.Error("acknowledging the notice hid the fact that the binding exists")
	}
}

// A workspace with no legacy binding is told nothing.
func TestACleanWorkspaceGetsNoNotice(t *testing.T) {
	notice := DetectLegacyPlanningBinding(map[string]any{}, []map[string]any{
		plainBinding("sb-1", "file-janitor"),
	})
	if notice.Present || notice.ShouldShow() {
		t.Errorf("a clean workspace was told about a legacy binding: %+v", notice)
	}
}

// A workspace-planning binding with NO config was never carrying policy. It is
// an ordinary skill binding and gets no notice — and, below, is not discarded.
func TestABindingWithNoConfigIsNotLegacyPolicy(t *testing.T) {
	notice := DetectLegacyPlanningBinding(map[string]any{}, []map[string]any{
		plainBinding("sb-1", "workspace-planning"),
	})
	if notice.Present {
		t.Error("a config-less planning binding was reported as legacy policy")
	}
}

// --- Discarding takes only the one binding (FR-187) ------------------------

func TestSavingDiscardsOnlyTheLegacyPlanningBinding(t *testing.T) {
	bindings := []map[string]any{
		plainBinding("sb-keep-1", "file-janitor"),
		legacyBinding(map[string]any{"mode": "feature", "tasks_dir": "tasks"}),
		plainBinding("sb-keep-2", "refresh-readme"),
		plainBinding("sb-keep-3", "workspace-planning"), // no config: ordinary
	}

	kept, discarded := DiscardLegacyPlanningBinding(bindings)
	if !discarded {
		t.Fatal("the legacy binding was not discarded")
	}
	if len(kept) != 3 {
		t.Fatalf("kept %d bindings, want 3: %+v", len(kept), kept)
	}

	keptIDs := map[string]bool{}
	for _, binding := range kept {
		keptIDs[stringValue(binding["id"])] = true
	}
	for _, id := range []string{"sb-keep-1", "sb-keep-2", "sb-keep-3"} {
		if !keptIDs[id] {
			t.Errorf("unrelated binding %s was removed", id)
		}
	}
	if keptIDs["sb-legacy"] {
		t.Error("the legacy binding survived")
	}
}

// Discarding is a no-op for a workspace that never had one, so an ordinary save
// does not rewrite bindings it had no reason to touch.
func TestDiscardingIsANoOpWithoutALegacyBinding(t *testing.T) {
	bindings := []map[string]any{
		plainBinding("sb-1", "file-janitor"),
		plainBinding("sb-2", "refresh-readme"),
	}

	kept, discarded := DiscardLegacyPlanningBinding(bindings)
	if discarded {
		t.Error("discarding reported a change with no legacy binding present")
	}
	if len(kept) != len(bindings) {
		t.Errorf("kept %d bindings, want %d", len(kept), len(bindings))
	}
}

// --- Detection never reads the values (FR-184, FR-188) ---------------------

// The notice carries the binding's ID and nothing from its config. A message
// that quoted the old values would be one step from offering to apply them,
// and there is deliberately no migration path.
func TestTheNoticeQuotesNoneOfTheOldValues(t *testing.T) {
	notice := DetectLegacyPlanningBinding(map[string]any{}, []map[string]any{
		legacyBinding(map[string]any{
			"mode":                   "investigation",
			"tasks_dir":              "somewhere-else",
			"default_execution_mode": "auto",
			"require_branch":         false,
		}),
	})

	for _, value := range []string{"investigation", "somewhere-else", "auto", "require_branch"} {
		if strings.Contains(notice.Message, value) {
			t.Errorf("the notice quoted a legacy value %q: %q", value, notice.Message)
		}
	}
}
