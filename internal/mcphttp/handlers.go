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
	json.NewEncoder(w).Encode(response)
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
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
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
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
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
			h.registry.StopServer(serverName) // Ignore error, might already be stopped
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
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
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
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
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
	json.NewEncoder(w).Encode(map[string]interface{}{
		"server": serverName,
		"tools":  tools,
	})
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
	json.NewEncoder(w).Encode(map[string]interface{}{
		"server": serverName,
		"status": status,
	})
}
