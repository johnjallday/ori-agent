package orchestrationhttp

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/agentcomm"
	"github.com/johnjallday/ori-agent/internal/agentstudio"
	"github.com/johnjallday/ori-agent/internal/orchestration"
	"github.com/johnjallday/ori-agent/internal/orchestration/templates"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
)

// Handler manages orchestration-related HTTP endpoints
type Handler struct {
	agentStore          store.Store
	workspaceStore      agentstudio.Store
	communicator        *agentcomm.Communicator
	orchestrator        *orchestration.Orchestrator
	templateManager     *templates.TemplateManager
	eventBus            *agentstudio.EventBus
	notificationService *agentstudio.NotificationService
	taskHandler         agentstudio.TaskHandler

	// Sub-handlers for modular organization
	workspaceHandler *WorkspaceHandler
}

// NewHandler creates a new orchestration handler
func NewHandler(agentStore store.Store, workspaceStore agentstudio.Store) *Handler {
	return &Handler{
		agentStore:     agentStore,
		workspaceStore: workspaceStore,
		communicator:   agentcomm.NewCommunicator(workspaceStore),
	}
}

// SetEventBus sets the event bus instance
func (h *Handler) SetEventBus(eb *agentstudio.EventBus) {
	h.eventBus = eb

	// Initialize sub-handlers that require eventBus
	h.workspaceHandler = NewWorkspaceHandler(h.agentStore, h.workspaceStore, eb)
}

// SetNotificationService sets the notification service instance
func (h *Handler) SetNotificationService(ns *agentstudio.NotificationService) {
	h.notificationService = ns
}

// SetTaskHandler sets the task handler instance
func (h *Handler) SetTaskHandler(th agentstudio.TaskHandler) {
	h.taskHandler = th
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
// Delegates to WorkspaceHandler for modular organization
func (h *Handler) WorkspaceHandler(w http.ResponseWriter, r *http.Request) {
	h.workspaceHandler.WorkspaceHandler(w, r)
}

// handleGetWorkspace retrieves workspace(s)
func (h *Handler) handleGetWorkspace(w http.ResponseWriter, r *http.Request) {
	// Check if a specific workspace ID is requested
	wsID := r.URL.Query().Get("id")
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
		Description string                 `json:"description"`
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

	// Verify all participating agents exist
	for _, agentName := range req.Agents {
		_, ok := h.agentStore.GetAgent(agentName)
		if !ok {
			http.Error(w, "agent not found: "+agentName, http.StatusNotFound)
			return
		}
	}

	// Create workspace
	ws := agentstudio.NewWorkspace(agentstudio.CreateWorkspaceParams{
		Name:        req.Name,
		Description: req.Description,
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

	// Publish workspace created event
	if h.eventBus != nil {
		event := agentstudio.NewWorkspaceEvent(
			agentstudio.EventWorkspaceCreated,
			ws.ID,
			"api",
			map[string]interface{}{
				"name":        req.Name,
				"description": req.Description,
				"agents":      req.Agents,
			},
		)
		h.eventBus.Publish(event)
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"studio_id":  ws.ID,
		"status":     ws.Status,
		"created_at": ws.CreatedAt,
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
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Workspace deleted successfully",
	})
}

// WorkspaceAgentsHandler handles adding/removing agents from workspace
// Delegates to WorkspaceHandler for modular organization
func (h *Handler) WorkspaceAgentsHandler(w http.ResponseWriter, r *http.Request) {
	h.workspaceHandler.WorkspaceAgentsHandler(w, r)
}

// handleAddAgentToWorkspace adds an agent to a workspace
func (h *Handler) handleAddAgentToWorkspace(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WorkspaceID string `json:"studio_id"`
		AgentName   string `json:"agent_name"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	if req.WorkspaceID == "" {
		http.Error(w, "workspace_id is required", http.StatusBadRequest)
		return
	}
	if req.AgentName == "" {
		http.Error(w, "agent_name is required", http.StatusBadRequest)
		return
	}

	// Verify agent exists
	_, ok := h.agentStore.GetAgent(req.AgentName)
	if !ok {
		http.Error(w, "agent not found: "+req.AgentName, http.StatusNotFound)
		return
	}

	// Get workspace
	ws, err := h.workspaceStore.Get(req.WorkspaceID)
	if err != nil {
		log.Printf("❌ Error getting workspace %s: %v", req.WorkspaceID, err)
		http.Error(w, "Workspace not found: "+err.Error(), http.StatusNotFound)
		return
	}

	// Add agent
	if err := ws.AddAgent(req.AgentName); err != nil {
		log.Printf("❌ Error adding agent to workspace: %v", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Save workspace
	if err := h.workspaceStore.Save(ws); err != nil {
		log.Printf("❌ Error saving workspace: %v", err)
		http.Error(w, "Failed to save workspace: "+err.Error(), http.StatusInternalServerError)
		return
	}

	log.Printf("✅ Added agent %s to workspace %s", req.AgentName, req.WorkspaceID)

	// Publish event
	if h.eventBus != nil {
		event := agentstudio.NewWorkspaceEvent(
			agentstudio.EventWorkspaceUpdated,
			req.WorkspaceID,
			"api",
			map[string]interface{}{
				"action": "agent_added",
				"agent":  req.AgentName,
			},
		)
		h.eventBus.Publish(event)
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Agent added successfully",
		"agent":   req.AgentName,
		"agents":  ws.Agents,
	})
}

// handleRemoveAgentFromWorkspace removes an agent from a workspace
func (h *Handler) handleRemoveAgentFromWorkspace(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.URL.Query().Get("studio_id")
	agentName := r.URL.Query().Get("agent_name")

	if workspaceID == "" {
		http.Error(w, "workspace_id parameter required", http.StatusBadRequest)
		return
	}
	if agentName == "" {
		http.Error(w, "agent_name parameter required", http.StatusBadRequest)
		return
	}

	// Get workspace
	ws, err := h.workspaceStore.Get(workspaceID)
	if err != nil {
		log.Printf("❌ Error getting workspace %s: %v", workspaceID, err)
		http.Error(w, "Workspace not found: "+err.Error(), http.StatusNotFound)
		return
	}

	// Remove agent
	if err := ws.RemoveAgent(agentName); err != nil {
		log.Printf("❌ Error removing agent from workspace: %v", err)
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	// Save workspace
	if err := h.workspaceStore.Save(ws); err != nil {
		log.Printf("❌ Error saving workspace: %v", err)
		http.Error(w, "Failed to save workspace: "+err.Error(), http.StatusInternalServerError)
		return
	}

	log.Printf("✅ Removed agent %s from workspace %s", agentName, workspaceID)

	// Publish event
	if h.eventBus != nil {
		event := agentstudio.NewWorkspaceEvent(
			agentstudio.EventWorkspaceUpdated,
			workspaceID,
			"api",
			map[string]interface{}{
				"action": "agent_removed",
				"agent":  agentName,
			},
		)
		h.eventBus.Publish(event)
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Agent removed successfully",
		"agent":   agentName,
		"agents":  ws.Agents,
	})
}

// MessagesHandler handles workspace message operations
// GET: Retrieve messages from workspace
// POST: Send message to workspace
func (h *Handler) MessagesHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	wsID := r.URL.Query().Get("studio_id")
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
func (h *Handler) handleGetMessages(w http.ResponseWriter, r *http.Request, ws *agentstudio.Workspace) {
	agentName := r.URL.Query().Get("agent")
	sinceStr := r.URL.Query().Get("since")

	var messages []agentstudio.AgentMessage

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
func (h *Handler) handleSendMessage(w http.ResponseWriter, r *http.Request, ws *agentstudio.Workspace) {
	var msg agentstudio.AgentMessage

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
		WorkspaceID string                 `json:"studio_id"`
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
	case http.MethodPost:
		h.handleCreateTask(w, r)
	case http.MethodPut:
		h.handleUpdateTask(w, r)
	case http.MethodDelete:
		h.handleDeleteTask(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// handleGetTasks retrieves tasks
func (h *Handler) handleGetTasks(w http.ResponseWriter, r *http.Request) {
	taskID := r.URL.Query().Get("id")
	workspaceID := r.URL.Query().Get("studio_id")
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
// handleCreateTask creates a new task in a workspace
func (h *Handler) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WorkspaceID            string   `json:"studio_id"`
		From                   string   `json:"from"`
		To                     string   `json:"to"`
		Description            string   `json:"description"`
		Priority               int      `json:"priority"`
		InputTaskIDs           []string `json:"input_task_ids"`
		ResultCombinationMode  string   `json:"result_combination_mode"`
		CombinationInstruction string   `json:"combination_instruction"`
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
		http.Error(w, "from (sender agent) is required", http.StatusBadRequest)
		return
	}
	if req.To == "" {
		http.Error(w, "to (recipient agent) is required", http.StatusBadRequest)
		return
	}
	if req.Description == "" {
		http.Error(w, "description is required", http.StatusBadRequest)
		return
	}

	// Get workspace
	ws, err := h.workspaceStore.Get(req.WorkspaceID)
	if err != nil {
		log.Printf("❌ Error getting workspace %s: %v", req.WorkspaceID, err)
		http.Error(w, "Workspace not found: "+err.Error(), http.StatusNotFound)
		return
	}

	// Create task
	task := agentstudio.Task{
		WorkspaceID:            req.WorkspaceID,
		From:                   req.From,
		To:                     req.To,
		Description:            req.Description,
		Priority:               req.Priority,
		InputTaskIDs:           req.InputTaskIDs,
		ResultCombinationMode:  req.ResultCombinationMode,
		CombinationInstruction: req.CombinationInstruction,
		Status:                 agentstudio.TaskStatusPending,
	}

	// Add task to workspace
	if err := ws.AddTask(task); err != nil {
		log.Printf("❌ Failed to add task to workspace: %v", err)
		http.Error(w, "Failed to add task: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Save workspace
	if err := h.workspaceStore.Save(ws); err != nil {
		log.Printf("❌ Failed to save workspace: %v", err)
		http.Error(w, "Failed to save workspace: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Get the task we just added (it now has an ID)
	// Find the most recently added task with matching properties
	var createdTask *agentstudio.Task
	for i := len(ws.Tasks) - 1; i >= 0; i-- {
		if ws.Tasks[i].Description == req.Description && ws.Tasks[i].From == req.From && ws.Tasks[i].To == req.To {
			createdTask = &ws.Tasks[i]
			break
		}
	}

	if createdTask == nil {
		log.Printf("❌ Could not find created task")
		http.Error(w, "Task created but could not be retrieved", http.StatusInternalServerError)
		return
	}

	if len(req.InputTaskIDs) > 0 {
		log.Printf("✅ Created connected task %s in workspace %s: %s -> %s (receiving input from %d task(s))",
			createdTask.ID, req.WorkspaceID, req.From, req.To, len(req.InputTaskIDs))
	} else {
		log.Printf("✅ Created task %s in workspace %s: %s -> %s", createdTask.ID, req.WorkspaceID, req.From, req.To)
	}

	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"task":    createdTask,
	})
}

func (h *Handler) handleUpdateTask(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TaskID                 string   `json:"task_id"`
		Status                 string   `json:"status"`
		Result                 string   `json:"result"`
		Error                  string   `json:"error"`
		To                     *string  `json:"to"`                      // Optional: reassign task to different agent
		InputTaskIDs           []string `json:"input_task_ids"`          // Optional: update input task connections
		ResultCombinationMode  *string  `json:"result_combination_mode"` // Optional: update combination mode
		CombinationInstruction *string  `json:"combination_instruction"` // Optional: update combination instruction
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Extract task ID from URL path if present (e.g., /api/orchestration/tasks/{id})
	pathParts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/orchestration/tasks/"), "/")
	if len(pathParts) > 0 && pathParts[0] != "" {
		req.TaskID = pathParts[0]
	}

	if req.TaskID == "" {
		http.Error(w, "task_id is required", http.StatusBadRequest)
		return
	}

	// Handle input task connections update
	if req.InputTaskIDs != nil {
		log.Printf("🔗 Updating input connections for task %s", req.TaskID)

		// Get workspace from task
		task, err := h.communicator.GetTask(req.TaskID)
		if err != nil {
			log.Printf("❌ Failed to get task: %v", err)
			http.Error(w, "Task not found: "+err.Error(), http.StatusNotFound)
			return
		}

		ws, err := h.workspaceStore.Get(task.WorkspaceID)
		if err != nil {
			log.Printf("❌ Failed to get workspace: %v", err)
			http.Error(w, "Workspace not found: "+err.Error(), http.StatusNotFound)
			return
		}

		// Update the task's input connections
		taskFound := false
		for i := range ws.Tasks {
			if ws.Tasks[i].ID == req.TaskID {
				ws.Tasks[i].InputTaskIDs = req.InputTaskIDs
				if req.ResultCombinationMode != nil {
					ws.Tasks[i].ResultCombinationMode = *req.ResultCombinationMode
				}
				if req.CombinationInstruction != nil {
					ws.Tasks[i].CombinationInstruction = *req.CombinationInstruction
				}
				taskFound = true
				log.Printf("📝 Updated task %s input connections: %v", req.TaskID, req.InputTaskIDs)
				break
			}
		}

		if !taskFound {
			log.Printf("❌ Task %s not found in workspace %s", req.TaskID, task.WorkspaceID)
			http.Error(w, "Task not found in workspace", http.StatusNotFound)
			return
		}

		// Save workspace
		if err := h.workspaceStore.Save(ws); err != nil {
			log.Printf("❌ Failed to save workspace: %v", err)
			http.Error(w, "Failed to update task: "+err.Error(), http.StatusInternalServerError)
			return
		}

		log.Printf("✅ Updated input connections for task %s", req.TaskID)

		// Publish event
		if h.eventBus != nil {
			h.eventBus.Publish(agentstudio.Event{
				Type:        agentstudio.EventWorkspaceUpdated,
				WorkspaceID: task.WorkspaceID,
				Data: map[string]interface{}{
					"task_id":        req.TaskID,
					"input_task_ids": req.InputTaskIDs,
					"update_type":    "task_connections",
				},
			})
		}

		// Return updated task
		updatedTask, _ := h.communicator.GetTask(req.TaskID)
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(updatedTask)
		return
	}

	// Handle task reassignment (changing "to" field)
	if req.To != nil {
		log.Printf("🔄 Reassigning task %s to %s", req.TaskID, *req.To)

		// Get workspace from task
		task, err := h.communicator.GetTask(req.TaskID)
		if err != nil {
			log.Printf("❌ Failed to get task: %v", err)
			http.Error(w, "Task not found: "+err.Error(), http.StatusNotFound)
			return
		}

		ws, err := h.workspaceStore.Get(task.WorkspaceID)
		if err != nil {
			log.Printf("❌ Failed to get workspace: %v", err)
			http.Error(w, "Workspace not found: "+err.Error(), http.StatusNotFound)
			return
		}

		// Update the task assignment
		taskFound := false
		for i := range ws.Tasks {
			if ws.Tasks[i].ID == req.TaskID {
				ws.Tasks[i].To = *req.To
				taskFound = true
				log.Printf("📝 Updated task in workspace: %s -> %s", req.TaskID, *req.To)
				break
			}
		}

		if !taskFound {
			log.Printf("❌ Task %s not found in workspace %s", req.TaskID, task.WorkspaceID)
			http.Error(w, "Task not found in workspace", http.StatusNotFound)
			return
		}

		// Save workspace
		if err := h.workspaceStore.Save(ws); err != nil {
			log.Printf("❌ Failed to save workspace: %v", err)
			http.Error(w, "Failed to update task: "+err.Error(), http.StatusInternalServerError)
			return
		}

		log.Printf("✅ Reassigned task %s to %s", req.TaskID, *req.To)

		// Publish event
		h.eventBus.Publish(agentstudio.Event{
			Type:        agentstudio.EventTaskAssigned,
			WorkspaceID: task.WorkspaceID,
			Data: map[string]interface{}{
				"task_id": req.TaskID,
				"to":      *req.To,
			},
		})

		// Return updated task
		updatedTask, _ := h.communicator.GetTask(req.TaskID)
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(updatedTask)
		return
	}

	// Handle status update
	if req.Status == "" {
		http.Error(w, "status is required when not reassigning task", http.StatusBadRequest)
		return
	}

	// Update task status
	err := h.communicator.UpdateTaskStatus(
		req.TaskID,
		agentstudio.TaskStatus(req.Status),
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

// handleDeleteTask deletes a task
func (h *Handler) handleDeleteTask(w http.ResponseWriter, r *http.Request) {
	taskID := r.URL.Query().Get("id")
	if taskID == "" {
		http.Error(w, "id parameter required", http.StatusBadRequest)
		return
	}

	// Delete task
	err := h.communicator.DeleteTask(taskID)
	if err != nil {
		log.Printf("❌ Failed to delete task: %v", err)
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	log.Printf("✅ Deleted task: %s", taskID)
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Task deleted successfully",
		"task_id": taskID,
	})
}

// TaskResultsHandler retrieves results from one or more tasks
// GET /api/orchestration/task-results?task_ids=id1,id2,id3
func (h *Handler) TaskResultsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// Get task IDs from query parameter
	taskIDsStr := r.URL.Query().Get("task_ids")
	if taskIDsStr == "" {
		http.Error(w, "task_ids parameter required (comma-separated)", http.StatusBadRequest)
		return
	}

	// Split comma-separated task IDs
	taskIDs := strings.Split(taskIDsStr, ",")
	for i := range taskIDs {
		taskIDs[i] = strings.TrimSpace(taskIDs[i])
	}

	// We need to find the workspace that contains these tasks
	// For simplicity, we'll search through all workspaces
	workspaceIDs, err := h.workspaceStore.List()
	if err != nil {
		log.Printf("❌ Error listing workspaces: %v", err)
		http.Error(w, "Failed to retrieve workspaces", http.StatusInternalServerError)
		return
	}

	// Collect results from all workspaces
	allResults := make(map[string]interface{})
	for _, wsID := range workspaceIDs {
		ws, err := h.workspaceStore.Get(wsID)
		if err != nil {
			log.Printf("⚠️ Error getting workspace %s: %v", wsID, err)
			continue
		}

		results := ws.GetTaskResults(taskIDs)
		for taskID, result := range results {
			// Get full task info
			task, err := ws.GetTask(taskID)
			if err == nil {
				allResults[taskID] = map[string]interface{}{
					"task_id":      task.ID,
					"description":  task.Description,
					"status":       task.Status,
					"result":       result,
					"from":         task.From,
					"to":           task.To,
					"completed_at": task.CompletedAt,
				}
			} else {
				allResults[taskID] = map[string]interface{}{
					"task_id": taskID,
					"result":  result,
				}
			}
		}
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"results": allResults,
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

	workspaceID := r.URL.Query().Get("studio_id")
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

	workspaceID := r.URL.Query().Get("studio_id")
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

	// Context with cancellation
	ctx := r.Context()

	log.Printf("🔄 Starting SSE stream for workspace %s", workspaceID)

	// Use event bus if available for real-time updates
	if h.eventBus != nil {
		h.streamEventsFromBus(ctx, w, flusher, workspaceID)
	} else {
		// Fallback to polling-based streaming
		h.streamEventsFromPolling(ctx, w, flusher, workspaceID)
	}
}

// streamEventsFromBus streams events using the event bus (real-time)
func (h *Handler) streamEventsFromBus(ctx context.Context, w http.ResponseWriter, flusher http.Flusher, workspaceID string) {
	// Create event channel
	eventChan := make(chan agentstudio.Event, 50)

	// Subscribe to workspace events
	subID := h.eventBus.SubscribeToWorkspace(workspaceID, func(event agentstudio.Event) {
		select {
		case eventChan <- event:
		default:
			log.Printf("⚠️  Event channel full for workspace %s", workspaceID)
		}
	})
	defer h.eventBus.Unsubscribe(subID)

	// Also create a ticker for periodic status updates (every 5 seconds)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	// Send initial status immediately
	h.sendWorkspaceStatus(w, flusher, workspaceID)

	// Stream events
	for {
		select {
		case <-ctx.Done():
			// Client disconnected
			log.Printf("⏹  SSE stream closed for workspace %s", workspaceID)
			return

		case event := <-eventChan:
			// Send event to client
			eventData := map[string]interface{}{
				"type":      event.Type,
				"studio_id": event.WorkspaceID,
				"timestamp": event.Timestamp,
				"source":    event.Source,
				"data":      event.Data,
			}

			data, err := json.Marshal(eventData)
			if err != nil {
				log.Printf("❌ Failed to marshal event: %v", err)
				continue
			}

			// Send with event type prefix
			_, err = w.Write([]byte(fmt.Sprintf("event: %s\ndata: %s\n\n", event.Type, data)))
			if err != nil {
				log.Printf("❌ Failed to write SSE event: %v", err)
				return
			}
			flusher.Flush()

			// Check for completion events
			if event.Type == agentstudio.EventWorkspaceCompleted || event.Type == agentstudio.EventWorkflowCompleted {
				log.Printf("✅ Workspace %s completed, closing SSE stream", workspaceID)
				return
			}

		case <-ticker.C:
			// Send periodic status update
			h.sendWorkspaceStatus(w, flusher, workspaceID)
		}
	}
}

// streamEventsFromPolling streams events using polling (fallback)
func (h *Handler) streamEventsFromPolling(ctx context.Context, w http.ResponseWriter, flusher http.Flusher, workspaceID string) {
	if h.orchestrator == nil {
		http.Error(w, "orchestrator not initialized and event bus not available", http.StatusInternalServerError)
		return
	}

	// Create ticker for periodic updates (every 2 seconds)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

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

// sendWorkspaceStatus sends the current workspace status
func (h *Handler) sendWorkspaceStatus(w http.ResponseWriter, flusher http.Flusher, workspaceID string) {
	// Try to get workspace
	ws, err := h.workspaceStore.Get(workspaceID)
	if err != nil {
		return
	}

	statusData := map[string]interface{}{
		"studio_id":  ws.ID,
		"status":     ws.Status,
		"updated_at": ws.UpdatedAt,
	}

	// Add workflow status if orchestrator is available
	if h.orchestrator != nil {
		workflowStatus, err := h.orchestrator.GetWorkflowStatus(workspaceID)
		if err == nil {
			statusData["workflow"] = workflowStatus
		}
	}

	data, err := json.Marshal(statusData)
	if err != nil {
		return
	}

	_, err = w.Write([]byte(fmt.Sprintf("event: status\ndata: %s\n\n", data)))
	if err == nil {
		flusher.Flush()
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

// NotificationsHandler handles notification operations
// GET: Retrieve notifications for an agent
// POST: Mark notification(s) as read
func (h *Handler) NotificationsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if h.notificationService == nil {
		http.Error(w, "notification service not initialized", http.StatusServiceUnavailable)
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.handleGetNotifications(w, r)
	case http.MethodPost:
		h.handleMarkNotificationsRead(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// handleGetNotifications retrieves notifications
func (h *Handler) handleGetNotifications(w http.ResponseWriter, r *http.Request) {
	agentName := r.URL.Query().Get("agent")
	unreadOnly := r.URL.Query().Get("unread") == "true"

	if agentName != "" && unreadOnly {
		// Get unread notifications for agent
		notifications := h.notificationService.GetUnreadForAgent(agentName)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"notifications": notifications,
			"count":         len(notifications),
		})
		return
	}

	// Get notification history
	limit := 50
	notifications := h.notificationService.GetHistory(limit)

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"notifications": notifications,
		"count":         len(notifications),
	})
}

// handleMarkNotificationsRead marks notifications as read
func (h *Handler) handleMarkNotificationsRead(w http.ResponseWriter, r *http.Request) {
	var req struct {
		NotificationID string `json:"notification_id,omitempty"`
		AgentName      string `json:"agent_name,omitempty"`
		MarkAll        bool   `json:"mark_all,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.MarkAll && req.AgentName != "" {
		// Mark all notifications for agent as read
		h.notificationService.MarkAllAsRead(req.AgentName)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "All notifications marked as read",
		})
		return
	}

	if req.NotificationID != "" {
		// Mark specific notification as read
		h.notificationService.MarkAsRead(req.NotificationID)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "Notification marked as read",
		})
		return
	}

	http.Error(w, "notification_id or agent_name with mark_all required", http.StatusBadRequest)
}

// NotificationStreamHandler streams notifications using Server-Sent Events (SSE)
// GET /api/orchestration/notifications/stream?agent=<name>
func (h *Handler) NotificationStreamHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if h.notificationService == nil {
		http.Error(w, "notification service not initialized", http.StatusServiceUnavailable)
		return
	}

	agentName := r.URL.Query().Get("agent")
	if agentName == "" {
		http.Error(w, "agent parameter required", http.StatusBadRequest)
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

	// Subscribe to notifications
	notifChan := h.notificationService.Subscribe(agentName)
	defer h.notificationService.Unsubscribe(agentName)

	// Context with cancellation
	ctx := r.Context()

	log.Printf("🔔 Starting notification stream for agent %s", agentName)

	// Send initial unread notifications
	unread := h.notificationService.GetUnreadForAgent(agentName)
	if len(unread) > 0 {
		data, _ := json.Marshal(map[string]interface{}{
			"notifications": unread,
			"count":         len(unread),
		})
		_, _ = w.Write([]byte(fmt.Sprintf("event: initial\ndata: %s\n\n", data)))
		flusher.Flush()
	}

	// Stream notifications
	for {
		select {
		case <-ctx.Done():
			// Client disconnected
			log.Printf("🔕 Notification stream closed for agent %s", agentName)
			return

		case notification, ok := <-notifChan:
			if !ok {
				// Channel closed
				log.Printf("🔕 Notification channel closed for agent %s", agentName)
				return
			}

			// Send notification to client
			data, err := json.Marshal(notification)
			if err != nil {
				log.Printf("❌ Failed to marshal notification: %v", err)
				continue
			}

			_, err = w.Write([]byte(fmt.Sprintf("event: notification\ndata: %s\n\n", data)))
			if err != nil {
				log.Printf("❌ Failed to write notification: %v", err)
				return
			}
			flusher.Flush()
		}
	}
}

// EventHistoryHandler retrieves event history
// GET /api/orchestration/events?workspace_id=<id>&limit=<n>&since=<timestamp>
func (h *Handler) EventHistoryHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if h.eventBus == nil {
		http.Error(w, "event bus not initialized", http.StatusServiceUnavailable)
		return
	}

	workspaceID := r.URL.Query().Get("studio_id")
	sinceStr := r.URL.Query().Get("since")
	limit := 100 // Default limit

	var events []agentstudio.Event

	if sinceStr != "" {
		// Get events since timestamp
		since, err := time.Parse(time.RFC3339, sinceStr)
		if err != nil {
			http.Error(w, "invalid since timestamp (use RFC3339)", http.StatusBadRequest)
			return
		}
		events = h.eventBus.GetEventsSince(since, limit)
	} else if workspaceID != "" {
		// Get events for workspace
		events = h.eventBus.GetWorkspaceHistory(workspaceID, limit)
	} else {
		// Get general event history
		events = h.eventBus.GetHistory(nil, limit)
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"events": events,
		"count":  len(events),
	})
}

// ExecuteTaskHandler handles manual task execution
func (h *Handler) ExecuteTaskHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		TaskID string `json:"task_id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	if req.TaskID == "" {
		http.Error(w, "task_id is required", http.StatusBadRequest)
		return
	}

	// Find the task across all workspaces
	workspaceIDs, err := h.workspaceStore.List()
	if err != nil {
		log.Printf("❌ Error listing workspaces: %v", err)
		http.Error(w, "Failed to list workspaces: "+err.Error(), http.StatusInternalServerError)
		return
	}

	var foundWorkspace *agentstudio.Workspace
	var foundTask *agentstudio.Task

	for _, wsID := range workspaceIDs {
		ws, err := h.workspaceStore.Get(wsID)
		if err != nil {
			continue
		}

		task, err := ws.GetTask(req.TaskID)
		if err == nil {
			foundWorkspace = ws
			foundTask = task
			break
		}
	}

	if foundTask == nil {
		http.Error(w, fmt.Sprintf("Task %s not found", req.TaskID), http.StatusNotFound)
		return
	}

	// Check if task is in a state that can be executed
	if foundTask.Status == agentstudio.TaskStatusCompleted {
		http.Error(w, "Task already completed", http.StatusBadRequest)
		return
	}

	if foundTask.Status == agentstudio.TaskStatusInProgress {
		http.Error(w, "Task is already in progress", http.StatusBadRequest)
		return
	}

	// Check if task handler is available
	if h.taskHandler == nil {
		log.Printf("❌ Task handler not set")
		http.Error(w, "Task execution not available", http.StatusInternalServerError)
		return
	}

	// Execute the task immediately in a goroutine
	go func() {
		ctx := context.Background()

		// Update task status to in_progress
		foundTask.Status = agentstudio.TaskStatusInProgress
		now := time.Now()
		foundTask.StartedAt = &now

		if err := foundWorkspace.UpdateTask(*foundTask); err != nil {
			log.Printf("❌ Failed to update task status: %v", err)
			return
		}
		if err := h.workspaceStore.Save(foundWorkspace); err != nil {
			log.Printf("❌ Failed to save workspace: %v", err)
			return
		}

		// Publish task started event
		if h.eventBus != nil {
			event := agentstudio.NewTaskEvent(agentstudio.EventTaskStarted, foundWorkspace.ID, foundTask.ID, foundTask.To, map[string]interface{}{
				"description": foundTask.Description,
				"priority":    foundTask.Priority,
				"manual":      true,
			})
			h.eventBus.Publish(event)
		}

		log.Printf("▶️  Manually executing task %s for agent %s: %s", foundTask.ID, foundTask.To, foundTask.Description)

		// Inject input task results into task context if InputTaskIDs are specified
		if len(foundTask.InputTaskIDs) > 0 {
			log.Printf("🔗 Task %s has %d input task IDs: %v", foundTask.ID, len(foundTask.InputTaskIDs), foundTask.InputTaskIDs)
			enrichedContext := foundWorkspace.GetInputContext(foundTask)
			foundTask.Context = enrichedContext

			// Debug: Check what was added to context
			if inputResults, ok := enrichedContext["input_task_results"]; ok {
				resultsMap := inputResults.(map[string]string)
				log.Printf("📥 Injected %d input task results into task %s context", len(resultsMap), foundTask.ID)
				for taskID, result := range resultsMap {
					preview := result
					if len(preview) > 100 {
						preview = preview[:100] + "..."
					}
					log.Printf("   - Task %s result: %s", taskID, preview)
				}
			} else {
				log.Printf("⚠️  Warning: No input results found for task %s despite having InputTaskIDs", foundTask.ID)
			}
		} else {
			log.Printf("ℹ️  Task %s has no input task IDs", foundTask.ID)
		}

		// Execute the task
		result, execErr := h.taskHandler.ExecuteTask(ctx, foundTask.To, *foundTask)

		// Reload workspace (may have changed)
		ws, err := h.workspaceStore.Get(foundWorkspace.ID)
		if err != nil {
			log.Printf("❌ Failed to reload workspace %s: %v", foundWorkspace.ID, err)
			return
		}

		// Find the task in the reloaded workspace
		task, err := ws.GetTask(foundTask.ID)
		if err != nil {
			log.Printf("❌ Task %s not found in workspace after execution", foundTask.ID)
			return
		}

		// Update task with result
		completedAt := time.Now()
		task.CompletedAt = &completedAt

		if execErr != nil {
			log.Printf("❌ Task %s failed: %v", task.ID, execErr)
			task.Status = agentstudio.TaskStatusFailed
			task.Error = execErr.Error()

			// Publish task failed event
			if h.eventBus != nil {
				event := agentstudio.NewTaskEvent(agentstudio.EventTaskFailed, ws.ID, task.ID, task.To, map[string]interface{}{
					"description": task.Description,
					"error":       execErr.Error(),
					"manual":      true,
				})
				h.eventBus.Publish(event)
			}
		} else {
			log.Printf("✅ Task %s completed successfully", task.ID)
			task.Status = agentstudio.TaskStatusCompleted
			task.Result = result

			// Publish task completed event
			if h.eventBus != nil {
				event := agentstudio.NewTaskEvent(agentstudio.EventTaskCompleted, ws.ID, task.ID, task.To, map[string]interface{}{
					"description": task.Description,
					"result":      result,
					"manual":      true,
				})
				h.eventBus.Publish(event)
			}
		}

		// Save updated task
		if err := ws.UpdateTask(*task); err != nil {
			log.Printf("❌ Failed to update task: %v", err)
			return
		}
		if err := h.workspaceStore.Save(ws); err != nil {
			log.Printf("❌ Failed to save workspace: %v", err)
		}

		// Publish workspace updated event
		if h.eventBus != nil {
			event := agentstudio.NewWorkspaceEvent(agentstudio.EventWorkspaceUpdated, ws.ID, "manual-execution", map[string]interface{}{
				"task_id": task.ID,
				"status":  task.Status,
			})
			h.eventBus.Publish(event)
		}
	}()

	log.Printf("✅ Started manual execution of task %s", req.TaskID)

	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Task execution started",
		"task_id": req.TaskID,
	})
}

// ScheduledTasksHandler handles listing and creating scheduled tasks
func (h *Handler) ScheduledTasksHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.handleListScheduledTasks(w, r)
	case http.MethodPost:
		h.handleCreateScheduledTask(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleListScheduledTasks lists all scheduled tasks for a workspace
func (h *Handler) handleListScheduledTasks(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.URL.Query().Get("studio_id")
	if workspaceID == "" {
		http.Error(w, "workspace_id is required", http.StatusBadRequest)
		return
	}

	ws, err := h.workspaceStore.Get(workspaceID)
	if err != nil {
		log.Printf("❌ Error getting workspace %s: %v", workspaceID, err)
		http.Error(w, "Workspace not found: "+err.Error(), http.StatusNotFound)
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"scheduled_tasks": ws.ScheduledTasks,
		"count":           len(ws.ScheduledTasks),
	})
}

// handleCreateScheduledTask creates a new scheduled task
func (h *Handler) handleCreateScheduledTask(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WorkspaceID string                     `json:"studio_id"`
		Name        string                     `json:"name"`
		Description string                     `json:"description"`
		From        string                     `json:"from"`
		To          string                     `json:"to"`
		Prompt      string                     `json:"prompt"`
		Priority    int                        `json:"priority"`
		Schedule    agentstudio.ScheduleConfig `json:"schedule"`
		Enabled     bool                       `json:"enabled"`
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
	if req.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	if req.Prompt == "" {
		http.Error(w, "prompt is required", http.StatusBadRequest)
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

	// Get workspace
	ws, err := h.workspaceStore.Get(req.WorkspaceID)
	if err != nil {
		log.Printf("❌ Error getting workspace %s: %v", req.WorkspaceID, err)
		http.Error(w, "Workspace not found: "+err.Error(), http.StatusNotFound)
		return
	}

	// Create scheduled task
	now := time.Now()
	st := agentstudio.ScheduledTask{
		WorkspaceID: req.WorkspaceID,
		Name:        req.Name,
		Description: req.Description,
		From:        req.From,
		To:          req.To,
		Prompt:      req.Prompt,
		Priority:    req.Priority,
		Schedule:    req.Schedule,
		Enabled:     req.Enabled,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	// Calculate initial NextRun if enabled
	if st.Enabled {
		nextRun := calculateInitialNextRun(st.Schedule, now)
		st.NextRun = nextRun
	}

	// Add to workspace
	if err := ws.AddScheduledTask(st); err != nil {
		log.Printf("❌ Failed to add scheduled task: %v", err)
		http.Error(w, "Failed to add scheduled task: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Save workspace
	if err := h.workspaceStore.Save(ws); err != nil {
		log.Printf("❌ Failed to save workspace: %v", err)
		http.Error(w, "Failed to save workspace: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Get the created scheduled task (now has ID)
	var createdTask *agentstudio.ScheduledTask
	for i := len(ws.ScheduledTasks) - 1; i >= 0; i-- {
		if ws.ScheduledTasks[i].Name == req.Name {
			createdTask = &ws.ScheduledTasks[i]
			break
		}
	}

	log.Printf("✅ Created scheduled task %s in workspace %s: %s", createdTask.ID, req.WorkspaceID, req.Name)

	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success":        true,
		"scheduled_task": createdTask,
	})
}

// ScheduledTaskHandler handles get/update/delete for a specific scheduled task
func (h *Handler) ScheduledTaskHandler(w http.ResponseWriter, r *http.Request) {
	// Extract ID from URL path
	// Path format: /api/orchestration/scheduled-tasks/{id} or /api/orchestration/scheduled-tasks/{id}/{action}
	parts := strings.Split(strings.TrimSuffix(r.URL.Path, "/"), "/")

	// Minimum parts: ["", "api", "orchestration", "scheduled-tasks", "{id}"] = 5
	if len(parts) < 5 {
		http.Error(w, "Invalid URL: missing task ID", http.StatusBadRequest)
		return
	}

	id := parts[4] // The ID is always at index 4

	// Handle special actions (e.g., /api/orchestration/scheduled-tasks/{id}/enable)
	if len(parts) >= 6 {
		action := parts[5]

		switch action {
		case "enable":
			h.handleEnableScheduledTask(w, r, id, true)
			return
		case "disable":
			h.handleEnableScheduledTask(w, r, id, false)
			return
		case "trigger":
			h.handleTriggerScheduledTask(w, r, id)
			return
		default:
			http.Error(w, "Unknown action: "+action, http.StatusBadRequest)
			return
		}
	}

	switch r.Method {
	case http.MethodGet:
		h.handleGetScheduledTask(w, r, id)
	case http.MethodPut:
		h.handleUpdateScheduledTask(w, r, id)
	case http.MethodDelete:
		h.handleDeleteScheduledTask(w, r, id)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleGetScheduledTask retrieves a specific scheduled task
func (h *Handler) handleGetScheduledTask(w http.ResponseWriter, r *http.Request, id string) {
	// Find the scheduled task across all workspaces
	workspaceIDs, err := h.workspaceStore.List()
	if err != nil {
		log.Printf("❌ Error listing workspaces: %v", err)
		http.Error(w, "Failed to list workspaces: "+err.Error(), http.StatusInternalServerError)
		return
	}

	for _, wsID := range workspaceIDs {
		ws, err := h.workspaceStore.Get(wsID)
		if err != nil {
			continue
		}

		st, err := ws.GetScheduledTask(id)
		if err == nil {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"scheduled_task": st,
			})
			return
		}
	}

	http.Error(w, fmt.Sprintf("Scheduled task %s not found", id), http.StatusNotFound)
}

// handleUpdateScheduledTask updates a scheduled task
func (h *Handler) handleUpdateScheduledTask(w http.ResponseWriter, r *http.Request, id string) {
	var req struct {
		Name        *string                     `json:"name,omitempty"`
		Description *string                     `json:"description,omitempty"`
		Prompt      *string                     `json:"prompt,omitempty"`
		Priority    *int                        `json:"priority,omitempty"`
		Schedule    *agentstudio.ScheduleConfig `json:"schedule,omitempty"`
		Enabled     *bool                       `json:"enabled,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Find the scheduled task
	workspaceIDs, err := h.workspaceStore.List()
	if err != nil {
		log.Printf("❌ Error listing workspaces: %v", err)
		http.Error(w, "Failed to list workspaces: "+err.Error(), http.StatusInternalServerError)
		return
	}

	for _, wsID := range workspaceIDs {
		ws, err := h.workspaceStore.Get(wsID)
		if err != nil {
			continue
		}

		st, err := ws.GetScheduledTask(id)
		if err != nil {
			continue
		}

		// Update fields if provided
		if req.Name != nil {
			st.Name = *req.Name
		}
		if req.Description != nil {
			st.Description = *req.Description
		}
		if req.Prompt != nil {
			st.Prompt = *req.Prompt
		}
		if req.Priority != nil {
			st.Priority = *req.Priority
		}
		if req.Schedule != nil {
			st.Schedule = *req.Schedule
			// Recalculate NextRun if schedule changed
			if st.Enabled {
				now := time.Now()
				nextRun := calculateInitialNextRun(st.Schedule, now)
				st.NextRun = nextRun
			}
		}
		if req.Enabled != nil {
			wasEnabled := st.Enabled
			st.Enabled = *req.Enabled

			// Calculate NextRun when enabling
			if st.Enabled && !wasEnabled {
				now := time.Now()
				nextRun := calculateInitialNextRun(st.Schedule, now)
				st.NextRun = nextRun
			} else if !st.Enabled && wasEnabled {
				st.NextRun = nil
			}
		}

		st.UpdatedAt = time.Now()

		if err := ws.UpdateScheduledTask(*st); err != nil {
			log.Printf("❌ Failed to update scheduled task: %v", err)
			http.Error(w, "Failed to update scheduled task: "+err.Error(), http.StatusInternalServerError)
			return
		}

		if err := h.workspaceStore.Save(ws); err != nil {
			log.Printf("❌ Failed to save workspace: %v", err)
			http.Error(w, "Failed to save workspace: "+err.Error(), http.StatusInternalServerError)
			return
		}

		log.Printf("✅ Updated scheduled task %s", id)

		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success":        true,
			"scheduled_task": st,
		})
		return
	}

	http.Error(w, fmt.Sprintf("Scheduled task %s not found", id), http.StatusNotFound)
}

// handleDeleteScheduledTask deletes a scheduled task
func (h *Handler) handleDeleteScheduledTask(w http.ResponseWriter, r *http.Request, id string) {
	workspaceIDs, err := h.workspaceStore.List()
	if err != nil {
		log.Printf("❌ Error listing workspaces: %v", err)
		http.Error(w, "Failed to list workspaces: "+err.Error(), http.StatusInternalServerError)
		return
	}

	for _, wsID := range workspaceIDs {
		ws, err := h.workspaceStore.Get(wsID)
		if err != nil {
			continue
		}

		if err := ws.DeleteScheduledTask(id); err == nil {
			if err := h.workspaceStore.Save(ws); err != nil {
				log.Printf("❌ Failed to save workspace: %v", err)
				http.Error(w, "Failed to save workspace: "+err.Error(), http.StatusInternalServerError)
				return
			}

			log.Printf("✅ Deleted scheduled task %s", id)

			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
			})
			return
		}
	}

	http.Error(w, fmt.Sprintf("Scheduled task %s not found", id), http.StatusNotFound)
}

// handleEnableScheduledTask enables or disables a scheduled task
func (h *Handler) handleEnableScheduledTask(w http.ResponseWriter, r *http.Request, id string, enable bool) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	workspaceIDs, err := h.workspaceStore.List()
	if err != nil {
		log.Printf("❌ Error listing workspaces: %v", err)
		http.Error(w, "Failed to list workspaces: "+err.Error(), http.StatusInternalServerError)
		return
	}

	for _, wsID := range workspaceIDs {
		ws, err := h.workspaceStore.Get(wsID)
		if err != nil {
			continue
		}

		st, err := ws.GetScheduledTask(id)
		if err != nil {
			continue
		}

		st.Enabled = enable
		st.UpdatedAt = time.Now()

		// Calculate NextRun when enabling
		if enable {
			now := time.Now()
			nextRun := calculateInitialNextRun(st.Schedule, now)
			st.NextRun = nextRun
		} else {
			st.NextRun = nil
		}

		if err := ws.UpdateScheduledTask(*st); err != nil {
			log.Printf("❌ Failed to update scheduled task: %v", err)
			http.Error(w, "Failed to update scheduled task: "+err.Error(), http.StatusInternalServerError)
			return
		}

		if err := h.workspaceStore.Save(ws); err != nil {
			log.Printf("❌ Failed to save workspace: %v", err)
			http.Error(w, "Failed to save workspace: "+err.Error(), http.StatusInternalServerError)
			return
		}

		action := "disabled"
		if enable {
			action = "enabled"
		}
		// Capitalize first letter manually (strings.Title is deprecated)
		capitalizedAction := action
		if len(action) > 0 {
			capitalizedAction = string(action[0]-32) + action[1:]
		}
		log.Printf("✅ %s scheduled task %s", capitalizedAction, id)

		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success":        true,
			"enabled":        enable,
			"scheduled_task": st,
		})
		return
	}

	http.Error(w, fmt.Sprintf("Scheduled task %s not found", id), http.StatusNotFound)
}

// handleTriggerScheduledTask manually triggers a scheduled task
func (h *Handler) handleTriggerScheduledTask(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	workspaceIDs, err := h.workspaceStore.List()
	if err != nil {
		log.Printf("❌ Error listing workspaces: %v", err)
		http.Error(w, "Failed to list workspaces: "+err.Error(), http.StatusInternalServerError)
		return
	}

	for _, wsID := range workspaceIDs {
		ws, err := h.workspaceStore.Get(wsID)
		if err != nil {
			continue
		}

		st, err := ws.GetScheduledTask(id)
		if err != nil {
			continue
		}

		// Create a task from the scheduled task
		task := agentstudio.Task{
			WorkspaceID: ws.ID,
			From:        st.From,
			To:          st.To,
			Description: st.Prompt,
			Priority:    st.Priority,
			Context:     st.Context,
			Status:      agentstudio.TaskStatusPending,
		}

		if err := ws.AddTask(task); err != nil {
			log.Printf("❌ Failed to create task from scheduled task: %v", err)
			http.Error(w, "Failed to create task: "+err.Error(), http.StatusBadRequest)
			return
		}

		if err := h.workspaceStore.Save(ws); err != nil {
			log.Printf("❌ Failed to save workspace: %v", err)
			http.Error(w, "Failed to save workspace: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// Get the created task ID
		var taskID string
		if len(ws.Tasks) > 0 {
			taskID = ws.Tasks[len(ws.Tasks)-1].ID
		}

		log.Printf("✅ Manually triggered scheduled task %s, created task %s", id, taskID)

		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"task_id": taskID,
		})
		return
	}

	http.Error(w, fmt.Sprintf("Scheduled task %s not found", id), http.StatusNotFound)
}

// calculateInitialNextRun calculates the initial next run time for a schedule
func calculateInitialNextRun(config agentstudio.ScheduleConfig, now time.Time) *time.Time {
	switch config.Type {
	case agentstudio.ScheduleOnce:
		if config.ExecuteAt != nil {
			return config.ExecuteAt
		}
		return nil

	case agentstudio.ScheduleInterval:
		if config.Interval == 0 {
			return nil
		}
		next := now.Add(config.Interval)
		return &next

	case agentstudio.ScheduleDaily:
		if config.TimeOfDay == "" {
			return nil
		}

		var hour, minute int
		if _, err := fmt.Sscanf(config.TimeOfDay, "%d:%d", &hour, &minute); err != nil {
			return nil
		}

		// Calculate next occurrence
		next := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, now.Location())
		if next.Before(now) || next.Equal(now) {
			// If time has passed today, schedule for tomorrow
			next = next.AddDate(0, 0, 1)
		}

		return &next

	case agentstudio.ScheduleWeekly:
		if config.TimeOfDay == "" {
			return nil
		}

		var hour, minute int
		if _, err := fmt.Sscanf(config.TimeOfDay, "%d:%d", &hour, &minute); err != nil {
			return nil
		}

		targetWeekday := time.Weekday(config.DayOfWeek)
		currentWeekday := now.Weekday()

		daysUntil := int(targetWeekday - currentWeekday)
		if daysUntil < 0 {
			daysUntil += 7
		} else if daysUntil == 0 {
			// Same day - check if time has passed
			testTime := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, now.Location())
			if testTime.Before(now) || testTime.Equal(now) {
				daysUntil = 7 // Next week
			}
		}

		next := time.Date(
			now.Year(),
			now.Month(),
			now.Day()+daysUntil,
			hour,
			minute,
			0,
			0,
			now.Location(),
		)

		return &next

	default:
		return nil
	}
}

// ProgressStreamHandler streams real-time progress updates using Server-Sent Events (SSE)
// GET /api/orchestration/progress/stream?workspace_id=<id>
func (h *Handler) ProgressStreamHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
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

	ctx := r.Context()

	// If no event bus, return error
	if h.eventBus == nil {
		http.Error(w, "event bus not available", http.StatusServiceUnavailable)
		return
	}

	// Create event channel
	eventChan := make(chan agentstudio.Event, 100)

	// Subscribe to workspace events
	subID := h.eventBus.SubscribeToWorkspace(workspaceID, func(event agentstudio.Event) {
		select {
		case eventChan <- event:
		default:
			log.Printf("⚠️  Progress event channel full for workspace %s", workspaceID)
		}
	})
	defer h.eventBus.Unsubscribe(subID)

	// Send initial workspace progress
	h.sendInitialProgress(w, flusher, workspaceID)

	// Create ticker for periodic workspace progress updates (every 10 seconds)
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	// Stream events
	for {
		select {
		case <-ctx.Done():
			return

		case event := <-eventChan:
			// Send event to client
			eventData := map[string]interface{}{
				"type":         event.Type,
				"workspace_id": event.WorkspaceID,
				"timestamp":    event.Timestamp,
				"source":       event.Source,
				"data":         event.Data,
			}

			data, err := json.Marshal(eventData)
			if err != nil {
				log.Printf("❌ Failed to marshal progress event: %v", err)
				continue
			}

			// Send with event type prefix
			_, err = w.Write([]byte(fmt.Sprintf("event: %s\ndata: %s\n\n", event.Type, data)))
			if err != nil {
				log.Printf("❌ Failed to write progress SSE event: %v", err)
				return
			}
			flusher.Flush()

			// After any task event, send updated workspace progress
			if strings.HasPrefix(string(event.Type), "task.") {
				h.sendWorkspaceProgressUpdate(w, flusher, workspaceID)
			}

		case <-ticker.C:
			// Send periodic workspace progress update
			h.sendWorkspaceProgressUpdate(w, flusher, workspaceID)
		}
	}
}

// sendInitialProgress sends the initial workspace progress
func (h *Handler) sendInitialProgress(w http.ResponseWriter, flusher http.Flusher, workspaceID string) {
	ws, err := h.workspaceStore.Get(workspaceID)
	if err != nil {
		log.Printf("❌ Failed to get workspace for initial progress: %v", err)
		return
	}

	progress := ws.GetWorkspaceProgress()
	agentStats := ws.GetAgentStats()

	data := map[string]interface{}{
		"workspace_id":       workspaceID,
		"workspace_progress": progress,
		"agent_stats":        agentStats,
		"tasks":              ws.Tasks,
	}

	jsonData, err := json.Marshal(data)
	if err != nil {
		log.Printf("❌ Failed to marshal initial progress: %v", err)
		return
	}

	_, _ = w.Write([]byte(fmt.Sprintf("event: initial\ndata: %s\n\n", jsonData)))
	flusher.Flush()
}

// sendWorkspaceProgressUpdate sends a workspace progress update
func (h *Handler) sendWorkspaceProgressUpdate(w http.ResponseWriter, flusher http.Flusher, workspaceID string) {
	ws, err := h.workspaceStore.Get(workspaceID)
	if err != nil {
		return // Workspace might have been deleted
	}

	progress := ws.GetWorkspaceProgress()
	agentStats := ws.GetAgentStats()

	eventData := map[string]interface{}{
		"type":               "workspace.progress",
		"workspace_id":       workspaceID,
		"timestamp":          time.Now(),
		"workspace_progress": progress,
		"agent_stats":        agentStats,
	}

	data, err := json.Marshal(eventData)
	if err != nil {
		log.Printf("❌ Failed to marshal workspace progress: %v", err)
		return
	}

	_, _ = w.Write([]byte(fmt.Sprintf("event: workspace.progress\ndata: %s\n\n", data)))
	flusher.Flush()
}

// SaveLayoutHandler handles saving canvas layout positions
// SaveLayoutHandler saves workspace canvas layout
// Delegates to WorkspaceHandler for modular organization
func (h *Handler) SaveLayoutHandler(w http.ResponseWriter, r *http.Request) {
	h.workspaceHandler.SaveLayoutHandler(w, r)
}
