package sessionhttp

import (
	"errors"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/google/uuid"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/reapersetup"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// Context keys stamped onto template-seeded tasks. They record template
// provenance and drive the first-open auto-start of the setup task; the
// consumed marker is written by the setup-start endpoint, never at seed time.
const (
	taskContextTemplateID          = "template_id"
	taskContextTemplateStarterTask = "template_starter_task"
	taskContextTemplateSetup       = "template_setup"
	taskContextSetupConsumedAt     = "template_setup_autostart_consumed_at"
	// taskContextSetupWizardCompletedAt marks a setup task the Setup Wizard
	// completed on the user's behalf. It is also the idempotency key: a second
	// completion attempt finds the marker and changes nothing.
	taskContextSetupWizardCompletedAt = "setup_wizard_completed_at"
)

// setupWizardCompletionNote is the system-authored result recorded on a setup
// task the wizard satisfied. It says who completed it and how, so the task's
// history does not read as though an agent did work it never did.
const setupWizardCompletionNote = "Completed by the blueprint Setup Wizard: every required setup step passed. No agent ran for this task."

// seedTemplateStarterTasks creates the template's starter tasks on the folder
// workspace, assigned to the entry agent (coordinator) with entry_agent_default
// provenance. It runs server-side during workspace creation — after the
// skeleton is instantiated and the agent roster is seeded — so API-created
// workspaces get their tasks too (the old client-side loop could not cover
// them). Tasks are created pending and never started here: the setup task
// auto-starts on first open of the workspace, not at creation.
//
// Best-effort by contract: callers log the error and continue, a failure must
// never fail workspace creation. The write runs inside the store's Update for
// lost-update safety.
func (h *Handler) seedTemplateStarterTasks(workspaceID string, tpl projecttemplates.Template) (int, error) {
	if h == nil || len(tpl.StarterTasks) == 0 {
		return 0, nil
	}
	store := h.taskMutationStore()
	if store == nil {
		return 0, nil
	}
	id := strings.TrimSpace(workspaceID)
	if id == "" {
		return 0, nil
	}

	seeded := 0
	err := store.Update(id, func(ws *agentworkspace.Workspace) error {
		tasks := make([]agentworkspace.Task, 0, len(tpl.StarterTasks))
		for _, st := range tpl.StarterTasks {
			task := agentworkspace.Task{
				ID:          uuid.New().String(),
				WorkspaceID: id,
				Description: st.Description,
				Details:     st.Details,
				Priority:    1,
				Status:      agentworkspace.TaskStatusPending,
				// Carry the template's capability preconditions onto the task so
				// execution can stop before spending a model call on work the
				// workspace is not connected for.
				RequiredCapabilities: agentworkspace.NormalizeCapabilityKeys(st.Requires),
				Context: map[string]any{
					taskContextTemplateID:          tpl.ID,
					taskContextTemplateStarterTask: true,
				},
			}
			if st.Setup {
				task.Context[taskContextTemplateSetup] = true
			}
			// Own the task from birth: assigned to the coordinator (entry
			// agent) when one resolves. When none does (explicit
			// create_template_agents opt-out), the task stays unassigned and
			// the claim-on-agent-add sweep picks it up later.
			ws.ApplyEntryAgentDefault(&task)
			tasks = append(tasks, task)
		}
		if err := ws.AddTasks(tasks); err != nil {
			return err
		}
		seeded = len(tasks)
		return nil
	})
	if err != nil {
		return 0, err
	}
	return seeded, nil
}

// seedTemplateStarterTasksLogged runs the seed as a best-effort side effect of
// workspace creation, logging the outcome and returning the seeded count (0 on
// failure, so a failed seed is never reported as success).
func (h *Handler) seedTemplateStarterTasksLogged(workspaceID string, tpl projecttemplates.Template) int {
	seeded, err := h.seedTemplateStarterTasks(workspaceID, tpl)
	if err != nil {
		logger.Warn("Failed to seed template starter tasks", logger.Fields{"workspace_id": workspaceID, "template": tpl.ID, "error": err})
		return 0
	}
	if seeded > 0 {
		logger.Info("Seeded template starter tasks", logger.Fields{"workspace_id": workspaceID, "template": tpl.ID, "seeded": seeded})
	}
	return seeded
}

// handleTemplateSetupStart serves POST /api/workspaces/{id}/template-setup/start:
// the first-open auto-start trigger for a template's setup task. Inside a
// single store Update it finds the unconsumed setup task and stamps the
// consumed marker, then starts the task through the injected manual-execution
// path. The endpoint is idempotent — repeat calls (reloads, concurrent tabs)
// find the marker and no-op — and an execution failure leaves the task
// manually startable but never auto-retried (the marker stays).
//
// An unassigned setup task (create_template_agents opt-out) is left
// unconsumed: it cannot run without an agent, and the claim-on-agent-add
// sweep assigns it later so a subsequent open still auto-starts it.
func (h *Handler) handleTemplateSetupStart(w http.ResponseWriter, r *http.Request, workspaceID string) {
	if r.Method != http.MethodPost {
		_ = orihttp.RespondMethodNotAllowed(w)
		return
	}
	store := h.taskMutationStore()
	if store == nil {
		_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "started": false, "reason": "no_task_store"})
		return
	}

	// A blueprint that declares a Setup Wizard owns its own setup. Auto-starting
	// the help task here would put an agent — and its autonomy prompt — on top of
	// the deterministic setup dialog, which is the collision this feature exists
	// to remove. The task stays seeded, pending, and unconsumed, so the user can
	// still open it for help, and the wizard completes it when setup passes.
	if h.workspaceHasSetupWizard(workspaceID) {
		_ = orihttp.RespondSuccess(w, map[string]any{
			"success": true,
			"started": false,
			"reason":  "setup_wizard_owned",
		})
		return
	}

	// Gate on normalized readiness BEFORE reserving/writing the consumed marker.
	// For a REAPER workspace, auto-start proceeds only when Ori is configured to
	// attempt live control (ori_ready). Otherwise the setup task is left pending
	// and unconsumed with a stable blocker reason, so a later readiness change
	// (repair, enable, permission) still auto-starts it exactly once. Non-REAPER
	// templates are not identified and keep the prior behavior.
	if h.reaperResolver != nil {
		if readiness, rerr := h.reaperResolver.Resolve(workspaceID); rerr == nil && readiness.Identified && readiness.Status != reapersetup.StatusOriReady {
			_ = orihttp.RespondSuccess(w, map[string]any{
				"success":          true,
				"started":          false,
				"reason":           "not_ready",
				"readiness_status": string(readiness.Status),
				"readiness":        readiness,
			})
			return
		}
	}

	var (
		taskID string
		reason = "no_setup_task"
	)
	err := store.Update(workspaceID, func(ws *agentworkspace.Workspace) error {
		for i := range ws.Tasks {
			task := &ws.Tasks[i]
			if task.Context[taskContextTemplateSetup] != true {
				continue
			}
			if _, consumed := task.Context[taskContextSetupConsumedAt]; consumed {
				reason = "already_consumed"
				return errTemplateSetupNoChange
			}
			assignee := strings.TrimSpace(task.To)
			if assignee == "" || strings.EqualFold(assignee, "unassigned") {
				// Not consumable yet: no agent to run it. Leave the marker off so
				// the next open (after an agent joins and the claim sweep assigns
				// the task) still auto-starts it.
				reason = "unassigned"
				return errTemplateSetupNoChange
			}
			task.Context[taskContextSetupConsumedAt] = time.Now().UTC().Format(time.RFC3339)
			taskID = task.ID
			return nil
		}
		return errTemplateSetupNoChange
	})
	if err != nil && !errors.Is(err, errTemplateSetupNoChange) {
		// A missing folder workspace is a normal state (folder-less workspace),
		// not a client error: report not-started rather than failing first open.
		logger.Warn("Template setup start sweep failed", logger.Fields{"workspace_id": workspaceID, "error": err})
		_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "started": false, "reason": "workspace_unavailable"})
		return
	}

	if taskID == "" {
		_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "started": false, "reason": reason})
		return
	}

	// Marker committed: from here the outcome is started or consumed-but-failed,
	// never retried automatically.
	if h.templateSetupStarter == nil {
		logger.Warn("Template setup task consumed but no starter is wired", logger.Fields{"workspace_id": workspaceID, "task_id": taskID})
		_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "started": false, "reason": "execution_unavailable", "task_id": taskID})
		return
	}
	if err := h.templateSetupStarter(workspaceID, taskID); err != nil {
		logger.Warn("Template setup task failed to start", logger.Fields{"workspace_id": workspaceID, "task_id": taskID, "error": err})
		_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "started": false, "reason": "start_failed", "task_id": taskID})
		return
	}
	logger.Info("Template setup task auto-started on first open", logger.Fields{"workspace_id": workspaceID, "task_id": taskID})
	_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "started": true, "task_id": taskID})
}

// errTemplateSetupNoChange skips the folder-store save (and its task-markdown
// rewrite) when the sweep found nothing to consume, mirroring errNoTasksClaimed.
var errTemplateSetupNoChange = errors.New("no template setup task to start")

// folderWorkspaceReader reads the canonical workspace.json record. The wizard
// snapshot has no SQLite column, so a plain Get would report every workspace as
// having no wizard — and every setup task would auto-start on top of its own
// setup dialog.
type folderWorkspaceReader interface {
	GetFolderWorkspace(id string) (*agentworkspace.Workspace, error)
}

// workspaceHasSetupWizard reports whether the workspace's blueprint recorded a
// usable Setup Wizard. A store that cannot read the canonical record answers
// "no", which preserves the pre-wizard behavior rather than silently disabling
// setup for everyone.
func (h *Handler) workspaceHasSetupWizard(workspaceID string) bool {
	if h == nil || strings.TrimSpace(workspaceID) == "" {
		return false
	}
	for _, candidate := range []agentworkspace.Store{h.workspaceTaskStore, h.workspaceStore} {
		reader, ok := candidate.(folderWorkspaceReader)
		// A store field can hold a typed nil pointer (an unwired handler), which
		// satisfies the interface and then panics on first use — so the pointer
		// itself has to be checked, not just the interface.
		if !ok || isNilPointer(reader) {
			continue
		}
		ws, err := reader.GetFolderWorkspace(workspaceID)
		if err != nil || ws == nil {
			continue
		}
		return ws.HasSetupWizard()
	}
	return false
}

// isNilPointer reports whether an interface value holds a nil pointer.
func isNilPointer(v any) bool {
	if v == nil {
		return true
	}
	value := reflect.ValueOf(v)
	switch value.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan:
		return value.IsNil()
	default:
		return false
	}
}

// CompleteSetupHelpTaskOnWizardReady marks the blueprint's `setup: true` help
// task complete once its Setup Wizard reaches ready. It is the completion hook
// the wizard service fires.
//
// Three properties matter more than the mechanics. It spends no model call and
// starts no agent: the work was done by the user in the dialog, and charging
// them for a summary of it would be theatre. It records a system-authored note
// so the task's history says plainly who completed it. And it is idempotent —
// a replayed hook, a concurrent completion, or a repair-then-ready cycle finds
// the marker and changes nothing, so the task is never completed twice or
// reopened.
//
// A blueprint with no setup task is normal: the wizard is the setup, and the
// help task is optional.
func (h *Handler) CompleteSetupHelpTaskOnWizardReady(workspaceID string) {
	if h == nil {
		return
	}
	store := h.taskMutationStore()
	if store == nil {
		return
	}
	id := strings.TrimSpace(workspaceID)
	if id == "" {
		return
	}

	var completedTaskID string
	err := store.Update(id, func(ws *agentworkspace.Workspace) error {
		for i := range ws.Tasks {
			task := &ws.Tasks[i]
			if task.Context[taskContextTemplateSetup] != true {
				continue
			}
			if _, done := task.Context[taskContextSetupWizardCompletedAt]; done {
				return errTemplateSetupNoChange
			}
			// A task the user already finished, or one an agent is mid-way
			// through, is left exactly as it is. Setup being ready is not a
			// reason to rewrite someone else's work.
			if task.Status == agentworkspace.TaskStatusCompleted || task.Status == agentworkspace.TaskStatusInProgress {
				return errTemplateSetupNoChange
			}
			now := time.Now().UTC()
			task.Status = agentworkspace.TaskStatusCompleted
			task.CompletedAt = &now
			task.Result = setupWizardCompletionNote
			if task.Context == nil {
				task.Context = map[string]any{}
			}
			task.Context[taskContextSetupWizardCompletedAt] = now.Format(time.RFC3339)
			completedTaskID = task.ID
			return nil
		}
		return errTemplateSetupNoChange
	})
	if err != nil && !errors.Is(err, errTemplateSetupNoChange) {
		logger.Warn("Failed to complete setup help task after wizard readiness", logger.Fields{
			"workspace_id": id, "error": err,
		})
		return
	}
	if completedTaskID != "" {
		logger.Info("Setup Wizard completed the blueprint's setup task", logger.Fields{
			"workspace_id": id, "task_id": completedTaskID,
		})
	}
}
