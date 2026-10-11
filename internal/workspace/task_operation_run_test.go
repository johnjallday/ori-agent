package workspace

import (
	"encoding/json"
	"testing"
	"time"
)

// The mark says exactly which execution an assistant operation's own start
// produced. It is bound by the recorder, never moved by a later run, and
// cleared when the process that was running the start is gone.
func TestTaskOperationRun_IsBoundByTheRecorderAndNeverMoves(t *testing.T) {
	at := time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC)

	// A task no operation launched is recorded exactly as before.
	plain := &Task{ID: "plain"}
	RecordTaskExecution(plain, "success", "done", at, time.Second)
	if plain.AssistantOperationRun != nil || len(plain.ExecutionHistory) != 1 {
		t.Fatalf("an ordinary execution must carry no mark: %+v", plain.AssistantOperationRun)
	}

	task := &Task{ID: "task-1", CurrentRunID: "attempt-1"}
	MarkOperationRunLaunched(task)
	if run := task.AssistantOperationRun; run == nil || !run.Pending || run.ExecutedAt != nil {
		t.Fatalf("launched: %+v", run)
	}
	// A copy of the task made before the record shares nothing mutable with it.
	before := *task
	task.CurrentRunID = "attempt-3" // the execution retried itself
	RecordTaskExecution(task, "success", "done", at, time.Second)
	run := task.AssistantOperationRun
	if run == nil || run.Pending || run.ExecutedAt == nil || !run.ExecutedAt.Equal(task.ExecutionHistory[0].ExecutedAt) {
		t.Fatalf("the record must be bound in the same write: %+v", run)
	}
	if !before.AssistantOperationRun.Pending {
		t.Fatal("recording changed an earlier copy of the task")
	}

	// A later run from the task's own controls is not the operation's.
	RecordTaskExecution(task, "success", "again", at.Add(time.Hour), time.Second)
	if !task.AssistantOperationRun.ExecutedAt.Equal(at) || len(task.ExecutionHistory) != 2 {
		t.Fatalf("a rerun moved the mark: %+v", task.AssistantOperationRun)
	}

	// The mark survives the task's own storage format, to the nanosecond.
	precise := at.Add(123456789 * time.Nanosecond)
	stored := &Task{ID: "task-2"}
	MarkOperationRunLaunched(stored)
	RecordTaskExecution(stored, "blocked", "needs input", precise, 0)
	encoded, err := json.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Task
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.AssistantOperationRun == nil || !decoded.AssistantOperationRun.ExecutedAt.Equal(decoded.ExecutionHistory[0].ExecutedAt) {
		t.Fatalf("the stored mark no longer identifies its record: %+v", decoded.AssistantOperationRun)
	}
}

// What a restart does: a task left in progress goes back to waiting, and a
// start that was running it is marked as never going to record. A later run
// is then not recorded as the operation's.
func TestReconcileTasksAtBoot_AbandonsAnOperationsInterruptedStart(t *testing.T) {
	store := newExecutorTestStore(t)
	now := time.Now()
	running := Task{ID: "running", To: "agent-a", Status: TaskStatusInProgress, StartedAt: &now, CurrentRunID: "run-1"}
	MarkOperationRunLaunched(&running)
	finishedAt := now.Add(-time.Hour)
	finished := Task{ID: "finished", To: "agent-a", Status: TaskStatusCompleted,
		AssistantOperationRun: &TaskOperationRun{ExecutedAt: &finishedAt}}
	ws := newWorkspaceWithTasks(t, []Task{running, finished, {ID: "plain", To: "agent-a", Status: TaskStatusInProgress, StartedAt: &now}})
	if err := store.Save(ws); err != nil {
		t.Fatalf("seed: %v", err)
	}
	te := NewTaskExecutor(store, &fakeTaskHandler{}, ExecutorConfig{PollInterval: time.Hour, MaxConcurrent: 5})
	te.reconcileTasksAtBoot()

	got, err := store.Get(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]Task{}
	for _, task := range got.Tasks {
		byID[task.ID] = task
	}
	interrupted := byID["running"]
	if interrupted.Status != TaskStatusPending || interrupted.AssistantOperationRun == nil ||
		interrupted.AssistantOperationRun.Pending || interrupted.AssistantOperationRun.ExecutedAt != nil {
		t.Fatalf("an interrupted start must be marked as never recorded: %s %+v", interrupted.Status, interrupted.AssistantOperationRun)
	}
	if kept := byID["finished"].AssistantOperationRun; kept == nil || kept.ExecutedAt == nil || !kept.ExecutedAt.Equal(finishedAt) {
		t.Fatalf("a recorded start must be left alone: %+v", kept)
	}
	if byID["plain"].AssistantOperationRun != nil || byID["plain"].Status != TaskStatusPending {
		t.Fatalf("an ordinary orphan is reset as before: %+v", byID["plain"])
	}

	// The user runs it again from the task's own controls.
	rerun := interrupted
	RecordTaskExecution(&rerun, "success", "done", now.Add(time.Minute), time.Second)
	if rerun.AssistantOperationRun.ExecutedAt != nil || len(rerun.ExecutionHistory) != 1 {
		t.Fatalf("a run after an interruption was recorded as the operation's: %+v", rerun.AssistantOperationRun)
	}
}
