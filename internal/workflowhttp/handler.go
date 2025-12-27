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
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
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
		if err := orihttp.RespondBadRequest(w, "Workflow ID is required"); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
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
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
	}
}

// handleListWorkflows returns all custom workflows

func (h *Handler) handleListWorkflows(w http.ResponseWriter, r *http.Request) {
	if h.workflowManager == nil {
		if err := orihttp.RespondInternalError(w, "Workflow manager not initialized"); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
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
		if err := orihttp.RespondInternalError(w, "Workflow manager not initialized"); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	var req CreateWorkflowRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	if req.Name == "" {
		if err := orihttp.RespondBadRequest(w, "Workflow name is required"); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	if len(req.Nodes) == 0 {
		if err := orihttp.RespondBadRequest(w, "At least one node is required"); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	if len(req.Nodes) > templates.MaxWorkflowNodes {
		if err := orihttp.RespondBadRequest(w, fmt.Sprintf("Maximum %d nodes allowed, got %d", templates.MaxWorkflowNodes, len(req.Nodes))); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
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
		if err := orihttp.RespondInternalError(w, fmt.Sprintf("Failed to save workflow: %v", err)); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
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
		if err := orihttp.RespondInternalError(w, "Workflow manager not initialized"); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	workflow, err := h.workflowManager.GetWorkflow(workflowID)
	if err != nil {
		if err := orihttp.RespondNotFound(w, fmt.Sprintf("Workflow not found: %v", err)); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	if err := json.NewEncoder(w).Encode(workflow); err != nil {
		logger.Error("Failed to encode workflow response", logger.Fields{"err": err})
	}
}

// handleDeleteWorkflow deletes a custom workflow
func (h *Handler) handleDeleteWorkflow(w http.ResponseWriter, r *http.Request, workflowID string) {
	if h.workflowManager == nil {
		if err := orihttp.RespondInternalError(w, "Workflow manager not initialized"); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	// Check if workflow exists
	workflow, err := h.workflowManager.GetWorkflow(workflowID)
	if err != nil {
		if err := orihttp.RespondNotFound(w, fmt.Sprintf("Workflow not found: %v", err)); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	// Only allow deletion of custom workflows

	if workflow.Source != templates.WorkflowSourceCustom {
		if encodeErr := orihttp.RespondForbidden(w, "Cannot delete built-in workflows"); encodeErr != nil {
			logger.Error("Failed to write forbidden response", logger.Fields{"error": encodeErr})
		}
		return
	}

	// Delete workflow
	if err := h.workflowManager.DeleteWorkflow(workflowID); err != nil {
		logger.Error("Failed to delete workflow", logger.Fields{"id": workflowID, "err": err})
		if err := orihttp.RespondInternalError(w, fmt.Sprintf("Failed to delete workflow: %v", err)); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	logger.Info("Deleted custom workflow", logger.Fields{"id": workflowID})
	w.WriteHeader(http.StatusNoContent)
}

// handleCheckAgents checks if all agents required by a workflow are available in a studio
func (h *Handler) handleCheckAgents(w http.ResponseWriter, r *http.Request, workflowID string) {
	if r.Method != http.MethodPost {
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	if h.workflowManager == nil {
		if err := orihttp.RespondInternalError(w, "Workflow manager not initialized"); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	// Get workflow
	workflow, err := h.workflowManager.GetWorkflow(workflowID)
	if err != nil {
		if err := orihttp.RespondNotFound(w, fmt.Sprintf("Workflow not found: %v", err)); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	// Parse request
	var req CheckAgentsRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	if req.StudioID == "" {
		if err := orihttp.RespondBadRequest(w, "Studio ID is required"); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	// Get studio agents
	studio, err := h.workspaceStore.Get(req.StudioID)
	if err != nil {
		if err := orihttp.RespondNotFound(w, fmt.Sprintf("Studio not found: %v", err)); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
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
