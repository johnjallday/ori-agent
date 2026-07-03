package orchestrationhttp

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/orchestration"
	"github.com/johnjallday/ori-agent/internal/orchestration/templates"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// TemplateHandler manages workflow template operations
type TemplateHandler struct {
	agentStore      store.Store
	workspaceStore  workspace.Store
	templateManager *templates.TemplateManager
	orchestrator    *orchestration.Orchestrator
	eventBus        *workspace.EventBus
}

// NewTemplateHandler creates a new template handler
func NewTemplateHandler(agentStore store.Store, workspaceStore workspace.Store,
	templateManager *templates.TemplateManager, orchestrator *orchestration.Orchestrator,
	eventBus *workspace.EventBus) *TemplateHandler {
	return &TemplateHandler{
		agentStore:      agentStore,
		workspaceStore:  workspaceStore,
		templateManager: templateManager,
		orchestrator:    orchestrator,
		eventBus:        eventBus,
	}
}

// TemplatesHandler handles workflow template operations
// GET: List templates or get specific template
// POST: Create new template
// DELETE: Delete template
func (th *TemplateHandler) TemplatesHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if th.templateManager == nil {
		orihttp.InternalError(w, "template manager not initialized")
		return
	}

	switch r.Method {
	case http.MethodGet:
		th.handleGetTemplates(w, r)
	case http.MethodPost:
		th.handleCreateTemplate(w, r)
	case http.MethodDelete:
		th.handleDeleteTemplate(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// handleGetTemplates retrieves workflow templates
func (th *TemplateHandler) handleGetTemplates(w http.ResponseWriter, r *http.Request) {
	templateID := r.URL.Query().Get("id")
	category := r.URL.Query().Get("category")

	if templateID != "" {
		// Get specific template
		template, err := th.templateManager.GetTemplate(templateID)
		if err != nil {
			orihttp.NotFound(w, err.Error())
			return
		}
		orihttp.WriteJSON(w, template)
		return
	}

	// List templates

	var templateList []*templates.WorkflowTemplate
	if category != "" {
		templateList = th.templateManager.ListTemplatesByCategory(category)
	} else {
		templateList = th.templateManager.ListTemplates()
	}

	orihttp.WriteJSON(w, map[string]any{
		"templates": templateList,
		"count":     len(templateList),
	})
}

// handleCreateTemplate creates a new custom workflow template
func (th *TemplateHandler) handleCreateTemplate(w http.ResponseWriter, r *http.Request) {
	var template templates.WorkflowTemplate
	if !orihttp.ParseJSONBody(w, r, &template) {
		return
	}
	// Save template

	if err := th.templateManager.SaveTemplate(&template); err != nil {
		logger.Error("Failed to save template", logger.Fields{"err": err})
		orihttp.InternalError(w, fmt.Sprintf("failed to save template: %v", err))
		return
	}

	logger.Info("Created workflow template", logger.Fields{"id": template.ID})
	w.WriteHeader(http.StatusCreated)
	orihttp.WriteJSON(w, template)
}

// handleDeleteTemplate deletes a custom workflow template
func (th *TemplateHandler) handleDeleteTemplate(w http.ResponseWriter, r *http.Request) {
	templateID := r.URL.Query().Get("id")
	if templateID == "" {
		orihttp.BadRequest(w, "template id required")
		return
	}

	if err := th.templateManager.DeleteTemplate(templateID); err != nil {
		logger.Error("Failed to delete template", logger.Fields{"templateID": templateID, "err": err})
		orihttp.InternalError(w, err.Error())
		return
	}

	logger.Debug("🗑️ Deleted workflow template", logger.Fields{"templateID": templateID})
	w.WriteHeader(http.StatusNoContent)
}

// InstantiateTemplateHandler handles instantiating a workflow from a template
// POST: Create workflow instance from template with parameters
func (th *TemplateHandler) InstantiateTemplateHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	if th.templateManager == nil {
		orihttp.InternalError(w, "template manager not initialized")
		return
	}

	var req struct {
		TemplateID             string            `json:"template_id"`
		Parameters             map[string]any    `json:"parameters"`
		AgentName              string            `json:"agent_name"`
		WorkspaceID            string            `json:"workspace_id"`
		AgentAssignments       map[string]string `json:"agent_assignments"`
		OrchestrationMode      string            `json:"orchestration_mode"`
		ResultCombinationMode  string            `json:"result_combination_mode"`
		CombinationInstruction string            `json:"combination_instruction"`
	}

	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	// Instantiate template
	instance, err := th.templateManager.InstantiateTemplate(req.TemplateID, req.Parameters)
	if err != nil {
		logger.Error("Failed to instantiate template", logger.Fields{"templateid": req.TemplateID, "err": err})
		orihttp.BadRequest(w, fmt.Sprintf("failed to instantiate template: %v", err))
		return
	}

	if strings.TrimSpace(req.WorkspaceID) != "" {
		parentTask, subtasks, err := th.instantiateTemplateIntoWorkspace(req, instance)
		if err != nil {
			logger.Error("Failed to instantiate template into workspace", logger.Fields{
				"templateid":   req.TemplateID,
				"workspace_id": req.WorkspaceID,
				"error":        err,
			})
			orihttp.InternalError(w, fmt.Sprintf("failed to instantiate template into workspace: %v", err))
			return
		}

		logger.Info("Instantiated workflow template into workspace tasks", logger.Fields{
			"templateid":    req.TemplateID,
			"workspace_id":  req.WorkspaceID,
			"parent_task":   parentTask.ID,
			"subtask_count": len(subtasks),
		})
		orihttp.WriteJSON(w, map[string]any{
			"instance":    instance,
			"parent_task": parentTask,
			"subtasks":    subtasks,
		})
		return
	}

	// Create collaborative task from instance
	task := orchestration.CollaborativeTask{
		Goal:          fmt.Sprintf("Execute workflow: %s", instance.TemplateName),
		RequiredRoles: instance.RequiredRoles,
		Context:       instance.Parameters,
		MaxDuration:   30 * time.Minute,
	}

	// Execute collaborative task
	result, err := th.orchestrator.ExecuteCollaborativeTask(r.Context(), req.AgentName, task)
	if err != nil {
		logger.Error("Failed to execute collaborative task", logger.Fields{"error": err})
		orihttp.InternalError(w, fmt.Sprintf("failed to execute workflow: %v", err))
		return
	}

	logger.Info("Instantiated and executed workflow from template", logger.Fields{"templateid": req.TemplateID})
	orihttp.WriteJSON(w, map[string]any{
		"instance": instance,
		"result":   result,
	})
}

func (th *TemplateHandler) instantiateTemplateIntoWorkspace(
	req struct {
		TemplateID             string            `json:"template_id"`
		Parameters             map[string]any    `json:"parameters"`
		AgentName              string            `json:"agent_name"`
		WorkspaceID            string            `json:"workspace_id"`
		AgentAssignments       map[string]string `json:"agent_assignments"`
		OrchestrationMode      string            `json:"orchestration_mode"`
		ResultCombinationMode  string            `json:"result_combination_mode"`
		CombinationInstruction string            `json:"combination_instruction"`
	},
	instance *templates.WorkflowInstance,
) (*workspace.Task, []workspace.Task, error) {
	if th.workspaceStore == nil {
		return nil, nil, fmt.Errorf("workspace store not initialized")
	}
	if instance == nil {
		return nil, nil, fmt.Errorf("workflow instance is required")
	}

	ws, err := th.workspaceStore.Get(strings.TrimSpace(req.WorkspaceID))
	if err != nil {
		return nil, nil, fmt.Errorf("workspace not found: %w", err)
	}

	parentOrchestrationMode := workspace.TaskOrchestrationModeGraph
	if value := strings.TrimSpace(req.OrchestrationMode); value != "" {
		parentOrchestrationMode = workspace.NormalizeTaskOrchestrationMode(value)
	} else if value := strings.TrimSpace(string(instance.OrchestrationMode)); value != "" {
		parentOrchestrationMode = workspace.NormalizeTaskOrchestrationMode(value)
	}

	parentCombinationMode := workspace.TaskResultCombinationStructuredOutput
	if value := strings.TrimSpace(req.ResultCombinationMode); value != "" {
		parentCombinationMode = workspace.NormalizeTaskResultCombinationMode(value)
	} else if value := strings.TrimSpace(string(instance.ResultCombinationMode)); value != "" {
		parentCombinationMode = workspace.NormalizeTaskResultCombinationMode(value)
	}

	combinationInstruction := strings.TrimSpace(req.CombinationInstruction)
	if combinationInstruction == "" {
		combinationInstruction = strings.TrimSpace(instance.CombinationInstruction)
	}

	parentTask := workspace.Task{
		ID:                     uuid.NewString(),
		WorkspaceID:            ws.ID,
		From:                   "template",
		To:                     strings.TrimSpace(req.AgentName),
		Description:            fmt.Sprintf("Execute template: %s", strings.TrimSpace(instance.TemplateName)),
		Details:                strings.TrimSpace(instance.TemplateDescription),
		Priority:               3,
		Status:                 workspace.TaskStatusPending,
		OrchestrationMode:      parentOrchestrationMode,
		ResultCombinationMode:  parentCombinationMode,
		CombinationInstruction: combinationInstruction,
		OutputSchema:           workspace.NormalizeTaskOutputSchema(instance.OutputSchema),
		TemplateRef: &workspace.TaskTemplateRef{
			TemplateID:   instance.TemplateID,
			TemplateName: instance.TemplateName,
		},
		Context: map[string]any{
			"template_parameters": cloneTemplateParameters(instance.Parameters),
		},
	}
	if err := ws.AddTask(parentTask); err != nil {
		return nil, nil, fmt.Errorf("add parent task: %w", err)
	}

	stepTaskIDs := make(map[string]string, len(instance.Steps))
	for _, step := range instance.Steps {
		stepTaskIDs[step.ID] = uuid.NewString()
	}

	subtasks := make([]workspace.Task, 0, len(instance.Steps))
	for index, step := range instance.Steps {
		assignedAgent := resolveTemplateStepAgent(step, req.AgentAssignments, req.AgentName)
		if assignedAgent != "" && !ws.HasAgent(assignedAgent) {
			if err := ws.AddAgent(assignedAgent); err != nil {
				return nil, nil, fmt.Errorf("add agent %s to workspace: %w", assignedAgent, err)
			}
		}

		inputTaskIDs := make([]string, 0, len(step.DependsOn))
		for _, depID := range step.DependsOn {
			taskID, ok := stepTaskIDs[depID]
			if !ok {
				return nil, nil, fmt.Errorf("step %s depends on unknown step %s", step.ID, depID)
			}
			inputTaskIDs = append(inputTaskIDs, taskID)
		}

		description := strings.TrimSpace(step.Description)
		if description == "" {
			description = strings.TrimSpace(step.Name)
		}
		if description == "" {
			description = fmt.Sprintf("Template step %d", index+1)
		}

		subtask := workspace.Task{
			ID:           stepTaskIDs[step.ID],
			WorkspaceID:  ws.ID,
			From:         parentTask.To,
			To:           assignedAgent,
			Description:  description,
			Details:      strings.TrimSpace(step.Details),
			Priority:     normalizeTemplateStepPriority(step.Priority),
			Context:      cloneTemplateContext(step.Context),
			Timeout:      step.Timeout,
			Status:       workspace.TaskStatusPending,
			InputTaskIDs: inputTaskIDs,
			ParentTaskID: parentTask.ID,
			SubtaskIndex: index + 1,
			OutputSchema: workspace.NormalizeTaskOutputSchema(step.OutputSchema),
			TemplateRef: &workspace.TaskTemplateRef{
				TemplateID:   instance.TemplateID,
				TemplateName: instance.TemplateName,
				StepID:       step.ID,
				StepName:     firstNonEmpty(strings.TrimSpace(step.Name), description),
			},
		}
		if err := ws.AddTask(subtask); err != nil {
			return nil, nil, fmt.Errorf("add subtask %s: %w", step.ID, err)
		}
		subtasks = append(subtasks, subtask)
	}

	if err := th.workspaceStore.Save(ws); err != nil {
		return nil, nil, fmt.Errorf("save workspace: %w", err)
	}

	return &parentTask, subtasks, nil
}

func resolveTemplateStepAgent(step templates.WorkflowStep, assignments map[string]string, defaultAgent string) string {
	if assigned := strings.TrimSpace(step.AgentName); assigned != "" {
		return assigned
	}
	if assignments != nil {
		if assigned := strings.TrimSpace(assignments[step.ID]); assigned != "" {
			return assigned
		}
		if assigned := strings.TrimSpace(assignments[string(step.Role)]); assigned != "" {
			return assigned
		}
	}
	return strings.TrimSpace(defaultAgent)
}

func normalizeTemplateStepPriority(priority int) int {
	if priority < 1 || priority > 5 {
		return 3
	}
	return priority
}

func cloneTemplateContext(src map[string]any) map[string]any {
	if len(src) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(src))
	for key, value := range src {
		out[key] = value
	}
	return out
}

func cloneTemplateParameters(src map[string]any) map[string]any {
	if len(src) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(src))
	for key, value := range src {
		out[key] = value
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
