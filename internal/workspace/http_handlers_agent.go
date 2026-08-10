package workspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/types"
)

// AddAgentRequest represents the request to add an agent to a workspace
type AddAgentRequest struct {
	AgentName string `json:"agent_name"`
}

// AddAgent handles POST /api/workspaces/:id/agents
func (h *HTTPHandler) AddAgent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		// Extract workspace ID from URL path
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/workspaces/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		orihttp.BadRequest(w, "Invalid URL format")
		return
	}
	workspaceID := parts[0]

	// Parse request body

	var req AddAgentRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	if req.AgentName == "" {
		orihttp.BadRequest(w, "Agent name is required")
		// Get workspace
		return
	}

	ws, err := h.store.Get(workspaceID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace not found: %v", err))
		// Add agent using workspace method (creates stable AgentInstance)
		return
	}

	if err := ws.AddAgent(req.AgentName); err != nil {
		if errors.Is(err, ErrAgentAlreadyInWorkspace) {
			_ = orihttp.RespondError(w, http.StatusConflict, err.Error())
			return
		}
		orihttp.InternalError(w, fmt.Sprintf("Failed to add agent: %v", err))
		// Save updated workspace
		return
	}

	if err := h.store.Save(ws); err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to update workspace: %v", err))
		return
	}

	logger.Debug("Added agent to workspace", logger.Fields{"agent": req.AgentName, "workspaceID": workspaceID})

	// Return success
	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]any{
		"message":   "Agent added successfully",
		"agent":     req.AgentName,
		"workspace": workspaceID,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// RemoveAgent handles DELETE /api/workspaces/:id/agents/:agent_name
func (h *HTTPHandler) RemoveAgent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		orihttp.MethodNotAllowed(w)
		// Extract workspace ID and agent identifier from URL path
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/workspaces/")
	parts := strings.Split(path, "/")
	if len(parts) < 3 {
		orihttp.BadRequest(w, "Invalid URL format")
		return
	}
	workspaceID := parts[0]
	agentIdentifier := parts[2]

	// Get workspace
	ws, err := h.store.Get(workspaceID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace not found: %v", err))
		// Parse agent identifier to extract name and instance number
		return
	}

	var agentName string
	var instanceNumber int
	if strings.Contains(agentIdentifier, ":") {
		identParts := strings.SplitN(agentIdentifier, ":", 2)
		agentName = identParts[0]
		instanceNumber, err = strconv.Atoi(identParts[1])
		if err != nil {
			orihttp.BadRequest(w, "Invalid instance number format")
			return
		}
	} else {
		agentName = agentIdentifier
		instanceNumber = 0
	}

	// Find the specific agent instance to remove
	var targetInstanceID string

	// If we have AgentInstances, always use them (even if no instance number provided)
	if len(ws.AgentInstances) > 0 {
		if instanceNumber > 0 {
			// Find by name and instance number
			for _, inst := range ws.AgentInstances {
				if inst.Name == agentName && inst.InstanceNumber == instanceNumber {
					targetInstanceID = inst.ID
					break
				}
			}
		} else {
			// No instance number provided - find first matching agent by name
			for _, inst := range ws.AgentInstances {
				if inst.Name == agentName {
					targetInstanceID = inst.ID
					instanceNumber = inst.InstanceNumber // For logging
					break
				}
			}
		}

		if targetInstanceID == "" {
			orihttp.NotFound(w, fmt.Sprintf("Agent instance %s not found", agentName))
			return
		}

		if err := ws.RemoveAgentInstance(targetInstanceID); err != nil {
			if errors.Is(err, ErrWorkspaceEntryAgentRequired) {
				orihttp.BadRequest(w, err.Error())
				return
			}
			orihttp.InternalError(w, fmt.Sprintf("Failed to remove agent instance: %v", err))
			return
		}
	} else {

		if err := ws.RemoveAgent(agentName); err != nil {
			if errors.Is(err, ErrWorkspaceEntryAgentRequired) {
				orihttp.BadRequest(w, err.Error())
				return
			}
			orihttp.NotFound(w, fmt.Sprintf("Failed to remove agent: %v", err))
			return
		}
	}

	if err := h.store.Save(ws); err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to save workspace: %v", err))
		return
	}

	if instanceNumber > 0 {
		logger.Debug("Removed agent instance from workspace", logger.Fields{"instance_number": instanceNumber, "workspace_id": workspaceID, "agent": agentName})
	} else {
		logger.Debug("Removed agent from workspace", logger.Fields{"agent": agentName, "workspace_id": workspaceID})
	}

	// Return success
	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]any{
		"message":   "Agent removed successfully",
		"agent":     agentName,
		"workspace": workspaceID,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// ListAgentSnapshots handles GET /api/workspaces/:id/agent-snapshots and
// returns the names of agents the workspace has on-disk (or in-store)
// snapshots for. Used by the workspace health UI to distinguish a
// recoverable "snapshot present" entry agent from a truly missing one.
func (h *HTTPHandler) ListAgentSnapshots(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.PathValue("workspaceID")

	ws, err := h.store.Get(workspaceID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace %s not found", workspaceID))
		return
	}

	available := make([]string, 0)
	for _, name := range referencedAgentNames(ws) {
		if _, ok, err := h.store.GetWorkspaceAgent(workspaceID, name); err == nil && ok {
			available = append(available, name)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]any{
		"workspace_id": workspaceID,
		"agents":       available,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// WorkspaceAgentProfile is the lightweight model/provider view of a
// workspace-local agent used by the workspace UI to render and edit the agent's
// LLM model without going through the global agent store. The global agent
// dashboard list does not include workspace-local agents, so this is what lets
// the UI show their real model and offer in-place model editing.
type WorkspaceAgentProfile struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Role     string `json:"role,omitempty"`
	Model    string `json:"model"`
	Provider string `json:"provider"`
	Source   string `json:"source"` // always "workspace"
	// Appearance travels so the workspace agent detail page renders through the
	// shared resolver instead of deriving its own initials — which is how that
	// one page used to show initials for an agent everything else showed with a
	// character or an uploaded image (unified-agent-appearance FR-80/FR-89).
	Appearance *types.AgentAppearance `json:"appearance,omitempty"`
}

// ListWorkspaceAgentProfiles handles GET /api/workspaces/:id/agents and returns
// the model/provider/type for each workspace-local agent that has an on-disk
// config.json snapshot.
func (h *HTTPHandler) ListWorkspaceAgentProfiles(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.PathValue("workspaceID")

	ws, err := h.store.Get(workspaceID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace %s not found", workspaceID))
		return
	}

	profiles := make([]WorkspaceAgentProfile, 0)
	for _, name := range referencedAgentNames(ws) {
		ag, ok, agErr := h.store.GetWorkspaceAgent(workspaceID, name)
		if agErr != nil || !ok || ag == nil {
			continue
		}
		// Snapshot reads already migrate and normalize appearance, so this is
		// always the canonical object by the time it gets here.
		ag.EnsureAppearance()
		profiles = append(profiles, WorkspaceAgentProfile{
			Name:       name,
			Type:       ag.Type,
			Role:       string(ag.Role),
			Model:      ag.Settings.Model,
			Provider:   ag.Settings.Provider,
			Source:     "workspace",
			Appearance: ag.Appearance,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]any{
		"workspace_id": workspaceID,
		"agents":       profiles,
	}); encErr != nil {
		logger.Error("Failed to encode workspace agent profiles", logger.Fields{"error": encErr})
	}
}

// UpdateWorkspaceAgentModelRequest is the body for PATCH
// /api/workspaces/:id/agents/:name. Only model + provider are editable here; all
// other agent settings are intentionally left untouched.
type UpdateWorkspaceAgentModelRequest struct {
	Model       string `json:"model"`
	LLMProvider string `json:"llm_provider"`
}

// UpdateWorkspaceAgentModel handles PATCH /api/workspaces/:id/agents/:name and
// updates only the model + provider of a workspace-local agent's config.json. It
// never touches the global agent store: the workspace config.json is the single
// source of truth for these agents.
func (h *HTTPHandler) UpdateWorkspaceAgentModel(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.PathValue("workspaceID")
	agentName := r.PathValue("name")
	// Drop any ":instance" suffix — model config is per agent name (slug),
	// shared by all instances of that agent in the workspace.
	if idx := strings.Index(agentName, ":"); idx >= 0 {
		agentName = agentName[:idx]
	}
	agentName = strings.TrimSpace(agentName)
	if agentName == "" {
		orihttp.BadRequest(w, "Agent name is required")
		return
	}

	var req UpdateWorkspaceAgentModelRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	model := strings.TrimSpace(req.Model)
	provider := strings.TrimSpace(req.LLMProvider)
	if model == "" {
		orihttp.BadRequest(w, "model is required")
		return
	}
	if provider == "" {
		orihttp.BadRequest(w, "llm_provider is required")
		return
	}

	if _, err := h.store.Get(workspaceID); err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace %s not found", workspaceID))
		return
	}

	ag, ok, err := h.store.GetWorkspaceAgent(workspaceID, agentName)
	if err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to read workspace agent: %v", err))
		return
	}
	if !ok || ag == nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace agent %q not found", agentName))
		return
	}

	// Update only model + provider; leave every other field intact.
	ag.Settings.Model = model
	ag.Settings.Provider = provider

	if err := h.store.SaveWorkspaceAgent(workspaceID, agentName, ag); err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to save workspace agent: %v", err))
		return
	}

	logger.Info("Updated workspace agent model", logger.Fields{
		"workspace_id": workspaceID,
		"agent":        agentName,
		"model":        model,
		"provider":     provider,
	})

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]any{
		"message":  "Agent model updated",
		"agent":    agentName,
		"model":    model,
		"provider": provider,
		"source":   "workspace",
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// maxWorkspaceAgentSystemPromptLen bounds a workspace-local agent's editable
// base system prompt. Generous, but guards against accidental huge payloads.
const maxWorkspaceAgentSystemPromptLen = 20000

// workspaceAgentNameFromPath returns the {name} path value with any ":instance"
// suffix stripped, since agent config (model, prompt) is per agent name.
func workspaceAgentNameFromPath(r *http.Request) string {
	name := strings.TrimSpace(r.PathValue("name"))
	if idx := strings.Index(name, ":"); idx >= 0 {
		name = name[:idx]
	}
	return strings.TrimSpace(name)
}

// GetWorkspaceAgentSystemPrompt handles GET
// /api/workspaces/{workspaceID}/agents/{name}/system-prompt and returns the
// base system prompt stored in a workspace-local agent's config.json. The
// workspace config.json is the single source of truth for these agents.
func (h *HTTPHandler) GetWorkspaceAgentSystemPrompt(w http.ResponseWriter, r *http.Request) {
	workspaceID := strings.TrimSpace(r.PathValue("workspaceID"))
	agentName := workspaceAgentNameFromPath(r)
	if workspaceID == "" || agentName == "" {
		orihttp.BadRequest(w, "workspace id and agent name are required")
		return
	}

	if _, err := h.store.Get(workspaceID); err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace %s not found", workspaceID))
		return
	}

	ag, ok, err := h.store.GetWorkspaceAgent(workspaceID, agentName)
	if err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to read workspace agent: %v", err))
		return
	}
	if !ok || ag == nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace agent %q not found", agentName))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]any{
		"agent":         agentName,
		"system_prompt": ag.Settings.SystemPrompt,
		"source":        "workspace",
	}); encErr != nil {
		logger.Error("Failed to encode workspace agent system prompt", logger.Fields{"error": encErr})
	}
}

// UpdateWorkspaceAgentSystemPromptRequest is the body for PATCH
// /api/workspaces/{id}/agents/{name}/system-prompt.
type UpdateWorkspaceAgentSystemPromptRequest struct {
	SystemPrompt *string `json:"system_prompt"`
}

// UpdateWorkspaceAgentSystemPrompt handles PATCH
// /api/workspaces/{workspaceID}/agents/{name}/system-prompt and writes the base
// system prompt into the workspace-local agent's config.json. It never touches
// the global agent store; the workspace config.json is the source of truth.
func (h *HTTPHandler) UpdateWorkspaceAgentSystemPrompt(w http.ResponseWriter, r *http.Request) {
	workspaceID := strings.TrimSpace(r.PathValue("workspaceID"))
	agentName := workspaceAgentNameFromPath(r)
	if workspaceID == "" || agentName == "" {
		orihttp.BadRequest(w, "workspace id and agent name are required")
		return
	}

	var req UpdateWorkspaceAgentSystemPromptRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	if req.SystemPrompt == nil {
		orihttp.BadRequest(w, "system_prompt is required")
		return
	}
	// Trim outer whitespace (also drops stray leading/trailing newlines) while
	// preserving intentional internal formatting. Empty is allowed (clears it).
	prompt := strings.TrimSpace(*req.SystemPrompt)
	if len(prompt) > maxWorkspaceAgentSystemPromptLen {
		orihttp.BadRequest(w, fmt.Sprintf("system_prompt exceeds %d characters", maxWorkspaceAgentSystemPromptLen))
		return
	}

	if _, err := h.store.Get(workspaceID); err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace %s not found", workspaceID))
		return
	}

	ag, ok, err := h.store.GetWorkspaceAgent(workspaceID, agentName)
	if err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to read workspace agent: %v", err))
		return
	}
	if !ok || ag == nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace agent %q not found", agentName))
		return
	}

	ag.Settings.SystemPrompt = prompt
	if err := h.store.SaveWorkspaceAgent(workspaceID, agentName, ag); err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to save workspace agent: %v", err))
		return
	}

	logger.Info("Updated workspace agent system prompt", logger.Fields{
		"workspace_id": workspaceID, "agent": agentName, "length": len(prompt),
	})

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]any{
		"message":       "Agent system prompt updated",
		"agent":         agentName,
		"system_prompt": prompt,
		"source":        "workspace",
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}
