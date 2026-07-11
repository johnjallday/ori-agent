package agenthttp

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/cliagent"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// SessionPurger removes sessions tied to a deleted agent so the UI cannot
// resolve stale references. Implemented by the session HybridStore.
type SessionPurger interface {
	DeleteSessionsByAgent(ctx context.Context, agentName string) (int, error)
}

// validAgentNameRegex defines the allowed characters for agent names
// Only alphanumeric, underscores, hyphens, and spaces are allowed
var validAgentNameRegex = regexp.MustCompile(`^[a-zA-Z0-9_\- ]+$`)

const (
	minAgentNameLength = 1
	maxAgentNameLength = 100
)

// validateAgentName validates that an agent name is safe for filesystem operations
func validateAgentName(name string) error {
	if len(name) < minAgentNameLength {
		return fmt.Errorf("agent name cannot be empty")
	}
	if len(name) > maxAgentNameLength {
		return fmt.Errorf("agent name too long (max %d characters)", maxAgentNameLength)
	}
	if !validAgentNameRegex.MatchString(name) {
		return fmt.Errorf("agent name contains invalid characters (only alphanumeric, spaces, underscores, and hyphens allowed)")
	}
	return nil
}

func cloneAgentEvolution(ag *agent.Agent) *types.AgentEvolution {
	if ag == nil || ag.Evolution == nil {
		return nil
	}
	copy := *ag.Evolution
	copy.EnsureDefaults()
	return &copy
}

func cloneRoutingProfile(profile *types.AgentRoutingProfile) *types.AgentRoutingProfile {
	if profile == nil {
		return nil
	}

	copy := *profile
	if len(profile.MatchPhrases) > 0 {
		copy.MatchPhrases = append([]string{}, profile.MatchPhrases...)
	}
	if len(profile.ExampleRequests) > 0 {
		copy.ExampleRequests = append([]string{}, profile.ExampleRequests...)
	}
	if len(profile.Domains) > 0 {
		copy.Domains = append([]string{}, profile.Domains...)
	}
	if len(profile.ExternalSystems) > 0 {
		copy.ExternalSystems = append([]string{}, profile.ExternalSystems...)
	}
	return &copy
}

type Handler struct {
	State            store.Store
	ActivityLogger   *ActivityLogger
	cliAgentRegistry *cliagent.CLIAgentRegistry
	workspaceStore   workspace.Store
	sessionPurger    SessionPurger
	// claudeSync returns read-only synced ~/.claude data for the Claude Code
	// agent (or nil when disabled). Injected so agenthttp stays decoupled from
	// the externalagents package.
	claudeSync func() any
	// codexSync returns read-only synced ~/.codex data for the Codex agent (or
	// nil when disabled). Injected so agenthttp stays decoupled from the
	// externalagents package.
	codexSync func() any
}

func New(state store.Store) *Handler {
	return &Handler{
		State:          state,
		ActivityLogger: nil, // Will be set by server initialization
	}
}

// SetCLIAgentRegistry wires the CLI agent registry so auto-detected
// CLI agents appear in the main agent list.
func (h *Handler) SetCLIAgentRegistry(r *cliagent.CLIAgentRegistry) {
	h.cliAgentRegistry = r
}

// SetWorkspaceStore wires the workspace store so workspace-scoped entry
// agents can be annotated in the /api/agents list response. Clients such as
// the sidebar use the annotation to hide agents that belong to a workspace.
func (h *Handler) SetWorkspaceStore(s workspace.Store) {
	h.workspaceStore = s
}

// SetSessionPurger wires the session store so that deleting an agent also
// removes its chat sessions (and dependent rows). Without this the UI can
// keep referring to a deleted agent through restored session state.
func (h *Handler) SetSessionPurger(p SessionPurger) {
	h.sessionPurger = p
}

// SetClaudeSyncProvider wires a provider of read-only ~/.claude data, attached
// to the Claude Code agent's detail response when available.
func (h *Handler) SetClaudeSyncProvider(provider func() any) {
	h.claudeSync = provider
}

// SetCodexSyncProvider wires a provider of read-only ~/.codex data, attached
// to the Codex agent's detail response when available.
func (h *Handler) SetCodexSyncProvider(provider func() any) {
	h.codexSync = provider
}

// ServeHTTP dispatches /api/agents requests by HTTP method; each verb is
// handled by a dedicated method below.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.handleGet(w, r)
	case http.MethodPost:
		h.handleCreate(w, r)
	case http.MethodPut:
		orihttp.WriteJSON(w, map[string]any{
			"success":    false,
			"deprecated": true,
			"message":    "Global agent switching is deprecated. Use Assistant sessions or pin a specialist to a specific session instead.",
		})
		return
	case http.MethodPatch:
		h.handleUpdate(w, r)
	case http.MethodDelete:
		h.handleDelete(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// handleGet serves GET /api/agents (list) and GET /api/agents/{name}
// (detail, falling back to CLI agent details for auto-detected CLIs).
func (h *Handler) handleGet(w http.ResponseWriter, r *http.Request) {
	// Check if requesting a specific agent: /api/agents/{name} or /api/agents?name=...
	agentName := orihttp.GetPathParamOrQuery(r, "/api/agents/", "name")

	// If specific agent requested, return its details
	if agentName != "" {
		agent, ok := h.State.GetAgent(agentName)
		if !ok || agent == nil {
			// Check if it's a CLI agent
			if resp, found := h.getCLIAgentDetail(agentName); found {
				orihttp.WriteJSON(w, resp)
				return
			}
			orihttp.NotFound(w, "Agent not found")
			return
		}

		orihttp.WriteJSON(w, map[string]any{
			"name":              agentName,
			"type":              agent.Type,
			"role":              agent.Role,
			"capabilities":      agent.Capabilities,
			"status":            agent.Status,
			"model":             agent.Settings.Model,
			"temperature":       agent.Settings.Temperature,
			"provider":          agent.Settings.Provider,
			"reasoning_effort":  agent.Settings.EffectiveReasoningEffort(agent.Settings.Provider),
			"max_output_tokens": agent.Settings.MaxOutputTokens,
			"system_prompt":     agent.Settings.SystemPrompt,
			"allow_web_search":  agent.Settings.IsWebSearchAllowed(),
			"metadata":          agent.Metadata,
			"evolution":         cloneAgentEvolution(agent),
			"source":            "user",
		})
		return
	}

	// Otherwise, return list of all agents
	names := h.State.ListAgents()

	// Per-agent workspace membership (lowercase agent name → attached
	// workspaces), computed from the metadata-only cache. Every referenced
	// definition is annotated with workspace_count + workspaces; scope is no
	// longer used to hide workspace entry agents (PRD FR1/FR2).
	memberships := workspace.AgentWorkspaceMemberships(h.workspaceStore)

	// Build agent details list with name and type
	type AgentInfo struct {
		Name           string                   `json:"name"`
		Type           string                   `json:"type"`
		Source         string                   `json:"source"`
		Scope          string                   `json:"scope,omitempty"`
		WorkspaceID    string                   `json:"workspace_id,omitempty"`
		WorkspaceCount int                      `json:"workspace_count"`
		Workspaces     []workspace.WorkspaceRef `json:"workspaces,omitempty"`
		Status         types.AgentStatus        `json:"status,omitempty"`
		Evolution      *types.AgentEvolution    `json:"evolution,omitempty"`
	}
	annotate := func(info AgentInfo) AgentInfo {
		if m, ok := memberships[strings.ToLower(strings.TrimSpace(info.Name))]; ok {
			info.WorkspaceCount = m.Count
			info.Workspaces = m.Workspaces
			for _, ref := range m.Workspaces {
				if ref.EntryPoint {
					info.WorkspaceID = ref.ID
					break
				}
			}
		}
		return info
	}
	agentInfos := make([]AgentInfo, 0, len(names))
	for _, name := range names {
		agent, ok := h.State.GetAgent(name)
		if ok && agent != nil {
			agentInfos = append(agentInfos, annotate(AgentInfo{
				Name:      name,
				Type:      agent.Type,
				Source:    "user",
				Status:    agent.Status,
				Evolution: cloneAgentEvolution(agent),
			}))
		} else {
			// Fallback for agents that couldn't be loaded
			agentInfos = append(agentInfos, annotate(AgentInfo{
				Name:   name,
				Type:   "tool-calling", // default
				Source: "user",
			}))
		}
	}

	// Append auto-detected CLI agents
	if h.cliAgentRegistry != nil {
		for _, info := range h.cliAgentRegistry.List() {
			if !info.Available {
				continue
			}
			agentInfos = append(agentInfos, AgentInfo{
				Name:   cliAgentDisplayName(info.Backend),
				Type:   "research",
				Source: "cli",
				Status: getCLIAgentOperationalStatus(info.Backend),
			})
		}
	}

	orihttp.WriteJSON(w, map[string]any{
		"agents": agentInfos,
	})
}

// handleCreate serves POST /api/agents.
func (h *Handler) handleCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name            string                     `json:"name"`
		Type            string                     `json:"type,omitempty"`
		Role            string                     `json:"role,omitempty"`
		Model           string                     `json:"model,omitempty"`
		Temperature     float64                    `json:"temperature,omitempty"`
		SystemPrompt    string                     `json:"system_prompt,omitempty"`
		Description     string                     `json:"description,omitempty"`
		Tags            []string                   `json:"tags,omitempty"`
		AvatarColor     string                     `json:"avatar_color,omitempty"`
		LLMProvider     string                     `json:"llm_provider,omitempty"`
		ReasoningEffort string                     `json:"reasoning_effort,omitempty"`
		MaxOutputTokens int                        `json:"max_output_tokens,omitempty"`
		AllowWebSearch  *bool                      `json:"allow_web_search,omitempty"`
		RoutingProfile  *types.AgentRoutingProfile `json:"routing_profile,omitempty"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	logger.Debug("CreateAgent request", logger.Fields{
		"name": req.Name, "type": req.Type, "model": req.Model, "temperature": req.Temperature,
	})

	if isSystemAssistantAgent(req.Name) {
		orihttp.BadRequest(w, "reserved agent name")
		return
	}

	// Validate agent name
	if err := validateAgentName(req.Name); err != nil {
		logger.Error("CreateAgent error: invalid agent name", logger.Fields{"error": err})
		orihttp.BadRequest(w, err.Error())
		return
	}

	reasoningEffort, err := normalizeAgentReasoningEffort(req.LLMProvider, req.Model, req.ReasoningEffort)
	if err != nil {
		logger.Error("CreateAgent error: invalid reasoning effort", logger.Fields{"error": err})
		orihttp.BadRequest(w, err.Error())
		return
	}

	// Build config from request
	config := &store.CreateAgentConfig{
		Type:            req.Type,
		Role:            types.AgentRole(req.Role),
		Model:           req.Model,
		Temperature:     req.Temperature,
		SystemPrompt:    req.SystemPrompt,
		LLMProvider:     req.LLMProvider,
		ReasoningEffort: reasoningEffort,
		MaxOutputTokens: req.MaxOutputTokens,
		AllowWebSearch:  req.AllowWebSearch,
	}

	logger.Debug("Creating agent", logger.Fields{"agent": req.Name})
	if err := h.State.CreateAgent(req.Name, config); err != nil {
		logger.Error("CreateAgent error", logger.Fields{"error": err})
		orihttp.BadRequest(w, err.Error())
		return
	}

	// Set metadata if provided
	if req.Description != "" || len(req.Tags) > 0 || req.AvatarColor != "" || req.RoutingProfile != nil {
		agent, ok := h.State.GetAgent(req.Name)
		if ok && agent != nil {
			if agent.Metadata == nil {
				agent.Metadata = &types.AgentMetadata{}
			}
			agent.Metadata.Description = req.Description
			agent.Metadata.Tags = req.Tags
			agent.Metadata.AvatarColor = req.AvatarColor
			agent.Metadata.RoutingProfile = cloneRoutingProfile(req.RoutingProfile)
			if err := h.State.SetAgent(req.Name, agent); err != nil {
				logger.Error("Failed to set metadata", logger.Fields{"err": err})
			}
		}
	}

	logger.Info("Agent created successfully", logger.Fields{"agent": req.Name})

	// Log activity
	if h.ActivityLogger != nil {
		details := map[string]any{
			"type":        req.Type,
			"model":       req.Model,
			"description": req.Description,
		}
		if err := h.ActivityLogger.LogActivity(req.Name, types.ActivityEventCreated, details, ""); err != nil {
			logger.Error("Failed to log activity", logger.Fields{"err": err})
		}
	}

	orihttp.Success(w, map[string]any{
		"success": true,
		"message": "Agent '" + req.Name + "' created successfully",
	})
}

// handleUpdate serves PATCH /api/agents/{name}: a partial update of core
// settings and metadata, including rename.
func (h *Handler) handleUpdate(w http.ResponseWriter, r *http.Request) {
	// PATCH /api/agents/:name - Update agent metadata
	agentName := orihttp.RequirePathParamOrQuery(w, r, "/api/agents/", "name")
	if agentName == "" {
		return
	}

	// CLI agents are read-only
	if h.isCLIAgent(agentName) {
		orihttp.BadRequest(w, "CLI agents are built-in and cannot be modified")
		return
	}

	// Get existing agent
	agent, ok := h.State.GetAgent(agentName)
	if !ok || agent == nil {
		orihttp.NotFound(w, "Agent not found")
		return
	}

	// Parse update request
	var req struct {
		// Core fields
		Name            *string  `json:"name,omitempty"`
		Type            *string  `json:"type,omitempty"`
		Role            *string  `json:"role,omitempty"`
		Model           *string  `json:"model,omitempty"`
		Temperature     *float64 `json:"temperature,omitempty"`
		LLMProvider     *string  `json:"llm_provider,omitempty"`
		ReasoningEffort *string  `json:"reasoning_effort,omitempty"`
		MaxTokens       *int     `json:"max_output_tokens,omitempty"`
		SystemPrompt    *string  `json:"system_prompt,omitempty"`
		AllowWebSearch  *bool    `json:"allow_web_search,omitempty"`
		// Metadata
		Description    *string                    `json:"description,omitempty"`
		Tags           *[]string                  `json:"tags,omitempty"`
		AvatarColor    *string                    `json:"avatar_color,omitempty"`
		Favorite       *bool                      `json:"favorite,omitempty"`
		RoutingProfile *types.AgentRoutingProfile `json:"routing_profile,omitempty"`
		// ExpectedVersion is the optimistic-concurrency token the client received
		// from GET /api/agents/{name}. When present and no longer matching the
		// stored agent, the update is rejected as stale (PRD FR13). Omitted =
		// no concurrency check (back-compat for existing callers).
		ExpectedVersion *string `json:"expected_version,omitempty"`
		// ConfirmSharedEdit acknowledges that this edit changes a definition
		// shared by multiple workspaces (PRD FR9). Required when the agent is
		// attached to >1 workspace and a shared-definition field is being
		// changed; the server re-checks membership at write time so API callers
		// cannot bypass the UI warning.
		ConfirmSharedEdit *bool `json:"confirm_shared_edit,omitempty"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	// Stale-edit guard (PRD FR13): if the caller sent the version it loaded and the
	// stored definition has since changed, reject before mutating so a concurrent
	// edit is not silently clobbered. Checked against the current stored agent.
	if req.ExpectedVersion != nil {
		current := agentConfigVersion(agent)
		if *req.ExpectedVersion != current {
			_ = orihttp.RespondJSON(w, http.StatusConflict, map[string]any{
				"error":           "stale_agent_edit",
				"message":         fmt.Sprintf("%q was changed since you loaded it. Reload the latest version and reapply your edit.", agentName),
				"current_version": current,
			})
			return
		}
	}

	// Workspace membership drives the shared-edit / rename guards (PRD FR9/FR10).
	// The system assistant ("Ori") is exempt (PRD FR1). Computed once from the
	// metadata-only cache.
	membership := workspace.WorkspaceMembershipFor(h.workspaceStore, agentName)
	guardsApply := !isSystemAssistantAgent(agentName)

	// Shared-edit confirmation: changing a field that lives on the shared
	// definition affects every attached workspace. When attached to more than
	// one workspace, require explicit confirmation and re-check here so a direct
	// PATCH cannot skip the warning.
	touchesSharedDefinition := req.SystemPrompt != nil || req.Model != nil || req.LLMProvider != nil ||
		req.Type != nil || req.Role != nil || req.Tags != nil || req.AvatarColor != nil ||
		req.RoutingProfile != nil || req.Temperature != nil || req.ReasoningEffort != nil ||
		req.MaxTokens != nil || req.AllowWebSearch != nil
	confirmed := req.ConfirmSharedEdit != nil && *req.ConfirmSharedEdit
	if guardsApply && membership.Count > 1 && touchesSharedDefinition && !confirmed {
		_ = orihttp.RespondJSON(w, http.StatusConflict, map[string]any{
			"error":           "shared_agent_edit_requires_confirmation",
			"message":         fmt.Sprintf("%q is attached to %d workspaces — this change affects all of them. Resend with confirm_shared_edit=true to proceed.", agentName, membership.Count),
			"workspace_count": membership.Count,
			"workspaces":      membership.Workspaces,
		})
		return
	}

	// Initialize metadata if nil
	if agent.Metadata == nil {
		agent.Metadata = &types.AgentMetadata{}
	}

	// Update core fields if provided (partial update)
	if req.Type != nil {
		agent.Type = *req.Type
	}
	if req.Role != nil {
		agent.Role = types.AgentRole(*req.Role)
	}
	if req.Model != nil {
		agent.Settings.Model = *req.Model
	}
	if req.Temperature != nil {
		agent.Settings.Temperature = *req.Temperature
	}
	if req.LLMProvider != nil {
		agent.Settings.Provider = *req.LLMProvider
	}
	if req.ReasoningEffort != nil {
		reasoningEffort, err := normalizeAgentReasoningEffort(agent.Settings.Provider, agent.Settings.Model, *req.ReasoningEffort)
		if err != nil {
			orihttp.BadRequest(w, err.Error())
			return
		}
		agent.Settings.ReasoningEffort = reasoningEffort
	}
	if req.MaxTokens != nil {
		agent.Settings.MaxOutputTokens = *req.MaxTokens
	}
	if req.SystemPrompt != nil {
		agent.Settings.SystemPrompt = *req.SystemPrompt
	}
	if req.AllowWebSearch != nil {
		allow := *req.AllowWebSearch
		agent.Settings.AllowWebSearch = &allow
	}
	if !agentSupportsReasoningEffort(agent.Settings.Provider, agent.Settings.Model) {
		agent.Settings.ReasoningEffort = ""
	}

	// Update metadata fields if provided (partial update)
	if req.Description != nil {
		agent.Metadata.Description = *req.Description
	}
	if req.Tags != nil {
		agent.Metadata.Tags = *req.Tags
	}
	if req.AvatarColor != nil {
		agent.Metadata.AvatarColor = *req.AvatarColor
	}
	if req.Favorite != nil {
		agent.Metadata.Favorite = *req.Favorite
	}
	if req.RoutingProfile != nil {
		agent.Metadata.RoutingProfile = cloneRoutingProfile(req.RoutingProfile)
	}

	newName := agentName

	// Update timestamp if statistics exist
	if agent.Statistics != nil {
		agent.Statistics.UpdatedAt = time.Now()
	}

	// Save updated agent (handle rename if needed)
	if req.Name != nil && *req.Name != "" && *req.Name != agentName {
		if isSystemAssistantAgent(agentName) || isSystemAssistantAgent(*req.Name) {
			orihttp.BadRequest(w, "system assistant cannot be renamed")
			return
		}

		// Agent identity is still the bare name, so renaming an attached
		// definition would strand every workspace that references the old name
		// (PRD FR10). Block it; users create a new definition instead.
		if guardsApply && membership.Count > 0 {
			_ = orihttp.RespondJSON(w, http.StatusConflict, map[string]any{
				"error":           "attached_agent_rename_blocked",
				"message":         fmt.Sprintf("%q is attached to %d workspace(s) and cannot be renamed. Create a new agent and attach it instead.", agentName, membership.Count),
				"workspace_count": membership.Count,
				"workspaces":      membership.Workspaces,
			})
			return
		}

		// Validate the new agent name
		if err := validateAgentName(*req.Name); err != nil {
			orihttp.BadRequest(w, err.Error())
			return
		}

		if _, exists := h.State.GetAgent(*req.Name); exists {
			orihttp.Conflict(w, "Agent with that name already exists")
			return
		}

		if err := h.State.SetAgent(*req.Name, agent); err != nil {
			logger.Error("Failed to save renamed agent", logger.Fields{"agent": *req.Name, "error": err})
			orihttp.RespondErrorWithErr(w, http.StatusInternalServerError, "Failed to update agent", err)
			return
		}
		if err := h.State.DeleteAgent(agentName); err != nil {
			logger.Error("Failed to delete old agent record after rename", logger.Fields{"name": agentName, "err": err})
		}
		newName = *req.Name
	} else {
		if err := h.State.SetAgent(agentName, agent); err != nil {
			logger.Error("Failed to update agent metadata", logger.Fields{"agent": agentName, "error": err})
			orihttp.RespondErrorWithErr(w, http.StatusInternalServerError, "Failed to update agent", err)
			return
		}
	}

	logger.Info("Agent metadata updated", logger.Fields{"agent": newName})

	// Log activity
	if h.ActivityLogger != nil {
		updatedFields := []string{}
		if req.Name != nil {
			updatedFields = append(updatedFields, "name")
		}
		if req.Type != nil {
			updatedFields = append(updatedFields, "type")
		}
		if req.Role != nil {
			updatedFields = append(updatedFields, "role")
		}
		if req.Model != nil {
			updatedFields = append(updatedFields, "model")
		}
		if req.Temperature != nil {
			updatedFields = append(updatedFields, "temperature")
		}
		if req.LLMProvider != nil {
			updatedFields = append(updatedFields, "llm_provider")
		}
		if req.MaxTokens != nil {
			updatedFields = append(updatedFields, "max_output_tokens")
		}
		if req.SystemPrompt != nil {
			updatedFields = append(updatedFields, "system_prompt")
		}
		if req.Description != nil {
			updatedFields = append(updatedFields, "description")
		}
		if req.Tags != nil {
			updatedFields = append(updatedFields, "tags")
		}
		if req.AvatarColor != nil {
			updatedFields = append(updatedFields, "avatar_color")
		}
		if req.Favorite != nil {
			updatedFields = append(updatedFields, "favorite")
		}

		details := map[string]any{
			"fields": updatedFields,
		}
		if err := h.ActivityLogger.LogActivity(newName, types.ActivityEventUpdated, details, ""); err != nil {
			logger.Error("Failed to log activity", logger.Fields{"err": err})
		}
	}

	orihttp.Success(w, map[string]any{
		"success": true,
		"name":    newName,
		"message": "Agent updated successfully",
	})
}

// handleDelete serves DELETE /api/agents?name=...: removes the agent and
// purges sessions that still reference it.
func (h *Handler) handleDelete(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		orihttp.BadRequest(w, "name required")
		return
	}
	if isSystemAssistantAgent(name) {
		orihttp.BadRequest(w, "system assistant cannot be deleted")
		return
	}
	if h.isCLIAgent(name) {
		orihttp.BadRequest(w, "CLI agents are built-in and cannot be deleted")
		return
	}

	// Deleting a definition still attached to a workspace would leave a stale
	// reference (and a restored snapshot pointing at a missing definition).
	// Only unattached/library definitions may be deleted (PRD FR11).
	if membership := workspace.WorkspaceMembershipFor(h.workspaceStore, name); membership.Count > 0 {
		_ = orihttp.RespondJSON(w, http.StatusConflict, map[string]any{
			"error":           "attached_agent_delete_blocked",
			"message":         fmt.Sprintf("%q is attached to %d workspace(s) and cannot be deleted. Detach it from every workspace first.", name, membership.Count),
			"workspace_count": membership.Count,
			"workspaces":      membership.Workspaces,
		})
		return
	}

	if err := h.State.DeleteAgent(name); err != nil {
		orihttp.BadRequest(w, err.Error())
		return
	}

	// Remove any sessions still referencing this agent so the UI cannot
	// restore stale state that resolves to a 404 on /api/agents.
	if h.sessionPurger != nil {
		if n, err := h.sessionPurger.DeleteSessionsByAgent(r.Context(), name); err != nil {
			logger.Error("Failed to purge sessions for deleted agent", logger.Fields{"agent": name, "err": err})
		} else if n > 0 {
			logger.Info("Purged sessions for deleted agent", logger.Fields{"agent": name, "count": n})
		}
	}

	// Log activity
	if h.ActivityLogger != nil {
		details := map[string]any{}
		if err := h.ActivityLogger.LogActivity(name, types.ActivityEventDeleted, details, ""); err != nil {
			logger.Error("Failed to log activity", logger.Fields{"err": err})
		}
	}

	w.WriteHeader(http.StatusOK)
}

func normalizeAgentReasoningEffort(providerName, modelName, reasoningEffort string) (string, error) {
	if strings.TrimSpace(reasoningEffort) == "" {
		return "", nil
	}

	normalized := types.NormalizeReasoningEffort(reasoningEffort)
	if normalized == "" {
		return "", fmt.Errorf("invalid reasoning_effort %q: must be one of [low medium high xhigh]", reasoningEffort)
	}

	if !agentSupportsReasoningEffort(providerName, modelName) {
		return "", nil
	}

	return normalized, nil
}

func agentSupportsReasoningEffort(providerName, modelName string) bool {
	if strings.EqualFold(strings.TrimSpace(providerName), "codex") {
		return true
	}
	return strings.Contains(strings.ToLower(strings.TrimSpace(modelName)), "codex")
}

// cliAgentDisplayName returns a human-friendly name for a CLI backend.
func cliAgentDisplayName(backend string) string {
	switch backend {
	case cliagent.BackendClaude:
		return "Claude Code"
	case cliagent.BackendCodex:
		return "Codex"
	case cliagent.BackendGemini:
		return "Gemini CLI"
	default:
		return backend
	}
}

// cliAgentBackendFromName resolves a display name or backend name to a backend key.
func cliAgentBackendFromName(name string) string {
	lower := strings.ToLower(strings.TrimSpace(name))
	switch lower {
	case "claude code", cliagent.BackendClaude:
		return cliagent.BackendClaude
	case cliagent.BackendCodex:
		return cliagent.BackendCodex
	case "gemini cli", cliagent.BackendGemini:
		return cliagent.BackendGemini
	default:
		return ""
	}
}

// isCLIAgent checks whether the given name refers to a built-in CLI agent.
func (h *Handler) isCLIAgent(name string) bool {
	if h.cliAgentRegistry == nil {
		return false
	}
	backend := cliAgentBackendFromName(name)
	return backend != "" && h.cliAgentRegistry.IsAvailable(backend)
}

// getCLIAgentDetail returns agent detail for a CLI agent, or false if not found.
func (h *Handler) getCLIAgentDetail(name string) (map[string]any, bool) {
	if h.cliAgentRegistry == nil {
		return nil, false
	}
	backend := cliAgentBackendFromName(name)
	if backend == "" {
		return nil, false
	}
	adapter, err := h.cliAgentRegistry.Get(backend)
	if err != nil || !adapter.IsAvailable() {
		return nil, false
	}
	caps := adapter.Capabilities()
	models := adapter.AvailableModels()
	defaultModel := ""
	if len(models) > 0 {
		defaultModel = models[0]
	}
	detail := map[string]any{
		"name":               cliAgentDisplayName(backend),
		"type":               "research",
		"role":               types.RoleCLIAgent,
		"capabilities":       []string{"file_operations", "code_generation", "code_analysis"},
		"model":              defaultModel,
		"available_models":   models,
		"temperature":        0.0,
		"provider":           backend,
		"status":             getCLIAgentOperationalStatus(backend),
		"max_context_window": caps.MaxContextWindow,
		"source":             "cli",
	}

	// Attach read-only synced ~/.claude state for the Claude Code agent.
	if backend == cliagent.BackendClaude && h.claudeSync != nil {
		if data := h.claudeSync(); data != nil {
			detail["claude_sync"] = data
		}
	}
	// Attach read-only synced ~/.codex state for the Codex CLI agent.
	if backend == cliagent.BackendCodex && h.codexSync != nil {
		if data := h.codexSync(); data != nil {
			detail["codex_sync"] = data
		}
	}

	return detail, true
}
