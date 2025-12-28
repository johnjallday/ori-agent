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
	mux.HandleFunc("/studios/", s.handleStudiosRoutes) // Dynamic route handler
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
	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/favicon.svg", http.StatusMovedPermanently)
	})

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

	// Agent auto-config endpoints
	mux.HandleFunc("/api/agents/auto-config", s.autoConfigHandler.AutoConfigHandler)
	mux.HandleFunc("/api/agents/auto-config/availability", s.autoConfigHandler.CheckLLMAvailabilityHandler)

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
	mux.HandleFunc("/api/plugins/updates/status", s.pluginUpdateHandler.HandleGetUpdateStatus)
	mux.HandleFunc("/api/plugins/backups", s.pluginUpdateHandler.HandleListBackups)
	mux.HandleFunc("/api/plugins/backups/clean", s.pluginUpdateHandler.HandleCleanBackups)

	// Plugin upload endpoint (must be before catch-all /api/plugins/ pattern)
	mux.HandleFunc("/api/plugins/upload", s.pluginHandler.ServeHTTP)

	// Dedicated plugins page endpoints (must be before catch-all /api/plugins/ pattern)
	mux.HandleFunc("/api/plugins/notifications", s.notificationsHandler.HandleGetNotifications)

	// Plugin-specific routes with pattern matching
	mux.HandleFunc("/api/plugins/", func(w http.ResponseWriter, r *http.Request) {
		// Check if this is a notification dismiss request
		if strings.Contains(r.URL.Path, "/notifications/") && strings.HasSuffix(r.URL.Path, "/dismiss") {
			s.notificationsHandler.HandleDismissNotification(w, r)
			return
		}
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
		// Check if this is an enable endpoint
		if strings.HasSuffix(r.URL.Path, "/enable") {
			s.pluginsPageHandler.HandleEnablePlugin(w, r)
			return
		}
		// Check if this is a disable endpoint
		if strings.HasSuffix(r.URL.Path, "/disable") {
			s.pluginsPageHandler.HandleDisablePlugin(w, r)
			return
		}
		// Check if this is an update endpoint
		if strings.HasSuffix(r.URL.Path, "/update") {
			s.pluginUpdateHandler.HandleUpdatePlugin(w, r)
			return
		}
		// Check if this is a rollback endpoint (new dedicated handler)
		if strings.HasSuffix(r.URL.Path, "/rollback") {
			s.rollbackHandler.HandleRollbackPlugin(w, r)
			return
		}
		// Check if this is a config endpoint
		if strings.HasSuffix(r.URL.Path, "/config") {
			if r.Method == http.MethodPut {
				// PUT - update config
				s.pluginsPageHandler.HandleUpdatePluginConfig(w, r)
			} else {
				// GET - fetch config info (delegated to init handler)
				s.pluginInitHandler.PluginInitHandler(w, r)
			}
			return
		}
		// Check if this is a test endpoint
		if strings.HasSuffix(r.URL.Path, "/test") {
			s.pluginsPageHandler.HandleTestPlugin(w, r)
			return
		}
		// Check if this is a logs endpoint
		if strings.HasSuffix(r.URL.Path, "/logs") {
			s.pluginsPageHandler.HandleGetPluginLogs(w, r)
			return
		}
		// Check if this is a reload endpoint
		if strings.HasSuffix(r.URL.Path, "/reload") {
			s.pluginsPageHandler.HandleReloadPlugin(w, r)
			return
		}
		// Check if this is an agents endpoint
		if strings.HasSuffix(r.URL.Path, "/agents") {
			s.pluginsPageHandler.HandleGetPluginAgents(w, r)
			return
		}
		// Check if this is a permissions request
		if strings.Contains(r.URL.Path, "/permissions") {
			if strings.HasSuffix(r.URL.Path, "/approve") {
				s.permissionsHandler.HandleApprovePermissions(w, r)
			} else {
				s.permissionsHandler.HandleGetPermissions(w, r)
			}
			return
		}
		// Check if this is a delete request
		if r.Method == http.MethodDelete {
			s.pluginsPageHandler.HandleDeletePlugin(w, r)
			return
		}
		// Check if this is a plugin details request (GET with plugin name)
		if r.Method == http.MethodGet && !strings.HasSuffix(r.URL.Path, "/plugins/") {
			s.pluginsPageHandler.HandleGetPluginDetails(w, r)
			return
		}
		// Otherwise, delegate to init handler
		s.pluginInitHandler.PluginInitHandler(w, r)
	})

	// Reuse the plugin handler instance
	mux.HandleFunc("/api/plugins/save-settings", s.pluginHandler.ServeHTTP)
	mux.HandleFunc("/api/plugins/tool-call", s.pluginHandler.DirectToolCallHandler)

	// Tags endpoints for the plugins management UI
	mux.HandleFunc("/api/plugins/tags", s.pluginsPageHandler.HandleListPluginTags)
	mux.HandleFunc("/api/plugins/tags/", s.pluginsPageHandler.HandleListPluginsByTag)

	// Main plugins list endpoint - route based on query parameters
	mux.HandleFunc("/api/plugins", func(w http.ResponseWriter, r *http.Request) {
		// If there's a 'management' query parameter, use the new handler
		if r.URL.Query().Get("management") == "true" {
			s.pluginsPageHandler.HandleListPlugins(w, r)
			return
		}
		// Otherwise use the original handler for backward compatibility
		s.pluginHandler.ServeHTTP(w, r)
	})

	// =============================================================================
	// Settings and Configuration Endpoints
	// =============================================================================
	mux.HandleFunc("/api/settings", s.settingsHandler.SettingsHandler)
	mux.HandleFunc("/api/api-key", s.settingsHandler.APIKeyHandler)
	mux.HandleFunc("/api/providers", s.settingsHandler.ProvidersHandler)
	mux.HandleFunc("/api/settings/system-model", s.settingsHandler.SystemModelHandler)
	mux.HandleFunc("/api/settings/available-models", s.settingsHandler.AvailableModelsHandler)

	// Reset endpoints
	mux.HandleFunc("/api/reset", s.resetHandler.HandleReset)
	mux.HandleFunc("/api/reset/preview", s.resetHandler.GetResetPreview)

	// =============================================================================
	// Marketplace Management Endpoints
	// =============================================================================
	if s.marketplaceHandler != nil {
		mux.HandleFunc("/api/marketplaces", func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				s.marketplaceHandler.ListMarketplaces(w, r)
			case http.MethodPost:
				s.marketplaceHandler.AddMarketplace(w, r)
			default:
				orihttp.MethodNotAllowed(w)
			}
		})
		mux.HandleFunc("/api/marketplaces/reorder", s.marketplaceHandler.ReorderMarketplaces)
		mux.HandleFunc("/api/marketplaces/test", s.marketplaceHandler.TestMarketplace)
		mux.HandleFunc("/api/marketplaces/", func(w http.ResponseWriter, r *http.Request) {
			// Handle /api/marketplaces/{id} and /api/marketplaces/{id}/refresh
			if strings.HasSuffix(r.URL.Path, "/refresh") {
				s.marketplaceHandler.RefreshMarketplace(w, r)
				return
			}
			switch r.Method {
			case http.MethodPut:
				s.marketplaceHandler.UpdateMarketplace(w, r)
			case http.MethodDelete:
				s.marketplaceHandler.DeleteMarketplace(w, r)
			default:
				orihttp.MethodNotAllowed(w)
				// =============================================================================
				// Chat Endpoint
				// =============================================================================
			}
		})
	}

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
	mux.HandleFunc("/api/files/upload", fileHandler.UploadFileHandler)
	mux.HandleFunc("/api/files/content", fileHandler.GetFileHandler)

	// =============================================================================
	// Onboarding Endpoints
	// =============================================================================
	mux.HandleFunc("/api/onboarding/status", s.onboardingHandler.GetStatus)
	mux.HandleFunc("/api/onboarding/step", s.onboardingHandler.CompleteStep)
	mux.HandleFunc("/api/onboarding/skip", s.onboardingHandler.Skip)
	mux.HandleFunc("/api/onboarding/complete", s.onboardingHandler.Complete)
	mux.HandleFunc("/api/onboarding/reset", s.onboardingHandler.Reset)

	// Smart onboarding endpoints (AI-powered profile inference)
	mux.HandleFunc("/api/onboarding/detect", s.smartOnboardingHandler.Detect)
	mux.HandleFunc("/api/onboarding/profile", s.smartOnboardingHandler.InferProfile)
	mux.HandleFunc("/api/onboarding/describe", s.smartOnboardingHandler.Describe)
	mux.HandleFunc("/api/onboarding/config", s.smartOnboardingHandler.GenerateConfig)
	mux.HandleFunc("/api/onboarding/apply-config", s.smartOnboardingHandler.Apply)
	mux.HandleFunc("/api/onboarding/update-profile", s.smartOnboardingHandler.UpdateProfile)

	// Theme endpoints
	mux.HandleFunc("/api/theme", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			s.onboardingHandler.GetTheme(w, r)
		case http.MethodPost:
			s.onboardingHandler.SetTheme(w, r)
		default:
			orihttp.MethodNotAllowed(w)
			// =============================================================================
			// Device Endpoints
			// =============================================================================
		}
	})

	mux.HandleFunc("/api/device/info", s.deviceHandler.GetDeviceInfo)
	mux.HandleFunc("/api/device/type", s.deviceHandler.SetDeviceType)
	mux.HandleFunc("/api/device/wifi/current", s.deviceHandler.GetCurrentWiFi)
	mux.HandleFunc("/api/device/ollama", s.deviceHandler.GetOllamaStatus)

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
	// Model Category Endpoints
	// =============================================================================
	if s.modelCategoryHandler != nil {
		mux.HandleFunc("/api/model-categories", s.modelCategoryHandler.CategoriesHandler)
		mux.HandleFunc("/api/model-categories/reorder", s.modelCategoryHandler.ReorderCategoriesHandler)
		mux.HandleFunc("/api/model-categories/view-preference", s.modelCategoryHandler.SetViewPreferenceHandler)
		mux.HandleFunc("/api/model-categories/", s.modelCategoryHandler.CategoryHandler)
		mux.HandleFunc("/api/models/", func(w http.ResponseWriter, r *http.Request) {
			// Handle model category assignments
			if strings.HasSuffix(r.URL.Path, "/categories") {
				s.modelCategoryHandler.SetModelAssignmentsHandler(w, r)
				return
			}
			// Otherwise, 404
			orihttp.NotFound(w, "Not found")
		})
	}

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
			orihttp.MethodNotAllowed(w)
		}
	})
	mux.HandleFunc("/api/location/zones/", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			s.locationHandler.UpdateZone(w, r)
		case http.MethodDelete:
			s.locationHandler.DeleteZone(w, r)
		default:
			orihttp.MethodNotAllowed(w)
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
			orihttp.MethodNotAllowed(w)
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
			orihttp.NotFound(w, "Not found")
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

	// =============================================================================
	// Custom Workflow API Endpoints
	// =============================================================================
	// List workflows or create new workflow
	mux.HandleFunc("/api/workflows", s.workflowHandler.WorkflowsHandler)
	// Get, delete specific workflow, or check agents
	mux.HandleFunc("/api/workflows/", s.workflowHandler.WorkflowHandler)

	// Notification endpoints
	mux.HandleFunc("/api/orchestration/notifications", s.orchestrationHandler.NotificationsHandler)
	mux.HandleFunc("/api/orchestration/notifications/stream", s.orchestrationHandler.NotificationStreamHandler)

	// Event history endpoint
	mux.HandleFunc("/api/orchestration/events", s.orchestrationHandler.EventHistoryHandler)

	// Scheduled task endpoints
	mux.HandleFunc("/api/orchestration/scheduled-tasks", s.orchestrationHandler.ScheduledTasksHandler)
	mux.HandleFunc("/api/orchestration/scheduled-tasks/", s.orchestrationHandler.ScheduledTaskHandler)

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
					s.orchestrationHandler.SchedulerNodeTriggerHandler(w, r)
					return
				}

				// Regular node operations (GET/PUT/DELETE)
				s.orchestrationHandler.SchedulerNodeHandler(w, r)
			} else {
				// No node ID: /workspaces/{id}/scheduler-nodes (list/create)
				s.orchestrationHandler.SchedulerNodesHandler(w, r)
			}
			return
		}

		// Fall through to other workspace endpoints (handled elsewhere)
		http.NotFound(w, r)
	})

	// =============================================================================
	// Session Management Endpoints (including Session Files)
	// =============================================================================
	if s.sessionHandler != nil {
		// Cleanup and stats routes (must be registered before the wildcard routes)
		mux.HandleFunc("/api/sessions/cleanup", s.sessionHandler.HandleCleanup)
		mux.HandleFunc("/api/sessions/stats", s.sessionHandler.HandleStorageStats)
		mux.HandleFunc("/api/sessions/", func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.Path

			// Session files routes (check if files handler is available)
			if s.sessionFilesHandler != nil {
				if strings.Contains(path, "/files/upload") && r.Method == http.MethodPost {
					s.sessionFilesHandler.UploadFile(w, r)
					return
				}
				if strings.Contains(path, "/files/link") && r.Method == http.MethodPost {
					s.sessionFilesHandler.LinkFile(w, r)
					return
				}
				if strings.Contains(path, "/files/validate") && r.Method == http.MethodPost {
					s.sessionFilesHandler.ValidateLinks(w, r)
					return
				}
				if strings.Contains(path, "/files/events") && r.Method == http.MethodGet {
					s.sessionFilesHandler.FileEvents(w, r)
					return
				}
				if strings.Contains(path, "/files/watch") {
					if r.Method == http.MethodPost {
						s.sessionFilesHandler.StartWatching(w, r)
					} else if r.Method == http.MethodDelete {
						s.sessionFilesHandler.StopWatching(w, r)
					}
					return
				}
				if strings.Contains(path, "/folder/open") && r.Method == http.MethodPost {
					s.sessionFilesHandler.OpenFolder(w, r)
					return
				}

				// File-specific routes (with file ID)
				if strings.Contains(path, "/files/") {
					if strings.HasSuffix(path, "/download") {
						s.sessionFilesHandler.DownloadFile(w, r)
						return
					}
					if strings.HasSuffix(path, "/relink") && r.Method == http.MethodPost {
						s.sessionFilesHandler.RelinkFile(w, r)
						return
					}
					if r.Method == http.MethodDelete {
						s.sessionFilesHandler.DeleteFile(w, r)
						return
					}
					if r.Method == http.MethodGet {
						s.sessionFilesHandler.GetFile(w, r)
						return
					}
				}

				// List files route
				if strings.HasSuffix(path, "/files") && r.Method == http.MethodGet {
					s.sessionFilesHandler.ListFiles(w, r)
					return
				}
			}

			// Fall through to session handler
			s.sessionHandler.HandleSessions(w, r)
		})
		mux.HandleFunc("/api/sessions", s.sessionHandler.HandleSessions)

		// Notes search must be before the wildcard /api/notes/
		mux.HandleFunc("/api/notes/search", s.sessionHandler.HandleNotes)
		mux.HandleFunc("/api/notes/", s.sessionHandler.HandleNotes)
		mux.HandleFunc("/api/notes", s.sessionHandler.HandleNotes)

		// Folder routes - note that /api/folders/{id}/notes needs to be handled
		mux.HandleFunc("/api/folders/", func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.Path
			// Check if this is a folder notes request
			if strings.Contains(path, "/notes") {
				s.sessionHandler.HandleFolderNotes(w, r)
				return
			}
			// Otherwise, handle as regular folder request
			s.sessionHandler.HandleFolders(w, r)
		})
		mux.HandleFunc("/api/folders", s.sessionHandler.HandleFolders)
		mux.HandleFunc("/api/tags", s.sessionHandler.HandleTags)
		mux.HandleFunc("/api/session-cache/stats", s.sessionHandler.HandleCacheStats)
	}

	// =============================================================================
	// Agent Studio API Endpoints
	// =============================================================================
	mux.HandleFunc("/api/studios", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			s.studioHandler.CreateStudio(w, r)
		case http.MethodGet:
			s.studioHandler.ListStudios(w, r)
		default:
			orihttp.MethodNotAllowed(w)
			// Handle routes with studio ID
		}
	})

	mux.HandleFunc("/api/studios/", func(w http.ResponseWriter, r *http.Request) {
		// Parse the path to determine which handler to use
		if strings.HasSuffix(r.URL.Path, "/events") {
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
				orihttp.MethodNotAllowed(w)
			}
		} else if strings.Contains(r.URL.Path, "/attachments") {

			switch r.Method {
			case http.MethodPost:
				s.studioHandler.CreateAttachment(w, r)
			case http.MethodPatch:
				s.studioHandler.UpdateAttachment(w, r)
			case http.MethodDelete:
				s.studioHandler.DeleteAttachment(w, r)
			default:
				orihttp.MethodNotAllowed(w)
			}
			// Handle canvas store node operations (must be before /store-nodes check)
		} else if strings.Contains(r.URL.Path, "/canvas/store-nodes") {

			if strings.HasSuffix(r.URL.Path, "/status") && r.Method == http.MethodGet {
				s.studioHandler.GetStoreNodeStatus(w, r)
			} else if r.Method == http.MethodPost {
				s.studioHandler.CreateStoreNode(w, r)
			} else if r.Method == http.MethodGet {
				s.studioHandler.GetStoreNodes(w, r)
			} else if r.Method == http.MethodPatch {
				s.studioHandler.UpdateStoreNode(w, r)
			} else if r.Method == http.MethodDelete {
				s.studioHandler.DeleteStoreNode(w, r)
			} else {
				orihttp.MethodNotAllowed(w)
			}
		} else if strings.Contains(r.URL.Path, "/store-nodes") {

			if strings.HasSuffix(r.URL.Path, "/status") && r.Method == http.MethodGet {
				s.studioHandler.GetStoreNodeStatus(w, r)
			} else if r.Method == http.MethodPost {
				s.studioHandler.CreateStoreNode(w, r)
			} else if r.Method == http.MethodGet {
				s.studioHandler.GetStoreNodes(w, r)
			} else if r.Method == http.MethodPut || r.Method == http.MethodPatch {
				s.studioHandler.UpdateStoreNode(w, r)
			} else if r.Method == http.MethodDelete {
				s.studioHandler.DeleteStoreNode(w, r)
			} else {
				orihttp.MethodNotAllowed(w)
			}
			// Handle agent add/remove operations
		} else if strings.Contains(r.URL.Path, "/agents") {

			switch r.Method {
			case http.MethodPost:
				s.studioHandler.AddAgent(w, r)
			case http.MethodDelete:
				s.studioHandler.RemoveAgent(w, r)
			default:
				orihttp.MethodNotAllowed(w)
			}
		} else {
			s.studioHandler.GetStudio(w, r)
		}
	})
}
