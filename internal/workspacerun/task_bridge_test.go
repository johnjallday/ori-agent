package workspacerun

import (
	"context"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestTaskRunBridgeCreatesRunPersistsTaskLinkAndReturnsResult(t *testing.T) {
	ctx := context.Background()
	runStore := NewMemoryStore()
	workspaceStore := workspace.NewInMemoryStore()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{
		Name: "Workspace",
	})
	task := workspace.Task{
		ID:          "task-1",
		WorkspaceID: ws.ID,
		To:          "Ori",
		Description: "do work",
		Status:      workspace.TaskStatusAssigned,
	}
	if err := ws.AddTask(task); err != nil {
		t.Fatalf("add task: %v", err)
	}
	if err := workspaceStore.Save(ws); err != nil {
		t.Fatalf("save workspace: %v", err)
	}

	executors := NewExecutorRegistry()
	executors.Register(ExecutorKindOriAgent, NewOriAgentExecutor(&stubOriAgentTaskRunner{result: "finished"}))
	service := NewService(runStore, NewProfileRegistry(), executors, NewLocalEnvironmentManager(t.TempDir()), NewValidator(), nil)
	bridge := NewTaskRunBridge(runStore, service, workspaceStore)

	result, err := bridge.ExecuteTaskRun(ctx, "Ori", task)
	if err != nil {
		t.Fatalf("ExecuteTaskRun: %v", err)
	}
	if result.Result != "finished" || result.RunID == "" {
		t.Fatalf("result = %+v, want task result and run id", result)
	}

	run, err := runStore.GetRun(ctx, ws.ID, result.RunID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if run.Status != RunStatusSucceeded || run.Scope.TargetTaskID != task.ID || run.Executor.Ref != "Ori" {
		t.Fatalf("run = %+v, want succeeded task-scoped ori_agent run", run)
	}
	if run.ContextPlan.Strategy != "task_default" || !run.ContextPlan.IncludeWorkspaceSnapshot || !run.ContextPlan.ExposeWorkspaceTools {
		t.Fatalf("ContextPlan = %+v, want default task plan", run.ContextPlan)
	}

	fresh, err := workspaceStore.Get(ws.ID)
	if err != nil {
		t.Fatalf("get workspace: %v", err)
	}
	gotTask, err := fresh.GetTask(task.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if gotTask.CurrentRunID != result.RunID {
		t.Fatalf("CurrentRunID = %q, want %q", gotTask.CurrentRunID, result.RunID)
	}
}
