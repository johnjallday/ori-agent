package agentcatalog

import "testing"

func TestResolveModelPicksDeterministicCandidate(t *testing.T) {
	assignments := map[string][]string{
		"gpt-5-nano":    {"cat_default_tool_calling"},
		"gpt-4o-mini":   {"cat_default_tool_calling", "cat_default_general_purpose"},
		"claude-3-opus": {"cat_default_research"},
	}

	model, ok := ResolveModel(TierFast, assignments)
	if !ok {
		t.Fatal("expected a model to resolve for TierFast")
	}
	// Alphabetically first of {gpt-4o-mini, gpt-5-nano}.
	if model != "gpt-4o-mini" {
		t.Fatalf("expected gpt-4o-mini, got %q", model)
	}
}

func TestResolveModelFallsBackWhenCategoryUnassigned(t *testing.T) {
	assignments := map[string][]string{
		"gpt-5-nano": {"cat_default_tool_calling"},
	}

	if _, ok := ResolveModel(TierDeep, assignments); ok {
		t.Fatal("expected no model assigned to the research category")
	}
}

func TestResolveModelHandlesEmptyAssignments(t *testing.T) {
	if _, ok := ResolveModel(TierBalanced, nil); ok {
		t.Fatal("expected no match against nil assignments")
	}
	if _, ok := ResolveModel(TierBalanced, map[string][]string{}); ok {
		t.Fatal("expected no match against empty assignments")
	}
}

func TestResolveModelUnknownTier(t *testing.T) {
	assignments := map[string][]string{
		"gpt-5-nano": {"cat_default_tool_calling"},
	}
	if _, ok := ResolveModel(ModelTier("unknown"), assignments); ok {
		t.Fatal("expected no match for an unknown tier")
	}
}
