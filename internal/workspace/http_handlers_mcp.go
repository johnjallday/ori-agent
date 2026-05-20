package workspace

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
)

// CreateMCPBinding handles POST /api/workspaces/{workspaceID}/mcp-bindings
func (h *HTTPHandler) CreateMCPBinding(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.PathValue("workspaceID")
	if workspaceID == "" {
		orihttp.BadRequest(w, "workspace ID is required")
		return
	}

	var req struct {
		ID         string         `json:"id,omitempty"`
		ServerName string         `json:"server_name"`
		Alias      string         `json:"alias,omitempty"`
		Enabled    *bool          `json:"enabled,omitempty"`
		Scope      map[string]any `json:"scope,omitempty"`
		Config     map[string]any `json:"config,omitempty"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.ServerName) == "" {
		orihttp.BadRequest(w, "server_name is required")
		return
	}

	workspace, err := h.store.Get(workspaceID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace not found: %v", err))
		return
	}

	bindingID := strings.TrimSpace(req.ID)
	if bindingID == "" {
		bindingID = uuid.New().String()
	}
	if _, exists := workspace.GetMCPBinding(bindingID); exists {
		orihttp.BadRequest(w, fmt.Sprintf("MCP binding %s already exists", bindingID))
		return
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	binding := WorkspaceMCPBinding{
		ID:         bindingID,
		ServerName: req.ServerName,
		Alias:      req.Alias,
		Enabled:    enabled,
		Scope:      req.Scope,
		Config:     req.Config,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	binding, err = h.normalizeBindingForPersistence(r.Context(), workspaceID, binding)
	if err != nil {
		orihttp.BadRequest(w, err.Error())
		return
	}

	if err := workspace.UpsertMCPBinding(binding); err != nil {
		orihttp.BadRequest(w, err.Error())
		return
	}
	if err := h.store.Save(workspace); err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to save workspace: %v", err))
		return
	}

	created, _ := workspace.GetMCPBinding(binding.ID)
	if created == nil {
		created = &binding
	}
	h.publishWorkspaceMCPEvent(workspaceID, "mcp_binding_created", map[string]any{"binding": created})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if encErr := json.NewEncoder(w).Encode(map[string]any{
		"message":   "MCP binding created successfully",
		"binding":   h.mcpBindingResponse(r.Context(), *created),
		"workspace": workspaceID,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// ListMCPBindings handles GET /api/workspaces/{workspaceID}/mcp-bindings
func (h *HTTPHandler) ListMCPBindings(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.PathValue("workspaceID")
	if workspaceID == "" {
		orihttp.BadRequest(w, "workspace ID is required")
		return
	}

	workspace, err := h.store.Get(workspaceID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace not found: %v", err))
		return
	}

	bindings := workspace.GetMCPBindings()
	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]any{
		"bindings":  h.mcpBindingResponses(r.Context(), bindings),
		"count":     len(bindings),
		"workspace": workspaceID,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// GetMCPBinding handles GET /api/workspaces/{workspaceID}/mcp-bindings/{bindingID}
func (h *HTTPHandler) GetMCPBinding(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.PathValue("workspaceID")
	bindingID := r.PathValue("bindingID")
	if workspaceID == "" || bindingID == "" {
		orihttp.BadRequest(w, "workspace ID and binding ID are required")
		return
	}

	workspace, err := h.store.Get(workspaceID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace not found: %v", err))
		return
	}

	binding, exists := workspace.GetMCPBinding(bindingID)
	if !exists {
		orihttp.NotFound(w, fmt.Sprintf("MCP binding %s not found", bindingID))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]any{
		"binding":   h.mcpBindingResponse(r.Context(), *binding),
		"workspace": workspaceID,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// UpdateMCPBinding handles PUT/PATCH /api/workspaces/{workspaceID}/mcp-bindings/{bindingID}
func (h *HTTPHandler) UpdateMCPBinding(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.PathValue("workspaceID")
	bindingID := r.PathValue("bindingID")
	if workspaceID == "" || bindingID == "" {
		orihttp.BadRequest(w, "workspace ID and binding ID are required")
		return
	}

	var req struct {
		ServerName *string        `json:"server_name,omitempty"`
		Alias      *string        `json:"alias,omitempty"`
		Enabled    *bool          `json:"enabled,omitempty"`
		Scope      map[string]any `json:"scope,omitempty"`
		Config     map[string]any `json:"config,omitempty"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	workspace, err := h.store.Get(workspaceID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace not found: %v", err))
		return
	}

	binding, exists := workspace.GetMCPBinding(bindingID)
	if !exists {
		orihttp.NotFound(w, fmt.Sprintf("MCP binding %s not found", bindingID))
		return
	}

	if req.ServerName != nil {
		binding.ServerName = *req.ServerName
	}
	if req.Alias != nil {
		binding.Alias = *req.Alias
	}
	if req.Enabled != nil {
		binding.Enabled = *req.Enabled
	}
	if req.Scope != nil {
		binding.Scope = req.Scope
	}
	if req.Config != nil {
		binding.Config = req.Config
	}
	*binding, err = h.normalizeBindingForPersistence(r.Context(), workspaceID, *binding)
	if err != nil {
		orihttp.BadRequest(w, err.Error())
		return
	}

	if err := workspace.UpsertMCPBinding(*binding); err != nil {
		orihttp.BadRequest(w, err.Error())
		return
	}
	if err := h.store.Save(workspace); err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to save workspace: %v", err))
		return
	}

	updated, _ := workspace.GetMCPBinding(bindingID)
	if updated == nil {
		updated = binding
	}
	h.publishWorkspaceMCPEvent(workspaceID, "mcp_binding_updated", map[string]any{"binding": updated})

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]any{
		"message":   "MCP binding updated successfully",
		"binding":   h.mcpBindingResponse(r.Context(), *updated),
		"workspace": workspaceID,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// DeleteMCPBinding handles DELETE /api/workspaces/{workspaceID}/mcp-bindings/{bindingID}
func (h *HTTPHandler) DeleteMCPBinding(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.PathValue("workspaceID")
	bindingID := r.PathValue("bindingID")
	if workspaceID == "" || bindingID == "" {
		orihttp.BadRequest(w, "workspace ID and binding ID are required")
		return
	}

	workspace, err := h.store.Get(workspaceID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace not found: %v", err))
		return
	}

	if err := workspace.DeleteMCPBinding(bindingID); err != nil {
		orihttp.NotFound(w, err.Error())
		return
	}
	if err := h.store.Save(workspace); err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to save workspace: %v", err))
		return
	}

	h.publishWorkspaceMCPEvent(workspaceID, "mcp_binding_deleted", map[string]any{"binding_id": bindingID})

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]any{
		"message":    "MCP binding deleted successfully",
		"binding_id": bindingID,
		"workspace":  workspaceID,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// ListAgentMCPAccess handles GET /api/workspaces/{workspaceID}/agent-mcp-access
func (h *HTTPHandler) ListAgentMCPAccess(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.PathValue("workspaceID")
	if workspaceID == "" {
		orihttp.BadRequest(w, "workspace ID is required")
		return
	}

	workspace, err := h.store.Get(workspaceID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace not found: %v", err))
		return
	}

	entries := workspace.ListAgentMCPAccess()
	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]any{
		"access":    entries,
		"count":     len(entries),
		"workspace": workspaceID,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// GetAgentMCPAccessEntry handles GET /api/workspaces/{workspaceID}/agent-mcp-access/{agentInstanceID}
func (h *HTTPHandler) GetAgentMCPAccessEntry(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.PathValue("workspaceID")
	agentInstanceID := r.PathValue("agentInstanceID")
	if workspaceID == "" || agentInstanceID == "" {
		orihttp.BadRequest(w, "workspace ID and agent instance ID are required")
		return
	}

	workspace, err := h.store.Get(workspaceID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace not found: %v", err))
		return
	}

	entry, exists := workspace.GetAgentMCPAccess(agentInstanceID)
	if !exists {
		orihttp.NotFound(w, fmt.Sprintf("agent MCP access %s not found", agentInstanceID))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]any{
		"access":    entry,
		"workspace": workspaceID,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// UpdateAgentMCPAccess handles PUT/PATCH /api/workspaces/{workspaceID}/agent-mcp-access/{agentInstanceID}
func (h *HTTPHandler) UpdateAgentMCPAccess(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.PathValue("workspaceID")
	agentInstanceID := r.PathValue("agentInstanceID")
	if workspaceID == "" || agentInstanceID == "" {
		orihttp.BadRequest(w, "workspace ID and agent instance ID are required")
		return
	}

	var req struct {
		EnabledBindingIDs []string `json:"enabled_binding_ids,omitempty"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	workspace, err := h.store.Get(workspaceID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace not found: %v", err))
		return
	}

	// Validate that all referenced binding IDs exist in the workspace
	if len(req.EnabledBindingIDs) > 0 {
		bindings := workspace.GetMCPBindings()
		bindingIDSet := make(map[string]bool, len(bindings))
		for _, b := range bindings {
			bindingIDSet[strings.ToLower(strings.TrimSpace(b.ID))] = true
		}
		for _, id := range req.EnabledBindingIDs {
			normalized := strings.ToLower(strings.TrimSpace(id))
			if normalized == "" {
				continue
			}
			if !bindingIDSet[normalized] {
				orihttp.BadRequest(w, fmt.Sprintf("binding ID %q does not exist in workspace", id))
				return
			}
		}
	}

	entry := WorkspaceAgentMCPAccess{
		AgentInstanceID:   agentInstanceID,
		EnabledBindingIDs: req.EnabledBindingIDs,
		UpdatedAt:         time.Now(),
	}
	if err := workspace.SetAgentMCPAccess(entry); err != nil {
		orihttp.BadRequest(w, err.Error())
		return
	}
	if err := h.store.Save(workspace); err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to save workspace: %v", err))
		return
	}

	updated, _ := workspace.GetAgentMCPAccess(agentInstanceID)
	if updated == nil {
		updated = &entry
	}
	h.publishWorkspaceMCPEvent(workspaceID, "agent_mcp_access_updated", map[string]any{"access": updated})

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]any{
		"message":   "Agent MCP access updated successfully",
		"access":    updated,
		"workspace": workspaceID,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// DeleteAgentMCPAccess handles DELETE /api/workspaces/{workspaceID}/agent-mcp-access/{agentInstanceID}
func (h *HTTPHandler) DeleteAgentMCPAccess(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.PathValue("workspaceID")
	agentInstanceID := r.PathValue("agentInstanceID")
	if workspaceID == "" || agentInstanceID == "" {
		orihttp.BadRequest(w, "workspace ID and agent instance ID are required")
		return
	}

	workspace, err := h.store.Get(workspaceID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace not found: %v", err))
		return
	}

	if err := workspace.DeleteAgentMCPAccess(agentInstanceID); err != nil {
		orihttp.NotFound(w, err.Error())
		return
	}
	if err := h.store.Save(workspace); err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to save workspace: %v", err))
		return
	}

	h.publishWorkspaceMCPEvent(workspaceID, "agent_mcp_access_deleted", map[string]any{"agent_instance_id": agentInstanceID})

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]any{
		"message":           "Agent MCP access deleted successfully",
		"agent_instance_id": agentInstanceID,
		"workspace":         workspaceID,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

func (h *HTTPHandler) publishWorkspaceMCPEvent(workspaceID, action string, data map[string]any) {
	if h == nil || h.eventBus == nil {
		return
	}

	payload := map[string]any{"action": action}
	for key, value := range data {
		payload[key] = value
	}

	h.eventBus.Publish(Event{
		Type:        EventWorkspaceUpdated,
		WorkspaceID: workspaceID,
		Source:      "api",
		Data:        payload,
	})
}
