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
	SessionStore        SessionStore                    // For fetching sessions and session tasks
	FileWatcher         *filewatcher.Watcher            // Deprecated fallback for workspace directory watching
	DirectorySync       *workspace.DirectorySyncManager // Lazy workspace directory watching
	// FolderStore is the canonical folder-based workspace.json store, used to
	// hydrate fields with no SQLite column (currently: Designation) that a
	// plain WorkspaceStore.Get (SQLite-primary) never carries.
	FolderStore *workspace.FileStore
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
	// taskCapabilityGate is remembered here so it survives being set before the
	// task sub-handler is constructed (the server wires it across build phases).
	taskCapabilityGate      workspace.TaskCapabilityGate
	taskCapabilityValidator workspace.TaskCapabilityValidator
	taskFileFallback        workspace.TaskFileFallbackPreparer
	agentStore              store.Store
	workspaceStore          workspace.Store
	sessionStore            SessionStore
	communicator            *agentcomm.Communicator
	orchestrator            *orchestration.Orchestrator
	templateManager         *templates.TemplateManager
	eventBus                *workspace.EventBus
	notificationService     *workspace.NotificationService
	taskHandler             workspace.TaskHandler
	fileWatcher             *filewatcher.Watcher
	directorySync           *workspace.DirectorySyncManager
	folderStore             *workspace.FileStore

	// Sub-handlers for modular organization
	workspaceHandler    *WorkspaceHandler
	messageHandler      *MessageHandler
	capabilitiesHandler *CapabilitiesHandler
	templateHandler     *TemplateHandler
	notificationHandler *NotificationHandler
	streamingHandler    *StreamingHandler
	taskHandlerSub      *TaskHandler
	dynamicAgentHandler *DynamicAgentHandler
	backlogHandler      *BacklogHandler
	ticketHandler       *TicketHandler
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
		directorySync:       cfg.DirectorySync,
		folderStore:         cfg.FolderStore,
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
	if h.directorySync != nil {
		h.workspaceHandler.SetDirectorySync(h.directorySync)
	}
	if h.folderStore != nil {
		h.workspaceHandler.SetFolderStore(h.folderStore)
	}
	h.messageHandler = NewMessageHandler(h.workspaceStore, h.eventBus)
	h.capabilitiesHandler = NewCapabilitiesHandler(h.agentStore, h.workspaceStore, h.communicator, h.eventBus)
	backlogFileSync := workspace.NewFileBacklogSynchronizer(h.workspaceStore)
	backlogService := workspace.NewBacklogService(h.workspaceStore)
	backlogService.SetEventBus(h.eventBus)
	backlogService.SetSynchronizer(backlogFileSync)
	h.backlogHandler = NewBacklogHandler(backlogService)
	h.backlogHandler.SetFileSynchronizer(backlogFileSync)
	h.ticketHandler = NewTicketHandler(newTicketService(h.workspaceStore, h.eventBus))

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
		h.taskHandlerSub.SetCapabilityGate(h.taskCapabilityGate)
		h.taskHandlerSub.SetCapabilityValidator(h.taskCapabilityValidator)
		h.taskHandlerSub.SetFileFallbackPreparer(h.taskFileFallback)
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
	legacyBacklogFileSync := workspace.NewFileBacklogSynchronizer(h.workspaceStore)
	backlogService := workspace.NewBacklogService(h.workspaceStore)
	backlogService.SetEventBus(eb)
	backlogService.SetSynchronizer(legacyBacklogFileSync)
	h.backlogHandler = NewBacklogHandler(backlogService)
	h.backlogHandler.SetFileSynchronizer(legacyBacklogFileSync)
	h.ticketHandler = NewTicketHandler(newTicketService(h.workspaceStore, eb))
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

// SetTaskCapabilityGate wires the connection-precondition check consulted before
// a task executes. Safe to call before or after the task sub-handler exists: the
// gate is remembered either way.
func (h *Handler) SetTaskCapabilityGate(gate workspace.TaskCapabilityGate) {
	if h == nil {
		return
	}
	h.taskCapabilityGate = gate
	if h.taskHandlerSub != nil {
		h.taskHandlerSub.SetCapabilityGate(gate)
	}
}

func (h *Handler) SetTaskCapabilityValidator(validator workspace.TaskCapabilityValidator) {
	if h == nil {
		return
	}
	h.taskCapabilityValidator = validator
	if h.taskHandlerSub != nil {
		h.taskHandlerSub.SetCapabilityValidator(validator)
	}
}

func (h *Handler) SetTaskFileFallbackPreparer(preparer workspace.TaskFileFallbackPreparer) {
	if h == nil {
		return
	}
	h.taskFileFallback = preparer
	if h.taskHandlerSub != nil {
		h.taskHandlerSub.SetFileFallbackPreparer(preparer)
	}
}

// initializeTaskHandlerLegacy initializes the task handler if all dependencies are available (legacy)
func (h *Handler) initializeTaskHandlerLegacy() {
	if h.eventBus != nil && h.taskHandler != nil && h.taskHandlerSub == nil {
		h.taskHandlerSub = NewTaskHandler(h.workspaceStore, h.communicator, h.taskHandler, h.eventBus)
		h.taskHandlerSub.SetCapabilityGate(h.taskCapabilityGate)
		h.taskHandlerSub.SetCapabilityValidator(h.taskCapabilityValidator)
		h.taskHandlerSub.SetFileFallbackPreparer(h.taskFileFallback)
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

// StartTaskAsync starts one task through the same execution path as the
// manual execute endpoint. It is the non-HTTP seam the template-setup
// first-open auto-start uses (injected into sessionhttp by the server
// builder).
func (h *Handler) StartTaskAsync(workspaceID, taskID string) error {
	if h == nil || h.taskHandlerSub == nil {
		return fmt.Errorf("task execution not available")
	}
	return h.taskHandlerSub.StartTaskAsync(workspaceID, taskID)
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

// UpcomingScheduledTasksHandler aggregates the next-N enabled scheduled tasks
// across every workspace. Powers the home dashboard's Upcoming section.
func (h *Handler) UpcomingScheduledTasksHandler(w http.ResponseWriter, r *http.Request) {
	h.taskHandlerSub.UpcomingScheduledTasksHandler(w, r)
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

// BacklogListHandler handles GET (list) and POST (create) on
// /api/orchestration/backlog. Delegates to BacklogHandler.
func (h *Handler) BacklogListHandler(w http.ResponseWriter, r *http.Request) {
	h.backlogHandler.BacklogListHandler(w, r)
}

// BacklogItemPathHandler handles /api/orchestration/backlog/{id}[/promote],
// reorder, and sync. Delegates to BacklogHandler.
func (h *Handler) BacklogItemPathHandler(w http.ResponseWriter, r *http.Request) {
	h.backlogHandler.BacklogItemPathHandler(w, r)
}

// --- Canonical Ticket API (tasks/prd-workspace-ticket-management.md) -------
//
// These are the owner-scoped product routes. The legacy task/backlog handlers
// above remain as compatibility adapters during the migration window.

// TicketCollectionHandler handles GET/POST /api/workspaces/{studio_id}/tickets.
func (h *Handler) TicketCollectionHandler(w http.ResponseWriter, r *http.Request) {
	h.ticketHandler.TicketCollectionHandler(w, r)
}

// TicketItemHandler handles GET/PATCH/DELETE on
// /api/workspaces/{studio_id}/tickets/{ticket_id}.
func (h *Handler) TicketItemHandler(w http.ResponseWriter, r *http.Request) {
	h.ticketHandler.TicketItemHandler(w, r)
}

// TicketTransitionHandler handles POST
// /api/workspaces/{studio_id}/tickets/{ticket_id}/transition.
func (h *Handler) TicketTransitionHandler(w http.ResponseWriter, r *http.Request) {
	h.ticketHandler.TicketTransitionHandler(w, r)
}

// TicketReorderHandler handles POST
// /api/workspaces/{studio_id}/tickets/reorder.
func (h *Handler) TicketReorderHandler(w http.ResponseWriter, r *http.Request) {
	h.ticketHandler.TicketReorderHandler(w, r)
}

// TicketNoteLinkHandler handles GET/POST/DELETE on
// /api/workspaces/{studio_id}/tickets/{ticket_id}/notes.
func (h *Handler) TicketNoteLinkHandler(w http.ResponseWriter, r *http.Request) {
	h.ticketHandler.TicketNoteLinkHandler(w, r)
}

// TicketsForNoteHandler handles GET
// /api/workspaces/{studio_id}/notes/{note_id}/tickets.
func (h *Handler) TicketsForNoteHandler(w http.ResponseWriter, r *http.Request) {
	h.ticketHandler.TicketsForNoteHandler(w, r)
}

// TicketFromNoteHandler handles POST
// /api/workspaces/{studio_id}/notes/{note_id}/tickets.
func (h *Handler) TicketFromNoteHandler(w http.ResponseWriter, r *http.Request) {
	h.ticketHandler.TicketFromNoteHandler(w, r)
}

// SetTicketNoteLookup wires Note validation into the canonical Ticket service.
// Without it, link operations are refused rather than accepting unvalidated
// Note IDs.
func (h *Handler) SetTicketNoteLookup(lookup workspace.TicketNoteLookup) {
	if h.ticketHandler == nil {
		return
	}
	h.ticketHandler.SetNoteLookup(lookup)
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

// SaveStationLayoutHandler handles saving HQ command-map station positions,
// scoped separately from canvas layout so the two writers never clobber each
// other. Delegates to WorkspaceHandler for modular organization.
func (h *Handler) SaveStationLayoutHandler(w http.ResponseWriter, r *http.Request) {
	h.workspaceHandler.SaveStationLayoutHandler(w, r)
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
