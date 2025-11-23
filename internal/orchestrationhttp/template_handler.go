package orchestrationhttp

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/johnjallday/ori-agent/internal/agentstudio"
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
		http.Error(w, "template manager not initialized", http.StatusInternalServerError)
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
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(template)
		return
	}

	// List templates
	var templateList []*templates.WorkflowTemplate
	if category != "" {
		templateList = th.templateManager.ListTemplatesByCategory(category)
	} else {
		templateList = th.templateManager.ListTemplates()
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"templates": templateList,
		"count":     len(templateList),
	})
}

// handleCreateTemplate creates a new custom workflow template
func (th *TemplateHandler) handleCreateTemplate(w http.ResponseWriter, r *http.Request) {
	var template templates.WorkflowTemplate
	if err := json.NewDecoder(r.Body).Decode(&template); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	// Save template
	if err := th.templateManager.SaveTemplate(&template); err != nil {
		log.Printf("❌ Failed to save template: %v", err)
		http.Error(w, fmt.Sprintf("failed to save template: %v", err), http.StatusInternalServerError)
		return
	}

	log.Printf("✅ Created workflow template: %s", template.ID)
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(template)
}

// handleDeleteTemplate deletes a custom workflow template
func (th *TemplateHandler) handleDeleteTemplate(w http.ResponseWriter, r *http.Request) {
	templateID := r.URL.Query().Get("id")
	if templateID == "" {
		http.Error(w, "template id required", http.StatusBadRequest)
		return
	}

	if err := th.templateManager.DeleteTemplate(templateID); err != nil {
		log.Printf("❌ Failed to delete template %s: %v", templateID, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	log.Printf("🗑️  Deleted workflow template: %s", templateID)
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
		http.Error(w, "template manager not initialized", http.StatusInternalServerError)
		return
	}

	var req struct {
		TemplateID string                 `json:"template_id"`
		Parameters map[string]interface{} `json:"parameters"`
		AgentName  string                 `json:"agent_name"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	// Instantiate template
	instance, err := th.templateManager.InstantiateTemplate(req.TemplateID, req.Parameters)
	if err != nil {
		log.Printf("❌ Failed to instantiate template %s: %v", req.TemplateID, err)
		http.Error(w, fmt.Sprintf("failed to instantiate template: %v", err), http.StatusBadRequest)
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
		log.Printf("❌ Failed to execute collaborative task: %v", err)
		http.Error(w, fmt.Sprintf("failed to execute workflow: %v", err), http.StatusInternalServerError)
		return
	}

	log.Printf("✅ Instantiated and executed workflow from template: %s", req.TemplateID)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"instance": instance,
		"result":   result,
	})
}
