package mcphttp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/mcp"
	"github.com/johnjallday/ori-agent/internal/store"
)

// Handler handles MCP-related HTTP requests
type Handler struct {
	registry      *mcp.Registry
	configManager *mcp.ConfigManager
	store         store.Store
}

// NewHandler creates a new MCP HTTP handler
func NewHandler(registry *mcp.Registry, configManager *mcp.ConfigManager, store store.Store) *Handler {
	return &Handler{
		registry:      registry,
		configManager: configManager,
		store:         store,
	}
}

// ListServersHandler lists all MCP servers
// GET /api/mcp/servers
func (h *Handler) ListServersHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		if respErr := orihttp.RespondMethodNotAllowed(w); respErr != nil {
			logger.Error("Failed to write response", logger.Fields{"error": respErr})
		}
		return
	}

	servers := h.registry.ListServers()
	stats := h.registry.GetServerStats()

	response := map[string]interface{}{
		"servers": servers,
		"stats":   stats,
	}

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(response); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// AddServerHandler adds a new MCP server
// POST /api/mcp/servers
func (h *Handler) AddServerHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		if respErr := orihttp.RespondMethodNotAllowed(w); respErr != nil {
			logger.Error("Failed to write response", logger.Fields{"error": respErr})
		}
		return
	}

	var serverConfig mcp.ServerConfig
	if !orihttp.ParseJSONBody(w, r, &serverConfig) {
		return
	}

	// Add to config manager (persists to disk)

	if err := h.configManager.AddServer(serverConfig); err != nil {
		logger.Error("Failed to add MCP server to config", logger.Fields{"server": err})
		if respErr := orihttp.RespondInternalError(w, err.Error()); respErr != nil {
			logger.Error("Failed to write response", logger.Fields{"error": respErr})
		}
		return
	}

	// Add to registry (runtime)

	if err := h.registry.AddServer(serverConfig); err != nil {
		logger.Error("Failed to add MCP server to registry", logger.Fields{"server": err})
		if respErr := orihttp.RespondInternalError(w, err.Error()); respErr != nil {
			logger.Error("Failed to write response", logger.Fields{"error": respErr})
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]string{"status": "success"}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// RemoveServerHandler removes an MCP server
// DELETE /api/mcp/servers/:name
func (h *Handler) RemoveServerHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		if respErr := orihttp.RespondMethodNotAllowed(w); respErr != nil {
			logger.Error("Failed to write response", logger.Fields{"error": respErr})
		}
		return
	}

	// Extract server name from path: /api/mcp/servers/NAME

	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 5 {
		if respErr := orihttp.RespondBadRequest(w, "Server name required"); respErr != nil {
			logger.Error("Failed to write response", logger.Fields{"error": respErr})
		}
		return
	}
	serverName := parts[4]

	// Remove from registry (stops if running)
	if err := h.registry.RemoveServer(serverName); err != nil {
		logger.Error("Failed to remove MCP server from registry", logger.Fields{"server": err})
		if respErr := orihttp.RespondInternalError(w, err.Error()); respErr != nil {
			logger.Error("Failed to write response", logger.Fields{"error": respErr})
		}
		return
	}

	// Remove from config (persists)

	if err := h.configManager.RemoveServer(serverName); err != nil {
		logger.Error("Failed to remove MCP server from config", logger.Fields{"server": err})
		if respErr := orihttp.RespondInternalError(w, err.Error()); respErr != nil {
			logger.Error("Failed to write response", logger.Fields{"error": respErr})
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]string{"status": "success"}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// EnableServerHandler enables an MCP server for the current agent
// POST /api/mcp/servers/:name/enable
func (h *Handler) EnableServerHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		if respErr := orihttp.RespondMethodNotAllowed(w); respErr != nil {
			logger.Error("Failed to write response", logger.Fields{"error": respErr})
		}
		return
	}

	// Extract server name from path
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 5 {
		if respErr := orihttp.RespondBadRequest(w, "Server name required"); respErr != nil {
			logger.Error("Failed to write response", logger.Fields{"error": respErr})
		}
		return
	}
	serverName := parts[4]

	// Get current agent
	_, currentAgentName := h.store.ListAgents()
	if currentAgentName == "" {
		if respErr := orihttp.RespondBadRequest(w, "No current agent"); respErr != nil {
			logger.Error("Failed to write response", logger.Fields{"error": respErr})
		}
		return
	}

	// Enable server for agent in config
	if err := h.configManager.EnableServerForAgent(currentAgentName, serverName); err != nil {
		logger.Error("Failed to enable MCP server", logger.Fields{"server": err})
		if respErr := orihttp.RespondInternalError(w, err.Error()); respErr != nil {
			logger.Error("Failed to write response", logger.Fields{"error": respErr})
		}
		return
	}

	// Check current server status
	status, err := h.registry.GetServerStatus(serverName)
	if err != nil {
		logger.Error("Failed to get MCP server status", logger.Fields{"server": err})
		if respErr := orihttp.RespondInternalError(w, err.Error()); respErr != nil {
			logger.Error("Failed to write response", logger.Fields{"error": respErr})
		}
		return
	}

	// If server is in error state or stopped, try to start/restart it

	switch status {
	case mcp.StatusError, mcp.StatusStopped:
		// Stop first if in error state to clean up
		if status == mcp.StatusError {
			_ = h.registry.StopServer(serverName) // Ignore error, might already be stopped
		}

		// Start the server
		if err := h.registry.StartServer(serverName); err != nil {
			logger.Error("Failed to start MCP server", logger.Fields{"server": err})
			if encodeErr := orihttp.RespondInternalError(w, fmt.Sprintf("Failed to start server: %v", err)); encodeErr != nil {
				logger.Error("Failed to write internal error response", logger.Fields{"error": encodeErr})
			}
			return
		}
	case mcp.StatusRunning:
		// Already running, this is fine
		logger.Debug("MCP server is already running", logger.Fields{"server": serverName})
	default:
		// Status is starting or restarting, wait a bit or just continue
		logger.Debug("MCP server is in state", logger.Fields{"server": serverName, "status": status})
	}

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]string{"status": "success"}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// DisableServerHandler disables an MCP server for the current agent
// POST /api/mcp/servers/:name/disable
func (h *Handler) DisableServerHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		if respErr := orihttp.RespondMethodNotAllowed(w); respErr != nil {
			logger.Error("Failed to write response", logger.Fields{"error": respErr})
		}
		return
	}

	// Extract server name from path
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 5 {
		if respErr := orihttp.RespondBadRequest(w, "Server name required"); respErr != nil {
			logger.Error("Failed to write response", logger.Fields{"error": respErr})
		}
		return
	}
	serverName := parts[4]

	// Get current agent
	_, currentAgentName := h.store.ListAgents()
	if currentAgentName == "" {
		if respErr := orihttp.RespondBadRequest(w, "No current agent"); respErr != nil {
			logger.Error("Failed to write response", logger.Fields{"error": respErr})
		}
		return
	}

	// Disable server for agent in config

	if err := h.configManager.DisableServerForAgent(currentAgentName, serverName); err != nil {
		logger.Error("Failed to disable MCP server", logger.Fields{"server": err})
		if respErr := orihttp.RespondInternalError(w, err.Error()); respErr != nil {
			logger.Error("Failed to write response", logger.Fields{"error": respErr})
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]string{"status": "success"}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// GetServerToolsHandler lists tools available from a specific server
// GET /api/mcp/servers/:name/tools
func (h *Handler) GetServerToolsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		if respErr := orihttp.RespondMethodNotAllowed(w); respErr != nil {
			logger.Error("Failed to write response", logger.Fields{"error": respErr})
		}
		return
	}

	// Extract server name from path

	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 5 {
		if respErr := orihttp.RespondBadRequest(w, "Server name required"); respErr != nil {
			logger.Error("Failed to write response", logger.Fields{"error": respErr})
		}
		return
	}
	serverName := parts[4]

	server, err := h.registry.GetServer(serverName)
	if err != nil {
		if encodeErr := orihttp.RespondNotFound(w, err.Error()); encodeErr != nil {
			logger.Error("Failed to write not found response", logger.Fields{"error": encodeErr})
		}
		return
	}

	tools := server.GetTools()

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]interface{}{
		"server": serverName,
		"tools":  tools,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// GetServerStatusHandler gets status for a specific server
// GET /api/mcp/servers/:name/status
func (h *Handler) GetServerStatusHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		if respErr := orihttp.RespondMethodNotAllowed(w); respErr != nil {
			logger.Error("Failed to write response", logger.Fields{"error": respErr})
		}
		return
	}

	// Extract server name from path

	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 5 {
		if respErr := orihttp.RespondBadRequest(w, "Server name required"); respErr != nil {
			logger.Error("Failed to write response", logger.Fields{"error": respErr})
		}
		return
	}
	serverName := parts[4]

	status, err := h.registry.GetServerStatus(serverName)
	if err != nil {
		if encodeErr := orihttp.RespondNotFound(w, err.Error()); encodeErr != nil {
			logger.Error("Failed to write not found response", logger.Fields{"error": encodeErr})
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]interface{}{
		"server": serverName,
		"status": status,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// TestConnectionHandler tests connection to an MCP server
// POST /api/mcp/servers/:name/test
func (h *Handler) TestConnectionHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		if respErr := orihttp.RespondMethodNotAllowed(w); respErr != nil {
			logger.Error("Failed to write response", logger.Fields{"error": respErr})
		}
		return
	}

	// Extract server name from path

	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 5 {
		if respErr := orihttp.RespondBadRequest(w, "Server name required"); respErr != nil {
			logger.Error("Failed to write response", logger.Fields{"error": respErr})
		}
		return
	}
	serverName := parts[4]

	// Get server
	server, err := h.registry.GetServer(serverName)
	if err != nil {
		if encodeErr := orihttp.RespondNotFound(w, err.Error()); encodeErr != nil {
			logger.Error("Failed to write not found response", logger.Fields{"error": encodeErr})
		}
		return
	}

	// Check current status
	status := server.GetStatus()

	// If stopped, try to start temporarily for testing
	wasStarted := false
	if status == mcp.StatusStopped {
		if err := server.Start(); err != nil {
			w.Header().Set("Content-Type", "application/json")
			if encErr := json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"error":   fmt.Sprintf("Failed to start server: %v", err),
			}); encErr != nil {
				logger.Error("Failed to encode response", logger.Fields{"error": encErr})
			}
			return
		}
		wasStarted = true
	}

	// Test connection by getting tools
	tools := server.GetTools()

	// Stop if we started it just for testing
	if wasStarted {
		_ = server.Stop() // Ignore error, server was just for testing
	}

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]interface{}{
		"success":    true,
		"tool_count": len(tools),
		"message":    "Connection successful",
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// RetryConnectionHandler manually retries a failed server connection
// POST /api/mcp/servers/:name/retry
func (h *Handler) RetryConnectionHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		if respErr := orihttp.RespondMethodNotAllowed(w); respErr != nil {
			logger.Error("Failed to write response", logger.Fields{"error": respErr})
		}
		return
	}

	// Extract server name from path

	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 5 {
		if respErr := orihttp.RespondBadRequest(w, "Server name required"); respErr != nil {
			logger.Error("Failed to write response", logger.Fields{"error": respErr})
		}
		return
	}
	serverName := parts[4]

	// Get server
	server, err := h.registry.GetServer(serverName)
	if err != nil {
		if encodeErr := orihttp.RespondNotFound(w, err.Error()); encodeErr != nil {
			logger.Error("Failed to write not found response", logger.Fields{"error": encodeErr})
		}
		return
	}

	// Restart the server (stops if running, then starts)
	if err := server.Restart(); err != nil {
		logger.Error("Failed to restart MCP server", logger.Fields{"server": serverName, "err": err})
		if respErr := orihttp.RespondInternalError(w, fmt.Sprintf("Failed to restart server: %v", err)); respErr != nil {
			logger.Error("Failed to write response", logger.Fields{"error": respErr})
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]string{"status": "success", "message": "Server restart initiated"}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// ImportServersHandler imports MCP server configurations from uploaded JSON/YAML
// POST /api/mcp/import
func (h *Handler) ImportServersHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		if respErr := orihttp.RespondMethodNotAllowed(w); respErr != nil {
			logger.Error("Failed to write response", logger.Fields{"error": respErr})
		}
		return
	}

	// Parse multipart form

	if err := r.ParseMultipartForm(10 << 20); err != nil { // 10 MB max
		if respErr := orihttp.RespondBadRequest(w, "Failed to parse form"); respErr != nil {
			logger.Error("Failed to write response", logger.Fields{"error": respErr})
		}
		return
	}

	// Get uploaded file
	file, _, err := r.FormFile("config_file")
	if err != nil {
		if respErr := orihttp.RespondBadRequest(w, "No file uploaded"); respErr != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}
	defer func() { _ = file.Close() }()

	// Read file content
	var config struct {
		Servers []mcp.ServerConfig `json:"servers"`
	}
	if err := json.NewDecoder(file).Decode(&config); err != nil {
		if respErr := orihttp.RespondBadRequest(w, fmt.Sprintf("Invalid JSON format: %v", err)); respErr != nil {
			logger.Error("Failed to write response", logger.Fields{"error": respErr})
		}
		return
	}

	// Validate and add servers

	var added []string
	var errors []string

	for _, serverConfig := range config.Servers {
		// Validate required fields
		if serverConfig.Name == "" || serverConfig.Command == "" {
			errors = append(errors, "Server missing required fields (name or command)")
			continue
		}

		// Add to config manager (persists to disk)
		if err := h.configManager.AddServer(serverConfig); err != nil {
			errors = append(errors, fmt.Sprintf("%s: %v", serverConfig.Name, err))
			continue
		}

		// Add to registry (runtime)
		if err := h.registry.AddServer(serverConfig); err != nil {
			errors = append(errors, fmt.Sprintf("%s: %v", serverConfig.Name, err))
			continue
		}

		added = append(added, serverConfig.Name)
	}

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]interface{}{
		"added":  added,
		"errors": errors,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// GetMarketplaceServersHandler returns available MCP servers from marketplace
// GET /api/mcp/marketplace
func (h *Handler) GetMarketplaceServersHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		if respErr := orihttp.RespondMethodNotAllowed(w); respErr != nil {
			logger.Error("Failed to write response", logger.Fields{"error": respErr})
		}
		return
	}

	// Return a curated list of well-known MCP servers.
	// For external registry integration, would need to:
	// - Define a registry API endpoint (e.g., GitHub raw JSON or custom API)
	// - Add caching with TTL to avoid excessive fetches
	// - Handle network errors gracefully with fallback to this static list
	marketplaceServers := []map[string]interface{}{
		{
			"name":        "filesystem",
			"description": "Provides read/write access to files and directories",
			"command":     "npx",
			"args":        []string{"-y", "@modelcontextprotocol/server-filesystem", "/path/to/allowed/directory"},
			"maintainer":  "Anthropic",
			"category":    "file-system",
			"transport":   "stdio",
		},
		{
			"name":        "github",
			"description": "Interact with GitHub repositories, issues, and pull requests",
			"command":     "npx",
			"args":        []string{"-y", "@modelcontextprotocol/server-github"},
			"maintainer":  "Anthropic",
			"category":    "development",
			"transport":   "stdio",
			"env_required": map[string]string{
				"GITHUB_TOKEN": "GitHub personal access token",
			},
		},
		{
			"name":        "brave-search",
			"description": "Perform web searches using Brave Search API",
			"command":     "npx",
			"args":        []string{"-y", "@modelcontextprotocol/server-brave-search"},
			"maintainer":  "Anthropic",
			"category":    "search",
			"transport":   "stdio",
			"env_required": map[string]string{
				"BRAVE_API_KEY": "Brave Search API key",
			},
		},
		{
			"name":        "postgres",
			"description": "Query and manage PostgreSQL databases",
			"command":     "npx",
			"args":        []string{"-y", "@modelcontextprotocol/server-postgres"},
			"maintainer":  "Anthropic",
			"category":    "database",
			"transport":   "stdio",
			"env_required": map[string]string{
				"DATABASE_URL": "PostgreSQL connection string",
			},
		},
		{
			"name":        "memory",
			"description": "Persistent memory storage across conversations",
			"command":     "npx",
			"args":        []string{"-y", "@modelcontextprotocol/server-memory"},
			"maintainer":  "Anthropic",
			"category":    "storage",
			"transport":   "stdio",
		},
	}

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]interface{}{
		"servers": marketplaceServers,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}
