package cliagent

import (
	"context"
	"fmt"
	"os/exec"
)

// ClaudeCLIAdapter implements CLIAgentAdapter for the Claude CLI.
type ClaudeCLIAdapter struct {
	cliPath string
	invoker *CLIInvoker
}

// NewClaudeCLIAdapter creates a ClaudeCLIAdapter that uses the given invoker for
// process execution. If invoker is nil, a default invoker is created.
func NewClaudeCLIAdapter(invoker *CLIInvoker) (*ClaudeCLIAdapter, error) {
	cliPath, err := exec.LookPath("claude")
	if err != nil {
		return nil, fmt.Errorf("claude CLI not found: %w", err)
	}
	if invoker == nil {
		invoker = NewCLIInvoker()
	}
	return &ClaudeCLIAdapter{cliPath: cliPath, invoker: invoker}, nil
}

// Backend returns the backend identifier.
func (a *ClaudeCLIAdapter) Backend() string { return BackendClaude }

// IsAvailable checks whether the Claude CLI is installed.
func (a *ClaudeCLIAdapter) IsAvailable() bool {
	return a.cliPath != ""
}

// Capabilities returns Claude CLI capabilities.
func (a *ClaudeCLIAdapter) Capabilities() CLIAgentCapabilities {
	return CLIAgentCapabilities{
		SupportsTools:     true,
		SupportsStreaming: true,
		MaxContextWindow:  200000,
		SupportedFormats:  []string{"text", "stream-json"},
	}
}

// AvailableModels returns the known Claude model aliases.
func (a *ClaudeCLIAdapter) AvailableModels() []string {
	return []string{"opus", "sonnet", "haiku"}
}

// ExecuteStep runs a single micro-step via the Claude CLI.
func (a *ClaudeCLIAdapter) ExecuteStep(ctx context.Context, req StepRequest) (*StepResult, error) {
	if a.invoker == nil {
		return nil, fmt.Errorf("claude adapter: invoker not initialized")
	}

	args := []string{
		"--print",
		"--output-format", "stream-json",
		"--permission-mode", "dontAsk",
		"--allowedTools", "Bash Edit Read Write Glob Grep",
	}
	if req.Model != "" {
		args = append(args, "--model", req.Model)
	}
	if req.Budget.MaxCostUSD > 0 {
		args = append(args, "--max-budget-usd", fmt.Sprintf("%.4f", req.Budget.MaxCostUSD))
	}
	args = append(args, req.Prompt)

	timeout := req.Budget.Timeout
	if timeout == 0 {
		timeout = DefaultStepTimeout
	}

	invocation := CLIInvocation{
		CLIPath:    a.cliPath,
		Args:       args,
		WorkingDir: req.WorkingDir,
		Timeout:    timeout,
		Format:     FormatClaudeStreamJSON,
	}

	raw, err := a.invoker.Invoke(ctx, invocation)
	if err != nil {
		return &StepResult{
			StepNumber: req.StepNumber,
			Status:     StepFailed,
			Error:      err.Error(),
		}, nil
	}

	return mapRawToStepResult(req.StepNumber, raw), nil
}
