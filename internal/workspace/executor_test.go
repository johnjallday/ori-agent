package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeTaskHandler counts ExecuteTask invocations and (optionally) blocks until
// a release channel is closed so tests can observe concurrent state.
type fakeTaskHandler struct {
	calls   int64
	mu      sync.Mutex
	seenIDs map[string]int

	block     <-chan struct{} // optional: hold ExecuteTask until closed
	returnErr error           // optional: return this error instead of a successful result
}

func (h *fakeTaskHandler) ExecuteTask(ctx context.Context, agentName string, task Task) (string, error) {
	atomic.AddInt64(&h.calls, 1)
	h.mu.Lock()
	if h.seenIDs == nil {
		h.seenIDs = map[string]int{}
	}
	h.seenIDs[task.ID]++
	h.mu.Unlock()

	if h.block != nil {
		select {
		case <-h.block:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	if h.returnErr != nil {
		return "", h.returnErr
	}
	return "ok", nil
}

// newExecutorTestStore returns a FileStore in a temp directory. We use
// FileStore (not InMemoryStore) because Get returns deep clones, which is
// the production-relevant concurrency contract this test exercises.
func newExecutorTestStore(t *testing.T) Store {
	t.Helper()
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	return store
}

func newWorkspaceWithTasks(t *testing.T, tasks []Task) *Workspace {
	t.Helper()
	ws := NewWorkspace(CreateWorkspaceParams{
		Name:   "executor-test",
		Agents: []string{"agent-a"},
	})
	ws.Status = StatusActive
	ws.Tasks = tasks
	ws.taskIndex = make(map[string]int, len(tasks))
	for i, task := range tasks {
		ws.taskIndex[task.ID] = i
	}
	return ws
}

// TestCleanupOrphanedTasks_ResetsInProgress verifies that tasks left in
// "in_progress" from a prior server crash are reset to "pending" on Start.
func TestCleanupOrphanedTasks_ResetsInProgress(t *testing.T) {
	store := newExecutorTestStore(t)
	now := time.Now()
	ws := newWorkspaceWithTasks(t, []Task{
		{ID: "t1", To: "agent-a", Status: TaskStatusInProgress, StartedAt: &now},
		{ID: "t2", To: "agent-a", Status: TaskStatusCompleted},
		{ID: "t3", To: "agent-a", Status: TaskStatusInProgress, StartedAt: &now},
		{ID: "t4", To: "agent-a", Status: TaskStatusPending},
	})
	if err := store.Save(ws); err != nil {
		t.Fatalf("seed: %v", err)
	}

	te := NewTaskExecutor(store, &fakeTaskHandler{}, ExecutorConfig{
		PollInterval:  time.Hour, // we drive cleanup directly, no polling needed
		MaxConcurrent: 5,
	})
	te.cleanupOrphanedTasks()

	got, err := store.Get(ws.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	wantStatuses := map[string]TaskStatus{
		"t1": TaskStatusPending,
		"t2": TaskStatusCompleted,
		"t3": TaskStatusPending,
		"t4": TaskStatusPending,
	}
	for _, task := range got.Tasks {
		if task.Status != wantStatuses[task.ID] {
			t.Errorf("task %s status = %q, want %q", task.ID, task.Status, wantStatuses[task.ID])
		}
		if task.Status == TaskStatusPending && task.StartedAt != nil {
			t.Errorf("task %s should have nil StartedAt after reset", task.ID)
		}
	}
}

func TestCleanupOrphanedTasks_SkipsTrashedWorkspaceWithoutRecreatingFolder(t *testing.T) {
	primary := NewInMemoryStore()
	dir := t.TempDir()
	fileSync, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer func() { _ = fileSync.Close() }()

	store := NewSyncStore(primary, fileSync)
	now := time.Now()
	ws := newWorkspaceWithTasks(t, []Task{
		{ID: "orphan", To: "agent-a", Status: TaskStatusInProgress, StartedAt: &now},
	})
	ws.ID = "ws-trashed-cleanup"
	ws.Name = "Trashed Cleanup"
	ws.Status = StatusTrashed

	if err := primary.Save(ws); err != nil {
		t.Fatalf("seed primary: %v", err)
	}

	te := NewTaskExecutor(store, &fakeTaskHandler{}, ExecutorConfig{
		PollInterval:  time.Hour,
		MaxConcurrent: 5,
	})
	te.cleanupOrphanedTasks()

	got, err := primary.Get(ws.ID)
	if err != nil {
		t.Fatalf("get primary: %v", err)
	}
	if got.Tasks[0].Status != TaskStatusInProgress {
		t.Fatalf("trashed workspace task status = %q, want %q", got.Tasks[0].Status, TaskStatusInProgress)
	}
	if _, err := os.Stat(filepath.Join(dir, "trashed-cleanup")); !os.IsNotExist(err) {
		t.Fatalf("trashed workspace folder should not be recreated, stat err = %v", err)
	}
}

// TestCleanupOrphanedTasks_AtomicAgainstConcurrentMutation pins down the
// cross-instance race fix: while cleanup runs on one task, an unrelated
// concurrent Store.Update on the same workspace mutating a DIFFERENT task
// must not be lost. With the previous Get/mutate/Save (no per-workspace
// lock) the cleanup's late Save could clobber the concurrent mutation;
// now both mutations are serialized by Store.Update and both must land.
func TestCleanupOrphanedTasks_AtomicAgainstConcurrentMutation(t *testing.T) {
	store := newExecutorTestStore(t)
	now := time.Now()
	ws := newWorkspaceWithTasks(t, []Task{
		{ID: "orphan", To: "agent-a", Status: TaskStatusInProgress, StartedAt: &now},
		// "victim" starts in Pending so cleanup ignores it; the concurrent
		// mutation flips its description.
		{ID: "victim", To: "agent-a", Status: TaskStatusPending, Description: "before"},
	})
	if err := store.Save(ws); err != nil {
		t.Fatalf("seed: %v", err)
	}

	te := NewTaskExecutor(store, &fakeTaskHandler{}, ExecutorConfig{
		PollInterval:  time.Hour,
		MaxConcurrent: 5,
	})

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		te.cleanupOrphanedTasks()
	}()
	go func() {
		defer wg.Done()
		// Repeatedly try to mutate so we maximize the chance of overlapping
		// the cleanup's critical section. With Store.Update both calls
		// serialize against each other; without it, the cleanup's blanket
		// Save would clobber this whole-workspace write.
		for i := 0; i < 10; i++ {
			if err := store.Update(ws.ID, func(fresh *Workspace) error {
				return fresh.MutateTask("victim", func(t *Task) error {
					t.Description = "after"
					return nil
				})
			}); err != nil {
				t.Errorf("concurrent update: %v", err)
				return
			}
		}
	}()
	wg.Wait()

	got, err := store.Get(ws.ID)
	if err != nil {
		t.Fatalf("final get: %v", err)
	}
	for _, task := range got.Tasks {
		switch task.ID {
		case "orphan":
			if task.Status != TaskStatusPending {
				t.Errorf("orphan: expected Pending, got %q", task.Status)
			}
		case "victim":
			if task.Description != "after" {
				t.Errorf("victim description clobbered by cleanup save: %q", task.Description)
			}
		}
	}
}

// TestCheckAndExecuteTasks_SingleClaimPerTask exercises the claim path under
// concurrent invocations of checkAndExecuteTasks. With the existing
// runningTasks-map check inside te.mu.Lock, the same task ID must never be
// dispatched to the handler twice.
func TestCheckAndExecuteTasks_SingleClaimPerTask(t *testing.T) {
	store := newExecutorTestStore(t)
	tasks := make([]Task, 0, 8)
	for i := 0; i < 8; i++ {
		tasks = append(tasks, Task{
			ID:     "task-" + string(rune('a'+i)),
			To:     "agent-a",
			Status: TaskStatusAssigned,
		})
	}
	ws := newWorkspaceWithTasks(t, tasks)
	if err := store.Save(ws); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Block handler so tasks remain in runningTasks throughout the race.
	release := make(chan struct{})
	handler := &fakeTaskHandler{block: release}

	te := NewTaskExecutor(store, handler, ExecutorConfig{
		PollInterval:  time.Hour,
		MaxConcurrent: len(tasks),
	})
	// Drain in-flight goroutines before t.TempDir cleanup runs. We close
	// `release` first so blocked handlers exit, then Stop waits.
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
		te.Stop()
	})

	const concurrency = 8
	var wg sync.WaitGroup
	wg.Add(concurrency)
	for i := 0; i < concurrency; i++ {
		go func() {
			defer wg.Done()
			te.checkAndExecuteTasks()
		}()
	}
	wg.Wait()

	// Wait briefly for goroutines spawned in executeTask to actually invoke
	// the handler. We expect exactly len(tasks) calls, never duplicates.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && atomic.LoadInt64(&handler.calls) < int64(len(tasks)) {
		time.Sleep(5 * time.Millisecond)
	}

	if got := atomic.LoadInt64(&handler.calls); got != int64(len(tasks)) {
		t.Fatalf("handler calls = %d, want %d (no duplicates)", got, len(tasks))
	}
	handler.mu.Lock()
	defer handler.mu.Unlock()
	for id, n := range handler.seenIDs {
		if n != 1 {
			t.Errorf("task %s executed %d times, want 1", id, n)
		}
	}
}

// fakeXPAwarder records AwardTaskXP calls so tests can assert whether task
// completion (vs failure) actually triggered an award.
type fakeXPAwarder struct {
	mu     sync.Mutex
	awards []string // agent names, in call order
	err    error
}

func (a *fakeXPAwarder) AwardTaskXP(agentName string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.awards = append(a.awards, agentName)
	return a.err
}

func (a *fakeXPAwarder) callCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.awards)
}

// waitForTaskStatus polls the store until the task reaches one of the target
// statuses or the deadline passes, returning the final status seen.
func waitForTaskStatus(t *testing.T, store Store, wsID, taskID string, deadline time.Time) TaskStatus {
	t.Helper()
	for time.Now().Before(deadline) {
		fresh, err := store.Get(wsID)
		if err == nil {
			if task, taskErr := fresh.GetTask(taskID); taskErr == nil && task != nil {
				if task.Status == TaskStatusCompleted || task.Status == TaskStatusFailed {
					return task.Status
				}
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("task %s did not reach a terminal status before deadline", taskID)
	return ""
}

// TestExecuteTask_AwardsXPOnCompletion verifies a successfully completed task
// awards XP to the executing agent exactly once (PRD FR15).
func TestExecuteTask_AwardsXPOnCompletion(t *testing.T) {
	store := newExecutorTestStore(t)
	ws := newWorkspaceWithTasks(t, []Task{
		{ID: "t1", To: "agent-a", Status: TaskStatusAssigned},
	})
	if err := store.Save(ws); err != nil {
		t.Fatalf("seed: %v", err)
	}

	handler := &fakeTaskHandler{}
	te := NewTaskExecutor(store, handler, ExecutorConfig{PollInterval: time.Hour, MaxConcurrent: 1})
	t.Cleanup(te.Stop)

	awarder := &fakeXPAwarder{}
	te.SetEvolutionAwarder(awarder)

	te.checkAndExecuteTasks()

	status := waitForTaskStatus(t, store, ws.ID, "t1", time.Now().Add(2*time.Second))
	if status != TaskStatusCompleted {
		t.Fatalf("task status = %q, want completed", status)
	}

	if got := awarder.callCount(); got != 1 {
		t.Fatalf("AwardTaskXP call count = %d, want 1", got)
	}
	if awarder.awards[0] != "agent-a" {
		t.Errorf("AwardTaskXP called for agent %q, want agent-a", awarder.awards[0])
	}
}

// TestExecuteTask_NoXPOnFailure verifies a failed task run awards no XP.
func TestExecuteTask_NoXPOnFailure(t *testing.T) {
	store := newExecutorTestStore(t)
	ws := newWorkspaceWithTasks(t, []Task{
		{ID: "t1", To: "agent-a", Status: TaskStatusAssigned},
	})
	if err := store.Save(ws); err != nil {
		t.Fatalf("seed: %v", err)
	}

	handler := &fakeTaskHandler{returnErr: errors.New("boom")}
	te := NewTaskExecutor(store, handler, ExecutorConfig{PollInterval: time.Hour, MaxConcurrent: 1})
	t.Cleanup(te.Stop)

	awarder := &fakeXPAwarder{}
	te.SetEvolutionAwarder(awarder)

	te.checkAndExecuteTasks()

	status := waitForTaskStatus(t, store, ws.ID, "t1", time.Now().Add(2*time.Second))
	if status != TaskStatusFailed {
		t.Fatalf("task status = %q, want failed", status)
	}

	if got := awarder.callCount(); got != 0 {
		t.Fatalf("AwardTaskXP call count = %d, want 0 for a failed task", got)
	}
}

// TestExecuteTask_NilAwarderIsSafeNoOp verifies that never calling
// SetEvolutionAwarder (feature disabled/unwired) does not panic and task
// completion proceeds normally.
func TestExecuteTask_NilAwarderIsSafeNoOp(t *testing.T) {
	store := newExecutorTestStore(t)
	ws := newWorkspaceWithTasks(t, []Task{
		{ID: "t1", To: "agent-a", Status: TaskStatusAssigned},
	})
	if err := store.Save(ws); err != nil {
		t.Fatalf("seed: %v", err)
	}

	handler := &fakeTaskHandler{}
	te := NewTaskExecutor(store, handler, ExecutorConfig{PollInterval: time.Hour, MaxConcurrent: 1})
	t.Cleanup(te.Stop)
	// Deliberately not calling te.SetEvolutionAwarder.

	te.checkAndExecuteTasks()

	status := waitForTaskStatus(t, store, ws.ID, "t1", time.Now().Add(2*time.Second))
	if status != TaskStatusCompleted {
		t.Fatalf("task status = %q, want completed", status)
	}
}

// TestStop_CancelsRunningTasks ensures Stop cancels the contexts of in-flight
// tasks and waits for goroutines to drain.
func TestStop_CancelsRunningTasks(t *testing.T) {
	store := newExecutorTestStore(t)
	ws := newWorkspaceWithTasks(t, []Task{
		{ID: "t1", To: "agent-a", Status: TaskStatusAssigned},
	})
	if err := store.Save(ws); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Handler blocks until ctx is cancelled.
	never := make(chan struct{})
	handler := &fakeTaskHandler{block: never}

	te := NewTaskExecutor(store, handler, ExecutorConfig{
		PollInterval:  time.Hour,
		MaxConcurrent: 5,
	})
	te.checkAndExecuteTasks()

	// Wait for the handler to actually be invoked.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && atomic.LoadInt64(&handler.calls) < 1 {
		time.Sleep(5 * time.Millisecond)
	}
	if atomic.LoadInt64(&handler.calls) < 1 {
		t.Fatal("handler was never called")
	}

	done := make(chan struct{})
	go func() {
		te.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Stop did not return within 3s")
	}
}
