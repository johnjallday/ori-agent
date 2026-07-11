package workspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/platform"
)

// HTTPHandler handles HTTP requests for Agent Workspaces
type HTTPHandler struct {
	store          Store
	orchestrator   *Orchestrator
	eventBus       *EventBus
	emailAccounts  emailAccountStore
	folderResolver FolderResolver
	openFile       func(string) error
	// scheduler is the TaskScheduler that owns the MissionTrigger reference.
	// Mission-related HTTP endpoints route through it so they share the same
	// trigger configuration as cadence-driven runs. Optional — handlers
	// that depend on it should fall back to a 503 when nil.
	scheduler *TaskScheduler
}

// NewHTTPHandler creates a new HTTP handler
func NewHTTPHandler(store Store, orchestrator *Orchestrator, eventBus *EventBus) *HTTPHandler {
	handler := &HTTPHandler{
		store:        store,
		orchestrator: orchestrator,
		eventBus:     eventBus,
		openFile:     platform.OpenFile,
	}
	if resolver, ok := store.(FolderResolver); ok {
		handler.folderResolver = resolver
	}
	return handler
}

// SetScheduler wires the task scheduler so mission HTTP endpoints can fire
// mission runs through the same MissionTrigger configured for cadence runs.
func (h *HTTPHandler) SetScheduler(scheduler *TaskScheduler) {
	h.scheduler = scheduler
}

// CreateWorkspaceRequest represents the request to create a new workspace
type CreateWorkspaceRequest struct {
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	Agents         []string `json:"agents"`
	EntryAgentName string   `json:"entry_agent_name,omitempty"`
}

// CreateWorkspace handles POST /api/workspaces
func (h *HTTPHandler) CreateWorkspace(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}

	var req CreateWorkspaceRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	// Validate request
	if req.Name == "" {
		orihttp.BadRequest(w, "Workspace name is required")
		return
	}
	if len(req.Agents) == 0 {
		orihttp.BadRequest(w, "At least one agent is required")
		return
	}

	// Create workspace
	workspace := &Workspace{
		ID:          uuid.New().String(),
		Name:        req.Name,
		Description: req.Description,
		SharedData:  make(map[string]any),
		Messages:    make([]AgentMessage, 0),
		Tasks:       make([]Task, 0),
		Attachments: make([]Attachment, 0),
		Status:      StatusActive,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	for _, agentName := range req.Agents {
		if err := workspace.AddAgent(agentName); err != nil {
			if strings.TrimSpace(agentName) == "" || errors.Is(err, ErrAgentAlreadyInWorkspace) {
				orihttp.BadRequest(w, fmt.Sprintf("Invalid workspace agents: %v", err))
				return
			}
			orihttp.InternalError(w, fmt.Sprintf("Failed to configure workspace agents: %v", err))
			return
		}
	}

	entryAgentName := strings.TrimSpace(req.EntryAgentName)
	if entryAgentName == "" {
		entryAgentName = strings.TrimSpace(req.Agents[0])
	}
	if err := workspace.SetEntryAgentName(entryAgentName); err != nil {
		orihttp.BadRequest(w, fmt.Sprintf("Invalid entry agent: %v", err))
		return
	}

	// Save workspace
	if err := h.store.Save(workspace); err != nil {
		logger.Error("Failed to save workspace", logger.Fields{"error": err})
		orihttp.InternalError(w, "Failed to create workspace")
		return
	}

	agentNames := workspace.AgentNames()
	logger.Info("Created workspace", logger.Fields{"workspace_name": workspace.Name, "workspace_id": workspace.ID, "agents": agentNames})

	// Return created workspace
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if encErr := json.NewEncoder(w).Encode(map[string]any{
		"id":               workspace.ID,
		"name":             workspace.Name,
		"agents":           agentNames,
		"agent_instances":  workspace.AgentInstances,
		"entry_agent_name": workspace.EntryAgentName(),
		"status":           workspace.Status,
		"message":          "Workspace created successfully",
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// GetWorkspace handles GET /api/workspaces/:id
func (h *HTTPHandler) GetWorkspace(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		orihttp.MethodNotAllowed(w)
		return
	}

	// Extract workspace ID from URL path
	path := strings.TrimPrefix(r.URL.Path, "/api/workspaces/")
	workspaceID := strings.TrimSuffix(path, "/")

	// Get workspace
	workspace, err := h.store.Get(workspaceID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace not found: %v", err))
		return
	}

	// Sync agents from tasks (handles legacy tasks assigned before auto-add feature)
	if added := workspace.SyncAgentsFromTasks(); added > 0 {
		if saveErr := h.store.Save(workspace); saveErr != nil {
			logger.Warn("Failed to save workspace after syncing agents", logger.Fields{"error": saveErr})
		}
	}

	// Get agent statistics
	agentStats := workspace.GetAgentStats()

	// Get workspace progress
	workspaceProgress := workspace.GetWorkspaceProgress()

	// Return workspace details
	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]any{
		"id":                   workspace.ID,
		"name":                 workspace.Name,
		"description":          workspace.Description,
		"entry_agent_name":     workspace.EntryAgentName(),
		"agents":               workspace.AgentNames(),
		"agent_instances":      workspace.AgentInstances, // NEW: Stable agent instances
		"agent_stats":          agentStats,
		"workspace_progress":   workspaceProgress,
		"status":               workspace.Status,
		"tasks":                workspace.Tasks,
		"attachments":          workspace.Attachments,
		"folders":              workspace.Folders,
		"scheduled_tasks":      workspace.ScheduledTasks, // Include scheduled tasks for scheduler nodes
		"store_nodes":          workspace.StoreNodes,     // Include store nodes
		"directory_references": workspace.DirectoryReferences,
		"mcp_bindings":         workspace.MCPBindings,
		"agent_mcp_access":     workspace.AgentMCPAccess,
		"messages":             workspace.Messages,
		"shared_data":          workspace.SharedData,
		"layout":               workspace.Layout,
		"created_at":           workspace.CreatedAt,
		"updated_at":           workspace.UpdatedAt,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// ListWorkspaces handles GET /api/workspaces
func (h *HTTPHandler) ListWorkspaces(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		orihttp.MethodNotAllowed(w)
		return
	}

	// Get all workspace IDs
	ids, err := h.store.List()
	if err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to list workspaces: %v", err))
		// Get workspace details
		return
	}

	workspaces := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		workspace, err := h.store.Get(id)
		if err != nil {
			logger.Error("Failed to get workspace", logger.Fields{"workspace_id": id, "err": err})
			continue
		}

		workspaces = append(workspaces, map[string]any{
			"id":               workspace.ID,
			"name":             workspace.Name,
			"description":      workspace.Description,
			"entry_agent_name": workspace.EntryAgentName(),
			"agents":           workspace.AgentNames(),
			"status":           workspace.Status,
			"created_at":       workspace.CreatedAt,
			"task_count":       len(workspace.Tasks),
		})
	}

	// Return workspaces
	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]any{
		"workspaces": workspaces,
		"count":      len(workspaces),
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// GetWorkspaceEvents handles GET /api/workspaces/:id/events (Server-Sent Events)
func (h *HTTPHandler) GetWorkspaceEvents(w http.ResponseWriter, r *http.Request) {
	// Extract workspace ID
	path := strings.TrimPrefix(r.URL.Path, "/api/workspaces/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		orihttp.BadRequest(w, "Invalid URL format")
		return
	}
	workspaceID := parts[0]

	// Verify workspace exists

	if _, err := h.store.Get(workspaceID); err != nil {
		orihttp.NotFound(w, "Workspace not found")
		// Set SSE headers
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Create event channel
	events := make(chan Event, 10)

	// Subscribe to events for this workspace.
	// Note: Currently sends a test event. To implement real event filtering:
	// - Subscribe to EventBus with a filter for workspace ID matching workspaceID
	// - Forward matching events to this channel
	go func() {
		time.Sleep(1 * time.Second)
		events <- Event{
			ID:          uuid.New().String(),
			Type:        EventType("info"),
			WorkspaceID: workspaceID,
			Timestamp:   time.Now(),
			Source:      "system",
			Data: map[string]any{
				"message": "Connected to event stream",
			},
			Metadata: make(map[string]string),
		}
	}()

	// Stream events
	for {
		select {
		case event := <-events:
			data, err := json.Marshal(event)
			if err != nil {
				logger.Error("Failed to marshal SSE event", logger.Fields{"error": err})
				continue
			}
			if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
				logger.Debug("Failed to write SSE event (client may have disconnected)", logger.Fields{"error": err})
				return
			}
			w.(http.Flusher).Flush()

		case <-r.Context().Done():
			// Client disconnected
			return
		}
	}
}
