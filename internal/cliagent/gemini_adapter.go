package cliagent

import (
	"context"
	"fmt"
	"os/exec"
)

// GeminiCLIAdapter implements CLIAgentAdapter for the Gemini CLI.
type GeminiCLIAdapter struct {
	cliPath string
	invoker *CLIInvoker
}

// NewGeminiCLIAdapter creates a GeminiCLIAdapter that uses the given invoker for
// process execution. If invoker is nil, a default invoker is created.
func NewGeminiCLIAdapter(invoker *CLIInvoker) (*GeminiCLIAdapter, error) {
	cliPath, err := exec.LookPath("gemini")
	if err != nil {
		return nil, fmt.Errorf("gemini CLI not found: %w", err)
	}
	if invoker == nil {
		invoker = NewCLIInvoker()
	}
	return &GeminiCLIAdapter{cliPath: cliPath, invoker: invoker}, nil
}

// Backend returns the backend identifier.
func (a *GeminiCLIAdapter) Backend() string { return BackendGemini }

// IsAvailable checks whether the Gemini CLI is installed.
func (a *GeminiCLIAdapter) IsAvailable() bool {
	return a.cliPath != ""
}

// Capabilities returns Gemini CLI capabilities.
func (a *GeminiCLIAdapter) Capabilities() CLIAgentCapabilities {
	return CLIAgentCapabilities{
		SupportsTools:     true,
		SupportsStreaming: true,
		MaxContextWindow:  1000000,
		SupportedFormats:  []string{"text", "json", "stream-json"},
	}
}

// AvailableModels returns the known Gemini model names.
func (a *GeminiCLIAdapter) AvailableModels() []string {
	return []string{"gemini-2.5-pro", "gemini-2.5-flash", "gemini-2.0-flash"}
}

// ExecuteStep runs a single micro-step via the Gemini CLI.
func (a *GeminiCLIAdapter) ExecuteStep(ctx context.Context, req StepRequest) (*StepResult, error) {
	if a.invoker == nil {
		return nil, fmt.Errorf("gemini adapter: invoker not initialized")
	}

	args := []string{
		"--prompt", req.Prompt,
		"--output-format", "stream-json",
		"--approval-mode", "yolo",
	}
	if req.Model != "" {
		args = append(args, "--model", req.Model)
	}

	timeout := req.Budget.Timeout
	if timeout == 0 {
		timeout = DefaultStepTimeout
	}

	invocation := CLIInvocation{
		CLIPath:    a.cliPath,
		Args:       args,
		WorkingDir: req.WorkingDir,
		Timeout:    timeout,
		Format:     FormatGeminiStreamJSON,
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
