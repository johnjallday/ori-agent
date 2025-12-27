package orchestrationhttp

import (
	"fmt"

	"net/http"
	"time"

	"github.com/johnjallday/ori-agent/internal/agentstudio"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/orchestration"
	"github.com/johnjallday/ori-agent/internal/orchestration/templates"
	"github.com/johnjallday/ori-agent/internal/store"
)

// TemplateHandler manages workflow template operations
type TemplateHandler struct {
	agentStore      store.Store
	workspaceStore  agentstudio.Store
	templateManager *templates.TemplateManager
	orchestrator    *orchestration.Orchestrator
	eventBus        *agentstudio.EventBus
}

// NewTemplateHandler creates a new template handler
func NewTemplateHandler(agentStore store.Store, workspaceStore agentstudio.Store,
	templateManager *templates.TemplateManager, orchestrator *orchestration.Orchestrator,
	eventBus *agentstudio.EventBus) *TemplateHandler {
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
			if respErr := orihttp.RespondNotFound(w, err.Error()); respErr != nil {
				logger.Error("Failed to write response", logger.Fields{"error":

				// List templates
				respErr})
			}
			return
		}
		orihttp.WriteJSON(w, template)
		return
	}

	var templateList []*templates.WorkflowTemplate
	if category != "" {
		templateList = th.templateManager.ListTemplatesByCategory(category)
	} else {
		templateList = th.templateManager.ListTemplates()
	}

	orihttp.WriteJSON(w, map[string]interface{}{
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
		if respErr := orihttp.RespondInternalError(w, fmt.Sprintf("failed to save template: %v", err)); respErr != nil {
			logger.Error("Failed to write response", logger.Fields{"error": respErr})
		}
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
		if respErr := orihttp.RespondInternalError(w, err.Error()); respErr != nil {
			logger.Error("Failed to write response", logger.Fields{"error": respErr})
		}
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
		TemplateID string                 `json:"template_id"`
		Parameters map[string]interface{} `json:"parameters"`
		AgentName  string                 `json:"agent_name"`
	}

	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	// Instantiate template

	instance, err := th.templateManager.InstantiateTemplate(req.TemplateID, req.Parameters)
	if err != nil {
		logger.Error("Failed to instantiate template", logger.Fields{"templateid": req.TemplateID, "err": err})
		if respErr := orihttp.RespondBadRequest(w, fmt.Sprintf("failed to instantiate template: %v", err)); respErr != nil {
			logger.

				// Create collaborative task from instance
				Error("Failed to write response", logger.Fields{"error": respErr})
		}
		return
	}

	task := orchestration.CollaborativeTask{
		Goal:          fmt.Sprintf("Execute workflow: %s", instance.TemplateName),
		RequiredRoles: instance.RequiredRoles,
		Context:       instance.Parameters,
		MaxDuration:   30 * time.Minute,
	}

	// Execute collaborative task
	result, err := th.orchestrator.ExecuteCollaborativeTask(r.Context(), req.AgentName, task)
	if err != nil {
		logger.Error("Failed to execute collaborative task", logger.Fields{"task_id": err})
		if respErr := orihttp.RespondInternalError(w, fmt.Sprintf("failed to execute workflow: %v", err)); respErr != nil {
			logger.Error("Failed to write response", logger.Fields{"error": respErr})
		}
		return
	}

	logger.Info("Instantiated and executed workflow from template", logger.Fields{"templateid": req.TemplateID})
	orihttp.WriteJSON(w, map[string]interface{}{
		"instance": instance,
		"result":   result,
	})
}
