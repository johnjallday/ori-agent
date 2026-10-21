package orchestrationhttp

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// ErrTaskNotStoppable means the task is neither running nor stopped by a run
// that has just ended, so there is nothing for Stop to put back.
var ErrTaskNotStoppable = errors.New("the task is not running")

// stopRunWait bounds how long Stop waits for a cancelled run to record its
// own end before the ticket is put back. A run that outlives it is left to
// finish its write; the ticket is reopened from whatever it recorded.
const stopRunWait = 20 * time.Second

// isRunning reports whether a run of the task is registered in this process.
func (th *TaskHandler) isRunning(taskID string) bool {
	th.runningMu.Lock()
	defer th.runningMu.Unlock()
	_, running := th.runningCancels[strings.TrimSpace(taskID)]
	return running
}

// StopTaskToReady stops a task's run without closing the task: the run is
// cancelled, and once it has recorded its end the ticket goes back to Ready
// (the workflow's "stop work without cancelling"), with what the cancelled
// run left behind cleared, so the same task can be started again. It is the
// Stop on a folder's first look; nothing else about the workspace changes.
func (th *TaskHandler) StopTaskToReady(ctx context.Context, workspaceID, taskID string) error {
	taskID = strings.TrimSpace(taskID)
	if th == nil || th.workspaceStore == nil || taskID == "" {
		return ErrTaskNotStoppable
	}
	cancelled := th.cancelRunningTask(taskID)
	if cancelled {
		// The run writes its own Cancelled record when it sees the cancel and
		// unregisters only after that write; reopening before it lands would be
		// overwritten by it.
		deadline := time.NewTimer(stopRunWait)
		defer deadline.Stop()
		tick := time.NewTicker(50 * time.Millisecond)
		defer tick.Stop()
		for th.isRunning(taskID) {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-deadline.C:
				return ErrTaskNotStoppable
			case <-tick.C:
			}
		}
	}
	return workspace.CanonicalUpdate(th.workspaceStore, workspaceID, func(ws *workspace.Workspace) error {
		for i := range ws.Tasks {
			task := &ws.Tasks[i]
			if task.ID != taskID {
				continue
			}
			// A task recorded as running with no run in this process (a restart
			// lost it) is put back the same way.
			switch {
			case task.CanonicalState() == workspace.TicketStateReady:
				return nil
			case task.Status == workspace.TaskStatusCancelled, task.CanonicalState() == workspace.TicketStateInProgress:
			default:
				return ErrTaskNotStoppable
			}
			if err := task.TransitionTicket(workspace.TicketStateReady, workspace.TicketStateChange{
				Actor: workspace.TicketActorUser, Reason: "stopped",
			}); err != nil {
				return err
			}
			task.Error, task.Result = "", ""
			workspace.ApplyTaskResultMetadata(task, "")
			return nil
		}
		return ErrTaskNotStoppable
	})
}
