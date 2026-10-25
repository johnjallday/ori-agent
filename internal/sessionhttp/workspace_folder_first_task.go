package sessionhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// The folder the user showed the assistant gets one read-only first task when
// its workspace is set up. This file starts it, once, when the user presses
// Start first look (on Home's receipt or mission card, or on the workspace
// page's banner), so no model tokens are spent until the user asks.
//
// It is deliberately separate from the template setup task's auto-start: that
// trigger belongs to a blueprint's own `setup: true` help task and is silenced
// for any workspace whose blueprint declares a Setup Wizard. A workspace the
// setup journey created has such a wizard, so reusing that trigger would never
// start this task. The condition here is the right one for a first look at the
// folder: the wizard, if there is one, has finished.
const (
	folderFirstTaskTemplateID = personalassistant.FolderFirstTaskTemplateID
	// taskContextFolderFirstTaskConsumedAt is stamped, once, in the same store
	// update that decides to start the task. A failed start keeps it: the task
	// stays pending and manually startable, and is never retried automatically.
	taskContextFolderFirstTaskConsumedAt = personalassistant.FolderFirstTaskConsumedKey
)

// isFolderFirstTask reports whether a task is the starter task a shown folder's
// workspace was seeded with.
func isFolderFirstTask(task *agentworkspace.Task) bool {
	return personalassistant.IsFolderFirstTask(task)
}

// canonicalWorkspace reads the workspace's canonical record, which carries the
// Setup Wizard snapshot a plain primary-store read lacks. nil when unreadable.
func (h *Handler) canonicalWorkspace(workspaceID string) *agentworkspace.Workspace {
	if h == nil || strings.TrimSpace(workspaceID) == "" {
		return nil
	}
	for _, candidate := range []agentworkspace.Store{h.workspaceTaskStore, h.workspaceStore} {
		reader, ok := candidate.(folderWorkspaceReader)
		if !ok || isNilPointer(reader) {
			continue
		}
		if ws, err := reader.GetFolderWorkspace(workspaceID); err == nil && ws != nil {
			return ws
		}
	}
	return nil
}

// setupWizardOpening reports a blueprint Setup Wizard dialog that is about to
// open on this load: the one case where starting an agent would land on top of a
// deterministic setup dialog. It is the wizard's own auto-open rule (see
// setupwizard.Service.status): a wizard that is unfinished, never shown, not
// dismissed, and not a backfill. A wizard that is ready, regressed, already
// opened (the setup journey walks its mode step) or dismissed will not appear, so
// nothing is covered. A store that cannot read the canonical record answers
// "not opening", the stance workspaceHasSetupWizard takes, which keeps the
// pre-wizard behavior rather than silently disabling the first task for everyone.
func (h *Handler) setupWizardOpening(workspaceID string) bool {
	ws := h.canonicalWorkspace(workspaceID)
	if ws == nil || !ws.HasSetupWizard() || ws.IsSetupWizardReady() {
		return false
	}
	progress := ws.GetSetupWizardProgress()
	if progress == nil {
		return true // never shown: it opens on the first load
	}
	if agentworkspace.NormalizeSetupWizardState(progress.State) == agentworkspace.SetupWizardStateNeedsAttention {
		return false // a regression is an invitation to repair, never an ambush
	}
	return !progress.WasMigrated() && !progress.HasBeenOpened() && !progress.IsDismissed()
}

// folderFirstTaskStartsOnClick says whether Start first look would start the
// first task now. The receipt only promises that when it is true: the task has
// an agent, has not been started or consumed, and no setup dialog is still to
// open.
func (h *Handler) folderFirstTaskStartsOnClick(ws *agentworkspace.Workspace) bool {
	if ws == nil {
		return false
	}
	for i := range ws.Tasks {
		task := &ws.Tasks[i]
		if !isFolderFirstTask(task) {
			continue
		}
		if _, consumed := task.Context[taskContextFolderFirstTaskConsumedAt]; consumed {
			return false
		}
		assignee := strings.TrimSpace(task.To)
		if !waitsToStart(task) || assignee == "" || strings.EqualFold(assignee, "unassigned") {
			return false
		}
		return !h.setupWizardOpening(ws.ID) && h.firstTaskRecipeReason(task) == ""
	}
	return false
}

// firstTaskRecipeReason reports why a first task seeded under a host recipe
// could not be run within the recipe's restriction right now, or "" when it
// could (and for a task with no recipe). A task it refuses is never started
// some other way: the restriction is the reason the task exists in that form.
func (h *Handler) firstTaskRecipeReason(task *agentworkspace.Task) string {
	if task == nil || agentworkspace.FirstTaskRecipe(task) == "" {
		return ""
	}
	if h.firstTaskRecipeGate == nil {
		return personalassistant.FolderFirstTaskReasonRecipeUnavailable
	}
	return strings.TrimSpace(h.firstTaskRecipeGate(*task))
}

var errFolderFirstTaskNoChange = errors.New("no folder first task to start")

// handleFolderFirstTaskRead serves GET /api/workspaces/{id}/folder-first-task:
// where the workspace's first task stands, as Home's card reads it. The
// workspace page uses the same answer, so both say the same thing about the
// same task. It starts nothing and reads no file in any linked folder. A
// workspace with no first task answers with a null first_task.
func (h *Handler) handleFolderFirstTaskRead(w http.ResponseWriter, r *http.Request, workspaceID string) {
	if r.Method != http.MethodGet {
		_ = orihttp.RespondMethodNotAllowed(w)
		return
	}
	read := h.FolderFirstTaskView
	if h.firstTaskViewer != nil {
		read = h.firstTaskViewer
	}
	view, ok := read(r.Context(), workspaceID)
	if !ok {
		_ = orihttp.RespondSuccess(w, map[string]any{"first_task": nil})
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"first_task": view})
}

// handleFolderFirstTaskStart serves POST /api/workspaces/{id}/folder-first-task/start,
// the Start first look click for a shown folder's first task. The start itself
// is StartFolderFirstTask, which a folder setup also calls under its consent.
func (h *Handler) handleFolderFirstTaskStart(w http.ResponseWriter, r *http.Request, workspaceID string) {
	if r.Method != http.MethodPost {
		_ = orihttp.RespondMethodNotAllowed(w)
		return
	}
	var input struct {
		ExecutionDigest string `json:"execution_digest"`
		Retry           bool   `json:"retry,omitempty"`
	}
	if r.ContentLength != 0 {
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			_ = orihttp.RespondError(w, http.StatusBadRequest, "Invalid summary start disclosure")
			return
		}
	}
	start := h.startReviewedFolderFirstTask(r.Context(), workspaceID, input.ExecutionDigest, input.Retry)
	body := map[string]any{"success": true, "started": start.Started}
	if start.Reason != "" {
		body["reason"] = start.Reason
	}
	if start.TaskID != "" {
		body["task_id"] = start.TaskID
	}
	if start.Reason == "execution_changed" {
		read := h.FolderFirstTaskView
		if h.firstTaskViewer != nil {
			read = h.firstTaskViewer
		}
		if view, ok := read(r.Context(), workspaceID); ok {
			body["first_task"] = view
		}
	}
	_ = orihttp.RespondSuccess(w, body)
}

// FolderFirstTaskStart is how one start of a folder's first task went:
// started, or the reason it was not, with the task when there is one.
type FolderFirstTaskStart struct {
	Started bool
	Reason  string
	TaskID  string
}

// waitsToStart says the task has not begun: its ticket is Ready, assigned or
// not. A task stopped mid-run is put back there with its agent kept.
func waitsToStart(task *agentworkspace.Task) bool {
	return task.CanonicalState() == agentworkspace.TicketStateReady &&
		(task.Status == agentworkspace.TaskStatusPending || task.Status == agentworkspace.TaskStatusAssigned)
}

// StartFolderFirstTask starts a shown folder's first task, once. Inside one
// store update it finds the unconsumed task and stamps the marker, then starts
// the task through the same manual-execution path as pressing Start on it. It
// is idempotent across clicks, tabs and a setup that starts it under its
// consent. A task with no agent, or a setup wizard still open, is left seeded
// and unconsumed so a later click starts it.
func (h *Handler) StartFolderFirstTask(ctx context.Context, workspaceID string) FolderFirstTaskStart {
	// Trusted internal caller; browser requests must supply the disclosure they
	// actually saw. Setup never calls this compatibility entrypoint.
	expected := ""
	if view, ok := h.FolderFirstTaskView(ctx, workspaceID); ok && view.Executor != nil {
		expected = view.Executor.Digest
	}
	return h.startReviewedFolderFirstTask(ctx, workspaceID, expected, false)
}

func (h *Handler) startReviewedFolderFirstTask(ctx context.Context, workspaceID, expected string, retry bool) FolderFirstTaskStart {
	store := h.taskMutationStore()
	if store == nil {
		return FolderFirstTaskStart{Reason: "no_task_store"}
	}
	if err := agentworkspace.RequireWorkspaceExecution(ctx, store, workspaceID, true); err != nil {
		reason := "admission_unavailable"
		if errors.Is(err, agentworkspace.ErrWorkspaceExecutionInactive) {
			reason = "local_activation_required"
		}
		return FolderFirstTaskStart{Reason: reason}
	}
	if h.setupWizardOpening(workspaceID) {
		return FolderFirstTaskStart{Reason: "setup_wizard_opening"}
	}

	var (
		taskID      string
		candidateID string
		reason      = "no_first_task"
	)
	err := store.Update(workspaceID, func(ws *agentworkspace.Workspace) error {
		for i := range ws.Tasks {
			task := &ws.Tasks[i]
			if !isFolderFirstTask(task) {
				continue
			}
			candidateID = task.ID
			isRetry := retry && agentworkspace.FirstTaskRecipe(task) != "" && personalassistant.FolderFirstTaskStateOf(task) == personalassistant.FolderFirstTaskFailed
			if _, consumed := task.Context[taskContextFolderFirstTaskConsumedAt]; consumed && !isRetry {
				reason = "already_consumed"
				return errFolderFirstTaskNoChange
			}
			if !waitsToStart(task) && !isRetry {
				// Already started by hand (or finished): not this trigger's to run.
				reason = "not_pending"
				return errFolderFirstTaskNoChange
			}
			assignee := strings.TrimSpace(task.To)
			if assignee == "" || strings.EqualFold(assignee, "unassigned") {
				reason = "unassigned"
				return errFolderFirstTaskNoChange
			}
			if blocked := h.firstTaskRecipeReason(task); blocked != "" {
				// Left seeded and unspent: a later click starts it once what
				// stands in the way has been repaired.
				reason = blocked
				return errFolderFirstTaskNoChange
			}
			if h.firstTaskExecutor != nil && agentworkspace.FirstTaskRecipe(task) != "" {
				executor, blocked := h.firstTaskExecutor(*task)
				if executor == nil {
					reason = blocked
					if reason == "" {
						reason = personalassistant.FolderFirstTaskReasonRecipeUnavailable
					}
					return errFolderFirstTaskNoChange
				}
				if expected != executor.Digest {
					reason = "execution_changed"
					return errFolderFirstTaskNoChange
				}
				task.Context[agentworkspace.TaskContextReviewedExecutor] = executor.Digest
			}
			if isRetry {
				if err := task.SetStatus(agentworkspace.TaskStatusPending); err != nil {
					return err
				}
				agentworkspace.ResetTaskRuntimeKeepingSteps(task)
				delete(task.Context, personalassistant.FolderFirstTaskStartFailedKey)
				delete(task.Context, agentworkspace.TaskContextFirstTaskFailure)
			}
			task.Context[taskContextFolderFirstTaskConsumedAt] = time.Now().UTC().Format(time.RFC3339)
			taskID = task.ID
			return nil
		}
		return errFolderFirstTaskNoChange
	})
	if err != nil && !errors.Is(err, errFolderFirstTaskNoChange) {
		logger.Warn("Folder first task start sweep failed", logger.Fields{"workspace_id": workspaceID, "error": err})
		return FolderFirstTaskStart{Reason: "workspace_unavailable"}
	}
	if errors.Is(err, errFolderFirstTaskNoChange) || taskID == "" {
		return FolderFirstTaskStart{Reason: reason, TaskID: candidateID}
	}

	if h.firstTaskLinker != nil {
		if err := h.firstTaskLinker(ctx, workspaceID, taskID); err != nil {
			h.markFolderFirstTaskStartFailed(store, workspaceID, taskID)
			return FolderFirstTaskStart{Reason: "report_link_unavailable", TaskID: taskID}
		}
	}
	// The marker is committed: from here the outcome is started or
	// consumed-but-failed, never retried automatically.
	if h.templateSetupStarter == nil {
		logger.Warn("Folder first task consumed but no starter is wired", logger.Fields{"workspace_id": workspaceID, "task_id": taskID})
		h.markFolderFirstTaskStartFailed(store, workspaceID, taskID)
		return FolderFirstTaskStart{Reason: "execution_unavailable", TaskID: taskID}
	}
	if err := h.templateSetupStarter(workspaceID, taskID); err != nil {
		logger.Warn("Folder first task failed to start", logger.Fields{"workspace_id": workspaceID, "task_id": taskID, "error": err})
		h.markFolderFirstTaskStartFailed(store, workspaceID, taskID)
		return FolderFirstTaskStart{Reason: "start_failed", TaskID: taskID}
	}
	logger.Info("Folder first task started", logger.Fields{"workspace_id": workspaceID, "task_id": taskID})
	return FolderFirstTaskStart{Started: true, TaskID: taskID}
}

// handleFolderFirstTaskStop serves POST /api/workspaces/{id}/folder-first-task/stop:
// Stop on a running first look. The run is cancelled and the same task is put
// back to wait for Start first look; the workspace, its agent and the folder
// link stay as they are.
func (h *Handler) handleFolderFirstTaskStop(w http.ResponseWriter, r *http.Request, workspaceID string) {
	if r.Method != http.MethodPost {
		_ = orihttp.RespondMethodNotAllowed(w)
		return
	}
	stopped, reason, taskID := h.StopFolderFirstTask(r.Context(), workspaceID)
	body := map[string]any{"success": true, "stopped": stopped}
	if reason != "" {
		body["reason"] = reason
	}
	if taskID != "" {
		body["task_id"] = taskID
	}
	_ = orihttp.RespondSuccess(w, body)
}

// StopFolderFirstTask stops a running first look and makes it startable again:
// its run is cancelled and its ticket put back to Ready, then its one-start
// marker is cleared, so Start first look starts the same task once more. A look
// that is not running is left as it is.
func (h *Handler) StopFolderFirstTask(ctx context.Context, workspaceID string) (stopped bool, reason, taskID string) {
	store := h.taskMutationStore()
	if store == nil || h.firstTaskStopper == nil {
		return false, "stop_unavailable", ""
	}
	ws, err := store.Get(workspaceID)
	if err != nil || ws == nil {
		return false, "workspace_unavailable", ""
	}
	task := personalassistant.FindFolderFirstTask(ws)
	if task == nil {
		return false, "no_first_task", ""
	}
	taskID = task.ID
	if personalassistant.FolderFirstTaskStateOf(task) != personalassistant.FolderFirstTaskRunning {
		return false, "not_running", taskID
	}
	if err := h.firstTaskStopper(ctx, workspaceID, taskID); err != nil {
		logger.Warn("Folder first task could not be stopped", logger.Fields{"workspace_id": workspaceID, "task_id": taskID, "error": err})
		return false, "stop_failed", taskID
	}
	err = store.Update(workspaceID, func(ws *agentworkspace.Workspace) error {
		for i := range ws.Tasks {
			if ws.Tasks[i].ID == taskID && isFolderFirstTask(&ws.Tasks[i]) {
				delete(ws.Tasks[i].Context, taskContextFolderFirstTaskConsumedAt)
				delete(ws.Tasks[i].Context, personalassistant.FolderFirstTaskStartFailedKey)
				return nil
			}
		}
		return errFolderFirstTaskNoChange
	})
	if err != nil {
		logger.Warn("Stopped folder first task kept its start marker", logger.Fields{"workspace_id": workspaceID, "task_id": taskID, "error": err})
		return false, "stop_failed", taskID
	}
	logger.Info("Folder first task stopped", logger.Fields{"workspace_id": workspaceID, "task_id": taskID})
	return true, "", taskID
}

// markFolderFirstTaskStartFailed records that the spent start never began, so
// the look reads as "did not start" at once rather than as starting. It changes
// nothing about retries: the task stays pending and is run again by hand. A
// write that fails only costs that promptness, so it is logged and ignored.
func (h *Handler) markFolderFirstTaskStartFailed(store agentworkspace.Store, workspaceID, taskID string) {
	err := store.Update(workspaceID, func(ws *agentworkspace.Workspace) error {
		for i := range ws.Tasks {
			if ws.Tasks[i].ID == taskID && isFolderFirstTask(&ws.Tasks[i]) {
				ws.Tasks[i].Context[personalassistant.FolderFirstTaskStartFailedKey] = time.Now().UTC().Format(time.RFC3339)
				return nil
			}
		}
		return errFolderFirstTaskNoChange
	})
	if err != nil && !errors.Is(err, errFolderFirstTaskNoChange) {
		logger.Warn("Folder first task start failure was not recorded", logger.Fields{"workspace_id": workspaceID, "task_id": taskID, "error": err})
	}
}
