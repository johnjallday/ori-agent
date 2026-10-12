package assistantops

import (
	"testing"
	"time"
)

func TestResolveOutcome_AttributesOnlyTheOperationsOwnExecution(t *testing.T) {
	now := time.Date(2026, 10, 11, 9, 30, 0, 0, time.UTC)
	t1, t2, t3 := now.Add(-20*time.Minute), now.Add(-10*time.Minute), now.Add(-5*time.Minute)
	claimedLongAgo := now.Add(-25 * time.Minute)
	claimedJustNow := now.Add(-2 * time.Second)
	ok := func(at time.Time) TaskRun { return TaskRun{At: at, Status: "success", HasResult: true} }
	binding := TaskBinding{TaskID: "t", RunID: "first-attempt", ClaimedAt: &claimedLongAgo}
	fresh := TaskBinding{TaskID: "t", ClaimedAt: &claimedJustNow}

	cases := map[string]struct {
		binding TaskBinding
		state   TaskState
		want    *Outcome
	}{
		// --- the ordinary journey
		"just launched, not yet running": {
			fresh, TaskState{Status: "pending", Launched: true, LaunchPending: true},
			&Outcome{Run: RunRunning, Result: ResultNone},
		},
		"running": {
			binding, TaskState{Status: "in_progress", Active: true, Launched: true, LaunchPending: true},
			&Outcome{Run: RunRunning, Result: ResultNone},
		},
		"finished: the task shows this execution's result": {
			binding, TaskState{Status: "completed", HasResult: true, Launched: true, LaunchedRunAt: t1, Runs: []TaskRun{ok(t1)}},
			&Outcome{Run: RunSucceeded, Result: ResultCurrent},
		},
		// One execution retried itself under other run identities. The record
		// is still this operation's: identity is the mark, not a run ID.
		"finished after internal retries under other run identities": {
			TaskBinding{TaskID: "t", RunID: "attempt-1", ClaimedAt: &claimedLongAgo},
			TaskState{Status: "completed", HasResult: true, RunID: "attempt-3", Launched: true, LaunchedRunAt: t1, Runs: []TaskRun{ok(t1)}},
			&Outcome{Run: RunSucceeded, Result: ResultCurrent},
		},
		"failed": {
			binding, TaskState{Status: "failed", Launched: true, LaunchedRunAt: t1, Runs: []TaskRun{{At: t1, Status: "failed"}}},
			&Outcome{Run: RunFailed, Result: ResultNone},
		},
		"stopped for the user and still waiting": {
			binding, TaskState{Status: "waiting_for_choice", Waiting: true, Launched: true, LaunchedRunAt: t1, Runs: []TaskRun{{At: t1, Status: "blocked"}}},
			&Outcome{Run: RunWaiting, Result: ResultNone},
		},
		"waiting before anything was recorded": {
			binding, TaskState{Status: "waiting_for_choice", Waiting: true, Launched: true, LaunchPending: true},
			&Outcome{Run: RunWaiting, Result: ResultNone},
		},
		"finished without writing a record": {
			binding, TaskState{Status: "completed", HasResult: true, Launched: true, LaunchPending: true},
			&Outcome{Run: RunSucceeded, Result: ResultCurrent},
		},
		"failed without writing a record": {
			binding, TaskState{Status: "failed", Launched: true, LaunchPending: true},
			&Outcome{Run: RunFailed, Result: ResultNone},
		},
		"cancelled": {
			binding, TaskState{Status: "cancelled", Launched: true, LaunchPending: true},
			&Outcome{Run: RunCancelled, Result: ResultNone},
		},

		// --- a later run must never be presented as this operation's
		"rerun finished: this execution's result is in the history": {
			binding, TaskState{Status: "completed", HasResult: true, Launched: true, LaunchedRunAt: t1, Runs: []TaskRun{ok(t1), ok(t2)}},
			&Outcome{Run: RunSucceeded, Result: ResultHistory, LaterRuns: 1},
		},
		"rerun in progress": {
			binding, TaskState{Status: "in_progress", Active: true, HasResult: true, Launched: true, LaunchedRunAt: t1, Runs: []TaskRun{ok(t1)}},
			&Outcome{Run: RunSucceeded, Result: ResultHistory, LaterRuns: 1},
		},
		"rerun, and the old record no longer holds the result": {
			binding, TaskState{Status: "completed", HasResult: true, Launched: true, LaunchedRunAt: t1, Runs: []TaskRun{{At: t1, Status: "success"}, ok(t2)}},
			&Outcome{Run: RunSucceeded, Result: ResultGone, LaterRuns: 1},
		},
		"the result was removed from the task": {
			binding, TaskState{Status: "completed", Launched: true, LaunchedRunAt: t1, Runs: []TaskRun{ok(t1)}},
			&Outcome{Run: RunSucceeded, Result: ResultGone},
		},
		"stopped for the user, then continued by a later attempt": {
			binding, TaskState{Status: "completed", HasResult: true, Launched: true, LaunchedRunAt: t1, Runs: []TaskRun{{At: t1, Status: "blocked"}, ok(t2)}},
			&Outcome{Run: RunStopped, Result: ResultNone, LaterRuns: 1},
		},
		"two reruns": {
			binding, TaskState{Status: "completed", HasResult: true, Launched: true, LaunchedRunAt: t1, Runs: []TaskRun{ok(t1), ok(t2), ok(t3)}},
			&Outcome{Run: RunSucceeded, Result: ResultHistory, LaterRuns: 2},
		},
		"the record was dropped from the task's history": {
			binding, TaskState{Status: "completed", HasResult: true, Launched: true, LaunchedRunAt: t1, Runs: []TaskRun{ok(t2), ok(t3)}},
			&Outcome{Run: RunUnknown, Result: ResultGone, LaterRuns: 2},
		},

		// --- interrupted: launched, and never recorded
		"a restart left the task waiting to start again": {
			binding, TaskState{Status: "pending", Launched: true},
			&Outcome{Run: RunInterrupted, Result: ResultNone},
		},
		"interrupted, then run again to completion": {
			binding, TaskState{Status: "completed", HasResult: true, Launched: true, Runs: []TaskRun{ok(t2)}},
			&Outcome{Run: RunInterrupted, Result: ResultNone, LaterRuns: 1},
		},
		"interrupted, and another run is in progress": {
			binding, TaskState{Status: "in_progress", Active: true, Launched: true},
			&Outcome{Run: RunInterrupted, Result: ResultNone, LaterRuns: 1},
		},
		"launched long ago and still waiting to begin": {
			binding, TaskState{Status: "pending", Launched: true, LaunchPending: true},
			&Outcome{Run: RunInterrupted, Result: ResultNone},
		},

		// --- not the operation's start at all
		"started from the task's own controls": {
			TaskBinding{TaskID: "t", RunID: "manual"}, TaskState{Status: "completed", HasResult: true, Runs: []TaskRun{ok(t1)}},
			nil,
		},
		"started from the task's own controls and running": {
			TaskBinding{TaskID: "t"}, TaskState{Status: "in_progress", Active: true},
			nil,
		},
	}
	for name, tc := range cases {
		got := ResolveOutcome(&tc.binding, tc.state, now, DefaultStartGrace)
		switch {
		case tc.want == nil && got != nil, tc.want != nil && got == nil:
			t.Errorf("%s: got %+v, want %+v", name, got, tc.want)
		case tc.want != nil && *got != *tc.want:
			t.Errorf("%s: got %+v, want %+v", name, got, tc.want)
		}
		// Only "current" lets the task's present result stand for this
		// execution, and never when the task has run again.
		if got != nil && got.Result == ResultCurrent && (got.LaterRuns != 0 || got.Run != RunSucceeded) {
			t.Errorf("%s: a later run's result was attributed to this operation: %+v", name, got)
		}
	}
	if ResolveOutcome(nil, TaskState{Launched: true}, now, DefaultStartGrace) != nil ||
		ResolveOutcome(&TaskBinding{}, TaskState{Launched: true}, now, DefaultStartGrace) != nil {
		t.Error("an operation without a task has no outcome")
	}
}

func TestSnapshot_ReportsTheOutcomeFromTheTaskAndStoresNone(t *testing.T) {
	f := newTaskFixture(t)
	reviewed := f.reviewed()
	if reviewed.Outcome != nil {
		t.Fatalf("a task that has not started has no outcome: %+v", reviewed.Outcome)
	}
	// The start owner marks the task as launched, as the real one does.
	f.tasks.onStart = func(taskID string) {
		f.tasks.change(taskID, func(state *TaskState) { state.Launched, state.LaunchPending = true, true })
	}
	if _, err := f.service.Start(t.Context(), reviewed.ID, "tab-a"); err != nil {
		t.Fatal(err)
	}
	f.tasks.change("task-1", func(state *TaskState) {
		state.Ran, state.Startable, state.RunID, state.Active, state.Status = true, false, "run-1", true, "in_progress"
	})
	running := f.read(reviewed.ID)
	if running.Status != StatusStarted || running.Outcome == nil || *running.Outcome != (Outcome{Run: RunRunning, Result: ResultNone}) {
		t.Fatalf("running: %+v", running.Outcome)
	}
	revision := running.Revision

	first := f.clock
	f.tasks.change("task-1", func(state *TaskState) {
		state.Active, state.Status, state.HasResult = false, "completed", true
		state.LaunchPending, state.LaunchedRunAt = false, first
		state.Runs = []TaskRun{{At: first, Status: "success", HasResult: true}}
	})
	done := f.read(reviewed.ID)
	if done.Outcome == nil || *done.Outcome != (Outcome{Run: RunSucceeded, Result: ResultCurrent}) {
		t.Fatalf("finished: %+v", done.Outcome)
	}
	// The user reruns the task from its own controls.
	f.tasks.change("task-1", func(state *TaskState) {
		state.Runs = append(state.Runs, TaskRun{At: first.Add(time.Hour), Status: "success", HasResult: true})
	})
	rerun := f.read(reviewed.ID)
	if rerun.Outcome == nil || *rerun.Outcome != (Outcome{Run: RunSucceeded, Result: ResultHistory, LaterRuns: 1}) || rerun.Task.RunID != "run-1" {
		t.Fatalf("after a rerun: %+v %+v", rerun.Outcome, rerun.Task)
	}
	// Reading an outcome writes nothing: the operation does not change with the task.
	if rerun.Revision != revision || f.tasks.started() != 1 {
		t.Fatalf("an outcome read changed the operation: revision %d -> %d", revision, rerun.Revision)
	}
	// An unreadable task means no outcome, not a guessed one.
	f.tasks.mu.Lock()
	f.tasks.readErr = errString("disk")
	f.tasks.mu.Unlock()
	if blind := f.read(reviewed.ID); blind.Outcome != nil || blind.Status != StatusStarted {
		t.Fatalf("got %+v", blind.Outcome)
	}
}

// A task the user started from its own controls is observed as started, and
// the operation attributes no run and no result to itself.
func TestSnapshot_AManualStartHasNoOutcome(t *testing.T) {
	f := newTaskFixture(t)
	reviewed := f.reviewed()
	f.tasks.change("task-1", func(state *TaskState) {
		state.Ran, state.Startable, state.Status, state.HasResult = true, false, "completed", true
		state.Runs = []TaskRun{{At: f.clock, Status: "success", HasResult: true}}
	})
	observed := f.read(reviewed.ID)
	if observed.Status != StatusStarted || observed.Outcome != nil || f.tasks.started() != 0 {
		t.Fatalf("got %+v %+v", observed.Status, observed.Outcome)
	}
}

type errString string

func (e errString) Error() string { return string(e) }
