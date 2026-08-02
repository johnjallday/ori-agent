package workspace

import (
	"strings"
	"testing"
)

// Focus coverage (task 3.18; PRD FR-63–FR-72).

func TestEvaluateFocus_HardFailuresAlwaysWin(t *testing.T) {
	// A tiny toolbox with an unresolved prerequisite is `Needs attention`, not
	// `Focused`: the state is about readiness, not size (FR-65).
	result := EvaluateFocus(
		FocusInputs{ActiveSkills: 1, ExposedOperations: 1},
		DefaultFocusThresholds(),
		[]string{"Notes is switched off."},
	)

	if result.State != FocusNeedsAttention {
		t.Fatalf("expected a hard failure to force Needs attention, got %q", result.State)
	}
	if len(result.Reasons) != 1 || result.Reasons[0] != "Notes is switched off." {
		t.Fatalf("expected the blocking reason to be reported verbatim, got %v", result.Reasons)
	}
	if result.Advisory() {
		t.Fatalf("Needs attention must not be advisory")
	}
}

func TestEvaluateFocus_OperationCountThresholds(t *testing.T) {
	thresholds := DefaultFocusThresholds()

	cases := []struct {
		operations int
		want       string
	}{
		{thresholds.FlexibleOperations - 1, FocusFocused},
		{thresholds.FlexibleOperations, FocusFlexible},
		{thresholds.CrowdedOperations - 1, FocusFlexible},
		{thresholds.CrowdedOperations, FocusCrowded},
	}
	for _, testCase := range cases {
		result := EvaluateFocus(FocusInputs{ExposedOperations: testCase.operations}, thresholds, nil)
		if result.State != testCase.want {
			t.Fatalf("with %d operations expected %q, got %q", testCase.operations, testCase.want, result.State)
		}
	}
}

// FR-69: reasons are concrete facts, not a score.
func TestEvaluateFocus_ReportsConcreteReasons(t *testing.T) {
	result := EvaluateFocus(FocusInputs{
		ExposedOperations: 30,
		OverlapGroups: []FocusOverlapGroup{
			{Operation: "search", Providers: []string{"Notes", "Web"}},
		},
		WriteOperations: 9,
		ActiveSkills:    3,
		SkillCapacity:   4,
	}, DefaultFocusThresholds(), nil)

	if result.State != FocusCrowded {
		t.Fatalf("expected Crowded, got %q", result.State)
	}
	joined := strings.Join(result.Reasons, " | ")
	for _, want := range []string{"30 exposed tools", "overlapping search operations", "change or send things"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected a reason mentioning %q, got %v", want, result.Reasons)
		}
	}
}

// An overlap inferred from names must never read as a declared one (§9.6).
func TestEvaluateFocus_MarksHeuristicOverlap(t *testing.T) {
	result := EvaluateFocus(FocusInputs{
		OverlapGroups: []FocusOverlapGroup{
			{Operation: "search", Providers: []string{"Notes", "Web"}, Heuristic: true},
		},
	}, DefaultFocusThresholds(), nil)

	joined := strings.Join(result.Reasons, " ")
	if !strings.Contains(joined, "matched by name, not declared") {
		t.Fatalf("expected a heuristic overlap to be labelled as such, got %v", result.Reasons)
	}
}

// FR-66: Flexible and Crowded are advisory and never block.
func TestEvaluateFocus_AdvisoryStates(t *testing.T) {
	thresholds := DefaultFocusThresholds()
	flexible := EvaluateFocus(FocusInputs{ExposedOperations: thresholds.FlexibleOperations}, thresholds, nil)
	crowded := EvaluateFocus(FocusInputs{ExposedOperations: thresholds.CrowdedOperations}, thresholds, nil)
	focused := EvaluateFocus(FocusInputs{ExposedOperations: 1}, thresholds, nil)

	if !flexible.Advisory() || !crowded.Advisory() {
		t.Fatalf("expected Flexible and Crowded to be advisory")
	}
	if focused.Advisory() {
		t.Fatalf("expected Focused not to be advisory")
	}
}

// A binding whose tools were never pinned makes the real surface unknown, and
// Focus must say so rather than report a number that could be wrong (FR-72).
func TestEvaluateFocus_UnpinnedBindingsAreReported(t *testing.T) {
	result := EvaluateFocus(FocusInputs{UnpinnedBindings: 1, ExposedOperations: 2}, DefaultFocusThresholds(), nil)

	if result.State != FocusFlexible {
		t.Fatalf("expected an unknown surface to raise Flexible, got %q", result.State)
	}
	if !strings.Contains(strings.Join(result.Reasons, " "), "real tool count is unknown") {
		t.Fatalf("expected the unknown count to be stated, got %v", result.Reasons)
	}
}

// Focus is deterministic: identical inputs produce an identical result,
// including reason ordering (FR-63, FR-70).
func TestEvaluateFocus_IsDeterministic(t *testing.T) {
	inputs := FocusInputs{
		ExposedOperations: 26,
		WriteOperations:   9,
		PromptChars:       25000,
		OverlapGroups: []FocusOverlapGroup{
			{Operation: "search", Providers: []string{"A", "B"}},
			{Operation: "write", Providers: []string{"A", "B", "C"}},
		},
	}
	first := EvaluateFocus(inputs, DefaultFocusThresholds(), nil)
	for range 20 {
		next := EvaluateFocus(inputs, DefaultFocusThresholds(), nil)
		if next.State != first.State || strings.Join(next.Reasons, "|") != strings.Join(first.Reasons, "|") {
			t.Fatalf("expected deterministic output, got %v then %v", first, next)
		}
	}
}

// FR-68: overlap is computed from concrete operations, preferring the
// workspace's own declared CapabilityMappings over a name guess.
func TestBuildFocusOverlapGroups_PrefersDeclaredMappings(t *testing.T) {
	bindings := []MCPBinding{
		{
			ID: "mb-1", ServerName: "notes", Alias: "Notes",
			CapabilityMappings: []CapabilityMapping{{
				Capability: "documents",
				Operations: map[string]OperationMapping{"search": {Tool: "find_note"}},
			}},
		},
		{
			ID: "mb-2", ServerName: "web", Alias: "Web",
			CapabilityMappings: []CapabilityMapping{{
				Capability: "documents",
				Operations: map[string]OperationMapping{"search": {Tool: "web_query"}},
			}},
		},
	}
	exposed := map[string][]string{
		"mb-1": {"find_note"},
		"mb-2": {"web_query"},
	}

	groups := BuildFocusOverlapGroups(bindings, exposed)
	if len(groups) != 1 {
		t.Fatalf("expected one semantic group, got %+v", groups)
	}
	if groups[0].Operation != "documents.search" {
		t.Fatalf("expected the declared capability.operation, got %q", groups[0].Operation)
	}
	if len(groups[0].Providers) != 2 {
		t.Fatalf("expected both providers in the overlap, got %v", groups[0].Providers)
	}
	if groups[0].Heuristic {
		t.Fatalf("expected a declared mapping not to be marked heuristic")
	}
}

func TestBuildFocusOverlapGroups_FallsBackToNamesAndMarksThem(t *testing.T) {
	bindings := []MCPBinding{
		{ID: "mb-1", ServerName: "notes", Alias: "Notes"},
		{ID: "mb-2", ServerName: "web", Alias: "Web"},
	}
	exposed := map[string][]string{
		"mb-1": {"search_notes"},
		"mb-2": {"search_web"},
	}

	groups := BuildFocusOverlapGroups(bindings, exposed)
	if len(groups) != 1 || groups[0].Operation != "search" {
		t.Fatalf("expected a name-matched search overlap, got %+v", groups)
	}
	if !groups[0].Heuristic {
		t.Fatalf("expected a name-matched overlap to be marked heuristic")
	}
}

// Core capabilities contribute to the risk and context readouts but never to
// capacity (FR-48, FR-58).
func TestFocusInputs_CoreDoesNotConsumeCapacity(t *testing.T) {
	inputs := FocusInputs{ActiveSkills: 2, SkillCapacity: 4, CoreCapabilities: 3}
	result := EvaluateFocus(inputs, DefaultFocusThresholds(), nil)

	if result.Inputs.ActiveSkills != 2 {
		t.Fatalf("expected core capabilities to stay out of the active-skill count, got %d", result.Inputs.ActiveSkills)
	}
	if result.Inputs.CoreCapabilities != 3 {
		t.Fatalf("expected core capabilities to remain visible, got %d", result.Inputs.CoreCapabilities)
	}
}
