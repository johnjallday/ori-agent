package economy

import (
	"strings"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// WorkspaceTasks reads Farms out of the workspace store.
//
// It is deliberately the only place in this package that touches a workspace,
// and it only ever reads: the economy prices a schedule change, it never makes
// one. The store's own Save path stays the single writer of task state.
type WorkspaceTasks struct {
	store workspace.Store
}

// NewWorkspaceTasks builds the workspace-backed task source.
func NewWorkspaceTasks(store workspace.Store) *WorkspaceTasks {
	return &WorkspaceTasks{store: store}
}

// Farms lists every task that is a Farm right now, across every active
// workspace.
func (w *WorkspaceTasks) Farms() ([]FarmTask, error) {
	farms := []FarmTask{}
	err := w.eachActiveWorkspace(func(ws *workspace.Workspace) {
		for i := range ws.Tasks {
			task := &ws.Tasks[i]
			if !IsFarm(task.Schedule, task.ScheduleEnabled) {
				continue
			}
			farms = append(farms, farmTaskFrom(ws.ID, task))
		}
	})
	if err != nil {
		return nil, err
	}
	return farms, nil
}

// eachActiveWorkspace visits every active workspace as a HYDRATED record.
//
// ListActive is allowed to return lean records — the folder store's cache holds
// metadata only, so the listed copies can arrive with no Tasks at all — and a
// Farm listing built straight off that list silently reports that nobody has
// any Farms. Re-Getting each candidate reads the full workspace.json, which is
// the same pattern ResolveEmailOpsWorkspace uses for the same reason.
func (w *WorkspaceTasks) eachActiveWorkspace(visit func(*workspace.Workspace)) error {
	if w == nil || w.store == nil {
		return nil
	}
	active, err := w.store.ListActive()
	if err != nil {
		return err
	}
	for _, lean := range active {
		if lean == nil {
			continue
		}
		ws, err := w.store.Get(lean.ID)
		if err != nil || ws == nil {
			// One unreadable workspace costs that workspace its Farms, not the
			// whole listing.
			continue
		}
		visit(ws)
	}
	return nil
}

// Task returns one task's economy-relevant fields. It is the "before" side of
// every price check: what cadence is this task on today.
func (w *WorkspaceTasks) Task(workspaceID, taskID string) (FarmTask, bool) {
	if w == nil || w.store == nil {
		return FarmTask{}, false
	}
	workspaceID = strings.TrimSpace(workspaceID)
	taskID = strings.TrimSpace(taskID)
	if workspaceID == "" || taskID == "" {
		return FarmTask{}, false
	}
	ws, err := w.store.Get(workspaceID)
	if err != nil || ws == nil {
		return FarmTask{}, false
	}
	task, err := ws.GetTask(taskID)
	if err != nil || task == nil {
		return FarmTask{}, false
	}
	return farmTaskFrom(ws.ID, task), true
}

// CountCompletedTasks counts finished tasks across every active workspace, for
// the one-time backfill grant (FR32).
func (w *WorkspaceTasks) CountCompletedTasks() (int64, error) {
	var completed int64
	err := w.eachActiveWorkspace(func(ws *workspace.Workspace) {
		for i := range ws.Tasks {
			if ws.Tasks[i].Status == workspace.TaskStatusCompleted {
				completed++
			}
		}
	})
	if err != nil {
		return 0, err
	}
	return completed, nil
}

func farmTaskFrom(workspaceID string, task *workspace.Task) FarmTask {
	return FarmTask{
		WorkspaceID:     workspaceID,
		TaskID:          task.ID,
		Name:            taskDisplayName(task),
		Schedule:        task.Schedule,
		ScheduleEnabled: task.ScheduleEnabled,
		LastSummary:     lastExecutionSummary(task),
	}
}

// taskDisplayName prefers the schedule's own name, which is what the user typed
// when they set the cadence up, and falls back to the task description.
func taskDisplayName(task *workspace.Task) string {
	if name := strings.TrimSpace(task.ScheduleName); name != "" {
		return name
	}
	return strings.TrimSpace(task.Description)
}

// lastExecutionSummary is the most recent run's summary, or empty when the task
// has never run.
func lastExecutionSummary(task *workspace.Task) string {
	if len(task.ExecutionHistory) == 0 {
		return ""
	}
	return strings.TrimSpace(task.ExecutionHistory[len(task.ExecutionHistory)-1].Summary)
}
