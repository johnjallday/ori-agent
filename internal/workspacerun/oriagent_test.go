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
	executor := NewOriAgentExecutor(&stubOriAgentTaskRunner{result: "completed"})
	run := &Run{
		ID: "run-1",
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
}

func TestOriAgentExecutorPreparesTaskContextWhenRunnerSupportsIt(t *testing.T) {
	ctx := context.Background()
	taskPayload := testRawConfig(t, workspace.Task{
		ID:          "task-1",
		WorkspaceID: "workspace-1",
		Description: "do work",
	})
	executor := NewOriAgentExecutor(&stubOriAgentTaskRunner{
		prepared: &workspace.TaskPreparedContext{
			Strategy:   "task_default",
			Summary:    "prepared",
			PreparedAt: time.Now(),
			Items: []workspace.TaskPreparedContextItem{
				{Kind: "workspace_snapshot", Access: "injected"},
			},
			AvailableTools: []string{"workspace_notes"},
		},
	})
	run := &Run{
		ID: "run-1",
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
}

type stubOriAgentTaskRunner struct {
	result   string
	err      error
	prepared *workspace.TaskPreparedContext
}

func (r *stubOriAgentTaskRunner) ExecuteTask(_ context.Context, _ string, _ workspace.Task) (string, error) {
	return r.result, r.err
}

func (r *stubOriAgentTaskRunner) PrepareTaskContext(_ context.Context, _ string, _ workspace.Task) (*workspace.TaskPreparedContext, error) {
	return r.prepared, nil
}
