package workspace

import (
	"reflect"
	"testing"
)

// TestBuildRuntimeInputs_DoesNotMutateContext is the regression test for the
// "Context overloading" bug. Previously, GetInputContext merged
// input_task_results / input_task_structured_outputs into a copy of
// task.Context and callers assigned the result back to task.Context — so the
// persisted task accumulated runtime injection across re-runs.
//
// BuildRuntimeInputs returns a fresh struct on the side and never touches
// task.Context. This test asserts that invariant.
func TestBuildRuntimeInputs_DoesNotMutateContext(t *testing.T) {
	t.Parallel()

	ws := &Workspace{
		ID:     "ws-runtime",
		Name:   "Runtime Inputs",
		Status: StatusActive,
		Agents: []string{"alice"},
	}
	upstream := Task{
		ID:          "upstream",
		WorkspaceID: ws.ID,
		Description: "compute thing",
		Status:      TaskStatusCompleted,
		Result:      "42",
		To:          "alice",
	}
	downstream := Task{
		ID:           "downstream",
		WorkspaceID:  ws.ID,
		Description:  "consume {result}",
		Status:       TaskStatusPending,
		To:           "alice",
		InputTaskIDs: []string{"upstream"},
		Context: map[string]interface{}{
			"author_note": "this is authored, not runtime",
		},
	}
	ws.Tasks = []Task{upstream, downstream}
	ws.taskIndex = map[string]int{"upstream": 0, "downstream": 1}

	authored := map[string]interface{}{
		"author_note": "this is authored, not runtime",
	}

	// Run BuildRuntimeInputs three times and verify Context never gains keys.
	for i := 0; i < 3; i++ {
		task, err := ws.GetTask("downstream")
		if err != nil {
			t.Fatalf("get task: %v", err)
		}

		ri := ws.BuildRuntimeInputs(task)
		if ri == nil {
			t.Fatalf("iteration %d: expected non-nil RuntimeInputs", i)
		}
		if got := ri.TaskResults["upstream"]; got != "42" {
			t.Errorf("iteration %d: upstream result = %q, want %q", i, got, "42")
		}

		// The persisted task.Context must remain exactly as authored.
		fresh, _ := ws.GetTask("downstream")
		if !reflect.DeepEqual(fresh.Context, authored) {
			t.Errorf("iteration %d: task.Context drifted from authored state.\n got: %#v\nwant: %#v", i, fresh.Context, authored)
		}
		if _, leaked := fresh.Context["input_task_results"]; leaked {
			t.Errorf("iteration %d: input_task_results leaked into task.Context", i)
		}
		if _, leaked := fresh.Context["input_task_structured_outputs"]; leaked {
			t.Errorf("iteration %d: input_task_structured_outputs leaked into task.Context", i)
		}
	}
}

// TestBuildRuntimeInputs_NilForEmptyInputs verifies the nil-return contract
// so callers can branch cleanly without checking for empty maps.
func TestBuildRuntimeInputs_NilForEmptyInputs(t *testing.T) {
	t.Parallel()

	ws := &Workspace{
		ID:     "ws-empty",
		Status: StatusActive,
		Agents: []string{"alice"},
	}
	ws.Tasks = []Task{{
		ID:          "t",
		WorkspaceID: "ws-empty",
		Description: "no inputs",
		Status:      TaskStatusPending,
	}}
	ws.taskIndex = map[string]int{"t": 0}

	if got := ws.BuildRuntimeInputs(&ws.Tasks[0]); got != nil {
		t.Errorf("expected nil for task without InputTaskIDs, got %#v", got)
	}

	// Task with InputTaskIDs but no upstream results yet
	ws.Tasks[0].InputTaskIDs = []string{"missing"}
	if got := ws.BuildRuntimeInputs(&ws.Tasks[0]); got != nil {
		// missing tasks are returned as their description by GetTaskResults,
		// so this actually produces a result. Verify behavior matches that.
		// (See workspace_tasks.go GetTaskResults: missing IDs aren't included,
		// only existing tasks with empty results fall back to description.)
		if len(got.TaskResults) != 0 {
			t.Errorf("expected empty TaskResults for missing input, got %v", got.TaskResults)
		}
	}
}
