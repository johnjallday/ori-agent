package cliagent

import "context"

// CLIAgentAdapter is the interface that CLI agent backends must implement.
// Each adapter wraps a specific CLI tool (Claude CLI, Codex CLI) and translates
// step requests into CLI invocations.
type CLIAgentAdapter interface {
	// ExecuteStep runs a single micro-step via the CLI and returns the result.
	ExecuteStep(ctx context.Context, req StepRequest) (*StepResult, error)

	// Capabilities reports what this CLI backend can do.
	Capabilities() CLIAgentCapabilities

	// AvailableModels returns the list of models this backend supports.
	AvailableModels() []string

	// IsAvailable checks whether the CLI is installed and ready to use.
	IsAvailable() bool

	// Backend returns the backend identifier (e.g., "claude", "codex").
	Backend() string
}
