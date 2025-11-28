package server

import (
	"context"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	agenthttp "github.com/johnjallday/ori-agent/internal/agenthttp"
	"github.com/johnjallday/ori-agent/internal/agentstudio"
	"github.com/johnjallday/ori-agent/internal/chathttp"
	"github.com/johnjallday/ori-agent/internal/client"
	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/devicehttp"
	"github.com/johnjallday/ori-agent/internal/healthhttp"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/location"
	"github.com/johnjallday/ori-agent/internal/locationhttp"
	"github.com/johnjallday/ori-agent/internal/mcp"
	"github.com/johnjallday/ori-agent/internal/mcphttp"
	"github.com/johnjallday/ori-agent/internal/onboarding"
	"github.com/johnjallday/ori-agent/internal/onboardinghttp"
	"github.com/johnjallday/ori-agent/internal/orchestrationhttp"
	"github.com/johnjallday/ori-agent/internal/platform"
	"github.com/johnjallday/ori-agent/internal/plugindownloader"
	pluginhttp "github.com/johnjallday/ori-agent/internal/pluginhttp"
	"github.com/johnjallday/ori-agent/internal/pluginloader"
	"github.com/johnjallday/ori-agent/internal/pluginupdate"
	"github.com/johnjallday/ori-agent/internal/registry"
	"github.com/johnjallday/ori-agent/internal/settingshttp"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/updatemanager"
	"github.com/johnjallday/ori-agent/internal/usagehttp"
	web "github.com/johnjallday/ori-agent/internal/web"
)

// Server holds all the dependencies and state for the HTTP server
type Server struct {
	clientFactory         *client.Factory
	llmFactory            *llm.Factory
	registryManager       *registry.Manager
	st                    store.Store
	pluginReg             types.PluginRegistry
	agentStorePath        string
	configManager         *config.Manager
	templateRenderer      *web.TemplateRenderer
	pluginDownloader      *plugindownloader.PluginDownloader
	updateMgr             *updatemanager.Manager
	healthManager         *healthhttp.Manager
	activityLogger        *agenthttp.ActivityLogger
	settingsHandler       *settingshttp.Handler
	chatHandler           *chathttp.Handler
	pluginHandler         *pluginhttp.Handler
	pluginRegistryHandler *pluginhttp.RegistryHandler
	pluginInitHandler     *pluginhttp.InitHandler
	healthHandler         *healthhttp.Handler
	pluginUpdateHandler   *pluginupdate.Handler
	onboardingMgr         *onboarding.Manager
	onboardingHandler     *onboardinghttp.Handler
	deviceHandler         *devicehttp.Handler
	webPageHandler        *pluginhttp.WebPageHandler
	workspaceStore        agentstudio.Store
	taskExecutor          *agentstudio.TaskExecutor
	stepExecutor          *agentstudio.StepExecutor
	taskScheduler         *agentstudio.TaskScheduler
	eventBus              *agentstudio.EventBus
	notificationService   *agentstudio.NotificationService
	orchestrationHandler  *orchestrationhttp.Handler
	studioOrchestrator    *agentstudio.Orchestrator
	studioHandler         *agentstudio.HTTPHandler
	costTracker           *llm.CostTracker
	usageHandler          *usagehttp.Handler
	mcpRegistry           *mcp.Registry
	mcpConfigManager      *mcp.ConfigManager
	mcpHandler            *mcphttp.Handler
	agentMCPHandler       *agenthttp.MCPHandler
	locationManager       *location.Manager
	locationHandler       *locationhttp.Handler
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
	if s.taskExecutor != nil {
		s.taskExecutor.Start()
	}
	if s.stepExecutor != nil {
		s.stepExecutor.Start()
	}
	if s.taskScheduler != nil {
		s.taskScheduler.Start()
	}
}

// Shutdown gracefully shuts down background services
func (s *Server) Shutdown() {
	// Stop background services
	if s.taskExecutor != nil {
		s.taskExecutor.Stop()
	}
	if s.stepExecutor != nil {
		s.stepExecutor.Stop()
	}
	if s.taskScheduler != nil {
		s.taskScheduler.Stop()
	}
	if s.notificationService != nil {
		s.notificationService.Shutdown()
	}
	if s.eventBus != nil {
		s.eventBus.Shutdown()
	}

	// Clean up all loaded plugins
	s.cleanupPlugins()
}

// cleanupPlugins closes all RPC plugin connections for all agents
func (s *Server) cleanupPlugins() {
	log.Println("Cleaning up plugins...")

	agentNames, _ := s.st.ListAgents()
	cleanedCount := 0

	for _, agentName := range agentNames {
		ag, ok := s.st.GetAgent(agentName)
		if !ok {
			continue
		}

		// Clean up each loaded plugin
		for pluginName, loadedPlugin := range ag.Plugins {
			if loadedPlugin.Tool != nil {
				pluginloader.CloseRPCPlugin(loadedPlugin.Tool)
				cleanedCount++
				log.Printf("Closed plugin '%s' for agent '%s'", pluginName, agentName)
			}
		}
	}

	log.Printf("Plugin cleanup complete: closed %d plugin(s)", cleanedCount)
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

func (s *Server) serveIndex(w http.ResponseWriter, r *http.Request) {
	// Only handle root path, not other paths
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	data := web.GetDefaultData()
	data.CurrentPage = "index"

	// Get theme from app state
	data.Theme = s.onboardingMgr.GetTheme()

	if agents, current := s.st.ListAgents(); len(agents) > 0 {
		currentAgentName := current
		if currentAgentName == "" {
			currentAgentName = agents[0]
		}
		if agent, found := s.st.GetAgent(currentAgentName); found && agent != nil {
			data.CurrentAgent = currentAgentName
			if agent.Settings.Model != "" {
				data.Model = agent.Settings.Model
			}
		}
	}

	html, err := s.templateRenderer.RenderTemplate("index", data)
	if err != nil {
		log.Printf("Failed to render template: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html")
	if _, err := w.Write([]byte(html)); err != nil {
		log.Printf("Failed to write response: %v", err)
	}
}

func (s *Server) serveAgents(w http.ResponseWriter, r *http.Request) {
	data := web.GetDefaultData()
	data.CurrentPage = "agents"
	data.ShowSidebarToggle = true // Enable sidebar toggle

	// Get theme from app state
	data.Theme = s.onboardingMgr.GetTheme()

	if agents, current := s.st.ListAgents(); len(agents) > 0 {
		currentAgentName := current
		if currentAgentName == "" {
			currentAgentName = agents[0]
		}
		if agent, found := s.st.GetAgent(currentAgentName); found && agent != nil {
			data.CurrentAgent = currentAgentName
			if agent.Settings.Model != "" {
				data.Model = agent.Settings.Model
			}
		}
	}

	html, err := s.templateRenderer.RenderTemplate("agents", data)
	if err != nil {
		log.Printf("Failed to render agents template: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html")
	if _, err := w.Write([]byte(html)); err != nil {
		log.Printf("Failed to write response: %v", err)
	}
}

func (s *Server) serveSettings(w http.ResponseWriter, r *http.Request) {
	data := web.GetDefaultData()
	data.CurrentPage = "settings"

	// Get theme from app state
	data.Theme = s.onboardingMgr.GetTheme()

	if agents, current := s.st.ListAgents(); len(agents) > 0 {
		currentAgentName := current
		if currentAgentName == "" {
			currentAgentName = agents[0]
		}
		if agent, found := s.st.GetAgent(currentAgentName); found && agent != nil {
			data.CurrentAgent = currentAgentName
			if agent.Settings.Model != "" {
				data.Model = agent.Settings.Model
			}
		}
	}

	// Add platform information for system info display
	currentPlatform := platform.DetectPlatform()
	currentPlatformDisplay := platform.GetPlatformDisplayName(currentPlatform)
	data.Extra = map[string]interface{}{
		"CurrentPlatform":        currentPlatform,
		"CurrentPlatformDisplay": currentPlatformDisplay,
	}

	html, err := s.templateRenderer.RenderTemplate("settings", data)
	if err != nil {
		log.Printf("Failed to render settings template: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html")
	if _, err := w.Write([]byte(html)); err != nil {
		log.Printf("Failed to write response: %v", err)
	}
}

func (s *Server) serveMCP(w http.ResponseWriter, r *http.Request) {
	data := web.GetDefaultData()
	data.CurrentPage = "mcp"

	// Get theme from app state
	data.Theme = s.onboardingMgr.GetTheme()

	if agents, current := s.st.ListAgents(); len(agents) > 0 {
		currentAgentName := current
		if currentAgentName == "" {
			currentAgentName = agents[0]
		}
		if agent, found := s.st.GetAgent(currentAgentName); found && agent != nil {
			data.CurrentAgent = currentAgentName
			if agent.Settings.Model != "" {
				data.Model = agent.Settings.Model
			}
		}
	}

	html, err := s.templateRenderer.RenderTemplate("mcp", data)
	if err != nil {
		log.Printf("Failed to render mcp template: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html")
	if _, err := w.Write([]byte(html)); err != nil {
		log.Printf("Failed to write response: %v", err)
	}
}

func (s *Server) serveMarketplace(w http.ResponseWriter, r *http.Request) {
	data := web.GetDefaultData()
	data.CurrentPage = "marketplace"
	data.ShowSidebarToggle = true // Enable sidebar toggle

	// Get theme from app state
	data.Theme = s.onboardingMgr.GetTheme()

	if agents, current := s.st.ListAgents(); len(agents) > 0 {
		currentAgentName := current
		if currentAgentName == "" {
			currentAgentName = agents[0]
		}
		data.CurrentAgent = currentAgentName
	}

	// Add platform information for compatibility checking
	currentPlatform := platform.DetectPlatform()
	currentPlatformDisplay := platform.GetPlatformDisplayName(currentPlatform)
	data.Extra = map[string]interface{}{
		"CurrentPlatform":        currentPlatform,
		"CurrentPlatformDisplay": currentPlatformDisplay,
	}

	html, err := s.templateRenderer.RenderTemplate("marketplace", data)
	if err != nil {
		log.Printf("Failed to render marketplace template: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html")
	if _, err := w.Write([]byte(html)); err != nil {
		log.Printf("Failed to write response: %v", err)
	}
}

func (s *Server) serveModels(w http.ResponseWriter, r *http.Request) {
	data := web.GetDefaultData()
	data.Title = "Ori Agent"
	data.CurrentPage = "models"
	data.ShowSidebarToggle = true // Enable sidebar toggle

	// Get theme from app state
	data.Theme = s.onboardingMgr.GetTheme()

	if agents, current := s.st.ListAgents(); len(agents) > 0 {
		currentAgentName := current
		if currentAgentName == "" {
			currentAgentName = agents[0]
		}
		data.CurrentAgent = currentAgentName
	}

	html, err := s.templateRenderer.RenderTemplate("models", data)
	if err != nil {
		log.Printf("Failed to render models template: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html")
	if _, err := w.Write([]byte(html)); err != nil {
		log.Printf("Failed to write response: %v", err)
	}
}

func (s *Server) serveWorkflows(w http.ResponseWriter, r *http.Request) {
	data := web.GetDefaultData()
	data.Title = "Workflow Templates - Ori Agent"
	data.CurrentPage = "workflows"

	// Get theme from app state
	data.Theme = s.onboardingMgr.GetTheme()

	if agents, current := s.st.ListAgents(); len(agents) > 0 {
		currentAgentName := current
		if currentAgentName == "" {
			currentAgentName = agents[0]
		}
		if agent, found := s.st.GetAgent(currentAgentName); found && agent != nil {
			data.CurrentAgent = currentAgentName
			if agent.Settings.Model != "" {
				data.Model = agent.Settings.Model
			}
		}
	}

	html, err := s.templateRenderer.RenderTemplate("workflows", data)
	if err != nil {
		log.Printf("Failed to render workflows template: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html")
	if _, err := w.Write([]byte(html)); err != nil {
		log.Printf("Failed to write response: %v", err)
	}
}

func (s *Server) serveWorkspaces(w http.ResponseWriter, r *http.Request) {
	data := web.GetDefaultData()
	data.Title = "Ori Agent"
	data.CurrentPage = "workspaces"

	// Get theme from app state
	data.Theme = s.onboardingMgr.GetTheme()

	if agents, current := s.st.ListAgents(); len(agents) > 0 {
		currentAgentName := current
		if currentAgentName == "" {
			currentAgentName = agents[0]
		}
		if agent, found := s.st.GetAgent(currentAgentName); found && agent != nil {
			data.CurrentAgent = currentAgentName
			if agent.Settings.Model != "" {
				data.Model = agent.Settings.Model
			}
		}
	}

	html, err := s.templateRenderer.RenderTemplate("studios", data)
	if err != nil {
		log.Printf("Failed to render studios template: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html")
	if _, err := w.Write([]byte(html)); err != nil {
		log.Printf("Failed to write response: %v", err)
	}
}

func (s *Server) serveWorkspaceDashboard(w http.ResponseWriter, r *http.Request) {
	// Extract workspace ID from URL path
	// URL format: /studios/:workspaceId
	path := strings.TrimPrefix(r.URL.Path, "/studios/")
	if path == "" || path == r.URL.Path {
		// No ID provided, redirect to studios list
		http.Redirect(w, r, "/studios", http.StatusSeeOther)
		return
	}

	workspaceID := path

	data := web.GetDefaultData()
	data.Title = "Workspace Dashboard - Ori Agent"
	data.CurrentPage = "workspaces"
	data.ShowSidebarToggle = true // Workspace dashboard has a sidebar

	// Add workspace ID to template data
	data.Extra = map[string]interface{}{
		"WorkspaceID": workspaceID,
	}

	// Get theme from app state
	data.Theme = s.onboardingMgr.GetTheme()

	if agents, current := s.st.ListAgents(); len(agents) > 0 {
		currentAgentName := current
		if currentAgentName == "" {
			currentAgentName = agents[0]
		}
		if agent, found := s.st.GetAgent(currentAgentName); found && agent != nil {
			data.CurrentAgent = currentAgentName
			if agent.Settings.Model != "" {
				data.Model = agent.Settings.Model
			}
		}
	}

	html, err := s.templateRenderer.RenderTemplate("workspace-dashboard", data)
	if err != nil {
		log.Printf("Failed to render workspace dashboard template: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html")
	if _, err := w.Write([]byte(html)); err != nil {
		log.Printf("Failed to write response: %v", err)
	}
}

func (s *Server) serveUsage(w http.ResponseWriter, r *http.Request) {
	data := web.GetDefaultData()
	data.Title = "Usage & Cost Tracking - Ori Agent"
	data.CurrentPage = "usage"

	// Get theme from app state
	data.Theme = s.onboardingMgr.GetTheme()

	if agents, current := s.st.ListAgents(); len(agents) > 0 {
		currentAgentName := current
		if currentAgentName == "" {
			currentAgentName = agents[0]
		}
		if agent, found := s.st.GetAgent(currentAgentName); found && agent != nil {
			data.CurrentAgent = currentAgentName
			if agent.Settings.Model != "" {
				data.Model = agent.Settings.Model
			}
		}
	}

	html, err := s.templateRenderer.RenderTemplate("usage", data)
	if err != nil {
		log.Printf("Failed to render usage template: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html")
	if _, err := w.Write([]byte(html)); err != nil {
		log.Printf("Failed to write response: %v", err)
	}
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

	w.Header().Set("Content-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")

	if _, err := w.Write(content); err != nil {

		log.Printf("Failed to write response: %v", err)

	}
}

func (s *Server) serveFavicon(w http.ResponseWriter, r *http.Request) {
	// Read the favicon SVG from assets
	content, err := os.ReadFile("assets/favicon.svg")
	if err != nil {
		log.Printf("Failed to read favicon: %v", err)
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=86400") // Cache for 1 day

	if _, err := w.Write(content); err != nil {
		log.Printf("Failed to write favicon response: %v", err)
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
	if strings.Contains(path, "..") {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	content, err := os.ReadFile(path)
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

		log.Printf("Failed to write response: %v", err)

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
