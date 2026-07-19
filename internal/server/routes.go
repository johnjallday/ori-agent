// Package server provides the HTTP server for the Ori Agent application.
// This file contains all HTTP route registrations organized by domain.
package server

import (
	"encoding/json"
	"net/http"
	"strings"

	agenthttp "github.com/johnjallday/ori-agent/internal/agenthttp"
	"github.com/johnjallday/ori-agent/internal/filehttp"
	"github.com/johnjallday/ori-agent/internal/fileshttp"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/updatehttp"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// registerRoutes registers all HTTP routes for the server, delegating to
// one registration function per domain. Each helper owns exactly the routes
// (and any locally-constructed handlers) for its domain.
func registerRoutes(mux *http.ServeMux, s *Server) {
	registerHealthRoutes(mux, s)
	registerPageRoutes(mux, s)
	registerStaticAssetRoutes(mux, s)
	registerAgentRoutes(mux, s)
	registerSettingsRoutes(mux, s)
	registerChatRoutes(mux, s)
	registerUpdateRoutes(mux, s)
	registerFileParsingRoutes(mux, s)
	registerOnboardingRoutes(mux, s)
	registerDeviceRoutes(mux, s)
	registerUsageRoutes(mux, s)
	registerModelCategoryRoutes(mux, s)
	registerLocationRoutes(mux, s)
	registerMCPRoutes(mux, s)
	registerOrchestrationRoutes(mux, s)
	registerSessionRoutes(mux, s)
	registerReviewRoutes(mux, s)
	registerCLIAgentRoutes(mux, s)
	registerWorkspaceRunRoutes(mux, s)
	registerTriggerRoutes(mux, s)
	registerWorkspaceMemoryRoutes(mux, s)
	registerExternalAgentRoutes(mux, s)
	registerSkillsRoutes(mux, s)
	registerPluginRoutes(mux, s)
	registerFolderPickerRoutes(mux, s)
	registerWorkspaceRuntimeRoutes(mux, s)
	registerActionCenterRoutes(mux, s)
}

// registerHealthRoutes registers health check and diagnostics endpoints.
func registerHealthRoutes(mux *http.ServeMux, s *Server) {
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
}

// registerPageRoutes registers HTML page handlers and legacy redirects.
func registerPageRoutes(mux *http.ServeMux, s *Server) {
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
}

// registerStaticAssetRoutes registers static files, favicon, and agent/avatar file serving.
func registerStaticAssetRoutes(mux *http.ServeMux, s *Server) {
	// =============================================================================
	// Static File Server (CSS, JS, Icons, Assets)
	// =============================================================================
	mux.HandleFunc("/styles.css", s.serveStaticFile)
	mux.HandleFunc("/css/", s.serveStaticFile)
	mux.HandleFunc("/js/", s.serveStaticFile)
	mux.HandleFunc("/icons/", s.serveStaticFile)
	mux.HandleFunc("/fonts/", s.serveStaticFile)
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
}

// registerAgentRoutes registers agent CRUD/dashboard/evolution APIs and home-assistant routing.
func registerAgentRoutes(mux *http.ServeMux, s *Server) {
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
		agentHandler.SetCodexSyncProvider(s.Handlers.ExternalAgents.CodexSyncData)
	}
	if s.Handlers.ModelCategory != nil {
		agentHandler.SetModelCategoryStore(s.Handlers.ModelCategory.Store())
	}
	if s.Handlers.Skills != nil {
		agentHandler.SetSkillsManager(s.Handlers.Skills.Manager())
	}
	avatarHandler := agenthttp.NewAvatarHandler(s.Storage.AgentStore)
	mux.Handle("/api/agents", agentHandler)
	mux.HandleFunc("/api/agents/catalog", agenthttp.CatalogHandler)
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
		dashboardHandler.SetCodexSyncProvider(s.Handlers.ExternalAgents.CodexSyncData)
	}
	mux.HandleFunc("/api/agents/dashboard/list", dashboardHandler.ListAgentsWithStats)
	mux.HandleFunc("/api/agents/dashboard/stats", dashboardHandler.GetDashboardStats)

	// Bounded bulk-operation endpoint (delete / add_tags / remove_tags /
	// set_favorite). Registered as an exact path so it resolves before the
	// generic /api/agents/ subpath handler and is never read as an agent named
	// "bulk" (PRD FR46).
	mux.HandleFunc("/api/agents/bulk", agentHandler.HandleBulk)

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
		// Agent-centric workspace assignment: PUT /api/agents/{name}/workspaces
		if strings.HasSuffix(r.URL.Path, "/workspaces") && r.Method == http.MethodPut {
			agentHandler.AssignWorkspaces(w, r)
			return
		}
		// Regular agent requests - delegate to agentHandler
		agentHandler.ServeHTTP(w, r)
	})

	// Agent capabilities endpoint
	mux.HandleFunc("/api/agents/capabilities", s.Handlers.Orchestration.AgentCapabilitiesHandler)

	// Agent auto-config endpoints
	mux.HandleFunc("/api/agents/auto-config", s.Handlers.AutoConfig.Handle)
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
	if s.Storage.PersonalHQ != nil {
		homeAssistantWorkspaceResolver.SetHQProvider(&homeAssistantHQAdapter{
			service:  s.Storage.PersonalHQ,
			provider: s.Storage.UserProvider,
		})
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
}

// registerSettingsRoutes registers settings, API keys, vault mount, Web3 (capability-gated), and reset.
func registerSettingsRoutes(mux *http.ServeMux, s *Server) {
	caps := s.privateCapabilitiesSnapshot()

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
	mux.HandleFunc("/api/settings/email-oauth", s.Handlers.Settings.EmailOAuthSettingsHandler)
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
}

// registerChatRoutes registers the chat endpoint.
func registerChatRoutes(mux *http.ServeMux, s *Server) {
	// =============================================================================
	// Chat Endpoint
	// =============================================================================
	mux.HandleFunc("/api/chat", s.Handlers.Chat.ChatHandler)
}

// registerUpdateRoutes registers update-check endpoints.
func registerUpdateRoutes(mux *http.ServeMux, s *Server) {
	// =============================================================================
	// Update Management Endpoints
	// =============================================================================
	updateHandler := updatehttp.NewHandler(s.Integration.UpdateManager)
	mux.HandleFunc("/api/updates/check", updateHandler.CheckUpdatesHandler)
	mux.HandleFunc("/api/updates/releases", updateHandler.ListReleasesHandler)
	mux.HandleFunc("/api/updates/version", updateHandler.GetVersionHandler)
}

// registerFileParsingRoutes registers file parse/upload/content endpoints.
func registerFileParsingRoutes(mux *http.ServeMux, s *Server) {
	// =============================================================================
	// File Parsing Endpoint
	// =============================================================================
	fileHandler := filehttp.NewHandler()
	mux.HandleFunc("/api/files/parse", fileHandler.ParseFileHandler)
	mux.HandleFunc("/api/files/upload", fileHandler.UploadFileHandler)
	mux.HandleFunc("/api/files/content", fileHandler.GetFileHandler)
}

// registerOnboardingRoutes registers onboarding, smart onboarding, user profile, theme, and notes-behavior preferences.
func registerOnboardingRoutes(mux *http.ServeMux, s *Server) {
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

	// Onboarding progression (quest log)
	if s.Handlers.Progression != nil {
		mux.HandleFunc("/api/progression", s.Handlers.Progression.GetStatus)
		mux.HandleFunc("/api/progression/dismiss", s.Handlers.Progression.Dismiss)
		mux.HandleFunc("/api/progression/skip", s.Handlers.Progression.Skip)
		mux.HandleFunc("/api/progression/reset", s.Handlers.Progression.Reset)
	}

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

	registerPersonalHQRoutes(mux, s)
	registerDailyBriefRoutes(mux, s)

	// Theme endpoints
	mux.HandleFunc("/api/theme", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			s.Handlers.Onboarding.GetTheme(w, r)
		case http.MethodPost:
			s.Handlers.Onboarding.SetTheme(w, r)
		default:
			orihttp.MethodNotAllowed(w)
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
}

// registerDeviceRoutes registers device info/capability endpoints.
func registerDeviceRoutes(mux *http.ServeMux, s *Server) {
	mux.HandleFunc("/api/device/info", s.Handlers.Device.GetDeviceInfo)
	mux.HandleFunc("/api/device/type", s.Handlers.Device.SetDeviceType)
	mux.HandleFunc("/api/device/wifi/current", s.Handlers.Device.GetCurrentWiFi)
	mux.HandleFunc("/api/device/ollama", s.Handlers.Device.GetOllamaStatus)
	mux.HandleFunc("/api/device/capabilities", s.Handlers.Device.GetCapabilities)
	mux.HandleFunc("/api/device/detect-hardware", s.Handlers.Device.DetectHardware)
}

// registerUsageRoutes registers usage and cost tracking endpoints.
func registerUsageRoutes(mux *http.ServeMux, s *Server) {
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
}

// registerModelCategoryRoutes registers model category and auto-categorize endpoints.
func registerModelCategoryRoutes(mux *http.ServeMux, s *Server) {
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
		mux.HandleFunc("/api/models/auto-categorize", s.Handlers.AutoCategorize.Suggest)
	}
}

// registerLocationRoutes registers location zone management endpoints.
func registerLocationRoutes(mux *http.ServeMux, s *Server) {
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
}

// registerMCPRoutes registers MCP server management and registry endpoints.
func registerMCPRoutes(mux *http.ServeMux, s *Server) {
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
}

// registerOrchestrationRoutes registers orchestration, custom workflows, notifications, events, and scheduled tasks.
func registerOrchestrationRoutes(mux *http.ServeMux, s *Server) {
	// =============================================================================
	// Orchestration Endpoints
	// =============================================================================
	mux.HandleFunc("/api/orchestration/workspace", s.Handlers.Orchestration.WorkspaceHandler)
	mux.HandleFunc("/api/orchestration/workspace/activate", s.Handlers.Orchestration.WorkspaceActivateHandler)
	mux.HandleFunc("/api/orchestration/workspace/agents", s.Handlers.Orchestration.WorkspaceAgentsHandler)
	mux.HandleFunc("/api/orchestration/workspace/layout", s.Handlers.Orchestration.SaveLayoutHandler)
	mux.HandleFunc("/api/orchestration/workspace/station-layout", s.Handlers.Orchestration.SaveStationLayoutHandler)
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
}

// registerSessionRoutes registers sessions, session files, notes, folders, workspaces, project templates, and tags.
func registerSessionRoutes(mux *http.ServeMux, s *Server) {
	// =============================================================================
	// Session Management Endpoints (including Session Files)
	// =============================================================================
	if s.Handlers.Session != nil {
		// Cleanup, stats, and bulk operations routes (must be registered before the wildcard routes)
		mux.HandleFunc("/api/sessions/cleanup", s.Handlers.Session.HandleCleanup)
		mux.HandleFunc("/api/sessions/stats", s.Handlers.Session.HandleStorageStats)
		mux.HandleFunc("/api/sessions/bulk", s.Handlers.Session.HandleBulkDeleteSessions)

		// Pre-create REAPER Setup preview for the Reaper Song template (no
		// workspace id yet). Per-workspace readiness is dispatched under
		// /api/workspaces/{id}/reaper-setup by the session handler.
		mux.HandleFunc("/api/reaper-setup/preview", s.Handlers.Session.GetReaperCreatePreview)

		// Auto-classify route (must be registered before the wildcard routes)
		if s.Handlers.AutoClassify != nil {
			mux.HandleFunc("/api/sessions/auto-classify", s.Handlers.AutoClassify.HandleAutoClassify)
		}

		if s.Handlers.SmartInput != nil {
			mux.HandleFunc("/api/smart-input/classify", s.Handlers.SmartInput.HandleClassify)
			mux.HandleFunc("/api/smart-input/override", s.Handlers.SmartInput.HandleOverride)
		}

		// Session files use explicit Go 1.22 method+pattern routes. They are
		// strictly more specific than the "/api/sessions/" subtree below, so
		// ServeMux dispatches file requests to them and leaves session CRUD,
		// tags, and tasks to the session handler.
		if s.Handlers.SessionFiles != nil {
			fileshttp.RegisterRoutes(mux, s.Handlers.SessionFiles)
		}

		mux.HandleFunc("/api/sessions/", s.Handlers.Session.HandleSessions)
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
		// All workspace runtime routes are explicit Go 1.22 method+patterns,
		// strictly more specific than the /api/workspaces/ subtree above, so
		// ServeMux dispatches them directly. Unmatched requests fall through
		// handleWorkspaceAPI to the session workspace handler (CRUD, layout).
		if s.Handlers.Workspace != nil {
			workspace.RegisterRoutes(mux, s.Handlers.Workspace)
		}

		// Closed prompt-variable vocabulary + preview (template authoring UI)
		mux.HandleFunc("GET /api/prompt-variables", s.handlePromptVariablesList)
		mux.HandleFunc("POST /api/prompt-variables/preview", s.handlePromptVariablesPreview)

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
		// Default tool bindings (skills / MCP servers / plugins), applied if present at creation
		mux.HandleFunc("PUT /api/project-templates/{templateID}/tools", s.handleProjectTemplateToolsSet)
		// Agent roster (first = entry agent, rest = specialists), seeded at creation
		mux.HandleFunc("PUT /api/project-templates/{templateID}/agents", s.handleProjectTemplateAgentsSet)

		mux.HandleFunc("/api/tags", s.Handlers.Session.HandleTags)
		mux.HandleFunc("/api/tags/usage", s.Handlers.Session.HandleTagUsage)
		mux.HandleFunc("/api/tags/rename", s.Handlers.Session.HandleTagRename)
		mux.HandleFunc("/api/tags/delete", s.Handlers.Session.HandleTagDelete)
		mux.HandleFunc("/api/session-cache/stats", s.Handlers.Session.HandleCacheStats)
	}
}

// registerReviewRoutes registers conversation review endpoints.
func registerReviewRoutes(mux *http.ServeMux, s *Server) {
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
}

// registerCLIAgentRoutes registers CLI agent adapter endpoints.
func registerCLIAgentRoutes(mux *http.ServeMux, s *Server) {
	// =============================================================================
	// CLI Agent Adapter Endpoints
	// =============================================================================
	if s.Handlers.CLIAgents != nil {
		mux.HandleFunc("/api/cli-agents", s.Handlers.CLIAgents.HandleListAgents)
		mux.HandleFunc("/api/cli-agents/tasks", s.Handlers.CLIAgents.HandleCreateTask)
		mux.HandleFunc("/api/cli-agents/tasks/", s.Handlers.CLIAgents.HandleGetTask)
	}
}

// registerWorkspaceRunRoutes registers workspace mission-run endpoints.
func registerWorkspaceRunRoutes(mux *http.ServeMux, s *Server) {
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
}

// registerTriggerRoutes registers event trigger and webhook endpoints.
func registerTriggerRoutes(mux *http.ServeMux, s *Server) {
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
}

// registerPersonalHQRoutes registers Personal HQ status/designation endpoints.
func registerPersonalHQRoutes(mux *http.ServeMux, s *Server) {
	// =============================================================================
	// Personal HQ Endpoints
	// =============================================================================
	if s.Handlers.PersonalHQ != nil {
		mux.HandleFunc("GET /api/personal-hq/status", s.Handlers.PersonalHQ.Status)
		mux.HandleFunc("POST /api/personal-hq/onboarding-state", s.Handlers.PersonalHQ.SetOnboardingState)
		mux.HandleFunc("POST /api/personal-hq/setup", s.Handlers.PersonalHQ.Setup)
		mux.HandleFunc("POST /api/personal-hq/designate", s.Handlers.PersonalHQ.Designate)
		mux.HandleFunc("POST /api/personal-hq/replace", s.Handlers.PersonalHQ.Replace)
		mux.HandleFunc("POST /api/personal-hq/clear", s.Handlers.PersonalHQ.Clear)
		mux.HandleFunc("GET /api/personal-hq/upgrade/preview", s.Handlers.PersonalHQ.UpgradePreview)
		mux.HandleFunc("POST /api/personal-hq/upgrade/apply", s.Handlers.PersonalHQ.UpgradeApply)
		mux.HandleFunc("GET /api/personal-hq/email/status", s.Handlers.PersonalHQ.MailboxStatusHandler)
		mux.HandleFunc("POST /api/personal-hq/email/link", s.Handlers.PersonalHQ.LinkMailbox)
		mux.HandleFunc("POST /api/personal-hq/email/unlink", s.Handlers.PersonalHQ.UnlinkMailbox)
		mux.HandleFunc("POST /api/personal-hq/mail/draft", s.Handlers.PersonalHQ.DraftReply)
		mux.HandleFunc("GET /api/personal-hq/mail/proposals", s.Handlers.PersonalHQ.ListProposals)
		mux.HandleFunc("POST /api/personal-hq/mail/edit", s.Handlers.PersonalHQ.EditReply)
		mux.HandleFunc("POST /api/personal-hq/mail/cancel", s.Handlers.PersonalHQ.CancelReply)
		mux.HandleFunc("POST /api/personal-hq/mail/confirm", s.Handlers.PersonalHQ.ConfirmSend)
		mux.HandleFunc("GET /api/personal-hq/followups", s.Handlers.PersonalHQ.ListFollowUps)
		mux.HandleFunc("GET /api/personal-hq/followups/home", s.Handlers.PersonalHQ.HomeFollowUps)
		mux.HandleFunc("POST /api/personal-hq/followups", s.Handlers.PersonalHQ.CreateFollowUp)
		mux.HandleFunc("POST /api/personal-hq/followups/confirm", s.Handlers.PersonalHQ.ConfirmFollowUp)
		mux.HandleFunc("POST /api/personal-hq/followups/edit", s.Handlers.PersonalHQ.EditFollowUp)
		mux.HandleFunc("POST /api/personal-hq/followups/snooze", s.Handlers.PersonalHQ.SnoozeFollowUp)
		mux.HandleFunc("POST /api/personal-hq/followups/complete", s.Handlers.PersonalHQ.CompleteFollowUp)
		mux.HandleFunc("POST /api/personal-hq/followups/dismiss", s.Handlers.PersonalHQ.DismissFollowUp)
		mux.HandleFunc("POST /api/personal-hq/followups/reopen", s.Handlers.PersonalHQ.ReopenFollowUp)
		mux.HandleFunc("GET /api/personal-hq/journal/propose", s.Handlers.PersonalHQ.ProposeJournal)
		mux.HandleFunc("POST /api/personal-hq/journal/save", s.Handlers.PersonalHQ.SaveJournal)
		mux.HandleFunc("POST /api/personal-hq/journal/dismiss", s.Handlers.PersonalHQ.DismissJournal)
	}
}

// registerDailyBriefRoutes registers Personal HQ Daily Brief endpoints.
func registerDailyBriefRoutes(mux *http.ServeMux, s *Server) {
	// =============================================================================
	// Daily Brief Endpoints
	// =============================================================================
	if s.Handlers.DailyBrief != nil {
		mux.HandleFunc("GET /api/personal-hq/brief/config", s.Handlers.DailyBrief.GetConfig)
		mux.HandleFunc("PUT /api/personal-hq/brief/config", s.Handlers.DailyBrief.UpdateConfig)
		mux.HandleFunc("GET /api/personal-hq/brief/current", s.Handlers.DailyBrief.GetCurrent)
		mux.HandleFunc("GET /api/personal-hq/brief/history", s.Handlers.DailyBrief.GetHistory)
		mux.HandleFunc("GET /api/personal-hq/brief/status", s.Handlers.DailyBrief.GetStatus)
		mux.HandleFunc("POST /api/personal-hq/brief/open", s.Handlers.DailyBrief.RequestFirstOpen)
		mux.HandleFunc("POST /api/personal-hq/brief/refresh", s.Handlers.DailyBrief.RequestRefresh)
	}
}

// registerWorkspaceMemoryRoutes registers workspace MEMORY.md endpoints.
func registerWorkspaceMemoryRoutes(mux *http.ServeMux, s *Server) {
	// =============================================================================
	// Workspace Memory (MEMORY.md) Endpoints
	// =============================================================================
	if s.Handlers.WorkspaceMemory != nil {
		mux.HandleFunc("GET /api/workspaces/{workspaceID}/memory", s.Handlers.WorkspaceMemory.GetMemory)
		mux.HandleFunc("POST /api/workspaces/{workspaceID}/memory/entries", s.Handlers.WorkspaceMemory.AddEntry)
		mux.HandleFunc("PUT /api/workspaces/{workspaceID}/memory/entries/{index}", s.Handlers.WorkspaceMemory.UpdateEntry)
		mux.HandleFunc("DELETE /api/workspaces/{workspaceID}/memory/entries/{index}", s.Handlers.WorkspaceMemory.DeleteEntry)
	}
}

// registerExternalAgentRoutes registers external agent (Claude Code / Codex) endpoints.
func registerExternalAgentRoutes(mux *http.ServeMux, s *Server) {
	// =============================================================================
	// External Agents (Claude Code, Codex) Endpoints
	// =============================================================================
	if s.Handlers.ExternalAgents != nil {
		mux.HandleFunc("/api/external-agents", s.Handlers.ExternalAgents.GetAll)
		mux.HandleFunc("/api/external-agents/claude", s.Handlers.ExternalAgents.GetClaude)
		mux.HandleFunc("/api/external-agents/codex", s.Handlers.ExternalAgents.GetCodex)
		mux.HandleFunc("/api/external-agents/refresh", s.Handlers.ExternalAgents.Refresh)
	}
}

// registerSkillsRoutes registers skills endpoints.
func registerSkillsRoutes(mux *http.ServeMux, s *Server) {
	// =============================================================================
	// Skills Endpoints
	// =============================================================================
	if s.Handlers.Skills != nil {
		mux.HandleFunc("/api/skills", s.Handlers.Skills.List)
		mux.HandleFunc("/api/skills/", s.Handlers.Skills.Handle)
	}
}

// registerPluginRoutes registers plugin bundle endpoints.
func registerPluginRoutes(mux *http.ServeMux, s *Server) {
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
}

// registerFolderPickerRoutes registers folder picker launcher endpoints.
func registerFolderPickerRoutes(mux *http.ServeMux, s *Server) {
	// =============================================================================
	// Folder Picker Launcher
	// =============================================================================
	mux.HandleFunc("/api/launch-folder-picker", s.Handlers.Workspace.LaunchFolderPicker)
	mux.HandleFunc("/api/folder-picker/select-path", s.Handlers.Workspace.SelectFolderPath)
}

// registerWorkspaceRuntimeRoutes registers workspace runtime subresources: MCP/skill bindings, agent access, mission, native-MCP.
func registerWorkspaceRuntimeRoutes(mux *http.ServeMux, s *Server) {
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

	// Per-instance refinement of a shared agent definition (role / description /
	// custom_instructions) scoped to this workspace only; never mutates the
	// global definition (PRD FR18). Handled by the session handler which owns
	// the workspace AgentInstances.
	if s.Handlers.Session != nil {
		mux.HandleFunc("PATCH /api/workspaces/{workspaceID}/agents/{name}/instance-settings", s.Handlers.Session.UpdateWorkspaceAgentInstanceSettings)
		// Resolved effective prompt inspector (base + per-workspace refinement).
		mux.HandleFunc("GET /api/workspaces/{workspaceID}/agents/{name}/effective-prompt", s.Handlers.Session.GetWorkspaceAgentEffectivePrompt)
	}

	// Editable base system prompt for a workspace-local agent, stored in the
	// workspace config.json (the source of truth for these agents). Handled by
	// the workspace handler which owns config.json access.
	mux.HandleFunc("GET /api/workspaces/{workspaceID}/agents/{name}/system-prompt", s.Handlers.Workspace.GetWorkspaceAgentSystemPrompt)
	mux.HandleFunc("PATCH /api/workspaces/{workspaceID}/agents/{name}/system-prompt", s.Handlers.Workspace.UpdateWorkspaceAgentSystemPrompt)
}

// registerActionCenterRoutes registers cross-workspace Action Center triage endpoints.
func registerActionCenterRoutes(mux *http.ServeMux, s *Server) {
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
	// Runtime subresources (attachments, tasks, store-nodes, files, folders,
	// directories, agents, project-open, events, output-dir, ...) are served by
	// explicit Go 1.22 ServeMux patterns registered via workspace.RegisterRoutes.
	// Everything else (workspace CRUD, agent add/remove, layout) is served by the
	// session workspace handler.
	s.Handlers.Session.HandleWorkspaces(w, r)
}
