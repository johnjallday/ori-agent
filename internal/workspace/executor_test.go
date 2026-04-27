package workspace

import (
	"context"
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

	block <-chan struct{} // optional: hold ExecuteTask until closed
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
