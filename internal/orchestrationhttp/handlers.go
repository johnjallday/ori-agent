package orchestrationhttp

import (
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

// WorkspaceAgentsHandler handles adding/removing agents from workspace
// Delegates to WorkspaceHandler for modular organization
func (h *Handler) WorkspaceAgentsHandler(w http.ResponseWriter, r *http.Request) {
	h.workspaceHandler.WorkspaceAgentsHandler(w, r)
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
