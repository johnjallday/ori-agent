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

	// =============================================================================
	// Page Handlers (HTML Pages)
	// =============================================================================
	mux.HandleFunc("/", s.serveIndex)
	mux.HandleFunc("/settings", s.serveSettings)
	mux.HandleFunc("/marketplace", s.serveMarketplace)
	mux.HandleFunc("/plugins", s.servePlugins)
	mux.HandleFunc("/skills", s.serveSkills)
	mux.HandleFunc("/workflows", s.serveWorkflows)
	mux.HandleFunc("/mcp", s.serveMCP)
	mux.HandleFunc("/models", s.serveModels)
	mux.HandleFunc("/agents", s.serveAgents)      // Clean URL
	mux.HandleFunc("/agents.html", s.serveAgents) // Legacy support
	mux.HandleFunc("/agents-detail.html", s.serveAgentsDetail)
	mux.HandleFunc("/agents-create.html", s.serveStaticFile)
	mux.HandleFunc("/agents-edit.html", s.serveAgentsEdit)
	mux.HandleFunc("/agents-dashboard", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/agents", http.StatusFound)
	})
	// Workspaces page routes (primary)
	mux.HandleFunc("/workspaces/", s.handleWorkspacesRoutes) // Dynamic route handler for /workspaces/{id}
	mux.HandleFunc("/workspaces", s.serveWorkspaces)
	// Legacy /studios routes (redirect to /workspaces)
	mux.HandleFunc("/studios/", func(w http.ResponseWriter, r *http.Request) {
		// Redirect /studios/{path} to /workspaces/{path}
		newPath := strings.Replace(r.URL.Path, "/studios/", "/workspaces/", 1)
		http.Redirect(w, r, newPath, http.StatusMovedPermanently)
	})
	mux.HandleFunc("/studios", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/workspaces", http.StatusMovedPermanently)
	})
	mux.HandleFunc("/usage", s.serveUsage)
	mux.HandleFunc("/review", s.serveReview)

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
	avatarHandler := agenthttp.NewAvatarHandler(s.Storage.AgentStore)
	mux.Handle("/api/agents", agentHandler)

	// Dashboard handlers
	dashboardHandler := agenthttp.NewDashboardHandler(s.Storage.AgentStore)
	dashboardHandler.ActivityLogger = s.Handlers.ActivityLogger
	mux.HandleFunc("/api/agents/dashboard/list", dashboardHandler.ListAgentsWithStats)
	mux.HandleFunc("/api/agents/dashboard/stats", dashboardHandler.GetDashboardStats)

	// Agent MCP handlers
	s.Handlers.AgentMCP = agenthttp.NewMCPHandler(s.Integration.MCPRegistry, s.Integration.MCPConfigManager, agentHandler)
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
		// Route avatar upload/delete requests
		if strings.Contains(r.URL.Path, "/avatar") {
			avatarHandler.ServeHTTP(w, r)
			return
		}
		// Route agent MCP-specific requests
		if strings.Contains(r.URL.Path, "/mcp-servers") {
			if strings.HasSuffix(r.URL.Path, "/enable") {
				s.Handlers.AgentMCP.EnableAgentMCPServerHandler(w, r)
			} else if strings.HasSuffix(r.URL.Path, "/disable") {
				s.Handlers.AgentMCP.DisableAgentMCPServerHandler(w, r)
			} else {
				// List MCP servers for agent
				s.Handlers.AgentMCP.ListAgentMCPServersHandler(w, r)
			}
		} else {
			// Regular agent requests - delegate to agentHandler
			agentHandler.ServeHTTP(w, r)
		}
	})

	// Agent capabilities endpoint
	mux.HandleFunc("/api/agents/capabilities", s.Handlers.Orchestration.AgentCapabilitiesHandler)

	// Agent auto-config endpoints
	mux.HandleFunc("/api/agents/auto-config", s.Handlers.AutoConfig.AutoConfigHandler)
	mux.HandleFunc("/api/agents/auto-config/availability", s.Handlers.AutoConfig.CheckLLMAvailabilityHandler)

	// =============================================================================
	// Plugin API Endpoints
	// =============================================================================
	mux.HandleFunc("/api/plugin-registry", s.Handlers.PluginRegistry.PluginRegistryHandler)
	mux.HandleFunc("/api/plugin-updates", s.Handlers.PluginRegistry.PluginUpdatesHandler)
	mux.HandleFunc("/api/plugins/download", s.Handlers.PluginRegistry.PluginDownloadHandler)
	mux.HandleFunc("/api/plugins/updates/check", s.Handlers.PluginRegistry.PluginUpdatesCheckHandler)
	mux.HandleFunc("/api/plugins/execute", s.Handlers.PluginInit.PluginExecuteHandler)
	mux.HandleFunc("/api/plugins/init-status", s.Handlers.PluginInit.PluginInitStatusHandler)

	// Plugin health check endpoints (must be before catch-all /api/plugins/ pattern)
	mux.HandleFunc("/api/plugins/health", s.Handlers.Health.HandleAllPluginsHealth)
	mux.HandleFunc("/api/plugins/check-updates", s.Handlers.PluginUpdate.HandleCheckUpdates)
	mux.HandleFunc("/api/plugins/updates/status", s.Handlers.PluginUpdate.HandleGetUpdateStatus)
	mux.HandleFunc("/api/plugins/backups", s.Handlers.PluginUpdate.HandleListBackups)
	mux.HandleFunc("/api/plugins/backups/clean", s.Handlers.PluginUpdate.HandleCleanBackups)

	// Plugin upload endpoint (must be before catch-all /api/plugins/ pattern)
	mux.HandleFunc("/api/plugins/upload", s.Handlers.Plugin.ServeHTTP)

	// Dedicated plugins page endpoints (must be before catch-all /api/plugins/ pattern)
	mux.HandleFunc("/api/plugins/notifications", s.Handlers.Notifications.HandleGetNotifications)

	// Plugin-specific routes with pattern matching
	mux.HandleFunc("/api/plugins/", s.routePluginRequest)

	// Reuse the plugin handler instance
	mux.HandleFunc("/api/plugins/save-settings", s.Handlers.Plugin.ServeHTTP)
	mux.HandleFunc("/api/plugins/tool-call", s.Handlers.Plugin.DirectToolCallHandler)

	// Tags endpoints for the plugins management UI
	mux.HandleFunc("/api/plugins/tags", s.Handlers.PluginsPage.HandleListPluginTags)
	mux.HandleFunc("/api/plugins/tags/", s.Handlers.PluginsPage.HandleListPluginsByTag)

	// Main plugins list endpoint - route based on query parameters
	mux.HandleFunc("/api/plugins", func(w http.ResponseWriter, r *http.Request) {
		// If there's a 'management' query parameter, use the new handler
		if r.URL.Query().Get("management") == "true" {
			s.Handlers.PluginsPage.HandleListPlugins(w, r)
			return
		}
		// Otherwise use the original handler for backward compatibility
		s.Handlers.Plugin.ServeHTTP(w, r)
	})

	// =============================================================================
	// Settings and Configuration Endpoints
	// =============================================================================
	mux.HandleFunc("/api/settings", s.Handlers.Settings.SettingsHandler)
	mux.HandleFunc("/api/api-key", s.Handlers.Settings.APIKeyHandler)
	mux.HandleFunc("/api/providers", s.Handlers.Settings.ProvidersHandler)
	mux.HandleFunc("/api/settings/system-model", s.Handlers.Settings.SystemModelHandler)
	mux.HandleFunc("/api/settings/available-models", s.Handlers.Settings.AvailableModelsHandler)
	mux.HandleFunc("/api/settings/system-paths", s.Handlers.Settings.SystemPathsHandler)
	mux.HandleFunc("/api/settings/external-agents", s.Handlers.Settings.ExternalAgentsSettingsHandler)
	mux.HandleFunc("/api/settings/speech", s.Handlers.Settings.SpeechSettingsHandler)
	mux.HandleFunc("/api/transcribe", s.Handlers.Speech.Transcribe)

	// Web3 Wallet endpoints
	if caps.Web3Wallet {
		mux.HandleFunc("/api/web3-wallet", s.Handlers.Settings.Web3WalletHandler)
		mux.HandleFunc("/api/web3-chains", s.Handlers.Settings.Web3ChainsHandler)
	}

	// Reset endpoints
	mux.HandleFunc("/api/reset", s.Handlers.Reset.HandleReset)
	mux.HandleFunc("/api/reset/preview", s.Handlers.Reset.GetResetPreview)

	// =============================================================================
	// Marketplace Management Endpoints
	// =============================================================================
	if s.Handlers.Marketplace != nil {
		mux.HandleFunc("/api/marketplaces", func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				s.Handlers.Marketplace.ListMarketplaces(w, r)
			case http.MethodPost:
				s.Handlers.Marketplace.AddMarketplace(w, r)
			default:
				orihttp.MethodNotAllowed(w)
			}
		})
		mux.HandleFunc("/api/marketplaces/reorder", s.Handlers.Marketplace.ReorderMarketplaces)
		mux.HandleFunc("/api/marketplaces/test", s.Handlers.Marketplace.TestMarketplace)
		mux.HandleFunc("/api/marketplaces/", func(w http.ResponseWriter, r *http.Request) {
			// Handle /api/marketplaces/{id} and /api/marketplaces/{id}/refresh
			if strings.HasSuffix(r.URL.Path, "/refresh") {
				s.Handlers.Marketplace.RefreshMarketplace(w, r)
				return
			}
			switch r.Method {
			case http.MethodPut:
				s.Handlers.Marketplace.UpdateMarketplace(w, r)
			case http.MethodDelete:
				s.Handlers.Marketplace.DeleteMarketplace(w, r)
			default:
				orihttp.MethodNotAllowed(w)
				// =============================================================================
				// Chat Endpoint
				// =============================================================================
			}
		})
	}

	mux.HandleFunc("/api/chat", s.Handlers.Chat.ChatHandler)

	// =============================================================================
	// Update Management Endpoints
	// =============================================================================
	updateHandler := updatehttp.NewHandler(s.Integration.UpdateManager)
	mux.HandleFunc("/api/updates/check", updateHandler.CheckUpdatesHandler)
	mux.HandleFunc("/api/updates/releases", updateHandler.ListReleasesHandler)
	mux.HandleFunc("/api/updates/download", updateHandler.DownloadUpdateHandler)
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
	mux.HandleFunc("/api/onboarding/recommend-plugins", s.Handlers.SmartOnboarding.RecommendPlugins)

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
	mux.HandleFunc("/api/mcp/servers", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			s.Handlers.MCP.ListServersHandler(w, r)
		case http.MethodPost:
			s.Handlers.MCP.AddServerHandler(w, r)
		default:
			orihttp.MethodNotAllowed(w)
		}
	})
	mux.HandleFunc("/api/mcp/servers/", func(w http.ResponseWriter, r *http.Request) {
		// Check for specific actions in the path
		if strings.HasSuffix(r.URL.Path, "/enable") {
			s.Handlers.MCP.EnableServerHandler(w, r)
		} else if strings.HasSuffix(r.URL.Path, "/disable") {
			s.Handlers.MCP.DisableServerHandler(w, r)
		} else if strings.HasSuffix(r.URL.Path, "/tools") {
			s.Handlers.MCP.GetServerToolsHandler(w, r)
		} else if strings.HasSuffix(r.URL.Path, "/status") {
			s.Handlers.MCP.GetServerStatusHandler(w, r)
		} else if strings.HasSuffix(r.URL.Path, "/test") {
			s.Handlers.MCP.TestConnectionHandler(w, r)
		} else if strings.HasSuffix(r.URL.Path, "/retry") {
			s.Handlers.MCP.RetryConnectionHandler(w, r)
		} else if r.Method == http.MethodDelete {
			s.Handlers.MCP.RemoveServerHandler(w, r)
		} else {
			orihttp.NotFound(w, "Not found")
		}
	})
	mux.HandleFunc("/api/mcp/import", s.Handlers.MCP.ImportServersHandler)
	mux.HandleFunc("/api/mcp/marketplace", s.Handlers.MCP.GetMarketplaceServersHandler)

	// =============================================================================
	// Orchestration Endpoints
	// =============================================================================
	mux.HandleFunc("/api/orchestration/workspace", s.Handlers.Orchestration.WorkspaceHandler)
	mux.HandleFunc("/api/orchestration/workspace/agents", s.Handlers.Orchestration.WorkspaceAgentsHandler)
	mux.HandleFunc("/api/orchestration/workspace/layout", s.Handlers.Orchestration.SaveLayoutHandler)
	mux.HandleFunc("/api/orchestration/messages", s.Handlers.Orchestration.MessagesHandler)
	mux.HandleFunc("/api/orchestration/delegate", s.Handlers.Orchestration.DelegateHandler)
	mux.HandleFunc("/api/orchestration/dynamic-agents/approve", s.Handlers.Orchestration.DynamicAgentApprovalHandler)
	mux.HandleFunc("/api/orchestration/tasks", s.Handlers.Orchestration.TasksHandler)
	mux.HandleFunc("/api/orchestration/tasks/bulk", s.Handlers.Orchestration.BulkDeleteTasksHandler)
	mux.HandleFunc("/api/orchestration/tasks/execute", s.Handlers.Orchestration.ExecuteTaskHandler)
	if s.Handlers.AutoTask != nil {
		mux.HandleFunc("/api/orchestration/tasks/auto-parse", s.Handlers.AutoTask.HandleAutoTask)
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

	// Scheduled task endpoints
	mux.HandleFunc("/api/orchestration/scheduled-tasks", s.Handlers.Orchestration.ScheduledTasksHandler)
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
		}
		mux.HandleFunc("/api/notes/", s.Handlers.Session.HandleNotes)
		mux.HandleFunc("/api/notes", s.Handlers.Session.HandleNotes)

		// Legacy folder routes (redirect to workspace routes)
		mux.HandleFunc("/api/folders/", func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.Path
			// Check if this is a workspace notes request
			if strings.Contains(path, "/notes") {
				s.Handlers.Session.HandleWorkspaceNotes(w, r)
				return
			}
			// Otherwise, handle as regular workspace request
			s.Handlers.Session.HandleWorkspaces(w, r)
		})
		mux.HandleFunc("/api/folders", s.Handlers.Session.HandleWorkspaces)

		// Workspace routes (unified workspace API)
		mux.HandleFunc("/api/workspaces", s.Handlers.Session.HandleWorkspaces)
		mux.HandleFunc("/api/workspaces/", func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.Path
			// Check if this is a workspace notes request
			if strings.Contains(path, "/notes") {
				s.Handlers.Session.HandleWorkspaceNotes(w, r)
				return
			}
			// Handle agent management (POST /api/workspaces/{id}/agents, DELETE /api/workspaces/{id}/agents/{name})
			if strings.Contains(path, "/agents") {
				s.Handlers.Session.HandleWorkspaces(w, r)
				return
			}
			// Handle layout management (GET/PUT /api/workspaces/{id}/layout)
			if strings.Contains(path, "/layout") {
				s.Handlers.Session.HandleWorkspaces(w, r)
				return
			}
			// Otherwise, handle as regular workspace request
			s.Handlers.Session.HandleWorkspaces(w, r)
		})

		mux.HandleFunc("/api/tags", s.Handlers.Session.HandleTags)
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
	// Folder Picker Launcher
	// =============================================================================
	mux.HandleFunc("/api/launch-folder-picker", s.Handlers.Studio.LaunchFolderPicker)

	// =============================================================================
	// Agent Studio API Endpoints
	// =============================================================================
	mux.HandleFunc("/api/studios", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			s.Handlers.Studio.CreateStudio(w, r)
		case http.MethodGet:
			s.Handlers.Studio.ListStudios(w, r)
		default:
			orihttp.MethodNotAllowed(w)
			// Handle routes with studio ID
		}
	})

	mux.HandleFunc("/api/studios/", func(w http.ResponseWriter, r *http.Request) {
		// Parse the path to determine which handler to use
		if strings.HasSuffix(r.URL.Path, "/events") {
			s.Handlers.Studio.GetStudioEvents(w, r)
		} else if strings.Contains(r.URL.Path, "/tasks") {
			// Handle task operations
			if strings.HasSuffix(r.URL.Path, "/execute") && r.Method == http.MethodPost {
				s.Handlers.Studio.ExecuteTaskManually(w, r)
			} else if r.Method == http.MethodPost {
				s.Handlers.Studio.CreateTask(w, r)
			} else if r.Method == http.MethodPatch {
				s.Handlers.Studio.UpdateTask(w, r)
			} else if r.Method == http.MethodDelete {
				s.Handlers.Studio.DeleteTask(w, r)
			} else {
				orihttp.MethodNotAllowed(w)
			}
		} else if strings.Contains(r.URL.Path, "/trash") {
			// Handle trash operations
			if strings.HasSuffix(r.URL.Path, "/trash") && r.Method == http.MethodGet {
				s.Handlers.Studio.ListTrash(w, r)
			} else if r.Method == http.MethodDelete {
				s.Handlers.Studio.EmptyTrash(w, r)
			} else {
				orihttp.MethodNotAllowed(w)
			}
		} else if strings.Contains(r.URL.Path, "/attachments") {
			// Handle attachment trash operations
			if strings.HasSuffix(r.URL.Path, "/trash") && r.Method == http.MethodPatch {
				s.Handlers.Studio.MoveToTrash(w, r)
			} else if strings.HasSuffix(r.URL.Path, "/restore") && r.Method == http.MethodPatch {
				s.Handlers.Studio.RestoreFromTrash(w, r)
			} else if strings.HasSuffix(r.URL.Path, "/bulk-trash") && r.Method == http.MethodPost {
				s.Handlers.Studio.BulkMoveToTrash(w, r)
			} else {
				switch r.Method {
				case http.MethodPost:
					s.Handlers.Studio.CreateAttachment(w, r)
				case http.MethodPatch:
					s.Handlers.Studio.UpdateAttachment(w, r)
				case http.MethodDelete:
					s.Handlers.Studio.DeleteAttachment(w, r)
				default:
					orihttp.MethodNotAllowed(w)
				}
			}
			// Handle canvas store node operations (must be before /store-nodes check)
		} else if strings.Contains(r.URL.Path, "/canvas/store-nodes") {

			if strings.HasSuffix(r.URL.Path, "/status") && r.Method == http.MethodGet {
				s.Handlers.Studio.GetStoreNodeStatus(w, r)
			} else if r.Method == http.MethodPost {
				s.Handlers.Studio.CreateStoreNode(w, r)
			} else if r.Method == http.MethodGet {
				s.Handlers.Studio.GetStoreNodes(w, r)
			} else if r.Method == http.MethodPatch {
				s.Handlers.Studio.UpdateStoreNode(w, r)
			} else if r.Method == http.MethodDelete {
				s.Handlers.Studio.DeleteStoreNode(w, r)
			} else {
				orihttp.MethodNotAllowed(w)
			}
		} else if strings.Contains(r.URL.Path, "/store-nodes") {

			if strings.HasSuffix(r.URL.Path, "/status") && r.Method == http.MethodGet {
				s.Handlers.Studio.GetStoreNodeStatus(w, r)
			} else if r.Method == http.MethodPost {
				s.Handlers.Studio.CreateStoreNode(w, r)
			} else if r.Method == http.MethodGet {
				s.Handlers.Studio.GetStoreNodes(w, r)
			} else if r.Method == http.MethodPut || r.Method == http.MethodPatch {
				s.Handlers.Studio.UpdateStoreNode(w, r)
			} else if r.Method == http.MethodDelete {
				s.Handlers.Studio.DeleteStoreNode(w, r)
			} else {
				orihttp.MethodNotAllowed(w)
			}
			// Handle workspace file upload/serving (must be before /directories which has its own /files)
		} else if strings.Contains(r.URL.Path, "/files") && !strings.Contains(r.URL.Path, "/directories") {
			// /api/studios/:id/files - file upload or /api/studios/:id/files/:filename - file serving
			switch r.Method {
			case http.MethodPost:
				s.Handlers.Studio.UploadFile(w, r)
			case http.MethodGet:
				s.Handlers.Studio.ServeFile(w, r)
			default:
				orihttp.MethodNotAllowed(w)
			}
			// Handle directory reference operations
		} else if strings.Contains(r.URL.Path, "/directories") {
			// Check for /files/ path to read file content
			if strings.Contains(r.URL.Path, "/files/") {
				s.Handlers.Studio.ReadDirectoryFile(w, r)
			} else if strings.HasSuffix(r.URL.Path, "/files") && r.Method == http.MethodGet {
				s.Handlers.Studio.ListDirectoryFiles(w, r)
			} else if strings.HasSuffix(r.URL.Path, "/directories") {
				// /api/studios/:id/directories
				switch r.Method {
				case http.MethodPost:
					s.Handlers.Studio.CreateDirectory(w, r)
				case http.MethodGet:
					s.Handlers.Studio.ListDirectories(w, r)
				default:
					orihttp.MethodNotAllowed(w)
				}
			} else {
				// /api/studios/:id/directories/:dir_id
				switch r.Method {
				case http.MethodGet:
					s.Handlers.Studio.GetDirectory(w, r)
				case http.MethodPut, http.MethodPatch:
					s.Handlers.Studio.UpdateDirectory(w, r)
				case http.MethodDelete:
					s.Handlers.Studio.DeleteDirectory(w, r)
				default:
					orihttp.MethodNotAllowed(w)
				}
			}
			// Handle agent add/remove operations
		} else if strings.Contains(r.URL.Path, "/agents") {

			switch r.Method {
			case http.MethodPost:
				s.Handlers.Studio.AddAgent(w, r)
			case http.MethodDelete:
				s.Handlers.Studio.RemoveAgent(w, r)
			default:
				orihttp.MethodNotAllowed(w)
			}
		} else {
			s.Handlers.Studio.GetStudio(w, r)
		}
	})
}

// routePluginRequest handles routing for /api/plugins/ requests using suffix-based matching
func (s *Server) routePluginRequest(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	// Exact path matches
	if path == "/api/plugins/all-pages" {
		s.Handlers.WebPage.ListAllPages(w, r)
		return
	}

	// Notification dismiss has special pattern: /notifications/{id}/dismiss
	if strings.Contains(path, "/notifications/") && strings.HasSuffix(path, "/dismiss") {
		s.Handlers.Notifications.HandleDismissNotification(w, r)
		return
	}

	// Suffix-based routing for plugin-specific endpoints
	suffixHandlers := map[string]http.HandlerFunc{
		"/pages":            s.Handlers.WebPage.ListPages,
		"/health":           s.Handlers.Health.HandlePluginHealth,
		"/enable":           s.Handlers.PluginsPage.HandleEnablePlugin,
		"/disable":          s.Handlers.PluginsPage.HandleDisablePlugin,
		"/update":           s.Handlers.PluginUpdate.HandleUpdatePlugin,
		"/default-settings": s.Handlers.PluginInit.PluginInitHandler,
		"/test":             s.Handlers.PluginsPage.HandleTestPlugin,
		"/logs":             s.Handlers.PluginsPage.HandleGetPluginLogs,
		"/reload":           s.Handlers.PluginsPage.HandleReloadPlugin,
		"/agents":           s.Handlers.PluginsPage.HandleGetPluginAgents,
	}

	for suffix, handler := range suffixHandlers {
		if strings.HasSuffix(path, suffix) {
			handler(w, r)
			return
		}
	}

	// Config endpoint has method-specific handling
	if strings.HasSuffix(path, "/config") {
		if r.Method == http.MethodPut {
			s.Handlers.PluginsPage.HandleUpdatePluginConfig(w, r)
		} else {
			s.Handlers.PluginInit.PluginInitHandler(w, r)
		}
		return
	}

	// Permissions endpoint has sub-route
	if strings.Contains(path, "/permissions") {
		if strings.HasSuffix(path, "/approve") {
			s.Handlers.Permissions.HandleApprovePermissions(w, r)
		} else {
			s.Handlers.Permissions.HandleGetPermissions(w, r)
		}
		return
	}

	// DELETE method routes to delete handler
	if r.Method == http.MethodDelete {
		s.Handlers.PluginsPage.HandleDeletePlugin(w, r)
		return
	}

	// GET requests: check if it's a web page or plugin details
	if r.Method == http.MethodGet && !strings.HasSuffix(path, "/plugins/") {
		pathAfterPlugins := strings.TrimPrefix(path, "/api/plugins/")
		if strings.Contains(pathAfterPlugins, "/") {
			// Has sub-path, try serving as web page
			s.Handlers.WebPage.ServeHTTP(w, r)
			return
		}
		// No sub-path, serve plugin details
		s.Handlers.PluginsPage.HandleGetPluginDetails(w, r)
		return
	}

	// Default: delegate to init handler
	s.Handlers.PluginInit.PluginInitHandler(w, r)
}
