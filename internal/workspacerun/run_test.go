package workspacerun

import "testing"

func TestRunStatusConstants(t *testing.T) {
	statuses := []RunStatus{
		RunStatusPending,
		RunStatusPreparing,
		RunStatusPreparingContext,
		RunStatusExecuting,
		RunStatusValidating,
		RunStatusAwaitingApproval,
		RunStatusSucceeded,
		RunStatusFailed,
		RunStatusCancelled,
		RunStatusRejected,
	}
	want := []string{
		"pending",
		"preparing",
		"preparing_context",
		"executing",
		"validating",
		"awaiting_approval",
		"succeeded",
		"failed",
		"cancelled",
		"rejected",
	}
	for i, status := range statuses {
		if string(status) != want[i] {
			t.Fatalf("status[%d] = %q, want %q", i, status, want[i])
		}
	}
}
