// Package server provides the HTTP server for the Ori Agent application.
// This file contains all HTTP route registrations organized by domain.
package server

import (
	"encoding/json"
	"net/http"
	"strings"

	agenthttp "github.com/johnjallday/ori-agent/internal/agenthttp"
	"github.com/johnjallday/ori-agent/internal/filehttp"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/updatehttp"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// registerRoutes registers all HTTP routes for the server.
// Routes are organized by domain for clarity and maintainability.
func registerRoutes(mux *http.ServeMux, s *Server) {
	caps := s.privateCapabilitiesSnapshot()

	// =============================================================================
	// Health Check Endpoint
	// =============================================================================
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if encErr := json.NewEncoder(w).Encode(map[string]string{"status": "ok"}); encErr != nil {
			logger.Error("Failed to encode health response", logger.Fields{"error": encErr})
		}
	})
	mux.HandleFunc("/api/diagnostics/ui-smoke-test", s.handleUISmokeTest)

	// =============================================================================
	// Page Handlers (HTML Pages)
	// =============================================================================
	mux.HandleFunc("/", s.serveIndex)
	mux.HandleFunc("/settings", s.serveSettings)
	mux.HandleFunc("/profile", s.serveProfile)
	mux.HandleFunc("/vaults", s.serveVault)
	mux.HandleFunc("/vault", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/vaults", http.StatusMovedPermanently)
	})
	mux.HandleFunc("/skills", s.serveSkills)
	mux.HandleFunc("/templates", s.serveTemplates)
	mux.HandleFunc("/workflows", s.serveWorkflows)
	mux.HandleFunc("/mcp", s.serveMCP)
	mux.HandleFunc("/plugins", s.servePlugins)
	mux.HandleFunc("/models", s.serveModels)
	mux.HandleFunc("/agents", s.serveAgents)      // Clean URL
	mux.HandleFunc("/agents.html", s.serveAgents) // Legacy support
	mux.HandleFunc("/agents-detail.html", s.serveAgentsDetail)
	mux.HandleFunc("/agents/create", s.serveAgentsCreate)
	mux.HandleFunc("/agents-create.html", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/agents/create", http.StatusMovedPermanently)
	})
	mux.HandleFunc("/agents-edit.html", s.serveAgentsEdit)
	mux.HandleFunc("/agents-dashboard", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/agents", http.StatusFound)
	})
	// Workspaces page routes (primary)
	mux.HandleFunc("/workspaces/", s.handleWorkspacesRoutes) // Dynamic route handler for /workspaces/{id}
	mux.HandleFunc("/notes/", s.handleNotesPageRoute)        // Focused note page: /notes/{id}
	mux.HandleFunc("/workspaces", s.serveWorkspaces)
	mux.HandleFunc("/action-center", s.serveActionCenter)
	mux.HandleFunc("/usage", s.serveUsage)
	mux.HandleFunc("/review", s.serveReview)
	mux.HandleFunc("/personalize", s.servePersonalize)

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
	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/favicon.svg", http.StatusMovedPermanently)
	})

	// =============================================================================
	// Agent Avatars (Static File Serving)
	// =============================================================================
	mux.HandleFunc("/avatars/", s.serveAvatarFiles)

	// =============================================================================
	// Agent API Endpoints
	// =============================================================================
	agentHandler := agenthttp.New(s.Storage.AgentStore)
	agentHandler.ActivityLogger = s.Handlers.ActivityLogger
	agentHandler.SetCLIAgentRegistry(s.Handlers.CLIAgentRegistry)
	agentHandler.SetWorkspaceStore(s.Storage.WorkspaceStore)
	if s.Storage.SessionStore != nil {
		agentHandler.SetSessionPurger(s.Storage.SessionStore)
	}
	if s.Handlers.ExternalAgents != nil {
		agentHandler.SetClaudeSyncProvider(s.Handlers.ExternalAgents.ClaudeSyncData)
	}
	avatarHandler := agenthttp.NewAvatarHandler(s.Storage.AgentStore)
	mux.Handle("/api/agents", agentHandler)
	if s.Handlers.Evolution != nil {
		mux.HandleFunc("/api/evolution/assistant", s.Handlers.Evolution.GetAssistantProgress)
		mux.HandleFunc("/api/evolution/suggestions", s.Handlers.Evolution.GetSuggestions)
	}

	// Dashboard handlers
	dashboardHandler := agenthttp.NewDashboardHandler(s.Storage.AgentStore)
	dashboardHandler.ActivityLogger = s.Handlers.ActivityLogger
	dashboardHandler.SetCLIAgentRegistry(s.Handlers.CLIAgentRegistry)
	dashboardHandler.SetWorkspaceStore(s.Storage.WorkspaceStore)
	if s.Handlers.ExternalAgents != nil {
		dashboardHandler.SetClaudeSyncProvider(s.Handlers.ExternalAgents.ClaudeSyncData)
	}
	mux.HandleFunc("/api/agents/dashboard/list", dashboardHandler.ListAgentsWithStats)
	mux.HandleFunc("/api/agents/dashboard/stats", dashboardHandler.GetDashboardStats)

	mux.HandleFunc("/api/agents/", func(w http.ResponseWriter, r *http.Request) {
		// Route evolution API requests first
		if s.Handlers.Evolution != nil && strings.HasSuffix(r.URL.Path, "/evolution/path") && r.Method == http.MethodPost {
			s.Handlers.Evolution.SetAgentPath(w, r)
			return
		}
		if s.Handlers.Evolution != nil && strings.HasSuffix(r.URL.Path, "/evolution") && r.Method == http.MethodGet {
			s.Handlers.Evolution.GetAgentEvolution(w, r)
			return
		}
		if s.Handlers.Evolution != nil && strings.HasSuffix(r.URL.Path, "/feed") && r.Method == http.MethodPost {
			s.Handlers.Evolution.FeedAgent(w, r)
			return
		}
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
		// Route avatar upload/delete requests
		if strings.Contains(r.URL.Path, "/avatar") {
			avatarHandler.ServeHTTP(w, r)
			return
		}
		// Regular agent requests - delegate to agentHandler
		agentHandler.ServeHTTP(w, r)
	})

	// Agent capabilities endpoint
	mux.HandleFunc("/api/agents/capabilities", s.Handlers.Orchestration.AgentCapabilitiesHandler)

	// Agent auto-config endpoints
	mux.HandleFunc("/api/agents/auto-config", s.Handlers.AutoConfig.AutoConfigHandler)
	mux.HandleFunc("/api/agents/auto-config/availability", s.Handlers.AutoConfig.CheckLLMAvailabilityHandler)

	// Home assistant task routing endpoint
	homeAssistantRouteHandler := agenthttp.NewHomeAssistantRouteHandler(s.Storage.AgentStore)
	homeAssistantRouteHandler.SetSystemModelReader(s.Core.ConfigManager)
	homeAssistantWorkspaceResolver := agenthttp.NewHomeAssistantWorkspaceResolver(
		s.Storage.WorkspaceStore,
		s.Storage.AgentStore,
	)
	if s.Storage.SessionStore != nil {
		traceStore := agenthttp.NewSQLiteHomeAssistantIntakeTraceStore(s.Storage.SessionStore.DB())
		homeAssistantRouteHandler.SetIntakeTraceStore(traceStore)
		homeAssistantWorkspaceResolver.SetFeedbackReader(traceStore)
	}
	homeAssistantRouteHandler.SetWorkspaceResolver(homeAssistantWorkspaceResolver)
	if s.Storage != nil && s.Integration != nil {
		homeAssistantRouteHandler.SetRuntimeResolver(
			workspace.NewAgentRuntimeResolver(
				s.Storage.AgentStore,
				s.Storage.WorkspaceStore,
				s.Integration.MCPRegistry,
				s.Integration.MCPConfigManager,
			),
		)
	}
	mux.HandleFunc("/api/home-assistant/route", homeAssistantRouteHandler.RouteHandler)
	mux.HandleFunc("/api/home-assistant/trace", homeAssistantRouteHandler.TraceHandler)
	mux.HandleFunc("/api/home-assistant/trace/summary", homeAssistantRouteHandler.TraceSummaryHandler)

	// Home harness inline endpoint: answers app-introspection / app-navigation
	// prompts using the cross-workspace home snapshot and read-only home tools.
	mux.HandleFunc("/api/home-assistant/ask", s.newHomeAssistantAskHandler().AskHandler)

	// =============================================================================
	// Settings and Configuration Endpoints
	// =============================================================================
	mux.HandleFunc("/api/settings", s.Handlers.Settings.SettingsHandler)
	mux.HandleFunc("/api/settings/session", s.Handlers.Settings.SessionSettingsHandler)
	mux.HandleFunc("/api/settings/workspace-root", s.Handlers.Settings.WorkspaceRootSettingsHandler)
	mux.HandleFunc("/api/settings/vault-root", s.Handlers.Settings.VaultRootSettingsHandler)
	mux.HandleFunc("/api/settings/templates-root", s.Handlers.Settings.TemplatesRootSettingsHandler)
	mux.HandleFunc("/api/api-key", s.Handlers.Settings.APIKeyHandler)
	mux.HandleFunc("/api/providers", s.Handlers.Settings.ProvidersHandler)
	mux.HandleFunc("/api/settings/system-model", s.Handlers.Settings.SystemModelHandler)
	mux.HandleFunc("/api/settings/available-models", s.Handlers.Settings.AvailableModelsHandler)
	mux.HandleFunc("/api/settings/system-paths", s.Handlers.Settings.SystemPathsHandler)
	mux.HandleFunc("/api/settings/external-agents", s.Handlers.Settings.ExternalAgentsSettingsHandler)
	mux.HandleFunc("/api/settings/speech", s.Handlers.Settings.SpeechSettingsHandler)
	mux.HandleFunc("/api/settings/utility", s.Handlers.Settings.UtilitySettingsHandler)
	mux.HandleFunc("/api/settings/mac-wake", s.Handlers.Settings.MacWakeSettingsHandler)
	mux.HandleFunc("/api/settings/mac-wake/permission", s.Handlers.Settings.MacWakePermissionHandler)
	mux.HandleFunc("/api/transcribe", s.Handlers.Speech.Transcribe)
	if s.Handlers.Vault != nil {
		mux.Handle("/api/vault", s.Handlers.Vault)
		mux.Handle("/api/vault/", s.Handlers.Vault)
	}

	// Web3 Wallet endpoints
	if caps.Web3Wallet {
		mux.HandleFunc("/api/web3-wallet", s.Handlers.Settings.Web3WalletHandler)
		mux.HandleFunc("/api/web3-chains", s.Handlers.Settings.Web3ChainsHandler)
	}

	// Reset endpoints
	mux.HandleFunc("/api/reset", s.Handlers.Reset.HandleReset)
	mux.HandleFunc("/api/reset/preview", s.Handlers.Reset.GetResetPreview)

	// =============================================================================
	// Chat Endpoint
	// =============================================================================
	mux.HandleFunc("/api/chat", s.Handlers.Chat.ChatHandler)

	// =============================================================================
	// Update Management Endpoints
	// =============================================================================
	updateHandler := updatehttp.NewHandler(s.Integration.UpdateManager)
	mux.HandleFunc("/api/updates/check", updateHandler.CheckUpdatesHandler)
	mux.HandleFunc("/api/updates/releases", updateHandler.ListReleasesHandler)
	mux.HandleFunc("/api/updates/version", updateHandler.GetVersionHandler)

	// =============================================================================
	// File Parsing Endpoint
	// =============================================================================
	fileHandler := filehttp.NewHandler()
	mux.HandleFunc("/api/files/parse", fileHandler.ParseFileHandler)
	mux.HandleFunc("/api/files/upload", fileHandler.UploadFileHandler)
	mux.HandleFunc("/api/files/content", fileHandler.GetFileHandler)

	// =============================================================================
	// Onboarding Endpoints
	// =============================================================================
	mux.HandleFunc("/api/onboarding/status", s.Handlers.Onboarding.GetStatus)
	mux.HandleFunc("/api/onboarding/names", s.Handlers.Onboarding.SaveNames)
	mux.HandleFunc("/api/onboarding/timezone", s.Handlers.Onboarding.SaveTimezone)
	mux.HandleFunc("/api/onboarding/step", s.Handlers.Onboarding.CompleteStep)
	mux.HandleFunc("/api/onboarding/skip-step", s.Handlers.Onboarding.SkipStep)
	mux.HandleFunc("/api/onboarding/skip", s.Handlers.Onboarding.Skip)
	mux.HandleFunc("/api/onboarding/complete", s.Handlers.Onboarding.Complete)
	mux.HandleFunc("/api/onboarding/reset", s.Handlers.Onboarding.Reset)

	// Smart onboarding endpoints (AI-powered profile inference)
	mux.HandleFunc("/api/onboarding/detect", s.Handlers.SmartOnboarding.Detect)
	mux.HandleFunc("/api/onboarding/profile", s.Handlers.SmartOnboarding.InferProfile)
	mux.HandleFunc("/api/onboarding/describe", s.Handlers.SmartOnboarding.Describe)
	mux.HandleFunc("/api/onboarding/config", s.Handlers.SmartOnboarding.GenerateConfig)
	mux.HandleFunc("/api/onboarding/apply-config", s.Handlers.SmartOnboarding.Apply)
	mux.HandleFunc("/api/onboarding/update-profile", s.Handlers.SmartOnboarding.UpdateProfile)
	mux.HandleFunc("/api/onboarding/user-profile", s.Handlers.SmartOnboarding.GetStoredProfile)
	mux.HandleFunc("/api/onboarding/personalize", s.Handlers.SmartOnboarding.SavePersonalization)

	if s.Handlers.User != nil {
		mux.HandleFunc("/api/user/profile", s.Handlers.User.Profile)
	}

	// Theme endpoints
	mux.HandleFunc("/api/theme", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			s.Handlers.Onboarding.GetTheme(w, r)
		case http.MethodPost:
			s.Handlers.Onboarding.SetTheme(w, r)
		default:
			orihttp.MethodNotAllowed(w)
			// =============================================================================
			// Device Endpoints
			// =============================================================================
		}
	})

	// Notes open-behavior preference: "modal" (default), "page", "page-new-tab".
	mux.HandleFunc("/api/notes-open-behavior", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			s.Handlers.Onboarding.GetNotesOpenBehavior(w, r)
		case http.MethodPost:
			s.Handlers.Onboarding.SetNotesOpenBehavior(w, r)
		default:
			orihttp.MethodNotAllowed(w)
		}
	})

	mux.HandleFunc("/api/device/info", s.Handlers.Device.GetDeviceInfo)
	mux.HandleFunc("/api/device/type", s.Handlers.Device.SetDeviceType)
	mux.HandleFunc("/api/device/wifi/current", s.Handlers.Device.GetCurrentWiFi)
	mux.HandleFunc("/api/device/ollama", s.Handlers.Device.GetOllamaStatus)
	mux.HandleFunc("/api/device/capabilities", s.Handlers.Device.GetCapabilities)
	mux.HandleFunc("/api/device/detect-hardware", s.Handlers.Device.DetectHardware)

	// =============================================================================
	// Usage and Cost Tracking Endpoints
	// =============================================================================
	mux.HandleFunc("/api/usage/stats/all", s.Handlers.Usage.GetAllTimeStats)
	mux.HandleFunc("/api/usage/stats/today", s.Handlers.Usage.GetTodayStats)
	mux.HandleFunc("/api/usage/stats/month", s.Handlers.Usage.GetThisMonthStats)
	mux.HandleFunc("/api/usage/stats/range", s.Handlers.Usage.GetCustomRangeStats)
	mux.HandleFunc("/api/usage/summary", s.Handlers.Usage.GetSummary)
	mux.HandleFunc("/api/usage/utility", s.Handlers.Usage.GetUtilityMetrics)
	mux.HandleFunc("/api/usage/pricing", s.Handlers.Usage.GetPricingModels)

	// =============================================================================
	// Model Category Endpoints
	// =============================================================================
	if s.Handlers.ModelCategory != nil {
		mux.HandleFunc("/api/model-categories", s.Handlers.ModelCategory.CategoriesHandler)
		mux.HandleFunc("/api/model-categories/reorder", s.Handlers.ModelCategory.ReorderCategoriesHandler)
		mux.HandleFunc("/api/model-categories/view-preference", s.Handlers.ModelCategory.SetViewPreferenceHandler)
		mux.HandleFunc("/api/model-categories/", s.Handlers.ModelCategory.CategoryHandler)
		mux.HandleFunc("/api/models/", func(w http.ResponseWriter, r *http.Request) {
			// Handle model category assignments
			if strings.HasSuffix(r.URL.Path, "/categories") {
				s.Handlers.ModelCategory.SetModelAssignmentsHandler(w, r)
				return
			}
			// Otherwise, 404
			orihttp.NotFound(w, "Not found")
		})
	}

	// Auto-categorize endpoints (requires both category store and LLM)
	if s.Handlers.AutoCategorize != nil {
		mux.HandleFunc("/api/models/auto-categorize/availability", s.Handlers.AutoCategorize.CheckAvailabilityHandler)
		mux.HandleFunc("/api/models/auto-categorize", s.Handlers.AutoCategorize.AutoCategorizeHandler)
	}

	// =============================================================================
	// Location Management Endpoints
	// =============================================================================
	mux.HandleFunc("/api/location/current", s.Handlers.Location.GetCurrentLocation)
	mux.HandleFunc("/api/location/zones", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			s.Handlers.Location.GetZones(w, r)
		case http.MethodPost:
			s.Handlers.Location.CreateZone(w, r)
		default:
			orihttp.MethodNotAllowed(w)
		}
	})
	mux.HandleFunc("/api/location/zones/", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			s.Handlers.Location.UpdateZone(w, r)
		case http.MethodDelete:
			s.Handlers.Location.DeleteZone(w, r)
		default:
			orihttp.MethodNotAllowed(w)
		}
	})
	mux.HandleFunc("/api/location/override", s.Handlers.Location.SetManualLocation)

	// =============================================================================
	// MCP (Model Context Protocol) Endpoints
	// =============================================================================
	mux.HandleFunc("GET /api/mcp/servers", s.Handlers.MCP.ListServersHandler)
	mux.HandleFunc("POST /api/mcp/servers", s.Handlers.MCP.AddServerHandler)
	mux.HandleFunc("DELETE /api/mcp/servers/{name}", s.Handlers.MCP.RemoveServerHandler)
	mux.HandleFunc("POST /api/mcp/servers/{name}/enable", s.Handlers.MCP.EnableServerHandler)
	mux.HandleFunc("POST /api/mcp/servers/{name}/disable", s.Handlers.MCP.DisableServerHandler)
	mux.HandleFunc("GET /api/mcp/servers/{name}/tools", s.Handlers.MCP.GetServerToolsHandler)
	mux.HandleFunc("GET /api/mcp/servers/{name}/details", s.Handlers.MCP.GetServerDetailsHandler)
	mux.HandleFunc("GET /api/mcp/servers/{name}/status", s.Handlers.MCP.GetServerStatusHandler)
	mux.HandleFunc("POST /api/mcp/servers/{name}/test", s.Handlers.MCP.TestConnectionHandler)
	mux.HandleFunc("POST /api/mcp/servers/{name}/retry", s.Handlers.MCP.RetryConnectionHandler)

	mux.HandleFunc("POST /api/mcp/import", s.Handlers.MCP.ImportServersHandler)
	mux.HandleFunc("GET /api/mcp/marketplace", s.Handlers.MCP.GetMarketplaceServersHandler)
	mux.HandleFunc("GET /api/mcp/search", s.Handlers.MCP.SearchServersHandler)
	mux.HandleFunc("GET /api/mcp/registry-sources", s.Handlers.MCP.ListRegistrySourcesHandler)
	mux.HandleFunc("POST /api/mcp/registry-sources", s.Handlers.MCP.AddRegistrySourceHandler)
	mux.HandleFunc("DELETE /api/mcp/registry-sources/{id}", s.Handlers.MCP.RegistrySourcesItemHandler)
	mux.HandleFunc("POST /api/mcp/registry/refresh", s.Handlers.MCP.RefreshRegistryHandler)

	// =============================================================================
	// Orchestration Endpoints
	// =============================================================================
	mux.HandleFunc("/api/orchestration/workspace", s.Handlers.Orchestration.WorkspaceHandler)
	mux.HandleFunc("/api/orchestration/workspace/activate", s.Handlers.Orchestration.WorkspaceActivateHandler)
	mux.HandleFunc("/api/orchestration/workspace/agents", s.Handlers.Orchestration.WorkspaceAgentsHandler)
	mux.HandleFunc("/api/orchestration/workspace/layout", s.Handlers.Orchestration.SaveLayoutHandler)
	mux.HandleFunc("/api/orchestration/messages", s.Handlers.Orchestration.MessagesHandler)
	mux.HandleFunc("/api/orchestration/delegate", s.Handlers.Orchestration.DelegateHandler)
	mux.HandleFunc("/api/orchestration/dynamic-agents/approve", s.Handlers.Orchestration.DynamicAgentApprovalHandler)
	mux.HandleFunc("/api/orchestration/tasks", s.Handlers.Orchestration.TasksHandler)
	mux.HandleFunc("/api/orchestration/tasks/bulk", s.Handlers.Orchestration.BulkDeleteTasksHandler)
	mux.HandleFunc("/api/orchestration/tasks/execute", s.Handlers.Orchestration.ExecuteTaskHandler)
	mux.HandleFunc("/api/orchestration/workflows", s.Handlers.Orchestration.WorkflowCreateHandler)
	if s.Handlers.AutoTask != nil {
		mux.HandleFunc("/api/orchestration/tasks/auto-parse", s.Handlers.AutoTask.HandleAutoTask)
		mux.HandleFunc("/api/orchestration/tasks/output-contract/suggest", s.Handlers.AutoTask.HandleOutputContractSuggestion)
		mux.HandleFunc("/api/orchestration/tasks/output-spec/suggest", s.Handlers.AutoTask.HandleOutputContractSuggestion)
		mux.HandleFunc("/api/orchestration/tasks/output-contract/telemetry", s.Handlers.AutoTask.HandleOutputContractTelemetry)
	}
	mux.HandleFunc("/api/orchestration/tasks/", s.Handlers.Orchestration.TasksPathHandler) // Handles /api/orchestration/tasks/{id} and /api/orchestration/tasks/{id}/complete
	mux.HandleFunc("/api/orchestration/task-results", s.Handlers.Orchestration.TaskResultsHandler)
	mux.HandleFunc("/api/orchestration/workflow/status", s.Handlers.Orchestration.WorkflowStatusHandler)
	mux.HandleFunc("/api/orchestration/workflow/stream", s.Handlers.Orchestration.WorkflowStatusStreamHandler)
	mux.HandleFunc("/api/orchestration/progress/stream", s.Handlers.Orchestration.ProgressStreamHandler)

	// Workflow template endpoints
	mux.HandleFunc("/api/orchestration/templates", s.Handlers.Orchestration.TemplatesHandler)
	mux.HandleFunc("/api/orchestration/templates/instantiate", s.Handlers.Orchestration.InstantiateTemplateHandler)

	// =============================================================================
	// Custom Workflow API Endpoints
	// =============================================================================
	// List workflows or create new workflow
	mux.HandleFunc("/api/workflows", s.Handlers.Workflow.WorkflowsHandler)
	// Get, delete specific workflow, or check agents
	mux.HandleFunc("/api/workflows/", s.Handlers.Workflow.WorkflowHandler)

	// Notification endpoints
	mux.HandleFunc("/api/orchestration/notifications", s.Handlers.Orchestration.NotificationsHandler)
	mux.HandleFunc("/api/orchestration/notifications/stream", s.Handlers.Orchestration.NotificationStreamHandler)

	// Event history endpoint
	mux.HandleFunc("/api/orchestration/events", s.Handlers.Orchestration.EventHistoryHandler)

	// Home dashboard: unified recent-activity feed across all workspaces.
	mux.HandleFunc("/api/activity/recent", s.Handlers.Orchestration.RecentActivityHandler)

	// Scheduled task endpoints
	mux.HandleFunc("/api/orchestration/scheduled-tasks", s.Handlers.Orchestration.ScheduledTasksHandler)
	mux.HandleFunc("/api/orchestration/scheduled-tasks/upcoming", s.Handlers.Orchestration.UpcomingScheduledTasksHandler)
	mux.HandleFunc("/api/orchestration/scheduled-tasks/", s.Handlers.Orchestration.ScheduledTaskHandler)

	// Scheduler node endpoints (canvas-based scheduled tasks)
	mux.HandleFunc("/api/orchestration/workspaces/", func(w http.ResponseWriter, r *http.Request) {
		// Route scheduler node endpoints
		if strings.Contains(r.URL.Path, "/scheduler-nodes") {
			// Check if this is a specific node operation (contains node ID after scheduler-nodes/)
			parts := strings.Split(strings.TrimSuffix(r.URL.Path, "/"), "/")
			schedulerNodesIndex := -1
			for i, part := range parts {
				if part == "scheduler-nodes" {
					schedulerNodesIndex = i
					break
				}
			}

			if schedulerNodesIndex != -1 && len(parts) > schedulerNodesIndex+1 {
				// Has node ID: /workspaces/{id}/scheduler-nodes/{node_id}
				// Check for /trigger action
				if len(parts) > schedulerNodesIndex+2 && parts[schedulerNodesIndex+2] == "trigger" {
					s.Handlers.Orchestration.SchedulerNodeTriggerHandler(w, r)
					return
				}

				// Regular node operations (GET/PUT/DELETE)
				s.Handlers.Orchestration.SchedulerNodeHandler(w, r)
			} else {
				// No node ID: /workspaces/{id}/scheduler-nodes (list/create)
				s.Handlers.Orchestration.SchedulerNodesHandler(w, r)
			}
			return
		}

		// Fall through to other workspace endpoints (handled elsewhere)
		http.NotFound(w, r)
	})

	// =============================================================================
	// Session Management Endpoints (including Session Files)
	// =============================================================================
	if s.Handlers.Session != nil {
		// Cleanup, stats, and bulk operations routes (must be registered before the wildcard routes)
		mux.HandleFunc("/api/sessions/cleanup", s.Handlers.Session.HandleCleanup)
		mux.HandleFunc("/api/sessions/stats", s.Handlers.Session.HandleStorageStats)
		mux.HandleFunc("/api/sessions/bulk", s.Handlers.Session.HandleBulkDeleteSessions)

		// Auto-classify route (must be registered before the wildcard routes)
		if s.Handlers.AutoClassify != nil {
			mux.HandleFunc("/api/sessions/auto-classify", s.Handlers.AutoClassify.HandleAutoClassify)
		}

		if s.Handlers.SmartInput != nil {
			mux.HandleFunc("/api/smart-input/classify", s.Handlers.SmartInput.HandleClassify)
			mux.HandleFunc("/api/smart-input/override", s.Handlers.SmartInput.HandleOverride)
		}

		mux.HandleFunc("/api/sessions/", func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.Path

			// Session files routes (check if files handler is available)
			if s.Handlers.SessionFiles != nil {
				if strings.Contains(path, "/files/upload") && r.Method == http.MethodPost {
					s.Handlers.SessionFiles.UploadFile(w, r)
					return
				}
				if strings.Contains(path, "/files/link") && r.Method == http.MethodPost {
					s.Handlers.SessionFiles.LinkFile(w, r)
					return
				}
				if strings.Contains(path, "/files/validate") && r.Method == http.MethodPost {
					s.Handlers.SessionFiles.ValidateLinks(w, r)
					return
				}
				if strings.Contains(path, "/files/events") && r.Method == http.MethodGet {
					s.Handlers.SessionFiles.FileEvents(w, r)
					return
				}
				if strings.Contains(path, "/files/watch") {
					switch r.Method {
					case http.MethodPost:
						s.Handlers.SessionFiles.StartWatching(w, r)
					case http.MethodDelete:
						s.Handlers.SessionFiles.StopWatching(w, r)
					}
					return
				}
				if strings.Contains(path, "/folder/open") && r.Method == http.MethodPost {
					s.Handlers.SessionFiles.OpenFolder(w, r)
					return
				}

				// File-specific routes (with file ID)
				if strings.Contains(path, "/files/") {
					if strings.HasSuffix(path, "/download") {
						s.Handlers.SessionFiles.DownloadFile(w, r)
						return
					}
					if strings.HasSuffix(path, "/relink") && r.Method == http.MethodPost {
						s.Handlers.SessionFiles.RelinkFile(w, r)
						return
					}
					if r.Method == http.MethodDelete {
						s.Handlers.SessionFiles.DeleteFile(w, r)
						return
					}
					if r.Method == http.MethodGet {
						s.Handlers.SessionFiles.GetFile(w, r)
						return
					}
				}

				// List files route
				if strings.HasSuffix(path, "/files") && r.Method == http.MethodGet {
					s.Handlers.SessionFiles.ListFiles(w, r)
					return
				}
			}

			// Fall through to session handler
			s.Handlers.Session.HandleSessions(w, r)
		})
		mux.HandleFunc("/api/sessions", s.Handlers.Session.HandleSessions)

		// Notes search and bulk operations must be before the wildcard /api/notes/
		mux.HandleFunc("/api/notes/search", s.Handlers.Session.HandleNotes)
		mux.HandleFunc("/api/notes/bulk", s.Handlers.Session.HandleBulkDeleteNotes)
		// Note AI generation endpoint
		if s.Handlers.Note != nil {
			mux.HandleFunc("/api/notes/generate", s.Handlers.Note.GenerateHandler)
			mux.HandleFunc("/api/notes/assist", s.Handlers.Note.AssistHandler)
		}
		mux.HandleFunc("/api/notes/", s.Handlers.Session.HandleNotes)
		mux.HandleFunc("/api/notes", s.Handlers.Session.HandleNotes)

		// Legacy folder routes (redirect to workspace routes)
		mux.HandleFunc("/api/folders/", func(w http.ResponseWriter, r *http.Request) {
			// Check if this is a workspace notes request: /api/folders/{id}/notes[/...]
			// Match only when "notes" is the second path segment so that linked-directory
			// file paths with a "notes/" subfolder don't get misrouted here.
			trimmed := strings.TrimPrefix(r.URL.Path, "/api/folders/")
			parts := strings.Split(trimmed, "/")
			if len(parts) >= 2 && parts[1] == "notes" {
				s.Handlers.Session.HandleWorkspaceNotes(w, r)
				return
			}
			// Otherwise, handle as regular workspace request
			s.Handlers.Session.HandleWorkspaces(w, r)
		})
		mux.HandleFunc("/api/folders", s.Handlers.Session.HandleWorkspaces)

		// Workspace routes (unified workspace API)
		mux.HandleFunc("/api/workspaces", s.handleWorkspaceCollectionAPI)
		mux.HandleFunc("/api/workspaces/", s.handleWorkspaceAPI)
		if s.Handlers.TemplateOnboarding != nil {
			s.Handlers.TemplateOnboarding.RegisterRoutes(mux)
		}

		// Project template library (used by the workspace creation flow)
		mux.HandleFunc("/api/project-templates", s.handleProjectTemplates)
		mux.HandleFunc("POST /api/project-templates", s.handleProjectTemplateCreate)
		mux.HandleFunc("POST /api/project-templates/import", s.handleProjectTemplateImport)
		mux.HandleFunc("POST /api/project-templates/reveal", s.handleProjectTemplateReveal)
		mux.HandleFunc("POST /api/project-templates/{templateID}/duplicate", s.handleProjectTemplateDuplicate)
		mux.HandleFunc("PUT /api/project-templates/{templateID}", s.handleProjectTemplateUpdate)
		mux.HandleFunc("DELETE /api/project-templates/{templateID}", s.handleProjectTemplateDelete)
		// In-app file authoring (path-jailed to the template folder)
		mux.HandleFunc("GET /api/project-templates/{templateID}/files", s.handleProjectTemplateFilesList)
		mux.HandleFunc("POST /api/project-templates/{templateID}/files", s.handleProjectTemplateFileCreate)
		mux.HandleFunc("GET /api/project-templates/{templateID}/files/content", s.handleProjectTemplateFileRead)
		mux.HandleFunc("PUT /api/project-templates/{templateID}/files/content", s.handleProjectTemplateFileWrite)
		mux.HandleFunc("POST /api/project-templates/{templateID}/files/rename", s.handleProjectTemplateFileRename)
		mux.HandleFunc("DELETE /api/project-templates/{templateID}/files", s.handleProjectTemplateFileDelete)
		// Onboarding intake block authoring
		mux.HandleFunc("GET /api/project-templates/{templateID}/onboarding", s.handleProjectTemplateOnboardingGet)
		mux.HandleFunc("PUT /api/project-templates/{templateID}/onboarding", s.handleProjectTemplateOnboardingSet)
		mux.HandleFunc("DELETE /api/project-templates/{templateID}/onboarding", s.handleProjectTemplateOnboardingDelete)
		// Default tool bindings (skills / MCP servers / plugins), applied if present at creation
		mux.HandleFunc("PUT /api/project-templates/{templateID}/tools", s.handleProjectTemplateToolsSet)

		mux.HandleFunc("/api/tags", s.Handlers.Session.HandleTags)
		mux.HandleFunc("/api/tags/usage", s.Handlers.Session.HandleTagUsage)
		mux.HandleFunc("/api/tags/rename", s.Handlers.Session.HandleTagRename)
		mux.HandleFunc("/api/tags/delete", s.Handlers.Session.HandleTagDelete)
		mux.HandleFunc("/api/session-cache/stats", s.Handlers.Session.HandleCacheStats)
	}

	// =============================================================================
	// Review System API Endpoints
	// =============================================================================
	if s.Handlers.Review != nil {
		mux.HandleFunc("/api/review/trigger", s.Handlers.Review.HandleTrigger)
		mux.HandleFunc("/api/review/status/", s.Handlers.Review.HandleStatus)
		mux.HandleFunc("/api/review/issues", s.Handlers.Review.HandleIssues)
		mux.HandleFunc("/api/review/export", s.Handlers.Review.HandleExport)
		mux.HandleFunc("/api/review/runs", s.Handlers.Review.HandleRuns)
	}

	// =============================================================================
	// CLI Agent Adapter Endpoints
	// =============================================================================
	if s.Handlers.CLIAgents != nil {
		mux.HandleFunc("/api/cli-agents", s.Handlers.CLIAgents.HandleListAgents)
		mux.HandleFunc("/api/cli-agents/tasks", s.Handlers.CLIAgents.HandleCreateTask)
		mux.HandleFunc("/api/cli-agents/tasks/", s.Handlers.CLIAgents.HandleGetTask)
	}

	// =============================================================================
	// Workspace Runs API Endpoints
	// =============================================================================
	if s.Handlers.WorkspaceRuns != nil {
		mux.HandleFunc("POST /api/workspaces/{workspaceID}/runs", s.Handlers.WorkspaceRuns.CreateRun)
		mux.HandleFunc("GET /api/workspaces/{workspaceID}/runs", s.Handlers.WorkspaceRuns.ListRuns)
		mux.HandleFunc("GET /api/workspaces/{workspaceID}/runs/{runID}", s.Handlers.WorkspaceRuns.GetRun)
		mux.HandleFunc("POST /api/workspaces/{workspaceID}/runs/{runID}/stop", s.Handlers.WorkspaceRuns.StopRun)
		mux.HandleFunc("POST /api/workspaces/{workspaceID}/runs/{runID}/approve", s.Handlers.WorkspaceRuns.ApproveRun)
		mux.HandleFunc("POST /api/workspaces/{workspaceID}/runs/{runID}/reject", s.Handlers.WorkspaceRuns.RejectRun)
		mux.HandleFunc("GET /api/workspaces/{workspaceID}/runs/{runID}/artifacts", s.Handlers.WorkspaceRuns.ListArtifacts)
		mux.HandleFunc("GET /api/workspaces/{workspaceID}/runs/{runID}/trace", s.Handlers.WorkspaceRuns.ListTrace)
	}

	// =============================================================================
	// Event Triggers API Endpoints
	// =============================================================================
	if s.Handlers.Triggers != nil {
		// Public webhook ingestion (auth is the per-trigger token + optional secret).
		mux.HandleFunc("POST /api/hooks/{token}", s.Handlers.Triggers.HandleWebhook)

		// Per-workspace trigger management.
		mux.HandleFunc("GET /api/workspaces/{workspaceID}/triggers", s.Handlers.Triggers.List)
		mux.HandleFunc("POST /api/workspaces/{workspaceID}/triggers", s.Handlers.Triggers.Create)
		mux.HandleFunc("GET /api/workspaces/{workspaceID}/triggers/{triggerID}", s.Handlers.Triggers.Get)
		mux.HandleFunc("PUT /api/workspaces/{workspaceID}/triggers/{triggerID}", s.Handlers.Triggers.Update)
		mux.HandleFunc("DELETE /api/workspaces/{workspaceID}/triggers/{triggerID}", s.Handlers.Triggers.Delete)
		mux.HandleFunc("POST /api/workspaces/{workspaceID}/triggers/{triggerID}/enable", s.Handlers.Triggers.SetEnabled(true))
		mux.HandleFunc("POST /api/workspaces/{workspaceID}/triggers/{triggerID}/disable", s.Handlers.Triggers.SetEnabled(false))
		mux.HandleFunc("POST /api/workspaces/{workspaceID}/triggers/{triggerID}/regenerate-token", s.Handlers.Triggers.RegenerateToken)
		mux.HandleFunc("POST /api/workspaces/{workspaceID}/triggers/{triggerID}/test-fire", s.Handlers.Triggers.TestFire)
		mux.HandleFunc("GET /api/workspaces/{workspaceID}/triggers/{triggerID}/fires", s.Handlers.Triggers.Fires)
	}

	// =============================================================================
	// Workspace Memory (MEMORY.md) Endpoints
	// =============================================================================
	if s.Handlers.WorkspaceMemory != nil {
		mux.HandleFunc("GET /api/workspaces/{workspaceID}/memory", s.Handlers.WorkspaceMemory.GetMemory)
		mux.HandleFunc("POST /api/workspaces/{workspaceID}/memory/entries", s.Handlers.WorkspaceMemory.AddEntry)
		mux.HandleFunc("PUT /api/workspaces/{workspaceID}/memory/entries/{index}", s.Handlers.WorkspaceMemory.UpdateEntry)
		mux.HandleFunc("DELETE /api/workspaces/{workspaceID}/memory/entries/{index}", s.Handlers.WorkspaceMemory.DeleteEntry)
	}

	// =============================================================================
	// External Agents (Claude Code, Codex) Endpoints
	// =============================================================================
	if s.Handlers.ExternalAgents != nil {
		mux.HandleFunc("/api/external-agents", s.Handlers.ExternalAgents.GetAll)
		mux.HandleFunc("/api/external-agents/claude", s.Handlers.ExternalAgents.GetClaude)
		mux.HandleFunc("/api/external-agents/codex", s.Handlers.ExternalAgents.GetCodex)
		mux.HandleFunc("/api/external-agents/refresh", s.Handlers.ExternalAgents.Refresh)
	}

	// =============================================================================
	// Skills Endpoints
	// =============================================================================
	if s.Handlers.Skills != nil {
		mux.HandleFunc("/api/skills", s.Handlers.Skills.List)
		mux.HandleFunc("/api/skills/", s.Handlers.Skills.Handle)
	}

	// =============================================================================
	// Plugin Endpoints (Claude Code / Codex-compatible bundles)
	// =============================================================================
	if s.Handlers.Plugin != nil {
		mux.HandleFunc("GET /api/plugins", s.Handlers.Plugin.ListHandler)
		mux.HandleFunc("POST /api/plugins/install", s.Handlers.Plugin.InstallHandler)
		mux.HandleFunc("GET /api/plugins/marketplaces", s.Handlers.Plugin.MarketplacesHandler)
		mux.HandleFunc("POST /api/plugins/marketplaces", s.Handlers.Plugin.MarketplacesHandler)
		mux.HandleFunc("POST /api/plugins/marketplaces/install", s.Handlers.Plugin.MarketplaceInstallHandler)
		mux.HandleFunc("DELETE /api/plugins/{name}", s.Handlers.Plugin.UninstallHandler)
		mux.HandleFunc("POST /api/plugins/{name}/enable", s.Handlers.Plugin.SetEnabledHandler(true))
		mux.HandleFunc("POST /api/plugins/{name}/disable", s.Handlers.Plugin.SetEnabledHandler(false))
		mux.HandleFunc("POST /api/plugins/{name}/update", s.Handlers.Plugin.UpdateHandler)
	}

	// =============================================================================
	// Folder Picker Launcher
	// =============================================================================
	mux.HandleFunc("/api/launch-folder-picker", s.Handlers.Workspace.LaunchFolderPicker)
	mux.HandleFunc("/api/folder-picker/select-path", s.Handlers.Workspace.SelectFolderPath)

	// =============================================================================
	// Workspace Runtime API Endpoints
	// =============================================================================
	// MCP binding routes (Go 1.22+ method patterns)
	mux.HandleFunc("POST /api/workspaces/{workspaceID}/mcp-bindings", s.Handlers.Workspace.CreateMCPBinding)
	mux.HandleFunc("GET /api/workspaces/{workspaceID}/mcp-bindings", s.Handlers.Workspace.ListMCPBindings)
	mux.HandleFunc("GET /api/workspaces/{workspaceID}/mcp-bindings/{bindingID}", s.Handlers.Workspace.GetMCPBinding)
	mux.HandleFunc("PUT /api/workspaces/{workspaceID}/mcp-bindings/{bindingID}", s.Handlers.Workspace.UpdateMCPBinding)
	mux.HandleFunc("PATCH /api/workspaces/{workspaceID}/mcp-bindings/{bindingID}", s.Handlers.Workspace.UpdateMCPBinding)
	mux.HandleFunc("DELETE /api/workspaces/{workspaceID}/mcp-bindings/{bindingID}", s.Handlers.Workspace.DeleteMCPBinding)
	mux.HandleFunc("POST /api/workspaces/{workspaceID}/dependency-actions", s.Handlers.Workspace.ResolveDependencyAction)

	// Agent MCP access routes
	mux.HandleFunc("GET /api/workspaces/{workspaceID}/agent-mcp-access", s.Handlers.Workspace.ListAgentMCPAccess)
	mux.HandleFunc("GET /api/workspaces/{workspaceID}/agent-mcp-access/{agentInstanceID}", s.Handlers.Workspace.GetAgentMCPAccessEntry)
	mux.HandleFunc("PUT /api/workspaces/{workspaceID}/agent-mcp-access/{agentInstanceID}", s.Handlers.Workspace.UpdateAgentMCPAccess)
	mux.HandleFunc("PATCH /api/workspaces/{workspaceID}/agent-mcp-access/{agentInstanceID}", s.Handlers.Workspace.UpdateAgentMCPAccess)
	mux.HandleFunc("DELETE /api/workspaces/{workspaceID}/agent-mcp-access/{agentInstanceID}", s.Handlers.Workspace.DeleteAgentMCPAccess)

	// Skill binding routes
	mux.HandleFunc("POST /api/workspaces/{workspaceID}/skill-bindings", s.Handlers.Workspace.CreateSkillBinding)
	mux.HandleFunc("GET /api/workspaces/{workspaceID}/skill-bindings", s.Handlers.Workspace.ListSkillBindings)
	mux.HandleFunc("GET /api/workspaces/{workspaceID}/skill-bindings/{bindingID}", s.Handlers.Workspace.GetSkillBindingByID)
	mux.HandleFunc("PUT /api/workspaces/{workspaceID}/skill-bindings/{bindingID}", s.Handlers.Workspace.UpdateSkillBinding)
	mux.HandleFunc("PATCH /api/workspaces/{workspaceID}/skill-bindings/{bindingID}", s.Handlers.Workspace.UpdateSkillBinding)
	mux.HandleFunc("DELETE /api/workspaces/{workspaceID}/skill-bindings/{bindingID}", s.Handlers.Workspace.DeleteSkillBinding)

	// Agent skill access routes
	mux.HandleFunc("GET /api/workspaces/{workspaceID}/agent-skill-access", s.Handlers.Workspace.ListAgentSkillAccess)
	mux.HandleFunc("GET /api/workspaces/{workspaceID}/agent-skill-access/{agentInstanceID}", s.Handlers.Workspace.GetAgentSkillAccessEntry)
	mux.HandleFunc("PUT /api/workspaces/{workspaceID}/agent-skill-access/{agentInstanceID}", s.Handlers.Workspace.UpdateAgentSkillAccess)
	mux.HandleFunc("PATCH /api/workspaces/{workspaceID}/agent-skill-access/{agentInstanceID}", s.Handlers.Workspace.UpdateAgentSkillAccess)
	mux.HandleFunc("DELETE /api/workspaces/{workspaceID}/agent-skill-access/{agentInstanceID}", s.Handlers.Workspace.DeleteAgentSkillAccess)

	// Mission routes — workspace-level persistent goal carried out by the
	// Workspace Manager on cadence. See internal/workspace/http_handlers_mission.go.
	mux.HandleFunc("GET /api/workspaces/{workspaceID}/mission", s.Handlers.Workspace.GetMission)
	mux.HandleFunc("PUT /api/workspaces/{workspaceID}/mission", s.Handlers.Workspace.UpdateMission)
	mux.HandleFunc("PATCH /api/workspaces/{workspaceID}/mission", s.Handlers.Workspace.UpdateMission)
	mux.HandleFunc("POST /api/workspaces/{workspaceID}/mission/trigger", s.Handlers.Workspace.TriggerMission)
	mux.HandleFunc("POST /api/workspaces/{workspaceID}/mission/baseline", s.Handlers.Workspace.RunBaselineNow)

	// Native-MCP CLI tooling opt-in (workspace + per-agent). Gates whether a
	// CLI-provider agent may run workspace MCP/built-in tools natively. See
	// internal/workspace/http_handlers_native_mcp.go.
	mux.HandleFunc("GET /api/workspaces/{workspaceID}/native-mcp", s.Handlers.Workspace.GetNativeMCPSettings)
	mux.HandleFunc("PATCH /api/workspaces/{workspaceID}/native-mcp", s.Handlers.Workspace.UpdateNativeMCPWorkspace)
	mux.HandleFunc("PATCH /api/workspaces/{workspaceID}/agents/{name}/native-mcp", s.Handlers.Workspace.UpdateNativeMCPAgent)

	// Action Center — cross-workspace triage of mission opportunities.
	if s.Handlers.ActionCenter != nil {
		mux.HandleFunc("GET /api/action-center/opportunities", s.Handlers.ActionCenter.List)
		mux.HandleFunc("GET /api/action-center/opportunities/{workspaceID}/{opportunityID}", s.Handlers.ActionCenter.Get)
		mux.HandleFunc("POST /api/action-center/opportunities/{workspaceID}/{opportunityID}/dismiss", s.Handlers.ActionCenter.Dismiss)
		mux.HandleFunc("POST /api/action-center/opportunities/{workspaceID}/{opportunityID}/snooze", s.Handlers.ActionCenter.Snooze)
		mux.HandleFunc("POST /api/action-center/opportunities/{workspaceID}/{opportunityID}/resolve", s.Handlers.ActionCenter.Resolve)
	}
}

func (s *Server) handleWorkspaceCollectionAPI(w http.ResponseWriter, r *http.Request) {
	s.Handlers.Session.HandleWorkspaces(w, r)
}

func (s *Server) handleWorkspaceAPI(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	// Check if this is a workspace notes request: /api/workspaces/{id}/notes[/...].
	// Match only when "notes" is the second path segment so that linked-directory
	// file paths with a "notes/" subfolder (e.g. /directories/{id}/files/notes/foo.md)
	// don't get misrouted to the notes handler and rejected as "Invalid path".
	trimmed := strings.TrimPrefix(path, "/api/workspaces/")
	parts := strings.Split(trimmed, "/")
	if len(parts) >= 2 && parts[1] == "notes" {
		s.Handlers.Session.HandleWorkspaceNotes(w, r)
		return
	}
	// Workspace runtime subresources are served only under the canonical
	// /api/workspaces surface.
	if s.routeWorkspaceRuntimeRequest(w, r) {
		return
	}
	// Handle agent management (POST /api/workspaces/{id}/agents, DELETE /api/workspaces/{id}/agents/{name}).
	if strings.Contains(path, "/agents") {
		s.Handlers.Session.HandleWorkspaces(w, r)
		return
	}
	// Handle layout management (GET/PUT /api/workspaces/{id}/layout).
	if strings.Contains(path, "/layout") {
		s.Handlers.Session.HandleWorkspaces(w, r)
		return
	}
	// Otherwise, handle as a regular workspace request.
	s.Handlers.Session.HandleWorkspaces(w, r)
}

func (s *Server) routeWorkspaceRuntimeRequest(w http.ResponseWriter, r *http.Request) bool {
	path := r.URL.Path
	if !strings.HasPrefix(path, "/api/workspaces/") {
		return false
	}
	trimmed := strings.TrimPrefix(path, "/api/workspaces/")
	parts := strings.Split(trimmed, "/")

	if strings.HasSuffix(path, "/events") {
		s.Handlers.Workspace.GetWorkspaceEvents(w, r)
		return true
	}

	if strings.HasSuffix(path, "/output-dir") {
		if r.Method == http.MethodGet {
			s.Handlers.Workspace.GetWorkspaceOutputDir(w, r)
		} else {
			orihttp.MethodNotAllowed(w)
		}
		return true
	}

	if strings.HasSuffix(path, "/output-dir/open") {
		if r.Method == http.MethodPost {
			s.Handlers.Workspace.OpenWorkspaceOutputDir(w, r)
		} else {
			orihttp.MethodNotAllowed(w)
		}
		return true
	}

	if strings.HasSuffix(path, "/agent-snapshots") {
		s.Handlers.Workspace.ListAgentSnapshots(w, r)
		return true
	}

	// Workspace-local agent profiles + in-place model editing.
	//   GET   /api/workspaces/{id}/agents         -> list profiles (model/provider/type)
	//   PATCH /api/workspaces/{id}/agents/{name}  -> update model + provider
	// POST/DELETE fall through to the session handler (add/remove agent).
	if len(parts) >= 2 && parts[1] == "agents" {
		if len(parts) == 2 && r.Method == http.MethodGet {
			s.Handlers.Workspace.ListWorkspaceAgentProfiles(w, r)
			return true
		}
		if len(parts) >= 3 && r.Method == http.MethodPatch {
			s.Handlers.Workspace.UpdateWorkspaceAgentModel(w, r)
			return true
		}
	}

	if strings.Contains(path, "/tasks") {
		if strings.HasSuffix(path, "/execute") && r.Method == http.MethodPost {
			s.Handlers.Workspace.ExecuteTaskManually(w, r)
		} else if strings.HasSuffix(path, "/results/append-csv") && r.Method == http.MethodPost {
			s.Handlers.Workspace.AppendResultToCSV(w, r)
		} else if strings.HasSuffix(path, "/results/export-csv") && r.Method == http.MethodGet {
			s.Handlers.Workspace.ExportResultCSV(w, r)
		} else if strings.HasSuffix(path, "/output-spec/draft") && (r.Method == http.MethodPost || r.Method == http.MethodPatch) {
			s.Handlers.Workspace.SaveTaskOutputSpecDraft(w, r)
		} else if strings.HasSuffix(path, "/output-spec/approve") && r.Method == http.MethodPost {
			s.Handlers.Workspace.ApproveTaskOutputSpecDraft(w, r)
		} else if strings.HasSuffix(path, "/output-spec/discard") && (r.Method == http.MethodPost || r.Method == http.MethodDelete) {
			s.Handlers.Workspace.DiscardTaskOutputSpecDraft(w, r)
		} else if r.Method == http.MethodPost {
			s.Handlers.Workspace.CreateTask(w, r)
		} else if r.Method == http.MethodPatch {
			s.Handlers.Workspace.UpdateTask(w, r)
		} else if r.Method == http.MethodDelete {
			s.Handlers.Workspace.DeleteTask(w, r)
		} else {
			orihttp.MethodNotAllowed(w)
		}
		return true
	}

	if strings.Contains(path, "/attachments") {
		if strings.HasSuffix(path, "/trash") && r.Method == http.MethodPatch {
			s.Handlers.Workspace.MoveToTrash(w, r)
		} else if strings.HasSuffix(path, "/restore") && r.Method == http.MethodPatch {
			s.Handlers.Workspace.RestoreFromTrash(w, r)
		} else if strings.HasSuffix(path, "/relink") && r.Method == http.MethodPost {
			s.Handlers.Workspace.RelinkAttachmentFile(w, r)
		} else if strings.HasSuffix(path, "/locate") && r.Method == http.MethodPatch {
			s.Handlers.Workspace.LocateAttachmentFile(w, r)
		} else if strings.HasSuffix(path, "/move") && r.Method == http.MethodPatch {
			s.Handlers.Workspace.MoveAttachmentFile(w, r)
		} else if strings.HasSuffix(path, "/bulk-trash") && r.Method == http.MethodPost {
			s.Handlers.Workspace.BulkMoveToTrash(w, r)
		} else {
			switch r.Method {
			case http.MethodPost:
				s.Handlers.Workspace.CreateAttachment(w, r)
			case http.MethodPatch:
				s.Handlers.Workspace.UpdateAttachment(w, r)
			case http.MethodDelete:
				s.Handlers.Workspace.DeleteAttachment(w, r)
			default:
				orihttp.MethodNotAllowed(w)
			}
		}
		return true
	}

	if strings.Contains(path, "/trash") {
		if strings.HasSuffix(path, "/trash") && r.Method == http.MethodGet {
			s.Handlers.Workspace.ListTrash(w, r)
		} else if r.Method == http.MethodDelete {
			s.Handlers.Workspace.EmptyTrash(w, r)
		} else {
			orihttp.MethodNotAllowed(w)
		}
		return true
	}

	if strings.Contains(path, "/canvas/store-nodes") {
		if strings.HasSuffix(path, "/status") && r.Method == http.MethodGet {
			s.Handlers.Workspace.GetStoreNodeStatus(w, r)
		} else if r.Method == http.MethodPost {
			s.Handlers.Workspace.CreateStoreNode(w, r)
		} else if r.Method == http.MethodGet {
			s.Handlers.Workspace.GetStoreNodes(w, r)
		} else if r.Method == http.MethodPatch {
			s.Handlers.Workspace.UpdateStoreNode(w, r)
		} else if r.Method == http.MethodDelete {
			s.Handlers.Workspace.DeleteStoreNode(w, r)
		} else {
			orihttp.MethodNotAllowed(w)
		}
		return true
	}

	if strings.Contains(path, "/store-nodes") {
		if strings.HasSuffix(path, "/status") && r.Method == http.MethodGet {
			s.Handlers.Workspace.GetStoreNodeStatus(w, r)
		} else if r.Method == http.MethodPost {
			s.Handlers.Workspace.CreateStoreNode(w, r)
		} else if r.Method == http.MethodGet {
			s.Handlers.Workspace.GetStoreNodes(w, r)
		} else if r.Method == http.MethodPut || r.Method == http.MethodPatch {
			s.Handlers.Workspace.UpdateStoreNode(w, r)
		} else if r.Method == http.MethodDelete {
			s.Handlers.Workspace.DeleteStoreNode(w, r)
		} else {
			orihttp.MethodNotAllowed(w)
		}
		return true
	}

	if len(parts) == 3 && parts[1] == "files" && parts[2] == "tree" {
		if r.Method == http.MethodGet {
			s.Handlers.Workspace.GetWorkspaceFilesTree(w, r)
		} else {
			orihttp.MethodNotAllowed(w)
		}
		return true
	}

	if len(parts) >= 2 && parts[1] == "folders" {
		switch r.Method {
		case http.MethodPost:
			s.Handlers.Workspace.CreateWorkspaceFolder(w, r)
		case http.MethodPatch, http.MethodPut:
			s.Handlers.Workspace.UpdateWorkspaceFolder(w, r)
		case http.MethodDelete:
			s.Handlers.Workspace.DeleteWorkspaceFolder(w, r)
		default:
			orihttp.MethodNotAllowed(w)
		}
		return true
	}

	if len(parts) == 3 && parts[1] == "files" && (parts[2] == "open" || parts[2] == "reveal") {
		if r.Method != http.MethodPost {
			orihttp.MethodNotAllowed(w)
			return true
		}
		if parts[2] == "open" {
			s.Handlers.Workspace.OpenWorkspaceFile(w, r)
		} else {
			s.Handlers.Workspace.RevealWorkspaceFile(w, r)
		}
		return true
	}

	if strings.Contains(path, "/files") && !strings.Contains(path, "/directories") {
		switch r.Method {
		case http.MethodPost:
			s.Handlers.Workspace.UploadFile(w, r)
		case http.MethodGet:
			s.Handlers.Workspace.ServeFile(w, r)
		default:
			orihttp.MethodNotAllowed(w)
		}
		return true
	}

	if strings.Contains(path, "/directories") {
		if strings.Contains(path, "/files/") {
			s.Handlers.Workspace.ReadDirectoryFile(w, r)
		} else if strings.HasSuffix(path, "/files") && r.Method == http.MethodGet {
			s.Handlers.Workspace.ListDirectoryFiles(w, r)
		} else if strings.HasSuffix(path, "/directories") {
			switch r.Method {
			case http.MethodPost:
				s.Handlers.Workspace.CreateDirectory(w, r)
			case http.MethodGet:
				s.Handlers.Workspace.ListDirectories(w, r)
			default:
				orihttp.MethodNotAllowed(w)
			}
		} else {
			switch r.Method {
			case http.MethodGet:
				s.Handlers.Workspace.GetDirectory(w, r)
			case http.MethodPut, http.MethodPatch:
				s.Handlers.Workspace.UpdateDirectory(w, r)
			case http.MethodDelete:
				s.Handlers.Workspace.DeleteDirectory(w, r)
			default:
				orihttp.MethodNotAllowed(w)
			}
		}
		return true
	}

	return false
}
