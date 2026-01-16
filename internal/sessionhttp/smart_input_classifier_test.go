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

func TestClassifySmartInputHeuristic_DefaultsToTask(t *testing.T) {
	result := classifySmartInputHeuristic("roadmap")

	if result.Decision != SmartInputDecisionTask {
		t.Fatalf("expected task decision, got %s", result.Decision)
	}
	if result.Confidence != 0.5 {
		t.Fatalf("expected default confidence 0.5, got %f", result.Confidence)
	}
}
