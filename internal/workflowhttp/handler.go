package workflowhttp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/johnjallday/ori-agent/internal/agentstudio"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/orchestration/templates"
)

// Handler manages custom workflow HTTP endpoints
type Handler struct {
	workflowManager *templates.CustomWorkflowManager
	workspaceStore  agentstudio.Store
}

// NewHandler creates a new workflow handler
func NewHandler(workflowManager *templates.CustomWorkflowManager, workspaceStore agentstudio.Store) *Handler {
	return &Handler{
		workflowManager: workflowManager,
		workspaceStore:  workspaceStore,
	}
}

// CreateWorkflowRequest represents the request to create a new custom workflow
type CreateWorkflowRequest struct {
	Name                string                         `json:"name"`
	Description         string                         `json:"description,omitempty"`
	Category            string                         `json:"category,omitempty"`
	Nodes               []templates.WorkflowNode       `json:"nodes"`
	InternalConnections []templates.WorkflowConnection `json:"internal_connections,omitempty"`
	InputPorts          []templates.WorkflowPort       `json:"input_ports,omitempty"`
	OutputPorts         []templates.WorkflowPort       `json:"output_ports,omitempty"`
	Layout              templates.WorkflowLayout       `json:"layout"`
}

// CheckAgentsRequest represents the request to check agent availability
type CheckAgentsRequest struct {
	StudioID string `json:"studio_id"`
}

// CheckAgentsResponse represents the response for agent availability check
type CheckAgentsResponse struct {
	Available      bool     `json:"available"`
	MissingAgents  []string `json:"missing_agents,omitempty"`
	RequiredAgents []string `json:"required_agents"`
}

// WorkflowsHandler handles /api/workflows requests
// GET: List all workflows (builtin templates + custom workflows)
// POST: Create new custom workflow
func (h *Handler) WorkflowsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	switch r.Method {
	case http.MethodGet:
		h.handleListWorkflows(w, r)
	case http.MethodPost:
		h.handleCreateWorkflow(w, r)
	default:
		orihttp.RespondMethodNotAllowed(w)
	}
}

// WorkflowHandler handles /api/workflows/:id requests
// GET: Get specific workflow
// DELETE: Delete custom workflow
func (h *Handler) WorkflowHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Extract workflow ID from URL path
	// Path format: /api/workflows/{id} or /api/workflows/{id}/check-agents
	path := strings.TrimPrefix(r.URL.Path, "/api/workflows/")
	parts := strings.Split(path, "/")
	workflowID := parts[0]

	if workflowID == "" {
		orihttp.RespondBadRequest(w, "Workflow ID is required")
		return
	}

	// Check if this is a check-agents request
	if len(parts) > 1 && parts[1] == "check-agents" {
		h.handleCheckAgents(w, r, workflowID)
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.handleGetWorkflow(w, r, workflowID)
	case http.MethodDelete:
		h.handleDeleteWorkflow(w, r, workflowID)
	default:
		orihttp.RespondMethodNotAllowed(w)
	}
}

// handleListWorkflows returns all custom workflows
func (h *Handler) handleListWorkflows(w http.ResponseWriter, r *http.Request) {
	if h.workflowManager == nil {
		orihttp.RespondInternalError(w, "Workflow manager not initialized")
		return
	}

	workflows := h.workflowManager.ListWorkflows()

	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"workflows": workflows,
		"count":     len(workflows),
	}); err != nil {
		logger.Error("Failed to encode workflows response", logger.Fields{"err": err})
	}
}

// handleCreateWorkflow creates a new custom workflow
func (h *Handler) handleCreateWorkflow(w http.ResponseWriter, r *http.Request) {
	if h.workflowManager == nil {
		orihttp.RespondInternalError(w, "Workflow manager not initialized")
		return
	}

	var req CreateWorkflowRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		orihttp.RespondBadRequest(w, fmt.Sprintf("Invalid request body: %v", err))
		return
	}

	// Validate required fields
	if req.Name == "" {
		orihttp.RespondBadRequest(w, "Workflow name is required")
		return
	}

	if len(req.Nodes) == 0 {
		orihttp.RespondBadRequest(w, "At least one node is required")
		return
	}

	if len(req.Nodes) > templates.MaxWorkflowNodes {
		orihttp.RespondBadRequest(w, fmt.Sprintf("Maximum %d nodes allowed, got %d", templates.MaxWorkflowNodes, len(req.Nodes)))
		return
	}

	// Create workflow
	workflow := templates.NewCustomWorkflow(req.Name, req.Description, req.Category)
	workflow.Nodes = req.Nodes
	workflow.InternalConnections = req.InternalConnections
	workflow.InputPorts = req.InputPorts
	workflow.OutputPorts = req.OutputPorts
	workflow.Layout = req.Layout

	// Save workflow
	if err := h.workflowManager.SaveWorkflow(workflow); err != nil {
		logger.Error("Failed to save workflow", logger.Fields{"err": err})
		orihttp.RespondInternalError(w, fmt.Sprintf("Failed to save workflow: %v", err))
		return
	}

	logger.Info("Created custom workflow", logger.Fields{"id": workflow.ID, "name": workflow.Name, "node_count": len(workflow.Nodes)})

	w.WriteHeader(http.StatusCreated)
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"id":      workflow.ID,
		"name":    workflow.Name,
		"message": "Workflow created successfully",
	}); err != nil {
		logger.Error("Failed to encode create response", logger.Fields{"err": err})
	}
}

// handleGetWorkflow retrieves a specific workflow
func (h *Handler) handleGetWorkflow(w http.ResponseWriter, r *http.Request, workflowID string) {
	if h.workflowManager == nil {
		orihttp.RespondInternalError(w, "Workflow manager not initialized")
		return
	}

	workflow, err := h.workflowManager.GetWorkflow(workflowID)
	if err != nil {
		orihttp.RespondNotFound(w, fmt.Sprintf("Workflow not found: %v", err))
		return
	}

	if err := json.NewEncoder(w).Encode(workflow); err != nil {
		logger.Error("Failed to encode workflow response", logger.Fields{"err": err})
	}
}

// handleDeleteWorkflow deletes a custom workflow
func (h *Handler) handleDeleteWorkflow(w http.ResponseWriter, r *http.Request, workflowID string) {
	if h.workflowManager == nil {
		orihttp.RespondInternalError(w, "Workflow manager not initialized")
		return
	}

	// Check if workflow exists
	workflow, err := h.workflowManager.GetWorkflow(workflowID)
	if err != nil {
		orihttp.RespondNotFound(w, fmt.Sprintf("Workflow not found: %v", err))
		return
	}

	// Only allow deletion of custom workflows
	if workflow.Source != templates.WorkflowSourceCustom {
		orihttp.RespondForbidden(w, "Cannot delete built-in workflows")
		return
	}

	// Delete workflow
	if err := h.workflowManager.DeleteWorkflow(workflowID); err != nil {
		logger.Error("Failed to delete workflow", logger.Fields{"id": workflowID, "err": err})
		orihttp.RespondInternalError(w, fmt.Sprintf("Failed to delete workflow: %v", err))
		return
	}

	logger.Info("Deleted custom workflow", logger.Fields{"id": workflowID})
	w.WriteHeader(http.StatusNoContent)
}

// handleCheckAgents checks if all agents required by a workflow are available in a studio
func (h *Handler) handleCheckAgents(w http.ResponseWriter, r *http.Request, workflowID string) {
	if r.Method != http.MethodPost {
		orihttp.RespondMethodNotAllowed(w)
		return
	}

	if h.workflowManager == nil {
		orihttp.RespondInternalError(w, "Workflow manager not initialized")
		return
	}

	// Get workflow
	workflow, err := h.workflowManager.GetWorkflow(workflowID)
	if err != nil {
		orihttp.RespondNotFound(w, fmt.Sprintf("Workflow not found: %v", err))
		return
	}

	// Parse request
	var req CheckAgentsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		orihttp.RespondBadRequest(w, fmt.Sprintf("Invalid request body: %v", err))
		return
	}

	if req.StudioID == "" {
		orihttp.RespondBadRequest(w, "Studio ID is required")
		return
	}

	// Get studio agents
	studio, err := h.workspaceStore.Get(req.StudioID)
	if err != nil {
		orihttp.RespondNotFound(w, fmt.Sprintf("Studio not found: %v", err))
		return
	}

	// Check agent availability
	requiredAgents := workflow.GetAgentNames()
	missingAgents := h.workflowManager.CheckAgentAvailability(workflow, studio.Agents)

	response := CheckAgentsResponse{
		Available:      len(missingAgents) == 0,
		MissingAgents:  missingAgents,
		RequiredAgents: requiredAgents,
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		logger.Error("Failed to encode check-agents response", logger.Fields{"err": err})
	}
}
