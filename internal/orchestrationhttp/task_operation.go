package orchestrationhttp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/assistantops"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// TaskOperationBinder binds one task create to the assistant operation whose
// reviewed form asked for it. The create handler stays the only writer of the
// task: the binder reserves the operation's single task before the write and
// records the outcome after it, so a repeated or retried save creates nothing
// more. It never creates, edits or starts a task itself.
type TaskOperationBinder interface {
	ClaimTaskCreate(ctx context.Context, req assistantops.TaskCreate) (assistantops.CreateClaim, error)
	ConfirmTaskCreate(ctx context.Context, operationID, taskID string) error
	ReleaseTaskCreate(ctx context.Context, operationID, taskID string) error
}

// SetTaskOperationBinder wires assistant-operation binding into task creation.
// Without it a create that names an operation is refused, never treated as an
// ordinary create.
func (h *Handler) SetTaskOperationBinder(binder TaskOperationBinder) {
	if h == nil {
		return
	}
	h.taskOperationBinder = binder
	if h.taskHandlerSub != nil {
		h.taskHandlerSub.SetTaskOperationBinder(binder)
	}
}

// SetTaskOperationBinder is initialization-only.
func (th *TaskHandler) SetTaskOperationBinder(binder TaskOperationBinder) {
	if th != nil {
		th.operationBinder = binder
	}
}

// taskOperationSettleTimeout bounds recording a create's outcome after the
// request that made it has gone away.
const taskOperationSettleTimeout = 5 * time.Second

// taskOperationBinding is one create's reservation. finish records what the
// handler did with it exactly once: a written task is confirmed, anything
// else gives the reservation back.
type taskOperationBinding struct {
	binder      TaskOperationBinder
	operationID string
	taskID      string
	written     bool
	finished    bool
}

func (b *taskOperationBinding) finish(ctx context.Context) {
	if b == nil || b.finished {
		return
	}
	b.finished = true
	// The outcome is recorded even if the browser hung up: the task either
	// exists or it does not, whoever is still listening.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), taskOperationSettleTimeout)
	defer cancel()
	var err error
	if b.written {
		err = b.binder.ConfirmTaskCreate(ctx, b.operationID, b.taskID)
	} else {
		err = b.binder.ReleaseTaskCreate(ctx, b.operationID, b.taskID)
	}
	if err != nil {
		// Not fatal: the operation settles itself from the task on its next read.
		logger.Warn("Assistant operation create outcome not recorded", logger.Fields{
			"operation_id": b.operationID, "task_id": b.taskID, "written": b.written, "error": err,
		})
	}
}

// bindTaskOperation reserves the operation's task for this create. It returns
// the reservation, or writes the response itself and returns ok=false: a
// refusal, or the task an earlier request already created for the operation.
// plain is false when the request asks for more than one ordinary task.
func (th *TaskHandler) bindTaskOperation(w http.ResponseWriter, r *http.Request, task *workspace.Task, operationID string, draftRevision int, plain bool) (*taskOperationBinding, bool) {
	if th.operationBinder == nil {
		respondTaskOperationError(w, assistantops.ErrUnavailable)
		return nil, false
	}
	if !plain {
		// A prepared task is one ordinary task. A schedule, a parent or inputs
		// make it something the assistant did not prepare and nobody reviewed
		// as such; the form saves those without naming an operation.
		w.Header().Set("Cache-Control", "no-store")
		_ = orihttp.RespondAPIError(w, http.StatusBadRequest, orihttp.NewAPIError("operation_not_plain",
			"A task prepared by the assistant cannot carry a schedule, a parent or input tasks."))
		return nil, false
	}
	claim, err := th.operationBinder.ClaimTaskCreate(r.Context(), assistantops.TaskCreate{
		OperationID: operationID, DraftRevision: draftRevision, WorkspaceID: task.WorkspaceID,
		AgentName: task.To, AgentNodeID: task.AssignedNodeID, TaskID: task.ID,
	})
	if err != nil {
		respondTaskOperationError(w, err)
		return nil, false
	}
	if claim.Existing {
		th.respondExistingOperationTask(w, task.WorkspaceID, claim.TaskID)
		return nil, false
	}
	// The assignee is the workspace's own instance exactly as the binder
	// resolved it, so the entry-agent default and the auto-add below have
	// nothing to do for this task.
	task.To, task.AssignedNodeID = claim.AgentName, claim.AgentNodeID
	task.AssistantOperationID = operationID
	return &taskOperationBinding{binder: th.operationBinder, operationID: operationID, taskID: task.ID}, true
}

// respondExistingOperationTask answers a repeated save with the task the
// operation already created. Nothing is written.
func (th *TaskHandler) respondExistingOperationTask(w http.ResponseWriter, workspaceID, taskID string) {
	w.Header().Set("Cache-Control", "no-store")
	ws, err := th.workspaceStore.Get(workspaceID)
	if err == nil && ws != nil {
		if existing, taskErr := ws.GetTask(taskID); taskErr == nil && existing != nil {
			orihttp.WriteJSON(w, map[string]any{"success": true, "task": existing, "already_created": true})
			return
		}
	}
	_ = orihttp.RespondAPIError(w, http.StatusConflict, orihttp.NewAPIError("operation_ended",
		"This request already created its task, and that task is not available any more."))
}

// plainTaskCreate reports whether a create asks for exactly one ordinary task.
func plainTaskCreate(scheduled bool, parentTaskID string, inputTaskIDs []string) bool {
	return !scheduled && strings.TrimSpace(parentTaskID) == "" && len(inputTaskIDs) == 0
}

// respondTaskOperationError words a refused binding for the task form. The
// codes match the operation endpoints'; nothing was written in any of them.
func respondTaskOperationError(w http.ResponseWriter, err error) {
	status, code, message := http.StatusServiceUnavailable, "operation_unavailable",
		"Ori could not check the assistant's request right now. Nothing was saved. Try again."
	switch {
	case errors.Is(err, assistantops.ErrAssignee):
		status, code, message = http.StatusConflict, "operation_assignee",
			"That agent is not in this workspace. Choose an agent from this workspace, then save again."
	case errors.Is(err, assistantops.ErrCreateInFlight):
		status, code, message = http.StatusConflict, "operation_create_in_flight",
			"This task is already being saved. Give it a moment."
	case errors.Is(err, assistantops.ErrNotFound), errors.Is(err, assistantops.ErrForbidden):
		status, code, message = http.StatusNotFound, "operation_not_found",
			"The assistant's request is not available any more. Nothing was saved."
	case errors.Is(err, assistantops.ErrExpired):
		status, code, message = http.StatusGone, "operation_expired",
			"The assistant's request ran out of time. Nothing was saved."
	case errors.Is(err, assistantops.ErrInvalidated):
		status, code, message = http.StatusGone, "operation_invalidated",
			"The assistant's request is no longer valid because the assistant, conversation or workspace changed. Nothing was saved."
	case errors.Is(err, assistantops.ErrTransition):
		status, code, message = http.StatusConflict, "operation_ended",
			"The assistant's request has ended. Nothing was saved."
	case errors.Is(err, assistantops.ErrConflict):
		status, code, message = http.StatusConflict, "operation_conflict",
			"The prepared task changed. Open it again from the assistant."
	case errors.Is(err, assistantops.ErrInvalidCommand), errors.Is(err, assistantops.ErrBounds):
		status, code, message = http.StatusBadRequest, "operation_invalid",
			"This task does not match what the assistant prepared. Nothing was saved."
	}
	w.Header().Set("Cache-Control", "no-store")
	_ = orihttp.RespondAPIError(w, status, orihttp.NewAPIError(code, message))
}

// OperationTaskHost is the canonical task owner as assistant operations see
// it: what a task says about itself, and its first start.
func (h *Handler) OperationTaskHost() assistantops.TaskHost {
	if h == nil {
		return nil
	}
	return operationTaskHost{handler: h}
}

// operationTaskHost resolves the task sub-handler on each call, because the
// server may wire operations before that sub-handler exists.
type operationTaskHost struct{ handler *Handler }

func (o operationTaskHost) Task(_ context.Context, workspaceID, taskID string) (assistantops.TaskState, error) {
	th := o.handler.taskHandlerSub
	if th == nil || th.workspaceStore == nil {
		return assistantops.TaskState{}, errors.New("task store unavailable")
	}
	ws, err := th.workspaceStore.Get(workspaceID)
	if err != nil || ws == nil {
		// Not definitive: the operation's own checks decide whether the
		// workspace is gone.
		return assistantops.TaskState{}, errors.New("workspace could not be read")
	}
	task, err := ws.GetTask(taskID)
	if err != nil || task == nil {
		return assistantops.TaskState{}, fmt.Errorf("%w: %s", assistantops.ErrTaskMissing, taskID)
	}
	return operationTaskState(task), nil
}

func (o operationTaskHost) Start(ctx context.Context, workspaceID, taskID string) error {
	th := o.handler.taskHandlerSub
	if th == nil {
		return &assistantops.StartRefusal{Reason: "Task execution is not available on this server."}
	}
	return th.StartReviewedTask(ctx, workspaceID, taskID)
}

// operationTaskState reads from a task everything an operation may know about
// it. "Ran" is any sign of execution the task itself carries; the first run is
// the oldest recorded one, or the current one while nothing is recorded yet.
func operationTaskState(task *workspace.Task) assistantops.TaskState {
	state := assistantops.TaskState{
		TaskID: task.ID, OperationID: strings.TrimSpace(task.AssistantOperationID),
		AgentName: strings.TrimSpace(task.To), AgentNodeID: strings.TrimSpace(task.AssignedNodeID),
		Status: string(task.Status),
	}
	switch task.Status {
	case workspace.TaskStatusInProgress, workspace.TaskStatusWaitingForChoice, workspace.TaskStatusCompleted,
		workspace.TaskStatusFailed, workspace.TaskStatusTimeout:
		state.Ran = true
	}
	if strings.TrimSpace(task.CurrentRunID) != "" || task.StartedAt != nil || task.LastRun != nil ||
		task.ExecutionCount > 0 || len(task.ExecutionHistory) > 0 {
		state.Ran = true
	}
	for _, execution := range task.ExecutionHistory {
		if runID := strings.TrimSpace(execution.RunID); runID != "" {
			state.RunID = runID
			break
		}
	}
	if state.RunID == "" {
		state.RunID = strings.TrimSpace(task.CurrentRunID)
	}
	state.Startable = !state.Ran && (task.Status == workspace.TaskStatusPending || task.Status == workspace.TaskStatusAssigned)

	// What a result is attributed from: the task's own execution records. The
	// record list is already capped by the task; a count above its length
	// means older records were dropped.
	state.Active = task.Status == workspace.TaskStatusInProgress
	state.Waiting = task.Status == workspace.TaskStatusWaitingForChoice
	state.HasResult = strings.TrimSpace(task.Result) != ""
	state.Runs = make([]assistantops.TaskRun, 0, len(task.ExecutionHistory))
	for _, execution := range task.ExecutionHistory {
		state.Runs = append(state.Runs, assistantops.TaskRun{
			At: execution.ExecutedAt, Status: strings.ToLower(strings.TrimSpace(execution.Status)),
			HasResult: strings.TrimSpace(execution.Result) != "",
		})
	}
	// Which execution was the operation's own start is the task's own mark,
	// written by StartReviewedTask and by the execution recorder.
	if run := task.AssistantOperationRun; run != nil {
		state.Launched, state.LaunchPending = true, run.Pending
		if run.ExecutedAt != nil {
			state.LaunchedRunAt = *run.ExecutedAt
		}
	}
	return state
}

// unmarkOperationRun removes a launch mark that was written for a start which
// then did not launch. It re-reads the task inside the store's own update, so
// it changes nothing else about it.
func (th *TaskHandler) unmarkOperationRun(workspaceID, taskID string) {
	err := workspace.CanonicalUpdate(th.workspaceStore, workspaceID, func(fresh *workspace.Workspace) error {
		task, err := fresh.GetTask(taskID)
		if err != nil {
			return err
		}
		if task.AssistantOperationRun == nil || !task.AssistantOperationRun.Pending {
			return nil
		}
		task.AssistantOperationRun = nil
		return fresh.UpdateTask(*task)
	})
	if err != nil {
		logger.Warn("Could not clear an assistant operation launch mark", logger.Fields{"task_id": taskID, "error": err})
	}
}

// StartReviewedTask is a task's first start at its owner's explicit request.
// It runs through the same execution path as the manual execute endpoint:
// reset admission, manual workspace admission, the backlog rule, the default
// execution mode and executeTaskWithDependencies with its capability gate.
// Unlike that endpoint it never resets a finished task and never runs a task
// that already ran, so a repeated or late call cannot become a rerun. A
// refusal is an *assistantops.StartRefusal worded for the user.
func (th *TaskHandler) StartReviewedTask(ctx context.Context, workspaceID, taskID string) error {
	if th == nil || th.taskHandler == nil {
		return &assistantops.StartRefusal{Reason: "Task execution is not available on this server."}
	}
	release, err := th.admissionGate.Enter()
	if err != nil {
		return &assistantops.StartRefusal{Reason: "Ori is being reset, so nothing can start right now."}
	}
	defer release()
	if err := workspace.RequireWorkspaceExecution(ctx, th.workspaceStore, workspaceID, false); err != nil {
		if errors.Is(err, workspace.ErrWorkspaceExecutionInactive) {
			return &assistantops.StartRefusal{Reason: "This workspace is not set up to run tasks on this computer yet."}
		}
		return err
	}
	ws, err := th.workspaceStore.Get(workspaceID)
	if err != nil {
		return fmt.Errorf("workspace %s not found: %w", workspaceID, err)
	}
	task, err := ws.GetTask(taskID)
	if err != nil {
		return fmt.Errorf("%w: %s", assistantops.ErrTaskMissing, taskID)
	}
	if err := workspace.RequireTaskNotBacklog(task, "cannot start task"); err != nil {
		return &assistantops.StartRefusal{Reason: "The task is in the backlog. Move it out of the backlog before starting it."}
	}
	state := operationTaskState(task)
	switch {
	case state.Ran:
		return &assistantops.StartRefusal{Reason: "The task has already run. Run it again from the task itself."}
	case !state.Startable:
		return &assistantops.StartRefusal{Reason: "The task is " + strings.ReplaceAll(state.Status, "_", " ") + " and cannot be started."}
	case state.AgentName == "" || strings.EqualFold(state.AgentName, "unassigned"):
		return &assistantops.StartRefusal{Reason: "Assign the task to an agent before starting it."}
	case len(ws.GetSubtasks(task.ID)) > 0:
		return &assistantops.StartRefusal{Reason: "This task has steps of its own. Start it from the task."}
	}
	if task.ExecutionMode == "" {
		task.ExecutionMode = workspace.TaskExecutionModeAuto
	}
	// Mark the task before the work is launched: the execution this start
	// records is the operation's own, and the recorder binds the two in the
	// same write. A later run from the task's controls is never marked.
	workspace.MarkOperationRunLaunched(task)
	if err := ws.UpdateTask(*task); err != nil {
		return fmt.Errorf("failed to mark task start: %w", err)
	}
	if err := th.persistTasks(ws.ID, *task); err != nil {
		return fmt.Errorf("failed to save workspace: %w", err)
	}
	if err := th.startTaskWork(func() {
		ws, err := th.workspaceStore.Get(workspaceID)
		if err != nil {
			logger.Error("Failed to reload workspace for reviewed task start", logger.Fields{"workspace_id": workspaceID, "error": err})
			return
		}
		task, err := ws.GetTask(taskID)
		if err != nil {
			logger.Error("Task not found for reviewed start", logger.Fields{"task_id": taskID, "error": err})
			return
		}
		if _, err := th.executeTaskWithDependencies(ws, task); err != nil {
			var blockedErr *workspace.TaskBlockedError
			if errors.As(err, &blockedErr) {
				return
			}
			logger.Error("Reviewed task execution failed", logger.Fields{"task_id": taskID, "error": err})
		}
	}); err != nil {
		// Nothing was launched, so nothing may later be recorded as this
		// start's: take the mark back before answering.
		th.unmarkOperationRun(workspaceID, taskID)
		return &assistantops.StartRefusal{Reason: "Ori is being reset, so nothing can start right now."}
	}
	logger.Info("Started reviewed task", logger.Fields{"task_id": taskID, "workspace_id": workspaceID})
	return nil
}
