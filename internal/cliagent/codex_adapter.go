package cliagent

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/johnjallday/ori-agent/internal/llm"
)

// CodexCLIAdapter implements CLIAgentAdapter for the Codex CLI.
type CodexCLIAdapter struct {
	cliPath string
	invoker *CLIInvoker
}

// NewCodexCLIAdapter creates a CodexCLIAdapter that uses the given invoker for
// process execution. If invoker is nil, a default invoker is created.
func NewCodexCLIAdapter(invoker *CLIInvoker) (*CodexCLIAdapter, error) {
	cliPath, err := exec.LookPath("codex")
	if err != nil {
		return nil, fmt.Errorf("codex CLI not found: %w", err)
	}
	if invoker == nil {
		invoker = NewCLIInvoker()
	}
	return &CodexCLIAdapter{cliPath: cliPath, invoker: invoker}, nil
}

// Backend returns the backend identifier.
func (a *CodexCLIAdapter) Backend() string { return BackendCodex }

// IsAvailable checks whether the Codex CLI is installed.
func (a *CodexCLIAdapter) IsAvailable() bool {
	return a.cliPath != ""
}

// Capabilities returns Codex CLI capabilities.
func (a *CodexCLIAdapter) Capabilities() Capabilities {
	return Capabilities{
		SupportsTools:     true,
		SupportsStreaming: true,
		MaxContextWindow:  128000,
		SupportedFormats:  []string{"text", "jsonl"},
	}
}

// AvailableModels returns available Codex models, preferring the local Codex
// CLI model cache and falling back to a curated default when the cache is empty.
func (a *CodexCLIAdapter) AvailableModels() []string {
	if models := llm.LoadCodexCachedModels(); len(models) > 0 {
		return models
	}
	return []string{"gpt-5.3-codex"}
}

// ExecuteStep runs a single micro-step via the Codex CLI.
func (a *CodexCLIAdapter) ExecuteStep(ctx context.Context, req StepRequest) (*StepResult, error) {
	if a.invoker == nil {
		return nil, fmt.Errorf("codex adapter: invoker not initialized")
	}

	tmpOut, err := os.CreateTemp("", "codex-step-*.txt")
	if err != nil {
		return nil, fmt.Errorf("codex adapter: create temp file: %w", err)
	}
	tmpOutPath := tmpOut.Name()
	_ = tmpOut.Close()
	defer func() { _ = os.Remove(tmpOutPath) }()

	args := []string{
		"exec",
		"--json",
		"--sandbox", "workspace-write",
		"--skip-git-repo-check",
		"--output-last-message", tmpOutPath,
	}
	if req.Model != "" {
		args = append(args, "--model", req.Model)
	}
	if req.WorkingDir != "" {
		args = append(args, "--cd", req.WorkingDir)
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
		Format:     FormatCodexJSONL,
		OutputFile: tmpOutPath,
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
