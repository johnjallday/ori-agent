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
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.

				// Extract agent name from path: /api/agents/{name}/mcp-servers
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	path := r.URL.Path
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 4 {
		if err := orihttp.RespondBadRequest(w, "Agent name required in path"); err != nil {
			logger.Error("Failed to write response",

				// api/agents/{name}/mcp-servers
				logger.Fields{"error":

				// Verify agent exists
				err})
		}
		return
	}
	agentName := parts[2]

	_, ok := h.agentHandler.State.GetAgent(agentName)
	if !ok {
		if err := orihttp.RespondNotFound(w, "Agent not found"); err != nil {
			logger.

				// Get all globally configured servers
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

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
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"agent":   agentName,
		"servers": servers,
	}); err != nil {
		logger.Error("Failed to encode response", logger.Fields{"response": err})
	}
}

// EnableAgentMCPServerHandler enables an MCP server for a specific agent
// POST /api/agents/{name}/mcp-servers/{serverName}/enable
func (h *MCPHandler) EnableAgentMCPServerHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.

				// Extract agent name and server name from path
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	path := r.URL.Path
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 5 {
		if err := orihttp.RespondBadRequest(w, "Agent name and server name required in path"); err != nil {
			logger.Error("Failed to write response",

				// api/agents/{name}/mcp-servers/{serverName}/enable
				logger.Fields{"error": err})
		}
		return
	}
	agentName := parts[2]
	serverName := parts[4] // api/agents/{name}/mcp-servers/{serverName}/enable

	// Verify agent exists
	_, ok := h.agentHandler.State.GetAgent(agentName)
	if !ok {
		if err := orihttp.RespondNotFound(w, "Agent not found"); err != nil {
			logger.

				// Verify server exists in global registry
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	_, err := h.registry.GetServer(serverName)
	if err != nil {
		if err := orihttp.RespondNotFound(w, fmt.Sprintf("MCP server '%s' not found in global registry", serverName)); err != nil {
			logger.

				// Enable server for agent
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	if err := h.configManager.EnableServerForAgent(agentName, serverName); err != nil {
		logger.Error("Failed to enable MCP server for agent", logger.Fields{"agentName": agentName, "err": err, "agent": serverName})
		if err := orihttp.RespondInternalError(w, err.Error()); err != nil {
			logger.

				// Try to start the server if not already running (best effort, don't fail if it doesn't start)
				Error("Failed to write response", logger.Fields{"error": err})
		}
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
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("MCP server '%s' enabled for agent '%s'", serverName, agentName),
	}); err != nil {
		logger.Error("Failed to encode response", logger.Fields{"response": err})
	}
}

// DisableAgentMCPServerHandler disables an MCP server for a specific agent
// POST /api/agents/{name}/mcp-servers/{serverName}/disable
func (h *MCPHandler) DisableAgentMCPServerHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.

				// Extract agent name and server name from path
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	path := r.URL.Path
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 5 {
		if err := orihttp.RespondBadRequest(w, "Agent name and server name required in path"); err != nil {
			logger.Error("Failed to write response",

				// api/agents/{name}/mcp-servers/{serverName}/disable
				logger.Fields{"error": err})
		}
		return
	}
	agentName := parts[2]
	serverName := parts[4] // api/agents/{name}/mcp-servers/{serverName}/disable

	// Verify agent exists
	_, ok := h.agentHandler.State.GetAgent(agentName)
	if !ok {
		if err := orihttp.RespondNotFound(w, "Agent not found"); err != nil {
			logger.

				// Disable server for agent
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	if err := h.configManager.DisableServerForAgent(agentName, serverName); err != nil {
		logger.Error("Failed to disable MCP server for agent", logger.Fields{"server": serverName, "agentName": agentName, "err": err})
		if err := orihttp.RespondInternalError(w, err.Error()); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	logger.Info("Disabled MCP server '' for agent ''", logger.Fields{"agent": serverName, "agentName": agentName})

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("MCP server '%s' disabled for agent '%s'", serverName, agentName),
	}); err != nil {
		logger.Error("Failed to encode response", logger.Fields{"response": err})
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
