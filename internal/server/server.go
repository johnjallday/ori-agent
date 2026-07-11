package server

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/cliagent"
	"github.com/johnjallday/ori-agent/internal/featureflags"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/platform"
	"github.com/johnjallday/ori-agent/internal/privateservices"
	web "github.com/johnjallday/ori-agent/internal/web"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Server holds all the dependencies and state for the HTTP server
// Dependencies are organized into domain-specific facades to reduce coupling
type Server struct {
	// Domain facades (grouped dependencies) - PUBLIC API
	Core        *CoreSystemFacade
	Storage     *StorageSystemFacade
	Workflow    *WorkflowSystemFacade
	Integration *IntegrationSystemFacade
	UI          *UISystemFacade
	Handlers    *HandlerFacade
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
	s.cleanupStaleWorkspaceManagerAgents()

	if s.Workflow != nil {
		s.Workflow.Start()
	}
}

// cleanupStaleWorkspaceManagerAgents removes leftover workspace-manager agents
// that were auto-created by the legacy system. These agents are identified by
// having the "workspace-manager" metadata tag or type.
func (s *Server) cleanupStaleWorkspaceManagerAgents() {
	if s.Storage == nil || s.Storage.AgentStore == nil {
		return
	}

	agentStore := s.Storage.AgentStore
	for _, name := range agentStore.ListAgents() {
		ag, ok := agentStore.GetAgent(name)
		if !ok || ag == nil {
			continue
		}
		if !isStaleWorkspaceManagerAgent(ag) {
			continue
		}
		if err := agentStore.DeleteAgent(name); err != nil {
			logger.Warn("Failed to delete stale workspace-manager agent", logger.Fields{
				"agent": name,
				"error": err,
			})
		} else {
			logger.Info("Deleted stale workspace-manager agent", logger.Fields{
				"agent": name,
			})
		}
	}
}

func isStaleWorkspaceManagerAgent(ag *agent.Agent) bool {
	if ag == nil {
		return false
	}
	if ag.Type == "workspace-manager" {
		return true
	}
	if ag.Metadata != nil {
		for _, tag := range ag.Metadata.Tags {
			if strings.EqualFold(strings.TrimSpace(tag), "workspace-manager") {
				return true
			}
		}
	}
	return false
}

// Shutdown gracefully shuts down background services
func (s *Server) Shutdown() {
	// Stop background services
	if s.Workflow != nil {
		s.Workflow.Shutdown()
	}
	if s.Handlers != nil && s.Handlers.SessionFiles != nil {
		if watcher := s.Handlers.SessionFiles.Watcher(); watcher != nil {
			_ = watcher.Close()
		}
	}

	// Shutdown folder picker if running
	workspace.ShutdownFolderPicker()

	// Shutdown gateway
	if s.Core != nil && s.Core.Gateway != nil {
		_ = s.Core.Gateway.Shutdown(context.Background())
	}
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
	if s == nil || s.Integration == nil || s.Integration.PrivateServices == nil {
		return privateservices.NoopClient{}.Capabilities()
	}
	return s.Integration.PrivateServices.Capabilities()
}

func resolveDefaultPageAgentName(storage *StorageSystemFacade) string {
	if storage == nil {
		return ""
	}
	if agent, found := storage.GetAgentByName("Ori"); found && agent != nil {
		return "Ori"
	}
	names := storage.ListAgents()
	if len(names) == 0 {
		return ""
	}
	return strings.TrimSpace(names[0])
}

// prepareBasePageData prepares common page data with theme and Assistant context.
func (s *Server) prepareBasePageData(pageName string) web.TemplateData {
	data := web.GetDefaultData()
	data.CurrentPage = pageName
	data.Theme = s.Storage.OnboardingMgr.GetTheme()

	caps := s.privateCapabilitiesSnapshot()
	data.Extra["Web3Enabled"] = caps.Web3Wallet
	data.Extra["MarketplacePaymentsEnabled"] = caps.MarketplacePayments
	data.Extra["TokenPayoutsEnabled"] = caps.TokenPayouts
	data.Extra["EvolutionEnabled"] = featureflags.EvolutionEnabled()

	defaultAgentName := resolveDefaultPageAgentName(s.Storage)
	data.Extra["DefaultAgentName"] = defaultAgentName
	if defaultAgentName != "" {
		if agent, found := s.Storage.GetAgentByName(defaultAgentName); found && agent != nil && agent.Settings.Model != "" {
			data.Model = agent.Settings.Model
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

	// Inject home-dashboard context: the workspace count drives the adaptive
	// layout (first-run wizard vs returning-user dashboard sections). We
	// compute it server-side so the template can render the correct shell
	// without a flash of empty-state from a client-side fetch.
	workspaceCount := 0
	if s.Storage != nil && s.Storage.WorkspaceStore != nil {
		if ids, err := s.Storage.WorkspaceStore.List(); err == nil {
			workspaceCount = len(ids)
		} else {
			logger.Warn("serveIndex: failed to list workspaces for first-run check", logger.Fields{"err": err})
		}
	}
	data.Extra["WorkspaceCount"] = workspaceCount
	data.Extra["IsFirstRun"] = workspaceCount == 0

	s.renderAndWritePage(w, "index", data)
}

func (s *Server) serveAgents(w http.ResponseWriter, r *http.Request) {
	data := s.prepareBasePageData("agents")
	data.ShowSidebarToggle = true // Enable sidebar toggle
	// The roster/stage redesign is now the default Agents page (G5). The classic
	// dashboard remains reachable at ?view=classic as a fallback.
	if r.URL.Query().Get("view") == "classic" {
		s.renderAndWritePage(w, "agents", data)
		return
	}
	s.renderAndWritePage(w, "agents-roster", data)
}

// serveActionCenter renders the cross-workspace Action Center page. The page
// only depends on the action-center JS module; all data comes from the
// /api/action-center/opportunities endpoints.
func (s *Server) serveActionCenter(w http.ResponseWriter, r *http.Request) {
	data := s.prepareBasePageData("action-center")
	data.Title = "Action Center - Ori Agent"
	data.ShowSidebarToggle = true
	s.renderAndWritePage(w, "action-center", data)
}

func (s *Server) serveAgentsDetail(w http.ResponseWriter, r *http.Request) {
	data := s.prepareBasePageData("agents")
	data.Title = "Agent Details - Ori Agent"
	data.BrandText = "Ori Agent"
	data.ShowSidebarToggle = true
	s.renderAndWritePage(w, "agents-detail", data)
}

// isClaudeCodeAgentName reports whether the given agent name refers to the
// built-in Claude Code CLI agent and that backend is currently available.
func (s *Server) isClaudeCodeAgentName(name string) bool {
	if s.Handlers == nil || s.Handlers.CLIAgentRegistry == nil {
		return false
	}
	lower := strings.ToLower(strings.TrimSpace(name))
	if lower != "claude code" && lower != cliagent.BackendClaude {
		return false
	}
	return s.Handlers.CLIAgentRegistry.IsAvailable(cliagent.BackendClaude)
}

// isCodexAgentName reports whether the given agent name refers to the built-in
// Codex CLI agent and that backend is currently available.
func (s *Server) isCodexAgentName(name string) bool {
	if s.Handlers == nil || s.Handlers.CLIAgentRegistry == nil {
		return false
	}
	lower := strings.ToLower(strings.TrimSpace(name))
	if lower != "codex" && lower != cliagent.BackendCodex {
		return false
	}
	return s.Handlers.CLIAgentRegistry.IsAvailable(cliagent.BackendCodex)
}

// serveClaudeAgentDetail serves the dedicated, read-only Claude Code agent page
// that mirrors the user's ~/.claude state.
func (s *Server) serveClaudeAgentDetail(w http.ResponseWriter, _ *http.Request) {
	data := s.prepareBasePageData("agents")
	data.Title = "Claude Code - Ori Agent"
	data.BrandText = "Ori Agent"
	data.ShowSidebarToggle = true
	s.renderAndWritePage(w, "agents-claude-detail", data)
}

// serveCodexAgentDetail serves the dedicated, read-only Codex agent page that
// mirrors the user's ~/.codex state.
func (s *Server) serveCodexAgentDetail(w http.ResponseWriter, _ *http.Request) {
	data := s.prepareBasePageData("agents")
	data.Title = "Codex - Ori Agent"
	data.BrandText = "Ori Agent"
	data.ShowSidebarToggle = true
	s.renderAndWritePage(w, "agents-codex-detail", data)
}

func (s *Server) serveAgentsEdit(w http.ResponseWriter, r *http.Request) {
	agentName := strings.TrimSpace(r.URL.Query().Get("name"))
	if agentName == "" {
		http.Redirect(w, r, "/agents", http.StatusFound)
		return
	}
	http.Redirect(w, r, "/agents/"+url.PathEscape(agentName), http.StatusFound)
}

func (s *Server) serveAgentsCreate(w http.ResponseWriter, r *http.Request) {
	data := s.prepareBasePageData("agents")
	data.Title = "Create Agent - Ori Agent"
	data.BrandText = "Ori Agent"
	data.ShowSidebarToggle = true
	s.renderAndWritePage(w, "agents-create", data)
}

func (s *Server) serveSettings(w http.ResponseWriter, r *http.Request) {
	data := s.prepareBasePageData("settings")

	// Add platform information for system info display
	currentPlatform := platform.DetectPlatform()
	currentPlatformDisplay := platform.GetPlatformDisplayName(currentPlatform)
	data.Extra["CurrentPlatform"] = currentPlatform
	data.Extra["CurrentPlatformDisplay"] = currentPlatformDisplay

	s.renderAndWritePage(w, "settings", data)
}

func (s *Server) serveProfile(w http.ResponseWriter, r *http.Request) {
	data := s.prepareBasePageData("profile")
	data.Title = "Profile - Ori Agent"
	data.BrandText = "Ori Agent"
	data.ShowSidebarToggle = true
	s.renderAndWritePage(w, "profile", data)
}

func (s *Server) serveVault(w http.ResponseWriter, r *http.Request) {
	data := s.prepareBasePageData("vault")
	data.Title = "Private Vaults - Ori Agent"
	data.BrandText = "Ori Agent"
	data.ShowSidebarToggle = true
	s.renderAndWritePage(w, "vault", data)
}

func (s *Server) serveMCP(w http.ResponseWriter, r *http.Request) {
	data := s.prepareBasePageData("mcp")
	s.renderAndWritePage(w, "mcp", data)
}

func (s *Server) servePlugins(w http.ResponseWriter, r *http.Request) {
	data := s.prepareBasePageData("plugins")
	s.renderAndWritePage(w, "plugins", data)
}

func (s *Server) serveSkills(w http.ResponseWriter, r *http.Request) {
	data := s.prepareBasePageData("skills")
	data.Title = "Skills - Ori Agent"
	data.BrandText = "Ori Agent"
	data.ShowSidebarToggle = true
	s.renderAndWritePage(w, "skills", data)
}

func (s *Server) serveTemplates(w http.ResponseWriter, r *http.Request) {
	data := s.prepareBasePageData("templates")
	data.Title = "Templates - Ori Agent"
	data.BrandText = "Ori Agent"
	data.ShowSidebarToggle = true
	s.renderAndWritePage(w, "templates", data)
}

func (s *Server) serveModels(w http.ResponseWriter, r *http.Request) {
	data := s.prepareBasePageData("models")
	data.ShowSidebarToggle = true
	s.renderAndWritePage(w, "models", data)
}

func (s *Server) serveWorkflows(w http.ResponseWriter, r *http.Request) {
	data := s.prepareBasePageData("workflows")
	data.Title = "Orchestration Skills - Ori Agent"
	data.BrandText = "Ori Agent"
	s.renderAndWritePage(w, "workflows", data)
}

func (s *Server) serveWorkspaces(w http.ResponseWriter, r *http.Request) {
	data := s.prepareBasePageData("workspaces")
	data.Title = "Workspaces - Ori Agent"
	data.BrandText = "Ori Agent"
	s.renderAndWritePage(w, "workspaces", data)
}

// handleWorkspacesRoutes handles all /workspaces/* routes
func (s *Server) handleWorkspacesRoutes(w http.ResponseWriter, r *http.Request) {
	// Extract path after /workspaces/
	path := strings.TrimPrefix(r.URL.Path, "/workspaces/")
	if path == "" || path == r.URL.Path {
		// No ID provided, redirect to workspaces page
		http.Redirect(w, r, "/workspaces", http.StatusSeeOther)
		return
	}

	// Split path into segments
	parts := strings.Split(path, "/")
	workspaceID := parts[0]

	// Check if this is a canvas route: /workspaces/{id}/canvas
	if len(parts) == 2 && parts[1] == "canvas" {
		s.serveWorkspaceCanvas(w, workspaceID)
		return
	}

	// Check if this is a diagnostics route: /workspaces/{id}/diagnostics
	if len(parts) == 2 && parts[1] == "diagnostics" {
		s.serveWorkspaceDiagnostics(w, workspaceID)
		return
	}

	// Check if this is a task route: /workspaces/{id}/task/{taskId}
	if len(parts) == 3 && parts[1] == "task" && strings.TrimSpace(parts[2]) != "" {
		s.serveWorkspaceTask(w, workspaceID, parts[2])
		return
	}

	// Check if this is a workspace run route: /workspaces/{id}/runs/{runId}
	if len(parts) == 3 && parts[1] == "runs" && strings.TrimSpace(parts[2]) != "" {
		s.serveWorkspaceRun(w, workspaceID, parts[2])
		return
	}

	// Workspace-scoped agent detail: /workspaces/{id}/agents/{name}. Workspace-
	// local agents live only in the workspace config.json (not the global agent
	// store), so this is their detail home rather than /agents/<name>.
	if len(parts) == 3 && parts[1] == "agents" && strings.TrimSpace(parts[2]) != "" {
		agentName, err := url.PathUnescape(parts[2])
		if err != nil {
			agentName = parts[2]
		}
		s.serveWorkspaceAgentDetail(w, workspaceID, agentName)
		return
	}

	// Workspace notes app: /workspaces/{id}/notes[/noteId].
	if len(parts) >= 2 && parts[1] == "notes" {
		if len(parts) == 2 {
			s.serveWorkspaceNotesPage(w, workspaceID, "")
			return
		}
		if len(parts) == 3 && strings.TrimSpace(parts[2]) != "" {
			s.serveWorkspaceNotesPage(w, workspaceID, parts[2])
			return
		}
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	// If just /workspaces/{id}, serve the workspace detail page
	if len(parts) == 1 {
		s.serveWorkspaceDetail(w, workspaceID)
		return
	}

	// Otherwise, redirect to home page
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) serveWorkspaceNotesPage(w http.ResponseWriter, workspaceID, noteID string) {
	data := s.prepareBasePageData("workspaces")
	data.Title = "Workspace Notes - Ori Agent"
	data.BrandText = "Ori Agent"
	data.ShowSidebarToggle = false
	data.Extra["WorkspaceID"] = workspaceID
	data.Extra["NoteID"] = noteID
	data.Extra["NotePageMode"] = "workspace"
	s.renderAndWritePage(w, "note-page", data)
}

// handleNotesPageRoute serves the focused `/notes/<id>` page. API endpoints
// under /api/notes/* are routed separately in routes.go.
func (s *Server) handleNotesPageRoute(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/notes/")
	if path == "" || path == r.URL.Path {
		http.Redirect(w, r, "/workspaces", http.StatusSeeOther)
		return
	}
	parts := strings.Split(path, "/")
	noteID := strings.TrimSpace(parts[0])
	if noteID == "" {
		http.Redirect(w, r, "/workspaces", http.StatusSeeOther)
		return
	}
	s.serveFocusedNotePage(w, noteID)
}

func (s *Server) serveFocusedNotePage(w http.ResponseWriter, noteID string) {
	data := s.prepareBasePageData("workspaces")
	data.Title = "Note - Ori Agent"
	data.BrandText = "Ori Agent"
	data.ShowSidebarToggle = false
	data.Extra["WorkspaceID"] = ""
	data.Extra["NoteID"] = noteID
	data.Extra["NotePageMode"] = "focused"
	s.renderAndWritePage(w, "note-page", data)
}

// serveWorkspaceDetail renders the workspace detail page for every workspace
// kind. Groups render the same page as concrete workspaces; the page itself
// shows group-specific UI (header badge, Members panel) based on the loaded
// workspace's kind.
func (s *Server) serveWorkspaceDetail(w http.ResponseWriter, workspaceID string) {
	data := s.prepareBasePageData("workspaces")
	data.Title = "Workspace - Ori Agent"
	data.BrandText = "Ori Agent"
	data.ShowSidebarToggle = true
	data.Extra["WorkspaceID"] = workspaceID
	s.renderAndWritePage(w, "workspace-detail", data)
}

// serveWorkspaceAgentDetail renders the detail page for a workspace-scoped
// agent (an entry/manager agent defined in the workspace's config.json). These
// agents are not registered in the global agent store, so they have no
// /agents/<name> page; this workspace-scoped route is their home.
func (s *Server) serveWorkspaceAgentDetail(w http.ResponseWriter, workspaceID, agentName string) {
	data := s.prepareBasePageData("workspaces")
	data.Title = "Workspace Agent - Ori Agent"
	data.BrandText = "Ori Agent"
	data.ShowSidebarToggle = true
	data.Extra["WorkspaceID"] = workspaceID
	data.Extra["AgentName"] = agentName
	s.renderAndWritePage(w, "workspace-agent-detail", data)
}

func (s *Server) serveWorkspaceDiagnostics(w http.ResponseWriter, workspaceID string) {
	data := s.prepareBasePageData("workspaces")
	data.Title = "Workspace Diagnostics - Ori Agent"
	data.BrandText = "Ori Agent"
	data.ShowSidebarToggle = true
	data.Extra["WorkspaceID"] = workspaceID
	s.renderAndWritePage(w, "workspace-diagnostics", data)
}

func (s *Server) serveWorkspaceCanvas(w http.ResponseWriter, workspaceID string) {
	data := s.prepareBasePageData("workspaces")
	data.Title = "Workspace Canvas - Ori Agent"
	data.BrandText = "Ori Agent"
	data.ShowSidebarToggle = true
	data.Extra["WorkspaceID"] = workspaceID
	s.renderAndWritePage(w, "workspace-canvas", data)
}

func (s *Server) serveWorkspaceTask(w http.ResponseWriter, workspaceID, taskID string) {
	data := s.prepareBasePageData("workspaces")
	data.Title = "Task - Ori Agent"
	data.BrandText = "Ori Agent"
	data.ShowSidebarToggle = true
	data.Extra["WorkspaceID"] = workspaceID
	data.Extra["TaskID"] = taskID
	s.renderAndWritePage(w, "workspace-task", data)
}

func (s *Server) serveWorkspaceRun(w http.ResponseWriter, workspaceID, runID string) {
	data := s.prepareBasePageData("workspaces")
	data.Title = "Workspace Run - Ori Agent"
	data.BrandText = "Ori Agent"
	data.ShowSidebarToggle = true
	data.Extra["WorkspaceID"] = workspaceID
	data.Extra["RunID"] = runID
	s.renderAndWritePage(w, "workspace-run", data)
}

func (s *Server) servePersonalize(w http.ResponseWriter, r *http.Request) {
	data := s.prepareBasePageData("personalize")
	data.Title = "Personalize - Ori Agent"
	data.BrandText = "Ori Agent"
	s.renderAndWritePage(w, "personalize", data)
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
	case strings.HasSuffix(path, ".woff2"):
		w.Header().Set("Content-Type", "font/woff2")
	default:
		w.Header().Set("Content-Type", "application/octet-stream")
	}

	// Embedded assets are immutable for the life of the process and only change
	// when the binary is rebuilt. Allow the browser to cache them but always
	// revalidate against a content-hash ETag, so we serve cheap 304s instead of
	// re-downloading (and re-parsing) megabytes of JS/CSS on every page load,
	// while never risking a stale asset after an update.
	etag := staticETag(path, content)
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "public, max-age=0, must-revalidate")

	if match := r.Header.Get("If-None-Match"); match != "" {
		if match == "*" || strings.Contains(match, etag) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}

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
		// This is a request for /agents/{agent-name} - serve the agent detail page.
		// The Claude Code CLI agent gets its own dedicated read-only page.
		agentName, err := url.PathUnescape(pathAfterAgents)
		if err != nil {
			agentName = pathAfterAgents
		}
		if s.isClaudeCodeAgentName(agentName) {
			s.serveClaudeAgentDetail(w, r)
			return
		}
		if s.isCodexAgentName(agentName) {
			s.serveCodexAgentDetail(w, r)
			return
		}
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
