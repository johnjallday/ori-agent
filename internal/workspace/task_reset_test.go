package workspace

import (
	"testing"
	"time"
)

func newCompletedTaskFixture() *Task {
	now := time.Now()
	earlier := now.Add(-1 * time.Minute)
	return &Task{
		ID:               "t1",
		Status:           TaskStatusCompleted,
		Description:      "do the thing",                   // authored — must survive reset
		Details:          "extra notes",                    // authored — must survive reset
		Result:           "did the thing",                  // runtime — must clear
		ResultType:       TaskResultTypeMarkdown,           // runtime — must clear
		StructuredResult: map[string]interface{}{"k": "v"}, // runtime — must clear
		Error:            "n/a",                            // runtime — must clear
		StartedAt:        &earlier,                         // runtime — must clear
		CompletedAt:      &now,                             // runtime — must clear
		Progress: &TaskProgress{
			Percentage:  50,
			CurrentStep: "halfway",
			UpdatedAt:   earlier,
		},
		ExecutionTrace: []TaskExecutionTrace{
			{Type: "test", Timestamp: earlier},
		},
		ExecutionSteps: []TaskExecutionStep{
			{Index: 0, Status: TaskExecutionStepCompleted, StartedAt: &earlier, CompletedAt: &now, Result: "step out"},
			{Index: 1, Status: TaskExecutionStepBlocked},
		},
		ExecutionHistory: []TaskExecution{
			{TaskID: "t1", Status: "success", ExecutedAt: earlier},
		},
		ExecutionCount: 5,
		Context: map[string]interface{}{
			"user_authored":                "keep me",
			"human_loop":                   map[string]interface{}{"state": "blocked"},
			"structured_output":            "{}",
			"execution_blocked_step_index": 1,
			"execution_blocked_step_title": "halt",
			"execution_step_waiting":       true,
			"execution_step_waiting_index": 1,
		},
	}
}

func TestResetTaskRuntime_ClearsAllRuntimeFields(t *testing.T) {
	task := newCompletedTaskFixture()
	originalDescription := task.Description
	originalDetails := task.Details
	originalHistoryLen := len(task.ExecutionHistory)
	originalExecutionCount := task.ExecutionCount

	ResetTaskRuntime(task)

	if task.Result != "" {
		t.Errorf("Result not cleared: %q", task.Result)
	}
	if task.ResultType != "" {
		t.Errorf("ResultType not cleared: %q", task.ResultType)
	}
	if task.StructuredResult != nil {
		t.Errorf("StructuredResult not cleared: %v", task.StructuredResult)
	}
	if task.Error != "" {
		t.Errorf("Error not cleared: %q", task.Error)
	}
	if task.StartedAt != nil {
		t.Errorf("StartedAt not cleared: %v", task.StartedAt)
	}
	if task.CompletedAt != nil {
		t.Errorf("CompletedAt not cleared: %v", task.CompletedAt)
	}
	if task.Progress != nil {
		t.Errorf("Progress not cleared: %v", task.Progress)
	}
	if len(task.ExecutionTrace) != 0 {
		t.Errorf("ExecutionTrace not cleared: %v", task.ExecutionTrace)
	}
	for i, step := range task.ExecutionSteps {
		if step.Status != TaskExecutionStepPending {
			t.Errorf("ExecutionSteps[%d].Status not reset to pending: %q", i, step.Status)
		}
		if step.StartedAt != nil || step.CompletedAt != nil {
			t.Errorf("ExecutionSteps[%d] timestamps not cleared", i)
		}
		if step.Result != "" || step.Error != "" {
			t.Errorf("ExecutionSteps[%d] result/error not cleared", i)
		}
	}

	// Runtime Context keys gone, authored key preserved.
	if _, ok := task.Context["human_loop"]; ok {
		t.Error("human_loop not removed from Context")
	}
	if _, ok := task.Context["structured_output"]; ok {
		t.Error("structured_output not removed from Context")
	}
	if _, ok := task.Context["execution_blocked_step_index"]; ok {
		t.Error("execution_blocked_step_index not removed")
	}
	if _, ok := task.Context["execution_blocked_step_title"]; ok {
		t.Error("execution_blocked_step_title not removed")
	}
	if _, ok := task.Context["execution_step_waiting"]; ok {
		t.Error("execution_step_waiting not removed")
	}
	if v, ok := task.Context["user_authored"]; !ok || v != "keep me" {
		t.Errorf("authored Context key clobbered: %v ok=%v", v, ok)
	}

	// Cross-run audit + identity preserved.
	if task.Description != originalDescription || task.Details != originalDetails {
		t.Error("authored fields clobbered")
	}
	if len(task.ExecutionHistory) != originalHistoryLen {
		t.Errorf("ExecutionHistory unexpectedly mutated: was %d now %d", originalHistoryLen, len(task.ExecutionHistory))
	}
	if task.ExecutionCount != originalExecutionCount {
		t.Errorf("ExecutionCount unexpectedly reset: was %d now %d", originalExecutionCount, task.ExecutionCount)
	}
	if task.Status != TaskStatusCompleted {
		t.Errorf("Status unexpectedly mutated: %q", task.Status)
	}
}

func TestResetTaskRuntimeKeepingSteps_PreservesExecutionSteps(t *testing.T) {
	task := newCompletedTaskFixture()
	originalSteps := make([]TaskExecutionStep, len(task.ExecutionSteps))
	copy(originalSteps, task.ExecutionSteps)
	originalProgress := *task.Progress

	ResetTaskRuntimeKeepingSteps(task)

	// Top-level runtime fields cleared.
	if task.Result != "" || task.Error != "" || task.StartedAt != nil || task.CompletedAt != nil {
		t.Error("top-level runtime fields not cleared")
	}
	if len(task.ExecutionTrace) != 0 {
		t.Error("ExecutionTrace not cleared")
	}
	if task.StructuredResult != nil {
		t.Error("StructuredResult not cleared")
	}

	// ExecutionSteps and Progress preserved (resume path needs them intact).
	if len(task.ExecutionSteps) != len(originalSteps) {
		t.Fatalf("ExecutionSteps slice length changed: %d → %d", len(originalSteps), len(task.ExecutionSteps))
	}
	for i, want := range originalSteps {
		got := task.ExecutionSteps[i]
		if got.Status != want.Status {
			t.Errorf("ExecutionSteps[%d].Status changed: %q → %q", i, want.Status, got.Status)
		}
		if got.Result != want.Result {
			t.Errorf("ExecutionSteps[%d].Result changed: %q → %q", i, want.Result, got.Result)
		}
	}
	if task.Progress == nil {
		t.Fatal("Progress was cleared but should be preserved")
	}
	if task.Progress.Percentage != originalProgress.Percentage {
		t.Errorf("Progress.Percentage changed: %d → %d", originalProgress.Percentage, task.Progress.Percentage)
	}

	// Runtime Context keys still cleared.
	if _, ok := task.Context["human_loop"]; ok {
		t.Error("human_loop not removed from Context")
	}
	if _, ok := task.Context["execution_blocked_step_title"]; ok {
		t.Error("execution_blocked_step_title not removed")
	}
}

func TestResetTaskRuntime_NilTaskIsNoOp(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("ResetTaskRuntime(nil) panicked: %v", r)
		}
	}()
	ResetTaskRuntime(nil)
	ResetTaskRuntimeKeepingSteps(nil)
}

func TestResetTaskRuntime_NilContextIsNoOp(t *testing.T) {
	task := &Task{ID: "t1", Status: TaskStatusFailed, Result: "x", Error: "y"}
	// Context starts nil. Reset must not panic and must not allocate.
	ResetTaskRuntime(task)
	if task.Result != "" || task.Error != "" {
		t.Error("runtime fields not cleared on nil-Context task")
	}
}
