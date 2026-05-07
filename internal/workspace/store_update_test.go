package workspace

import (
	"fmt"
	"sync"
	"testing"
)

// TestStoreUpdate_NoLostUpdatesUnderConcurrency exercises the cross-instance
// race that motivated Store.Update: with the old GetClone → mutate → Save
// pattern, two goroutines could clone, mutate disjoint tasks, and have one
// Save silently overwrite the other's mutation.
//
// Each goroutine flips one task's FailureCount independently, so a correct
// implementation must record exactly N increments across N tasks. Lost updates
// would show up as one or more tasks with FailureCount < expected.
func TestStoreUpdate_NoLostUpdatesUnderConcurrency(t *testing.T) {
	t.Parallel()

	store := NewInMemoryStore()
	const numTasks = 20
	const incrementsPerTask = 50

	ws := &Workspace{
		ID:     "ws-update-race",
		Name:   "Update Race",
		Status: StatusActive,
		Agents: []string{"alice"},
	}
	for i := 0; i < numTasks; i++ {
		ws.Tasks = append(ws.Tasks, Task{
			ID:          fmt.Sprintf("task-%d", i),
			WorkspaceID: ws.ID,
			Description: fmt.Sprintf("task %d", i),
			Status:      TaskStatusPending,
			To:          "alice",
		})
	}
	ws.taskIndex = make(map[string]int, numTasks)
	for i, task := range ws.Tasks {
		ws.taskIndex[task.ID] = i
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("seed save: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(numTasks * incrementsPerTask)
	for i := 0; i < numTasks; i++ {
		taskID := fmt.Sprintf("task-%d", i)
		for j := 0; j < incrementsPerTask; j++ {
			go func(taskID string) {
				defer wg.Done()
				if err := store.Update(ws.ID, func(fresh *Workspace) error {
					return fresh.MutateTask(taskID, func(t *Task) error {
						t.FailureCount++
						return nil
					})
				}); err != nil {
					t.Errorf("update %s: %v", taskID, err)
				}
			}(taskID)
		}
	}
	wg.Wait()

	final, err := store.Get(ws.ID)
	if err != nil {
		t.Fatalf("final get: %v", err)
	}
	for _, task := range final.Tasks {
		if task.FailureCount != incrementsPerTask {
			t.Errorf("%s: expected FailureCount=%d, got %d (lost updates)", task.ID, incrementsPerTask, task.FailureCount)
		}
	}
}

// TestStoreUpdate_SerializesPerWorkspace verifies that two Update calls on the
// same workspace cannot run their fn bodies concurrently. We rely on the lock
// to prevent interleaving rather than checking timing.
func TestStoreUpdate_SerializesPerWorkspace(t *testing.T) {
	t.Parallel()

	store := NewInMemoryStore()
	ws := &Workspace{
		ID:     "ws-serialize",
		Name:   "Serialize",
		Status: StatusActive,
		Agents: []string{"alice"},
		Tasks: []Task{{
			ID:          "task-1",
			WorkspaceID: "ws-serialize",
			Description: "t1",
			Status:      TaskStatusPending,
			To:          "alice",
		}},
	}
	ws.taskIndex = map[string]int{"task-1": 0}
	if err := store.Save(ws); err != nil {
		t.Fatalf("seed save: %v", err)
	}

	var (
		mu      sync.Mutex
		active  int
		maxSeen int
	)
	const goroutines = 8
	const iterations = 25

	var wg sync.WaitGroup
	wg.Add(goroutines * iterations)
	for i := 0; i < goroutines; i++ {
		for j := 0; j < iterations; j++ {
			go func() {
				defer wg.Done()
				_ = store.Update(ws.ID, func(fresh *Workspace) error {
					mu.Lock()
					active++
					if active > maxSeen {
						maxSeen = active
					}
					mu.Unlock()

					_ = fresh.MutateTask("task-1", func(t *Task) error {
						t.FailureCount++
						return nil
					})

					mu.Lock()
					active--
					mu.Unlock()
					return nil
				})
			}()
		}
	}
	wg.Wait()

	if maxSeen != 1 {
		t.Errorf("expected at most 1 concurrent Update on the same workspace, observed %d", maxSeen)
	}

	final, err := store.Get(ws.ID)
	if err != nil {
		t.Fatalf("final get: %v", err)
	}
	if final.Tasks[0].FailureCount != goroutines*iterations {
		t.Errorf("expected FailureCount=%d, got %d", goroutines*iterations, final.Tasks[0].FailureCount)
	}
}

// TestStoreUpdate_DistinctWorkspacesRunInParallel verifies that the
// per-workspace lock does NOT serialize across different workspaces. We
// don't measure timing; we just assert two Updates on different workspaces
// can hold their fn bodies simultaneously without deadlocking.
func TestStoreUpdate_DistinctWorkspacesRunInParallel(t *testing.T) {
	t.Parallel()

	store := NewInMemoryStore()
	for _, id := range []string{"ws-a", "ws-b"} {
		ws := &Workspace{
			ID:     id,
			Name:   id,
			Status: StatusActive,
			Agents: []string{"alice"},
			Tasks: []Task{{
				ID:          "t",
				WorkspaceID: id,
				Description: "t",
				Status:      TaskStatusPending,
				To:          "alice",
			}},
		}
		ws.taskIndex = map[string]int{"t": 0}
		if err := store.Save(ws); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	startA := make(chan struct{})
	startB := make(chan struct{})
	doneA := make(chan struct{})
	doneB := make(chan struct{})

	go func() {
		_ = store.Update("ws-a", func(fresh *Workspace) error {
			close(startA)
			<-startB // wait until B is also inside its closure — proves no cross-workspace serialization
			return fresh.MutateTask("t", func(t *Task) error {
				t.FailureCount = 1
				return nil
			})
		})
		close(doneA)
	}()

	go func() {
		<-startA
		_ = store.Update("ws-b", func(fresh *Workspace) error {
			close(startB)
			return fresh.MutateTask("t", func(t *Task) error {
				t.FailureCount = 1
				return nil
			})
		})
		close(doneB)
	}()

	<-doneA
	<-doneB
}
