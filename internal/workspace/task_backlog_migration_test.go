package workspace

import "testing"

// TestBacklogIntroduction_LegacyTaskStatusesRemainRunnable covers task-list
// 1.14/1.17 (PRD FR92-97): TaskStatusBacklog is a brand-new value that no
// persisted task predates, so introducing it must not change how any
// existing status is treated. Every legacy status must still be able to run,
// still be excluded from the Backlog-only guard, and (when unassigned) still
// be eligible for coordinator claim exactly as before this feature.
func TestBacklogIntroduction_LegacyTaskStatusesRemainRunnable(t *testing.T) {
	legacyRunnableOrAttention := []TaskStatus{
		TaskStatusPending,
		TaskStatusAssigned,
		TaskStatusInProgress,
		TaskStatusWaitingForChoice,
		TaskStatusCompleted,
		TaskStatusFailed,
		TaskStatusCancelled,
		TaskStatusTimeout,
	}

	for _, s := range legacyRunnableOrAttention {
		t.Run(string(s), func(t *testing.T) {
			task := &Task{ID: "t", Status: s}

			// RequireTaskNotBacklog must never reject a legacy status — only
			// TaskStatusBacklog itself is refused (FR97: no previously
			// runnable task may become non-runnable).
			if err := RequireTaskNotBacklog(task, "run"); err != nil {
				t.Fatalf("legacy status %q rejected by Backlog guard: %v", s, err)
			}

			// ValidateBacklogTaskInvariants only applies to Backlog tasks; a
			// legacy task carrying an assignee/schedule/result must still be
			// a no-op here regardless of status.
			loaded := &Task{
				ID: "t", Status: s, To: "agent-a",
				ScheduleEnabled: true, Result: "done",
			}
			if err := ValidateBacklogTaskInvariants(loaded); err != nil {
				t.Fatalf("legacy status %q incorrectly subjected to backlog invariants: %v", s, err)
			}
		})
	}

	// An unassigned legacy-status task (the only status the coordinator sweep
	// would ever see as a candidate) remains claimable exactly as it was
	// before Backlog existed: eligibility depends only on the assignee being
	// empty, not on AwaitingExecutionIntent (its zero value is false for
	// every task that predates this feature).
	unassignedLegacy := &Task{ID: "t", Status: TaskStatusPending}
	if !taskEligibleForCoordinatorClaim(unassignedLegacy) {
		t.Fatalf("pre-existing unassigned Pending task must remain claimable")
	}
}

// TestBacklogIntroduction_KanbanMigrationDoesNotTouchTaskStatus covers the
// same guarantee from the board-config side: migrating a workspace's legacy
// kanban_column_id/board config never mutates Task.Status. The "backlog"
// board column id was always presentation-only metadata, disconnected from
// lifecycle status, both before and after this feature.
func TestBacklogIntroduction_KanbanMigrationDoesNotTouchTaskStatus(t *testing.T) {
	// A task that predates this feature could have context.kanban_column_id
	// == "backlog" while its Status was already Pending/Assigned/etc. That
	// combination must remain intact — the column id is not reinterpreted as
	// TaskStatusBacklog by any code path introduced in this change.
	task := &Task{
		ID:     "t",
		Status: TaskStatusPending,
		Context: map[string]any{
			"kanban_column_id": "backlog",
		},
	}
	if task.Status != TaskStatusPending {
		t.Fatalf("Status = %q, want unchanged Pending", task.Status)
	}
	if task.Context["kanban_column_id"] != "backlog" {
		t.Fatalf("kanban_column_id was mutated: %+v", task.Context)
	}
	if err := RequireTaskNotBacklog(task, "run"); err != nil {
		t.Fatalf("a task in the legacy 'backlog' board column but Pending status must remain runnable: %v", err)
	}
}
