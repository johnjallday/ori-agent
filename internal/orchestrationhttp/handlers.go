package orchestrationhttp

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/johnjallday/ori-agent/internal/agentcomm"
	"github.com/johnjallday/ori-agent/internal/agentstudio"
	"github.com/johnjallday/ori-agent/internal/orchestration"
	"github.com/johnjallday/ori-agent/internal/orchestration/templates"
	"github.com/johnjallday/ori-agent/internal/store"
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
	workspaceHandler    *WorkspaceHandler
	messageHandler      *MessageHandler
	capabilitiesHandler *CapabilitiesHandler
	templateHandler     *TemplateHandler
	notificationHandler *NotificationHandler
	streamingHandler    *StreamingHandler
	taskHandlerSub      *TaskHandler
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
	h.messageHandler = NewMessageHandler(h.workspaceStore, eb)
	h.capabilitiesHandler = NewCapabilitiesHandler(h.agentStore, h.workspaceStore, h.communicator, eb)
	h.initializeTemplateHandler()
	h.initializeStreamingHandler()
	h.initializeTaskHandler()

	// Initialize notification handler if notificationService is available
	if h.notificationService != nil {
		h.notificationHandler = NewNotificationHandler(h.workspaceStore, h.notificationService, eb)
	}
}

// SetNotificationService sets the notification service instance
func (h *Handler) SetNotificationService(ns *agentstudio.NotificationService) {
	h.notificationService = ns
	// Initialize notification handler if eventBus is available
	if h.eventBus != nil {
		h.notificationHandler = NewNotificationHandler(h.workspaceStore, ns, h.eventBus)
	}
}

// SetTaskHandler sets the task handler instance
func (h *Handler) SetTaskHandler(th agentstudio.TaskHandler) {
	h.taskHandler = th
	h.initializeTaskHandler()
}

// SetOrchestrator sets the orchestrator instance
func (h *Handler) SetOrchestrator(orch *orchestration.Orchestrator) {
	h.orchestrator = orch
	h.initializeTemplateHandler()
	h.initializeStreamingHandler()
}

// SetTemplateManager sets the template manager instance
func (h *Handler) SetTemplateManager(tm *templates.TemplateManager) {
	h.templateManager = tm
	h.initializeTemplateHandler()
}

// initializeTemplateHandler initializes the template handler if all dependencies are available
func (h *Handler) initializeTemplateHandler() {
	if h.templateManager != nil && h.orchestrator != nil && h.eventBus != nil && h.templateHandler == nil {
		h.templateHandler = NewTemplateHandler(h.agentStore, h.workspaceStore, h.templateManager, h.orchestrator, h.eventBus)
	}
}

// initializeStreamingHandler initializes the streaming handler if all dependencies are available
func (h *Handler) initializeStreamingHandler() {
	if h.orchestrator != nil && h.eventBus != nil && h.streamingHandler == nil {
		h.streamingHandler = NewStreamingHandler(h.workspaceStore, h.orchestrator, h.eventBus)
	}
}

// initializeTaskHandler initializes the task handler if all dependencies are available
func (h *Handler) initializeTaskHandler() {
	if h.eventBus != nil && h.taskHandler != nil && h.taskHandlerSub == nil {
		h.taskHandlerSub = NewTaskHandler(h.workspaceStore, h.communicator, h.taskHandler, h.eventBus)
	}
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
// Delegates to MessageHandler for modular organization
func (h *Handler) MessagesHandler(w http.ResponseWriter, r *http.Request) {
	h.messageHandler.MessagesHandler(w, r)
}

// AgentCapabilitiesHandler handles agent capability management
// Delegates to CapabilitiesHandler for modular organization
func (h *Handler) AgentCapabilitiesHandler(w http.ResponseWriter, r *http.Request) {
	h.capabilitiesHandler.AgentCapabilitiesHandler(w, r)
}

// DelegateHandler handles task delegation between agents
// Delegates to CapabilitiesHandler for modular organization
func (h *Handler) DelegateHandler(w http.ResponseWriter, r *http.Request) {
	h.capabilitiesHandler.DelegateHandler(w, r)
}

// TasksHandler handles task queries
// Delegates to TaskHandler for modular organization
func (h *Handler) TasksHandler(w http.ResponseWriter, r *http.Request) {
	h.taskHandlerSub.TasksHandler(w, r)
}

// TaskResultsHandler retrieves results from one or more tasks
// Delegates to TaskHandler for modular organization
func (h *Handler) TaskResultsHandler(w http.ResponseWriter, r *http.Request) {
	h.taskHandlerSub.TaskResultsHandler(w, r)
}

// WorkflowStatusHandler returns the status of a workspace workflow
// Delegates to StreamingHandler for modular organization
func (h *Handler) WorkflowStatusHandler(w http.ResponseWriter, r *http.Request) {
	h.streamingHandler.WorkflowStatusHandler(w, r)
}

// WorkflowStatusStreamHandler streams real-time workflow status updates using Server-Sent Events (SSE)
// Delegates to StreamingHandler for modular organization
func (h *Handler) WorkflowStatusStreamHandler(w http.ResponseWriter, r *http.Request) {
	h.streamingHandler.WorkflowStatusStreamHandler(w, r)
}

// TemplatesHandler handles workflow template operations
// Delegates to TemplateHandler for modular organization
func (h *Handler) TemplatesHandler(w http.ResponseWriter, r *http.Request) {
	h.templateHandler.TemplatesHandler(w, r)
}

// InstantiateTemplateHandler handles instantiating a workflow from a template
// Delegates to TemplateHandler for modular organization
func (h *Handler) InstantiateTemplateHandler(w http.ResponseWriter, r *http.Request) {
	h.templateHandler.InstantiateTemplateHandler(w, r)
}

// NotificationsHandler handles notification operations
// Delegates to NotificationHandler for modular organization
func (h *Handler) NotificationsHandler(w http.ResponseWriter, r *http.Request) {
	h.notificationHandler.NotificationsHandler(w, r)
}

// NotificationStreamHandler streams notifications using Server-Sent Events (SSE)
// Delegates to NotificationHandler for modular organization
func (h *Handler) NotificationStreamHandler(w http.ResponseWriter, r *http.Request) {
	h.notificationHandler.NotificationStreamHandler(w, r)
}

// EventHistoryHandler retrieves event history
// Delegates to NotificationHandler for modular organization
func (h *Handler) EventHistoryHandler(w http.ResponseWriter, r *http.Request) {
	h.notificationHandler.EventHistoryHandler(w, r)
}

// ExecuteTaskHandler handles manual task execution
// Delegates to TaskHandler for modular organization
func (h *Handler) ExecuteTaskHandler(w http.ResponseWriter, r *http.Request) {
	h.taskHandlerSub.ExecuteTaskHandler(w, r)
}

// ScheduledTasksHandler handles listing and creating scheduled tasks
// Delegates to TaskHandler for modular organization
func (h *Handler) ScheduledTasksHandler(w http.ResponseWriter, r *http.Request) {
	h.taskHandlerSub.ScheduledTasksHandler(w, r)
}

// ScheduledTaskHandler handles get/update/delete for a specific scheduled task
// Delegates to TaskHandler for modular organization
func (h *Handler) ScheduledTaskHandler(w http.ResponseWriter, r *http.Request) {
	h.taskHandlerSub.ScheduledTaskHandler(w, r)
}

// ProgressStreamHandler streams real-time progress updates using Server-Sent Events (SSE)
// Delegates to StreamingHandler for modular organization
func (h *Handler) ProgressStreamHandler(w http.ResponseWriter, r *http.Request) {
	h.streamingHandler.ProgressStreamHandler(w, r)
}

// SaveLayoutHandler handles saving canvas layout positions
// SaveLayoutHandler saves workspace canvas layout
// Delegates to WorkspaceHandler for modular organization
func (h *Handler) SaveLayoutHandler(w http.ResponseWriter, r *http.Request) {
	h.workspaceHandler.SaveLayoutHandler(w, r)
}
