package workspace

import (
	"errors"
	"testing"
	"time"
)

func TestRequireTaskNotBacklog(t *testing.T) {
	t.Parallel()

	if err := RequireTaskNotBacklog(nil, "run"); err != nil {
		t.Fatalf("nil task should be a no-op, got %v", err)
	}

	ready := &Task{ID: "t", Status: TaskStatusPending}
	if err := RequireTaskNotBacklog(ready, "run"); err != nil {
		t.Fatalf("non-Backlog task should be a no-op, got %v", err)
	}

	backlog := &Task{ID: "t", Status: TaskStatusBacklog}
	err := RequireTaskNotBacklog(backlog, "run task")
	if err == nil {
		t.Fatalf("expected error for Backlog task")
	}
	if !errors.Is(err, ErrBacklogTaskNotRunnable) {
		t.Fatalf("expected ErrBacklogTaskNotRunnable, got %v", err)
	}
}

func TestValidateBacklogTaskInvariants(t *testing.T) {
	t.Parallel()

	if err := ValidateBacklogTaskInvariants(nil); err == nil {
		t.Fatalf("expected error for nil task")
	}

	// Non-Backlog tasks are never subject to these invariants.
	nonBacklog := &Task{ID: "t", Status: TaskStatusPending, To: "agent-a", ScheduleEnabled: true}
	if err := ValidateBacklogTaskInvariants(nonBacklog); err != nil {
		t.Fatalf("non-Backlog task should be a no-op, got %v", err)
	}

	valid := &Task{ID: "t", Status: TaskStatusBacklog, Description: "investigate flaky test"}
	if err := ValidateBacklogTaskInvariants(valid); err != nil {
		t.Fatalf("valid backlog item rejected: %v", err)
	}

	now := time.Now()
	cases := []struct {
		name string
		task *Task
	}{
		{"empty description", &Task{Status: TaskStatusBacklog}},
		{"whitespace-only description", &Task{Status: TaskStatusBacklog, Description: "   "}},
		{"has assignee", &Task{Status: TaskStatusBacklog, Description: "x", To: "agent-a"}},
		{"schedule enabled", &Task{Status: TaskStatusBacklog, Description: "x", ScheduleEnabled: true}},
		{"has schedule config", &Task{Status: TaskStatusBacklog, Description: "x", Schedule: &ScheduleConfig{Type: ScheduleDaily}}},
		{"has next run", &Task{Status: TaskStatusBacklog, Description: "x", NextRun: &now}},
		{"has started at", &Task{Status: TaskStatusBacklog, Description: "x", StartedAt: &now}},
		{"has current run id", &Task{Status: TaskStatusBacklog, Description: "x", CurrentRunID: "run-1"}},
		{"has result", &Task{Status: TaskStatusBacklog, Description: "x", Result: "done"}},
		{"has error", &Task{Status: TaskStatusBacklog, Description: "x", Error: "boom"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateBacklogTaskInvariants(tc.task); err == nil {
				t.Fatalf("expected invariant violation to be rejected")
			}
		})
	}

	// "unassigned" sentinel is treated as no assignee.
	unassigned := &Task{ID: "t", Status: TaskStatusBacklog, Description: "x", To: "unassigned"}
	if err := ValidateBacklogTaskInvariants(unassigned); err != nil {
		t.Fatalf("'unassigned' sentinel should be accepted, got %v", err)
	}
}

// TestGetTaskStats_CountsBacklogSeparately covers task-list 1.10: Backlog
// tasks must be visible in workspace task-count analytics under their own
// bucket rather than silently dropped from every per-status count.
func TestGetTaskStats_CountsBacklogSeparately(t *testing.T) {
	ws := &Workspace{}
	ws.Tasks = []Task{
		{ID: "a", Status: TaskStatusBacklog},
		{ID: "b", Status: TaskStatusBacklog},
		{ID: "c", Status: TaskStatusPending},
	}

	stats := ws.GetTaskStats()
	if stats["total"] != 3 {
		t.Fatalf("total = %d, want 3", stats["total"])
	}
	if stats["backlog"] != 2 {
		t.Fatalf("backlog = %d, want 2", stats["backlog"])
	}
	if stats["pending"] != 1 {
		t.Fatalf("pending = %d, want 1", stats["pending"])
	}
}
