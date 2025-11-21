package agenthttp

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

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
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract agent name from path: /api/agents/{name}/mcp-servers
	path := r.URL.Path
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 4 {
		http.Error(w, "Agent name required in path", http.StatusBadRequest)
		return
	}
	agentName := parts[2] // api/agents/{name}/mcp-servers

	// Verify agent exists
	_, ok := h.agentHandler.State.GetAgent(agentName)
	if !ok {
		http.Error(w, "Agent not found", http.StatusNotFound)
		return
	}

	// Get all globally configured servers
	globalServers := h.registry.ListServers()
	stats := h.registry.GetServerStats()

	// Get enabled servers for this agent
	enabledServers, err := h.configManager.GetEnabledServersForAgent(agentName)
	if err != nil {
		log.Printf("Failed to get enabled servers for agent %s: %v", agentName, err)
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
		log.Printf("Failed to encode response: %v", err)
	}
}

// EnableAgentMCPServerHandler enables an MCP server for a specific agent
// POST /api/agents/{name}/mcp-servers/{serverName}/enable
func (h *MCPHandler) EnableAgentMCPServerHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract agent name and server name from path
	path := r.URL.Path
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 5 {
		http.Error(w, "Agent name and server name required in path", http.StatusBadRequest)
		return
	}
	agentName := parts[2]  // api/agents/{name}/mcp-servers/{serverName}/enable
	serverName := parts[4] // api/agents/{name}/mcp-servers/{serverName}/enable

	// Verify agent exists
	_, ok := h.agentHandler.State.GetAgent(agentName)
	if !ok {
		http.Error(w, "Agent not found", http.StatusNotFound)
		return
	}

	// Verify server exists in global registry
	_, err := h.registry.GetServer(serverName)
	if err != nil {
		http.Error(w, fmt.Sprintf("MCP server '%s' not found in global registry", serverName), http.StatusNotFound)
		return
	}

	// Enable server for agent
	if err := h.configManager.EnableServerForAgent(agentName, serverName); err != nil {
		log.Printf("Failed to enable MCP server %s for agent %s: %v", serverName, agentName, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Try to start the server if not already running (best effort, don't fail if it doesn't start)
	status, _ := h.registry.GetServerStatus(serverName)
	if status == mcp.StatusStopped || status == mcp.StatusError {
		if err := h.registry.StartServer(serverName); err != nil {
			log.Printf("Warning: Failed to start MCP server %s: %v (will remain enabled for agent)", serverName, err)
			// Don't return error - server is enabled for agent even if not currently running
		}
	}

	log.Printf("✅ Enabled MCP server '%s' for agent '%s'", serverName, agentName)

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("MCP server '%s' enabled for agent '%s'", serverName, agentName),
	}); err != nil {
		log.Printf("Failed to encode response: %v", err)
	}
}

// DisableAgentMCPServerHandler disables an MCP server for a specific agent
// POST /api/agents/{name}/mcp-servers/{serverName}/disable
func (h *MCPHandler) DisableAgentMCPServerHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract agent name and server name from path
	path := r.URL.Path
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 5 {
		http.Error(w, "Agent name and server name required in path", http.StatusBadRequest)
		return
	}
	agentName := parts[2]  // api/agents/{name}/mcp-servers/{serverName}/disable
	serverName := parts[4] // api/agents/{name}/mcp-servers/{serverName}/disable

	// Verify agent exists
	_, ok := h.agentHandler.State.GetAgent(agentName)
	if !ok {
		http.Error(w, "Agent not found", http.StatusNotFound)
		return
	}

	// Disable server for agent
	if err := h.configManager.DisableServerForAgent(agentName, serverName); err != nil {
		log.Printf("Failed to disable MCP server %s for agent %s: %v", serverName, agentName, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	log.Printf("✅ Disabled MCP server '%s' for agent '%s'", serverName, agentName)

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("MCP server '%s' disabled for agent '%s'", serverName, agentName),
	}); err != nil {
		log.Printf("Failed to encode response: %v", err)
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
