package agenthttp

import (
	"context"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// taskIDByDescription returns the generated ID of the task with the given
// description in a stored workspace, so tests can assert against real IDs.
func taskIDByDescription(t *testing.T, store workspace.Store, wsID, description string) string {
	t.Helper()
	ws, err := store.Get(wsID)
	if err != nil {
		t.Fatalf("get workspace %s: %v", wsID, err)
	}
	for _, task := range ws.Tasks {
		if task.Description == description {
			return task.ID
		}
	}
	t.Fatalf("no task with description %q in workspace %s", description, wsID)
	return ""
}

func newTaskMutationHandler(t *testing.T, store workspace.Store) *HomeAssistantAskHandler {
	t.Helper()
	return NewHomeAssistantAskHandler(
		HomeSnapshotSources{Workspaces: store, Now: time.Now},
		nil,
		nil,
	)
}

func TestDetectMutation_CreateTaskInWorkspace(t *testing.T) {
	store := workspace.NewInMemoryStore()
	makeTestWorkspace(t, store, "ws-1", "Q3 Planning", nil)
	h := newTaskMutationHandler(t, store)

	conf := h.detectHomeMutationRequest("create a task to summarize Q2 sales in Q3 Planning")
	if conf == nil {
		t.Fatal("expected a create_task confirmation")
	}
	if conf.ActionType != HomeActionCreateTask {
		t.Errorf("action type = %q, want %q", conf.ActionType, HomeActionCreateTask)
	}
	if got, _ := conf.Arguments["workspace_id"].(string); got != "ws-1" {
		t.Errorf("workspace_id = %q, want ws-1", got)
	}
	if got, _ := conf.Arguments["description"].(string); got != "summarize Q2 sales" {
		t.Errorf("description = %q, want %q", got, "summarize Q2 sales")
	}
}

func TestDetectMutation_CreateTaskWithoutDescriptionDeclines(t *testing.T) {
	store := workspace.NewInMemoryStore()
	makeTestWorkspace(t, store, "ws-1", "Q3 Planning", nil)
	h := newTaskMutationHandler(t, store)

	if conf := h.detectHomeMutationRequest("add a task in Q3 Planning"); conf != nil {
		t.Fatalf("expected no confirmation when description is empty, got %+v", conf)
	}
}

func TestDetectMutation_CreateTaskUnknownWorkspaceDeclines(t *testing.T) {
	store := workspace.NewInMemoryStore()
	makeTestWorkspace(t, store, "ws-1", "Q3 Planning", nil)
	h := newTaskMutationHandler(t, store)

	if conf := h.detectHomeMutationRequest("create a task to do x in Nonexistent"); conf != nil {
		t.Fatalf("expected no confirmation for unknown workspace, got %+v", conf)
	}
}

func TestDetectMutation_StartTaskByDescription(t *testing.T) {
	store := workspace.NewInMemoryStore()
	makeTestWorkspace(t, store, "ws-ops", "Operations", []workspace.Task{
		{Description: "deploy to production", Status: workspace.TaskStatusPending, CreatedAt: time.Now()},
		{Description: "rotate signing keys", Status: workspace.TaskStatusPending, CreatedAt: time.Now()},
	})
	h := newTaskMutationHandler(t, store)
	deployID := taskIDByDescription(t, store, "ws-ops", "deploy to production")

	conf := h.detectHomeMutationRequest("start the deploy task in Operations")
	if conf == nil {
		t.Fatal("expected a start_task confirmation")
	}
	if conf.ActionType != HomeActionStartTask {
		t.Errorf("action type = %q, want %q", conf.ActionType, HomeActionStartTask)
	}
	if got, _ := conf.Arguments["workspace_id"].(string); got != "ws-ops" {
		t.Errorf("workspace_id = %q, want ws-ops", got)
	}
	if got, _ := conf.Arguments["task_id"].(string); got != deployID {
		t.Errorf("task_id = %q, want %q (deploy task)", got, deployID)
	}
}

func TestDetectMutation_StartTaskSingleRunnable(t *testing.T) {
	store := workspace.NewInMemoryStore()
	makeTestWorkspace(t, store, "ws-solo", "Solo", []workspace.Task{
		{Description: "the only pending job", Status: workspace.TaskStatusPending, CreatedAt: time.Now()},
	})
	h := newTaskMutationHandler(t, store)
	onlyID := taskIDByDescription(t, store, "ws-solo", "the only pending job")

	conf := h.detectHomeMutationRequest("run the task in Solo")
	if conf == nil {
		t.Fatal("expected a start_task confirmation for the single runnable task")
	}
	if got, _ := conf.Arguments["task_id"].(string); got != onlyID {
		t.Errorf("task_id = %q, want %q", got, onlyID)
	}
}

func TestDetectMutation_StartTaskAmbiguousDeclines(t *testing.T) {
	store := workspace.NewInMemoryStore()
	makeTestWorkspace(t, store, "ws-ops", "Operations", []workspace.Task{
		{Description: "deploy to production", Status: workspace.TaskStatusPending, CreatedAt: time.Now()},
		{Description: "rotate signing keys", Status: workspace.TaskStatusPending, CreatedAt: time.Now()},
	})
	h := newTaskMutationHandler(t, store)

	// No descriptor that distinguishes a task, and more than one runnable task.
	if conf := h.detectHomeMutationRequest("start a task in Operations"); conf != nil {
		t.Fatalf("expected no confirmation for ambiguous start, got %+v", conf)
	}
}

func TestDetectMutation_StartTaskNoRunnableDeclines(t *testing.T) {
	store := workspace.NewInMemoryStore()
	makeTestWorkspace(t, store, "ws-done", "Archive", []workspace.Task{
		{Description: "shipped already", Status: workspace.TaskStatusCompleted, CreatedAt: time.Now()},
	})
	h := newTaskMutationHandler(t, store)

	if conf := h.detectHomeMutationRequest("start the shipped task in Archive"); conf != nil {
		t.Fatalf("expected no confirmation when no runnable task exists, got %+v", conf)
	}
}

func TestDetectMutation_QuestionFallsThrough(t *testing.T) {
	store := workspace.NewInMemoryStore()
	makeTestWorkspace(t, store, "ws-1", "Operations", []workspace.Task{
		{Description: "deploy to production", Status: workspace.TaskStatusPending, CreatedAt: time.Now()},
	})
	h := newTaskMutationHandler(t, store)

	for _, prompt := range []string{
		"how many tasks are in Operations",
		"what is the deploy task status in Operations",
		"summarize my activity",
	} {
		if conf := h.detectHomeMutationRequest(prompt); conf != nil {
			t.Errorf("prompt %q should not trigger a mutation, got %+v", prompt, conf)
		}
	}
}

func TestExtractTaskDescription(t *testing.T) {
	cases := []struct {
		prompt string
		wsName string
		want   string
	}{
		{"create a task to summarize sales in Q3 Planning", "Q3 Planning", "summarize sales"},
		{"add task: deploy to prod in Operations", "Operations", "deploy to prod"},
		{"new task that reviews the backlog in the Roadmap workspace", "Roadmap", "reviews the backlog"},
		{"make a task for onboarding docs in Docs", "Docs", "onboarding docs"},
		{"create a task in Ops", "Ops", ""},
	}
	for _, c := range cases {
		if got := extractTaskDescription(c.prompt, c.wsName); got != c.want {
			t.Errorf("extractTaskDescription(%q, %q) = %q, want %q", c.prompt, c.wsName, got, c.want)
		}
	}
}

// Regression test for a gap found via a Group 7 cross-surface audit:
// isRunnableTaskStatus (used by the "start task" NLU matcher) had no Backlog
// exclusion, so a Backlog item whose description matched a "start the X
// task" prompt could be proposed for execution.
func TestIsRunnableTaskStatus_ExcludesBacklog(t *testing.T) {
	if isRunnableTaskStatus(workspace.TaskStatusBacklog) {
		t.Error("Backlog must not be considered runnable")
	}
	if !isRunnableTaskStatus(workspace.TaskStatusPending) {
		t.Error("Pending must remain runnable")
	}
}

func TestExtractBacklogDescription(t *testing.T) {
	cases := []struct {
		prompt string
		wsName string
		want   string
	}{
		{"add explore competitor pricing to the backlog in Demo Quest Board", "Demo Quest Board", "explore competitor pricing"},
		{"add draft onboarding copy to backlog in Operations", "Operations", "draft onboarding copy"},
		{"create fix the header in the backlog in Website Redesign", "Website Redesign", "fix the header"},
		{"add to the backlog in Ops", "Ops", ""},
	}
	for _, c := range cases {
		if got := extractBacklogDescription(c.prompt, c.wsName); got != c.want {
			t.Errorf("extractBacklogDescription(%q, %q) = %q, want %q", c.prompt, c.wsName, got, c.want)
		}
	}
}

func TestDetectMutation_AddToBacklog(t *testing.T) {
	store := workspace.NewInMemoryStore()
	makeTestWorkspace(t, store, "ws-1", "Demo Quest Board", nil)
	h := newTaskMutationHandler(t, store)

	conf, decline := h.detectBacklogCaptureRequest("add explore competitor pricing to the backlog in Demo Quest Board")
	if decline != "" {
		t.Fatalf("expected no decline, got %q", decline)
	}
	if conf == nil {
		t.Fatal("expected a create_backlog_item confirmation")
	}
	if conf.ActionType != HomeActionCreateBacklogItem {
		t.Errorf("action type = %q, want %q", conf.ActionType, HomeActionCreateBacklogItem)
	}
	if got, _ := conf.Arguments["workspace_id"].(string); got != "ws-1" {
		t.Errorf("workspace_id = %q, want ws-1", got)
	}
	if got, _ := conf.Arguments["description"].(string); got != "explore competitor pricing" {
		t.Errorf("description = %q, want %q", got, "explore competitor pricing")
	}
}

// A prompt whose task description merely mentions the word "backlog" (as a
// subject, not a request to capture into Ori's Backlog feature) must not be
// misread as backlog capture — regression for the connective-phrase guard.
func TestDetectMutation_TaskMentioningBacklogWordNotMisreadAsBacklogCapture(t *testing.T) {
	store := workspace.NewInMemoryStore()
	makeTestWorkspace(t, store, "ws-1", "Roadmap", nil)
	h := newTaskMutationHandler(t, store)

	conf, decline := h.detectBacklogCaptureRequest("new task that reviews the backlog in the Roadmap workspace")
	if conf != nil || decline != "" {
		t.Fatalf("expected no backlog-capture match, got conf=%+v decline=%q", conf, decline)
	}
	// The same prompt is still recognized as ordinary task creation by the
	// ordinary detector chain.
	taskConf := h.detectHomeMutationRequest("new task that reviews the backlog in the Roadmap workspace")
	if taskConf == nil || taskConf.ActionType != HomeActionCreateTask {
		t.Fatalf("expected the prompt to still resolve as create_task, got %+v", taskConf)
	}
}

func TestDetectMutation_AddToBacklogUnknownWorkspaceDeclines(t *testing.T) {
	store := workspace.NewInMemoryStore()
	makeTestWorkspace(t, store, "ws-1", "Demo Quest Board", nil)
	h := newTaskMutationHandler(t, store)

	conf, decline := h.detectBacklogCaptureRequest("add fix the header to the backlog in Nonexistent")
	if conf != nil {
		t.Fatalf("expected no confirmation for an unresolved workspace, got %+v", conf)
	}
	if decline == "" {
		t.Fatal("expected a user-readable decline message, got empty string")
	}
}

func TestDetectMutation_AddToBacklogAmbiguousWorkspaceDeclines(t *testing.T) {
	store := workspace.NewInMemoryStore()
	// Same length (11 chars each) so neither wins the longest-match tiebreak.
	makeTestWorkspace(t, store, "ws-1", "Alpha Squad", nil)
	makeTestWorkspace(t, store, "ws-2", "Bravo Squad", nil)
	h := newTaskMutationHandler(t, store)

	conf, decline := h.detectBacklogCaptureRequest("add ship the launch email to the backlog in Alpha Squad or Bravo Squad")
	if conf != nil {
		t.Fatalf("expected no confirmation for an ambiguous workspace match, got %+v", conf)
	}
	if decline == "" {
		t.Fatal("expected a user-readable ambiguity decline message, got empty string")
	}
}

func TestDetectMutation_AddToBacklogExcludesTrashedAndGroupWorkspaces(t *testing.T) {
	store := workspace.NewInMemoryStore()

	trashed := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Archive Project"})
	trashed.Status = workspace.StatusTrashed
	if err := store.Save(trashed); err != nil {
		t.Fatalf("save trashed workspace: %v", err)
	}

	group := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Engineering Group"})
	group.Kind = "group"
	if err := store.Save(group); err != nil {
		t.Fatalf("save group workspace: %v", err)
	}

	h := newTaskMutationHandler(t, store)

	if conf, _ := h.detectBacklogCaptureRequest("add clean up the archive to the backlog in Archive Project"); conf != nil {
		t.Fatalf("expected a trashed workspace to be unroutable, got %+v", conf)
	}
	if conf, _ := h.detectBacklogCaptureRequest("add plan the offsite to the backlog in Engineering Group"); conf != nil {
		t.Fatalf("expected a group workspace to be unroutable, got %+v", conf)
	}
}

func TestAsk_ConfirmAndExecuteStartTask(t *testing.T) {
	store := workspace.NewInMemoryStore()
	makeTestWorkspace(t, store, "ws-ops", "Operations", []workspace.Task{
		{Description: "deploy to production", Status: workspace.TaskStatusPending, CreatedAt: time.Now()},
	})
	factory := llm.NewFactory()
	factory.Register("fake", &fakeProvider{content: "irrelevant"})
	h := NewHomeAssistantAskHandler(
		HomeSnapshotSources{Workspaces: store, Now: time.Now},
		factory,
		stubSystemModel{provider: "fake", model: "fake-model"},
	)
	mut := &recordingMutator{}
	h.SetMutator(mut)
	deployID := taskIDByDescription(t, store, "ws-ops", "deploy to production")

	// 1. The natural-language request should require confirmation.
	resp := h.Ask(context.Background(), HomeAssistantAskRequest{
		Prompt: "start the deploy task in Operations",
		Intent: "app_introspection",
	})
	if !resp.RequiresConfirmation || resp.Confirmation == nil {
		t.Fatalf("expected a start_task confirmation, got %+v", resp)
	}
	if resp.Confirmation.ActionType != HomeActionStartTask {
		t.Fatalf("confirmation type = %q, want %q", resp.Confirmation.ActionType, HomeActionStartTask)
	}

	// 2. Confirming executes the mutation against the resolved task.
	resp2 := h.Ask(context.Background(), HomeAssistantAskRequest{
		Intent: "app_introspection",
		ConfirmedAction: &HomeAction{
			Type:      HomeActionStartTask,
			Arguments: resp.Confirmation.Arguments,
		},
	})
	if mut.startedWS != "ws-ops" || mut.startedTask != deployID {
		t.Errorf("StartTask called with (%q, %q), want (ws-ops, %q)", mut.startedWS, mut.startedTask, deployID)
	}
	foundOpen := false
	for _, a := range resp2.Actions {
		if a.Type == HomeActionOpenWorkspace && a.WorkspaceID == "ws-ops" {
			foundOpen = true
		}
	}
	if !foundOpen {
		t.Errorf("expected an open_workspace action after start, got %+v", resp2.Actions)
	}
}

// recordingMutator captures the arguments passed to each mutator method.
type recordingMutator struct {
	startedWS         string
	startedTask       string
	createdTaskWS     string
	createdTaskDsc    string
	createdBacklogWS  string
	createdBacklogDsc string
	assignedWS        string
	assignedAgent     string
	createdAgent      string
	createdAgentDsc   string
	removedWS         string
	removedAgent      string
}

func (m *recordingMutator) CreateWorkspace(_ context.Context, name, _ string) (string, string, error) {
	return "ws-new", "/workspaces/ws-new", nil
}

func (m *recordingMutator) CreateTask(_ context.Context, wsID, description string) (string, string, error) {
	m.createdTaskWS = wsID
	m.createdTaskDsc = description
	return "t-new", "/workspaces/" + wsID, nil
}

func (m *recordingMutator) CreateBacklogItem(_ context.Context, wsID, description string) (string, string, error) {
	m.createdBacklogWS = wsID
	m.createdBacklogDsc = description
	return "b-new", "/workspaces/" + wsID, nil
}

func (m *recordingMutator) StartTask(_ context.Context, wsID, taskID string) (string, error) {
	m.startedWS = wsID
	m.startedTask = taskID
	return "/workspaces/" + wsID, nil
}

func (m *recordingMutator) AssignAgent(_ context.Context, wsID, agentName string) (string, error) {
	m.assignedWS = wsID
	m.assignedAgent = agentName
	return "/workspaces/" + wsID, nil
}

func (m *recordingMutator) CreateAgent(_ context.Context, name, description string) (string, error) {
	m.createdAgent = name
	m.createdAgentDsc = description
	return "/agents", nil
}

func (m *recordingMutator) RemoveAgent(_ context.Context, wsID, agentName string) (string, error) {
	m.removedWS = wsID
	m.removedAgent = agentName
	return "/workspaces/" + wsID, nil
}
