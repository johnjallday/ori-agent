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
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
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
	if err := json.NewEncoder(w).Encode(response); err != nil {
		logger.Error("Failed to encode response", logger.Fields{"response": err})
	}
}

// AddServerHandler adds a new MCP server
// POST /api/mcp/servers
func (h *Handler) AddServerHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	var serverConfig mcp.ServerConfig
	if err := json.NewDecoder(r.Body).Decode(&serverConfig); err != nil {
		if err := orihttp.RespondBadRequest(w, "Invalid request body"); err != nil {
			logger.

				// Add to config manager (persists to disk)
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	if err := h.configManager.AddServer(serverConfig); err != nil {
		logger.Error("Failed to add MCP server to config", logger.Fields{"server": err})
		if err := orihttp.RespondInternalError(w, err.Error()); err != nil {
			logger.

				// Add to registry (runtime)
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	if err := h.registry.AddServer(serverConfig); err != nil {
		logger.Error("Failed to add MCP server to registry", logger.Fields{"server": err})
		if err := orihttp.RespondInternalError(w, err.Error()); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]string{"status": "success"}); err != nil {
		logger.Error("Failed to encode response", logger.Fields{"response": err})
	}
}

// RemoveServerHandler removes an MCP server
// DELETE /api/mcp/servers/:name
func (h *Handler) RemoveServerHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.

				// Extract server name from path: /api/mcp/servers/NAME
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 5 {
		if err := orihttp.RespondBadRequest(w, "Server name required"); err != nil {
			logger.Error("Failed to write response",

				// Remove from registry (stops if running)
				logger.Fields{"error": err})
		}
		return
	}
	serverName := parts[4]

	if err := h.registry.RemoveServer(serverName); err != nil {
		logger.Error("Failed to remove MCP server from registry", logger.Fields{"server": err})
		if err := orihttp.RespondInternalError(w, err.Error()); err != nil {
			logger.

				// Remove from config (persists)
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	if err := h.configManager.RemoveServer(serverName); err != nil {
		logger.Error("Failed to remove MCP server from config", logger.Fields{"server": err})
		if err := orihttp.RespondInternalError(w, err.Error()); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]string{"status": "success"}); err != nil {
		logger.Error("Failed to encode response", logger.Fields{"response": err})
	}
}

// EnableServerHandler enables an MCP server for the current agent
// POST /api/mcp/servers/:name/enable
func (h *Handler) EnableServerHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.

				// Extract server name from path
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 5 {
		if err := orihttp.RespondBadRequest(w, "Server name required"); err != nil {
			logger.Error("Failed to write response",

				// Get current agent
				logger.Fields{"error": err})
		}
		return
	}
	serverName := parts[4]

	_, currentAgentName := h.store.ListAgents()
	if currentAgentName == "" {
		if err := orihttp.RespondBadRequest(w, "No current agent"); err != nil {
			logger.

				// Enable server for agent in config
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	if err := h.configManager.EnableServerForAgent(currentAgentName, serverName); err != nil {
		logger.Error("Failed to enable MCP server", logger.Fields{"server": err})
		if err := orihttp.RespondInternalError(w, err.Error()); err != nil {
			logger.

				// Check current server status
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	status, err := h.registry.GetServerStatus(serverName)
	if err != nil {
		logger.Error("Failed to get MCP server status", logger.Fields{"server": err})
		if err := orihttp.RespondInternalError(w, err.Error()); err != nil {
			logger.

				// If server is in error state or stopped, try to start/restart it
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

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
	if err := json.NewEncoder(w).Encode(map[string]string{"status": "success"}); err != nil {
		logger.Error("Failed to encode response", logger.Fields{"response": err})
	}
}

// DisableServerHandler disables an MCP server for the current agent
// POST /api/mcp/servers/:name/disable
func (h *Handler) DisableServerHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.

				// Extract server name from path
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 5 {
		if err := orihttp.RespondBadRequest(w, "Server name required"); err != nil {
			logger.Error("Failed to write response",

				// Get current agent
				logger.Fields{"error": err})
		}
		return
	}
	serverName := parts[4]

	_, currentAgentName := h.store.ListAgents()
	if currentAgentName == "" {
		if err := orihttp.RespondBadRequest(w, "No current agent"); err != nil {
			logger.

				// Disable server for agent in config
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	if err := h.configManager.DisableServerForAgent(currentAgentName, serverName); err != nil {
		logger.Error("Failed to disable MCP server", logger.Fields{"server": err})
		if err := orihttp.RespondInternalError(w, err.Error()); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]string{"status": "success"}); err != nil {
		logger.Error("Failed to encode response", logger.Fields{"response": err})
	}
}

// GetServerToolsHandler lists tools available from a specific server
// GET /api/mcp/servers/:name/tools
func (h *Handler) GetServerToolsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.

				// Extract server name from path
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 5 {
		if err := orihttp.RespondBadRequest(w, "Server name required"); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
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
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"server": serverName,
		"tools":  tools,
	}); err != nil {
		logger.Error("Failed to encode response", logger.Fields{"response": err})
	}
}

// GetServerStatusHandler gets status for a specific server
// GET /api/mcp/servers/:name/status
func (h *Handler) GetServerStatusHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.

				// Extract server name from path
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 5 {
		if err := orihttp.RespondBadRequest(w, "Server name required"); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
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
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"server": serverName,
		"status": status,
	}); err != nil {
		logger.Error("Failed to encode response", logger.Fields{"response": err})
	}
}

// TestConnectionHandler tests connection to an MCP server
// POST /api/mcp/servers/:name/test
func (h *Handler) TestConnectionHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.

				// Extract server name from path
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 5 {
		if err := orihttp.RespondBadRequest(w, "Server name required"); err != nil {
			logger.Error("Failed to write response",

				// Get server
				logger.Fields{"error": err})
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

	// Check current status
	status := server.GetStatus()

	// If stopped, try to start temporarily for testing
	wasStarted := false
	if status == mcp.StatusStopped {
		if err := server.Start(); err != nil {
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"error":   fmt.Sprintf("Failed to start server: %v", err),
			}); err != nil {
				logger.Error("Failed to encode response", logger.Fields{"response": err})
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
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"success":    true,
		"tool_count": len(tools),
		"message":    "Connection successful",
	}); err != nil {
		logger.Error("Failed to encode response", logger.Fields{"response": err})
	}
}

// RetryConnectionHandler manually retries a failed server connection
// POST /api/mcp/servers/:name/retry
func (h *Handler) RetryConnectionHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.

				// Extract server name from path
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 5 {
		if err := orihttp.RespondBadRequest(w, "Server name required"); err != nil {
			logger.Error("Failed to write response",

				// Get server
				logger.Fields{"error": err})
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

	// Restart the server (stops if running, then starts)
	if err := server.Restart(); err != nil {
		logger.Error("Failed to restart MCP server", logger.Fields{"server": serverName, "err": err})
		if err := orihttp.RespondInternalError(w, fmt.Sprintf("Failed to restart server: %v", err)); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]string{"status": "success", "message": "Server restart initiated"}); err != nil {
		logger.Error("Failed to encode response", logger.Fields{"response": err})
	}
}

// ImportServersHandler imports MCP server configurations from uploaded JSON/YAML
// POST /api/mcp/import
func (h *Handler) ImportServersHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.

				// Parse multipart form
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	if err := r.ParseMultipartForm(10 << 20); err != nil {
		if // 10 MB max
		err := orihttp.RespondBadRequest(w, "Failed to parse form"); err != nil {
			logger.

				// Get uploaded file
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	file, _, err := r.FormFile("config_file")
	if err != nil {
		if err := orihttp.RespondBadRequest(w, "No file uploaded"); err != nil {
			logger.Error("Failed to write response", logger.

				// Read file content
				Fields{"error": err})
		}
		return
	}
	defer func() { _ = file.Close() }()

	var config struct {
		Servers []mcp.ServerConfig `json:"servers"`
	}
	if err := json.NewDecoder(file).Decode(&config); err != nil {
		if err := orihttp.RespondBadRequest(w, fmt.Sprintf("Invalid JSON format: %v", err)); err != nil {
			logger.

				// Validate and add servers
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

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
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"added":  added,
		"errors": errors,
	}); err != nil {
		logger.Error("Failed to encode response", logger.Fields{"response": err})
	}
}

// GetMarketplaceServersHandler returns available MCP servers from marketplace
// GET /api/mcp/marketplace
func (h *Handler) GetMarketplaceServersHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.

				// For now, return a curated list of well-known MCP servers
				// TODO: Fetch from external registry in the future
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

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
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"servers": marketplaceServers,
	}); err != nil {
		logger.Error("Failed to encode response", logger.Fields{"response": err})
	}
}
