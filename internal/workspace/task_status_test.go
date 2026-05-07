package workspace

import (
	"errors"
	"testing"
)

func TestSetStatus_LegalForwardLifecycle(t *testing.T) {
	t.Parallel()

	tk := &Task{ID: "t"}
	transitions := []TaskStatus{
		TaskStatusPending,
		TaskStatusAssigned,
		TaskStatusInProgress,
		TaskStatusCompleted,
	}
	for _, next := range transitions {
		if err := tk.SetStatus(next); err != nil {
			t.Fatalf("legal transition rejected: %v", err)
		}
		if tk.Status != next {
			t.Fatalf("Status = %q, want %q", tk.Status, next)
		}
	}
}

func TestSetStatus_RejectsSameStateNoop(t *testing.T) {
	t.Parallel()

	tk := &Task{ID: "t", Status: TaskStatusInProgress}
	err := tk.SetStatus(TaskStatusInProgress)
	if err == nil {
		t.Fatalf("expected error for same-state transition")
	}
	var ie *IllegalTaskTransitionError
	if !errors.As(err, &ie) {
		t.Fatalf("expected IllegalTaskTransitionError, got %T", err)
	}
}

func TestSetStatus_RejectsIllegalSkip(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		from TaskStatus
		to   TaskStatus
	}{
		{"Pending→Failed", TaskStatusPending, TaskStatusFailed},
		{"Pending→Timeout", TaskStatusPending, TaskStatusTimeout},
		{"Pending→WaitingForChoice", TaskStatusPending, TaskStatusWaitingForChoice},
		{"Assigned→Completed", TaskStatusAssigned, TaskStatusCompleted},
		{"Assigned→Failed", TaskStatusAssigned, TaskStatusFailed},
		{"Completed→Failed", TaskStatusCompleted, TaskStatusFailed},
		{"Failed→Completed", TaskStatusFailed, TaskStatusCompleted},
		{"Cancelled→Completed", TaskStatusCancelled, TaskStatusCompleted},
		{"Cancelled→Cancelled", TaskStatusCancelled, TaskStatusCancelled},
		{"Completed→Completed (no-op)", TaskStatusCompleted, TaskStatusCompleted},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tk := &Task{ID: "t", Status: tc.from}
			if err := tk.SetStatus(tc.to); err == nil {
				t.Fatalf("expected illegal transition %s, got nil", tc.name)
			}
			if tk.Status != tc.from {
				t.Fatalf("status changed despite error: from %q to %q", tc.from, tk.Status)
			}
		})
	}
}

func TestSetStatus_AllowsResetPaths(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		from TaskStatus
		to   TaskStatus
	}{
		{"Completed→Pending", TaskStatusCompleted, TaskStatusPending},
		{"Completed→Assigned", TaskStatusCompleted, TaskStatusAssigned},
		{"Failed→Pending", TaskStatusFailed, TaskStatusPending},
		{"Failed→Assigned", TaskStatusFailed, TaskStatusAssigned},
		{"Cancelled→Pending", TaskStatusCancelled, TaskStatusPending},
		{"Cancelled→Assigned", TaskStatusCancelled, TaskStatusAssigned},
		{"Timeout→Assigned", TaskStatusTimeout, TaskStatusAssigned},
		{"InProgress→Pending (orphan cleanup)", TaskStatusInProgress, TaskStatusPending},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tk := &Task{ID: "t", Status: tc.from}
			if err := tk.SetStatus(tc.to); err != nil {
				t.Fatalf("expected legal reset, got error: %v", err)
			}
		})
	}
}

func TestSetStatus_BlockedFlow(t *testing.T) {
	t.Parallel()

	// InProgress → WaitingForChoice → InProgress (resumed)
	tk := &Task{ID: "t", Status: TaskStatusInProgress}
	if err := tk.SetStatus(TaskStatusWaitingForChoice); err != nil {
		t.Fatalf("InProgress → WaitingForChoice rejected: %v", err)
	}
	if err := tk.SetStatus(TaskStatusInProgress); err != nil {
		t.Fatalf("WaitingForChoice → InProgress rejected: %v", err)
	}
}

func TestSetStatus_ZeroStateInitial(t *testing.T) {
	t.Parallel()

	for _, next := range []TaskStatus{TaskStatusPending, TaskStatusAssigned, TaskStatusInProgress, TaskStatusCompleted} {
		tk := &Task{ID: "t"}
		if err := tk.SetStatus(next); err != nil {
			t.Errorf("zero-state → %q rejected: %v", next, err)
		}
	}
	// Failed should NOT be a legal initial state
	tk := &Task{ID: "t"}
	if err := tk.SetStatus(TaskStatusFailed); err == nil {
		t.Errorf("zero-state → Failed should be illegal")
	}
}

func TestForceStatus_BypassesTable(t *testing.T) {
	t.Parallel()

	tk := &Task{ID: "t", Status: TaskStatusCompleted}
	tk.ForceStatus(TaskStatusFailed)
	if tk.Status != TaskStatusFailed {
		t.Fatalf("ForceStatus did not apply")
	}
}
