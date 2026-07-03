package agenthttp

import (
	"net/http"
	"net/url"
	"sort"
	"strconv"
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

// DashboardHandler handles dashboard-specific API endpoints
type DashboardHandler struct {
	State            store.Store
	ActivityLogger   *ActivityLogger
	cliAgentRegistry *cliagent.CLIAgentRegistry
	workspaceStore   workspace.Store
	// claudeSync returns read-only synced ~/.claude data for the Claude Code
	// agent (or nil when disabled). Injected to keep agenthttp decoupled from
	// the externalagents package.
	claudeSync func() any
	// codexSync returns read-only synced ~/.codex data for the Codex agent (or
	// nil when disabled). Injected to keep agenthttp decoupled from the
	// externalagents package.
	codexSync func() any
}

// NewDashboardHandler creates a new dashboard handler
func NewDashboardHandler(state store.Store) *DashboardHandler {
	return &DashboardHandler{State: state}
}

// SetCLIAgentRegistry wires the CLI agent registry so auto-detected
// CLI agents appear in the dashboard agent list.
func (h *DashboardHandler) SetCLIAgentRegistry(r *cliagent.CLIAgentRegistry) {
	h.cliAgentRegistry = r
}

// SetWorkspaceStore wires the workspace store so dashboard can filter out
// workspace entry agents from the top-level agents list.
func (h *DashboardHandler) SetWorkspaceStore(s workspace.Store) {
	h.workspaceStore = s
}

// SetClaudeSyncProvider wires a provider of read-only ~/.claude data, attached
// to the Claude Code agent's detail response when available.
func (h *DashboardHandler) SetClaudeSyncProvider(provider func() any) {
	h.claudeSync = provider
}

// SetCodexSyncProvider wires a provider of read-only ~/.codex data, attached
// to the Codex agent's detail response when available.
func (h *DashboardHandler) SetCodexSyncProvider(provider func() any) {
	h.codexSync = provider
}

// AgentListItem represents an agent in the dashboard list view
type AgentListItem struct {
	Name           string                 `json:"name"`
	Type           string                 `json:"type"`
	Role           types.AgentRole        `json:"role"`
	Source         string                 `json:"source"`
	Scope          string                 `json:"scope,omitempty"`
	WorkspaceID    string                 `json:"workspace_id,omitempty"`
	Capabilities   []string               `json:"capabilities,omitempty"`
	Status         types.AgentStatus      `json:"status"`
	Statistics     *types.AgentStatistics `json:"statistics,omitempty"`
	Metadata       *types.AgentMetadata   `json:"metadata,omitempty"`
	Evolution      *types.AgentEvolution  `json:"evolution,omitempty"`
	AllowWebSearch bool                   `json:"allow_web_search"`
	Model          string                 `json:"model"`
}

// AgentDetailResponse represents detailed agent information
type AgentDetailResponse struct {
	Name            string                 `json:"name"`
	Type            string                 `json:"type"`
	Role            types.AgentRole        `json:"role"`
	Capabilities    []string               `json:"capabilities"`
	Status          types.AgentStatus      `json:"status"`
	Statistics      *types.AgentStatistics `json:"statistics,omitempty"`
	Metadata        *types.AgentMetadata   `json:"metadata,omitempty"`
	Evolution       *types.AgentEvolution  `json:"evolution,omitempty"`
	Model           string                 `json:"model"`
	Temperature     float64                `json:"temperature"`
	Provider        string                 `json:"provider,omitempty"`
	ReasoningEffort string                 `json:"reasoning_effort,omitempty"`
	MaxOutputTokens int                    `json:"max_output_tokens,omitempty"`
	SystemPrompt    string                 `json:"system_prompt"`
	AllowWebSearch  bool                   `json:"allow_web_search"`
	// ClaudeSync carries read-only ~/.claude state for the Claude Code agent.
	ClaudeSync any `json:"claude_sync,omitempty"`
	// CodexSync carries read-only ~/.codex state for the Codex CLI agent.
	CodexSync any `json:"codex_sync,omitempty"`
}

// ListAgentsWithStats handles GET /api/agents/dashboard/list
// Returns list of agents with their statistics for the dashboard
func (h *DashboardHandler) ListAgentsWithStats(w http.ResponseWriter, r *http.Request) {
	// Get query parameters for filtering and sorting
	sortBy := r.URL.Query().Get("sort_by") // name, created_at, last_active
	order := r.URL.Query().Get("order")    // asc, desc
	statusFilter := r.URL.Query().Get("status")
	tagFilter := r.URL.Query().Get("tag")
	favoriteOnly := r.URL.Query().Get("favorite") == "true"

	// Map of workspace entry agent name (lowercase) → workspace ID, so each
	// agent can be annotated with scope="workspace" and its workspace link.
	entryAgentWorkspaces := collectWorkspaceEntryAgentNames(h.workspaceStore)

	// Get all agents
	names := h.State.ListAgents()
	agents := make([]AgentListItem, 0, len(names))

	for _, name := range names {
		ag, ok := h.State.GetAgent(name)
		if !ok || ag == nil {
			continue
		}

		// Set default status for agents that don't have one (migration for existing agents)
		if ag.Status == "" {
			ag.Status = types.AgentStatusIdle
		}

		// Initialize statistics for agents that don't have them (migration for existing agents)
		if ag.Statistics == nil {
			ag.InitializeStatistics()
		}

		// Apply status filter
		if statusFilter != "" && string(ag.Status) != statusFilter {
			continue
		}

		// Apply favorite filter
		if favoriteOnly && (ag.Metadata == nil || !ag.Metadata.Favorite) {
			continue
		}

		// Apply tag filter
		if tagFilter != "" {
			if ag.Metadata == nil || !containsTag(ag.Metadata.Tags, tagFilter) {
				continue
			}
		}

		item := AgentListItem{
			Name:           name,
			Type:           ag.Type,
			Role:           ag.Role,
			Source:         "user",
			Capabilities:   append([]string{}, ag.Capabilities...),
			Status:         ag.Status,
			Statistics:     ag.Statistics,
			Metadata:       ag.Metadata,
			Evolution:      cloneAgentEvolution(ag),
			AllowWebSearch: ag.Settings.IsWebSearchAllowed(),
			Model:          ag.Settings.Model,
		}

		// Annotate workspace entry agents so the UI can group / hide them.
		if wsID, isEntry := entryAgentWorkspaces[strings.ToLower(strings.TrimSpace(name))]; isEntry {
			item.Scope = "workspace"
			item.WorkspaceID = wsID
		}

		agents = append(agents, item)
	}

	// Append auto-detected CLI agents
	if h.cliAgentRegistry != nil {
		for _, info := range h.cliAgentRegistry.List() {
			if !info.Available {
				continue
			}
			defaultModel := ""
			if len(info.Models) > 0 {
				defaultModel = info.Models[0]
			}
			status := getCLIAgentOperationalStatus(info.Backend)
			if statusFilter != "" && string(status) != statusFilter {
				continue
			}
			agents = append(agents, AgentListItem{
				Name:         cliAgentDisplayName(info.Backend),
				Type:         "research",
				Role:         types.RoleCLIAgent,
				Source:       "cli",
				Capabilities: []string{"file_operations", "code_generation", "code_analysis"},
				Status:       status,
				Model:        defaultModel,
			})
		}
	}

	// Sort agents
	sortAgents(agents, sortBy, order)

	// Return JSON response
	w.Header().Set("Content-Type", "application/json")
	orihttp.WriteJSON(w, map[string]any{
		"agents": agents,
		"total":  len(agents),
	})
}

// GetAgentDetail handles GET /api/agents/:id/detail
// Returns detailed information for a specific agent
func (h *DashboardHandler) GetAgentDetail(w http.ResponseWriter, r *http.Request) {
	// Extract agent name from URL path
	path := r.URL.Path
	var agentName string

	// Try multiple patterns
	if strings.HasPrefix(path, "/api/agents/") {
		parts := strings.Split(strings.TrimPrefix(path, "/api/agents/"), "/")
		if len(parts) > 0 {
			agentName = parts[0]
		}
	}
	if decodedName, err := url.PathUnescape(agentName); err == nil {
		agentName = decodedName
	}

	// Also check query parameter as fallback
	if agentName == "" {
		agentName = r.URL.Query().Get("name")
	}

	if agentName == "" {
		orihttp.BadRequest(w, "Agent name is required")
		// Get agent
		return
	}

	ag, ok := h.State.GetAgent(agentName)
	if !ok || ag == nil {
		// Check if it's a CLI agent
		if h.cliAgentRegistry != nil {
			backend := cliAgentBackendFromName(agentName)
			if backend != "" {
				adapter, err := h.cliAgentRegistry.Get(backend)
				if err == nil && adapter.IsAvailable() {
					caps := adapter.Capabilities()
					models := adapter.AvailableModels()
					defaultModel := ""
					if len(models) > 0 {
						defaultModel = models[0]
					}
					response := AgentDetailResponse{
						Name:         cliAgentDisplayName(backend),
						Type:         "research",
						Role:         types.RoleCLIAgent,
						Capabilities: []string{"file_operations", "code_generation", "code_analysis"},
						Status:       getCLIAgentOperationalStatus(backend),
						Model:        defaultModel,
						Provider:     backend,
					}
					// Attach read-only synced ~/.claude state for the Claude Code agent.
					if backend == cliagent.BackendClaude && h.claudeSync != nil {
						response.ClaudeSync = h.claudeSync()
					}
					// Attach read-only synced ~/.codex state for the Codex CLI agent.
					if backend == cliagent.BackendCodex && h.codexSync != nil {
						response.CodexSync = h.codexSync()
					}
					_ = caps // context window info available via /api/cli-agents
					w.Header().Set("Content-Type", "application/json")
					orihttp.WriteJSON(w, response)
					return
				}
			}
		}
		orihttp.NotFound(w, "Agent not found")
		return
	}

	// Set default status for agents that don't have one (migration for existing agents)
	if ag.Status == "" {
		ag.Status = types.AgentStatusIdle
	}

	// Initialize statistics for agents that don't have them (migration for existing agents)
	if ag.Statistics == nil {
		ag.InitializeStatistics()
	}

	// Build response
	response := AgentDetailResponse{
		Name:            agentName,
		Type:            ag.Type,
		Role:            ag.Role,
		Capabilities:    ag.Capabilities,
		Status:          ag.Status,
		Statistics:      ag.Statistics,
		Metadata:        ag.Metadata,
		Evolution:       cloneAgentEvolution(ag),
		Model:           ag.Settings.Model,
		Temperature:     ag.Settings.Temperature,
		Provider:        ag.Settings.Provider,
		ReasoningEffort: ag.Settings.EffectiveReasoningEffort(ag.Settings.Provider),
		MaxOutputTokens: ag.Settings.MaxOutputTokens,
		SystemPrompt:    ag.Settings.SystemPrompt,
		AllowWebSearch:  ag.Settings.IsWebSearchAllowed(),
	}

	// Return JSON response
	w.Header().Set("Content-Type", "application/json")
	orihttp.WriteJSON(w, response)
}

// collectWorkspaceEntryAgentNames returns a map (lowercase agent name →
// workspace ID) for every workspace that designates an entry agent. Returns
// an empty map when the workspace store is nil or unreachable. Shared by the
// dashboard and main agent list handlers so both can annotate workspace-scoped
// entry agents consistently.
func collectWorkspaceEntryAgentNames(wsStore workspace.Store) map[string]string {
	names := make(map[string]string)
	if wsStore == nil {
		return names
	}

	ids, err := wsStore.List()
	if err != nil {
		return names
	}

	for _, id := range ids {
		ws, err := wsStore.Get(id)
		if err != nil || ws == nil {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(ws.EntryAgentName()))
		if name != "" {
			names[name] = ws.ID
		}
	}
	return names
}

// Helper functions

func containsTag(tags []string, tag string) bool {
	for _, t := range tags {
		if strings.EqualFold(t, tag) {
			return true
		}
	}
	return false
}

func sortAgents(agents []AgentListItem, sortBy, order string) {
	// Default to ascending order
	ascending := order != "desc"

	switch sortBy {
	case "name":
		sort.Slice(agents, func(i, j int) bool {
			if ascending {
				return strings.ToLower(agents[i].Name) < strings.ToLower(agents[j].Name)
			}
			return strings.ToLower(agents[i].Name) > strings.ToLower(agents[j].Name)
		})

	case "created_at":
		sort.Slice(agents, func(i, j int) bool {
			iTime := getCreatedAt(agents[i].Statistics)
			jTime := getCreatedAt(agents[j].Statistics)
			if ascending {
				return iTime.Before(jTime)
			}
			return iTime.After(jTime)
		})

	case "last_active":
		sort.Slice(agents, func(i, j int) bool {
			iTime := getLastActive(agents[i].Statistics)
			jTime := getLastActive(agents[j].Statistics)
			if ascending {
				return iTime.Before(jTime)
			}
			return iTime.After(jTime)
		})

	default:
		// Default sort by name
		sort.Slice(agents, func(i, j int) bool {
			return strings.ToLower(agents[i].Name) < strings.ToLower(agents[j].Name)
		})
	}
}

func getCreatedAt(stats *types.AgentStatistics) time.Time {
	if stats != nil && !stats.CreatedAt.IsZero() {
		return stats.CreatedAt
	}
	return time.Time{} // Return zero time
}

func getLastActive(stats *types.AgentStatistics) time.Time {
	if stats != nil && !stats.LastActive.IsZero() {
		return stats.LastActive
	}
	return time.Time{} // Return zero time
}

// GetDashboardStats handles GET /api/agents/dashboard/stats
// Returns aggregate statistics across all agents
func (h *DashboardHandler) GetDashboardStats(w http.ResponseWriter, r *http.Request) {
	// Get all agents
	names := h.State.ListAgents()
	agentsMap := make(map[string]*agent.Agent)

	for _, name := range names {
		ag, ok := h.State.GetAgent(name)
		if ok && ag != nil {
			agentsMap[name] = ag
		}
	}

	// Compute aggregate statistics
	stats := ComputeOverallStatistics(agentsMap)

	// Return JSON response
	w.Header().Set("Content-Type", "application/json")
	orihttp.WriteJSON(w, stats)
}

// UpdateAgentStatus handles POST /api/agents/:id/status
// Updates the operational status of an agent
func (h *DashboardHandler) UpdateAgentStatus(w http.ResponseWriter, r *http.Request) {
	// Extract agent name from URL path
	path := r.URL.Path
	var agentName string

	if strings.HasPrefix(path, "/api/agents/") {
		parts := strings.Split(strings.TrimPrefix(path, "/api/agents/"), "/")
		if len(parts) > 0 {
			agentName = parts[0]
		}
	}
	if decodedName, err := url.PathUnescape(agentName); err == nil {
		agentName = decodedName
	}

	if agentName == "" {
		orihttp.BadRequest(w, "Agent name is required")
		// Parse request
		return
	}

	var req struct {
		Status string `json:"status"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	// Validate status
	validStatuses := map[string]bool{
		string(types.AgentStatusActive):   true,
		string(types.AgentStatusIdle):     true,
		string(types.AgentStatusError):    true,
		string(types.AgentStatusDisabled): true,
	}
	if !validStatuses[req.Status] {
		orihttp.BadRequest(w, "Invalid status. Must be one of: active, idle, error, disabled")
		// Get agent
		return
	}

	// The system assistant must always remain available for routing and chat.
	if isSystemAssistantAgent(agentName) && req.Status == string(types.AgentStatusDisabled) {
		orihttp.BadRequest(w, "system assistant cannot be disabled")
		return
	}

	agent, ok := h.State.GetAgent(agentName)
	if !ok || agent == nil {
		if h.cliAgentRegistry != nil {
			backend := cliAgentBackendFromName(agentName)
			if backend != "" && h.cliAgentRegistry.IsAvailable(backend) {
				oldStatus := string(getCLIAgentOperationalStatus(backend))
				setCLIAgentOperationalStatus(backend, types.AgentStatus(req.Status))

				if h.ActivityLogger != nil {
					details := map[string]any{
						"old_status": oldStatus,
						"new_status": req.Status,
						"source":     "cli",
					}
					if err := h.ActivityLogger.LogActivity(cliAgentDisplayName(backend), types.ActivityEventStatusChanged, details, ""); err != nil {
						logger.Error("Failed to log CLI status change activity", logger.Fields{"error": err})
					}
				}

				w.Header().Set("Content-Type", "application/json")
				orihttp.WriteJSON(w, map[string]any{
					"success": true,
					"message": "Agent status updated successfully",
					"status":  req.Status,
				})
				return
			}
		}
		orihttp.NotFound(w, "Agent not found")
		// Store old status for logging
		return
	}

	oldStatus := string(agent.Status)

	// Update status
	agent.Status = types.AgentStatus(req.Status)

	// Update timestamp if statistics exist
	if agent.Statistics != nil {
		agent.Statistics.UpdatedAt = time.Now()
	}

	// Save agent
	if err := h.State.SetAgent(agentName, agent); err != nil {
		orihttp.RespondErrorWithErr(w, http.StatusInternalServerError, "Failed to update agent status", err)
		return
	}

	// Log activity
	if h.ActivityLogger != nil {
		details := map[string]any{
			"old_status": oldStatus,
			"new_status": req.Status,
		}
		if err := h.ActivityLogger.LogActivity(agentName, types.ActivityEventStatusChanged, details, ""); err != nil {
			// Log error but don't fail the request
			logger.Error("Failed to log status change activity", logger.Fields{"error": err})
		}
	}

	// Return success
	w.Header().Set("Content-Type", "application/json")
	orihttp.WriteJSON(w, map[string]any{
		"success": true,
		"message": "Agent status updated successfully",
		"status":  req.Status,
	})
}

// GetAgentActivity handles GET /api/agents/:name/activity
// Returns activity log for a specific agent with pagination and filtering
func (h *DashboardHandler) GetAgentActivity(w http.ResponseWriter, r *http.Request) {
	if h.ActivityLogger == nil {
		orihttp.ServiceUnavailable(w, "Activity logging not enabled")
		// Extract agent name from URL path
		return
	}

	path := r.URL.Path
	var agentName string

	if strings.HasPrefix(path, "/api/agents/") {
		parts := strings.Split(strings.TrimPrefix(path, "/api/agents/"), "/")
		if len(parts) > 0 {
			agentName = parts[0]
		}
	}

	if agentName == "" {
		orihttp.BadRequest(w, "Agent name is required")
		// Parse query parameters
		return
	}

	query := r.URL.Query()
	limit := 50 // Default limit
	offset := 0

	if limitStr := query.Get("limit"); limitStr != "" {
		if n, err := strconv.Atoi(limitStr); err == nil && n > 0 {
			limit = n
		}
	}

	if offsetStr := query.Get("offset"); offsetStr != "" {
		if n, err := strconv.Atoi(offsetStr); err == nil && n >= 0 {
			offset = n
		}
	}

	// Parse event type filter
	var eventType types.ActivityEventType
	if eventTypeStr := query.Get("event_type"); eventTypeStr != "" {
		eventType = types.ActivityEventType(eventTypeStr)
	}

	// Parse date range filters
	var startDate, endDate time.Time
	if startStr := query.Get("start_date"); startStr != "" {
		if t, err := time.Parse(time.RFC3339, startStr); err == nil {
			startDate = t
		}
	}

	if endStr := query.Get("end_date"); endStr != "" {
		if t, err := time.Parse(time.RFC3339, endStr); err == nil {
			endDate = t
		}
	}

	// Get activity logs
	logs, total, err := h.ActivityLogger.GetActivityLog(agentName, limit, offset, eventType, startDate, endDate)
	if err != nil {
		orihttp.InternalError(w, "Failed to retrieve activity log: "+err.Error())
		return
	}

	// Format logs for UI rendering
	formattedLogs := make([]types.ActivityLogEntry, len(logs))
	for i, log := range logs {
		formattedLogs[i] = FormatLogEntry(log)
	}

	// Return response
	w.Header().Set("Content-Type", "application/json")
	orihttp.WriteJSON(w, map[string]any{
		"logs":   formattedLogs,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}
