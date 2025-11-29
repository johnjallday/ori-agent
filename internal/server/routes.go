// Package server provides the HTTP server for the Ori Agent application.
// This file contains all HTTP route registrations organized by domain.
package server

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"

	agenthttp "github.com/johnjallday/ori-agent/internal/agenthttp"
	"github.com/johnjallday/ori-agent/internal/filehttp"
	"github.com/johnjallday/ori-agent/internal/updatehttp"
)

// registerRoutes registers all HTTP routes for the server.
// Routes are organized by domain for clarity and maintainability.
func registerRoutes(mux *http.ServeMux, s *Server) {
	// =============================================================================
	// Health Check Endpoint
	// =============================================================================
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]string{"status": "ok"}); err != nil {
			log.Printf("Failed to encode health response: %v", err)
		}
	})

	// =============================================================================
	// Page Handlers (HTML Pages)
	// =============================================================================
	mux.HandleFunc("/", s.serveIndex)
	mux.HandleFunc("/settings", s.serveSettings)
	mux.HandleFunc("/marketplace", s.serveMarketplace)
	mux.HandleFunc("/workflows", s.serveWorkflows)
	mux.HandleFunc("/mcp", s.serveMCP)
	mux.HandleFunc("/models", s.serveModels)
	mux.HandleFunc("/agents", s.serveAgents)      // Clean URL
	mux.HandleFunc("/agents.html", s.serveAgents) // Legacy support
	mux.HandleFunc("/agents-detail.html", s.serveStaticFile)
	mux.HandleFunc("/agents-create.html", s.serveStaticFile)
	mux.HandleFunc("/agents-dashboard", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/agents", http.StatusFound)
	})
	mux.HandleFunc("/studios/", s.serveWorkspaceDashboard) // Dynamic route for /studios/:id
	mux.HandleFunc("/studios", s.serveWorkspaces)
	mux.HandleFunc("/usage", s.serveUsage)

	// =============================================================================
	// Static File Server (CSS, JS, Icons, Assets)
	// =============================================================================
	mux.HandleFunc("/styles.css", s.serveStaticFile)
	mux.HandleFunc("/css/", s.serveStaticFile)
	mux.HandleFunc("/js/", s.serveStaticFile)
	mux.HandleFunc("/icons/", s.serveStaticFile)
	mux.HandleFunc("/chat-area.html", s.serveStaticFile)
	mux.HandleFunc("/agents/", s.serveAgentFiles)

	// Favicon endpoint
	mux.HandleFunc("/favicon.svg", s.serveFavicon)

	// =============================================================================
	// Agent API Endpoints
	// =============================================================================
	agentHandler := agenthttp.New(s.st)
	agentHandler.ActivityLogger = s.activityLogger
	mux.Handle("/api/agents", agentHandler)

	// Dashboard handlers
	dashboardHandler := agenthttp.NewDashboardHandler(s.st)
	dashboardHandler.ActivityLogger = s.activityLogger
	mux.HandleFunc("/api/agents/dashboard/list", dashboardHandler.ListAgentsWithStats)
	mux.HandleFunc("/api/agents/dashboard/stats", dashboardHandler.GetDashboardStats)

	// Agent MCP handlers
	s.agentMCPHandler = agenthttp.NewMCPHandler(s.mcpRegistry, s.mcpConfigManager, agentHandler)
	mux.HandleFunc("/api/agents/", func(w http.ResponseWriter, r *http.Request) {
		// Route dashboard detail requests first
		if strings.Contains(r.URL.Path, "/detail") {
			dashboardHandler.GetAgentDetail(w, r)
			return
		}
		// Route status update requests
		if strings.Contains(r.URL.Path, "/status") && r.Method == http.MethodPost {
			dashboardHandler.UpdateAgentStatus(w, r)
			return
		}
		// Route activity log requests
		if strings.Contains(r.URL.Path, "/activity") && r.Method == http.MethodGet {
			dashboardHandler.GetAgentActivity(w, r)
			return
		}
		// Route agent MCP-specific requests
		if strings.Contains(r.URL.Path, "/mcp-servers") {
			if strings.HasSuffix(r.URL.Path, "/enable") {
				s.agentMCPHandler.EnableAgentMCPServerHandler(w, r)
			} else if strings.HasSuffix(r.URL.Path, "/disable") {
				s.agentMCPHandler.DisableAgentMCPServerHandler(w, r)
			} else {
				// List MCP servers for agent
				s.agentMCPHandler.ListAgentMCPServersHandler(w, r)
			}
		} else {
			// Regular agent requests - delegate to agentHandler
			agentHandler.ServeHTTP(w, r)
		}
	})

	// Agent capabilities endpoint
	mux.HandleFunc("/api/agents/capabilities", s.orchestrationHandler.AgentCapabilitiesHandler)

	// =============================================================================
	// Plugin API Endpoints
	// =============================================================================
	mux.HandleFunc("/api/plugin-registry", s.pluginRegistryHandler.PluginRegistryHandler)
	mux.HandleFunc("/api/plugin-updates", s.pluginRegistryHandler.PluginUpdatesHandler)
	mux.HandleFunc("/api/plugins/download", s.pluginRegistryHandler.PluginDownloadHandler)
	mux.HandleFunc("/api/plugins/updates/check", s.pluginRegistryHandler.PluginUpdatesCheckHandler)
	mux.HandleFunc("/api/plugins/execute", s.pluginInitHandler.PluginExecuteHandler)
	mux.HandleFunc("/api/plugins/init-status", s.pluginInitHandler.PluginInitStatusHandler)

	// Plugin health check endpoints (must be before catch-all /api/plugins/ pattern)
	mux.HandleFunc("/api/plugins/health", s.healthHandler.HandleAllPluginsHealth)
	mux.HandleFunc("/api/plugins/check-updates", s.pluginUpdateHandler.HandleCheckUpdates)
	mux.HandleFunc("/api/plugins/backups", s.pluginUpdateHandler.HandleListBackups)
	mux.HandleFunc("/api/plugins/backups/clean", s.pluginUpdateHandler.HandleCleanBackups)

	// Plugin upload endpoint (must be before catch-all /api/plugins/ pattern)
	mux.HandleFunc("/api/plugins/upload", s.pluginHandler.ServeHTTP)
	mux.HandleFunc("/api/plugins/", func(w http.ResponseWriter, r *http.Request) {
		// Check if this is a web page request
		if strings.Contains(r.URL.Path, "/pages/") {
			s.webPageHandler.ServeHTTP(w, r)
			return
		}
		// Check if this is a pages list request
		if strings.HasSuffix(r.URL.Path, "/pages") {
			s.webPageHandler.ListPages(w, r)
			return
		}
		// Check if this is a health endpoint for a specific plugin
		if strings.HasSuffix(r.URL.Path, "/health") {
			s.healthHandler.HandlePluginHealth(w, r)
			return
		}
		// Check if this is an update endpoint
		if strings.HasSuffix(r.URL.Path, "/update") {
			s.pluginUpdateHandler.HandleUpdatePlugin(w, r)
			return
		}
		// Check if this is a rollback endpoint
		if strings.HasSuffix(r.URL.Path, "/rollback") {
			s.pluginUpdateHandler.HandleRollbackPlugin(w, r)
			return
		}
		// Otherwise, delegate to init handler
		s.pluginInitHandler.PluginInitHandler(w, r)
	})

	// Reuse the plugin handler instance
	mux.HandleFunc("/api/plugins/save-settings", s.pluginHandler.ServeHTTP)
	mux.HandleFunc("/api/plugins/tool-call", s.pluginHandler.DirectToolCallHandler)
	mux.Handle("/api/plugins", s.pluginHandler)

	// =============================================================================
	// Settings and Configuration Endpoints
	// =============================================================================
	mux.HandleFunc("/api/settings", s.settingsHandler.SettingsHandler)
	mux.HandleFunc("/api/api-key", s.settingsHandler.APIKeyHandler)
	mux.HandleFunc("/api/providers", s.settingsHandler.ProvidersHandler)

	// =============================================================================
	// Chat Endpoint
	// =============================================================================
	mux.HandleFunc("/api/chat", s.chatHandler.ChatHandler)

	// =============================================================================
	// Update Management Endpoints
	// =============================================================================
	updateHandler := updatehttp.NewHandler(s.updateMgr)
	mux.HandleFunc("/api/updates/check", updateHandler.CheckUpdatesHandler)
	mux.HandleFunc("/api/updates/releases", updateHandler.ListReleasesHandler)
	mux.HandleFunc("/api/updates/download", updateHandler.DownloadUpdateHandler)
	mux.HandleFunc("/api/updates/version", updateHandler.GetVersionHandler)

	// =============================================================================
	// File Parsing Endpoint
	// =============================================================================
	fileHandler := filehttp.NewHandler()
	mux.HandleFunc("/api/files/parse", fileHandler.ParseFileHandler)

	// =============================================================================
	// Onboarding Endpoints
	// =============================================================================
	mux.HandleFunc("/api/onboarding/status", s.onboardingHandler.GetStatus)
	mux.HandleFunc("/api/onboarding/step", s.onboardingHandler.CompleteStep)
	mux.HandleFunc("/api/onboarding/skip", s.onboardingHandler.Skip)
	mux.HandleFunc("/api/onboarding/complete", s.onboardingHandler.Complete)
	mux.HandleFunc("/api/onboarding/reset", s.onboardingHandler.Reset)

	// Theme endpoints
	mux.HandleFunc("/api/theme", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			s.onboardingHandler.GetTheme(w, r)
		} else if r.Method == http.MethodPost {
			s.onboardingHandler.SetTheme(w, r)
		} else {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})

	// =============================================================================
	// Device Endpoints
	// =============================================================================
	mux.HandleFunc("/api/device/info", s.deviceHandler.GetDeviceInfo)
	mux.HandleFunc("/api/device/type", s.deviceHandler.SetDeviceType)
	mux.HandleFunc("/api/device/wifi/current", s.deviceHandler.GetCurrentWiFi)

	// =============================================================================
	// Usage and Cost Tracking Endpoints
	// =============================================================================
	mux.HandleFunc("/api/usage/stats/all", s.usageHandler.GetAllTimeStats)
	mux.HandleFunc("/api/usage/stats/today", s.usageHandler.GetTodayStats)
	mux.HandleFunc("/api/usage/stats/month", s.usageHandler.GetThisMonthStats)
	mux.HandleFunc("/api/usage/stats/range", s.usageHandler.GetCustomRangeStats)
	mux.HandleFunc("/api/usage/summary", s.usageHandler.GetSummary)
	mux.HandleFunc("/api/usage/pricing", s.usageHandler.GetPricingModels)

	// =============================================================================
	// Location Management Endpoints
	// =============================================================================
	mux.HandleFunc("/api/location/current", s.locationHandler.GetCurrentLocation)
	mux.HandleFunc("/api/location/zones", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			s.locationHandler.GetZones(w, r)
		case http.MethodPost:
			s.locationHandler.CreateZone(w, r)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/location/zones/", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			s.locationHandler.UpdateZone(w, r)
		case http.MethodDelete:
			s.locationHandler.DeleteZone(w, r)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/location/override", s.locationHandler.SetManualLocation)

	// =============================================================================
	// MCP (Model Context Protocol) Endpoints
	// =============================================================================
	mux.HandleFunc("/api/mcp/servers", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			s.mcpHandler.ListServersHandler(w, r)
		case http.MethodPost:
			s.mcpHandler.AddServerHandler(w, r)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/mcp/servers/", func(w http.ResponseWriter, r *http.Request) {
		// Check for specific actions in the path
		if strings.HasSuffix(r.URL.Path, "/enable") {
			s.mcpHandler.EnableServerHandler(w, r)
		} else if strings.HasSuffix(r.URL.Path, "/disable") {
			s.mcpHandler.DisableServerHandler(w, r)
		} else if strings.HasSuffix(r.URL.Path, "/tools") {
			s.mcpHandler.GetServerToolsHandler(w, r)
		} else if strings.HasSuffix(r.URL.Path, "/status") {
			s.mcpHandler.GetServerStatusHandler(w, r)
		} else if strings.HasSuffix(r.URL.Path, "/test") {
			s.mcpHandler.TestConnectionHandler(w, r)
		} else if strings.HasSuffix(r.URL.Path, "/retry") {
			s.mcpHandler.RetryConnectionHandler(w, r)
		} else if r.Method == http.MethodDelete {
			s.mcpHandler.RemoveServerHandler(w, r)
		} else {
			http.Error(w, "Not found", http.StatusNotFound)
		}
	})
	mux.HandleFunc("/api/mcp/import", s.mcpHandler.ImportServersHandler)
	mux.HandleFunc("/api/mcp/marketplace", s.mcpHandler.GetMarketplaceServersHandler)

	// =============================================================================
	// Orchestration Endpoints
	// =============================================================================
	mux.HandleFunc("/api/orchestration/workspace", s.orchestrationHandler.WorkspaceHandler)
	mux.HandleFunc("/api/orchestration/workspace/agents", s.orchestrationHandler.WorkspaceAgentsHandler)
	mux.HandleFunc("/api/orchestration/workspace/layout", s.orchestrationHandler.SaveLayoutHandler)
	mux.HandleFunc("/api/orchestration/messages", s.orchestrationHandler.MessagesHandler)
	mux.HandleFunc("/api/orchestration/delegate", s.orchestrationHandler.DelegateHandler)
	mux.HandleFunc("/api/orchestration/tasks", s.orchestrationHandler.TasksHandler)
	mux.HandleFunc("/api/orchestration/tasks/execute", s.orchestrationHandler.ExecuteTaskHandler)
	mux.HandleFunc("/api/orchestration/task-results", s.orchestrationHandler.TaskResultsHandler)
	mux.HandleFunc("/api/orchestration/workflow/status", s.orchestrationHandler.WorkflowStatusHandler)
	mux.HandleFunc("/api/orchestration/workflow/stream", s.orchestrationHandler.WorkflowStatusStreamHandler)
	mux.HandleFunc("/api/orchestration/progress/stream", s.orchestrationHandler.ProgressStreamHandler)

	// Workflow template endpoints
	mux.HandleFunc("/api/orchestration/templates", s.orchestrationHandler.TemplatesHandler)
	mux.HandleFunc("/api/orchestration/templates/instantiate", s.orchestrationHandler.InstantiateTemplateHandler)

	// Notification endpoints
	mux.HandleFunc("/api/orchestration/notifications", s.orchestrationHandler.NotificationsHandler)
	mux.HandleFunc("/api/orchestration/notifications/stream", s.orchestrationHandler.NotificationStreamHandler)

	// Event history endpoint
	mux.HandleFunc("/api/orchestration/events", s.orchestrationHandler.EventHistoryHandler)

	// Scheduled task endpoints
	mux.HandleFunc("/api/orchestration/scheduled-tasks", s.orchestrationHandler.ScheduledTasksHandler)
	mux.HandleFunc("/api/orchestration/scheduled-tasks/", s.orchestrationHandler.ScheduledTaskHandler)

	// =============================================================================
	// Agent Studio API Endpoints
	// =============================================================================
	mux.HandleFunc("/api/studios", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			s.studioHandler.CreateStudio(w, r)
		} else if r.Method == http.MethodGet {
			s.studioHandler.ListStudios(w, r)
		} else {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})

	// Handle routes with studio ID
	mux.HandleFunc("/api/studios/", func(w http.ResponseWriter, r *http.Request) {
		// Parse the path to determine which handler to use
		if strings.HasSuffix(r.URL.Path, "/mission") {
			s.studioHandler.ExecuteMission(w, r)
		} else if strings.HasSuffix(r.URL.Path, "/events") {
			s.studioHandler.GetStudioEvents(w, r)
		} else if strings.Contains(r.URL.Path, "/tasks") {
			// Handle task operations
			if strings.HasSuffix(r.URL.Path, "/execute") && r.Method == http.MethodPost {
				s.studioHandler.ExecuteTaskManually(w, r)
			} else if r.Method == http.MethodPost {
				s.studioHandler.CreateTask(w, r)
			} else if r.Method == http.MethodPatch {
				s.studioHandler.UpdateTask(w, r)
			} else if r.Method == http.MethodDelete {
				s.studioHandler.DeleteTask(w, r)
			} else {
				http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			}
		} else if strings.Contains(r.URL.Path, "/agents") {
			// Handle agent add/remove operations
			if r.Method == http.MethodPost {
				s.studioHandler.AddAgent(w, r)
			} else if r.Method == http.MethodDelete {
				s.studioHandler.RemoveAgent(w, r)
			} else {
				http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			}
		} else {
			s.studioHandler.GetStudio(w, r)
		}
	})
}
