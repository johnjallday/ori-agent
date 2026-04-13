//go:build integration

package cliagent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestIntegration_ClaudeCLIStep runs a real Claude CLI step and validates output parsing.
// Requires: `claude` CLI installed and ANTHROPIC_API_KEY set.
func TestIntegration_ClaudeCLIStep(t *testing.T) {
	if _, err := findCLI("claude"); err != nil {
		t.Skip("claude CLI not found, skipping integration test")
	}

	tmpDir := t.TempDir()
	adapter := NewClaudeCLIAdapter("")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	result, err := adapter.ExecuteStep(ctx, StepRequest{
		Prompt:     "Create a file called hello.txt containing 'Hello from Claude CLI integration test'",
		WorkingDir: tmpDir,
		Budget: StepBudget{
			MaxCostUSD: 0.10,
		},
	})
	if err != nil {
		t.Fatalf("ExecuteStep failed: %v", err)
	}

	if result.Status != StepCompleted {
		t.Errorf("expected status %s, got %s (error: %s)", StepCompleted, result.Status, result.Error)
	}

	// Verify file was created
	content, err := os.ReadFile(filepath.Join(tmpDir, "hello.txt"))
	if err != nil {
		t.Fatalf("expected hello.txt to be created: %v", err)
	}
	if len(content) == 0 {
		t.Error("hello.txt is empty")
	}

	// Verify usage was tracked
	if result.Usage.InputTokens == 0 && result.Usage.OutputTokens == 0 {
		t.Error("expected non-zero token usage")
	}

	t.Logf("Claude step completed: %d input tokens, %d output tokens, cost $%.4f",
		result.Usage.InputTokens, result.Usage.OutputTokens, result.Usage.CostUSD)
}

// TestIntegration_CodexCLIStep runs a real Codex CLI step with workspace-write sandbox.
// Requires: `codex` CLI installed and OPENAI_API_KEY set.
func TestIntegration_CodexCLIStep(t *testing.T) {
	if _, err := findCLI("codex"); err != nil {
		t.Skip("codex CLI not found, skipping integration test")
	}

	tmpDir := t.TempDir()
	adapter := NewCodexCLIAdapter("")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	result, err := adapter.ExecuteStep(ctx, StepRequest{
		Prompt:     "Create a file called hello.txt containing 'Hello from Codex CLI integration test'",
		WorkingDir: tmpDir,
		Budget: StepBudget{
			MaxCostUSD: 0.10,
		},
	})
	if err != nil {
		t.Fatalf("ExecuteStep failed: %v", err)
	}

	if result.Status != StepCompleted {
		t.Errorf("expected status %s, got %s (error: %s)", StepCompleted, result.Status, result.Error)
	}

	// Verify file was created
	content, err := os.ReadFile(filepath.Join(tmpDir, "hello.txt"))
	if err != nil {
		t.Fatalf("expected hello.txt to be created: %v", err)
	}
	if len(content) == 0 {
		t.Error("hello.txt is empty")
	}

	if result.Usage.InputTokens == 0 && result.Usage.OutputTokens == 0 {
		t.Error("expected non-zero token usage")
	}

	t.Logf("Codex step completed: %d input tokens, %d output tokens",
		result.Usage.InputTokens, result.Usage.OutputTokens)
}

// TestIntegration_FullMicroStepTask runs a complete multi-step task against a real CLI.
// Requires: at least one CLI installed with valid API key.
func TestIntegration_FullMicroStepTask(t *testing.T) {
	registry := NewRegistry()
	registry.AutoDetect()

	available := registry.List()
	if len(available) == 0 {
		t.Skip("no CLI agents available, skipping integration test")
	}

	backend := available[0].Backend
	t.Logf("Using backend: %s", backend)

	tmpDir := t.TempDir()

	// Initialize git repo so diff detection works
	invoker := &CLIInvoker{}
	_ = invoker // just to show we have access

	eventLogger := NewEventLogger(t.TempDir())
	diffDetector := NewDiffDetector()

	// We need a real LLM provider for the planner, which we don't have in tests.
	// Use nil planner — the executor will skip planning and run the prompt directly.
	executor := NewMicroStepExecutor(registry, nil, eventLogger, diffDetector, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	result, err := executor.Execute(ctx, TaskConfig{
		CLIBackend:  backend,
		Prompt:      "Create two files: 1) math.py with a function add(a, b) that returns a+b, and 2) test_math.py that imports math and tests the add function",
		WorkingDir:  tmpDir,
		MaxSteps:    3,
		TokenBudget: 50000,
		CostBudget:  0.50,
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	t.Logf("Task result: status=%s, steps=%d, summary=%s",
		result.Status, result.StepsExecuted, result.Summary)

	if result.Status != TaskCompleted && result.Status != TaskMaxStepsReached {
		t.Errorf("unexpected status: %s (error: %s)", result.Status, result.Error)
	}

	if result.StepsExecuted == 0 {
		t.Error("expected at least one step to execute")
	}

	// Check that files were created
	for _, name := range []string{"math.py", "test_math.py"} {
		if _, err := os.Stat(filepath.Join(tmpDir, name)); err != nil {
			t.Errorf("expected %s to exist: %v", name, err)
		}
	}

	// Verify events were logged
	events := eventLogger.GetEvents(result.TaskID)
	if len(events) == 0 {
		t.Error("expected events to be logged")
	}

	t.Logf("Total usage: %d input, %d output tokens, $%.4f",
		result.TotalUsage.InputTokens, result.TotalUsage.OutputTokens, result.TotalUsage.CostUSD)
}
