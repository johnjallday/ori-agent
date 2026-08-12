package orchestrationhttp

import (
	"context"
	"sync"
	"testing"
)

// FR-26: starting a Ticket claims it exclusively. A second concurrent start
// must be refused rather than run alongside the first.
//
// Before this claim existed the map simply overwrote its entry, so two
// executions mutated one record at once AND the first run's cancel function
// was dropped, leaving it running with no way to stop it.
func TestRegisterRunningTask_IsAnExclusiveClaim(t *testing.T) {
	th := &TaskHandler{}
	noop := context.CancelFunc(func() {})

	if !th.registerRunningTask("task-1", noop) {
		t.Fatalf("the first start should claim the task")
	}
	if th.registerRunningTask("task-1", noop) {
		t.Fatalf("a second concurrent start must be refused")
	}

	// A different task is unaffected.
	if !th.registerRunningTask("task-2", noop) {
		t.Fatalf("an unrelated task should still be claimable")
	}

	// Once the first run finishes, the task can be started again — this is
	// what makes a sequential retry work (FR-33).
	th.unregisterRunningTask("task-1")
	if !th.registerRunningTask("task-1", noop) {
		t.Fatalf("a task should be claimable again after its run finishes")
	}
}

func TestRegisterRunningTask_RejectsEmptyInput(t *testing.T) {
	th := &TaskHandler{}
	if th.registerRunningTask("", context.CancelFunc(func() {})) {
		t.Fatalf("an empty task id must not claim anything")
	}
	if th.registerRunningTask("task-1", nil) {
		t.Fatalf("a nil cancel func must not claim anything")
	}
}

// Exactly one of many racing starts may win.
func TestRegisterRunningTask_OnlyOneRacerWins(t *testing.T) {
	th := &TaskHandler{}
	const racers = 32

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		wins int
	)
	wg.Add(racers)
	for range racers {
		go func() {
			defer wg.Done()
			if th.registerRunningTask("task-1", context.CancelFunc(func() {})) {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if wins != 1 {
		t.Fatalf("%d starts claimed the same task, want exactly 1", wins)
	}
}
