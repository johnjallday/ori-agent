package orchestrationhttp

import (
	"context"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Explicit retry after a repair.
//
// The contract has two halves, and both matter. A retry must actually START
// something — clearing the interaction that was waiting on the user and running
// a fresh attempt — while PRESERVING what already happened. Wiping the history
// on retry would hide the failure the user just repaired, which is the evidence
// they need to judge whether the repair worked.

// scriptedTaskHandler returns a fixed result and records what it was asked to
// run.
type scriptedTaskHandler struct {
	result string
	err    error
	calls  int
}

func (s *scriptedTaskHandler) ExecuteTask(_ context.Context, _ string, _ workspace.Task) (string, error) {
	s.calls++
	if s.err != nil {
		return "", s.err
	}
	return s.result, nil
}

// blockedTaskFixture builds a workspace holding one task that is already
// blocked, with its prior failure recorded — the state a repaired task is in.
func blockedTaskFixture(t *testing.T) (*TaskHandler, workspace.Store, *scriptedTaskHandler) {
	t.Helper()
	ws := &workspace.Workspace{
		ID: "ws-1", Name: "Email Ops",
		AgentInstances: []workspace.AgentInstance{{ID: "inbox-id", Name: "Inbox"}},
	}
	task := workspace.Task{
		ID: "task-1", WorkspaceID: "ws-1", To: "Inbox",
		Description: "Triage today's inbox",
		Status:      workspace.TaskStatusPending,
	}
	if err := ws.AddTasks([]workspace.Task{task}); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	// Put it in the blocked state the same way execution does.
	stored, err := ws.GetTask("task-1")
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	// A task only reaches waiting_for_choice by running first — the status
	// machine enforces that, and so does reality.
	if err := stored.SetStatus(workspace.TaskStatusInProgress); err != nil {
		t.Fatalf("start task: %v", err)
	}
	if err := stored.SetStatus(workspace.TaskStatusWaitingForChoice); err != nil {
		t.Fatalf("block task: %v", err)
	}
	stored.Context = map[string]any{
		"human_loop": map[string]any{
			"state":       "waiting_for_choice",
			"reason_code": "connection_required",
			"reason":      "Connect your Google account before this workspace can read email.",
		},
	}
	workspace.RecordTaskExecution(stored, "blocked", "connection required", stored.CreatedAt, 0)
	if err := ws.UpdateTask(*stored); err != nil {
		t.Fatalf("update task: %v", err)
	}

	store := workspace.NewInMemoryStore()
	if err := store.Save(ws); err != nil {
		t.Fatalf("save workspace: %v", err)
	}
	handler := &scriptedTaskHandler{result: "Triaged 4 threads; 2 need a reply."}
	return NewTaskHandler(store, nil, handler, workspace.NewEventBus(16, 64)), store, handler
}

// FR 39, 57, 89: the retry clears the waiting interaction, actually runs, and
// leaves the prior failure in history.
func TestExplicitRetry_StartsAFreshAttemptAndKeepsHistory(t *testing.T) {
	th, store, scripted := blockedTaskFixture(t)

	ws, err := store.Get("ws-1")
	if err != nil {
		t.Fatalf("get workspace: %v", err)
	}
	before, _ := ws.GetTask("task-1")
	historyBefore := len(before.ExecutionHistory)
	if historyBefore == 0 {
		t.Fatal("the fixture must start with a recorded failure")
	}
	if _, blocked := before.Context["human_loop"]; !blocked {
		t.Fatal("the fixture must start blocked")
	}

	result, err := th.executeTaskWithDependencies(ws, before)
	if err != nil {
		t.Fatalf("explicit retry: %v", err)
	}
	if scripted.calls != 1 {
		t.Fatalf("the agent ran %d times, want exactly one fresh attempt", scripted.calls)
	}
	if result == "" {
		t.Fatal("the retry produced no result")
	}

	saved, err := store.Get("ws-1")
	if err != nil {
		t.Fatalf("reload workspace: %v", err)
	}
	after, err := saved.GetTask("task-1")
	if err != nil {
		t.Fatalf("get task: %v", err)
	}

	// The interaction that was waiting on the user is gone.
	if _, stillBlocked := after.Context["human_loop"]; stillBlocked {
		t.Fatalf("the waiting_for_choice interaction survived the retry: %+v", after.Context["human_loop"])
	}
	if after.Status == workspace.TaskStatusWaitingForChoice {
		t.Fatalf("status = %q, want the task moved on", after.Status)
	}
	// The prior failure is still visible: the retry added to history, it did not
	// replace it.
	if len(after.ExecutionHistory) <= historyBefore {
		t.Fatalf("execution history = %d entries, want more than the %d it started with",
			len(after.ExecutionHistory), historyBefore)
	}
	foundPriorFailure := false
	for _, entry := range after.ExecutionHistory {
		if entry.Status == "blocked" {
			foundPriorFailure = true
		}
	}
	if !foundPriorFailure {
		t.Fatal("the failure the user repaired was erased from history")
	}
}

// A task whose connection precondition is STILL unmet must not run, even when
// the user explicitly asks — the retry would reproduce the same block and cost
// another attempt.
func TestExplicitRetry_StillGatedWhenThePreconditionIsUnmet(t *testing.T) {
	th, store, scripted := blockedTaskFixture(t)
	th.SetCapabilityGate(stubGate{blocked: &workspace.TaskBlockedError{
		ReasonCode: workspace.BlockedReasonConnectionRequired,
		Reason:     "Connect your Google account first.",
		Repair:     &workspace.TaskRepairAction{Code: "connect_google", Label: "Connect Google"},
	}})

	ws, _ := store.Get("ws-1")
	task, _ := ws.GetTask("task-1")
	task.RequiredCapabilities = []string{workspace.CapabilityEmail}
	if err := ws.UpdateTask(*task); err != nil {
		t.Fatalf("update: %v", err)
	}

	if _, err := th.executeTaskWithDependencies(ws, task); err == nil {
		t.Fatal("a still-unmet precondition must block the retry")
	}
	if scripted.calls != 0 {
		t.Fatalf("the agent ran %d times; a gated retry must spend no model call", scripted.calls)
	}

	saved, _ := store.Get("ws-1")
	after, _ := saved.GetTask("task-1")
	if after.Status != workspace.TaskStatusWaitingForChoice {
		t.Fatalf("status = %q, want it still waiting", after.Status)
	}
	loop, _ := after.Context["human_loop"].(map[string]any)
	if loop["reason_code"] != workspace.BlockedReasonConnectionRequired {
		t.Fatalf("human loop = %+v, want the connection repair", loop)
	}
	if loop["repair"] == nil {
		t.Fatal("the block must carry its structured repair so the UI can offer it")
	}
}

type stubGate struct{ blocked *workspace.TaskBlockedError }

func (s stubGate) CheckTaskCapabilities(string, []string) *workspace.TaskBlockedError {
	return s.blocked
}
