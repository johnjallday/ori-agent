package workflowhttp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/orchestration/templates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Handler manages custom workflow HTTP endpoints
type Handler struct {
	workflowManager *templates.CustomWorkflowManager
	workspaceStore  workspace.Store
}

// NewHandler creates a new workflow handler
func NewHandler(workflowManager *templates.CustomWorkflowManager, workspaceStore workspace.Store) *Handler {
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
	WorkspaceID string `json:"workspace_id"`
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
		h.handleListWorkflows(w)
	case http.MethodPost:
		h.handleCreateWorkflow(w, r)
	default:
		orihttp.MethodNotAllowed(w)
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

	// Handle import endpoint (no workflow ID)
	if workflowID == "import" {
		h.handleImportWorkflow(w, r)
		return
	}

	if workflowID == "" {
		orihttp.BadRequest(w, "Workflow ID is required")
		return
	}

	// Check if this is a sub-resource request
	if len(parts) > 1 {
		switch parts[1] {
		case "check-agents":
			h.handleCheckAgents(w, r, workflowID)
			return
		case "export":
			h.handleExportWorkflow(w, r, workflowID)
			return
		}
	}

	switch r.Method {
	case http.MethodGet:
		h.handleGetWorkflow(w, workflowID)
	case http.MethodDelete:
		h.handleDeleteWorkflow(w, workflowID)
	default:
		orihttp.MethodNotAllowed(w)
	}
}

// handleListWorkflows returns all custom workflows

func (h *Handler) handleListWorkflows(w http.ResponseWriter) {
	if h.workflowManager == nil {
		orihttp.InternalError(w, "Workflow manager not initialized")
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
		orihttp.InternalError(w, "Workflow manager not initialized")
		return
	}

	var req CreateWorkflowRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	if req.Name == "" {
		orihttp.BadRequest(w, "Workflow name is required")
		return
	}

	if len(req.Nodes) == 0 {
		orihttp.BadRequest(w, "At least one node is required")
		return
	}

	if len(req.Nodes) > templates.MaxWorkflowNodes {
		orihttp.BadRequest(w, fmt.Sprintf("Maximum %d nodes allowed, got %d", templates.MaxWorkflowNodes, len(req.Nodes)))
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
		orihttp.InternalError(w, fmt.Sprintf("Failed to save workflow: %v", err))
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
func (h *Handler) handleGetWorkflow(w http.ResponseWriter, workflowID string) {
	if h.workflowManager == nil {
		orihttp.InternalError(w, "Workflow manager not initialized")
		return
	}

	workflow, err := h.workflowManager.GetWorkflow(workflowID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workflow not found: %v", err))
		return
	}

	if err := json.NewEncoder(w).Encode(workflow); err != nil {
		logger.Error("Failed to encode workflow response", logger.Fields{"err": err})
	}
}

// handleDeleteWorkflow deletes a custom workflow
func (h *Handler) handleDeleteWorkflow(w http.ResponseWriter, workflowID string) {
	if h.workflowManager == nil {
		orihttp.InternalError(w, "Workflow manager not initialized")
		return
	}

	// Check if workflow exists
	workflow, err := h.workflowManager.GetWorkflow(workflowID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workflow not found: %v", err))
		return
	}

	// Only allow deletion of custom workflows

	if workflow.Source != templates.WorkflowSourceCustom {
		orihttp.Forbidden(w, "Cannot delete built-in workflows")
		return
	}

	// Delete workflow
	if err := h.workflowManager.DeleteWorkflow(workflowID); err != nil {
		logger.Error("Failed to delete workflow", logger.Fields{"id": workflowID, "err": err})
		orihttp.InternalError(w, fmt.Sprintf("Failed to delete workflow: %v", err))
		return
	}

	logger.Info("Deleted custom workflow", logger.Fields{"id": workflowID})
	w.WriteHeader(http.StatusNoContent)
}

// handleExportWorkflow exports a workflow as a downloadable JSON file
func (h *Handler) handleExportWorkflow(w http.ResponseWriter, r *http.Request, workflowID string) {
	if r.Method != http.MethodGet {
		orihttp.MethodNotAllowed(w)
		return
	}

	if h.workflowManager == nil {
		orihttp.InternalError(w, "Workflow manager not initialized")
		return
	}

	workflow, err := h.workflowManager.GetWorkflow(workflowID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workflow not found: %v", err))
		return
	}

	data, err := json.MarshalIndent(workflow, "", "  ")
	if err != nil {
		logger.Error("Failed to marshal workflow for export", logger.Fields{"id": workflowID, "err": err})
		orihttp.InternalError(w, "Failed to export workflow")
		return
	}

	// Sanitize filename - replace spaces and special chars
	safeName := strings.ReplaceAll(workflow.Name, " ", "-")
	safeName = strings.ReplaceAll(safeName, "/", "-")
	filename := fmt.Sprintf("workflow-%s.json", safeName)

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	_, _ = w.Write(data)

	logger.Info("Exported workflow", logger.Fields{"id": workflowID, "name": workflow.Name})
}

// handleImportWorkflow imports a workflow from an uploaded JSON file
func (h *Handler) handleImportWorkflow(w http.ResponseWriter, r *http.Request) {
	if h.workflowManager == nil {
		orihttp.InternalError(w, "Workflow manager not initialized")
		return
	}

	// Parse multipart form (max 10MB)
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		orihttp.BadRequest(w, "Failed to parse form data")
		return
	}

	file, _, err := r.FormFile("file")
	if err != nil {
		orihttp.BadRequest(w, "No file provided")
		return
	}
	defer func() { _ = file.Close() }()

	// Decode JSON
	var workflow templates.CustomWorkflow
	if err := json.NewDecoder(file).Decode(&workflow); err != nil {
		orihttp.BadRequest(w, fmt.Sprintf("Invalid workflow JSON: %v", err))
		return
	}

	// Validate workflow structure
	if err := workflow.Validate(); err != nil {
		orihttp.BadRequest(w, fmt.Sprintf("Invalid workflow: %v", err))
		return
	}

	// Check for name conflicts and generate unique name if needed
	existingWorkflows := h.workflowManager.ListWorkflows()
	originalName := workflow.Name
	nameExists := true
	counter := 1

	for nameExists {
		nameExists = false
		for _, existing := range existingWorkflows {
			if existing.Name == workflow.Name {
				nameExists = true
				workflow.Name = fmt.Sprintf("%s (%d)", originalName, counter)
				counter++
				break
			}
		}
	}

	// Generate new ID for imported workflow
	workflow.ID = ""
	importedWorkflow := templates.NewCustomWorkflow(workflow.Name, workflow.Description, workflow.Category)
	importedWorkflow.Nodes = workflow.Nodes
	importedWorkflow.InternalConnections = workflow.InternalConnections
	importedWorkflow.InputPorts = workflow.InputPorts
	importedWorkflow.OutputPorts = workflow.OutputPorts
	importedWorkflow.Layout = workflow.Layout

	// Save workflow
	if err := h.workflowManager.SaveWorkflow(importedWorkflow); err != nil {
		logger.Error("Failed to save imported workflow", logger.Fields{"err": err})
		orihttp.InternalError(w, fmt.Sprintf("Failed to save workflow: %v", err))
		return
	}

	logger.Info("Imported workflow", logger.Fields{
		"id":            importedWorkflow.ID,
		"name":          importedWorkflow.Name,
		"original_name": originalName,
	})

	w.WriteHeader(http.StatusCreated)
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"id":            importedWorkflow.ID,
		"name":          importedWorkflow.Name,
		"original_name": originalName,
		"message":       "Workflow imported successfully",
	}); err != nil {
		logger.Error("Failed to encode import response", logger.Fields{"err": err})
	}
}

// handleCheckAgents checks if all agents required by a workflow are available in a workspace
func (h *Handler) handleCheckAgents(w http.ResponseWriter, r *http.Request, workflowID string) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}

	if h.workflowManager == nil {
		orihttp.InternalError(w, "Workflow manager not initialized")
		return
	}

	// Get workflow
	workflow, err := h.workflowManager.GetWorkflow(workflowID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workflow not found: %v", err))
		return
	}

	// Parse request
	var req CheckAgentsRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	if req.WorkspaceID == "" {
		orihttp.BadRequest(w, "Workspace ID is required")
		return
	}

	// Get workspace agents
	ws, err := h.workspaceStore.Get(req.WorkspaceID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace not found: %v", err))
		return
	}

	// Check agent availability

	requiredAgents := workflow.GetAgentNames()
	missingAgents := h.workflowManager.CheckAgentAvailability(workflow, ws.Agents)

	response := CheckAgentsResponse{
		Available:      len(missingAgents) == 0,
		MissingAgents:  missingAgents,
		RequiredAgents: requiredAgents,
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		logger.Error("Failed to encode check-agents response", logger.Fields{"err": err})
	}
}
