package workspacerun

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/cliagent"
)

func TestNativeCLIExecutorSuccessCapturesArtifactsTraceAndCost(t *testing.T) {
	ctx := context.Background()
	workingDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workingDir, "existing.txt"), []byte("before"), 0644); err != nil {
		t.Fatalf("write existing file: %v", err)
	}

	adapter := &stubCLIAgentAdapter{
		backend:   cliagent.BackendCodex,
		available: true,
		onExecute: func(req cliagent.StepRequest) {
			if err := os.WriteFile(filepath.Join(req.WorkingDir, "new.txt"), []byte("after"), 0644); err != nil {
				t.Errorf("write changed file: %v", err)
			}
		},
		result: &cliagent.StepResult{
			Output: "done",
			Events: []cliagent.CLIEvent{
				{Type: "tool_call", StepNumber: 1, Content: "write file", Payload: map[string]interface{}{"tool": "write"}},
			},
			Usage:  cliagent.StepUsage{InputTokens: 10, OutputTokens: 5, CostUSD: 0.01},
			Status: cliagent.StepCompleted,
		},
	}
	executor := NewNativeCLIExecutor(cliagent.NewRegistry(adapter))
	run := &Run{
		ID:     "run-1",
		Prompt: "change a file",
		Executor: Executor{
			Kind:   ExecutorKindNativeCLI,
			Ref:    cliagent.BackendCodex,
			Config: testRawConfig(t, NativeCLIExecutorConfig{Model: "gpt-5.3-codex"}),
		},
		Scope: Scope{RepoPath: workingDir},
	}

	if err := executor.Execute(ctx, run); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if adapter.lastRequest.Model != "gpt-5.3-codex" || adapter.lastRequest.WorkingDir != workingDir {
		t.Fatalf("request = %+v, want model and working dir", adapter.lastRequest)
	}
	if run.Cost == nil || run.Cost.TotalTokens != 15 || run.Cost.USD != 0.01 {
		t.Fatalf("Cost = %+v, want CLI usage", run.Cost)
	}

	artifacts, err := executor.Artifacts(ctx, run)
	if err != nil {
		t.Fatalf("artifacts: %v", err)
	}
	if !hasArtifactKind(artifacts, ArtifactLog) {
		t.Fatalf("artifacts = %+v, want log artifact", artifacts)
	}
	if !changedFilesArtifactContains(artifacts, "new.txt") {
		t.Fatalf("artifacts = %+v, want changed file artifact", artifacts)
	}
	if !hasArtifactKind(artifacts, ArtifactDiff) {
		t.Fatalf("artifacts = %+v, want diff/change-summary artifact", artifacts)
	}

	trace, err := executor.TraceEvents(ctx, run)
	if err != nil {
		t.Fatalf("trace events: %v", err)
	}
	if !hasTraceKind(trace, TraceToolCall) || !hasTraceKind(trace, TraceMessage) {
		t.Fatalf("trace = %+v, want CLI event and output message", trace)
	}
}

func TestNativeCLIExecutorRejectsUnsafeArgsUnsupportedBackendAndGemini(t *testing.T) {
	ctx := context.Background()
	workingDir := t.TempDir()
	executor := NewNativeCLIExecutor(cliagent.NewRegistry(&stubCLIAgentAdapter{backend: cliagent.BackendCodex, available: true}))

	run := &Run{
		ID:       "run-1",
		Executor: Executor{Kind: ExecutorKindNativeCLI, Ref: cliagent.BackendCodex, Config: testRawConfig(t, NativeCLIExecutorConfig{Args: []string{"--dangerously-allow-write-to-arbitrary-path"}})},
		Scope:    Scope{RepoPath: workingDir},
	}
	if err := executor.Execute(ctx, run); err == nil || !strings.Contains(err.Error(), "args are not enabled") {
		t.Fatalf("Execute with unsafe args error = %v, want args rejection", err)
	}

	run.Executor = Executor{Kind: ExecutorKindNativeCLI, Ref: "aider"}
	if err := executor.Execute(ctx, run); err == nil || !strings.Contains(err.Error(), "unsupported native_cli backend") {
		t.Fatalf("Execute with unsupported backend error = %v, want unsupported backend", err)
	}

	run.Executor = Executor{Kind: ExecutorKindNativeCLI, Ref: cliagent.BackendGemini}
	if err := executor.Execute(ctx, run); err == nil || !strings.Contains(err.Error(), "out of scope for MVP") {
		t.Fatalf("Execute with Gemini error = %v, want MVP out-of-scope", err)
	}
}

func TestNativeCLIExecutorFailureAndCancel(t *testing.T) {
	ctx := context.Background()
	adapter := &stubCLIAgentAdapter{
		backend:   cliagent.BackendClaude,
		available: true,
		result:    &cliagent.StepResult{Status: cliagent.StepFailed, Error: "boom"},
	}
	executor := NewNativeCLIExecutor(cliagent.NewRegistry(adapter))
	run := &Run{
		ID:       "run-1",
		Executor: Executor{Kind: ExecutorKindNativeCLI, Ref: cliagent.BackendClaude},
		Scope:    Scope{RepoPath: t.TempDir()},
	}

	if err := executor.Execute(ctx, run); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("Execute failed error = %v, want backend failure", err)
	}
	if err := executor.Cancel(ctx, run); err != nil {
		t.Fatalf("Cancel returned error: %v", err)
	}
}

func TestNativeCLIExecutorPropagatesAdapterError(t *testing.T) {
	ctx := context.Background()
	wantErr := errors.New("adapter unavailable during step")
	executor := NewNativeCLIExecutor(cliagent.NewRegistry(&stubCLIAgentAdapter{
		backend:   cliagent.BackendCodex,
		available: true,
		err:       wantErr,
	}))
	run := &Run{
		ID:       "run-1",
		Executor: Executor{Kind: ExecutorKindNativeCLI, Ref: cliagent.BackendCodex},
		Scope:    Scope{RepoPath: t.TempDir()},
	}

	if err := executor.Execute(ctx, run); !errors.Is(err, wantErr) {
		t.Fatalf("Execute error = %v, want %v", err, wantErr)
	}
}

type stubCLIAgentAdapter struct {
	backend     string
	available   bool
	result      *cliagent.StepResult
	err         error
	onExecute   func(cliagent.StepRequest)
	lastRequest cliagent.StepRequest
}

func (a *stubCLIAgentAdapter) ExecuteStep(_ context.Context, req cliagent.StepRequest) (*cliagent.StepResult, error) {
	a.lastRequest = req
	if a.onExecute != nil {
		a.onExecute(req)
	}
	return a.result, a.err
}

func (a *stubCLIAgentAdapter) Capabilities() cliagent.CLIAgentCapabilities {
	return cliagent.CLIAgentCapabilities{SupportsStreaming: true}
}

func (a *stubCLIAgentAdapter) AvailableModels() []string {
	return []string{"test-model"}
}

func (a *stubCLIAgentAdapter) IsAvailable() bool {
	return a.available
}

func (a *stubCLIAgentAdapter) Backend() string {
	return a.backend
}

func testRawConfig(t *testing.T, value interface{}) *RawConfig {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	raw := RawConfig(data)
	return &raw
}

func hasArtifactKind(artifacts []Artifact, kind ArtifactKind) bool {
	for _, artifact := range artifacts {
		if artifact.Kind == kind {
			return true
		}
	}
	return false
}

func changedFilesArtifactContains(artifacts []Artifact, path string) bool {
	for _, artifact := range artifacts {
		if artifact.Kind != ArtifactChangedFiles {
			continue
		}
		files, ok := artifact.Metadata["files"].([]string)
		if !ok {
			return false
		}
		for _, file := range files {
			if file == path {
				return true
			}
		}
	}
	return false
}

func hasTraceKind(events []TraceEvent, kind TraceEventKind) bool {
	for _, event := range events {
		if event.Kind == kind {
			return true
		}
	}
	return false
}
