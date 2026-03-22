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

// CreateSkillBinding handles POST /api/studios/{studioID}/skill-bindings
func (h *HTTPHandler) CreateSkillBinding(w http.ResponseWriter, r *http.Request) {
	studioID := r.PathValue("studioID")
	if studioID == "" {
		orihttp.BadRequest(w, "studio ID is required")
		return
	}

	var req struct {
		ID        string                 `json:"id,omitempty"`
		SkillName string                 `json:"skill_name"`
		Enabled   *bool                  `json:"enabled,omitempty"`
		Trusted   *bool                  `json:"trusted,omitempty"`
		Config    map[string]interface{} `json:"config,omitempty"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.SkillName) == "" {
		orihttp.BadRequest(w, "skill_name is required")
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
	if _, exists := studio.GetSkillBinding(bindingID); exists {
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

	binding := WorkspaceSkillBinding{
		ID:        bindingID,
		SkillName: req.SkillName,
		Enabled:   enabled,
		Trusted:   trusted,
		Config:    req.Config,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if err := studio.UpsertSkillBinding(binding); err != nil {
		orihttp.BadRequest(w, err.Error())
		return
	}
	if err := h.store.Save(studio); err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to save studio: %v", err))
		return
	}

	created, _ := studio.GetSkillBinding(binding.ID)
	if created == nil {
		created = &binding
	}
	h.publishWorkspaceSkillEvent(studioID, "skill_binding_created", map[string]interface{}{"binding": created})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if encErr := json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Skill binding created successfully",
		"binding": created,
		"studio":  studioID,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// ListSkillBindings handles GET /api/studios/{studioID}/skill-bindings
func (h *HTTPHandler) ListSkillBindings(w http.ResponseWriter, r *http.Request) {
	studioID := r.PathValue("studioID")
	if studioID == "" {
		orihttp.BadRequest(w, "studio ID is required")
		return
	}

	studio, err := h.store.Get(studioID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Studio not found: %v", err))
		return
	}

	bindings := studio.GetSkillBindings()
	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]interface{}{
		"bindings": bindings,
		"count":    len(bindings),
		"studio":   studioID,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// GetSkillBindingByID handles GET /api/studios/{studioID}/skill-bindings/{bindingID}
func (h *HTTPHandler) GetSkillBindingByID(w http.ResponseWriter, r *http.Request) {
	studioID := r.PathValue("studioID")
	bindingID := r.PathValue("bindingID")
	if studioID == "" || bindingID == "" {
		orihttp.BadRequest(w, "studio ID and binding ID are required")
		return
	}

	studio, err := h.store.Get(studioID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Studio not found: %v", err))
		return
	}

	binding, exists := studio.GetSkillBinding(bindingID)
	if !exists {
		orihttp.NotFound(w, fmt.Sprintf("skill binding %s not found", bindingID))
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

// UpdateSkillBinding handles PUT/PATCH /api/studios/{studioID}/skill-bindings/{bindingID}
func (h *HTTPHandler) UpdateSkillBinding(w http.ResponseWriter, r *http.Request) {
	studioID := r.PathValue("studioID")
	bindingID := r.PathValue("bindingID")
	if studioID == "" || bindingID == "" {
		orihttp.BadRequest(w, "studio ID and binding ID are required")
		return
	}

	var req struct {
		SkillName *string                `json:"skill_name,omitempty"`
		Enabled   *bool                  `json:"enabled,omitempty"`
		Trusted   *bool                  `json:"trusted,omitempty"`
		Config    map[string]interface{} `json:"config,omitempty"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	studio, err := h.store.Get(studioID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Studio not found: %v", err))
		return
	}

	binding, exists := studio.GetSkillBinding(bindingID)
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

	if err := studio.UpsertSkillBinding(*binding); err != nil {
		orihttp.BadRequest(w, err.Error())
		return
	}
	if err := h.store.Save(studio); err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to save studio: %v", err))
		return
	}

	updated, _ := studio.GetSkillBinding(bindingID)
	if updated == nil {
		updated = binding
	}
	h.publishWorkspaceSkillEvent(studioID, "skill_binding_updated", map[string]interface{}{"binding": updated})

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Skill binding updated successfully",
		"binding": updated,
		"studio":  studioID,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// DeleteSkillBinding handles DELETE /api/studios/{studioID}/skill-bindings/{bindingID}
func (h *HTTPHandler) DeleteSkillBinding(w http.ResponseWriter, r *http.Request) {
	studioID := r.PathValue("studioID")
	bindingID := r.PathValue("bindingID")
	if studioID == "" || bindingID == "" {
		orihttp.BadRequest(w, "studio ID and binding ID are required")
		return
	}

	studio, err := h.store.Get(studioID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Studio not found: %v", err))
		return
	}

	if err := studio.DeleteSkillBinding(bindingID); err != nil {
		orihttp.NotFound(w, err.Error())
		return
	}
	if err := h.store.Save(studio); err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to save studio: %v", err))
		return
	}

	h.publishWorkspaceSkillEvent(studioID, "skill_binding_deleted", map[string]interface{}{"binding_id": bindingID})

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]interface{}{
		"message":    "Skill binding deleted successfully",
		"binding_id": bindingID,
		"studio":     studioID,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// ListAgentSkillAccess handles GET /api/studios/{studioID}/agent-skill-access
func (h *HTTPHandler) ListAgentSkillAccess(w http.ResponseWriter, r *http.Request) {
	studioID := r.PathValue("studioID")
	if studioID == "" {
		orihttp.BadRequest(w, "studio ID is required")
		return
	}

	studio, err := h.store.Get(studioID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Studio not found: %v", err))
		return
	}

	entries := studio.ListAgentSkillAccess()
	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]interface{}{
		"access": entries,
		"count":  len(entries),
		"studio": studioID,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// GetAgentSkillAccessEntry handles GET /api/studios/{studioID}/agent-skill-access/{agentInstanceID}
func (h *HTTPHandler) GetAgentSkillAccessEntry(w http.ResponseWriter, r *http.Request) {
	studioID := r.PathValue("studioID")
	agentInstanceID := r.PathValue("agentInstanceID")
	if studioID == "" || agentInstanceID == "" {
		orihttp.BadRequest(w, "studio ID and agent instance ID are required")
		return
	}

	studio, err := h.store.Get(studioID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Studio not found: %v", err))
		return
	}

	entry, exists := studio.GetAgentSkillAccess(agentInstanceID)
	if !exists {
		orihttp.NotFound(w, fmt.Sprintf("agent skill access %s not found", agentInstanceID))
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

// UpdateAgentSkillAccess handles PUT/PATCH /api/studios/{studioID}/agent-skill-access/{agentInstanceID}
func (h *HTTPHandler) UpdateAgentSkillAccess(w http.ResponseWriter, r *http.Request) {
	studioID := r.PathValue("studioID")
	agentInstanceID := r.PathValue("agentInstanceID")
	if studioID == "" || agentInstanceID == "" {
		orihttp.BadRequest(w, "studio ID and agent instance ID are required")
		return
	}

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

	// Validate that all referenced binding IDs exist in the workspace
	if len(req.EnabledBindingIDs) > 0 {
		bindings := studio.GetSkillBindings()
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

	entry := WorkspaceAgentSkillAccess{
		AgentInstanceID:   agentInstanceID,
		EnabledBindingIDs: req.EnabledBindingIDs,
		UpdatedAt:         time.Now(),
	}
	if err := studio.SetAgentSkillAccess(entry); err != nil {
		orihttp.BadRequest(w, err.Error())
		return
	}
	if err := h.store.Save(studio); err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to save studio: %v", err))
		return
	}

	updated, _ := studio.GetAgentSkillAccess(agentInstanceID)
	if updated == nil {
		updated = &entry
	}
	h.publishWorkspaceSkillEvent(studioID, "agent_skill_access_updated", map[string]interface{}{"access": updated})

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Agent skill access updated successfully",
		"access":  updated,
		"studio":  studioID,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// DeleteAgentSkillAccess handles DELETE /api/studios/{studioID}/agent-skill-access/{agentInstanceID}
func (h *HTTPHandler) DeleteAgentSkillAccess(w http.ResponseWriter, r *http.Request) {
	studioID := r.PathValue("studioID")
	agentInstanceID := r.PathValue("agentInstanceID")
	if studioID == "" || agentInstanceID == "" {
		orihttp.BadRequest(w, "studio ID and agent instance ID are required")
		return
	}

	studio, err := h.store.Get(studioID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Studio not found: %v", err))
		return
	}

	if err := studio.DeleteAgentSkillAccess(agentInstanceID); err != nil {
		orihttp.NotFound(w, err.Error())
		return
	}
	if err := h.store.Save(studio); err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to save studio: %v", err))
		return
	}

	h.publishWorkspaceSkillEvent(studioID, "agent_skill_access_deleted", map[string]interface{}{"agent_instance_id": agentInstanceID})

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]interface{}{
		"message":           "Agent skill access deleted successfully",
		"agent_instance_id": agentInstanceID,
		"studio":            studioID,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

func (h *HTTPHandler) publishWorkspaceSkillEvent(studioID, action string, data map[string]interface{}) {
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
