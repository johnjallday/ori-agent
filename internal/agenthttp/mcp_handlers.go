package agenthttp

import (
	"encoding/json"
	"fmt"

	"net/http"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/mcp"
)

// MCPHandler handles MCP-related requests for agents
type MCPHandler struct {
	registry      *mcp.Registry
	configManager *mcp.ConfigManager
	agentHandler  *Handler
}

type updateServerConfigRequest struct {
	Path string `json:"path"`
}

// NewMCPHandler creates a new MCP handler for agents
func NewMCPHandler(registry *mcp.Registry, configManager *mcp.ConfigManager, agentHandler *Handler) *MCPHandler {
	return &MCPHandler{
		registry:      registry,
		configManager: configManager,
		agentHandler:  agentHandler,
	}
}

// ListAgentMCPServersHandler lists all available MCP servers and their status for a specific agent
// GET /api/agents/{name}/mcp-servers
func (h *MCPHandler) ListAgentMCPServersHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		orihttp.MethodNotAllowed(w)
		// Extract agent name from path: /api/agents/{name}/mcp-servers
		return
	}

	// api/agents/{name}/mcp-servers
	path := r.URL.Path
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 4 {
		orihttp.BadRequest(w, "Agent name required in path")
		return
	}
	agentName := parts[2]

	// Verify agent exists
	_, ok := h.agentHandler.State.GetAgent(agentName)
	if !ok {
		orihttp.NotFound(w, "Agent not found")
		return
	}

	// Get all globally configured servers
	globalServers := h.registry.ListServers()
	stats := h.registry.GetServerStats()

	// Get enabled servers for this agent
	enabledServers, err := h.configManager.GetEnabledServersForAgent(agentName)
	if err != nil {
		logger.Error("Failed to get enabled servers for agent", logger.Fields{"agent": agentName, "err": err})
		enabledServers = []mcp.ServerConfig{} // Default to empty if error
	}

	// Build enabled set for quick lookup
	enabledSet := make(map[string]bool)
	for _, server := range enabledServers {
		enabledSet[server.Name] = true
	}

	// Build response with server details
	type ServerInfo struct {
		Name        string `json:"name"`
		Description string `json:"description,omitempty"`
		Status      string `json:"status"`
		ToolCount   int    `json:"tool_count"`
		Enabled     bool   `json:"enabled"` // Enabled for this agent
	}

	servers := make([]ServerInfo, 0, len(globalServers))
	for _, server := range globalServers {
		stat, hasStats := stats[server.Name]
		toolCount := 0
		status := "stopped"

		if hasStats {
			toolCount = stat.ToolCount
			status = string(stat.Status)
		}

		servers = append(servers, ServerInfo{
			Name:        server.Name,
			Description: getServerDescription(server.Name),
			Status:      status,
			ToolCount:   toolCount,
			Enabled:     enabledSet[server.Name],
		})
	}

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]interface{}{
		"agent":   agentName,
		"servers": servers,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// EnableAgentMCPServerHandler enables an MCP server for a specific agent
// POST /api/agents/{name}/mcp-servers/{serverName}/enable
func (h *MCPHandler) EnableAgentMCPServerHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		// Extract agent name and server name from path
		return
	}

	path := r.URL.Path
	parts := strings.Split(strings.Trim(path, "/"), "/")
	// api/agents/{name}/mcp-servers/{serverName}/enable
	if len(parts) < 5 {
		orihttp.BadRequest(w, "Agent name and server name required in path")
		return
	}
	agentName := parts[2]
	serverName := parts[4]

	// Verify agent exists
	_, ok := h.agentHandler.State.GetAgent(agentName)
	if !ok {
		orihttp.NotFound(w, "Agent not found")
		return
	}

	_, err := h.registry.GetServer(serverName)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("MCP server '%s' not found in global registry", serverName))
		// Enable server for agent
		return
	}

	if err := h.configManager.EnableServerForAgent(agentName, serverName); err != nil {
		logger.Error("Failed to enable MCP server for agent", logger.Fields{"agentName": agentName, "err": err, "agent": serverName})
		orihttp.InternalError(w, err.Error())
		// Try to start the server if not already running (best effort, don't fail if it doesn't start)
		return
	}

	if err := h.syncAgentMCPServer(agentName, serverName, true); err != nil {
		logger.Error("Failed to sync enabled MCP server into agent state", logger.Fields{"agentName": agentName, "server": serverName, "err": err})
		orihttp.InternalError(w, err.Error())
		return
	}

	status, _ := h.registry.GetServerStatus(serverName)
	if status == mcp.StatusStopped || status == mcp.StatusError {
		if err := h.registry.StartServer(serverName); err != nil {
			logger.Error("Failed to start MCP server : (will remain enabled for agent)", logger.Fields{"server": serverName, "err": err})
			// Don't return error - server is enabled for agent even if not currently running
		}
	}

	logger.Info("Enabled MCP server '' for agent ''", logger.Fields{"agentName": agentName, "server": serverName})

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("MCP server '%s' enabled for agent '%s'", serverName, agentName),
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// DisableAgentMCPServerHandler disables an MCP server for a specific agent
// POST /api/agents/{name}/mcp-servers/{serverName}/disable
func (h *MCPHandler) DisableAgentMCPServerHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}

	// Extract agent name and server name from path
	// api/agents/{name}/mcp-servers/{serverName}/disable
	path := r.URL.Path
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 5 {
		orihttp.BadRequest(w, "Agent name and server name required in path")
		return
	}
	agentName := parts[2]
	serverName := parts[4]

	// Verify agent exists
	_, ok := h.agentHandler.State.GetAgent(agentName)
	if !ok {
		orihttp.NotFound(w, "Agent not found")
		// Disable server for agent
		return
	}

	if err := h.configManager.DisableServerForAgent(agentName, serverName); err != nil {
		logger.Error("Failed to disable MCP server for agent", logger.Fields{"server": serverName, "agentName": agentName, "err": err})
		orihttp.InternalError(w, err.Error())
		return
	}

	if err := h.syncAgentMCPServer(agentName, serverName, false); err != nil {
		logger.Error("Failed to sync disabled MCP server into agent state", logger.Fields{"agentName": agentName, "server": serverName, "err": err})
		orihttp.InternalError(w, err.Error())
		return
	}

	logger.Info("Disabled MCP server '' for agent ''", logger.Fields{"agent": serverName, "agentName": agentName})

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("MCP server '%s' disabled for agent '%s'", serverName, agentName),
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// UpdateAgentMCPServerConfigHandler updates MCP server configuration for a specific agent.
// PUT /api/agents/{name}/mcp-servers/{serverName}/config
func (h *MCPHandler) UpdateAgentMCPServerConfigHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		orihttp.MethodNotAllowed(w)
		return
	}

	path := r.URL.Path
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 6 {
		orihttp.BadRequest(w, "Agent name and server name required in path")
		return
	}
	agentName := parts[2]
	serverName := parts[4]

	// Verify agent exists
	_, ok := h.agentHandler.State.GetAgent(agentName)
	if !ok {
		orihttp.NotFound(w, "Agent not found")
		return
	}

	if serverName != "filesystem" {
		orihttp.BadRequest(w, "Config updates are currently only supported for the filesystem MCP server")
		return
	}

	var req updateServerConfigRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	newPath := strings.TrimSpace(req.Path)
	if newPath == "" {
		orihttp.BadRequest(w, "Path is required")
		return
	}

	updatedConfig, err := h.updateFilesystemServerPath(newPath)
	if err != nil {
		logger.Error("Failed to update filesystem MCP server path", logger.Fields{
			"agentName": agentName,
			"server":    serverName,
			"path":      newPath,
			"err":       err,
		})
		orihttp.InternalError(w, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"server":  updatedConfig.Name,
		"path":    newPath,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// getServerDescription returns a human-readable description for known MCP servers
func getServerDescription(serverName string) string {
	descriptions := map[string]string{
		"filesystem": "Read, write, and manage files and directories within allowed paths",
		"github":     "Interact with GitHub repositories, issues, and pull requests",
		"sqlite":     "Query and manage SQLite databases",
		"postgres":   "Query and manage PostgreSQL databases",
	}

	if desc, ok := descriptions[serverName]; ok {
		return desc
	}
	return "" // No description available
}

func (h *MCPHandler) syncAgentMCPServer(agentName, serverName string, enabled bool) error {
	ag, ok := h.agentHandler.State.GetAgent(agentName)
	if !ok || ag == nil {
		return fmt.Errorf("agent not found")
	}

	if enabled {
		for _, existing := range ag.MCPServers {
			if existing == serverName {
				return nil
			}
		}
		ag.MCPServers = append(ag.MCPServers, serverName)
	} else {
		filtered := make([]string, 0, len(ag.MCPServers))
		for _, existing := range ag.MCPServers {
			if existing == serverName {
				continue
			}
			filtered = append(filtered, existing)
		}
		ag.MCPServers = filtered
	}

	return h.agentHandler.State.SetAgent(agentName, ag)
}

func (h *MCPHandler) updateFilesystemServerPath(newPath string) (*mcp.ServerConfig, error) {
	serverConfig, err := h.configManager.GetServer("filesystem")
	if err != nil {
		return nil, err
	}

	updated := *serverConfig
	switch {
	case len(updated.Args) >= 3:
		updated.Args[2] = newPath
	case len(updated.Args) == 2:
		updated.Args = append(updated.Args, newPath)
	default:
		updated.Args = []string{"-y", "@modelcontextprotocol/server-filesystem", newPath}
	}

	if err := h.configManager.UpdateServer(updated); err != nil {
		return nil, err
	}

	previousStatus := mcp.StatusStopped
	if status, statusErr := h.registry.GetServerStatus(updated.Name); statusErr == nil {
		previousStatus = status
	}

	if _, getErr := h.registry.GetServer(updated.Name); getErr == nil {
		if err := h.registry.RemoveServer(updated.Name); err != nil {
			return nil, err
		}
	}

	if err := h.registry.AddServer(updated); err != nil {
		return nil, err
	}

	if previousStatus == mcp.StatusRunning || previousStatus == mcp.StatusStarting || previousStatus == mcp.StatusRestarting || previousStatus == mcp.StatusError {
		if err := h.registry.StartServer(updated.Name); err != nil {
			// Keep saved config even if restart fails; frontend will display tools status.
			logger.Warn("Updated filesystem MCP path but failed to restart server", logger.Fields{
				"server": updated.Name,
				"err":    err,
			})
		}
	}

	return &updated, nil
}
