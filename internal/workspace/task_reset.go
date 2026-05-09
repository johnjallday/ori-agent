package workspace

// ResetTaskRuntime clears all per-execution runtime state on t so a re-run
// starts from a clean slate. Use this from scheduler-driven re-runs and
// manual "Run again" actions on completed/failed tasks — paths that intend
// to restart the structured execution plan from step zero.
//
// If the caller wants the next run to resume mid-plan (the blocked-task
// retry/switch-agent flow), call ResetTaskRuntimeKeepingSteps instead and
// follow it with PrepareTaskExecutionStepsForResume.
//
// Cleared:
//   - Result, ResultType, StructuredResult (via ApplyTaskResultMetadata)
//   - Error
//   - StartedAt, CompletedAt
//   - ExecutionTrace
//   - Progress + ExecutionSteps statuses (via ResetTaskExecutionSteps)
//   - Runtime Context keys: human_loop, structured_output, the
//     execution_blocked_step_* and execution_step_waiting_* scratch keys
//
// NOT cleared (callers control these explicitly):
//   - Status — call SetStatus before or after reset
//   - ExecutionHistory — the cross-run audit trail
//   - Schedule fields (LastRun, NextRun, ExecutionCount, FailureCount)
//   - Authored Context keys (anything not in the well-known runtime set)
//   - Description, Details, OutputSchema, InputTaskIDs, ParentTaskID, etc.
func ResetTaskRuntime(t *Task) {
	ResetTaskRuntimeKeepingSteps(t)
	// ResetTaskExecutionSteps wipes ExecutionSteps[*].Status back to Pending,
	// clears their per-step Result/Error/timestamps, resets Progress, and
	// drops the execution_step_waiting* scratch Context keys.
	ResetTaskExecutionSteps(t)
}

// ResetTaskRuntimeKeepingSteps clears the per-execution runtime state that
// always decays between runs while leaving the structured ExecutionSteps
// slice and Progress intact. Used by the blocked-task resume paths, where
// the next run is meant to continue from the first not-yet-completed step.
//
// See ResetTaskRuntime for the full field list this covers.
func ResetTaskRuntimeKeepingSteps(t *Task) {
	if t == nil {
		return
	}

	t.Result = ""
	// ApplyTaskResultMetadata("") nils ResultType and StructuredResult.
	ApplyTaskResultMetadata(t, "")
	t.Error = ""
	t.StartedAt = nil
	t.CompletedAt = nil
	t.ExecutionTrace = nil

	if t.Context != nil {
		// Runtime scratch keys written by the executor / blocked-task
		// machinery. Authored Context keys (those the user / planner placed
		// on the task) are left alone.
		delete(t.Context, "human_loop")
		delete(t.Context, "structured_output")
		delete(t.Context, "execution_blocked_step_index")
		delete(t.Context, "execution_blocked_step_title")
	}
}
