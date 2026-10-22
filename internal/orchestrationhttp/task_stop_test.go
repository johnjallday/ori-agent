package orchestrationhttp

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Stop cancels the run and, once the run has recorded its end, puts the same
// task back to Ready with nothing of the cancelled run left on it, so it can be
// started again.
func TestStopTaskToReady_StopsTheRunAndReopensTheTask(t *testing.T) {
	store := workspace.NewInMemoryStore()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "amr", Agents: []string{"Ori"}})
	task := workspace.Task{ID: "task-stop", To: "Ori", Description: "read the project"}
	if err := ws.AddTask(task); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}
	persisted, err := ws.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	executor := &stubCancellableTaskExecutor{started: make(chan struct{}, 1)}
	handler := &TaskHandler{workspaceStore: store, taskHandler: executor}
	done := make(chan error, 1)
	go func() {
		_, err := handler.executeTaskWithDependencies(ws, persisted)
		done <- err
	}()
	select {
	case <-executor.started:
	case <-time.After(3 * time.Second):
		t.Fatal("the run did not start")
	}

	if err := handler.StopTaskToReady(context.Background(), ws.ID, task.ID); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("run ended with %v", err)
	}
	saved, err := store.Get(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	stopped, err := saved.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stopped.CanonicalState() != workspace.TicketStateReady || stopped.Status == workspace.TaskStatusCancelled ||
		stopped.Error != "" || stopped.Result != "" || stopped.CompletedAt != nil {
		t.Fatalf("stopped task = state %s status %s error %q completed %v", stopped.CanonicalState(), stopped.Status, stopped.Error, stopped.CompletedAt)
	}
	if last := stopped.StateHistory[len(stopped.StateHistory)-1]; last.To != workspace.TicketStateReady || last.Actor != workspace.TicketActorUser {
		t.Fatalf("history = %+v", last)
	}
	// Stopping it again changes nothing.
	if err := handler.StopTaskToReady(context.Background(), ws.ID, task.ID); err != nil {
		t.Fatalf("a second stop: %v", err)
	}
}

func TestStopTaskToReady_RefusesATaskThatIsNotRunning(t *testing.T) {
	store := workspace.NewInMemoryStore()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "amr"})
	task := workspace.Task{ID: "task-done", To: "Ori", Description: "done", Status: workspace.TaskStatusCompleted, Result: "ok"}
	if err := ws.AddTask(task); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}
	handler := &TaskHandler{workspaceStore: store}
	if err := handler.StopTaskToReady(context.Background(), ws.ID, task.ID); !errors.Is(err, ErrTaskNotStoppable) {
		t.Fatalf("err = %v", err)
	}
	if err := handler.StopTaskToReady(context.Background(), ws.ID, "missing"); !errors.Is(err, ErrTaskNotStoppable) {
		t.Fatalf("missing: err = %v", err)
	}
}
