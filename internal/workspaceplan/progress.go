package workspaceplan

import (
	"context"
	"fmt"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Plan progress is DERIVED, every time it is read.
//
// The Plan stores which Tasks it created; the Tasks store how that work is
// going. Keeping a copy of the second on the Plan would create two answers to
// the same question, and the copy would be the stale one (FR-11, FR-12).
//
// So this file reads the linked Tasks and computes the counts. Nothing here
// writes anything.

// TaskReader reads the workspace whose Tasks a Plan links to.
type TaskReader interface {
	Get(id string) (*workspace.Workspace, error)
}

// TaskProgressSource derives Plan progress from live Task state.
type TaskProgressSource struct {
	tasks TaskReader
	// slots reports whether a Plan is waiting for the workspace execution
	// slot. It is optional until group 6 introduces the lease; without it a
	// Plan simply never reports as waiting (FR-107).
	slots SlotReporter
}

// SlotReporter answers whether a Plan is queued behind another Plan for the
// workspace's single execution slot.
type SlotReporter interface {
	WaitingForSlot(ctx context.Context, workspaceID, planID string) (bool, error)
}

// NewTaskProgressSource returns a progress source over the workspace store.
func NewTaskProgressSource(tasks TaskReader, opts ...ProgressOption) *TaskProgressSource {
	source := &TaskProgressSource{tasks: tasks}
	for _, opt := range opts {
		opt(source)
	}
	return source
}

// ProgressOption configures a TaskProgressSource.
type ProgressOption func(*TaskProgressSource)

// WithSlotReporter attaches the execution-slot lookup.
func WithSlotReporter(slots SlotReporter) ProgressOption {
	return func(s *TaskProgressSource) { s.slots = slots }
}

var _ ProgressSource = (*TaskProgressSource)(nil)

// PlanProgress computes the Plan's progress from its linked Tasks.
func (s *TaskProgressSource) PlanProgress(ctx context.Context, plan *Plan) (Progress, error) {
	if plan == nil || s.tasks == nil {
		return Progress{}, nil
	}
	ws, err := s.tasks.Get(plan.WorkspaceID)
	if err != nil {
		return Progress{}, err
	}
	if ws == nil {
		return Progress{}, fmt.Errorf("%w: %s", ErrWorkspaceNotFound, plan.WorkspaceID)
	}

	progress := DeriveProgress(plan, ws.Tasks)

	if s.slots != nil {
		waiting, err := s.slots.WaitingForSlot(ctx, plan.WorkspaceID, plan.ID)
		if err == nil && waiting {
			// Waiting for the slot is counted separately from blocked: the
			// work is ready, it is the workspace that is busy (FR-107).
			progress.WaitingForSlot = progress.Ready
			progress.Ready = 0
		}
	}
	return progress, nil
}

// DeriveProgress computes the counts from a Plan's links and the workspace's
// Tasks. It is a pure function so the derivation can be tested directly.
//
// Only item-level Tasks are counted. A group Task is a container for its items;
// counting both would report a plan with two tasks as having three.
func DeriveProgress(plan *Plan, tasks []workspace.Task) Progress {
	byID := make(map[string]workspace.Task, len(tasks))
	for _, task := range tasks {
		byID[task.ID] = task
	}

	var progress Progress
	for _, link := range plan.TaskLinks {
		// Retired links belong to work a corrective revision replaced; they
		// are history, not outstanding work (FR-77).
		if link.RetiredAt != nil {
			continue
		}
		if link.Role == LinkRoleGroup {
			continue
		}
		task, exists := byID[link.TaskID]
		if !exists {
			// A link whose Task is gone is not silently dropped from the
			// total: the Plan committed to that work, and its absence is a
			// discrepancy worth surfacing rather than hiding.
			progress.Total++
			progress.Blocked++
			continue
		}

		progress.Total++
		switch classifyTask(task, byID) {
		case taskCompleted:
			progress.Completed++
		case taskRunning:
			progress.Running++
		case taskFailed:
			progress.Failed++
		case taskBlocked:
			progress.Blocked++
		default:
			progress.Ready++
		}
	}

	progress.Remaining = max(progress.Total-progress.Completed-progress.Failed, 0)
	return progress
}

type taskState int

const (
	taskReady taskState = iota
	taskRunning
	taskBlocked
	taskCompleted
	taskFailed
)

// classifyTask decides what one Task contributes to Plan progress.
//
// "Ready" and "blocked" are computed from the Task's own dependencies rather
// than from its status, because a pending Task whose input has not finished is
// not waiting to be picked up — it cannot run yet, and reporting it as ready
// would overstate what the user can start.
func classifyTask(task workspace.Task, byID map[string]workspace.Task) taskState {
	switch task.Status {
	case workspace.TaskStatusCompleted:
		return taskCompleted
	case workspace.TaskStatusFailed, workspace.TaskStatusTimeout:
		return taskFailed
	case workspace.TaskStatusCancelled:
		// A cancelled task is finished, not outstanding. It is counted as
		// completed-with-nothing rather than failed, because nobody needs to
		// act on it.
		return taskCompleted
	case workspace.TaskStatusInProgress, workspace.TaskStatusWaitingForChoice:
		return taskRunning
	}

	if !DependenciesSatisfied(task, byID) {
		return taskBlocked
	}
	return taskReady
}

// DependenciesSatisfied reports whether every Task this one waits on has
// finished successfully.
//
// A failed or cancelled predecessor does NOT satisfy a dependency. Dispatching
// work whose input never produced a result would run it against missing data,
// which is worse than leaving it blocked and visible (FR-104).
func DependenciesSatisfied(task workspace.Task, byID map[string]workspace.Task) bool {
	for _, inputID := range task.InputTaskIDs {
		input, exists := byID[inputID]
		if !exists {
			return false
		}
		if input.Status != workspace.TaskStatusCompleted {
			return false
		}
	}
	// A child task also waits on its parent's other constraints only through
	// its own inputs; the parent/child relationship is structural, not a
	// dependency, so it is deliberately not treated as one here.
	return true
}

// EligibleTasks returns the Plan's item Tasks that could start right now, in
// Plan order.
//
// Order matters: step-through dispatches the first of these, and a user
// stepping through a plan expects it to follow the order they approved.
func EligibleTasks(plan *Plan, tasks []workspace.Task) []workspace.Task {
	byID := make(map[string]workspace.Task, len(tasks))
	for _, task := range tasks {
		byID[task.ID] = task
	}

	var eligible []workspace.Task
	for _, link := range plan.TaskLinks {
		if link.RetiredAt != nil || link.Role == LinkRoleGroup {
			continue
		}
		task, exists := byID[link.TaskID]
		if !exists {
			continue
		}
		if classifyTask(task, byID) == taskReady {
			eligible = append(eligible, task)
		}
	}
	return eligible
}

// FailedTasks returns the Plan's item Tasks that ended in failure, which is
// what a paused Plan needs to explain itself (FR-113).
func FailedTasks(plan *Plan, tasks []workspace.Task) []workspace.Task {
	byID := make(map[string]workspace.Task, len(tasks))
	for _, task := range tasks {
		byID[task.ID] = task
	}

	var failed []workspace.Task
	for _, link := range plan.TaskLinks {
		if link.RetiredAt != nil || link.Role == LinkRoleGroup {
			continue
		}
		if task, exists := byID[link.TaskID]; exists && classifyTask(task, byID) == taskFailed {
			failed = append(failed, task)
		}
	}
	return failed
}

// RunningTasks returns the Plan's Tasks that are currently executing, which is
// what a pause has to wait for before the Plan can safely stop (FR-108).
func RunningTasks(plan *Plan, tasks []workspace.Task) []workspace.Task {
	byID := make(map[string]workspace.Task, len(tasks))
	for _, task := range tasks {
		byID[task.ID] = task
	}

	var running []workspace.Task
	for _, link := range plan.TaskLinks {
		if link.RetiredAt != nil || link.Role == LinkRoleGroup {
			continue
		}
		if task, exists := byID[link.TaskID]; exists && classifyTask(task, byID) == taskRunning {
			running = append(running, task)
		}
	}
	return running
}
