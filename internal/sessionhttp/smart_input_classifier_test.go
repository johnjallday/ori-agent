package sessionhttp

import "testing"

func TestClassifySmartInputHeuristic_TaskPrefix(t *testing.T) {
	result := classifySmartInputHeuristic("todo: update release notes")

	if result.Decision != SmartInputDecisionTask {
		t.Fatalf("expected task decision, got %s", result.Decision)
	}
	if result.Confidence < 0.9 {
		t.Fatalf("expected high confidence, got %f", result.Confidence)
	}
}

func TestClassifySmartInputHeuristic_Question(t *testing.T) {
	result := classifySmartInputHeuristic("How do we roll back this deploy?")

	if result.Decision != SmartInputDecisionChat {
		t.Fatalf("expected chat decision, got %s", result.Decision)
	}
	if result.Confidence < 0.85 {
		t.Fatalf("expected question confidence, got %f", result.Confidence)
	}
}

func TestClassifySmartInputHeuristic_Imperative(t *testing.T) {
	result := classifySmartInputHeuristic("Fix the billing alert")

	if result.Decision != SmartInputDecisionTask {
		t.Fatalf("expected task decision, got %s", result.Decision)
	}
	if result.Confidence < 0.6 {
		t.Fatalf("expected imperative confidence, got %f", result.Confidence)
	}
}

func TestClassifySmartInputHeuristic_BacklogPrefix(t *testing.T) {
	cases := []string{
		"backlog: explore competitor pricing",
		"idea: dark mode toggle",
		"someday redesign the onboarding flow",
	}
	for _, input := range cases {
		result := classifySmartInputHeuristic(input)
		if result.Decision != SmartInputDecisionBacklog {
			t.Errorf("classifySmartInputHeuristic(%q) decision = %s, want backlog", input, result.Decision)
		}
		if result.Confidence < 0.9 {
			t.Errorf("classifySmartInputHeuristic(%q) confidence = %f, want >= 0.9", input, result.Confidence)
		}
	}
}

func TestClassifySmartInputHeuristic_BacklogPrefixTakesPrecedenceOverTaskPrefix(t *testing.T) {
	// "backlog: fix the header" also reads as an imperative task once the
	// prefix is stripped, but the explicit backlog framing must win.
	result := classifySmartInputHeuristic("backlog: fix the header")
	if result.Decision != SmartInputDecisionBacklog {
		t.Fatalf("expected backlog decision, got %s", result.Decision)
	}
}

func TestClassifySmartInputHeuristic_DefaultsToTask(t *testing.T) {
	result := classifySmartInputHeuristic("roadmap")

	if result.Decision != SmartInputDecisionTask {
		t.Fatalf("expected task decision, got %s", result.Decision)
	}
	if result.Confidence != 0.5 {
		t.Fatalf("expected default confidence 0.5, got %f", result.Confidence)
	}
}
