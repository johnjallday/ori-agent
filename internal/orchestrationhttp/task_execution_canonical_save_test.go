package orchestrationhttp

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// A task run holds the workspace it loaded when it started, and other writers
// keep changing that workspace while the run waits on the model: a library
// scan, a folder grant, a settings save. These tests have such a write land
// mid-run and check that the run records its outcome without undoing it.

const changedMidRun = "changed by another writer while the task ran"

// writesMidRun is the run's model. On its first call another writer changes
// the workspace on disk, then the inner executor answers.
type writesMidRun struct {
	inner   workspace.TaskHandler
	store   workspace.Store
	id      string
	written bool
	err     error
}

func (w *writesMidRun) ExecuteTask(ctx context.Context, agentName string, task workspace.Task) (string, error) {
	if !w.written {
		w.written = true
		w.err = w.store.Update(w.id, func(fresh *workspace.Workspace) error {
			fresh.Description = changedMidRun
			return nil
		})
	}
	return w.inner.ExecuteTask(ctx, agentName, task)
}

// newRunWorkspace saves a workspace holding one task to a real FileStore. A
// Home is fenced: the store refuses a write based on an older version, as it
// does for an Assistant Home and every song in it.
func newRunWorkspace(t *testing.T, home bool, task workspace.Task) (workspace.Store, *workspace.Workspace) {
	t.Helper()
	store, err := workspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Songs", Agents: []string{"Ori"}})
	if home {
		ws.OwnerUserID = "owner"
		ws.SetAssistantProgramState(&workspace.AssistantProgramState{
			SchemaVersion: workspace.AssistantProgramStateSchemaVersion,
			Key:           workspace.AssistantProgramKey{OwnerUserID: "owner", ProgramID: "music"},
		})
	}
	if err := ws.AddTask(task); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}
	if home && !workspace.WorkspaceFenceProtected(ws) {
		t.Fatal("test workspace is not fenced")
	}
	held, err := store.Get(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	return store, held
}

// runToEnd runs the task the way an async start does: from the snapshot loaded
// when the run began.
func runToEnd(t *testing.T, store workspace.Store, held *workspace.Workspace, taskID string, model workspace.TaskHandler) error {
	t.Helper()
	mid := &writesMidRun{inner: model, store: store, id: held.ID}
	handler := &TaskHandler{workspaceStore: store, taskHandler: mid}
	task, err := held.GetTask(taskID)
	if err != nil {
		t.Fatal(err)
	}
	_, runErr := handler.executeTaskWithDependencies(held, task)
	if mid.err != nil {
		t.Fatalf("the other writer's change was refused: %v", mid.err)
	}
	if !mid.written {
		t.Fatal("the model was never called, so nothing changed mid-run")
	}
	return runErr
}

// onDisk is the workspace and task as the store now holds them.
func onDisk(t *testing.T, store workspace.Store, wsID, taskID string) (*workspace.Workspace, *workspace.Task) {
	t.Helper()
	ws, err := store.Get(wsID)
	if err != nil {
		t.Fatal(err)
	}
	task, err := ws.GetTask(taskID)
	if err != nil {
		t.Fatal(err)
	}
	return ws, task
}

func TestTaskRun_OnAHomeRecordsSuccessAfterAMidRunWrite(t *testing.T) {
	task := workspace.Task{ID: "task-summary", To: "Ori", Description: "summarize workspace"}
	store, held := newRunWorkspace(t, true, task)

	if err := runToEnd(t, store, held, task.ID, &stubWorkspaceTaskExecutor{result: "Workspace summary result"}); err != nil {
		t.Fatalf("run failed after another writer changed the Home: %v", err)
	}

	ws, got := onDisk(t, store, held.ID, task.ID)
	if got.Status != workspace.TaskStatusCompleted || got.Result != "Workspace summary result" {
		t.Fatalf("task on disk = status %q result %q, want the completed run", got.Status, got.Result)
	}
	if len(got.ExecutionHistory) != 1 || got.ExecutionHistory[0].Status != "success" {
		t.Fatalf("run history on disk = %+v, want one success", got.ExecutionHistory)
	}
	if ws.Description != changedMidRun {
		t.Fatalf("the other writer's change was lost: description %q", ws.Description)
	}
}

// The reported symptom: a run that does not succeed must still record how it
// ended. Before, its end was written from the same stale snapshot, the Home
// refused it, and the task showed In progress forever.
func TestTaskRun_OnAHomeRecordsAFailedModelCallAfterAMidRunWrite(t *testing.T) {
	task := workspace.Task{ID: "task-fails", To: "Ori", Description: "summarize workspace"}
	store, held := newRunWorkspace(t, true, task)

	err := runToEnd(t, store, held, task.ID, &stubWorkspaceTaskExecutor{err: errors.New("model provider unavailable")})
	if err == nil || errors.Is(err, workspace.ErrStaleWorkspaceVersion) {
		t.Fatalf("run error = %v, want the model's failure, not a refused save", err)
	}

	// An error Ori cannot classify asks the user what to do next.
	ws, got := onDisk(t, store, held.ID, task.ID)
	if got.Status != workspace.TaskStatusWaitingForChoice {
		t.Fatalf("task on disk is %q, want waiting for the user's choice", got.Status)
	}
	if len(got.ExecutionHistory) != 1 || got.ExecutionHistory[0].Status != "blocked" {
		t.Fatalf("run history on disk = %+v, want one blocked run", got.ExecutionHistory)
	}
	if ws.Description != changedMidRun {
		t.Fatalf("the other writer's change was lost: description %q", ws.Description)
	}
}

func TestTaskRun_OnAHomeRecordsACancelAfterAMidRunWrite(t *testing.T) {
	task := workspace.Task{ID: "task-cancel", To: "Ori", Description: "run a long step"}
	store, held := newRunWorkspace(t, true, task)
	model := &stubCancellableTaskExecutor{started: make(chan struct{}, 1)}
	mid := &writesMidRun{inner: model, store: store, id: held.ID}
	handler := &TaskHandler{workspaceStore: store, taskHandler: mid}
	running, err := held.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}

	errCh := make(chan error, 1)
	go func() {
		_, err := handler.executeTaskWithDependencies(held, running)
		errCh <- err
	}()
	select {
	case <-model.started:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for the run to reach the model")
	}
	if !handler.cancelRunningTask(task.ID) {
		t.Fatal("the run was not registered as cancellable")
	}
	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("run error = %v, want context.Canceled", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for the cancelled run to end")
	}
	if mid.err != nil || !mid.written {
		t.Fatalf("no mid-run write: written %v err %v", mid.written, mid.err)
	}

	ws, got := onDisk(t, store, held.ID, task.ID)
	if got.Status != workspace.TaskStatusCancelled || got.Error != "Cancelled by user" {
		t.Fatalf("task on disk = status %q error %q, want cancelled by the user", got.Status, got.Error)
	}
	if ws.Description != changedMidRun {
		t.Fatalf("the other writer's change was lost: description %q", ws.Description)
	}
}

// A song folder's first task runs in steps, and the run saves its progress
// after each one. The first of those saves after a mid-run write is where the
// reported run failed.
func TestTaskRun_OnAHomeSavesEachStepAfterAMidRunWrite(t *testing.T) {
	task := workspace.Task{
		ID:            "task-steps",
		To:            "Ori",
		Description:   "Gather DNM related files into DNM folder",
		ExecutionMode: workspace.TaskExecutionModeAuto,
		Context:       map[string]any{},
	}
	store, held := newRunWorkspace(t, true, task)
	model := &stubSequenceTaskExecutor{results: []string{
		"Allowed directories: /Users/jjdev/Documents",
		"Inspected candidate directories.",
		"Identified matching DNM files.",
		"Created DNM folder.",
		"Moved matching files into DNM.",
		"Verified final folder contents.",
		"Moved 3 files into /Users/jjdev/Documents/DNM",
	}}

	if err := runToEnd(t, store, held, task.ID, model); err != nil {
		t.Fatalf("stepped run failed after another writer changed the Home: %v", err)
	}

	ws, got := onDisk(t, store, held.ID, task.ID)
	if got.Status != workspace.TaskStatusCompleted || len(got.ExecutionSteps) != 7 {
		t.Fatalf("task on disk = status %q with %d steps, want completed with 7", got.Status, len(got.ExecutionSteps))
	}
	for _, step := range got.ExecutionSteps {
		if step.Status != workspace.TaskExecutionStepCompleted {
			t.Fatalf("step %d on disk is %q, want completed", step.Index, step.Status)
		}
	}
	if ws.Description != changedMidRun {
		t.Fatalf("the other writer's change was lost: description %q", ws.Description)
	}
}

// An ordinary workspace never refused the stale write; it took it, and the
// other writer's change silently vanished.
func TestTaskRun_OnAnOrdinaryWorkspaceKeepsAMidRunWrite(t *testing.T) {
	task := workspace.Task{ID: "task-plain", To: "Ori", Description: "summarize workspace"}
	store, held := newRunWorkspace(t, false, task)

	if err := runToEnd(t, store, held, task.ID, &stubWorkspaceTaskExecutor{result: "Workspace summary result"}); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	ws, got := onDisk(t, store, held.ID, task.ID)
	if got.Status != workspace.TaskStatusCompleted {
		t.Fatalf("task on disk is %q, want completed", got.Status)
	}
	if ws.Description != changedMidRun {
		t.Fatalf("the run's final save erased the other writer's change: description %q", ws.Description)
	}
}

// A parent task saves its own start and end, and its subtasks' resets, around
// the subtasks' runs.
func TestTaskRun_ParentSequenceOnAHomeRecordsItsEndAfterAMidRunWrite(t *testing.T) {
	parent := workspace.Task{ID: "parent", To: "Ori", Description: "Prepare the session"}
	store, held := newRunWorkspace(t, true, parent)
	for i, id := range []string{"sub-1", "sub-2"} {
		sub := workspace.Task{
			ID:           id,
			To:           "Ori",
			Description:  "summarize workspace",
			ParentTaskID: parent.ID,
			SubtaskIndex: i + 1,
			Status:       workspace.TaskStatusCompleted,
			Result:       "an earlier run's result",
		}
		if err := held.AddTask(sub); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Save(held); err != nil {
		t.Fatal(err)
	}

	mid := &writesMidRun{inner: &stubWorkspaceTaskExecutor{result: "Workspace summary result"}, store: store, id: held.ID}
	handler := &TaskHandler{workspaceStore: store, taskHandler: mid}
	handler.executeParentTaskSequence(held.ID, parent.ID)
	if mid.err != nil || !mid.written {
		t.Fatalf("no mid-run write: written %v err %v", mid.written, mid.err)
	}

	ws, got := onDisk(t, store, held.ID, parent.ID)
	if got.Status != workspace.TaskStatusCompleted {
		t.Fatalf("parent on disk is %q (error %q), want completed", got.Status, got.Error)
	}
	for _, id := range []string{"sub-1", "sub-2"} {
		if _, sub := onDisk(t, store, held.ID, id); sub.Status != workspace.TaskStatusCompleted || sub.Result != "Workspace summary result" {
			t.Fatalf("subtask %s on disk = status %q result %q, want this run's result", id, sub.Status, sub.Result)
		}
	}
	if ws.Description != changedMidRun {
		t.Fatalf("the other writer's change was lost: description %q", ws.Description)
	}
}
