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
	te.reconcileTasksAtBoot()

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
	te.reconcileTasksAtBoot()

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
		te.reconcileTasksAtBoot()
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

// TestReconcileTasksAtBoot_HoldsPreexistingBacklog verifies that work merely
// queued when the previous process ended is demoted to Pending rather than
// auto-running the instant the server comes back, matching how an interrupted
// in-progress task has always been treated.
func TestReconcileTasksAtBoot_HoldsPreexistingBacklog(t *testing.T) {
	store := newExecutorTestStore(t)
	now := time.Now()
	ws := newWorkspaceWithTasks(t, []Task{
		{ID: "queued", To: "agent-a", Status: TaskStatusAssigned},
		{ID: "orphan", To: "agent-a", Status: TaskStatusInProgress, StartedAt: &now},
		{ID: "done", To: "agent-a", Status: TaskStatusCompleted},
		{ID: "waiting", To: "agent-a", Status: TaskStatusPending},
	})
	if err := store.Save(ws); err != nil {
		t.Fatalf("seed: %v", err)
	}

	te := NewTaskExecutor(store, &fakeTaskHandler{}, ExecutorConfig{
		PollInterval:  time.Hour,
		MaxConcurrent: 5,
	})
	te.reconcileTasksAtBoot()

	got, err := store.Get(ws.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	want := map[string]TaskStatus{
		"queued":  TaskStatusPending, // held: no fresh intent behind it
		"orphan":  TaskStatusPending, // reset: died mid-run
		"done":    TaskStatusCompleted,
		"waiting": TaskStatusPending,
	}
	for _, task := range got.Tasks {
		if task.Status != want[task.ID] {
			t.Errorf("task %s status = %q, want %q", task.ID, task.Status, want[task.ID])
		}
	}
}

// TestReconcileTasksAtBoot_ResumeBacklogOptOut verifies unattended deployments
// can keep the old auto-resume behavior, where a restart picks the queue back
// up with nobody watching.
func TestReconcileTasksAtBoot_ResumeBacklogOptOut(t *testing.T) {
	store := newExecutorTestStore(t)
	ws := newWorkspaceWithTasks(t, []Task{
		{ID: "queued", To: "agent-a", Status: TaskStatusAssigned},
	})
	if err := store.Save(ws); err != nil {
		t.Fatalf("seed: %v", err)
	}

	te := NewTaskExecutor(store, &fakeTaskHandler{}, ExecutorConfig{
		PollInterval:  time.Hour,
		MaxConcurrent: 5,
	})
	te.resumeBacklog = true // ORI_TASK_RESUME_BACKLOG=true
	te.reconcileTasksAtBoot()

	got, err := store.Get(ws.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	task, err := got.GetTask("queued")
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if task.Status != TaskStatusAssigned {
		t.Errorf("status = %q, want %q (backlog should stay auto-runnable)", task.Status, TaskStatusAssigned)
	}
}

// TestCheckAndExecuteTasks_BootRampLimitsFirstCycles verifies a server booting
// next to a full queue admits tasks gradually instead of opening maxConcurrent
// LLM calls in the same instant.
func TestCheckAndExecuteTasks_BootRampLimitsFirstCycles(t *testing.T) {
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

	release := make(chan struct{})
	handler := &fakeTaskHandler{block: release}
	te := NewTaskExecutor(store, handler, ExecutorConfig{
		PollInterval:  time.Hour,
		MaxConcurrent: len(tasks),
	})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
		te.Stop()
	})

	// First cycle admits bootAdmissionRamp[0], not all 8.
	te.checkAndExecuteTasks()
	waitForRunningCount(t, te, bootAdmissionRamp[0])

	// Second cycle tops up to the next ramp step.
	te.checkAndExecuteTasks()
	waitForRunningCount(t, te, bootAdmissionRamp[0]+bootAdmissionRamp[1])
}

// waitForRunningCount waits for the executor to reach exactly want running
// tasks, then confirms it holds there rather than overshooting.
func waitForRunningCount(t *testing.T, te *TaskExecutor, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && te.GetRunningTaskCount() < want {
		time.Sleep(5 * time.Millisecond)
	}
	if got := te.GetRunningTaskCount(); got != want {
		t.Fatalf("running tasks = %d, want %d", got, want)
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
	// This test is about claim exclusivity, not admission pacing: fast-forward
	// past the boot ramp so every candidate is admissible in one cycle.
	te.cycle = len(bootAdmissionRamp)
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

// waitForAwards polls until at least want awards are recorded or the deadline
// passes, then returns a copy of everything recorded so far.
//
// The wait is load-bearing, not defensive padding. executeTask awards XP as a
// post-mutation side effect, *after* the store Update that flips the task to
// completed has already committed. A test that waits on the task status and
// then asserts immediately is racing that goroutine through the window between
// the Update returning and the award landing, and reads zero awards whenever
// the runner deschedules it there — rare locally, reproducible on loaded CI.
func (a *fakeXPAwarder) waitForAwards(want int, deadline time.Time) []string {
	for {
		a.mu.Lock()
		got := len(a.awards)
		a.mu.Unlock()
		if got >= want || !time.Now().Before(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.awards...)
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

	awards := awarder.waitForAwards(1, time.Now().Add(2*time.Second))
	if len(awards) != 1 {
		t.Fatalf("AwardTaskXP call count = %d, want 1", len(awards))
	}
	if awards[0] != "agent-a" {
		t.Errorf("AwardTaskXP called for agent %q, want agent-a", awards[0])
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
