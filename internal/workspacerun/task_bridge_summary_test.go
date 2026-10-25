package workspacerun

import (
	"context"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestTaskRunBridge_SummaryRecordsDeniedEffectsAndNoToolContext(t *testing.T) {
	ctx := context.Background()
	runs := NewMemoryStore()
	workspaces := workspace.NewInMemoryStore()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Summary"})
	task := workspace.Task{ID: "summary", WorkspaceID: ws.ID, To: "Reader", Description: "Read saved session", Status: workspace.TaskStatusPending,
		Context: map[string]any{"template_id": "folder-digest", "template_starter_task": true}}
	if _, err := ws.AddRecipeTask(task, workspace.FirstTaskRecipeFolderSummary, 1); err != nil {
		t.Fatal(err)
	}
	if err := workspaces.Save(ws); err != nil {
		t.Fatal(err)
	}
	registry := NewExecutorRegistry()
	registry.Register(ExecutorKindOriAgent, NewOriAgentExecutor(&stubOriAgentTaskRunner{result: "Report"}))
	service := NewService(runs, NewProfileRegistry(), registry, NewLocalEnvironmentManager(t.TempDir()), NewValidator(), nil)
	task.Context = nil // Copied payloads cannot remove canonical saved policy.
	result, err := NewTaskRunBridge(runs, service, workspaces).ExecuteTaskRun(ctx, "Reader", task)
	if err != nil {
		t.Fatal(err)
	}
	run, err := runs.GetRun(ctx, ws.ID, result.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Policy.Mutation != PolicyMutationDenied || run.Policy.ExternalEffects != PolicyExternalEffectsDenied || run.ContextPlan.ExposeWorkspaceTools || run.ContextPlan.IncludeWorkspaceSnapshot || run.ContextPlan.IncludeAttachedFiles {
		t.Fatalf("summary trace overstates its authority: %+v %+v", run.Policy, run.ContextPlan)
	}
}
