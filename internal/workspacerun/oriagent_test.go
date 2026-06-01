package workspacerun

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestOriAgentExecutorConfigDecodingAndDisabledExecution(t *testing.T) {
	executor := NewOriAgentExecutor()
	run := &Run{
		ID: "run-1",
		Executor: Executor{
			Kind: ExecutorKindOriAgent,
			Ref:  "workspace-agent",
			Config: testRawConfig(t, OriAgentExecutorConfig{
				Model: "codex",
				ToolPolicy: ToolPolicy{
					Mode:      "allowlist",
					Allowlist: []string{"mcp:web/*"},
				},
				MemoryPolicy: MemoryPolicy{
					Enabled: true,
					Types:   []string{"user", "project"},
				},
				ContextPolicy: ContextPolicy{
					Strategy:        "compact",
					KeepRecentTurns: 8,
				},
				Subagents: SubagentPolicy{MaxConcurrent: 2},
			}),
		},
	}

	cfg, err := DecodeExecutorConfig[OriAgentExecutorConfig](run.Executor)
	if err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if cfg.Model != "codex" || cfg.ToolPolicy.Allowlist[0] != "mcp:web/*" || !cfg.MemoryPolicy.Enabled {
		t.Fatalf("config = %+v, want provider/model/tool/memory passthrough", cfg)
	}
	if err := executor.Execute(context.Background(), run); !errors.Is(err, ErrOriAgentExecutionDisabled) {
		t.Fatalf("Execute error = %v, want disabled execution error", err)
	}
	if err := executor.Cancel(context.Background(), run); err != nil {
		t.Fatalf("Cancel returned error: %v", err)
	}
}

func TestOriAgentExecutorRegistryRegistration(t *testing.T) {
	registry := NewExecutorRegistry()
	registry.Register(ExecutorKindOriAgent, NewOriAgentExecutor())

	runner, err := registry.Get(ExecutorKindOriAgent)
	if err != nil {
		t.Fatalf("Get ori_agent: %v", err)
	}
	if _, ok := runner.(*OriAgentExecutor); !ok {
		t.Fatalf("runner = %T, want *OriAgentExecutor", runner)
	}
}

func TestOriAgentExecutorRunsWorkspaceTaskAndCapturesResult(t *testing.T) {
	ctx := context.Background()
	taskPayload := testRawConfig(t, workspace.Task{
		ID:          "task-1",
		WorkspaceID: "workspace-1",
		Description: "do work",
	})
	runner := &stubOriAgentTaskRunner{result: "completed"}
	executor := NewOriAgentExecutor(runner)
	run := &Run{
		ID:           "run-1",
		ReferenceURL: "https://example.com/spec",
		Executor: Executor{
			Kind: ExecutorKindOriAgent,
			Ref:  "Ori",
			Config: testRawConfig(t, OriAgentExecutorConfig{
				TaskPayload: taskPayload,
			}),
		},
	}

	if err := executor.Execute(ctx, run); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	artifacts, err := executor.Artifacts(ctx, run)
	if err != nil {
		t.Fatalf("Artifacts: %v", err)
	}
	if result, ok := taskResultFromArtifacts(artifacts); !ok || result != "completed" {
		t.Fatalf("artifacts = %+v, want task result", artifacts)
	}
	if runner.lastTask.ReferenceURL != "https://example.com/spec" {
		t.Fatalf("task ReferenceURL = %q, want run reference URL propagated", runner.lastTask.ReferenceURL)
	}
	if !hasReferenceURLInspectionArtifact(artifacts, ReferenceURLInspectionUnknown) {
		t.Fatalf("artifacts = %+v, want reference URL inspection evidence", artifacts)
	}
}

func TestOriAgentExecutorPreparesTaskContextWhenRunnerSupportsIt(t *testing.T) {
	ctx := context.Background()
	taskPayload := testRawConfig(t, workspace.Task{
		ID:          "task-1",
		WorkspaceID: "workspace-1",
		Description: "do work",
	})
	runner := &stubOriAgentTaskRunner{
		prepared: &workspace.TaskPreparedContext{
			Strategy:   "task_default",
			Summary:    "prepared",
			PreparedAt: time.Now(),
			Items: []workspace.TaskPreparedContextItem{
				{Kind: "workspace_snapshot", Access: "injected"},
			},
			AvailableTools: []string{"workspace_notes"},
		},
	}
	executor := NewOriAgentExecutor(runner)
	run := &Run{
		ID:           "run-1",
		ReferenceURL: "https://example.com/spec",
		Executor: Executor{
			Kind: ExecutorKindOriAgent,
			Ref:  "Ori",
			Config: testRawConfig(t, OriAgentExecutorConfig{
				TaskPayload: taskPayload,
			}),
		},
	}

	prepared, err := executor.PrepareContext(ctx, run)
	if err != nil {
		t.Fatalf("PrepareContext: %v", err)
	}
	if prepared == nil || prepared.Summary != "prepared" || len(prepared.Items) != 1 || prepared.Items[0].Kind != "workspace_snapshot" {
		t.Fatalf("prepared = %+v, want converted task context", prepared)
	}
	if runner.lastPreparedTask.ReferenceURL != "https://example.com/spec" {
		t.Fatalf("prepared task ReferenceURL = %q, want run reference URL propagated", runner.lastPreparedTask.ReferenceURL)
	}
}

type stubOriAgentTaskRunner struct {
	result           string
	err              error
	prepared         *workspace.TaskPreparedContext
	lastTask         workspace.Task
	lastPreparedTask workspace.Task
}

func (r *stubOriAgentTaskRunner) ExecuteTask(_ context.Context, _ string, task workspace.Task) (string, error) {
	r.lastTask = task
	return r.result, r.err
}

func (r *stubOriAgentTaskRunner) PrepareTaskContext(_ context.Context, _ string, task workspace.Task) (*workspace.TaskPreparedContext, error) {
	r.lastPreparedTask = task
	return r.prepared, nil
}
