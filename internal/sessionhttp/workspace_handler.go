package sessionhttp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/session"
)

// HandleWorkspaces routes requests to /api/workspaces (also supports legacy /api/folders).
func (h *Handler) HandleWorkspaces(w http.ResponseWriter, r *http.Request) {
	// Normalize path for both /api/folders and /api/workspaces
	path := r.URL.Path
	path = strings.TrimPrefix(path, "/api/folders")
	path = strings.TrimPrefix(path, "/api/workspaces")
	path = strings.TrimPrefix(path, "/")

	// Handle sub-paths like {id}/agents, {id}/layout
	if path != "" && strings.Contains(path, "/") {
		parts := strings.SplitN(path, "/", 3)
		id := parts[0]
		subPath := parts[1]

		switch subPath {
		case "agents":
			h.handleWorkspaceAgents(w, r, id, parts)
			return
		case "layout":
			h.handleWorkspaceLayout(w, r, id)
			return
		}
	}

	if path != "" && !strings.Contains(path, "/") {
		// This is a request for a specific workspace
		h.handleWorkspace(w, r, path)
		return
	}

	// Handle collection-level requests
	switch r.Method {
	case http.MethodGet:
		h.listWorkspaces(w, r)
	case http.MethodPost:
		h.createWorkspace(w, r)
	default:
		_ = orihttp.RespondMethodNotAllowed(w)
	}
}

// handleWorkspace handles requests for a specific workspace.
func (h *Handler) handleWorkspace(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		h.getWorkspace(w, r, id)
	case http.MethodPut:
		h.updateWorkspace(w, r, id)
	case http.MethodPatch:
		h.updateWorkspace(w, r, id)
	case http.MethodDelete:
		h.deleteWorkspace(w, r, id)
	default:
		_ = orihttp.RespondMethodNotAllowed(w)
	}
}

// createWorkspace handles POST /api/workspaces.
func (h *Handler) createWorkspace(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description,omitempty"`
		ParentID    string `json:"parent_id,omitempty"`
		Color       string `json:"color,omitempty"`
	}

	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	if req.Name == "" {
		_ = orihttp.RespondBadRequest(w, "name is required")
		return
	}

	workspace := &session.Workspace{
		Name:        req.Name,
		Description: req.Description,
		ParentID:    req.ParentID,
		Color:       req.Color,
	}

	if err := h.store.CreateWorkspace(r.Context(), workspace); err != nil {
		logger.Error("Failed to create workspace", logger.Fields{"error": err})
		_ = orihttp.RespondInternalError(w, "Failed to create workspace")
		return
	}

	logger.Info("Workspace created", logger.Fields{"id": workspace.ID, "name": req.Name})

	_ = orihttp.RespondCreated(w, map[string]interface{}{
		"success": true,
		"folder":  workspace,
	})
}

// getWorkspace handles GET /api/workspaces/{id}.
func (h *Handler) getWorkspace(w http.ResponseWriter, r *http.Request, id string) {
	workspace, err := h.store.GetWorkspace(r.Context(), id)
	if err == session.ErrWorkspaceNotFound {
		_ = orihttp.RespondNotFound(w, "Workspace not found")
		return
	}
	if err != nil {
		logger.Error("Failed to get workspace", logger.Fields{"id": id, "error": err})
		_ = orihttp.RespondInternalError(w, "Failed to get workspace")
		return
	}

	orihttp.WriteJSON(w, workspace)
}

// updateWorkspace handles PUT/PATCH /api/workspaces/{id}.
func (h *Handler) updateWorkspace(w http.ResponseWriter, r *http.Request, id string) {
	workspace, err := h.store.GetWorkspace(r.Context(), id)
	if err == session.ErrWorkspaceNotFound {
		_ = orihttp.RespondNotFound(w, "Workspace not found")
		return
	}
	if err != nil {
		_ = orihttp.RespondInternalError(w, "Failed to get workspace")
		return
	}

	var req struct {
		Name        *string `json:"name,omitempty"`
		Description *string `json:"description,omitempty"`
		ParentID    *string `json:"parent_id,omitempty"`
		Color       *string `json:"color,omitempty"`
	}

	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	// Apply partial updates
	if req.Name != nil {
		workspace.Name = *req.Name
	}
	if req.Description != nil {
		workspace.Description = *req.Description
	}
	if req.ParentID != nil {
		// Check for circular reference
		if *req.ParentID == workspace.ID {
			_ = orihttp.RespondBadRequest(w, "Workspace cannot be its own parent")
			return
		}
		workspace.ParentID = *req.ParentID
	}
	if req.Color != nil {
		workspace.Color = *req.Color
	}

	if err := h.store.UpdateWorkspace(r.Context(), workspace); err != nil {
		logger.Error("Failed to update workspace", logger.Fields{"id": id, "error": err})
		_ = orihttp.RespondInternalError(w, "Failed to update workspace")
		return
	}

	logger.Info("Workspace updated", logger.Fields{"id": id})

	orihttp.WriteJSON(w, map[string]interface{}{
		"success": true,
		"folder":  workspace,
	})
}

// deleteWorkspace handles DELETE /api/workspaces/{id}.
func (h *Handler) deleteWorkspace(w http.ResponseWriter, r *http.Request, id string) {
	err := h.store.DeleteWorkspace(r.Context(), id)
	if err == session.ErrWorkspaceNotFound {
		_ = orihttp.RespondNotFound(w, "Workspace not found")
		return
	}
	if err != nil {
		logger.Error("Failed to delete workspace", logger.Fields{"id": id, "error": err})
		_ = orihttp.RespondInternalError(w, "Failed to delete workspace")
		return
	}

	logger.Info("Workspace deleted", logger.Fields{"id": id})

	orihttp.RespondNoContent(w)
}

// listWorkspaces handles GET /api/workspaces.
func (h *Handler) listWorkspaces(w http.ResponseWriter, r *http.Request) {
	tree := r.URL.Query().Get("tree") == "true"

	if tree {
		workspaces, err := h.store.GetWorkspaceTree(r.Context())
		if err != nil {
			// Don't log context canceled - it's normal when client disconnects
			if errors.Is(err, context.Canceled) {
				return
			}
			logger.Error("Failed to get workspace tree", logger.Fields{"error": err})
			_ = orihttp.RespondInternalError(w, "Failed to get workspaces")
			return
		}

		orihttp.WriteJSON(w, map[string]interface{}{
			"folders": workspaces,
		})
		return
	}

	workspaces, err := h.store.ListWorkspaces(r.Context())
	if err != nil {
		// Don't log context canceled - it's normal when client disconnects
		if errors.Is(err, context.Canceled) {
			return
		}
		logger.Error("Failed to list workspaces", logger.Fields{"error": err})
		_ = orihttp.RespondInternalError(w, "Failed to list workspaces")
		return
	}

	orihttp.WriteJSON(w, map[string]interface{}{
		"folders": workspaces,
	})
}

// =============================================================================
// Workspace Agent Management
// =============================================================================

// handleWorkspaceAgents handles requests to /api/workspaces/{id}/agents.
func (h *Handler) handleWorkspaceAgents(w http.ResponseWriter, r *http.Request, workspaceID string, parts []string) {
	switch r.Method {
	case http.MethodPost:
		h.addWorkspaceAgent(w, r, workspaceID)
	case http.MethodDelete:
		// Extract agent name or instance ID from path
		var agentIdentifier string
		if len(parts) > 2 {
			agentIdentifier = parts[2]
		}
		h.removeWorkspaceAgent(w, r, workspaceID, agentIdentifier)
	default:
		_ = orihttp.RespondMethodNotAllowed(w)
	}
}

// addWorkspaceAgent handles POST /api/workspaces/{id}/agents.
func (h *Handler) addWorkspaceAgent(w http.ResponseWriter, r *http.Request, workspaceID string) {
	var req struct {
		AgentName string `json:"agent_name"`
	}

	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	if req.AgentName == "" {
		_ = orihttp.RespondBadRequest(w, "agent_name is required")
		return
	}

	// Get the workspace
	workspace, err := h.store.GetWorkspace(r.Context(), workspaceID)
	if err == session.ErrWorkspaceNotFound {
		_ = orihttp.RespondNotFound(w, "Workspace not found")
		return
	}
	if err != nil {
		logger.Error("Failed to get workspace", logger.Fields{"id": workspaceID, "error": err})
		_ = orihttp.RespondInternalError(w, "Failed to get workspace")
		return
	}

	// Count existing instances of this agent type
	instanceCount := 0
	for _, inst := range workspace.AgentInstances {
		if inst.Name == req.AgentName {
			instanceCount++
		}
	}

	// Create new agent instance
	instanceNumber := instanceCount + 1
	nodeID := req.AgentName + "-node-" + uuid.New().String()[:8]
	if instanceNumber > 1 {
		nodeID = fmt.Sprintf("%s-%d-node-%s", req.AgentName, instanceNumber, uuid.New().String()[:8])
	}

	newInstance := session.AgentInstance{
		ID:             uuid.New().String(),
		Name:           req.AgentName,
		InstanceNumber: instanceNumber,
		NodeID:         nodeID,
		CreatedAt:      time.Now(),
	}

	// Add to workspace
	workspace.AgentInstances = append(workspace.AgentInstances, newInstance)

	// Also add to legacy agents array for backward compatibility
	found := false
	for _, a := range workspace.Agents {
		if a == req.AgentName {
			found = true
			break
		}
	}
	if !found {
		workspace.Agents = append(workspace.Agents, req.AgentName)
	}

	workspace.UpdatedAt = time.Now()

	if err := h.store.UpdateWorkspace(r.Context(), workspace); err != nil {
		logger.Error("Failed to update workspace", logger.Fields{"id": workspaceID, "error": err})
		_ = orihttp.RespondInternalError(w, "Failed to add agent")
		return
	}

	logger.Info("Agent added to workspace", logger.Fields{
		"workspace_id":    workspaceID,
		"agent_name":      req.AgentName,
		"instance_id":     newInstance.ID,
		"instance_number": instanceNumber,
	})

	_ = orihttp.RespondCreated(w, map[string]interface{}{
		"success":        true,
		"agent_instance": newInstance,
		"workspace":      workspace,
	})
}

// removeWorkspaceAgent handles DELETE /api/workspaces/{id}/agents/{name}.
func (h *Handler) removeWorkspaceAgent(w http.ResponseWriter, r *http.Request, workspaceID, agentIdentifier string) {
	if agentIdentifier == "" {
		_ = orihttp.RespondBadRequest(w, "agent name or instance ID is required")
		return
	}

	// Get the workspace
	workspace, err := h.store.GetWorkspace(r.Context(), workspaceID)
	if err == session.ErrWorkspaceNotFound {
		_ = orihttp.RespondNotFound(w, "Workspace not found")
		return
	}
	if err != nil {
		logger.Error("Failed to get workspace", logger.Fields{"id": workspaceID, "error": err})
		_ = orihttp.RespondInternalError(w, "Failed to get workspace")
		return
	}

	// Try to find and remove by instance ID first, then by node ID, then by name
	removed := false
	var removedInstance *session.AgentInstance
	newInstances := make([]session.AgentInstance, 0, len(workspace.AgentInstances))
	for _, inst := range workspace.AgentInstances {
		if inst.ID == agentIdentifier || inst.NodeID == agentIdentifier || inst.Name == agentIdentifier {
			removed = true
			removedInstance = &inst
			// Don't add this one to new list
		} else {
			newInstances = append(newInstances, inst)
		}
	}

	if !removed {
		_ = orihttp.RespondNotFound(w, "Agent not found in workspace")
		return
	}

	workspace.AgentInstances = newInstances

	// Update legacy agents array - remove if no more instances of this agent type
	if removedInstance != nil {
		hasOtherInstances := false
		for _, inst := range workspace.AgentInstances {
			if inst.Name == removedInstance.Name {
				hasOtherInstances = true
				break
			}
		}
		if !hasOtherInstances {
			newAgents := make([]string, 0, len(workspace.Agents))
			for _, a := range workspace.Agents {
				if a != removedInstance.Name {
					newAgents = append(newAgents, a)
				}
			}
			workspace.Agents = newAgents
		}
	}

	workspace.UpdatedAt = time.Now()

	if err := h.store.UpdateWorkspace(r.Context(), workspace); err != nil {
		logger.Error("Failed to update workspace", logger.Fields{"id": workspaceID, "error": err})
		_ = orihttp.RespondInternalError(w, "Failed to remove agent")
		return
	}

	logger.Info("Agent removed from workspace", logger.Fields{
		"workspace_id": workspaceID,
		"agent":        agentIdentifier,
	})

	orihttp.WriteJSON(w, map[string]interface{}{
		"success":   true,
		"workspace": workspace,
	})
}

// =============================================================================
// Workspace Layout Management
// =============================================================================

// handleWorkspaceLayout handles requests to /api/workspaces/{id}/layout.
func (h *Handler) handleWorkspaceLayout(w http.ResponseWriter, r *http.Request, workspaceID string) {
	switch r.Method {
	case http.MethodGet:
		h.getWorkspaceLayout(w, r, workspaceID)
	case http.MethodPut:
		h.saveWorkspaceLayout(w, r, workspaceID)
	case http.MethodPatch:
		h.saveWorkspaceLayout(w, r, workspaceID)
	default:
		_ = orihttp.RespondMethodNotAllowed(w)
	}
}

// getWorkspaceLayout handles GET /api/workspaces/{id}/layout.
func (h *Handler) getWorkspaceLayout(w http.ResponseWriter, r *http.Request, workspaceID string) {
	workspace, err := h.store.GetWorkspace(r.Context(), workspaceID)
	if err == session.ErrWorkspaceNotFound {
		_ = orihttp.RespondNotFound(w, "Workspace not found")
		return
	}
	if err != nil {
		logger.Error("Failed to get workspace", logger.Fields{"id": workspaceID, "error": err})
		_ = orihttp.RespondInternalError(w, "Failed to get workspace")
		return
	}

	layout := workspace.Layout
	if layout == nil {
		layout = &session.CanvasLayout{}
	}

	orihttp.WriteJSON(w, map[string]interface{}{
		"layout": layout,
	})
}

// saveWorkspaceLayout handles PUT/PATCH /api/workspaces/{id}/layout.
func (h *Handler) saveWorkspaceLayout(w http.ResponseWriter, r *http.Request, workspaceID string) {
	var layout session.CanvasLayout
	if !orihttp.ParseJSONBody(w, r, &layout) {
		return
	}

	// Get the workspace
	workspace, err := h.store.GetWorkspace(r.Context(), workspaceID)
	if err == session.ErrWorkspaceNotFound {
		_ = orihttp.RespondNotFound(w, "Workspace not found")
		return
	}
	if err != nil {
		logger.Error("Failed to get workspace", logger.Fields{"id": workspaceID, "error": err})
		_ = orihttp.RespondInternalError(w, "Failed to get workspace")
		return
	}

	workspace.Layout = &layout
	workspace.UpdatedAt = time.Now()

	if err := h.store.UpdateWorkspace(r.Context(), workspace); err != nil {
		logger.Error("Failed to update workspace layout", logger.Fields{"id": workspaceID, "error": err})
		_ = orihttp.RespondInternalError(w, "Failed to save layout")
		return
	}

	logger.Info("Workspace layout saved", logger.Fields{"workspace_id": workspaceID})

	orihttp.WriteJSON(w, map[string]interface{}{
		"success": true,
		"layout":  layout,
	})
}
