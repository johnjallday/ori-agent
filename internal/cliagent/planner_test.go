package cliagent

import (
	"context"
	"testing"

	"github.com/johnjallday/ori-agent/internal/llm"
)

// mockProvider is a minimal LLM provider for testing the planner.
type mockProvider struct {
	response string
	err      error
}

func (m *mockProvider) Chat(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &llm.ChatResponse{Content: m.response}, nil
}

func (m *mockProvider) StreamChat(_ context.Context, _ llm.ChatRequest) (llm.StreamReader, error) {
	return nil, nil
}
func (m *mockProvider) Name() string                              { return "mock" }
func (m *mockProvider) Type() llm.ProviderType                    { return llm.ProviderTypeCloud }
func (m *mockProvider) Capabilities() llm.ProviderCapabilities    { return llm.ProviderCapabilities{} }
func (m *mockProvider) ValidateConfig(_ llm.ProviderConfig) error { return nil }
func (m *mockProvider) DefaultModels() []string                   { return []string{"mock-model"} }

func TestStepPlanner_DecomposeTask(t *testing.T) {
	provider := &mockProvider{
		response: `[
			{"step_number": 1, "description": "Analyze the codebase", "expected_outcome": "Understanding of structure"},
			{"step_number": 2, "description": "Implement the feature", "expected_outcome": "Working code"}
		]`,
	}

	p := NewStepPlanner(provider, "mock-model")
	plans, err := p.DecomposeTask(context.Background(), "Add a REST endpoint")
	if err != nil {
		t.Fatalf("decompose: %v", err)
	}
	if len(plans) != 2 {
		t.Fatalf("expected 2 plans, got %d", len(plans))
	}
	if plans[0].Description != "Analyze the codebase" {
		t.Errorf("unexpected description: %s", plans[0].Description)
	}
}

func TestStepPlanner_DecomposeTask_CodeFences(t *testing.T) {
	provider := &mockProvider{
		response: "```json\n[{\"step_number\":1,\"description\":\"Do it\",\"expected_outcome\":\"Done\"}]\n```",
	}

	p := NewStepPlanner(provider, "mock-model")
	plans, err := p.DecomposeTask(context.Background(), "task")
	if err != nil {
		t.Fatalf("decompose with code fences: %v", err)
	}
	if len(plans) != 1 {
		t.Fatalf("expected 1 plan, got %d", len(plans))
	}
}

func TestStepPlanner_DecomposeTask_Empty(t *testing.T) {
	provider := &mockProvider{response: "[]"}

	p := NewStepPlanner(provider, "mock-model")
	_, err := p.DecomposeTask(context.Background(), "task")
	if err == nil {
		t.Error("expected error for empty plan")
	}
}

func TestStepPlanner_GenerateStepPrompt(t *testing.T) {
	p := NewStepPlanner(nil, "")

	step := StepPlan{
		StepNumber:      2,
		Description:     "Implement the handler",
		ExpectedOutcome: "Working HTTP handler",
	}

	prompt := p.GenerateStepPrompt(step, []string{"Step 1 analyzed the code."})
	if !contains(prompt, "Context from previous steps") {
		t.Error("expected context header")
	}
	if !contains(prompt, "Step 1 analyzed the code.") {
		t.Error("expected previous summary")
	}
	if !contains(prompt, "Implement the handler") {
		t.Error("expected step description")
	}
	if !contains(prompt, "Working HTTP handler") {
		t.Error("expected expected outcome")
	}
}

func TestStepPlanner_GenerateStepPrompt_NoContext(t *testing.T) {
	p := NewStepPlanner(nil, "")
	step := StepPlan{Description: "First step"}
	prompt := p.GenerateStepPrompt(step, nil)
	if contains(prompt, "Context from previous steps") {
		t.Error("should not include context header when no summaries")
	}
}

func TestStepPlanner_SummarizeStepResult(t *testing.T) {
	provider := &mockProvider{response: "Step 1 completed the analysis successfully."}
	p := NewStepPlanner(provider, "mock-model")

	result := StepResult{
		StepNumber: 1,
		Output:     "I analyzed the codebase and found...",
		FilesChanged: []FileChange{
			{Path: "main.go", ChangeType: ChangeModified, LinesAdded: 10, LinesRemoved: 2},
		},
	}

	summary, err := p.SummarizeStepResult(context.Background(), result)
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}
	if summary != "Step 1 completed the analysis successfully." {
		t.Errorf("unexpected summary: %s", summary)
	}
}

func TestStepPlanner_EvaluateCompletion(t *testing.T) {
	provider := &mockProvider{response: `{"done": true, "rationale": "All steps completed successfully."}`}
	p := NewStepPlanner(provider, "mock-model")

	done, rationale, err := p.EvaluateCompletion(context.Background(), "task", []string{"step 1 done"})
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if !done {
		t.Error("expected done=true")
	}
	if rationale != "All steps completed successfully." {
		t.Errorf("unexpected rationale: %s", rationale)
	}
}

func TestStepPlanner_EvaluateCompletion_NotDone(t *testing.T) {
	provider := &mockProvider{response: `{"done": false, "rationale": "Tests not yet written."}`}
	p := NewStepPlanner(provider, "mock-model")

	done, _, err := p.EvaluateCompletion(context.Background(), "task", []string{"step 1 done"})
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if done {
		t.Error("expected done=false")
	}
}

func TestStripCodeFences(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hello", "hello"},
		{"```json\n[1,2,3]\n```", "[1,2,3]"},
		{"```\nplain\n```", "plain"},
		{"  ```json\n{}\n```  ", "{}"},
	}
	for _, tt := range tests {
		got := stripCodeFences(tt.input)
		if got != tt.expected {
			t.Errorf("stripCodeFences(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("hello", 10); got != "hello" {
		t.Errorf("expected hello, got %s", got)
	}
	if got := truncate("hello world", 5); got != "hello..." {
		t.Errorf("expected hello..., got %s", got)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
