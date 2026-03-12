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

func parseStudioPathParts(path, prefix string) ([]string, bool) {
	trimmed := strings.TrimPrefix(path, prefix)
	parts := strings.Split(trimmed, "/")
	if len(parts) < 2 || strings.TrimSpace(parts[0]) == "" {
		return nil, false
	}
	return parts, true
}

// CreateMCPBinding handles POST /api/studios/:id/mcp-bindings
func (h *HTTPHandler) CreateMCPBinding(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}

	parts, ok := parseStudioPathParts(r.URL.Path, "/api/studios/")
	if !ok {
		orihttp.BadRequest(w, "Invalid URL format")
		return
	}
	studioID := parts[0]

	var req struct {
		ID         string                 `json:"id,omitempty"`
		ServerName string                 `json:"server_name"`
		Alias      string                 `json:"alias,omitempty"`
		Enabled    *bool                  `json:"enabled,omitempty"`
		Scope      map[string]interface{} `json:"scope,omitempty"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.ServerName) == "" {
		orihttp.BadRequest(w, "server_name is required")
		return
	}

	studio, err := h.store.Get(studioID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Studio not found: %v", err))
		return
	}

	bindingID := strings.TrimSpace(req.ID)
	if bindingID == "" {
		bindingID = uuid.New().String()
	}
	if _, exists := studio.GetMCPBinding(bindingID); exists {
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
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}

	if err := studio.UpsertMCPBinding(binding); err != nil {
		orihttp.BadRequest(w, err.Error())
		return
	}
	if err := h.store.Save(studio); err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to save studio: %v", err))
		return
	}

	created, _ := studio.GetMCPBinding(binding.ID)
	if created == nil {
		created = &binding
	}
	h.publishWorkspaceMCPEvent(studioID, "mcp_binding_created", map[string]interface{}{"binding": created})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if encErr := json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "MCP binding created successfully",
		"binding": created,
		"studio":  studioID,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// ListMCPBindings handles GET /api/studios/:id/mcp-bindings
func (h *HTTPHandler) ListMCPBindings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		orihttp.MethodNotAllowed(w)
		return
	}

	parts, ok := parseStudioPathParts(r.URL.Path, "/api/studios/")
	if !ok {
		orihttp.BadRequest(w, "Invalid URL format")
		return
	}
	studioID := parts[0]

	studio, err := h.store.Get(studioID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Studio not found: %v", err))
		return
	}

	bindings := studio.GetMCPBindings()
	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]interface{}{
		"bindings": bindings,
		"count":    len(bindings),
		"studio":   studioID,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// GetMCPBinding handles GET /api/studios/:id/mcp-bindings/:binding_id
func (h *HTTPHandler) GetMCPBinding(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		orihttp.MethodNotAllowed(w)
		return
	}

	parts, ok := parseStudioPathParts(r.URL.Path, "/api/studios/")
	if !ok || len(parts) < 3 {
		orihttp.BadRequest(w, "Invalid URL format")
		return
	}
	studioID := parts[0]
	bindingID := parts[2]

	studio, err := h.store.Get(studioID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Studio not found: %v", err))
		return
	}

	binding, exists := studio.GetMCPBinding(bindingID)
	if !exists {
		orihttp.NotFound(w, fmt.Sprintf("MCP binding %s not found", bindingID))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]interface{}{
		"binding": binding,
		"studio":  studioID,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// UpdateMCPBinding handles PUT/PATCH /api/studios/:id/mcp-bindings/:binding_id
func (h *HTTPHandler) UpdateMCPBinding(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut && r.Method != http.MethodPatch {
		orihttp.MethodNotAllowed(w)
		return
	}

	parts, ok := parseStudioPathParts(r.URL.Path, "/api/studios/")
	if !ok || len(parts) < 3 {
		orihttp.BadRequest(w, "Invalid URL format")
		return
	}
	studioID := parts[0]
	bindingID := parts[2]

	var req struct {
		ServerName *string                `json:"server_name,omitempty"`
		Alias      *string                `json:"alias,omitempty"`
		Enabled    *bool                  `json:"enabled,omitempty"`
		Scope      map[string]interface{} `json:"scope,omitempty"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	studio, err := h.store.Get(studioID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Studio not found: %v", err))
		return
	}

	binding, exists := studio.GetMCPBinding(bindingID)
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

	if err := studio.UpsertMCPBinding(*binding); err != nil {
		orihttp.BadRequest(w, err.Error())
		return
	}
	if err := h.store.Save(studio); err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to save studio: %v", err))
		return
	}

	updated, _ := studio.GetMCPBinding(bindingID)
	if updated == nil {
		updated = binding
	}
	h.publishWorkspaceMCPEvent(studioID, "mcp_binding_updated", map[string]interface{}{"binding": updated})

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "MCP binding updated successfully",
		"binding": updated,
		"studio":  studioID,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// DeleteMCPBinding handles DELETE /api/studios/:id/mcp-bindings/:binding_id
func (h *HTTPHandler) DeleteMCPBinding(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		orihttp.MethodNotAllowed(w)
		return
	}

	parts, ok := parseStudioPathParts(r.URL.Path, "/api/studios/")
	if !ok || len(parts) < 3 {
		orihttp.BadRequest(w, "Invalid URL format")
		return
	}
	studioID := parts[0]
	bindingID := parts[2]

	studio, err := h.store.Get(studioID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Studio not found: %v", err))
		return
	}

	if err := studio.DeleteMCPBinding(bindingID); err != nil {
		orihttp.NotFound(w, err.Error())
		return
	}
	if err := h.store.Save(studio); err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to save studio: %v", err))
		return
	}

	h.publishWorkspaceMCPEvent(studioID, "mcp_binding_deleted", map[string]interface{}{"binding_id": bindingID})

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]interface{}{
		"message":    "MCP binding deleted successfully",
		"binding_id": bindingID,
		"studio":     studioID,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// ListAgentMCPAccess handles GET /api/studios/:id/agent-mcp-access
func (h *HTTPHandler) ListAgentMCPAccess(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		orihttp.MethodNotAllowed(w)
		return
	}

	parts, ok := parseStudioPathParts(r.URL.Path, "/api/studios/")
	if !ok {
		orihttp.BadRequest(w, "Invalid URL format")
		return
	}
	studioID := parts[0]

	studio, err := h.store.Get(studioID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Studio not found: %v", err))
		return
	}

	entries := studio.ListAgentMCPAccess()
	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]interface{}{
		"access": entries,
		"count":  len(entries),
		"studio": studioID,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// GetAgentMCPAccessEntry handles GET /api/studios/:id/agent-mcp-access/:agent_instance_id
func (h *HTTPHandler) GetAgentMCPAccessEntry(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		orihttp.MethodNotAllowed(w)
		return
	}

	parts, ok := parseStudioPathParts(r.URL.Path, "/api/studios/")
	if !ok || len(parts) < 3 {
		orihttp.BadRequest(w, "Invalid URL format")
		return
	}
	studioID := parts[0]
	agentInstanceID := parts[2]

	studio, err := h.store.Get(studioID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Studio not found: %v", err))
		return
	}

	entry, exists := studio.GetAgentMCPAccess(agentInstanceID)
	if !exists {
		orihttp.NotFound(w, fmt.Sprintf("agent MCP access %s not found", agentInstanceID))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]interface{}{
		"access": entry,
		"studio": studioID,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// UpdateAgentMCPAccess handles PUT/PATCH /api/studios/:id/agent-mcp-access/:agent_instance_id
func (h *HTTPHandler) UpdateAgentMCPAccess(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut && r.Method != http.MethodPatch {
		orihttp.MethodNotAllowed(w)
		return
	}

	parts, ok := parseStudioPathParts(r.URL.Path, "/api/studios/")
	if !ok || len(parts) < 3 {
		orihttp.BadRequest(w, "Invalid URL format")
		return
	}
	studioID := parts[0]
	agentInstanceID := parts[2]

	var req struct {
		EnabledBindingIDs []string `json:"enabled_binding_ids,omitempty"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	studio, err := h.store.Get(studioID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Studio not found: %v", err))
		return
	}

	entry := WorkspaceAgentMCPAccess{
		AgentInstanceID:   agentInstanceID,
		EnabledBindingIDs: req.EnabledBindingIDs,
		UpdatedAt:         time.Now(),
	}
	if err := studio.SetAgentMCPAccess(entry); err != nil {
		orihttp.BadRequest(w, err.Error())
		return
	}
	if err := h.store.Save(studio); err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to save studio: %v", err))
		return
	}

	updated, _ := studio.GetAgentMCPAccess(agentInstanceID)
	if updated == nil {
		updated = &entry
	}
	h.publishWorkspaceMCPEvent(studioID, "agent_mcp_access_updated", map[string]interface{}{"access": updated})

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Agent MCP access updated successfully",
		"access":  updated,
		"studio":  studioID,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// DeleteAgentMCPAccess handles DELETE /api/studios/:id/agent-mcp-access/:agent_instance_id
func (h *HTTPHandler) DeleteAgentMCPAccess(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		orihttp.MethodNotAllowed(w)
		return
	}

	parts, ok := parseStudioPathParts(r.URL.Path, "/api/studios/")
	if !ok || len(parts) < 3 {
		orihttp.BadRequest(w, "Invalid URL format")
		return
	}
	studioID := parts[0]
	agentInstanceID := parts[2]

	studio, err := h.store.Get(studioID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Studio not found: %v", err))
		return
	}

	if err := studio.DeleteAgentMCPAccess(agentInstanceID); err != nil {
		orihttp.NotFound(w, err.Error())
		return
	}
	if err := h.store.Save(studio); err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to save studio: %v", err))
		return
	}

	h.publishWorkspaceMCPEvent(studioID, "agent_mcp_access_deleted", map[string]interface{}{"agent_instance_id": agentInstanceID})

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]interface{}{
		"message":           "Agent MCP access deleted successfully",
		"agent_instance_id": agentInstanceID,
		"studio":            studioID,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

func (h *HTTPHandler) publishWorkspaceMCPEvent(studioID, action string, data map[string]interface{}) {
	if h == nil || h.eventBus == nil {
		return
	}

	payload := map[string]interface{}{"action": action}
	for key, value := range data {
		payload[key] = value
	}

	h.eventBus.Publish(Event{
		Type:        EventWorkspaceUpdated,
		WorkspaceID: studioID,
		Source:      "api",
		Data:        payload,
	})
}
