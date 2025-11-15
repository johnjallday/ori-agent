package agenthttp

import (
	"encoding/json"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
)

// DashboardHandler handles dashboard-specific API endpoints
type DashboardHandler struct {
	State          store.Store
	ActivityLogger *ActivityLogger
}

// NewDashboardHandler creates a new dashboard handler
func NewDashboardHandler(state store.Store) *DashboardHandler {
	return &DashboardHandler{State: state}
}

// AgentListItem represents an agent in the dashboard list view
type AgentListItem struct {
	Name           string                 `json:"name"`
	Type           string                 `json:"type"`
	Role           types.AgentRole        `json:"role"`
	Status         types.AgentStatus      `json:"status"`
	Statistics     *types.AgentStatistics `json:"statistics,omitempty"`
	Metadata       *types.AgentMetadata   `json:"metadata,omitempty"`
	EnabledPlugins []string               `json:"enabled_plugins"`
	Model          string                 `json:"model"`
}

// AgentDetailResponse represents detailed agent information
type AgentDetailResponse struct {
	Name           string                 `json:"name"`
	Type           string                 `json:"type"`
	Role           types.AgentRole        `json:"role"`
	Capabilities   []string               `json:"capabilities"`
	Status         types.AgentStatus      `json:"status"`
	Statistics     *types.AgentStatistics `json:"statistics,omitempty"`
	Metadata       *types.AgentMetadata   `json:"metadata,omitempty"`
	Model          string                 `json:"model"`
	Temperature    float64                `json:"temperature"`
	SystemPrompt   string                 `json:"system_prompt"`
	EnabledPlugins []PluginInfo           `json:"enabled_plugins"`
	MCPServers     []string               `json:"mcp_servers,omitempty"`
}

// PluginInfo represents plugin information in the detail view
type PluginInfo struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
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

	// Get all agents
	names, _ := h.State.ListAgents()
	agents := make([]AgentListItem, 0, len(names))

	for _, name := range names {
		ag, ok := h.State.GetAgent(name)
		if !ok || ag == nil {
			continue
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

		// Get enabled plugins
		enabledPlugins := make([]string, 0, len(ag.Plugins))
		for pluginName := range ag.Plugins {
			enabledPlugins = append(enabledPlugins, pluginName)
		}

		agents = append(agents, AgentListItem{
			Name:           name,
			Type:           ag.Type,
			Role:           ag.Role,
			Status:         ag.Status,
			Statistics:     ag.Statistics,
			Metadata:       ag.Metadata,
			EnabledPlugins: enabledPlugins,
			Model:          ag.Settings.Model,
		})
	}

	// Sort agents
	sortAgents(agents, sortBy, order)

	// Return JSON response
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
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

	// Also check query parameter as fallback
	if agentName == "" {
		agentName = r.URL.Query().Get("name")
	}

	if agentName == "" {
		http.Error(w, "Agent name is required", http.StatusBadRequest)
		return
	}

	// Get agent
	ag, ok := h.State.GetAgent(agentName)
	if !ok || ag == nil {
		http.Error(w, "Agent not found", http.StatusNotFound)
		return
	}

	// Build plugin info list
	pluginInfos := make([]PluginInfo, 0, len(ag.Plugins))
	for pluginName, plugin := range ag.Plugins {
		pluginInfos = append(pluginInfos, PluginInfo{
			Name:    pluginName,
			Version: plugin.Version,
		})
	}

	// Build response
	response := AgentDetailResponse{
		Name:           agentName,
		Type:           ag.Type,
		Role:           ag.Role,
		Capabilities:   ag.Capabilities,
		Status:         ag.Status,
		Statistics:     ag.Statistics,
		Metadata:       ag.Metadata,
		Model:          ag.Settings.Model,
		Temperature:    ag.Settings.Temperature,
		SystemPrompt:   ag.Settings.SystemPrompt,
		EnabledPlugins: pluginInfos,
		MCPServers:     ag.MCPServers,
	}

	// Return JSON response
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
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
	names, _ := h.State.ListAgents()
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
	_ = json.NewEncoder(w).Encode(stats)
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

	if agentName == "" {
		http.Error(w, "Agent name is required", http.StatusBadRequest)
		return
	}

	// Parse request
	var req struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body: "+err.Error(), http.StatusBadRequest)
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
		http.Error(w, "Invalid status. Must be one of: active, idle, error, disabled", http.StatusBadRequest)
		return
	}

	// Get agent
	agent, ok := h.State.GetAgent(agentName)
	if !ok || agent == nil {
		http.Error(w, "Agent not found", http.StatusNotFound)
		return
	}

	// Store old status for logging
	oldStatus := string(agent.Status)

	// Update status
	agent.Status = types.AgentStatus(req.Status)

	// Update timestamp if statistics exist
	if agent.Statistics != nil {
		agent.Statistics.UpdatedAt = time.Now()
	}

	// Save agent
	if err := h.State.SetAgent(agentName, agent); err != nil {
		http.Error(w, "Failed to update agent status: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Log activity
	if h.ActivityLogger != nil {
		details := map[string]interface{}{
			"old_status": oldStatus,
			"new_status": req.Status,
		}
		if err := h.ActivityLogger.LogActivity(agentName, types.ActivityEventStatusChanged, details, ""); err != nil {
			// Log error but don't fail the request
			log.Printf("⚠️  Failed to log status change activity: %v", err)
		}
	}

	// Return success
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"success": true,
		"message": "Agent status updated successfully",
		"status":  req.Status,
	})
}

// GetAgentActivity handles GET /api/agents/:name/activity
// Returns activity log for a specific agent with pagination and filtering
func (h *DashboardHandler) GetAgentActivity(w http.ResponseWriter, r *http.Request) {
	if h.ActivityLogger == nil {
		http.Error(w, "Activity logging not enabled", http.StatusServiceUnavailable)
		return
	}

	// Extract agent name from URL path
	path := r.URL.Path
	var agentName string

	if strings.HasPrefix(path, "/api/agents/") {
		parts := strings.Split(strings.TrimPrefix(path, "/api/agents/"), "/")
		if len(parts) > 0 {
			agentName = parts[0]
		}
	}

	if agentName == "" {
		http.Error(w, "Agent name is required", http.StatusBadRequest)
		return
	}

	// Parse query parameters
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
		http.Error(w, "Failed to retrieve activity log: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Format logs for UI rendering
	formattedLogs := make([]types.ActivityLogEntry, len(logs))
	for i, log := range logs {
		formattedLogs[i] = FormatLogEntry(log)
	}

	// Return response
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"logs":   formattedLogs,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}
