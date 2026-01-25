package server

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	agenthttp "github.com/johnjallday/ori-agent/internal/agenthttp"
	"github.com/johnjallday/ori-agent/internal/chathttp"
	"github.com/johnjallday/ori-agent/internal/client"
	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/devicehttp"
	"github.com/johnjallday/ori-agent/internal/externalagents"
	"github.com/johnjallday/ori-agent/internal/externalagentshttp"
	"github.com/johnjallday/ori-agent/internal/fileshttp"
	"github.com/johnjallday/ori-agent/internal/filewatcher"
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
	"github.com/johnjallday/ori-agent/internal/modelcategoryhttp"
	"github.com/johnjallday/ori-agent/internal/notehttp"
	"github.com/johnjallday/ori-agent/internal/onboarding"
	"github.com/johnjallday/ori-agent/internal/onboardinghttp"
	"github.com/johnjallday/ori-agent/internal/orchestrationhttp"
	"github.com/johnjallday/ori-agent/internal/platform"
	"github.com/johnjallday/ori-agent/internal/plugindownloader"
	pluginhttp "github.com/johnjallday/ori-agent/internal/pluginhttp"
	"github.com/johnjallday/ori-agent/internal/pluginloader"
	"github.com/johnjallday/ori-agent/internal/pluginmanager"
	"github.com/johnjallday/ori-agent/internal/pluginupdate"
	"github.com/johnjallday/ori-agent/internal/pluginupdateservice"
	"github.com/johnjallday/ori-agent/internal/privateservices"
	"github.com/johnjallday/ori-agent/internal/registry"
	"github.com/johnjallday/ori-agent/internal/reviewhttp"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/sessionfiles"
	"github.com/johnjallday/ori-agent/internal/sessionhttp"
	"github.com/johnjallday/ori-agent/internal/settingshttp"
	"github.com/johnjallday/ori-agent/internal/skills"
	"github.com/johnjallday/ori-agent/internal/skillshttp"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/updatemanager"
	"github.com/johnjallday/ori-agent/internal/usagehttp"
	web "github.com/johnjallday/ori-agent/internal/web"
	"github.com/johnjallday/ori-agent/internal/workflowhttp"
	"github.com/johnjallday/ori-agent/internal/workspace"
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
	clientFactory         *client.Factory
	llmFactory            *llm.Factory
	registryManager       *registry.Manager
	st                    store.Store
	pluginReg             types.PluginRegistry
	agentStorePath        string
	configManager         *config.Manager
	privateServicesClient privateservices.Client
	templateRenderer      *web.TemplateRenderer
	pluginDownloader      *plugindownloader.PluginDownloader
	updateMgr             *updatemanager.Manager
	workspaceStore        workspace.Store
	taskExecutor          *workspace.TaskExecutor
	stepExecutor          *workspace.StepExecutor
	taskScheduler         *workspace.TaskScheduler
	eventBus              *workspace.EventBus
	notificationService   *workspace.NotificationService
	studioOrchestrator    *workspace.Orchestrator
	costTracker           *llm.CostTracker
	mcpRegistry           *mcp.Registry
	mcpConfigManager      *mcp.ConfigManager
	locationManager       *location.Manager
	onboardingMgr         *onboarding.Manager
	categoryManager       *pluginmanager.CategoryManager
	permissionManager     *pluginmanager.PermissionManager
	notificationManager   *pluginmanager.NotificationManager
	backupManager         *pluginmanager.BackupManager
	pluginUpdateService   *pluginupdateservice.Service

	// HTTP Handlers (kept separate as they're endpoints, not core logic)
	healthManager          *healthhttp.Manager
	activityLogger         *agenthttp.ActivityLogger
	settingsHandler        *settingshttp.Handler
	chatHandler            *chathttp.Handler
	pluginHandler          *pluginhttp.Handler
	pluginRegistryHandler  *pluginhttp.RegistryHandler
	pluginInitHandler      *pluginhttp.InitHandler
	healthHandler          *healthhttp.Handler
	pluginUpdateHandler    *pluginupdate.Handler
	onboardingHandler      *onboardinghttp.Handler
	deviceHandler          *devicehttp.Handler
	webPageHandler         *pluginhttp.WebPageHandler
	orchestrationHandler   *orchestrationhttp.Handler
	autoTaskHandler        *orchestrationhttp.AutoTaskHandler
	studioHandler          *workspace.HTTPHandler
	usageHandler           *usagehttp.Handler
	mcpHandler             *mcphttp.Handler
	agentMCPHandler        *agenthttp.MCPHandler
	locationHandler        *locationhttp.Handler
	pluginsPageHandler     *pluginhttp.PluginsPageHandler
	permissionsHandler     *pluginhttp.PermissionsHandler
	backupHandler          *pluginhttp.BackupHandler
	notificationsHandler   *pluginhttp.NotificationsHandler
	workflowHandler        *workflowhttp.Handler
	marketplaceStore       *marketplace.Store
	marketplaceHandler     *marketplacehttp.Handler
	modelCategoryStore     store.ModelCategoryStore
	modelCategoryHandler   *modelcategoryhttp.Handler
	autoCategorizeHandler  *modelcategoryhttp.AutoCategorizeHandler
	resetHandler           *settingshttp.ResetHandler
	autoConfigHandler      *agenthttp.AutoConfigHandler
	smartOnboardingHandler *onboardinghttp.SmartOnboardingHandler

	// Session management
	sessionStore        session.HybridStore
	sessionHandler      *sessionhttp.Handler
	autoClassifyHandler *sessionhttp.AutoClassifyHandler
	smartInputHandler   *sessionhttp.SmartInputHandler

	// Note generation
	noteHandler *notehttp.Handler

	// Session files management
	sessionFilesStore   *sessionfiles.Store
	sessionFilesWatcher *filewatcher.Watcher
	sessionFilesHandler *fileshttp.Handler

	// Review system
	reviewHandler *reviewhttp.Handler

	// External agents (Claude Code, Codex)
	externalAgentsCache   *externalagents.Cache
	externalAgentsHandler *externalagentshttp.Handler

	// Skills (local + external)
	skillsManager *skills.Manager
	skillsHandler *skillshttp.Handler
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

	// Apply middleware chain: SecurityHeaders -> ErrorRecovery -> CORS -> routes
	handler := orihttp.Chain(
		orihttp.SecurityHeaders(),
		orihttp.ErrorRecovery(),
	)(s.CORSMiddleware(mux))

	return handler
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
	if s.pluginUpdateService != nil {
		s.pluginUpdateService.Stop()
	}
	if s.sessionFilesWatcher != nil {
		_ = s.sessionFilesWatcher.Close()
	}

	// Shutdown folder picker if running
	workspace.ShutdownFolderPicker()

	// Clean up all loaded plugins
	s.cleanupPlugins()
}

// cleanupPlugins closes all RPC plugin connections for all agents
func (s *Server) cleanupPlugins() {
	logger.Debug("Cleaning up plugins", logger.Fields{})

	agentNames, _ := s.Storage.ListAgents()
	cleanedCount := 0
	errorCount := 0

	for _, agentName := range agentNames {
		ag, ok := s.Storage.GetAgentByName(agentName)
		if !ok {
			continue
		}

		// Clean up each loaded plugin with panic recovery
		// This ensures one plugin failure doesn't prevent cleanup of others
		for pluginName, loadedPlugin := range ag.Plugins {
			if loadedPlugin.Tool != nil {
				func() {
					defer func() {
						if r := recover(); r != nil {
							errorCount++
							logger.Error("Panic during plugin cleanup", logger.Fields{
								"plugin": pluginName,
								"agent":  agentName,
								"error":  r,
							})
						}
					}()
					pluginloader.CloseRPCPlugin(loadedPlugin.Tool)
					cleanedCount++
					logger.Debug("Closed plugin for agent", logger.Fields{"plugin": pluginName, "agent": agentName})
				}()
			}
		}
	}

	logger.Debug("Plugin cleanup complete", logger.Fields{"closed": cleanedCount, "errors": errorCount})
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
		WriteTimeout:      6 * time.Minute,
		IdleTimeout:       90 * time.Second,
	}
}

func (s *Server) privateCapabilitiesSnapshot() privateservices.Capabilities {
	if s.privateServicesClient == nil {
		return privateservices.NoopClient{}.Capabilities()
	}
	return s.privateServicesClient.Capabilities()
}

// prepareBasePageData prepares common page data with theme and current agent
func (s *Server) prepareBasePageData(pageName string) web.TemplateData {
	data := web.GetDefaultData()
	data.CurrentPage = pageName
	data.Theme = s.Storage.OnboardingMgr.GetTheme()

	caps := s.privateCapabilitiesSnapshot()
	data.Extra["Web3Enabled"] = caps.Web3Wallet
	data.Extra["MarketplacePaymentsEnabled"] = caps.MarketplacePayments
	data.Extra["TokenPayoutsEnabled"] = caps.TokenPayouts

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
		orihttp.InternalError(w, "Internal Server Error")
		return
	}
	orihttp.WriteHTML(w, html)
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

func (s *Server) serveSkills(w http.ResponseWriter, r *http.Request) {
	data := s.prepareBasePageData("skills")
	data.Title = "Skills - Ori Agent"
	data.BrandText = "Ori Agent"
	data.ShowSidebarToggle = true
	s.renderAndWritePage(w, "skills", data)
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
	// Redirect to home page - workspace hub is now on the home page
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// handleWorkspacesRoutes handles all /workspaces/* routes
func (s *Server) handleWorkspacesRoutes(w http.ResponseWriter, r *http.Request) {
	// Extract path after /workspaces/
	path := strings.TrimPrefix(r.URL.Path, "/workspaces/")
	if path == "" || path == r.URL.Path {
		// No ID provided, redirect to home page
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	// Split path into segments
	parts := strings.Split(path, "/")
	workspaceID := parts[0]

	// Check if this is a canvas route: /workspaces/{id}/canvas
	if len(parts) == 2 && parts[1] == "canvas" {
		s.serveWorkspaceCanvas(w, r, workspaceID)
		return
	}

	// If just /workspaces/{id}, serve the workspace detail page
	if len(parts) == 1 {
		s.serveWorkspaceDetail(w, r, workspaceID)
		return
	}

	// Otherwise, redirect to home page
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) serveWorkspaceDetail(w http.ResponseWriter, r *http.Request, workspaceID string) {
	data := s.prepareBasePageData("workspaces")
	data.Title = "Workspace - Ori Agent"
	data.BrandText = "Ori Agent"
	data.ShowSidebarToggle = true
	data.Extra = map[string]interface{}{
		"WorkspaceID": workspaceID,
	}
	s.renderAndWritePage(w, "workspace-detail", data)
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

func (s *Server) serveReview(w http.ResponseWriter, r *http.Request) {
	data := s.prepareBasePageData("review")
	data.Title = "Conversation Review - Ori Agent"
	data.BrandText = "Ori Agent"
	s.renderAndWritePage(w, "review", data)
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
	orihttp.WriteBytes(w, content)
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
	orihttp.WriteBytes(w, content)
}

func (s *Server) serveAgentFiles(w http.ResponseWriter, r *http.Request) {
	// Redirect /agents/ to /agents (agents dashboard)
	if r.URL.Path == "/agents/" {
		http.Redirect(w, r, "/agents", http.StatusMovedPermanently)
		return
	}

	// Check if this is a clean agent detail URL: /agents/{agent-name}
	// (not a file request like /agents/{agent-name}/config.json)
	pathAfterAgents := strings.TrimPrefix(r.URL.Path, "/agents/")
	if !strings.Contains(pathAfterAgents, "/") && !strings.Contains(pathAfterAgents, ".") && pathAfterAgents != "" {
		// This is a request for /agents/{agent-name} - serve the agent detail page
		s.serveAgentsDetail(w, r)
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
		orihttp.BadRequest(w, "Invalid path")
		// Verify path starts with "agents/" to prevent access to other directories
		return
	}

	if !strings.HasPrefix(cleanPath, "agents/") && !strings.HasPrefix(cleanPath, "agents\\") {
		orihttp.BadRequest(w, "Invalid path")
		// Resolve to absolute path and verify it's still within the agents directory
		return
	}

	absPath, err := filepath.Abs(cleanPath)
	if err != nil {
		orihttp.BadRequest(w, "Invalid path")
		return
	}

	agentsDir, err := filepath.Abs("agents")
	if err != nil {
		orihttp.InternalError(w, "Internal server error")
		// Final check: ensure resolved path is within agents directory
		return
	}

	if !strings.HasPrefix(absPath, agentsDir+string(filepath.Separator)) {
		orihttp.BadRequest(w, "Invalid path")
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
	orihttp.WriteBytes(w, content)
}

// serveAvatarFiles serves agent avatar images from the agent_avatars directory
func (s *Server) serveAvatarFiles(w http.ResponseWriter, r *http.Request) {
	// Extract the filename from the path
	filename := strings.TrimPrefix(r.URL.Path, "/avatars/")
	if filename == "" || filename == r.URL.Path {
		http.NotFound(w, r)
		return
	}

	// Security: prevent directory traversal
	filename = filepath.Clean(filename)
	if strings.Contains(filename, "..") || strings.Contains(filename, "/") || strings.Contains(filename, "\\") {
		orihttp.BadRequest(w, "Invalid path")
		return
	}

	// Build the full path
	avatarPath := filepath.Join("agent_avatars", filename)

	// Verify the file exists and is within the avatar directory
	absPath, err := filepath.Abs(avatarPath)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	avatarsDir, err := filepath.Abs("agent_avatars")
	if err != nil {
		orihttp.InternalError(w, "Internal server error")
		return
	}

	if !strings.HasPrefix(absPath, avatarsDir+string(filepath.Separator)) {
		orihttp.BadRequest(w, "Invalid path")
		return
	}

	// Read the file
	content, err := os.ReadFile(avatarPath)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	// Set content type based on extension
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".png":
		w.Header().Set("Content-Type", "image/png")
	case ".jpg", ".jpeg":
		w.Header().Set("Content-Type", "image/jpeg")
	case ".gif":
		w.Header().Set("Content-Type", "image/gif")
	case ".webp":
		w.Header().Set("Content-Type", "image/webp")
	default:
		w.Header().Set("Content-Type", "application/octet-stream")
	}

	// Cache avatars for 1 hour
	w.Header().Set("Cache-Control", "public, max-age=3600")
	orihttp.WriteBytes(w, content)
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
