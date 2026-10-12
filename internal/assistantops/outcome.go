package assistantops

import "time"

// TaskRun is one execution a task recorded for itself. TaskState.Runs lists
// them oldest first.
type TaskRun struct {
	// At is when the execution began. It identifies the record.
	At time.Time
	// Status is the record's own word: "success", "failed" or "blocked".
	Status string
	// HasResult: the record still holds that execution's result body.
	HasResult bool
}

// What became of the operation's own run.
const (
	RunRunning     = "running"     // still executing
	RunWaiting     = "waiting"     // stopped for the user (input, a choice, a repair) and still waiting
	RunStopped     = "stopped"     // stopped for the user; the task was continued by a later attempt
	RunSucceeded   = "succeeded"   // recorded as finished
	RunFailed      = "failed"      // recorded as failed or timed out
	RunCancelled   = "cancelled"   // the task was cancelled
	RunInterrupted = "interrupted" // it was launched and never recorded an execution
	RunUnknown     = "unknown"     // the task no longer keeps its record
)

// Where the result of the operation's own run can be found.
const (
	ResultNone    = "none"    // there is no result (not finished, failed, interrupted)
	ResultCurrent = "current" // it is the result the task shows now
	ResultHistory = "history" // the task ran again; this run's result is in its history
	ResultGone    = "gone"    // the run finished, but its result is no longer kept
)

// Outcome says what happened to the run this operation started, and where its
// result is. It is resolved from the task on every read and never stored, so
// a rerun, a trimmed history or a removed result is reported as it is now. It
// exists so that a later run's output is never presented as this operation's.
type Outcome struct {
	Run    string `json:"run"`
	Result string `json:"result"`
	// LaterRuns counts executions of the task after the operation's own.
	LaterRuns int `json:"later_runs,omitempty"`
}

// ResolveOutcome attributes the task's state to the operation's own run.
//
// Which execution was the operation's is not inferred. The task owner marks
// it: when the operation's Start is launched, and again, atomically with the
// execution record, when that execution is recorded (see TaskState.Launched).
// A run identity is not used, because one execution may retry itself under
// several. So:
//
//   - not launched by the operation (the user started the task from its own
//     controls): no outcome. The operation claims no run and no result.
//   - launched and recorded: that record, and everything after it is a rerun.
//   - launched, not recorded, still in flight: running or waiting.
//   - launched and never recorded: interrupted. A later run is not this one.
//
// grace bounds the moment between the launch and the task reporting that it
// is running; claimed is when Start was claimed.
func ResolveOutcome(binding *TaskBinding, state TaskState, now time.Time, grace time.Duration) *Outcome {
	if binding == nil || binding.TaskID == "" || !state.Launched {
		return nil
	}
	later := len(state.Runs)
	if state.Active {
		// A run in progress has no record yet.
		later++
	}

	if !state.LaunchedRunAt.IsZero() {
		mine := -1
		for i, run := range state.Runs {
			if run.At.Equal(state.LaunchedRunAt) {
				mine = i
				break
			}
		}
		if mine < 0 {
			// The task has dropped the record from its history.
			return &Outcome{Run: RunUnknown, Result: ResultGone, LaterRuns: later}
		}
		record := state.Runs[mine]
		out := &Outcome{Result: ResultNone, LaterRuns: later - 1 - mine}
		switch record.Status {
		case "success":
			out.Run = RunSucceeded
			switch {
			case out.LaterRuns == 0 && state.HasResult:
				out.Result = ResultCurrent
			case out.LaterRuns > 0 && record.HasResult:
				out.Result = ResultHistory
			default:
				out.Result = ResultGone
			}
		case "blocked":
			// It stopped for the user. Either it is still waiting on them, or
			// the task has since been continued by a later attempt.
			out.Run = RunStopped
			if state.Waiting && out.LaterRuns == 0 {
				out.Run = RunWaiting
			}
		case "cancelled":
			out.Run = RunCancelled
		default:
			out.Run = RunFailed
		}
		return out
	}

	if state.LaunchPending {
		switch {
		case state.Active:
			return &Outcome{Run: RunRunning, Result: ResultNone}
		case state.Waiting:
			return &Outcome{Run: RunWaiting, Result: ResultNone}
		}
		// Not running and nothing recorded. The task's own status is all there is.
		switch state.Status {
		case "completed":
			if state.HasResult {
				return &Outcome{Run: RunSucceeded, Result: ResultCurrent}
			}
			return &Outcome{Run: RunSucceeded, Result: ResultGone}
		case "failed", "timeout":
			return &Outcome{Run: RunFailed, Result: ResultNone}
		case "cancelled":
			return &Outcome{Run: RunCancelled, Result: ResultNone}
		}
		// Launched and still waiting to begin: for a moment that is normal.
		// After that, nothing in this process is going to run it.
		if binding.ClaimedAt != nil && now.Sub(*binding.ClaimedAt) <= grace {
			return &Outcome{Run: RunRunning, Result: ResultNone}
		}
		return &Outcome{Run: RunInterrupted, Result: ResultNone}
	}

	// Launched, and marked as never going to record: the process that was
	// running it stopped. Whatever the task has done since is a later run.
	return &Outcome{Run: RunInterrupted, Result: ResultNone, LaterRuns: later}
}
