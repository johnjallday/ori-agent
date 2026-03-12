package mcphttp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/mcp"
	"github.com/johnjallday/ori-agent/internal/mcp/mcpregistry"

	"github.com/google/uuid"
)

// Handler handles MCP-related HTTP requests
type Handler struct {
	registry      *mcp.Registry
	configManager *mcp.ConfigManager
	regStore      *mcpregistry.Store
	regFetcher    *mcpregistry.Fetcher
}

// NewHandler creates a new MCP HTTP handler
func NewHandler(registry *mcp.Registry, configManager *mcp.ConfigManager) *Handler {
	return &Handler{
		registry:      registry,
		configManager: configManager,
	}
}

// SetRegistryStore wires the MCP registry browser store into the handler.
func (h *Handler) SetRegistryStore(s *mcpregistry.Store) {
	h.regStore = s
	h.regFetcher = mcpregistry.NewFetcher()
}

// ListServersHandler lists all MCP servers
// GET /api/mcp/servers
func (h *Handler) ListServersHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		orihttp.MethodNotAllowed(w)
		return
	}

	servers := h.registry.ListServers()
	stats := h.registry.GetServerStats()

	response := map[string]any{
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
		orihttp.MethodNotAllowed(w)
		return
	}

	var serverConfig mcp.ServerConfig
	if !orihttp.ParseJSONBody(w, r, &serverConfig) {
		return
	}

	// Add to config manager (persists to disk)

	if err := h.configManager.AddServer(serverConfig); err != nil {
		logger.Error("Failed to add MCP server to config", logger.Fields{"server": err})
		orihttp.InternalError(w, err.Error())
		return
	}

	// Add to registry (runtime)

	if err := h.registry.AddServer(serverConfig); err != nil {
		logger.Error("Failed to add MCP server to registry", logger.Fields{"server": err})
		orihttp.InternalError(w, err.Error())
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
		orihttp.MethodNotAllowed(w)
		return
	}

	// Extract server name from path: /api/mcp/servers/NAME

	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 5 {
		orihttp.BadRequest(w, "Server name required")
		return
	}
	serverName := parts[4]

	// Remove from registry (stops if running)
	if err := h.registry.RemoveServer(serverName); err != nil {
		logger.Error("Failed to remove MCP server from registry", logger.Fields{"server": err})
		orihttp.InternalError(w, err.Error())
		return
	}

	// Remove from config (persists)

	if err := h.configManager.RemoveServer(serverName); err != nil {
		logger.Error("Failed to remove MCP server from config", logger.Fields{"server": err})
		orihttp.InternalError(w, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]string{"status": "success"}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// EnableServerHandler enables an MCP server globally so it is available to workspaces.
// POST /api/mcp/servers/:name/enable
func (h *Handler) EnableServerHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}

	// Extract server name from path
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 5 {
		orihttp.BadRequest(w, "Server name required")
		return
	}
	serverName := parts[4]

	current, err := h.configManager.GetServer(serverName)
	if err != nil {
		orihttp.NotFound(w, err.Error())
		return
	}

	updated := *current
	updated.Enabled = true
	if err := h.configManager.UpdateServer(updated); err != nil {
		logger.Error("Failed to persist enabled MCP server", logger.Fields{"server": serverName, "err": err})
		orihttp.InternalError(w, err.Error())
		return
	}

	if err := h.registry.UpsertServer(updated); err != nil {
		logger.Error("Failed to update MCP server in registry", logger.Fields{"server": serverName, "err": err})
		orihttp.InternalError(w, err.Error())
		return
	}

	var startErrMsg string

	// Check current server status
	status, err := h.registry.GetServerStatus(serverName)
	if err != nil {
		logger.Warn("Failed to get MCP server status after enable", logger.Fields{"server": serverName, "err": err})
	} else {
		// If server is in error state or stopped, try to start/restart it

		switch status {
		case mcp.StatusError, mcp.StatusStopped:
			// Stop first if in error state to clean up.
			if status == mcp.StatusError {
				_ = h.registry.StopServer(serverName)
			}

			if err := h.registry.StartServer(serverName); err != nil {
				startErrMsg = err.Error()
				logger.Warn("Enabled MCP server globally but failed to start it", logger.Fields{"server": serverName, "err": err})
			}
		case mcp.StatusRunning:
			logger.Debug("MCP server is already running", logger.Fields{"server": serverName})
		default:
			logger.Debug("MCP server is in state", logger.Fields{"server": serverName, "status": status})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]any{
		"status":      "success",
		"scope":       "global",
		"start_error": startErrMsg,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// DisableServerHandler disables an MCP server globally.
// POST /api/mcp/servers/:name/disable
func (h *Handler) DisableServerHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}

	// Extract server name from path
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 5 {
		orihttp.BadRequest(w, "Server name required")
		return
	}
	serverName := parts[4]

	current, err := h.configManager.GetServer(serverName)
	if err != nil {
		orihttp.NotFound(w, err.Error())
		return
	}

	updated := *current
	updated.Enabled = false
	if err := h.configManager.UpdateServer(updated); err != nil {
		logger.Error("Failed to persist disabled MCP server", logger.Fields{"server": serverName, "err": err})
		orihttp.InternalError(w, err.Error())
		return
	}

	if err := h.registry.UpsertServer(updated); err != nil {
		logger.Error("Failed to update disabled MCP server in registry", logger.Fields{"server": serverName, "err": err})
		orihttp.InternalError(w, err.Error())
		return
	}

	if status, err := h.registry.GetServerStatus(serverName); err == nil {
		switch status {
		case mcp.StatusRunning, mcp.StatusStarting, mcp.StatusRestarting, mcp.StatusError:
			if stopErr := h.registry.StopServer(serverName); stopErr != nil {
				logger.Warn("Disabled MCP server globally but failed to stop it", logger.Fields{"server": serverName, "err": stopErr})
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]any{"status": "success", "scope": "global"}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// GetServerToolsHandler lists tools available from a specific server
// GET /api/mcp/servers/:name/tools
func (h *Handler) GetServerToolsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		orihttp.MethodNotAllowed(w)
		return
	}

	// Extract server name from path

	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 5 {
		orihttp.BadRequest(w, "Server name required")
		return
	}
	serverName := parts[4]

	server, err := h.registry.GetServer(serverName)
	if err != nil {
		orihttp.NotFound(w, err.Error())
		return
	}

	status := server.GetStatus()
	var startErr string
	if status == mcp.StatusStopped || status == mcp.StatusError {
		if status == mcp.StatusError {
			_ = h.registry.StopServer(serverName)
		}

		if err := h.registry.StartServer(serverName); err != nil {
			startErr = err.Error()
			logger.Warn("Failed to start MCP server while loading tools", logger.Fields{
				"server": serverName,
				"error":  err,
			})
		}
	}

	tools := server.GetTools()
	status = server.GetStatus()

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]any{
		"server":      serverName,
		"status":      status,
		"start_error": startErr,
		"tools":       tools,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// GetServerStatusHandler gets status for a specific server
// GET /api/mcp/servers/:name/status
func (h *Handler) GetServerStatusHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		orihttp.MethodNotAllowed(w)
		return
	}

	// Extract server name from path

	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 5 {
		orihttp.BadRequest(w, "Server name required")
		return
	}
	serverName := parts[4]

	status, err := h.registry.GetServerStatus(serverName)
	if err != nil {
		orihttp.NotFound(w, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]any{
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
		orihttp.MethodNotAllowed(w)
		return
	}

	// Extract server name from path

	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 5 {
		orihttp.BadRequest(w, "Server name required")
		return
	}
	serverName := parts[4]

	// Get server
	server, err := h.registry.GetServer(serverName)
	if err != nil {
		orihttp.NotFound(w, err.Error())
		return
	}

	// Check current status
	status := server.GetStatus()

	// If stopped, try to start temporarily for testing
	wasStarted := false
	if status == mcp.StatusStopped {
		if err := server.Start(); err != nil {
			w.Header().Set("Content-Type", "application/json")
			if encErr := json.NewEncoder(w).Encode(map[string]any{
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
	if encErr := json.NewEncoder(w).Encode(map[string]any{
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
		orihttp.MethodNotAllowed(w)
		return
	}

	// Extract server name from path

	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 5 {
		orihttp.BadRequest(w, "Server name required")
		return
	}
	serverName := parts[4]

	// Get server
	server, err := h.registry.GetServer(serverName)
	if err != nil {
		orihttp.NotFound(w, err.Error())
		return
	}

	// Restart the server (stops if running, then starts)
	if err := server.Restart(); err != nil {
		logger.Error("Failed to restart MCP server", logger.Fields{"server": serverName, "err": err})
		orihttp.InternalError(w, fmt.Sprintf("Failed to restart server: %v", err))
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
		orihttp.MethodNotAllowed(w)
		return
	}

	// Parse multipart form
	if !orihttp.ParseFormData(w, r) {
		return
	}

	// Get uploaded file
	file, _, err := r.FormFile("config_file")
	if err != nil {
		orihttp.BadRequest(w, "No file uploaded")
		return
	}
	defer func() { _ = file.Close() }()

	// Read file content
	var config struct {
		Servers []mcp.ServerConfig `json:"servers"`
	}
	if err := json.NewDecoder(file).Decode(&config); err != nil {
		orihttp.BadRequest(w, fmt.Sprintf("Invalid JSON format: %v", err))
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
	if encErr := json.NewEncoder(w).Encode(map[string]any{
		"added":  added,
		"errors": errors,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// SearchServersHandler returns filtered registry entries from the cache.
// GET /api/mcp/search?q=&category=&source=
func (h *Handler) SearchServersHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		orihttp.MethodNotAllowed(w)
		return
	}

	if h.regStore == nil {
		w.Header().Set("Content-Type", "application/json")
		if encErr := json.NewEncoder(w).Encode([]mcpregistry.RegistryEntry{}); encErr != nil {
			logger.Error("Failed to encode response", logger.Fields{"error": encErr})
		}
		return
	}

	// Refresh cache if stale.
	if !h.regStore.IsCacheValid() {
		sources := h.regStore.GetSources()
		entries := h.regFetcher.FetchAll(sources)
		if setErr := h.regStore.SetCache(entries); setErr != nil {
			logger.Warn("Failed to persist MCP registry cache", logger.Fields{"error": setErr})
		}
	}

	entries := h.regStore.GetCachedEntries()

	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	category := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("category")))
	source := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("source")))

	filtered := make([]mcpregistry.RegistryEntry, 0, len(entries))
	for _, e := range entries {
		if q != "" {
			haystack := strings.ToLower(e.Name + " " + e.Description + " " + e.Category + " " + strings.Join(e.Tags, " "))
			if !strings.Contains(haystack, q) {
				continue
			}
		}
		if category != "" && category != "all" && strings.ToLower(e.Category) != category {
			continue
		}
		if source != "" && source != "all" && strings.ToLower(e.Source) != source {
			continue
		}
		filtered = append(filtered, e)
	}

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(filtered); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// ListRegistrySourcesHandler returns all configured registry sources.
// GET /api/mcp/registry-sources
func (h *Handler) ListRegistrySourcesHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		orihttp.MethodNotAllowed(w)
		return
	}

	var sources []mcpregistry.RegistrySource
	if h.regStore != nil {
		sources = h.regStore.GetSources()
	}

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(sources); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// AddRegistrySourceHandler adds a new registry source.
// POST /api/mcp/registry-sources  body: {"name": "...", "url": "..."}
func (h *Handler) AddRegistrySourceHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}

	if h.regStore == nil {
		orihttp.InternalError(w, "Registry store not initialized")
		return
	}

	var req struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	if req.Name == "" || req.URL == "" {
		orihttp.BadRequest(w, "name and url are required")
		return
	}

	// Determine source type.
	sourceType := "url"
	if !strings.Contains(req.URL, "://") {
		// GitHub shorthand "user/repo"
		sourceType = "github"
	}

	src := mcpregistry.RegistrySource{
		ID:         uuid.New().String(),
		Name:       req.Name,
		URL:        req.URL,
		SourceType: sourceType,
		Enabled:    true,
		IsBuiltin:  false,
	}

	if err := h.regStore.AddSource(src); err != nil {
		orihttp.InternalError(w, err.Error())
		return
	}

	// Invalidate cache so next search re-fetches with the new source.
	h.regStore.InvalidateCache()

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(src); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// RegistrySourcesItemHandler handles DELETE /api/mcp/registry-sources/:id
func (h *Handler) RegistrySourcesItemHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		orihttp.MethodNotAllowed(w)
		return
	}

	if h.regStore == nil {
		orihttp.InternalError(w, "Registry store not initialized")
		return
	}

	// Extract ID from path: /api/mcp/registry-sources/ID
	parts := strings.Split(strings.TrimSuffix(r.URL.Path, "/"), "/")
	if len(parts) < 5 {
		orihttp.BadRequest(w, "Source ID required")
		return
	}
	id := parts[len(parts)-1]

	// Prevent deleting builtin sources.
	for _, src := range h.regStore.GetSources() {
		if src.ID == id && src.IsBuiltin {
			orihttp.BadRequest(w, "Cannot delete built-in registry sources")
			return
		}
	}

	if err := h.regStore.RemoveSource(id); err != nil {
		orihttp.InternalError(w, err.Error())
		return
	}

	h.regStore.InvalidateCache()

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]string{"status": "success"}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// RefreshRegistryHandler force-refreshes all registry sources, clearing the cache.
// POST /api/mcp/registry/refresh
func (h *Handler) RefreshRegistryHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}

	if h.regStore == nil {
		orihttp.InternalError(w, "Registry store not initialized")
		return
	}

	h.regStore.InvalidateCache()
	sources := h.regStore.GetSources()
	entries := h.regFetcher.FetchAll(sources)
	if err := h.regStore.SetCache(entries); err != nil {
		logger.Warn("Failed to persist refreshed MCP registry cache", logger.Fields{"error": err})
	}

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]any{
		"status": "success",
		"count":  len(entries),
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// GetMarketplaceServersHandler returns available MCP servers from marketplace
// GET /api/mcp/marketplace
func (h *Handler) GetMarketplaceServersHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		orihttp.MethodNotAllowed(w)
		return
	}

	// Return a curated list of well-known MCP servers.
	// For external registry integration, would need to:
	// - Define a registry API endpoint (e.g., GitHub raw JSON or custom API)
	// - Add caching with TTL to avoid excessive fetches
	// - Handle network errors gracefully with fallback to this static list
	marketplaceServers := []map[string]any{
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
		{
			"name":        "playwright",
			"description": "Browser automation and interactive control using Playwright MCP",
			"command":     "npx",
			"args":        []string{"-y", "@playwright/mcp"},
			"maintainer":  "Microsoft",
			"category":    "automation",
			"transport":   "stdio",
		},
	}

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]any{
		"servers": marketplaceServers,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}
