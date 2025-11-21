package mcphttp

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

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
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
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
		log.Printf("Failed to encode response: %v", err)
	}
}

// AddServerHandler adds a new MCP server
// POST /api/mcp/servers
func (h *Handler) AddServerHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var serverConfig mcp.ServerConfig
	if err := json.NewDecoder(r.Body).Decode(&serverConfig); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Add to config manager (persists to disk)
	if err := h.configManager.AddServer(serverConfig); err != nil {
		log.Printf("Failed to add MCP server to config: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Add to registry (runtime)
	if err := h.registry.AddServer(serverConfig); err != nil {
		log.Printf("Failed to add MCP server to registry: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]string{"status": "success"}); err != nil {
		log.Printf("Failed to encode response: %v", err)
	}
}

// RemoveServerHandler removes an MCP server
// DELETE /api/mcp/servers/:name
func (h *Handler) RemoveServerHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract server name from path: /api/mcp/servers/NAME
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 5 {
		http.Error(w, "Server name required", http.StatusBadRequest)
		return
	}
	serverName := parts[4]

	// Remove from registry (stops if running)
	if err := h.registry.RemoveServer(serverName); err != nil {
		log.Printf("Failed to remove MCP server from registry: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Remove from config (persists)
	if err := h.configManager.RemoveServer(serverName); err != nil {
		log.Printf("Failed to remove MCP server from config: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]string{"status": "success"}); err != nil {
		log.Printf("Failed to encode response: %v", err)
	}
}

// EnableServerHandler enables an MCP server for the current agent
// POST /api/mcp/servers/:name/enable
func (h *Handler) EnableServerHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract server name from path
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 5 {
		http.Error(w, "Server name required", http.StatusBadRequest)
		return
	}
	serverName := parts[4]

	// Get current agent
	_, currentAgentName := h.store.ListAgents()
	if currentAgentName == "" {
		http.Error(w, "No current agent", http.StatusBadRequest)
		return
	}

	// Enable server for agent in config
	if err := h.configManager.EnableServerForAgent(currentAgentName, serverName); err != nil {
		log.Printf("Failed to enable MCP server: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Check current server status
	status, err := h.registry.GetServerStatus(serverName)
	if err != nil {
		log.Printf("Failed to get MCP server status: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// If server is in error state or stopped, try to start/restart it
	if status == mcp.StatusError || status == mcp.StatusStopped {
		// Stop first if in error state to clean up
		if status == mcp.StatusError {
			_ = h.registry.StopServer(serverName) // Ignore error, might already be stopped
		}

		// Start the server
		if err := h.registry.StartServer(serverName); err != nil {
			log.Printf("Failed to start MCP server: %v", err)
			http.Error(w, fmt.Sprintf("Failed to start server: %v", err), http.StatusInternalServerError)
			return
		}
	} else if status == mcp.StatusRunning {
		// Already running, this is fine
		log.Printf("MCP server %s is already running", serverName)
	} else {
		// Status is starting or restarting, wait a bit or just continue
		log.Printf("MCP server %s is in state: %s", serverName, status)
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]string{"status": "success"}); err != nil {
		log.Printf("Failed to encode response: %v", err)
	}
}

// DisableServerHandler disables an MCP server for the current agent
// POST /api/mcp/servers/:name/disable
func (h *Handler) DisableServerHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract server name from path
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 5 {
		http.Error(w, "Server name required", http.StatusBadRequest)
		return
	}
	serverName := parts[4]

	// Get current agent
	_, currentAgentName := h.store.ListAgents()
	if currentAgentName == "" {
		http.Error(w, "No current agent", http.StatusBadRequest)
		return
	}

	// Disable server for agent in config
	if err := h.configManager.DisableServerForAgent(currentAgentName, serverName); err != nil {
		log.Printf("Failed to disable MCP server: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]string{"status": "success"}); err != nil {
		log.Printf("Failed to encode response: %v", err)
	}
}

// GetServerToolsHandler lists tools available from a specific server
// GET /api/mcp/servers/:name/tools
func (h *Handler) GetServerToolsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract server name from path
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 5 {
		http.Error(w, "Server name required", http.StatusBadRequest)
		return
	}
	serverName := parts[4]

	server, err := h.registry.GetServer(serverName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	tools := server.GetTools()

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"server": serverName,
		"tools":  tools,
	}); err != nil {
		log.Printf("Failed to encode response: %v", err)
	}
}

// GetServerStatusHandler gets status for a specific server
// GET /api/mcp/servers/:name/status
func (h *Handler) GetServerStatusHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract server name from path
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 5 {
		http.Error(w, "Server name required", http.StatusBadRequest)
		return
	}
	serverName := parts[4]

	status, err := h.registry.GetServerStatus(serverName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"server": serverName,
		"status": status,
	}); err != nil {
		log.Printf("Failed to encode response: %v", err)
	}
}

// TestConnectionHandler tests connection to an MCP server
// POST /api/mcp/servers/:name/test
func (h *Handler) TestConnectionHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract server name from path
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 5 {
		http.Error(w, "Server name required", http.StatusBadRequest)
		return
	}
	serverName := parts[4]

	// Get server
	server, err := h.registry.GetServer(serverName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
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
				log.Printf("Failed to encode response: %v", err)
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
		log.Printf("Failed to encode response: %v", err)
	}
}

// RetryConnectionHandler manually retries a failed server connection
// POST /api/mcp/servers/:name/retry
func (h *Handler) RetryConnectionHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract server name from path
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 5 {
		http.Error(w, "Server name required", http.StatusBadRequest)
		return
	}
	serverName := parts[4]

	// Get server
	server, err := h.registry.GetServer(serverName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	// Restart the server (stops if running, then starts)
	if err := server.Restart(); err != nil {
		log.Printf("Failed to restart MCP server %s: %v", serverName, err)
		http.Error(w, fmt.Sprintf("Failed to restart server: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]string{"status": "success", "message": "Server restart initiated"}); err != nil {
		log.Printf("Failed to encode response: %v", err)
	}
}

// ImportServersHandler imports MCP server configurations from uploaded JSON/YAML
// POST /api/mcp/import
func (h *Handler) ImportServersHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse multipart form
	if err := r.ParseMultipartForm(10 << 20); err != nil { // 10 MB max
		http.Error(w, "Failed to parse form", http.StatusBadRequest)
		return
	}

	// Get uploaded file
	file, _, err := r.FormFile("config_file")
	if err != nil {
		http.Error(w, "No file uploaded", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Read file content
	var config struct {
		Servers []mcp.ServerConfig `json:"servers"`
	}
	if err := json.NewDecoder(file).Decode(&config); err != nil {
		http.Error(w, fmt.Sprintf("Invalid JSON format: %v", err), http.StatusBadRequest)
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
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"added":  added,
		"errors": errors,
	}); err != nil {
		log.Printf("Failed to encode response: %v", err)
	}
}

// GetMarketplaceServersHandler returns available MCP servers from marketplace
// GET /api/mcp/marketplace
func (h *Handler) GetMarketplaceServersHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// For now, return a curated list of well-known MCP servers
	// TODO: Fetch from external registry in the future
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
		log.Printf("Failed to encode response: %v", err)
	}
}
