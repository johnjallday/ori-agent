package workspacerun

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// TaskRunBridge adapts the existing workspace.TaskHandler contract onto
// Workspace Runs so task callers keep their legacy result API while execution
// becomes durable and inspectable through the harness.
type TaskRunBridge struct {
	store          Store
	service        *Service
	workspaceStore workspace.Store
}

func NewTaskRunBridge(store Store, service *Service, workspaceStore workspace.Store) *TaskRunBridge {
	return &TaskRunBridge{
		store:          store,
		service:        service,
		workspaceStore: workspaceStore,
	}
}

func (b *TaskRunBridge) ExecuteTask(ctx context.Context, agentName string, task workspace.Task) (string, error) {
	result, err := b.ExecuteTaskRun(ctx, agentName, task)
	return result.Result, err
}

func (b *TaskRunBridge) ExecuteTaskRun(ctx context.Context, agentName string, task workspace.Task) (workspace.TaskRunResult, error) {
	if b == nil || b.store == nil || b.service == nil {
		return workspace.TaskRunResult{}, fmt.Errorf("workspace run task bridge is not configured")
	}
	payload, err := json.Marshal(task)
	if err != nil {
		return workspace.TaskRunResult{}, fmt.Errorf("marshal workspace task payload: %w", err)
	}
	rawPayload := RawConfig(payload)
	cfgPayload, err := json.Marshal(OriAgentExecutorConfig{TaskPayload: &rawPayload})
	if err != nil {
		return workspace.TaskRunResult{}, fmt.Errorf("marshal ori_agent executor config: %w", err)
	}
	rawConfig := RawConfig(cfgPayload)

	run, err := b.service.CreateRun(ctx, task.WorkspaceID, CreateRunRequest{
		ProfileID: ProfileGeneral,
		Executor: Executor{
			Kind:   ExecutorKindOriAgent,
			Ref:    agentName,
			Config: &rawConfig,
		},
		Prompt: task.Description,
		Scope: Scope{
			TargetTaskID: task.ID,
		},
		Policy: Policy{
			Mutation: PolicyMutationAllowed,
			Approval: PolicyApprovalNone,
		},
		ContextPlan:       DefaultTaskContextPlan(),
		ValidationRequest: &ValidationRequest{Profile: ValidationProfileNone},
	})
	if err != nil {
		return workspace.TaskRunResult{}, err
	}
	b.persistCurrentRunID(task.WorkspaceID, task.ID, run.ID)

	if err := b.service.ExecuteRun(ctx, task.WorkspaceID, run.ID); err != nil {
		return workspace.TaskRunResult{RunID: run.ID}, err
	}
	artifacts, err := b.store.ListArtifacts(ctx, task.WorkspaceID, run.ID)
	if err != nil {
		return workspace.TaskRunResult{RunID: run.ID}, err
	}
	result, ok := taskResultFromArtifacts(artifacts)
	if !ok {
		return workspace.TaskRunResult{RunID: run.ID}, fmt.Errorf("workspace run %s completed without a task result artifact", run.ID)
	}
	return workspace.TaskRunResult{Result: result, RunID: run.ID}, nil
}

func (b *TaskRunBridge) persistCurrentRunID(workspaceID, taskID, runID string) {
	if b.workspaceStore == nil || strings.TrimSpace(workspaceID) == "" || strings.TrimSpace(taskID) == "" || strings.TrimSpace(runID) == "" {
		return
	}
	_ = b.workspaceStore.Update(workspaceID, func(ws *workspace.Workspace) error {
		return ws.MutateTask(taskID, func(task *workspace.Task) error {
			task.CurrentRunID = runID
			return nil
		})
	})
}

func taskResultFromArtifacts(artifacts []Artifact) (string, bool) {
	for i := len(artifacts) - 1; i >= 0; i-- {
		artifact := artifacts[i]
		if artifact.Kind != ArtifactFile {
			continue
		}
		role, _ := artifact.Metadata["role"].(string)
		if role != oriAgentTaskResultRole {
			continue
		}
		return string(artifact.Inline), true
	}
	return "", false
}

var _ workspace.RunAwareTaskHandler = (*TaskRunBridge)(nil)
