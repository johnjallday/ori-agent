package orchestrationhttp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/johnjallday/ori-agent/internal/filewatcher"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// WorkspaceHandler manages workspace-related operations
type WorkspaceHandler struct {
	agentStore     store.Store
	workspaceStore workspace.Store
	eventBus       *workspace.EventBus
	sessionStore   SessionStore
	fileWatcher    *filewatcher.Watcher // optional, for workspace directory watching
}

// NewWorkspaceHandler creates a new workspace handler
func NewWorkspaceHandler(agentStore store.Store, workspaceStore workspace.Store, eventBus *workspace.EventBus, sessionStore SessionStore) *WorkspaceHandler {
	return &WorkspaceHandler{
		agentStore:     agentStore,
		workspaceStore: workspaceStore,
		eventBus:       eventBus,
		sessionStore:   sessionStore,
	}
}

// SetFileWatcher sets the optional file watcher for workspace directory watching.
func (wh *WorkspaceHandler) SetFileWatcher(fw *filewatcher.Watcher) {
	wh.fileWatcher = fw
}

// WorkspaceHandler handles workspace CRUD operations
// GET: List all workspaces or get workspace by ID
// POST: Create new workspace
// PUT: Update workspace metadata
// DELETE: Delete workspace
func (wh *WorkspaceHandler) WorkspaceHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	switch r.Method {
	case http.MethodGet:
		wh.handleGetWorkspace(w, r)
	case http.MethodPost:
		wh.handleCreateWorkspace(w, r)
	case http.MethodPut:
		wh.handleUpdateWorkspace(w, r)
	case http.MethodDelete:
		wh.handleDeleteWorkspace(w, r)
	default:
		orihttp.MethodNotAllowed(w)
	}
}

func (wh *WorkspaceHandler) handleGetWorkspace(w http.ResponseWriter, r *http.Request) {
	// Check if a specific workspace ID is requested
	wsID := r.URL.Query().Get("id")
	activeOnly := r.URL.Query().Get("active") == "true"

	if wsID != "" {
		// Get specific workspace
		ws, err := wh.workspaceStore.Get(wsID)
		if err != nil {
			logger.Error("Error getting workspace", logger.Fields{"workspace_id": wsID, "error": err})
			orihttp.NotFound(w, err.Error())
			return
		}

		// Build response with workspace data
		response := map[string]interface{}{
			"id":                   ws.ID,
			"name":                 ws.Name,
			"description":          ws.Description,
			"agents":               ws.Agents,
			"agent_instances":      ws.AgentInstances,
			"shared_data":          ws.SharedData,
			"messages":             ws.Messages,
			"tasks":                ws.Tasks,
			"attachments":          ws.Attachments,
			"scheduled_tasks":      ws.ScheduledTasks,
			"store_nodes":          ws.StoreNodes,
			"directory_references": ws.DirectoryReferences,
			"mcp_bindings":         ws.MCPBindings,
			"agent_mcp_access":     ws.AgentMCPAccess,
			"workflows":            ws.Workflows,
			"layout":               ws.Layout,
			"status":               ws.Status,
			"created_at":           ws.CreatedAt,
			"updated_at":           ws.UpdatedAt,
		}

		// Add sessions if session store is available
		if wh.sessionStore != nil {
			ctx := context.Background()
			sessions, err := wh.sessionStore.ListSessionsByWorkspace(ctx, wsID)
			if err != nil {
				logger.Debug("Failed to load sessions for workspace", logger.Fields{"workspace_id": wsID, "error": err})
			} else {
				response["sessions"] = sessions
			}

			// Add workspace notes
			notes, err := wh.sessionStore.ListNotesByWorkspace(ctx, wsID)
			if err != nil {
				logger.Debug("Failed to load notes for workspace", logger.Fields{"workspace_id": wsID, "error": err})
			} else {
				response["notes"] = notes
			}
		}

		orihttp.WriteJSON(w, response)
		return
	}

	// List workspaces with optional filters
	if activeOnly {
		// Get only active workspaces
		workspaces, err := wh.workspaceStore.ListActive()
		if err != nil {
			logger.Error("Error listing active workspaces", logger.Fields{"error": err})
			orihttp.InternalError(w, err.Error())
			return
		}

		orihttp.WriteJSON(w, map[string]interface{}{
			"workspaces": workspaces,
			"count":      len(workspaces),
		})
		return
	}

	// List all workspaces
	ids, err := wh.workspaceStore.List()
	if err != nil {
		logger.Error("Error listing workspaces", logger.Fields{"error": err})
		orihttp.InternalError(w, err.Error())
		return
	}

	// Load summaries for all workspaces
	ctx := context.Background()
	summaries := make([]map[string]interface{}, 0, len(ids))
	for _, id := range ids {
		ws, err := wh.workspaceStore.Get(id)
		if err != nil {
			continue // Skip workspaces that fail to load
		}
		summary := ws.GetSummary()

		// Add session and note counts if session store is available
		if wh.sessionStore != nil {
			sessions, err := wh.sessionStore.ListSessionsByWorkspace(ctx, id)
			if err == nil {
				summary["session_count"] = len(sessions)
			} else {
				summary["session_count"] = 0
			}

			notes, err := wh.sessionStore.ListNotesByWorkspace(ctx, id)
			if err == nil {
				summary["note_count"] = len(notes)
			} else {
				summary["note_count"] = 0
			}
		} else {
			summary["session_count"] = 0
			summary["note_count"] = 0
		}

		summaries = append(summaries, summary)
	}

	orihttp.WriteJSON(w, map[string]interface{}{
		"workspaces": summaries,
		"count":      len(summaries),
	})
}

// handleCreateWorkspace creates a new workspace
func (wh *WorkspaceHandler) handleCreateWorkspace(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string                 `json:"name"`
		Description string                 `json:"description"`
		Agents      []string               `json:"participating_agents"`
		InitialData map[string]interface{} `json:"initial_context"`
	}

	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	// Validate required fields

	if req.Name == "" {
		orihttp.BadRequest(w, "name is required")
		return
	}

	// Verify all participating agents exist
	for _, agentName := range req.Agents {
		_, ok := wh.agentStore.GetAgent(agentName)
		if !ok {
			orihttp.NotFound(w, "agent not found: "+agentName)
			return
		}
	}

	// Create workspace

	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{
		Name:        req.Name,
		Description: req.Description,
		Agents:      req.Agents,
		InitialData: req.InitialData,
	})

	// Save workspace
	if err := wh.workspaceStore.Save(ws); err != nil {
		logger.Error("Error saving workspace", logger.Fields{"error": err})
		orihttp.InternalError(w, "Failed to save workspace: "+err.Error())
		return
	}

	logger.Info("Created workspace", logger.Fields{"name": req.Name, "workspace_id": ws.ID})

	// Publish workspace created event
	if wh.eventBus != nil {
		event := workspace.NewWorkspaceEvent(
			workspace.EventWorkspaceCreated,
			ws.ID,
			"api",
			map[string]interface{}{
				"name":        req.Name,
				"description": req.Description,
				"agents":      req.Agents,
			},
		)
		wh.eventBus.Publish(event)
	}

	w.WriteHeader(http.StatusOK)
	orihttp.WriteJSON(w, map[string]interface{}{
		"studio_id":  ws.ID,
		"status":     ws.Status,
		"created_at": ws.CreatedAt,
	})
}

// handleUpdateWorkspace updates workspace metadata (name, description)
func (wh *WorkspaceHandler) handleUpdateWorkspace(w http.ResponseWriter, r *http.Request) {
	wsID := r.URL.Query().Get("id")
	if wsID == "" {
		orihttp.BadRequest(w, "id is required")
		return
	}

	var req struct {
		Name        *string `json:"name,omitempty"`
		Description *string `json:"description,omitempty"`
	}

	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	// Get existing workspace
	ws, err := wh.workspaceStore.Get(wsID)
	if err != nil {
		logger.Error("Error getting workspace for update", logger.Fields{"workspace_id": wsID, "error": err})
		orihttp.NotFound(w, err.Error())
		return
	}

	// Update fields if provided
	if req.Name != nil {
		ws.Name = *req.Name
	}
	if req.Description != nil {
		ws.Description = *req.Description
	}

	// Update timestamp
	ws.UpdatedAt = time.Now()

	// Save workspace
	if err := wh.workspaceStore.Save(ws); err != nil {
		logger.Error("Error saving workspace update", logger.Fields{"workspace_id": wsID, "error": err})
		orihttp.InternalError(w, "Failed to save workspace: "+err.Error())
		return
	}

	logger.Info("Updated workspace", logger.Fields{"workspace_id": wsID})

	// Publish workspace updated event
	if wh.eventBus != nil {
		event := workspace.NewWorkspaceEvent(
			workspace.EventWorkspaceUpdated,
			ws.ID,
			"api",
			map[string]interface{}{
				"name":        ws.Name,
				"description": ws.Description,
			},
		)
		wh.eventBus.Publish(event)
	}

	orihttp.WriteJSON(w, map[string]interface{}{
		"id":          ws.ID,
		"name":        ws.Name,
		"description": ws.Description,
		"updated_at":  ws.UpdatedAt,
	})
}

// handleDeleteWorkspace deletes a workspace
func (wh *WorkspaceHandler) handleDeleteWorkspace(w http.ResponseWriter, r *http.Request) {
	wsID := r.URL.Query().Get("id")
	if wsID == "" {
		orihttp.BadRequest(w, "id parameter required")
		return
	}

	if err := wh.workspaceStore.Delete(wsID); err != nil {
		logger.Error("Error deleting workspace", logger.Fields{"workspace_id": wsID, "error": err})
		orihttp.NotFound(w, err.Error())
		return
	}

	logger.Info("Deleted workspace", logger.Fields{"workspace_id": wsID})
	w.WriteHeader(http.StatusOK)
	orihttp.WriteJSON(w, map[string]interface{}{
		"success": true,
		"message": "Workspace deleted successfully",
	})
}

// WorkspaceAgentsHandler handles adding/removing agents from workspace
// POST: Add agent to workspace
// DELETE: Remove agent from workspace
func (wh *WorkspaceHandler) WorkspaceAgentsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	switch r.Method {
	case http.MethodPost:
		wh.handleAddAgentToWorkspace(w, r)
	case http.MethodDelete:
		wh.handleRemoveAgentFromWorkspace(w, r)
	default:
		orihttp.MethodNotAllowed(w)
	}
}

func (wh *WorkspaceHandler) handleAddAgentToWorkspace(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WorkspaceID string `json:"studio_id"`
		AgentName   string `json:"agent_name"`
	}

	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	if req.WorkspaceID == "" {
		orihttp.BadRequest(w, "workspace_id is required")
		return
	}
	if req.AgentName == "" {
		orihttp.BadRequest(w, "agent_name is required")
		return
	}

	// Verify agent exists
	_, ok := wh.agentStore.GetAgent(req.AgentName)
	if !ok {
		orihttp.NotFound(w, "agent not found: "+req.AgentName)
		return
	}

	// Get workspace
	ws, err := wh.workspaceStore.Get(req.WorkspaceID)
	if err != nil {
		logger.Error("Error getting workspace", logger.Fields{"workspace_id": req.WorkspaceID, "error": err})
		orihttp.NotFound(w, "Workspace not found: "+err.Error())
		return
	}

	// Add agent
	if err := ws.AddAgent(req.AgentName); err != nil {
		logger.Error("Error adding agent to workspace", logger.Fields{"error": err})
		orihttp.BadRequest(w, err.Error())
		return
	}

	// Save workspace
	if err := wh.workspaceStore.Save(ws); err != nil {
		logger.Error("Error saving workspace", logger.Fields{"error": err})
		orihttp.InternalError(w, "Failed to save workspace: "+err.Error())
		return
	}

	logger.Info("Added agent to workspace", logger.Fields{"agent": req.AgentName, "workspace_id": req.WorkspaceID})

	// Publish event
	if wh.eventBus != nil {
		event := workspace.NewWorkspaceEvent(
			workspace.EventWorkspaceUpdated,
			req.WorkspaceID,
			"api",
			map[string]interface{}{
				"action": "agent_added",
				"agent":  req.AgentName,
			},
		)
		wh.eventBus.Publish(event)
	}

	w.WriteHeader(http.StatusOK)
	orihttp.WriteJSON(w, map[string]interface{}{
		"success": true,
		"message": "Agent added successfully",
		"agent":   req.AgentName,
		"agents":  ws.Agents,
	})
}

// handleRemoveAgentFromWorkspace removes an agent from a workspace
func (wh *WorkspaceHandler) handleRemoveAgentFromWorkspace(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.URL.Query().Get("studio_id")
	agentName := r.URL.Query().Get("agent_name")

	if workspaceID == "" {
		orihttp.BadRequest(w, "workspace_id parameter required")
		return
	}
	if agentName == "" {
		orihttp.BadRequest(w, "agent_name parameter required")
		return
	}

	// Get workspace
	ws, err := wh.workspaceStore.Get(workspaceID)
	if err != nil {
		logger.Error("Error getting workspace", logger.Fields{"workspace_id": workspaceID, "error": err})
		orihttp.NotFound(w, "Workspace not found: "+err.Error())
		return
	}

	// Remove agent
	if err := ws.RemoveAgent(agentName); err != nil {
		logger.Error("Error removing agent from workspace", logger.Fields{"error": err})
		orihttp.NotFound(w, err.Error())
		return
	}

	// Save workspace
	if err := wh.workspaceStore.Save(ws); err != nil {
		logger.Error("Error saving workspace", logger.Fields{"error": err})
		orihttp.InternalError(w, "Failed to save workspace: "+err.Error())
		return
	}

	logger.Info("Removed agent from workspace", logger.Fields{"agent": agentName, "workspace_id": workspaceID})

	// Publish event
	if wh.eventBus != nil {
		event := workspace.NewWorkspaceEvent(
			workspace.EventWorkspaceUpdated,
			workspaceID,
			"api",
			map[string]interface{}{
				"action": "agent_removed",
				"agent":  agentName,
			},
		)
		wh.eventBus.Publish(event)
	}

	w.WriteHeader(http.StatusOK)
	orihttp.WriteJSON(w, map[string]interface{}{
		"success": true,
		"message": "Agent removed successfully",
		"agent":   agentName,
		"agents":  ws.Agents,
	})
}

// SaveLayoutHandler saves the canvas layout for a workspace
// PUT: Save workspace layout (task positions, agent positions, zoom, pan)
func (wh *WorkspaceHandler) SaveLayoutHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		orihttp.MethodNotAllowed(w)
		return
	}

	var req struct {
		WorkspaceID         string                               `json:"workspace_id"`
		TaskPositions       map[string]workspace.Position        `json:"task_positions"`
		AgentPositions      map[string]workspace.Position        `json:"agent_positions"`
		AttachmentPositions map[string]workspace.Position        `json:"attachment_positions"`
		SchedulerPositions  map[string]workspace.Position        `json:"scheduler_positions"`
		StorePositions      map[string]workspace.Position        `json:"store_positions"`
		WorkflowConnections []workspace.WorkflowConnectionLayout `json:"workflow_connections"`
		Scale               float64                              `json:"scale"`
		OffsetX             float64                              `json:"offset_x"`
		OffsetY             float64                              `json:"offset_y"`
	}

	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	if req.WorkspaceID == "" {
		orihttp.BadRequest(w, "workspace_id is required")
		return
	}

	// Get workspace
	ws, err := wh.workspaceStore.Get(req.WorkspaceID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Failed to get workspace: %v", err))
		return
	}

	// Update layout

	if ws.Layout == nil {
		ws.Layout = &workspace.CanvasLayout{}
	}

	ws.Layout.TaskPositions = req.TaskPositions
	ws.Layout.AgentPositions = req.AgentPositions
	ws.Layout.AttachmentPositions = req.AttachmentPositions
	ws.Layout.SchedulerPositions = req.SchedulerPositions
	ws.Layout.StorePositions = req.StorePositions
	ws.Layout.WorkflowConnections = req.WorkflowConnections
	ws.Layout.Scale = req.Scale
	ws.Layout.OffsetX = req.OffsetX
	ws.Layout.OffsetY = req.OffsetY

	// Save workspace
	if err := wh.workspaceStore.Save(ws); err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to save workspace: %v", err))
		return
	}

	// Broadcast workspace update event to notify all connected clients
	wh.eventBus.Publish(workspace.Event{
		WorkspaceID: req.WorkspaceID,
		Type:        workspace.EventWorkspaceUpdated,
		Timestamp:   time.Now(),
	})

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Layout saved successfully",
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// ActivateHandler handles POST /api/orchestration/workspace/activate?id={id}
// It starts watching all directory references in the workspace so that
// file change events are available while the user is on the workspace page.
func (wh *WorkspaceHandler) ActivateHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}

	wsID := r.URL.Query().Get("id")
	if wsID == "" {
		orihttp.BadRequest(w, "workspace id is required")
		return
	}

	ws, err := wh.workspaceStore.Get(wsID)
	if err != nil || ws == nil {
		orihttp.NotFound(w, "workspace not found")
		return
	}

	watched := 0
	if wh.fileWatcher != nil && len(ws.DirectoryReferences) > 0 {
		for _, dir := range ws.DirectoryReferences {
			if dir.Path == "" {
				continue
			}
			watchKey := fmt.Sprintf("workspace:%s:dir:%s", wsID, dir.ID)
			if wh.fileWatcher.IsWatching(watchKey) {
				watched++
				continue
			}
			if err := wh.fileWatcher.Watch(watchKey, dir.Path); err != nil {
				logger.Warn("Failed to watch workspace directory", logger.Fields{
					"workspace_id": wsID,
					"directory":    dir.Name,
					"path":         dir.Path,
					"error":        err,
				})
			} else {
				watched++
			}
		}
	}

	logger.Info("Workspace activated", logger.Fields{
		"workspace_id":        wsID,
		"directories_watched": watched,
		"total_directories":   len(ws.DirectoryReferences),
	})

	orihttp.WriteJSON(w, map[string]interface{}{
		"success":             true,
		"workspace_id":        wsID,
		"directories_watched": watched,
	})
}
