package workspacerun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

var ErrOriAgentExecutionDisabled = errors.New("ori_agent execution not enabled in MVP")

const oriAgentTaskResultRole = "task_result"

type OriAgentTaskRunner interface {
	ExecuteTask(ctx context.Context, agentName string, task workspace.Task) (string, error)
}

type OriAgentTaskContextPreparer interface {
	PrepareTaskContext(ctx context.Context, agentName string, task workspace.Task) (*workspace.TaskPreparedContext, error)
}

type OriAgentExecutor struct {
	runner OriAgentTaskRunner

	mu        sync.Mutex
	artifacts map[string][]Artifact
}

func NewOriAgentExecutor(runners ...OriAgentTaskRunner) *OriAgentExecutor {
	var runner OriAgentTaskRunner
	if len(runners) > 0 {
		runner = runners[0]
	}
	return &OriAgentExecutor{
		runner:    runner,
		artifacts: make(map[string][]Artifact),
	}
}

func (e *OriAgentExecutor) Execute(ctx context.Context, run *Run) error {
	if run == nil || e == nil || e.runner == nil {
		return ErrOriAgentExecutionDisabled
	}
	e.reset(run.ID)

	cfg, err := DecodeExecutorConfig[OriAgentExecutorConfig](run.Executor)
	if err != nil {
		return err
	}
	task, err := decodeOriAgentTaskPayload(cfg.TaskPayload)
	if err != nil {
		return err
	}
	result, err := e.runner.ExecuteTask(ctx, run.Executor.Ref, task)
	if err != nil {
		return err
	}
	e.addArtifact(run.ID, NewArtifact(
		run.ID,
		ArtifactFile,
		ArtifactInline([]byte(result)),
		ArtifactMetadata(map[string]any{
			"role":     oriAgentTaskResultRole,
			"executor": string(ExecutorKindOriAgent),
		}),
	))
	e.addArtifact(run.ID, NewArtifact(
		run.ID,
		ArtifactTaskRawResult,
		ArtifactInline([]byte(result)),
		ArtifactMetadata(map[string]any{
			"role":     oriAgentTaskResultRole,
			"executor": string(ExecutorKindOriAgent),
			"task_id":  task.ID,
		}),
	))
	return nil
}

func (e *OriAgentExecutor) PrepareContext(ctx context.Context, run *Run) (*PreparedContext, error) {
	if run == nil || e == nil || e.runner == nil {
		return nil, nil
	}
	preparer, ok := e.runner.(OriAgentTaskContextPreparer)
	if !ok {
		return nil, nil
	}
	cfg, err := DecodeExecutorConfig[OriAgentExecutorConfig](run.Executor)
	if err != nil {
		return nil, err
	}
	task, err := decodeOriAgentTaskPayload(cfg.TaskPayload)
	if err != nil {
		return nil, err
	}
	prepared, err := preparer.PrepareTaskContext(ctx, run.Executor.Ref, task)
	if err != nil {
		return nil, err
	}
	if prepared == nil {
		return nil, nil
	}
	items := make([]PreparedContextItem, 0, len(prepared.Items))
	for _, item := range prepared.Items {
		items = append(items, PreparedContextItem{
			Kind:   item.Kind,
			Ref:    item.Ref,
			Name:   item.Name,
			Access: item.Access,
			Detail: item.Detail,
		})
	}
	return &PreparedContext{
		Strategy:       prepared.Strategy,
		Summary:        prepared.Summary,
		Items:          items,
		AvailableTools: append([]string(nil), prepared.AvailableTools...),
		PreparedAt:     prepared.PreparedAt,
	}, nil
}

func (e *OriAgentExecutor) Cancel(_ context.Context, _ *Run) error {
	return nil
}

func (e *OriAgentExecutor) Artifacts(_ context.Context, run *Run) ([]Artifact, error) {
	if e == nil || run == nil {
		return nil, nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return CloneArtifacts(e.artifacts[run.ID]), nil
}

func (e *OriAgentExecutor) reset(runID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.artifacts, runID)
}

func (e *OriAgentExecutor) addArtifact(runID string, artifact Artifact) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.artifacts[runID] = append(e.artifacts[runID], artifact)
}

func decodeOriAgentTaskPayload(raw *RawConfig) (workspace.Task, error) {
	if raw == nil || len(*raw) == 0 {
		return workspace.Task{}, fmt.Errorf("ori_agent task_payload is required")
	}
	var task workspace.Task
	if err := json.Unmarshal(*raw, &task); err != nil {
		return workspace.Task{}, fmt.Errorf("decode ori_agent task payload: %w", err)
	}
	if task.ID == "" || task.WorkspaceID == "" {
		return workspace.Task{}, fmt.Errorf("ori_agent task_payload requires task id and workspace id")
	}
	return task, nil
}

var _ ContextPreparer = (*OriAgentExecutor)(nil)
