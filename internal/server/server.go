package server

import (
	"context"
	"log"

	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	agenthttp "github.com/johnjallday/ori-agent/internal/agenthttp"
	"github.com/johnjallday/ori-agent/internal/agentstudio"
	"github.com/johnjallday/ori-agent/internal/chathttp"
	"github.com/johnjallday/ori-agent/internal/client"
	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/devicehttp"
	"github.com/johnjallday/ori-agent/internal/healthhttp"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/location"
	"github.com/johnjallday/ori-agent/internal/locationhttp"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/marketplace"
	"github.com/johnjallday/ori-agent/internal/marketplacehttp"
	"github.com/johnjallday/ori-agent/internal/mcp"
	"github.com/johnjallday/ori-agent/internal/mcphttp"
	"github.com/johnjallday/ori-agent/internal/onboarding"
	"github.com/johnjallday/ori-agent/internal/onboardinghttp"
	"github.com/johnjallday/ori-agent/internal/orchestrationhttp"
	"github.com/johnjallday/ori-agent/internal/platform"
	"github.com/johnjallday/ori-agent/internal/plugindownloader"
	pluginhttp "github.com/johnjallday/ori-agent/internal/pluginhttp"
	"github.com/johnjallday/ori-agent/internal/pluginloader"
	"github.com/johnjallday/ori-agent/internal/pluginmanager"
	"github.com/johnjallday/ori-agent/internal/pluginupdate"
	"github.com/johnjallday/ori-agent/internal/registry"
	"github.com/johnjallday/ori-agent/internal/settingshttp"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/updatemanager"
	"github.com/johnjallday/ori-agent/internal/usagehttp"
	web "github.com/johnjallday/ori-agent/internal/web"
	"github.com/johnjallday/ori-agent/internal/workflowhttp"
)

// Server holds all the dependencies and state for the HTTP server
// Dependencies are organized into domain-specific facades to reduce coupling
type Server struct {
	// Domain facades (grouped dependencies) - PUBLIC API
	Core        *CoreSystemFacade
	Plugin      *PluginSystemFacade
	Storage     *StorageSystemFacade
	Workflow    *WorkflowSystemFacade
	Integration *IntegrationSystemFacade
	UI          *UISystemFacade

	// Internal fields (used by builder, kept for backward compatibility)
	// These are populated during initialization and wrapped in facades
	clientFactory       *client.Factory
	llmFactory          *llm.Factory
	registryManager     *registry.Manager
	st                  store.Store
	pluginReg           types.PluginRegistry
	agentStorePath      string
	configManager       *config.Manager
	templateRenderer    *web.TemplateRenderer
	pluginDownloader    *plugindownloader.PluginDownloader
	updateMgr           *updatemanager.Manager
	workspaceStore      agentstudio.Store
	taskExecutor        *agentstudio.TaskExecutor
	stepExecutor        *agentstudio.StepExecutor
	taskScheduler       *agentstudio.TaskScheduler
	eventBus            *agentstudio.EventBus
	notificationService *agentstudio.NotificationService
	studioOrchestrator  *agentstudio.Orchestrator
	costTracker         *llm.CostTracker
	mcpRegistry         *mcp.Registry
	mcpConfigManager    *mcp.ConfigManager
	locationManager     *location.Manager
	onboardingMgr       *onboarding.Manager
	categoryManager     *pluginmanager.CategoryManager
	permissionManager   *pluginmanager.PermissionManager
	versionManager      *pluginmanager.VersionManager
	notificationManager *pluginmanager.NotificationManager
	backupManager       *pluginmanager.BackupManager

	// HTTP Handlers (kept separate as they're endpoints, not core logic)
	healthManager         *healthhttp.Manager
	activityLogger        *agenthttp.ActivityLogger
	settingsHandler       *settingshttp.Handler
	chatHandler           *chathttp.Handler
	pluginHandler         *pluginhttp.Handler
	pluginRegistryHandler *pluginhttp.RegistryHandler
	pluginInitHandler     *pluginhttp.InitHandler
	healthHandler         *healthhttp.Handler
	pluginUpdateHandler   *pluginupdate.Handler
	onboardingHandler     *onboardinghttp.Handler
	deviceHandler         *devicehttp.Handler
	webPageHandler        *pluginhttp.WebPageHandler
	orchestrationHandler  *orchestrationhttp.Handler
	studioHandler         *agentstudio.HTTPHandler
	usageHandler          *usagehttp.Handler
	mcpHandler            *mcphttp.Handler
	agentMCPHandler       *agenthttp.MCPHandler
	locationHandler       *locationhttp.Handler
	pluginsPageHandler    *pluginhttp.PluginsPageHandler
	rollbackHandler       *pluginhttp.RollbackHandler
	permissionsHandler    *pluginhttp.PermissionsHandler
	backupHandler         *pluginhttp.BackupHandler
	notificationsHandler  *pluginhttp.NotificationsHandler
	workflowHandler       *workflowhttp.Handler
	marketplaceStore      *marketplace.Store
	marketplaceHandler    *marketplacehttp.Handler
}

// New creates and initializes a new Server with all dependencies using the ServerBuilder.
func New() (*Server, error) {
	builder, err := NewServerBuilder()
	if err != nil {
		return nil, err
	}
	return builder.Build()
}

// Handler returns the configured HTTP handler with all routes
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	registerRoutes(mux, s)
	return s.CORSMiddleware(mux)
}

// Start starts background services (task executor, etc.)
func (s *Server) Start() {
	if s.Workflow != nil {
		s.Workflow.Start()
	}
}

// Shutdown gracefully shuts down background services
func (s *Server) Shutdown() {
	// Stop background services
	if s.Workflow != nil {
		s.Workflow.Shutdown()
	}

	// Clean up all loaded plugins
	s.cleanupPlugins()
}

// cleanupPlugins closes all RPC plugin connections for all agents
func (s *Server) cleanupPlugins() {
	log.Println("Cleaning up plugins...")

	agentNames, _ := s.Storage.ListAgents()
	cleanedCount := 0

	for _, agentName := range agentNames {
		ag, ok := s.Storage.GetAgentByName(agentName)
		if !ok {
			continue
		}

		// Clean up each loaded plugin
		for pluginName, loadedPlugin := range ag.Plugins {
			if loadedPlugin.Tool != nil {
				pluginloader.CloseRPCPlugin(loadedPlugin.Tool)
				cleanedCount++
				logger.Debug("Closed plugin '' for agent ''", logger.Fields{"agent": pluginName, "agentName": agentName})
			}
		}
	}

	logger.Debug("Plugin cleanup complete: closed plugin(s)", logger.Fields{"plugin": cleanedCount})
}

// HTTPServer returns a fully configured http.Server
func (s *Server) HTTPServer(addr string) *http.Server {
	// Start background services
	s.Start()

	return &http.Server{
		Addr:              addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      120 * time.Second,
		IdleTimeout:       90 * time.Second,
	}
}

// prepareBasePageData prepares common page data with theme and current agent
func (s *Server) prepareBasePageData(pageName string) web.TemplateData {
	data := web.GetDefaultData()
	data.CurrentPage = pageName
	data.Theme = s.Storage.OnboardingMgr.GetTheme()

	if agents, current := s.Storage.ListAgents(); len(agents) > 0 {
		currentAgentName := current
		if currentAgentName == "" {
			currentAgentName = agents[0]
		}
		if agent, found := s.Storage.GetAgentByName(currentAgentName); found && agent != nil {
			data.CurrentAgent = currentAgentName
			if agent.Settings.Model != "" {
				data.Model = agent.Settings.Model
			}
		}
	}

	return data
}

// renderAndWritePage renders a template and writes the response
func (s *Server) renderAndWritePage(w http.ResponseWriter, templateName string, data web.TemplateData) {
	html, err := s.UI.TemplateRenderer.RenderTemplate(templateName, data)
	if err != nil {
		logger.Error("Failed to render template", logger.Fields{"template": templateName, "error": err})
		if err := orihttp.RespondInternalError(w, "Internal Server Error"); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	w.Header().Set("Content-Type", "text/html")
	if _, err := w.Write([]byte(html)); err != nil {
		logger.Error("Failed to write response", logger.Fields{"error": err})
	}
}

func (s *Server) serveIndex(w http.ResponseWriter, r *http.Request) {
	// Only handle root path, not other paths
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	data := s.prepareBasePageData("index")
	s.renderAndWritePage(w, "index", data)
}

func (s *Server) serveAgents(w http.ResponseWriter, r *http.Request) {
	data := s.prepareBasePageData("agents")
	data.ShowSidebarToggle = true // Enable sidebar toggle
	s.renderAndWritePage(w, "agents", data)
}

func (s *Server) serveAgentsDetail(w http.ResponseWriter, r *http.Request) {
	data := s.prepareBasePageData("agents")
	data.Title = "Agent Details - Ori Agent"
	data.BrandText = "Ori Agent"
	data.ShowSidebarToggle = true
	s.renderAndWritePage(w, "agents-detail", data)
}

func (s *Server) serveAgentsEdit(w http.ResponseWriter, r *http.Request) {
	data := s.prepareBasePageData("agents")
	data.Title = "Edit Agent - Ori Agent"
	data.BrandText = "Ori Agent"
	data.ShowSidebarToggle = true
	s.renderAndWritePage(w, "agents-edit", data)
}

func (s *Server) serveSettings(w http.ResponseWriter, r *http.Request) {
	data := s.prepareBasePageData("settings")

	// Add platform information for system info display
	currentPlatform := platform.DetectPlatform()
	currentPlatformDisplay := platform.GetPlatformDisplayName(currentPlatform)
	data.Extra = map[string]interface{}{
		"CurrentPlatform":        currentPlatform,
		"CurrentPlatformDisplay": currentPlatformDisplay,
	}

	s.renderAndWritePage(w, "settings", data)
}

func (s *Server) serveMCP(w http.ResponseWriter, r *http.Request) {
	data := s.prepareBasePageData("mcp")
	s.renderAndWritePage(w, "mcp", data)
}

func (s *Server) serveMarketplace(w http.ResponseWriter, r *http.Request) {
	data := s.prepareBasePageData("marketplace")
	data.ShowSidebarToggle = true

	// Add platform information for compatibility checking
	currentPlatform := platform.DetectPlatform()
	currentPlatformDisplay := platform.GetPlatformDisplayName(currentPlatform)
	data.Extra = map[string]interface{}{
		"CurrentPlatform":        currentPlatform,
		"CurrentPlatformDisplay": currentPlatformDisplay,
	}

	s.renderAndWritePage(w, "marketplace", data)
}

func (s *Server) servePlugins(w http.ResponseWriter, r *http.Request) {
	data := s.prepareBasePageData("plugins")
	data.ShowSidebarToggle = true
	s.renderAndWritePage(w, "plugins", data)
}

func (s *Server) serveModels(w http.ResponseWriter, r *http.Request) {
	data := s.prepareBasePageData("models")
	data.ShowSidebarToggle = true
	s.renderAndWritePage(w, "models", data)
}

func (s *Server) serveWorkflows(w http.ResponseWriter, r *http.Request) {
	data := s.prepareBasePageData("workflows")
	data.Title = "Workflow Templates - Ori Agent"
	data.BrandText = "Ori Agent"
	s.renderAndWritePage(w, "workflows", data)
}

func (s *Server) serveWorkspaces(w http.ResponseWriter, r *http.Request) {
	data := s.prepareBasePageData("workspaces")
	// Note: uses "studios" template
	html, err := s.UI.TemplateRenderer.RenderTemplate("studios", data)
	if err != nil {
		logger.Error("Failed to render studios template", logger.Fields{"error": err})
		if err := orihttp.RespondInternalError(w, "Internal Server Error"); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	w.Header().Set("Content-Type", "text/html")
	if _, err := w.Write([]byte(html)); err != nil {
		logger.Error("Failed to write response", logger.Fields{"response": err})
	}
}

// handleStudiosRoutes handles all /studios/* routes
func (s *Server) handleStudiosRoutes(w http.ResponseWriter, r *http.Request) {
	// Extract path after /studios/
	path := strings.TrimPrefix(r.URL.Path, "/studios/")
	if path == "" || path == r.URL.Path {
		// No ID provided, redirect to studios list
		http.Redirect(w, r, "/studios", http.StatusSeeOther)
		return
	}

	// Split path into segments
	parts := strings.Split(path, "/")
	workspaceID := parts[0]

	// Check if this is a canvas route: /studios/{id}/canvas
	if len(parts) == 2 && parts[1] == "canvas" {
		s.serveWorkspaceCanvas(w, r, workspaceID)
		return
	}

	// Otherwise, serve the workspace dashboard
	s.serveWorkspaceDashboard(w, r, workspaceID)
}

func (s *Server) serveWorkspaceDashboard(w http.ResponseWriter, r *http.Request, workspaceID string) {
	data := s.prepareBasePageData("workspaces")
	data.Title = "Workspace Dashboard - Ori Agent"
	data.BrandText = "Ori Agent"
	data.ShowSidebarToggle = true
	data.Extra = map[string]interface{}{
		"WorkspaceID": workspaceID,
	}
	s.renderAndWritePage(w, "workspace-dashboard", data)
}

func (s *Server) serveWorkspaceCanvas(w http.ResponseWriter, r *http.Request, workspaceID string) {
	data := s.prepareBasePageData("workspaces")
	data.Title = "Workspace Canvas - Ori Agent"
	data.BrandText = "Ori Agent"
	data.ShowSidebarToggle = true
	data.Extra = map[string]interface{}{
		"WorkspaceID": workspaceID,
	}
	s.renderAndWritePage(w, "workspace-canvas", data)
}

func (s *Server) serveUsage(w http.ResponseWriter, r *http.Request) {
	data := s.prepareBasePageData("usage")
	data.Title = "Usage & Cost Tracking - Ori Agent"
	data.BrandText = "Ori Agent"
	s.renderAndWritePage(w, "usage", data)
}

func (s *Server) serveStaticFile(w http.ResponseWriter, r *http.Request) {
	path := "static" + r.URL.Path

	content, err := web.Static.ReadFile(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	switch {
	case strings.HasSuffix(path, ".css"):
		w.Header().Set("Content-Type", "text/css")
	case strings.HasSuffix(path, ".js"):
		w.Header().Set("Content-Type", "application/javascript")
	case strings.HasSuffix(path, ".svg"):
		w.Header().Set("Content-Type", "image/svg+xml")
	case strings.HasSuffix(path, ".html"):
		w.Header().Set("Content-Type", "text/html")
	case strings.HasSuffix(path, ".json"):
		w.Header().Set("Content-Type", "application/json")
	default:
		w.Header().Set("Content-Type", "application/octet-stream")
	}

	// Prevent browsers from caching embedded static assets during local dev.
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")

	if _, err := w.Write(content); err != nil {

		logger.Error("Failed to write response", logger.Fields{"response": err})

	}
}

func (s *Server) serveFavicon(w http.ResponseWriter, r *http.Request) {
	content, err := web.Static.ReadFile("static/favicon.svg")
	if err != nil {
		// Fallback to filesystem for local dev scenarios where embedded assets
		// might not include the favicon.
		content, err = os.ReadFile("assets/favicon.svg")
		if err != nil {
			logger.Error("Failed to read favicon", logger.Fields{"err": err})
			http.NotFound(w, r)
			return
		}
	}

	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=86400") // Cache for 1 day

	if _, err := w.Write(content); err != nil {
		logger.Error("Failed to write favicon response", logger.Fields{"response": err})
	}
}

func (s *Server) serveAgentFiles(w http.ResponseWriter, r *http.Request) {
	// Redirect /agents/ to /agents (agents dashboard)
	if r.URL.Path == "/agents/" {
		http.Redirect(w, r, "/agents", http.StatusMovedPermanently)
		return
	}

	// Serve files from the agents directory
	// URL format: /agents/<agent-name>/agent_settings.json
	path := strings.TrimPrefix(r.URL.Path, "/")

	// Security: prevent directory traversal
	// Clean the path and validate it's within the agents directory
	cleanPath := filepath.Clean(path)

	// Ensure path doesn't contain traversal sequences
	if strings.Contains(cleanPath, "..") {
		if err := orihttp.RespondBadRequest(w, "Invalid path"); err != nil {
			logger.

				// Verify path starts with "agents/" to prevent access to other directories
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	if !strings.HasPrefix(cleanPath, "agents/") && !strings.HasPrefix(cleanPath, "agents\\") {
		if err := orihttp.RespondBadRequest(w, "Invalid path"); err != nil {
			logger.

				// Resolve to absolute path and verify it's still within the agents directory
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	absPath, err := filepath.Abs(cleanPath)
	if err != nil {
		if err := orihttp.RespondBadRequest(w, "Invalid path"); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	agentsDir, err := filepath.Abs("agents")
	if err != nil {
		if err := orihttp.RespondInternalError(w, "Internal server error"); err != nil {
			logger.

				// Final check: ensure resolved path is within agents directory
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	if !strings.HasPrefix(absPath, agentsDir+string(filepath.Separator)) {
		if err := orihttp.RespondBadRequest(w, "Invalid path"); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	content, err := os.ReadFile(cleanPath)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	// Set content type based on file extension
	if strings.HasSuffix(path, ".json") {
		w.Header().Set("Content-Type", "application/json")
	} else {
		w.Header().Set("Content-Type", "application/octet-stream")
	}

	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")

	if _, err := w.Write(content); err != nil {

		logger.Error("Failed to write response", logger.Fields{"response": err})

	}
}

// truncateString truncates a string to a maximum length with ellipsis
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}

// HTTPServerWrapper wraps http.Server to provide graceful shutdown capabilities
type HTTPServerWrapper struct {
	Server *http.Server
}

// Shutdown gracefully shuts down the HTTP server
func (w *HTTPServerWrapper) Shutdown(ctx context.Context) error {
	if w.Server == nil {
		return nil
	}
	return w.Server.Shutdown(ctx)
}
