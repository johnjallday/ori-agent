package sessionhttp

import (
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
// its workspace is set up. This file starts it, once, the first time the
// workspace is opened, so no model tokens are spent until the user looks.
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

// folderFirstTaskAutoStarts says whether the first task will start by itself the
// next time the workspace is opened. The receipt only promises that when it is
// true: the task has an agent, has not been started or consumed, and no setup
// dialog is still open.
func (h *Handler) folderFirstTaskAutoStarts(ws *agentworkspace.Workspace) bool {
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
		if task.Status != agentworkspace.TaskStatusPending || assignee == "" || strings.EqualFold(assignee, "unassigned") {
			return false
		}
		return !h.setupWizardOpening(ws.ID)
	}
	return false
}

var errFolderFirstTaskNoChange = errors.New("no folder first task to start")

// handleFolderFirstTaskStart serves POST /api/workspaces/{id}/folder-first-task/start,
// the first-open trigger for a shown folder's first task. Inside one store update
// it finds the unconsumed task and stamps the marker, then starts the task
// through the same manual-execution path as pressing Start on it. It is
// idempotent across reloads and tabs. A task with no agent, or a setup wizard
// still open, is left seeded and unconsumed so a later open starts it.
func (h *Handler) handleFolderFirstTaskStart(w http.ResponseWriter, r *http.Request, workspaceID string) {
	if r.Method != http.MethodPost {
		_ = orihttp.RespondMethodNotAllowed(w)
		return
	}
	store := h.taskMutationStore()
	if store == nil {
		_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "started": false, "reason": "no_task_store"})
		return
	}
	if err := agentworkspace.RequireWorkspaceExecution(r.Context(), store, workspaceID, true); err != nil {
		reason := "admission_unavailable"
		if errors.Is(err, agentworkspace.ErrWorkspaceExecutionInactive) {
			reason = "local_activation_required"
		}
		_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "started": false, "reason": reason})
		return
	}
	if h.setupWizardOpening(workspaceID) {
		_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "started": false, "reason": "setup_wizard_opening"})
		return
	}

	var (
		taskID string
		reason = "no_first_task"
	)
	err := store.Update(workspaceID, func(ws *agentworkspace.Workspace) error {
		for i := range ws.Tasks {
			task := &ws.Tasks[i]
			if !isFolderFirstTask(task) {
				continue
			}
			if _, consumed := task.Context[taskContextFolderFirstTaskConsumedAt]; consumed {
				reason = "already_consumed"
				return errFolderFirstTaskNoChange
			}
			if task.Status != agentworkspace.TaskStatusPending {
				// Already started by hand (or finished): not this trigger's to run.
				reason = "not_pending"
				return errFolderFirstTaskNoChange
			}
			assignee := strings.TrimSpace(task.To)
			if assignee == "" || strings.EqualFold(assignee, "unassigned") {
				reason = "unassigned"
				return errFolderFirstTaskNoChange
			}
			task.Context[taskContextFolderFirstTaskConsumedAt] = time.Now().UTC().Format(time.RFC3339)
			taskID = task.ID
			return nil
		}
		return errFolderFirstTaskNoChange
	})
	if err != nil && !errors.Is(err, errFolderFirstTaskNoChange) {
		logger.Warn("Folder first task start sweep failed", logger.Fields{"workspace_id": workspaceID, "error": err})
		_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "started": false, "reason": "workspace_unavailable"})
		return
	}
	if taskID == "" {
		_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "started": false, "reason": reason})
		return
	}

	// The marker is committed: from here the outcome is started or
	// consumed-but-failed, never retried automatically.
	if h.templateSetupStarter == nil {
		logger.Warn("Folder first task consumed but no starter is wired", logger.Fields{"workspace_id": workspaceID, "task_id": taskID})
		_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "started": false, "reason": "execution_unavailable", "task_id": taskID})
		return
	}
	if err := h.templateSetupStarter(workspaceID, taskID); err != nil {
		logger.Warn("Folder first task failed to start", logger.Fields{"workspace_id": workspaceID, "task_id": taskID, "error": err})
		_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "started": false, "reason": "start_failed", "task_id": taskID})
		return
	}
	logger.Info("Folder first task started on first open", logger.Fields{"workspace_id": workspaceID, "task_id": taskID})
	_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "started": true, "task_id": taskID})
}
