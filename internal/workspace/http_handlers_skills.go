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

// CreateSkillBinding handles POST /api/workspaces/{workspaceID}/skill-bindings
func (h *HTTPHandler) CreateSkillBinding(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.PathValue("workspaceID")
	if workspaceID == "" {
		orihttp.BadRequest(w, "workspace ID is required")
		return
	}

	var req struct {
		ID                string                `json:"id,omitempty"`
		SkillName         string                `json:"skill_name"`
		Enabled           *bool                 `json:"enabled,omitempty"`
		Trusted           *bool                 `json:"trusted,omitempty"`
		Config            map[string]any        `json:"config,omitempty"`
		DefaultSideEffect SideEffect            `json:"default_side_effect,omitempty"`
		ToolOverrides     map[string]SideEffect `json:"tool_overrides,omitempty"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.SkillName) == "" {
		orihttp.BadRequest(w, "skill_name is required")
		return
	}
	if req.DefaultSideEffect != "" && !isValidSideEffect(req.DefaultSideEffect) {
		orihttp.BadRequest(w, fmt.Sprintf("invalid default_side_effect: %q", req.DefaultSideEffect))
		return
	}
	for tool, se := range req.ToolOverrides {
		if se != "" && !isValidSideEffect(se) {
			orihttp.BadRequest(w, fmt.Sprintf("invalid tool_overrides[%q]: %q", tool, se))
			return
		}
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
	if _, exists := workspace.GetSkillBinding(bindingID); exists {
		orihttp.BadRequest(w, fmt.Sprintf("skill binding %s already exists", bindingID))
		return
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	trusted := false
	if req.Trusted != nil {
		trusted = *req.Trusted
	}

	binding := SkillBinding{
		ID:                bindingID,
		SkillName:         req.SkillName,
		Enabled:           enabled,
		Trusted:           trusted,
		Config:            req.Config,
		DefaultSideEffect: req.DefaultSideEffect,
		ToolOverrides:     req.ToolOverrides,
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}

	if err := workspace.UpsertSkillBinding(binding); err != nil {
		orihttp.BadRequest(w, err.Error())
		return
	}
	if err := h.store.Save(workspace); err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to save workspace: %v", err))
		return
	}

	created, _ := workspace.GetSkillBinding(binding.ID)
	if created == nil {
		created = &binding
	}
	h.publishWorkspaceSkillEvent(workspaceID, "skill_binding_created", map[string]any{"binding": created})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if encErr := json.NewEncoder(w).Encode(map[string]any{
		"message":   "Skill binding created successfully",
		"binding":   created,
		"workspace": workspaceID,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// ListSkillBindings handles GET /api/workspaces/{workspaceID}/skill-bindings
func (h *HTTPHandler) ListSkillBindings(w http.ResponseWriter, r *http.Request) {
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

	bindings := workspace.GetSkillBindings()
	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]any{
		"bindings":  bindings,
		"count":     len(bindings),
		"workspace": workspaceID,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// GetSkillBindingByID handles GET /api/workspaces/{workspaceID}/skill-bindings/{bindingID}
func (h *HTTPHandler) GetSkillBindingByID(w http.ResponseWriter, r *http.Request) {
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

	binding, exists := workspace.GetSkillBinding(bindingID)
	if !exists {
		orihttp.NotFound(w, fmt.Sprintf("skill binding %s not found", bindingID))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]any{
		"binding":   binding,
		"workspace": workspaceID,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// UpdateSkillBinding handles PUT/PATCH /api/workspaces/{workspaceID}/skill-bindings/{bindingID}
func (h *HTTPHandler) UpdateSkillBinding(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.PathValue("workspaceID")
	bindingID := r.PathValue("bindingID")
	if workspaceID == "" || bindingID == "" {
		orihttp.BadRequest(w, "workspace ID and binding ID are required")
		return
	}

	var req struct {
		SkillName         *string                `json:"skill_name,omitempty"`
		Enabled           *bool                  `json:"enabled,omitempty"`
		Trusted           *bool                  `json:"trusted,omitempty"`
		Config            map[string]any         `json:"config,omitempty"`
		DefaultSideEffect *SideEffect            `json:"default_side_effect,omitempty"`
		ToolOverrides     *map[string]SideEffect `json:"tool_overrides,omitempty"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	if req.DefaultSideEffect != nil && *req.DefaultSideEffect != "" && !isValidSideEffect(*req.DefaultSideEffect) {
		orihttp.BadRequest(w, fmt.Sprintf("invalid default_side_effect: %q", *req.DefaultSideEffect))
		return
	}
	if req.ToolOverrides != nil {
		for tool, se := range *req.ToolOverrides {
			if se != "" && !isValidSideEffect(se) {
				orihttp.BadRequest(w, fmt.Sprintf("invalid tool_overrides[%q]: %q", tool, se))
				return
			}
		}
	}

	workspace, err := h.store.Get(workspaceID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace not found: %v", err))
		return
	}

	binding, exists := workspace.GetSkillBinding(bindingID)
	if !exists {
		orihttp.NotFound(w, fmt.Sprintf("skill binding %s not found", bindingID))
		return
	}

	if req.SkillName != nil {
		binding.SkillName = *req.SkillName
	}
	if req.Enabled != nil {
		binding.Enabled = *req.Enabled
	}
	if req.Trusted != nil {
		binding.Trusted = *req.Trusted
	}
	if req.Config != nil {
		binding.Config = req.Config
	}
	if req.DefaultSideEffect != nil {
		binding.DefaultSideEffect = *req.DefaultSideEffect
	}
	if req.ToolOverrides != nil {
		binding.ToolOverrides = *req.ToolOverrides
	}

	if err := workspace.UpsertSkillBinding(*binding); err != nil {
		orihttp.BadRequest(w, err.Error())
		return
	}
	if err := h.store.Save(workspace); err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to save workspace: %v", err))
		return
	}

	updated, _ := workspace.GetSkillBinding(bindingID)
	if updated == nil {
		updated = binding
	}
	h.publishWorkspaceSkillEvent(workspaceID, "skill_binding_updated", map[string]any{"binding": updated})

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]any{
		"message":   "Skill binding updated successfully",
		"binding":   updated,
		"workspace": workspaceID,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// DeleteSkillBinding handles DELETE /api/workspaces/{workspaceID}/skill-bindings/{bindingID}
func (h *HTTPHandler) DeleteSkillBinding(w http.ResponseWriter, r *http.Request) {
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

	if err := workspace.DeleteSkillBinding(bindingID); err != nil {
		orihttp.NotFound(w, err.Error())
		return
	}
	if err := h.store.Save(workspace); err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to save workspace: %v", err))
		return
	}

	h.publishWorkspaceSkillEvent(workspaceID, "skill_binding_deleted", map[string]any{"binding_id": bindingID})

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]any{
		"message":    "Skill binding deleted successfully",
		"binding_id": bindingID,
		"workspace":  workspaceID,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// ListAgentSkillAccess handles GET /api/workspaces/{workspaceID}/agent-skill-access
func (h *HTTPHandler) ListAgentSkillAccess(w http.ResponseWriter, r *http.Request) {
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

	entries := workspace.ListAgentSkillAccess()
	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]any{
		"access":    entries,
		"count":     len(entries),
		"workspace": workspaceID,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// GetAgentSkillAccessEntry handles GET /api/workspaces/{workspaceID}/agent-skill-access/{agentInstanceID}
func (h *HTTPHandler) GetAgentSkillAccessEntry(w http.ResponseWriter, r *http.Request) {
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

	entry, exists := workspace.GetAgentSkillAccess(agentInstanceID)
	if !exists {
		orihttp.NotFound(w, fmt.Sprintf("agent skill access %s not found", agentInstanceID))
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

// UpdateAgentSkillAccess handles PUT/PATCH /api/workspaces/{workspaceID}/agent-skill-access/{agentInstanceID}
func (h *HTTPHandler) UpdateAgentSkillAccess(w http.ResponseWriter, r *http.Request) {
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
		bindings := workspace.GetSkillBindings()
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

	entry := AgentSkillAccess{
		AgentInstanceID:   agentInstanceID,
		EnabledBindingIDs: req.EnabledBindingIDs,
		UpdatedAt:         time.Now(),
	}
	if err := workspace.SetAgentSkillAccess(entry); err != nil {
		orihttp.BadRequest(w, err.Error())
		return
	}
	// Keep the explicit Toolbox authoritative: on a migrated workspace the
	// runtime reads the pinned Toolbox and never this entry, so writing only
	// the entry would silently change nothing (PRD FR-36).
	assignment, bridged, err := ApplyLegacyAccessToToolbox(workspace, agentInstanceID, LegacyAccessSkills, req.EnabledBindingIDs, "legacy-skill-access-api")
	if err != nil {
		orihttp.BadRequest(w, fmt.Sprintf("Failed to update the agent's toolbox: %v", err))
		return
	}
	if err := h.store.Save(workspace); err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to save workspace: %v", err))
		return
	}

	updated, _ := workspace.GetAgentSkillAccess(agentInstanceID)
	if updated == nil {
		updated = &entry
	}
	eventData := map[string]any{"access": updated}
	if bridged {
		eventData["toolbox_assignment"] = assignment
	}
	h.publishWorkspaceSkillEvent(workspaceID, "agent_skill_access_updated", eventData)

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]any{
		"message":   "Agent skill access updated successfully",
		"access":    updated,
		"workspace": workspaceID,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// DeleteAgentSkillAccess handles DELETE /api/workspaces/{workspaceID}/agent-skill-access/{agentInstanceID}
func (h *HTTPHandler) DeleteAgentSkillAccess(w http.ResponseWriter, r *http.Request) {
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

	if err := workspace.DeleteAgentSkillAccess(agentInstanceID); err != nil {
		orihttp.NotFound(w, err.Error())
		return
	}
	if err := h.store.Save(workspace); err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to save workspace: %v", err))
		return
	}

	h.publishWorkspaceSkillEvent(workspaceID, "agent_skill_access_deleted", map[string]any{"agent_instance_id": agentInstanceID})

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]any{
		"message":           "Agent skill access deleted successfully",
		"agent_instance_id": agentInstanceID,
		"workspace":         workspaceID,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

func (h *HTTPHandler) publishWorkspaceSkillEvent(workspaceID, action string, data map[string]any) {
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
