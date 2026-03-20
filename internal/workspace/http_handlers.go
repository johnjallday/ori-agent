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
)

// HTTPHandler handles HTTP requests for Agent Studio
type HTTPHandler struct {
	store        Store
	orchestrator *Orchestrator
	eventBus     *EventBus
}

// NewHTTPHandler creates a new HTTP handler
func NewHTTPHandler(store Store, orchestrator *Orchestrator, eventBus *EventBus) *HTTPHandler {
	return &HTTPHandler{
		store:        store,
		orchestrator: orchestrator,
		eventBus:     eventBus,
	}
}

// CreateStudioRequest represents the request to create a new studio
type CreateStudioRequest struct {
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	Agents         []string `json:"agents"`
	EntryAgentName string   `json:"entry_agent_name,omitempty"`
}

// CreateStudio handles POST /api/studios
func (h *HTTPHandler) CreateStudio(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}

	var req CreateStudioRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	// Validate request
	if req.Name == "" {
		orihttp.BadRequest(w, "Studio name is required")
		return
	}
	if len(req.Agents) == 0 {
		orihttp.BadRequest(w, "At least one agent is required")
		return
	}

	// Create studio
	studio := &Workspace{
		ID:          uuid.New().String(),
		Name:        req.Name,
		Description: req.Description,
		SharedData:  make(map[string]interface{}),
		Messages:    make([]AgentMessage, 0),
		Tasks:       make([]Task, 0),
		Attachments: make([]Attachment, 0),
		Status:      StatusActive,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	for _, agentName := range req.Agents {
		if err := studio.AddAgent(agentName); err != nil {
			if strings.TrimSpace(agentName) == "" || errors.Is(err, ErrAgentAlreadyInWorkspace) {
				orihttp.BadRequest(w, fmt.Sprintf("Invalid studio agents: %v", err))
				return
			}
			orihttp.InternalError(w, fmt.Sprintf("Failed to configure studio agents: %v", err))
			return
		}
	}

	entryAgentName := strings.TrimSpace(req.EntryAgentName)
	if entryAgentName == "" {
		entryAgentName = strings.TrimSpace(req.Agents[0])
	}
	if err := studio.SetEntryAgentName(entryAgentName); err != nil {
		orihttp.BadRequest(w, fmt.Sprintf("Invalid entry agent: %v", err))
		return
	}

	// Save studio
	if err := h.store.Save(studio); err != nil {
		logger.Error("Failed to save studio", logger.Fields{"error": err})
		orihttp.InternalError(w, "Failed to create studio")
		return
	}

	logger.Info("Created studio: (ID: ) with agents", logger.Fields{"workspace_id": studio.Name, "id": studio.ID, "agents": studio.Agents})

	// Return created studio
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if encErr := json.NewEncoder(w).Encode(map[string]interface{}{
		"id":               studio.ID,
		"name":             studio.Name,
		"agents":           studio.Agents,
		"agent_instances":  studio.AgentInstances,
		"entry_agent_name": studio.EntryAgentName(),
		"status":           studio.Status,
		"message":          "Studio created successfully",
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// GetStudio handles GET /api/studios/:id
func (h *HTTPHandler) GetStudio(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		orihttp.MethodNotAllowed(w)
		return
	}

	// Extract studio ID from URL path
	path := strings.TrimPrefix(r.URL.Path, "/api/studios/")
	studioID := strings.TrimSuffix(path, "/")

	// Get studio
	studio, err := h.store.Get(studioID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Studio not found: %v", err))
		return
	}

	// Sync agents from tasks (handles legacy tasks assigned before auto-add feature)
	if added := studio.SyncAgentsFromTasks(); added > 0 {
		if saveErr := h.store.Save(studio); saveErr != nil {
			logger.Warn("Failed to save workspace after syncing agents", logger.Fields{"error": saveErr})
		}
	}

	// Get agent statistics
	agentStats := studio.GetAgentStats()

	// Get workspace progress
	workspaceProgress := studio.GetWorkspaceProgress()

	// Return studio details
	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]interface{}{
		"id":                   studio.ID,
		"name":                 studio.Name,
		"description":          studio.Description,
		"entry_agent_name":     studio.EntryAgentName(),
		"agents":               studio.Agents,
		"agent_instances":      studio.AgentInstances, // NEW: Stable agent instances
		"agent_stats":          agentStats,
		"workspace_progress":   workspaceProgress,
		"status":               studio.Status,
		"tasks":                studio.Tasks,
		"attachments":          studio.Attachments,
		"scheduled_tasks":      studio.ScheduledTasks, // Include scheduled tasks for scheduler nodes
		"store_nodes":          studio.StoreNodes,     // Include store nodes
		"directory_references": studio.DirectoryReferences,
		"mcp_bindings":         studio.MCPBindings,
		"agent_mcp_access":     studio.AgentMCPAccess,
		"messages":             studio.Messages,
		"shared_data":          studio.SharedData,
		"layout":               studio.Layout,
		"created_at":           studio.CreatedAt,
		"updated_at":           studio.UpdatedAt,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// ListStudios handles GET /api/studios
func (h *HTTPHandler) ListStudios(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		orihttp.MethodNotAllowed(w)
		return
	}

	// Get all studio IDs
	ids, err := h.store.List()
	if err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to list studios: %v", err))
		// Get studio details
		return
	}

	studios := make([]map[string]interface{}, 0, len(ids))
	for _, id := range ids {
		studio, err := h.store.Get(id)
		if err != nil {
			logger.Error("Failed to get studio", logger.Fields{"workspace_id": id, "err": err})
			continue
		}

		studios = append(studios, map[string]interface{}{
			"id":               studio.ID,
			"name":             studio.Name,
			"description":      studio.Description,
			"entry_agent_name": studio.EntryAgentName(),
			"agents":           studio.Agents,
			"status":           studio.Status,
			"created_at":       studio.CreatedAt,
			"task_count":       len(studio.Tasks),
		})
	}

	// Return studios
	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]interface{}{
		"studios": studios,
		"count":   len(studios),
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// GetStudioEvents handles GET /api/studios/:id/events (Server-Sent Events)
func (h *HTTPHandler) GetStudioEvents(w http.ResponseWriter, r *http.Request) {
	// Extract studio ID
	path := strings.TrimPrefix(r.URL.Path, "/api/studios/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		orihttp.BadRequest(w, "Invalid URL format")
		return
	}
	studioID := parts[0]

	// Verify studio exists

	if _, err := h.store.Get(studioID); err != nil {
		orihttp.NotFound(w, "Studio not found")
		// Set SSE headers
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Create event channel
	events := make(chan Event, 10)

	// Subscribe to events for this studio.
	// Note: Currently sends a test event. To implement real event filtering:
	// - Subscribe to EventBus with a filter for workspace ID matching studioID
	// - Forward matching events to this channel
	go func() {
		time.Sleep(1 * time.Second)
		events <- Event{
			ID:          uuid.New().String(),
			Type:        EventType("info"),
			WorkspaceID: studioID,
			Timestamp:   time.Now(),
			Source:      "system",
			Data: map[string]interface{}{
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
