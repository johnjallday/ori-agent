package orchestrationhttp

import (
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspaceplan"
)

// Task-result promotion stays its own explicit action (FR-93).
//
// It turns one task's structured task-list result into subtasks. That is
// useful and it is NOT plan approval: no version is snapshotted, no approval is
// consumed, and the work it creates must never look like work an approved Plan
// authorized. Otherwise promotion becomes a way to manufacture tasks that read
// as approved without anyone approving anything.

func promotableTask() *workspace.Task {
	return &workspace.Task{
		ID:          "task-1",
		WorkspaceID: "ws-1",
		To:          "builder",
		Description: "Draft the migration steps",
		Status:      workspace.TaskStatusCompleted,
		Priority:    2,
	}
}

func promotableList() *workspace.TaskListResult {
	return &workspace.TaskListResult{
		ParentTitle: "Migration steps",
		Groups: []workspace.TaskListResultGroup{{
			Title: "Prepare",
			Items: []workspace.TaskListResultItem{
				{Title: "Snapshot staging"},
				{Title: "Verify checksums"},
			},
		}},
	}
}

// Promoted work carries promotion provenance and NOT plan provenance. A task
// stamped with workspace_plan would be read everywhere else as approved plan
// work — by progress derivation, by the reverse lookup, by the audit trail.
func TestPromotedTasksCarryNoPlanProvenance(t *testing.T) {
	ws := &workspace.Workspace{ID: "ws-1"}
	if err := ws.AddAgent("builder"); err != nil {
		t.Fatalf("add agent: %v", err)
	}

	handler := &TaskHandler{}
	parent, subtasks, err := handler.createTasksFromTaskListResult(ws, promotableTask(), promotableList())
	if err != nil {
		t.Fatalf("promote: %v", err)
	}

	for _, task := range append([]workspace.Task{*parent}, subtasks...) {
		if _, claimed := task.Context[workspaceplan.PlanProvenanceContextKey]; claimed {
			t.Errorf("promoted task %q claims plan provenance", task.Description)
		}
	}

	// It does record where it came from, so the relationship is not lost.
	if parent.Context["promoted_from_task_id"] != "task-1" {
		t.Errorf("parent does not record its source task: %#v", parent.Context)
	}
}

// Promotion creates ordinary pending tasks. Nothing here may mark work as
// approved, because no approval was consumed to produce it.
func TestPromotedTasksAreOrdinaryPendingWork(t *testing.T) {
	ws := &workspace.Workspace{ID: "ws-1"}
	if err := ws.AddAgent("builder"); err != nil {
		t.Fatalf("add agent: %v", err)
	}

	handler := &TaskHandler{}
	parent, subtasks, err := handler.createTasksFromTaskListResult(ws, promotableTask(), promotableList())
	if err != nil {
		t.Fatalf("promote: %v", err)
	}

	if len(subtasks) != 2 {
		t.Fatalf("subtasks = %d, want 2", len(subtasks))
	}
	for _, task := range append([]workspace.Task{*parent}, subtasks...) {
		if task.Status != workspace.TaskStatusPending {
			t.Errorf("promoted task %q status = %q, want pending", task.Description, task.Status)
		}
		// AssignmentReason is where the plan compiler records "created from
		// approved plan version N". Promotion must not borrow that sentence.
		if task.AssignmentReason != "" {
			t.Errorf("promoted task %q claims an assignment reason: %q",
				task.Description, task.AssignmentReason)
		}
	}
}

// An invalid task list is refused rather than partially promoted, so a
// malformed result cannot leave half a subtask tree behind.
func TestAnInvalidTaskListIsRefused(t *testing.T) {
	if err := workspace.ValidateTaskListResult(&workspace.TaskListResult{}); err == nil {
		t.Error("an empty task list was accepted")
	}
	if err := workspace.ValidateTaskListResult(promotableList()); err != nil {
		t.Errorf("a valid task list was refused: %v", err)
	}
}
