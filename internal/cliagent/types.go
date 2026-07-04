// Package cliagent provides an adapter layer for delegating tasks to external
// CLI agents (Claude CLI, Codex CLI) via a supervised micro-step execution model.
package cliagent

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// Backend constants identify supported CLI agent backends.
const (
	BackendClaude = "claude"
	BackendCodex  = "codex"
	BackendGemini = "gemini"
)

// StepStatus represents the outcome of a single micro-step execution.
type StepStatus string

const (
	StepCompleted      StepStatus = "completed"
	StepFailed         StepStatus = "failed"
	StepBudgetExceeded StepStatus = "budget_exceeded"
)

// TaskStatus represents the overall outcome of a CLI agent task.
type TaskStatus string

const (
	TaskRunning         TaskStatus = "running"
	TaskCompleted       TaskStatus = "completed"
	TaskFailed          TaskStatus = "failed"
	TaskBudgetExceeded  TaskStatus = "budget_exceeded"
	TaskMaxStepsReached TaskStatus = "max_steps_reached"
	TaskStopped         TaskStatus = "stopped"
)

// ChangeType describes how a file was modified.
type ChangeType string

const (
	ChangeAdded    ChangeType = "added"
	ChangeModified ChangeType = "modified"
	ChangeDeleted  ChangeType = "deleted"
)

// DefaultMaxSteps is the default maximum number of micro-steps per task.
const DefaultMaxSteps = 10

// DefaultStepTimeout is the default per-step timeout.
const DefaultStepTimeout = 120 * time.Second

// DefaultMaxConcurrent is the default maximum concurrent CLI agent tasks.
const DefaultMaxConcurrent = 3

// TaskConfig holds the configuration for a CLI agent task.
type TaskConfig struct {
	CLIBackend    string  `json:"cli_backend"`               // "claude" or "codex" (required)
	Model         string  `json:"model"`                     // Model to use (e.g., "opus", "gpt-5.3-codex")
	Prompt        string  `json:"prompt"`                    // The task description
	WorkingDir    string  `json:"working_dir"`               // Absolute path to scoped working directory
	TokenBudget   int     `json:"token_budget,omitempty"`    // Max total tokens (0 = unlimited)
	CostBudgetUSD float64 `json:"cost_budget_usd,omitempty"` // Max total cost in USD (0 = unlimited)
	MaxSteps      int     `json:"max_steps,omitempty"`       // Max micro-steps (0 = DefaultMaxSteps)
}

// Validate checks that the TaskConfig is well-formed.
func (c TaskConfig) Validate() error {
	backend := strings.ToLower(strings.TrimSpace(c.CLIBackend))
	if backend != BackendClaude && backend != BackendCodex && backend != BackendGemini {
		return fmt.Errorf("cli_backend must be %q, %q, or %q, got %q", BackendClaude, BackendCodex, BackendGemini, c.CLIBackend)
	}
	if strings.TrimSpace(c.Prompt) == "" {
		return fmt.Errorf("prompt is required")
	}
	if strings.TrimSpace(c.WorkingDir) == "" {
		return fmt.Errorf("working_dir is required")
	}
	info, err := os.Stat(c.WorkingDir)
	if err != nil {
		return fmt.Errorf("working_dir %q: %w", c.WorkingDir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("working_dir %q is not a directory", c.WorkingDir)
	}
	if c.TokenBudget < 0 {
		return fmt.Errorf("token_budget must be non-negative")
	}
	if c.CostBudgetUSD < 0 {
		return fmt.Errorf("cost_budget_usd must be non-negative")
	}
	if c.MaxSteps < 0 {
		return fmt.Errorf("max_steps must be non-negative")
	}
	return nil
}

// EffectiveMaxSteps returns the max steps to use, applying the default if unset.
func (c TaskConfig) EffectiveMaxSteps() int {
	if c.MaxSteps > 0 {
		return c.MaxSteps
	}
	return DefaultMaxSteps
}

// StepBudget defines resource limits for a single micro-step.
type StepBudget struct {
	MaxTokens  int           `json:"max_tokens,omitempty"`
	MaxCostUSD float64       `json:"max_cost_usd,omitempty"`
	Timeout    time.Duration `json:"timeout,omitempty"`
}

// StepRequest contains everything needed to execute a single micro-step.
type StepRequest struct {
	TaskID     string       `json:"task_id"`
	StepNumber int          `json:"step_number"`
	Prompt     string       `json:"prompt"`
	WorkingDir string       `json:"working_dir"`
	Model      string       `json:"model"`
	Budget     StepBudget   `json:"budget"`
	Context    []StepResult `json:"context,omitempty"` // Summarized previous step results
}

// StepUsage tracks token and cost consumption for a single step.
type StepUsage struct {
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	CostUSD      float64 `json:"cost_usd"`
}

// TotalTokens returns the sum of input and output tokens.
func (u StepUsage) TotalTokens() int {
	return u.InputTokens + u.OutputTokens
}

// StepResult contains the outcome of a single micro-step execution.
type StepResult struct {
	StepNumber   int           `json:"step_number"`
	Output       string        `json:"output"`
	Events       []CLIEvent    `json:"events,omitempty"`
	FilesChanged []FileChange  `json:"files_changed,omitempty"`
	Usage        StepUsage     `json:"usage"`
	Status       StepStatus    `json:"status"`
	Error        string        `json:"error,omitempty"`
	Duration     time.Duration `json:"duration"`
}

// CLIEvent represents a single event captured from CLI stream output.
type CLIEvent struct {
	Timestamp  time.Time `json:"timestamp"`
	StepNumber int       `json:"step_number"`
	Type       string    `json:"type"`
	Content    string    `json:"content,omitempty"`
	Payload    any       `json:"payload,omitempty"`
}

// FileChange describes a file modification detected during a step.
type FileChange struct {
	Path         string     `json:"path"` // Relative to WorkingDir
	ChangeType   ChangeType `json:"change_type"`
	LinesAdded   int        `json:"lines_added,omitempty"`
	LinesRemoved int        `json:"lines_removed,omitempty"`
}

// StepPlan describes a planned micro-step from the LLM decomposition.
type StepPlan struct {
	StepNumber      int    `json:"step_number"`
	Description     string `json:"description"`
	ExpectedOutcome string `json:"expected_outcome"`
}

// TaskResult contains the complete outcome of a CLI agent task.
type TaskResult struct {
	TaskID        string        `json:"task_id"`
	Status        TaskStatus    `json:"status"`
	Summary       string        `json:"summary"`
	Steps         []StepResult  `json:"steps"`
	FilesChanged  []FileChange  `json:"files_changed"`
	TotalUsage    StepUsage     `json:"total_usage"`
	StepsExecuted int           `json:"steps_executed"`
	Duration      time.Duration `json:"duration"`
	Error         string        `json:"error,omitempty"`
}

// Capabilities describes what a CLI backend can do.
type Capabilities struct {
	SupportsTools     bool     `json:"supports_tools"`
	SupportsStreaming bool     `json:"supports_streaming"`
	MaxContextWindow  int      `json:"max_context_window"`
	SupportedFormats  []string `json:"supported_formats"`
}

// Info provides metadata about a registered CLI agent backend.
type Info struct {
	Backend      string       `json:"backend"`
	Available    bool         `json:"available"`
	Models       []string     `json:"models,omitempty"`
	Capabilities Capabilities `json:"capabilities"`
}
