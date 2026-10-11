package workspace

import "time"

// TaskOperationRun marks which of a task's executions was the first start an
// assistant operation made for it. One execution may retry itself under
// several run identities, and a task may be run again later from its own
// controls, so neither a run identity nor "the latest result" says whose an
// execution was. This does, exactly:
//
//   - nil: no assistant operation started this task.
//   - Pending: that start was launched and has not recorded an execution yet.
//   - ExecutedAt set: the execution record with that ExecutedAt is the one.
//   - neither: the start was launched and never recorded an execution, as
//     when the process stopped mid-run. A later run is not the operation's.
//
// It is written by the task owner only (never from a request body) and
// grants nothing.
type TaskOperationRun struct {
	Pending    bool       `json:"pending,omitempty"`
	ExecutedAt *time.Time `json:"executed_at,omitempty"`
}

// MarkOperationRunLaunched records that an assistant operation's start is
// being launched for the task. The next execution record it writes is that
// start's.
func MarkOperationRunLaunched(task *Task) {
	if task != nil {
		task.AssistantOperationRun = &TaskOperationRun{Pending: true}
	}
}

// recordOperationRun binds a launched start to the execution being recorded.
// It runs inside RecordTaskExecution, so the mark and the record are written
// together and no reader sees one without the other.
func recordOperationRun(task *Task, executedAt time.Time) {
	if run := task.AssistantOperationRun; run != nil && run.Pending {
		at := executedAt
		task.AssistantOperationRun = &TaskOperationRun{ExecutedAt: &at}
	}
}

// abandonOperationRun records that a launched start will never record an
// execution: the process that was running it is gone.
func abandonOperationRun(task *Task) {
	if run := task.AssistantOperationRun; run != nil && run.Pending {
		task.AssistantOperationRun = &TaskOperationRun{}
	}
}
