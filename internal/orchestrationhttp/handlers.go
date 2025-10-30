package orchestrationhttp

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/johnjallday/ori-agent/internal/agentcomm"
	"github.com/johnjallday/ori-agent/internal/orchestration"
	"github.com/johnjallday/ori-agent/internal/orchestration/templates"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Handler manages orchestration-related HTTP endpoints
type Handler struct {
	agentStore      store.Store
	workspaceStore  workspace.Store
	communicator    *agentcomm.Communicator
	orchestrator    *orchestration.Orchestrator
	templateManager *templates.TemplateManager
}

// NewHandler creates a new orchestration handler
func NewHandler(agentStore store.Store, workspaceStore workspace.Store) *Handler {
	return &Handler{
		agentStore:     agentStore,
		workspaceStore: workspaceStore,
		communicator:   agentcomm.NewCommunicator(workspaceStore),
	}
}

// SetOrchestrator sets the orchestrator instance
func (h *Handler) SetOrchestrator(orch *orchestration.Orchestrator) {
	h.orchestrator = orch
}

// SetTemplateManager sets the template manager instance
func (h *Handler) SetTemplateManager(tm *templates.TemplateManager) {
	h.templateManager = tm
}

// WorkspaceHandler handles workspace CRUD operations
// GET: List all workspaces or get workspace by ID
// POST: Create new workspace
// DELETE: Delete workspace
func (h *Handler) WorkspaceHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	switch r.Method {
	case http.MethodGet:
		h.handleGetWorkspace(w, r)
	case http.MethodPost:
		h.handleCreateWorkspace(w, r)
	case http.MethodDelete:
		h.handleDeleteWorkspace(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// handleGetWorkspace retrieves workspace(s)
func (h *Handler) handleGetWorkspace(w http.ResponseWriter, r *http.Request) {
	// Check if a specific workspace ID is requested
	wsID := r.URL.Query().Get("id")
	agentName := r.URL.Query().Get("agent")
	activeOnly := r.URL.Query().Get("active") == "true"

	if wsID != "" {
		// Get specific workspace
		ws, err := h.workspaceStore.Get(wsID)
		if err != nil {
			log.Printf("❌ Error getting workspace %s: %v", wsID, err)
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}

		_ = json.NewEncoder(w).Encode(ws)
		return
	}

	// List workspaces with optional filters
	if agentName != "" {
		// Get workspaces for specific agent
		workspaces, err := h.workspaceStore.GetByParentAgent(agentName)
		if err != nil {
			log.Printf("❌ Error listing workspaces for agent %s: %v", agentName, err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"workspaces": workspaces,
			"count":      len(workspaces),
		})
		return
	}

	if activeOnly {
		// Get only active workspaces
		workspaces, err := h.workspaceStore.ListActive()
		if err != nil {
			log.Printf("❌ Error listing active workspaces: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"workspaces": workspaces,
			"count":      len(workspaces),
		})
		return
	}

	// List all workspaces
	ids, err := h.workspaceStore.List()
	if err != nil {
		log.Printf("❌ Error listing workspaces: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Load summaries for all workspaces
	summaries := make([]map[string]interface{}, 0, len(ids))
	for _, id := range ids {
		ws, err := h.workspaceStore.Get(id)
		if err != nil {
			continue // Skip workspaces that fail to load
		}
		summaries = append(summaries, ws.GetSummary())
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"workspaces": summaries,
		"count":      len(summaries),
	})
}

// handleCreateWorkspace creates a new workspace
func (h *Handler) handleCreateWorkspace(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string                 `json:"name"`
		ParentAgent string                 `json:"parent_agent"`
		Agents      []string               `json:"participating_agents"`
		InitialData map[string]interface{} `json:"initial_context"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Printf("❌ Error decoding workspace creation request: %v", err)
		http.Error(w, "Invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Validate required fields
	if req.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	if req.ParentAgent == "" {
		http.Error(w, "parent_agent is required", http.StatusBadRequest)
		return
	}

	// Verify parent agent exists
	_, ok := h.agentStore.GetAgent(req.ParentAgent)
	if !ok {
		http.Error(w, "parent agent not found", http.StatusNotFound)
		return
	}

	// Verify all participating agents exist
	for _, agentName := range req.Agents {
		_, ok := h.agentStore.GetAgent(agentName)
		if !ok {
			http.Error(w, "agent not found: "+agentName, http.StatusNotFound)
			return
		}
	}

	// Create workspace
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{
		Name:        req.Name,
		ParentAgent: req.ParentAgent,
		Agents:      req.Agents,
		InitialData: req.InitialData,
	})

	// Save workspace
	if err := h.workspaceStore.Save(ws); err != nil {
		log.Printf("❌ Error saving workspace: %v", err)
		http.Error(w, "Failed to save workspace: "+err.Error(), http.StatusInternalServerError)
		return
	}

	log.Printf("✅ Created workspace: %s (ID: %s)", req.Name, ws.ID)

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"workspace_id": ws.ID,
		"status":       ws.Status,
		"created_at":   ws.CreatedAt,
	})
}

// handleDeleteWorkspace deletes a workspace
func (h *Handler) handleDeleteWorkspace(w http.ResponseWriter, r *http.Request) {
	wsID := r.URL.Query().Get("id")
	if wsID == "" {
		http.Error(w, "id parameter required", http.StatusBadRequest)
		return
	}

	if err := h.workspaceStore.Delete(wsID); err != nil {
		log.Printf("❌ Error deleting workspace %s: %v", wsID, err)
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	log.Printf("✅ Deleted workspace: %s", wsID)
	w.WriteHeader(http.StatusOK)
}

// MessagesHandler handles workspace message operations
// GET: Retrieve messages from workspace
// POST: Send message to workspace
func (h *Handler) MessagesHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	wsID := r.URL.Query().Get("workspace_id")
	if wsID == "" {
		http.Error(w, "workspace_id parameter required", http.StatusBadRequest)
		return
	}

	// Get workspace
	ws, err := h.workspaceStore.Get(wsID)
	if err != nil {
		log.Printf("❌ Error getting workspace %s: %v", wsID, err)
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.handleGetMessages(w, r, ws)
	case http.MethodPost:
		h.handleSendMessage(w, r, ws)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// handleGetMessages retrieves messages from workspace
func (h *Handler) handleGetMessages(w http.ResponseWriter, r *http.Request, ws *workspace.Workspace) {
	agentName := r.URL.Query().Get("agent")
	sinceStr := r.URL.Query().Get("since")

	var messages []workspace.AgentMessage

	if sinceStr != "" {
		// Get messages since timestamp
		since, err := time.Parse(time.RFC3339, sinceStr)
		if err != nil {
			http.Error(w, "Invalid since timestamp format (use RFC3339)", http.StatusBadRequest)
			return
		}
		messages = ws.GetMessagesSince(since)
	} else if agentName != "" {
		// Get messages for specific agent
		messages = ws.GetMessagesForAgent(agentName)
	} else {
		// Get all messages (direct field access through getter method)
		messages = ws.GetMessagesSince(time.Time{}) // epoch time returns all messages
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"messages": messages,
		"count":    len(messages),
	})
}

// handleSendMessage sends a message to workspace
func (h *Handler) handleSendMessage(w http.ResponseWriter, r *http.Request, ws *workspace.Workspace) {
	var msg workspace.AgentMessage

	if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
		log.Printf("❌ Error decoding message: %v", err)
		http.Error(w, "Invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Validate required fields
	if msg.From == "" {
		http.Error(w, "from field is required", http.StatusBadRequest)
		return
	}
	if msg.Content == "" {
		http.Error(w, "content field is required", http.StatusBadRequest)
		return
	}

	// Add message to workspace
	if err := ws.AddMessage(msg); err != nil {
		log.Printf("❌ Error adding message to workspace: %v", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Save updated workspace
	if err := h.workspaceStore.Save(ws); err != nil {
		log.Printf("❌ Error saving workspace after adding message: %v", err)
		http.Error(w, "Failed to save workspace: "+err.Error(), http.StatusInternalServerError)
		return
	}

	log.Printf("✅ Added message from %s to workspace %s", msg.From, ws.ID)

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success":    true,
		"message_id": msg.ID,
		"timestamp":  msg.Timestamp,
	})
}

// AgentCapabilitiesHandler handles agent capability management
// GET: Get agent capabilities
// PUT: Update agent capabilities
func (h *Handler) AgentCapabilitiesHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	agentName := r.URL.Query().Get("name")
	if agentName == "" {
		http.Error(w, "name parameter required", http.StatusBadRequest)
		return
	}

	agent, ok := h.agentStore.GetAgent(agentName)
	if !ok {
		http.Error(w, "agent not found", http.StatusNotFound)
		return
	}

	switch r.Method {
	case http.MethodGet:
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"agent":        agentName,
			"role":         agent.Role,
			"capabilities": agent.Capabilities,
		})

	case http.MethodPut:
		var req struct {
			Role         string   `json:"role"`
			Capabilities []string `json:"capabilities"`
		}

		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request body: "+err.Error(), http.StatusBadRequest)
			return
		}

		// Update agent role and capabilities
		if req.Role != "" {
			agent.Role = types.AgentRole(req.Role)
		}
		if req.Capabilities != nil {
			agent.Capabilities = req.Capabilities
		}

		if err := h.agentStore.SetAgent(agentName, agent); err != nil {
			log.Printf("❌ Error updating agent capabilities: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		log.Printf("✅ Updated agent %s capabilities and role", agentName)

		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"agent":   agentName,
		})

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// DelegateHandler handles task delegation between agents
// POST: Delegate a task to another agent
func (h *Handler) DelegateHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		WorkspaceID string                 `json:"workspace_id"`
		From        string                 `json:"from"`
		To          string                 `json:"to"`
		Description string                 `json:"description"`
		Priority    int                    `json:"priority"`
		Context     map[string]interface{} `json:"context"`
		Timeout     int                    `json:"timeout"` // timeout in seconds
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Validate required fields
	if req.WorkspaceID == "" {
		http.Error(w, "workspace_id is required", http.StatusBadRequest)
		return
	}
	if req.From == "" {
		http.Error(w, "from is required", http.StatusBadRequest)
		return
	}
	if req.To == "" {
		http.Error(w, "to is required", http.StatusBadRequest)
		return
	}
	if req.Description == "" {
		http.Error(w, "description is required", http.StatusBadRequest)
		return
	}

	// Default priority to 3 (medium) if not specified
	if req.Priority == 0 {
		req.Priority = 3
	}

	// Convert timeout from seconds to duration
	timeout := time.Duration(req.Timeout) * time.Second
	if req.Timeout == 0 {
		timeout = 5 * time.Minute // Default timeout
	}

	// Delegate task
	task, err := h.communicator.DelegateTask(agentcomm.DelegationRequest{
		WorkspaceID: req.WorkspaceID,
		From:        req.From,
		To:          req.To,
		Description: req.Description,
		Priority:    req.Priority,
		Context:     req.Context,
		Timeout:     timeout,
	})

	if err != nil {
		log.Printf("❌ Failed to delegate task: %v", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(agentcomm.DelegationResponse{
		TaskID:    task.ID,
		Status:    string(task.Status),
		CreatedAt: task.CreatedAt,
	})
}

// TasksHandler handles task queries
// GET: Get task by ID or list tasks for workspace/agent
// PUT: Update task status
func (h *Handler) TasksHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	switch r.Method {
	case http.MethodGet:
		h.handleGetTasks(w, r)
	case http.MethodPut:
		h.handleUpdateTask(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// handleGetTasks retrieves tasks
func (h *Handler) handleGetTasks(w http.ResponseWriter, r *http.Request) {
	taskID := r.URL.Query().Get("id")
	workspaceID := r.URL.Query().Get("workspace_id")
	agentName := r.URL.Query().Get("agent")

	if taskID != "" {
		// Get specific task
		task, err := h.communicator.GetTask(taskID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(task)
		return
	}

	if workspaceID != "" {
		// List tasks for workspace
		tasks := h.communicator.ListTasks(workspaceID)
		stats := h.communicator.GetTaskStats(workspaceID)

		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"tasks": tasks,
			"stats": stats,
			"count": len(tasks),
		})
		return
	}

	if agentName != "" {
		// List tasks for agent
		tasks := h.communicator.ListTasksForAgent(agentName)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"tasks": tasks,
			"count": len(tasks),
		})
		return
	}

	http.Error(w, "id, workspace_id, or agent parameter required", http.StatusBadRequest)
}

// handleUpdateTask updates a task's status
func (h *Handler) handleUpdateTask(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TaskID string `json:"task_id"`
		Status string `json:"status"`
		Result string `json:"result"`
		Error  string `json:"error"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	if req.TaskID == "" {
		http.Error(w, "task_id is required", http.StatusBadRequest)
		return
	}
	if req.Status == "" {
		http.Error(w, "status is required", http.StatusBadRequest)
		return
	}

	// Update task status
	err := h.communicator.UpdateTaskStatus(
		req.TaskID,
		agentcomm.TaskStatus(req.Status),
		req.Result,
		req.Error,
	)

	if err != nil {
		log.Printf("❌ Failed to update task status: %v", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"task_id": req.TaskID,
		"status":  req.Status,
	})
}

// WorkflowStatusHandler returns the status of a workspace workflow
// GET /api/orchestration/workflow/status?workspace_id=<id>
func (h *Handler) WorkflowStatusHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if h.orchestrator == nil {
		http.Error(w, "orchestrator not initialized", http.StatusInternalServerError)
		return
	}

	workspaceID := r.URL.Query().Get("workspace_id")
	if workspaceID == "" {
		http.Error(w, "workspace_id is required", http.StatusBadRequest)
		return
	}

	// Get workflow status from orchestrator
	status, err := h.orchestrator.GetWorkflowStatus(workspaceID)
	if err != nil {
		log.Printf("❌ Failed to get workflow status: %v", err)
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	_ = json.NewEncoder(w).Encode(status)
}

// WorkflowStatusStreamHandler streams real-time workflow status updates using Server-Sent Events (SSE)
// GET /api/orchestration/workflow/stream?workspace_id=<id>
func (h *Handler) WorkflowStatusStreamHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if h.orchestrator == nil {
		http.Error(w, "orchestrator not initialized", http.StatusInternalServerError)
		return
	}

	workspaceID := r.URL.Query().Get("workspace_id")
	if workspaceID == "" {
		http.Error(w, "workspace_id is required", http.StatusBadRequest)
		return
	}

	// Set headers for SSE
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// Get flusher
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	// Create ticker for periodic updates (every 2 seconds)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	// Context with cancellation
	ctx := r.Context()

	log.Printf("🔄 Starting SSE stream for workspace %s", workspaceID)

	// Send initial status immediately
	status, err := h.orchestrator.GetWorkflowStatus(workspaceID)
	if err == nil {
		data, _ := json.Marshal(status)
		_, _ = w.Write([]byte(fmt.Sprintf("data: %s\n\n", data)))
		flusher.Flush()
	}

	// Stream updates
	for {
		select {
		case <-ctx.Done():
			// Client disconnected
			log.Printf("⏹  SSE stream closed for workspace %s", workspaceID)
			return

		case <-ticker.C:
			// Send status update
			status, err := h.orchestrator.GetWorkflowStatus(workspaceID)
			if err != nil {
				// Workspace might have been deleted or completed
				log.Printf("❌ Failed to get status for workspace %s: %v", workspaceID, err)
				_, _ = w.Write([]byte("event: error\ndata: workspace not found\n\n"))
				flusher.Flush()
				return
			}

			// Send status as JSON
			data, err := json.Marshal(status)
			if err != nil {
				log.Printf("❌ Failed to marshal status: %v", err)
				continue
			}

			_, err = w.Write([]byte(fmt.Sprintf("data: %s\n\n", data)))
			if err != nil {
				log.Printf("❌ Failed to write SSE data: %v", err)
				return
			}
			flusher.Flush()

			// If workflow is completed, send completion event and close
			if status.Phase == "completed" {
				_, _ = w.Write([]byte("event: complete\ndata: workflow completed\n\n"))
				flusher.Flush()
				log.Printf("✅ Workflow %s completed, closing SSE stream", workspaceID)
				return
			}
		}
	}
}

// TemplatesHandler handles workflow template operations
// GET: List all templates or get specific template by ID
// POST: Create new custom template
// DELETE: Delete custom template
func (h *Handler) TemplatesHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if h.templateManager == nil {
		http.Error(w, "template manager not initialized", http.StatusInternalServerError)
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.handleGetTemplates(w, r)
	case http.MethodPost:
		h.handleCreateTemplate(w, r)
	case http.MethodDelete:
		h.handleDeleteTemplate(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// handleGetTemplates retrieves workflow templates
func (h *Handler) handleGetTemplates(w http.ResponseWriter, r *http.Request) {
	templateID := r.URL.Query().Get("id")
	category := r.URL.Query().Get("category")

	if templateID != "" {
		// Get specific template
		template, err := h.templateManager.GetTemplate(templateID)
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
		templateList = h.templateManager.ListTemplatesByCategory(category)
	} else {
		templateList = h.templateManager.ListTemplates()
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"templates": templateList,
		"count":     len(templateList),
	})
}

// handleCreateTemplate creates a new custom workflow template
func (h *Handler) handleCreateTemplate(w http.ResponseWriter, r *http.Request) {
	var template templates.WorkflowTemplate
	if err := json.NewDecoder(r.Body).Decode(&template); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	// Save template
	if err := h.templateManager.SaveTemplate(&template); err != nil {
		log.Printf("❌ Failed to save template: %v", err)
		http.Error(w, fmt.Sprintf("failed to save template: %v", err), http.StatusInternalServerError)
		return
	}

	log.Printf("✅ Created workflow template: %s", template.ID)
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(template)
}

// handleDeleteTemplate deletes a custom workflow template
func (h *Handler) handleDeleteTemplate(w http.ResponseWriter, r *http.Request) {
	templateID := r.URL.Query().Get("id")
	if templateID == "" {
		http.Error(w, "template id required", http.StatusBadRequest)
		return
	}

	if err := h.templateManager.DeleteTemplate(templateID); err != nil {
		log.Printf("❌ Failed to delete template %s: %v", templateID, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	log.Printf("🗑️  Deleted workflow template: %s", templateID)
	w.WriteHeader(http.StatusNoContent)
}

// InstantiateTemplateHandler handles instantiating a workflow from a template
// POST: Create workflow instance from template with parameters
func (h *Handler) InstantiateTemplateHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	if h.templateManager == nil {
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
	instance, err := h.templateManager.InstantiateTemplate(req.TemplateID, req.Parameters)
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
	result, err := h.orchestrator.ExecuteCollaborativeTask(r.Context(), req.AgentName, task)
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
