package cliagent

import (
	"context"
	"testing"

	"github.com/johnjallday/ori-agent/internal/llm"
)

// testAdapter is a controllable adapter for executor tests.
type testAdapter struct {
	results []StepResult
	callIdx int
}

func (a *testAdapter) Backend() string            { return BackendClaude }
func (a *testAdapter) IsAvailable() bool          { return true }
func (a *testAdapter) AvailableModels() []string  { return []string{"test"} }
func (a *testAdapter) Capabilities() Capabilities { return Capabilities{} }

func (a *testAdapter) ExecuteStep(_ context.Context, req StepRequest) (*StepResult, error) {
	if a.callIdx < len(a.results) {
		r := a.results[a.callIdx]
		r.StepNumber = req.StepNumber
		a.callIdx++
		return &r, nil
	}
	return &StepResult{
		StepNumber: req.StepNumber,
		Status:     StepCompleted,
		Output:     "default output",
		Usage:      StepUsage{InputTokens: 10, OutputTokens: 5, CostUSD: 0.001},
	}, nil
}

// multiMockProvider returns responses in sequence.
type multiMockProvider struct {
	responses []string
	idx       int
}

func (m *multiMockProvider) Chat(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
	if m.idx < len(m.responses) {
		resp := m.responses[m.idx]
		m.idx++
		return &llm.ChatResponse{Content: resp}, nil
	}
	return &llm.ChatResponse{Content: `{"done": true, "rationale": "fallback"}`}, nil
}

func (m *multiMockProvider) StreamChat(_ context.Context, _ llm.ChatRequest) (llm.StreamReader, error) {
	return nil, nil
}
func (m *multiMockProvider) Name() string           { return "multi-mock" }
func (m *multiMockProvider) Type() llm.ProviderType { return llm.ProviderTypeCloud }
func (m *multiMockProvider) Capabilities() llm.ProviderCapabilities {
	return llm.ProviderCapabilities{}
}
func (m *multiMockProvider) ValidateConfig(_ llm.ProviderConfig) error { return nil }
func (m *multiMockProvider) DefaultModels() []string                   { return []string{"mock"} }

func TestExecutor_HappyPath(t *testing.T) {
	adapter := &testAdapter{
		results: []StepResult{
			{Status: StepCompleted, Output: "analyzed", Usage: StepUsage{InputTokens: 100, OutputTokens: 50, CostUSD: 0.01}},
			{Status: StepCompleted, Output: "implemented", Usage: StepUsage{InputTokens: 200, OutputTokens: 100, CostUSD: 0.02}},
		},
	}

	planJSON := `[
		{"step_number":1,"description":"Analyze","expected_outcome":"Understanding"},
		{"step_number":2,"description":"Implement","expected_outcome":"Code"}
	]`

	registry := NewRegistry(adapter)
	provider := &multiMockProvider{
		responses: []string{
			planJSON,
			"Step 1 summary",
			`{"done": false, "rationale": "need step 2"}`,
			"Step 2 summary",
			`{"done": true, "rationale": "all done"}`,
		},
	}
	planner := NewStepPlanner(provider, "test")
	eventLogger := NewEventLogger(t.TempDir())
	diff := NewDiffDetector()

	executor := NewMicroStepExecutor(registry, planner, eventLogger, diff, nil)

	config := TaskConfig{
		CLIBackend: BackendClaude,
		Prompt:     "Build something",
		WorkingDir: t.TempDir(),
		MaxSteps:   5,
	}

	result, err := executor.Execute(context.Background(), config)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	if result.Status != TaskCompleted {
		t.Errorf("expected completed, got %s", result.Status)
	}
	if result.StepsExecuted != 2 {
		t.Errorf("expected 2 steps, got %d", result.StepsExecuted)
	}
	if result.TotalUsage.InputTokens != 300 {
		t.Errorf("expected 300 input tokens, got %d", result.TotalUsage.InputTokens)
	}
}

func TestExecutor_BudgetExceeded(t *testing.T) {
	adapter := &testAdapter{
		results: []StepResult{
			{Status: StepCompleted, Output: "expensive", Usage: StepUsage{InputTokens: 5000, OutputTokens: 5000, CostUSD: 0.80}},
		},
	}

	planJSON := `[
		{"step_number":1,"description":"Step 1","expected_outcome":"x"},
		{"step_number":2,"description":"Step 2","expected_outcome":"y"}
	]`

	registry := NewRegistry(adapter)
	provider := &multiMockProvider{
		responses: []string{
			planJSON,
			"Summary",
			`{"done": false, "rationale": "not done"}`,
		},
	}
	planner := NewStepPlanner(provider, "test")
	executor := NewMicroStepExecutor(registry, planner, NewEventLogger(t.TempDir()), NewDiffDetector(), nil)

	config := TaskConfig{
		CLIBackend:    BackendClaude,
		Prompt:        "task",
		WorkingDir:    t.TempDir(),
		CostBudgetUSD: 0.50, // Budget is 0.50, step costs 0.80
	}

	result, err := executor.Execute(context.Background(), config)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	// First step execeds budget, second step should not run
	if result.Status != TaskBudgetExceeded {
		t.Errorf("expected budget_exceeded, got %s", result.Status)
	}
	if result.StepsExecuted != 1 {
		t.Errorf("expected 1 step executed, got %d", result.StepsExecuted)
	}
}

func TestExecutor_StepFailure(t *testing.T) {
	adapter := &testAdapter{
		results: []StepResult{
			{Status: StepFailed, Error: "CLI crashed", Output: ""},
		},
	}

	planJSON := `[{"step_number":1,"description":"Step 1","expected_outcome":"x"}]`
	registry := NewRegistry(adapter)
	provider := &multiMockProvider{responses: []string{planJSON}}
	planner := NewStepPlanner(provider, "test")
	executor := NewMicroStepExecutor(registry, planner, NewEventLogger(t.TempDir()), NewDiffDetector(), nil)

	config := TaskConfig{
		CLIBackend: BackendClaude,
		Prompt:     "task",
		WorkingDir: t.TempDir(),
	}

	result, err := executor.Execute(context.Background(), config)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result.Status != TaskFailed {
		t.Errorf("expected failed, got %s", result.Status)
	}
	if result.Error != "CLI crashed" {
		t.Errorf("expected error 'CLI crashed', got %q", result.Error)
	}
}

func TestExecutor_MaxStepsReached(t *testing.T) {
	adapter := &testAdapter{} // Returns default completed results

	planJSON := `[
		{"step_number":1,"description":"s1","expected_outcome":"x"},
		{"step_number":2,"description":"s2","expected_outcome":"x"},
		{"step_number":3,"description":"s3","expected_outcome":"x"}
	]`

	registry := NewRegistry(adapter)
	// Always say "not done"
	provider := &multiMockProvider{
		responses: []string{
			planJSON,
			"summary", `{"done": false, "rationale": "not done"}`,
			"summary", `{"done": false, "rationale": "not done"}`,
			"summary", `{"done": false, "rationale": "not done"}`,
		},
	}
	planner := NewStepPlanner(provider, "test")
	executor := NewMicroStepExecutor(registry, planner, NewEventLogger(t.TempDir()), NewDiffDetector(), nil)

	config := TaskConfig{
		CLIBackend: BackendClaude,
		Prompt:     "task",
		WorkingDir: t.TempDir(),
		MaxSteps:   2, // Only allow 2 steps but 3 planned
	}

	result, err := executor.Execute(context.Background(), config)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result.Status != TaskMaxStepsReached {
		t.Errorf("expected max_steps_reached, got %s", result.Status)
	}
	if result.StepsExecuted != 2 {
		t.Errorf("expected 2 steps, got %d", result.StepsExecuted)
	}
}

func TestExecutor_ConcurrencyLimit(t *testing.T) {
	adapter := &stubAdapter{backend: BackendClaude, available: true}
	registry := NewRegistry(adapter)
	provider := &multiMockProvider{responses: []string{`[{"step_number":1,"description":"x","expected_outcome":"y"}]`}}
	planner := NewStepPlanner(provider, "test")
	executor := NewMicroStepExecutor(registry, planner, NewEventLogger(t.TempDir()), NewDiffDetector(), nil)
	executor.SetMaxConcurrent(0) // 0 means unlimited... let's test with 1

	// Actually test with limit 1 and a fake running task
	executor.SetMaxConcurrent(1)
	executor.mu.Lock()
	executor.running["fake"] = func() {}
	executor.mu.Unlock()

	config := TaskConfig{
		CLIBackend: BackendClaude,
		Prompt:     "task",
		WorkingDir: t.TempDir(),
	}

	_, err := executor.Execute(context.Background(), config)
	if err == nil {
		t.Error("expected concurrency limit error")
	}

	// Clean up
	executor.mu.Lock()
	delete(executor.running, "fake")
	executor.mu.Unlock()
}

func TestExecutor_Stop(t *testing.T) {
	executor := &MicroStepExecutor{
		running: make(map[string]context.CancelFunc),
	}

	ctx, cancel := context.WithCancel(context.Background())
	executor.running["task1"] = cancel

	if !executor.Stop("task1") {
		t.Error("should return true for running task")
	}
	if ctx.Err() == nil {
		t.Error("context should be cancelled")
	}
	if executor.Stop("nonexistent") {
		t.Error("should return false for nonexistent task")
	}
}

func TestDedupeFileChanges(t *testing.T) {
	changes := []FileChange{
		{Path: "a.go", ChangeType: ChangeAdded},
		{Path: "b.go", ChangeType: ChangeModified},
		{Path: "a.go", ChangeType: ChangeModified}, // Should replace first
	}

	deduped := dedupeFileChanges(changes)
	if len(deduped) != 2 {
		t.Fatalf("expected 2 unique files, got %d", len(deduped))
	}
	// a.go should be modified (last wins)
	for _, c := range deduped {
		if c.Path == "a.go" && c.ChangeType != ChangeModified {
			t.Errorf("a.go should be modified, got %s", c.ChangeType)
		}
	}
}
