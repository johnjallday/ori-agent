package orchestrationhttp

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/johnjallday/ori-agent/internal/agentcomm"
	"github.com/johnjallday/ori-agent/internal/filewatcher"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/orchestration"
	"github.com/johnjallday/ori-agent/internal/orchestration/templates"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// HandlerConfig holds all dependencies for the orchestration handler.
// This provides clear, upfront dependency declaration and validation.
//
// Required fields:
//   - AgentStore: Agent storage backend
//   - WorkspaceStore: Workspace storage backend
//   - EventBus: Event bus for inter-component communication
//
// Optional fields (enable additional functionality):
//   - Orchestrator: Enables workflow orchestration endpoints
//   - TemplateManager: Enables template-based workflow creation
//   - NotificationService: Enables notification endpoints
//   - TaskHandler: Enables task execution endpoints
type HandlerConfig struct {
	// Required dependencies
	AgentStore     store.Store
	WorkspaceStore workspace.Store
	EventBus       *workspace.EventBus

	// Optional dependencies (enable additional features)
	Orchestrator        *orchestration.Orchestrator
	TemplateManager     *templates.TemplateManager
	NotificationService *workspace.NotificationService
	TaskHandler         workspace.TaskHandler
	SessionStore        SessionStore         // For fetching sessions and session tasks
	FileWatcher         *filewatcher.Watcher // For workspace directory watching
}

// SessionStore interface for fetching session data
type SessionStore interface {
	ListSessionsByWorkspace(ctx context.Context, workspaceID string) ([]SessionListItem, error)
	ListNotesByWorkspace(ctx context.Context, workspaceID string) ([]WorkspaceNoteItem, error)
}

// SessionListItem represents a session for API responses
type SessionListItem struct {
	ID           string    `json:"id"`
	Title        string    `json:"title"`
	AgentName    string    `json:"agent_name"`
	MessageCount int       `json:"message_count"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// WorkspaceNoteItem represents a workspace note for API responses
type WorkspaceNoteItem struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Preview   string    `json:"preview,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Validate checks that all required dependencies are present
func (c *HandlerConfig) Validate() error {
	if c.AgentStore == nil {
		return fmt.Errorf("AgentStore is required")
	}
	if c.WorkspaceStore == nil {
		return fmt.Errorf("WorkspaceStore is required")
	}
	if c.EventBus == nil {
		return fmt.Errorf("EventBus is required")
	}
	return nil
}

// Handler manages orchestration-related HTTP endpoints
type Handler struct {
	agentStore          store.Store
	workspaceStore      workspace.Store
	sessionStore        SessionStore
	communicator        *agentcomm.Communicator
	orchestrator        *orchestration.Orchestrator
	templateManager     *templates.TemplateManager
	eventBus            *workspace.EventBus
	notificationService *workspace.NotificationService
	taskHandler         workspace.TaskHandler
	fileWatcher         *filewatcher.Watcher

	// Sub-handlers for modular organization
	workspaceHandler    *WorkspaceHandler
	messageHandler      *MessageHandler
	capabilitiesHandler *CapabilitiesHandler
	templateHandler     *TemplateHandler
	notificationHandler *NotificationHandler
	streamingHandler    *StreamingHandler
	taskHandlerSub      *TaskHandler
	dynamicAgentHandler *DynamicAgentHandler
}

// NewHandler creates a new orchestration handler with all dependencies.
// Returns an error if required dependencies are missing.
//
// Example usage:
//
//	handler, err := orchestrationhttp.NewHandler(orchestrationhttp.HandlerConfig{
//	    AgentStore:     store,
//	    WorkspaceStore: wsStore,
//	    EventBus:       eventBus,
//	    Orchestrator:   orch,        // optional
//	    TaskHandler:    taskHandler, // optional
//	})
func NewHandler(cfg HandlerConfig) (*Handler, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid handler config: %w", err)
	}

	h := &Handler{
		agentStore:          cfg.AgentStore,
		workspaceStore:      cfg.WorkspaceStore,
		sessionStore:        cfg.SessionStore,
		eventBus:            cfg.EventBus,
		communicator:        agentcomm.NewCommunicator(cfg.WorkspaceStore),
		orchestrator:        cfg.Orchestrator,
		templateManager:     cfg.TemplateManager,
		notificationService: cfg.NotificationService,
		taskHandler:         cfg.TaskHandler,
		fileWatcher:         cfg.FileWatcher,
	}

	// Initialize all sub-handlers
	h.initializeSubHandlers()

	return h, nil
}

// initializeSubHandlers creates all sub-handlers based on available dependencies
func (h *Handler) initializeSubHandlers() {
	// Core sub-handlers (always created - depend only on required fields)
	h.workspaceHandler = NewWorkspaceHandler(h.agentStore, h.workspaceStore, h.eventBus, h.sessionStore)
	if h.fileWatcher != nil {
		h.workspaceHandler.SetFileWatcher(h.fileWatcher)
	}
	h.messageHandler = NewMessageHandler(h.workspaceStore, h.eventBus)
	h.capabilitiesHandler = NewCapabilitiesHandler(h.agentStore, h.workspaceStore, h.communicator, h.eventBus)

	// Optional sub-handlers (created if dependencies are available)
	if h.orchestrator != nil {
		h.streamingHandler = NewStreamingHandler(h.workspaceStore, h.orchestrator, h.eventBus)
		h.dynamicAgentHandler = NewDynamicAgentHandler(h.workspaceStore, h.orchestrator, h.eventBus)
	}

	if h.templateManager != nil && h.orchestrator != nil {
		h.templateHandler = NewTemplateHandler(h.agentStore, h.workspaceStore, h.templateManager, h.orchestrator, h.eventBus)
	}

	if h.notificationService != nil {
		h.notificationHandler = NewNotificationHandler(h.workspaceStore, h.notificationService, h.eventBus)
	}

	if h.taskHandler != nil {
		h.taskHandlerSub = NewTaskHandler(h.workspaceStore, h.communicator, h.taskHandler, h.eventBus)
	}
}

// NewHandlerLegacy creates a new orchestration handler using the legacy pattern.
// Deprecated: Use NewHandler with HandlerConfig instead for better dependency validation.
func NewHandlerLegacy(agentStore store.Store, workspaceStore workspace.Store) *Handler {
	return &Handler{
		agentStore:     agentStore,
		workspaceStore: workspaceStore,
		communicator:   agentcomm.NewCommunicator(workspaceStore),
	}
}

// SetTemplateManager sets the template manager after construction.
// This is needed because template loading happens after handler construction.
func (h *Handler) SetTemplateManager(tm *templates.TemplateManager) {
	h.templateManager = tm
	// Initialize template handler if all dependencies are now available
	if h.orchestrator != nil && h.eventBus != nil && h.templateHandler == nil {
		h.templateHandler = NewTemplateHandler(h.agentStore, h.workspaceStore, h.templateManager, h.orchestrator, h.eventBus)
	}
}

// --- Legacy setter methods (deprecated) ---
// These are kept for backward compatibility with NewHandlerLegacy.
// Use NewHandler with HandlerConfig instead for new code.

// SetEventBus sets the event bus instance.
// Deprecated: Use NewHandler with HandlerConfig instead.
func (h *Handler) SetEventBus(eb *workspace.EventBus) {
	h.eventBus = eb

	// Initialize sub-handlers that require eventBus
	h.workspaceHandler = NewWorkspaceHandler(h.agentStore, h.workspaceStore, eb, h.sessionStore)
	h.messageHandler = NewMessageHandler(h.workspaceStore, eb)
	h.capabilitiesHandler = NewCapabilitiesHandler(h.agentStore, h.workspaceStore, h.communicator, eb)
	h.initializeTemplateHandlerLegacy()
	h.initializeStreamingHandlerLegacy()
	h.initializeTaskHandlerLegacy()

	// Initialize notification handler if notificationService is available
	if h.notificationService != nil {
		h.notificationHandler = NewNotificationHandler(h.workspaceStore, h.notificationService, eb)
	}
}

// SetNotificationService sets the notification service instance.
// Deprecated: Use NewHandler with HandlerConfig instead.
func (h *Handler) SetNotificationService(ns *workspace.NotificationService) {
	h.notificationService = ns
	// Initialize notification handler if eventBus is available
	if h.eventBus != nil {
		h.notificationHandler = NewNotificationHandler(h.workspaceStore, ns, h.eventBus)
	}
}

// SetTaskHandler sets the task handler instance.
// Deprecated: Use NewHandler with HandlerConfig instead.
func (h *Handler) SetTaskHandler(th workspace.TaskHandler) {
	h.taskHandler = th
	h.initializeTaskHandlerLegacy()
}

// SetOrchestrator sets the orchestrator instance.
// Deprecated: Use NewHandler with HandlerConfig instead.
func (h *Handler) SetOrchestrator(orch *orchestration.Orchestrator) {
	h.orchestrator = orch
	h.initializeTemplateHandlerLegacy()
	h.initializeStreamingHandlerLegacy()
	if h.orchestrator != nil && h.dynamicAgentHandler == nil {
		h.dynamicAgentHandler = NewDynamicAgentHandler(h.workspaceStore, h.orchestrator, h.eventBus)
	}
}

// initializeTemplateHandlerLegacy initializes the template handler if all dependencies are available (legacy)
func (h *Handler) initializeTemplateHandlerLegacy() {
	if h.templateManager != nil && h.orchestrator != nil && h.eventBus != nil && h.templateHandler == nil {
		h.templateHandler = NewTemplateHandler(h.agentStore, h.workspaceStore, h.templateManager, h.orchestrator, h.eventBus)
	}
}

// initializeStreamingHandlerLegacy initializes the streaming handler if all dependencies are available (legacy)
func (h *Handler) initializeStreamingHandlerLegacy() {
	if h.orchestrator != nil && h.eventBus != nil && h.streamingHandler == nil {
		h.streamingHandler = NewStreamingHandler(h.workspaceStore, h.orchestrator, h.eventBus)
	}
}

// initializeTaskHandlerLegacy initializes the task handler if all dependencies are available (legacy)
func (h *Handler) initializeTaskHandlerLegacy() {
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

// WorkspaceActivateHandler starts watching workspace directories on page load.
func (h *Handler) WorkspaceActivateHandler(w http.ResponseWriter, r *http.Request) {
	h.workspaceHandler.ActivateHandler(w, r)
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

// DynamicAgentApprovalHandler handles dynamic agent approvals.
func (h *Handler) DynamicAgentApprovalHandler(w http.ResponseWriter, r *http.Request) {
	if h.dynamicAgentHandler == nil {
		orihttp.ServiceUnavailable(w, "dynamic agent approvals not available")
		return
	}
	h.dynamicAgentHandler.DynamicAgentApprovalHandler(w, r)
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

// WorkflowCreateHandler accepts a parent task plus N subtasks and persists
// them atomically. Used by the workflow modal builder and the
// "Break this into steps" flow on the task detail page.
//
// Delegates to TaskHandler for modular organization.
func (h *Handler) WorkflowCreateHandler(w http.ResponseWriter, r *http.Request) {
	h.taskHandlerSub.HandleCreateWorkflow(w, r)
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

// TasksPathHandler handles requests to /api/orchestration/tasks/{id}...
// Routes to appropriate handler based on path and method
// Delegates to TaskHandler for modular organization
func (h *Handler) TasksPathHandler(w http.ResponseWriter, r *http.Request) {
	h.taskHandlerSub.TasksPathHandler(w, r)
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

// BulkDeleteTasksHandler handles bulk deletion of tasks
// Delegates to TaskHandler for modular organization
func (h *Handler) BulkDeleteTasksHandler(w http.ResponseWriter, r *http.Request) {
	h.taskHandlerSub.BulkDeleteTasksHandler(w, r)
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

// SchedulerNodesHandler handles listing and creating scheduler nodes (canvas-based scheduled tasks)
// Delegates to TaskHandler for modular organization
func (h *Handler) SchedulerNodesHandler(w http.ResponseWriter, r *http.Request) {
	h.taskHandlerSub.SchedulerNodesHandler(w, r)
}

// SchedulerNodeHandler handles get/update/delete for a specific scheduler node
// Delegates to TaskHandler for modular organization
func (h *Handler) SchedulerNodeHandler(w http.ResponseWriter, r *http.Request) {
	h.taskHandlerSub.SchedulerNodeHandler(w, r)
}

// SchedulerNodeTriggerHandler handles manual triggering of a scheduler node
// Delegates to TaskHandler for modular organization
func (h *Handler) SchedulerNodeTriggerHandler(w http.ResponseWriter, r *http.Request) {
	h.taskHandlerSub.SchedulerNodeTriggerHandler(w, r)
}
