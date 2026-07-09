package sessionhttp

import (
	"strings"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// Context keys stamped onto template-seeded tasks. They record template
// provenance and drive the first-open auto-start of the setup task; the
// consumed marker (template_setup_autostart_consumed_at) is written by the
// setup-start endpoint, never here.
const (
	taskContextTemplateID          = "template_id"
	taskContextTemplateStarterTask = "template_starter_task"
	taskContextTemplateSetup       = "template_setup"
)

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
