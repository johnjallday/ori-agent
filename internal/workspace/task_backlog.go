package workspace

import (
	"errors"
	"fmt"
	"strings"
)

// This file holds the Group-1 safety primitives for the Backlog lifecycle
// stage (tasks/prd-workspace-backlog.md): creation invariants and the
// server-side guard every mutation path must call before assigning,
// scheduling, executing, reviewing, or completing a task (FR7-8). The
// capture/query/promotion service and HTTP API live in backlog.go (Group 2).

// ErrBacklogTaskNotRunnable is returned when a caller attempts to assign,
// schedule, execute, review, or complete a task that is still in the Backlog
// stage. The item must be promoted to Ready first.
var ErrBacklogTaskNotRunnable = errors.New("task is in Backlog and must be promoted to Ready before it can be assigned, scheduled, or executed")

// RequireTaskNotBacklog returns an actionable error when task is still in the
// Backlog stage, naming the action that was refused (FR8). It is a no-op
// (nil) for a nil task or any other status, so callers can use it as a plain
// guard clause: `if err := RequireTaskNotBacklog(task, "run task"); err != nil { ... }`.
// It reads CANONICAL Ticket state, not the legacy Status field, so the guard
// holds identically whether the call arrived through a canonical Ticket route
// or a legacy Task endpoint (FR-21, FR-103). Every existing call site inherits
// that without changing.
func RequireTaskNotBacklog(task *Task, action string) error {
	if task == nil || task.CanonicalState() != TicketStateBacklog {
		return nil
	}
	action = strings.TrimSpace(action)
	if action == "" {
		action = "this action"
	}
	return fmt.Errorf("%s: %w", action, ErrBacklogTaskNotRunnable)
}

// ValidateBacklogTaskInvariants enforces FR4-8: a Backlog task must have a
// non-empty description and must not carry any commitment or runtime field.
// It is a no-op for tasks in any other status. The backlog capture service
// (Group 2) calls this before persisting a new or updated Backlog item.
func ValidateBacklogTaskInvariants(task *Task) error {
	if task == nil {
		return fmt.Errorf("task is nil")
	}
	if task.CanonicalState() != TicketStateBacklog {
		return nil
	}
	if strings.TrimSpace(task.Description) == "" {
		return fmt.Errorf("backlog item requires a non-empty description")
	}
	if task.To != "" && !strings.EqualFold(task.To, "unassigned") {
		return fmt.Errorf("backlog item must not have an assignee")
	}
	if task.ScheduleEnabled || task.Schedule != nil {
		return fmt.Errorf("backlog item must not have a schedule")
	}
	if task.NextRun != nil {
		return fmt.Errorf("backlog item must not have a next-run time")
	}
	if task.StartedAt != nil || task.CurrentRunID != "" {
		return fmt.Errorf("backlog item must not have active execution state")
	}
	if task.Result != "" || task.ResultType != "" || task.Error != "" {
		return fmt.Errorf("backlog item must not have a runtime result")
	}
	return nil
}
