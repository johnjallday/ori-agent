package workspacerun

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/johnjallday/ori-agent/internal/cliagent"
)

type NativeCLIExecutor struct {
	registry *cliagent.CLIAgentRegistry
	diff     *cliagent.DiffDetector

	mu        sync.Mutex
	artifacts map[string][]Artifact
	traces    map[string][]TraceEvent
}

func NewNativeCLIExecutor(registry *cliagent.CLIAgentRegistry) *NativeCLIExecutor {
	if registry == nil {
		registry = cliagent.NewRegistry()
		registry.AutoDetect()
	}
	return &NativeCLIExecutor{
		registry:  registry,
		diff:      cliagent.NewDiffDetector(),
		artifacts: make(map[string][]Artifact),
		traces:    make(map[string][]TraceEvent),
	}
}

func (e *NativeCLIExecutor) Execute(ctx context.Context, run *Run) error {
	if run == nil {
		return fmt.Errorf("run is nil")
	}
	e.reset(run.ID)
	cfg, err := DecodeExecutorConfig[NativeCLIExecutorConfig](run.Executor)
	if err != nil {
		return err
	}
	if len(cfg.Args) > 0 {
		return fmt.Errorf("native_cli args are not enabled in MVP")
	}
	backend := strings.ToLower(strings.TrimSpace(run.Executor.Ref))
	if backend == cliagent.BackendGemini {
		return fmt.Errorf("native_cli backend %q is out of scope for MVP", backend)
	}
	if backend != cliagent.BackendCodex && backend != cliagent.BackendClaude {
		return fmt.Errorf("unsupported native_cli backend %q", run.Executor.Ref)
	}
	adapter, err := e.registry.Get(backend)
	if err != nil {
		return err
	}
	if !adapter.IsAvailable() {
		return fmt.Errorf("native_cli backend %q is not available", backend)
	}

	workingDir := strings.TrimSpace(run.Scope.RepoPath)
	if workingDir == "" {
		workingDir = strings.TrimSpace(run.Environment.WorktreePath)
	}
	if workingDir == "" {
		return fmt.Errorf("native_cli run requires repo_path or worktree_path")
	}

	snapshot, _ := e.diff.Snapshot(workingDir)
	result, err := adapter.ExecuteStep(ctx, cliagent.StepRequest{
		TaskID:     run.ID,
		StepNumber: 1,
		Prompt:     run.Prompt,
		WorkingDir: workingDir,
		Model:      cfg.Model,
		Budget: cliagent.StepBudget{
			Timeout: cliagent.DefaultStepTimeout,
		},
	})
	if err != nil {
		return err
	}
	if result == nil {
		return fmt.Errorf("native_cli backend %q returned no result", backend)
	}
	e.addTraceEvents(run.ID, nativeCLITraceEvents(run.ID, result.Events)...)
	if result.Output != "" {
		e.addTraceEvents(run.ID, NewTraceEvent(run.ID, TraceMessage, TraceSource("native_cli"), TraceMessageText(result.Output), TraceData(map[string]interface{}{"backend": backend})))
		e.addArtifact(run.ID, NewArtifact(run.ID, ArtifactLog, ArtifactInline([]byte(result.Output)), ArtifactMetadata(map[string]interface{}{"backend": backend})))
	}
	if snapshot != nil {
		if changes, cmpErr := e.diff.Compare(snapshot, workingDir); cmpErr == nil && len(changes) > 0 {
			files := make([]string, 0, len(changes))
			for _, change := range changes {
				files = append(files, change.Path)
			}
			e.addArtifact(run.ID, NewArtifact(run.ID, ArtifactChangedFiles, ArtifactMetadata(map[string]interface{}{"files": files})))
			e.addArtifact(run.ID, NewArtifact(run.ID, ArtifactDiff, ArtifactInline(nativeCLIChangeSummary(changes)), ArtifactMetadata(map[string]interface{}{"format": "change_summary"})))
		}
	}
	if result.Usage.TotalTokens() > 0 || result.Usage.CostUSD > 0 {
		run.Cost = &CostSummary{
			InputTokens:  result.Usage.InputTokens,
			OutputTokens: result.Usage.OutputTokens,
			TotalTokens:  result.Usage.TotalTokens(),
			USD:          result.Usage.CostUSD,
		}
	}
	if result.Status == cliagent.StepFailed {
		return fmt.Errorf("native_cli backend %q failed: %s", backend, result.Error)
	}
	return nil
}

func (e *NativeCLIExecutor) Cancel(_ context.Context, _ *Run) error {
	return nil
}

func (e *NativeCLIExecutor) Artifacts(_ context.Context, run *Run) ([]Artifact, error) {
	if run == nil {
		return nil, nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return CloneArtifacts(e.artifacts[run.ID]), nil
}

func (e *NativeCLIExecutor) TraceEvents(_ context.Context, run *Run) ([]TraceEvent, error) {
	if run == nil {
		return nil, nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return CloneTraceEvents(e.traces[run.ID]), nil
}

func (e *NativeCLIExecutor) reset(runID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.artifacts, runID)
	delete(e.traces, runID)
}

func (e *NativeCLIExecutor) addArtifact(runID string, artifact Artifact) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.artifacts[runID] = append(e.artifacts[runID], artifact)
}

func (e *NativeCLIExecutor) addTraceEvents(runID string, events ...TraceEvent) {
	if len(events) == 0 {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.traces[runID] = append(e.traces[runID], events...)
}

func nativeCLITraceEvents(runID string, events []cliagent.CLIEvent) []TraceEvent {
	out := make([]TraceEvent, 0, len(events))
	for _, event := range events {
		kind := TraceMessage
		eventType := strings.ToLower(strings.TrimSpace(event.Type))
		switch eventType {
		case "tool_call":
			kind = TraceToolCall
		case "tool_result":
			kind = TraceToolResult
		case "error":
			kind = TraceError
		}
		data := map[string]interface{}{
			"cli_type":    event.Type,
			"step_number": event.StepNumber,
		}
		if event.Payload != nil {
			data["payload"] = event.Payload
		}
		out = append(out, NewTraceEvent(runID, kind, TraceSource("native_cli"), TraceMessageText(event.Content), TraceToolName(event.Type), TraceData(data)))
	}
	return out
}

func nativeCLIChangeSummary(changes []cliagent.FileChange) []byte {
	var builder strings.Builder
	for _, change := range changes {
		if builder.Len() > 0 {
			builder.WriteByte('\n')
		}
		if change.ChangeType != "" {
			builder.WriteString(string(change.ChangeType))
		} else {
			builder.WriteString("changed")
		}
		builder.WriteByte(' ')
		builder.WriteString(change.Path)
	}
	return []byte(builder.String())
}
