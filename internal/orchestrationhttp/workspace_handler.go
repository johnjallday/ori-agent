package orchestrationhttp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/johnjallday/ori-agent/internal/filewatcher"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacesettings"
)

// WorkspaceHandler manages workspace-related operations
type WorkspaceHandler struct {
	agentStore     store.Store
	workspaceStore workspace.Store
	eventBus       *workspace.EventBus
	sessionStore   SessionStore
	fileWatcher    *filewatcher.Watcher // optional, for workspace directory watching
	directorySync  *workspace.DirectorySyncManager
	// folderStore is the canonical folder-based workspace.json store. Production
	// wraps workspaceStore with a SQLite-primary SyncStore, so workspaceStore.Get
	// never carries fields with no SQLite column (e.g. Designation) — only the
	// disk record does. Optional; wired via SetFolderStore once the folder store
	// exists (mirrors workspace.HTTPHandler.SetFolderStore).
	folderStore *workspace.FileStore
}

// NewWorkspaceHandler creates a new workspace handler
func NewWorkspaceHandler(agentStore store.Store, workspaceStore workspace.Store, eventBus *workspace.EventBus, sessionStore SessionStore) *WorkspaceHandler {
	return &WorkspaceHandler{
		agentStore:     agentStore,
		workspaceStore: workspaceStore,
		eventBus:       eventBus,
		sessionStore:   sessionStore,
	}
}

// SetFileWatcher sets the optional file watcher for workspace directory watching.
func (wh *WorkspaceHandler) SetFileWatcher(fw *filewatcher.Watcher) {
	wh.fileWatcher = fw
}

func (wh *WorkspaceHandler) SetDirectorySync(sync *workspace.DirectorySyncManager) {
	wh.directorySync = sync
}

// SetFolderStore wires the canonical folder-based workspace.json store, used
// to hydrate fields that have no SQLite column (currently: Designation).
// Production wraps workspaceStore in decorators (SyncStore, AgentSnapshotStore)
// that do not promote the folder store's extra read methods, so it must be
// injected explicitly rather than type-asserted off workspaceStore (mirrors
// workspace.HTTPHandler.SetFolderStore).
func (wh *WorkspaceHandler) SetFolderStore(store *workspace.FileStore) {
	if wh == nil {
		return
	}
	wh.folderStore = store
}

// hydrateDesignation overlays the workspace-side Designation field from the
// canonical folder-store record. workspaceStore.Get (SQLite-primary) never
// carries it: there is no SQLite column (FR3), so a plain Get always returns
// the zero value. No-op when the folder store isn't wired.
func (wh *WorkspaceHandler) hydrateDesignation(ws *workspace.Workspace) {
	if wh == nil || wh.folderStore == nil || ws == nil {
		return
	}
	diskWorkspace, err := wh.folderStore.Get(ws.ID)
	if err != nil || diskWorkspace == nil {
		return
	}
	ws.Designation = diskWorkspace.Designation
}

// addWorkspaceMapFields augments a workspace list summary with the fields the
// Workspace Map view needs but GetSummary() omits (entry agent, group kind /
// parent, tool/skill counts, ops mode, open-task count, and a working flag).
// All keys are additive; Cards/Tree simply ignore them.
func addWorkspaceMapFields(ws *workspace.Workspace, summary map[string]any) {
	if ws == nil || summary == nil {
		return
	}
	fields := workspace.ComputeMapSummaryFields(ws)
	summary["entry_agent_name"] = fields.EntryAgentName
	summary["kind"] = ws.Kind
	summary["designation"] = ws.Designation
	summary["parent_id"] = ws.ParentID
	summary["mcp_count"] = fields.MCPCount
	summary["skill_count"] = fields.SkillCount
	summary["ops_mode"] = fields.OpsMode
	summary["open_task_count"] = fields.OpenTaskCount
	// ticket_counts is the one canonical count by ticket state (PRD 760 FR-6).
	// Browser surfaces read this, not open_task_count, which stays for older
	// clients.
	summary["ticket_counts"] = fields.TicketCounts
	// backlog_count is the workspace's own Backlog item count, shown by the
	// global Workspace Map as a local count/link only — never aggregated or
	// editable from that surface (PRD workspace-backlog FR58-59).
	summary["backlog_count"] = fields.BacklogCount
	summary["task_summary_available"] = fields.TaskSummaryAvailable
	summary["unknown_task_count"] = fields.UnknownTaskCount
	summary["needs_attention_count"] = fields.NeedsAttentionCount
	summary["active"] = fields.Active
}

// WorkspaceHandler handles workspace CRUD operations
// GET: List all workspaces or get workspace by ID
// POST: Create new workspace
// PUT: Update workspace metadata
// DELETE: Delete workspace
func (wh *WorkspaceHandler) WorkspaceHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	switch r.Method {
	case http.MethodGet:
		wh.handleGetWorkspace(w, r)
	case http.MethodPost:
		wh.handleCreateWorkspace(w, r)
	case http.MethodPut:
		wh.handleUpdateWorkspace(w, r)
	case http.MethodDelete:
		wh.handleDeleteWorkspace(w, r)
	default:
		orihttp.MethodNotAllowed(w)
	}
}

func (wh *WorkspaceHandler) handleGetWorkspace(w http.ResponseWriter, r *http.Request) {
	// Check if a specific workspace ID is requested
	wsID := r.URL.Query().Get("id")
	activeOnly := r.URL.Query().Get("active") == "true"

	if wsID != "" {
		// Get specific workspace
		ws, err := wh.workspaceStore.Get(wsID)
		if err != nil {
			logger.Error("Error getting workspace", logger.Fields{"workspace_id": wsID, "error": err})
			orihttp.NotFound(w, err.Error())
			return
		}
		wh.hydrateDesignation(ws)

		settings := workspacesettings.Extract(ws.SharedData)
		// Build response with workspace data
		response := map[string]any{
			"id":                                    ws.ID,
			"name":                                  ws.Name,
			"kind":                                  ws.Kind,
			"designation":                           ws.Designation,
			"description":                           ws.Description,
			"tags":                                  append([]string(nil), ws.Tags...),
			"entry_agent_name":                      ws.EntryAgentName(),
			"agents":                                ws.AgentNames(),
			"agent_instances":                       ws.AgentInstances,
			"shared_data":                           ws.SharedData,
			"messages":                              ws.Messages,
			"tasks":                                 ws.Tasks,
			"ticket_counts":                         workspace.ComputeMapSummaryFields(ws).TicketCounts,
			"attachments":                           ws.Attachments,
			"scheduled_tasks":                       ws.ScheduledTasks,
			"directory_references":                  ws.DirectoryReferences,
			"mcp_bindings":                          ws.MCPBindings,
			"agent_mcp_access":                      ws.AgentMCPAccess,
			"skill_bindings":                        ws.SkillBindings,
			"agent_skill_access":                    ws.AgentSkillAccess,
			"workflows":                             ws.Workflows,
			"layout":                                ws.Layout,
			"status":                                ws.Status,
			"created_at":                            ws.CreatedAt,
			"updated_at":                            ws.UpdatedAt,
			"workspace_settings":                    settings,
			"workspace_settings_effective_behavior": workspacesettings.BuildEffectiveBehavior(settings),
			"task_markdown_status": workspace.TaskMarkdownStatusForSettings(
				wh.workspaceStore,
				ws.ID,
				settings.TaskMarkdown,
			),
		}
		groupContractWorkspace := ws
		if wh.folderStore != nil {
			if canonical, canonicalErr := wh.folderStore.Get(ws.ID); canonicalErr == nil && canonical != nil {
				groupContractWorkspace = canonical
			}
		}
		if status := workspace.EvaluateGroupRequirementLifecycle(groupContractWorkspace, wh.workspaceStore.Get); status != nil {
			response["group_requirement_status"] = status
		}
		// "How this was set up", for a workspace built with the assistant.
		// Provenance is portable workspace.json state, read from the canonical
		// folder copy.
		if provenance := groupContractWorkspace.GetTemplateProvenance(); provenance != nil && provenance.BuildSummary != nil {
			response["build_summary"] = provenance.BuildSummary
		}

		// Add sessions if session store is available
		if wh.sessionStore != nil {
			ctx := context.Background()
			sessions, err := wh.sessionStore.ListSessionsByWorkspace(ctx, wsID)
			if err != nil {
				logger.Debug("Failed to load sessions for workspace", logger.Fields{"workspace_id": wsID, "error": err})
			} else {
				response["sessions"] = sessions
			}

			// Add workspace notes
			notes, err := wh.sessionStore.ListNotesByWorkspace(ctx, wsID)
			if err != nil {
				logger.Debug("Failed to load notes for workspace", logger.Fields{"workspace_id": wsID, "error": err})
			} else {
				response["notes"] = notes
			}
		}

		orihttp.WriteJSON(w, response)
		return
	}

	// List workspaces with optional filters
	if activeOnly {
		// Get only active workspaces
		workspaces, err := wh.workspaceStore.ListActive()
		if err != nil {
			logger.Error("Error listing active workspaces", logger.Fields{"error": err})
			orihttp.InternalError(w, err.Error())
			return
		}

		orihttp.WriteJSON(w, map[string]any{
			"workspaces": workspaces,
			"count":      len(workspaces),
		})
		return
	}

	// List all workspaces
	ids, err := wh.workspaceStore.List()
	if err != nil {
		logger.Error("Error listing workspaces", logger.Fields{"error": err})
		orihttp.InternalError(w, err.Error())
		return
	}

	// Load summaries for all workspaces
	ctx := context.Background()
	summaries := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		ws, err := wh.workspaceStore.Get(id)
		if err != nil {
			continue // Skip workspaces that fail to load
		}
		wh.hydrateDesignation(ws)
		summary := ws.GetSummary()
		addWorkspaceMapFields(ws, summary)

		// Add session and note counts if session store is available
		if wh.sessionStore != nil {
			sessions, err := wh.sessionStore.ListSessionsByWorkspace(ctx, id)
			if err == nil {
				summary["session_count"] = len(sessions)
			} else {
				summary["session_count"] = 0
			}

			notes, err := wh.sessionStore.ListNotesByWorkspace(ctx, id)
			if err == nil {
				summary["note_count"] = len(notes)
			} else {
				summary["note_count"] = 0
			}
		} else {
			summary["session_count"] = 0
			summary["note_count"] = 0
		}

		summaries = append(summaries, summary)
	}

	orihttp.WriteJSON(w, map[string]any{
		"workspaces": summaries,
		"count":      len(summaries),
	})
}

// handleCreateWorkspace creates a new workspace
func (wh *WorkspaceHandler) handleCreateWorkspace(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		Agents      []string       `json:"participating_agents"`
		InitialData map[string]any `json:"initial_context"`
	}

	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	// Validate required fields

	if req.Name == "" {
		orihttp.BadRequest(w, "name is required")
		return
	}

	// Verify all participating agents exist
	for _, agentName := range req.Agents {
		_, ok := wh.agentStore.GetAgent(agentName)
		if !ok {
			orihttp.NotFound(w, "agent not found: "+agentName)
			return
		}
	}

	// Create workspace

	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{
		Name:        req.Name,
		Description: req.Description,
		Agents:      req.Agents,
		InitialData: req.InitialData,
	})

	// Only explicit creation crosses native admission; ordinary updates do not.
	if err := workspace.CreateNativeWorkspace(r.Context(), wh.workspaceStore, ws); err != nil {
		logger.Error("Error saving workspace", logger.Fields{"error": err})
		orihttp.InternalError(w, "Failed to save workspace: "+err.Error())
		return
	}

	logger.Info("Created workspace", logger.Fields{"name": req.Name, "workspace_id": ws.ID})

	// Publish workspace created event
	if wh.eventBus != nil {
		event := workspace.NewWorkspaceEvent(
			workspace.EventWorkspaceCreated,
			ws.ID,
			"api",
			map[string]any{
				"name":        req.Name,
				"description": req.Description,
				"agents":      req.Agents,
			},
		)
		wh.eventBus.Publish(event)
	}

	w.WriteHeader(http.StatusOK)
	orihttp.WriteJSON(w, map[string]any{
		"workspace_id":   ws.ID,
		"workspace_slug": ws.FolderSlug,
		"status":         ws.Status,
		"created_at":     ws.CreatedAt,
	})
}

// handleUpdateWorkspace updates workspace metadata (name, description)
func (wh *WorkspaceHandler) handleUpdateWorkspace(w http.ResponseWriter, r *http.Request) {
	wsID := r.URL.Query().Get("id")
	if wsID == "" {
		orihttp.BadRequest(w, "id is required")
		return
	}

	var req struct {
		Name        *string `json:"name,omitempty"`
		Description *string `json:"description,omitempty"`
	}

	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	// Get existing workspace
	ws, err := wh.workspaceStore.Get(wsID)
	if err != nil {
		logger.Error("Error getting workspace for update", logger.Fields{"workspace_id": wsID, "error": err})
		orihttp.NotFound(w, err.Error())
		return
	}

	// Update fields if provided
	if req.Name != nil {
		ws.Name = *req.Name
	}
	if req.Description != nil {
		ws.Description = *req.Description
	}

	// Update timestamp
	ws.UpdatedAt = time.Now()

	// Save workspace
	if err := wh.workspaceStore.Save(ws); err != nil {
		logger.Error("Error saving workspace update", logger.Fields{"workspace_id": wsID, "error": err})
		orihttp.InternalError(w, "Failed to save workspace: "+err.Error())
		return
	}

	logger.Info("Updated workspace", logger.Fields{"workspace_id": wsID})

	// Publish workspace updated event
	if wh.eventBus != nil {
		event := workspace.NewWorkspaceEvent(
			workspace.EventWorkspaceUpdated,
			ws.ID,
			"api",
			map[string]any{
				"name":        ws.Name,
				"description": ws.Description,
			},
		)
		wh.eventBus.Publish(event)
	}

	orihttp.WriteJSON(w, map[string]any{
		"id":          ws.ID,
		"name":        ws.Name,
		"description": ws.Description,
		"updated_at":  ws.UpdatedAt,
	})
}

// handleDeleteWorkspace deletes a workspace
func (wh *WorkspaceHandler) handleDeleteWorkspace(w http.ResponseWriter, r *http.Request) {
	wsID := r.URL.Query().Get("id")
	if wsID == "" {
		orihttp.BadRequest(w, "id parameter required")
		return
	}

	if err := wh.workspaceStore.Delete(wsID); err != nil {
		logger.Error("Error deleting workspace", logger.Fields{"workspace_id": wsID, "error": err})
		orihttp.NotFound(w, err.Error())
		return
	}

	logger.Info("Deleted workspace", logger.Fields{"workspace_id": wsID})
	w.WriteHeader(http.StatusOK)
	orihttp.WriteJSON(w, map[string]any{
		"success": true,
		"message": "Workspace deleted successfully",
	})
}

// WorkspaceAgentsHandler handles adding/removing agents from workspace
// POST: Add agent to workspace
// DELETE: Remove agent from workspace
func (wh *WorkspaceHandler) WorkspaceAgentsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	switch r.Method {
	case http.MethodPost:
		wh.handleAddAgentToWorkspace(w, r)
	case http.MethodDelete:
		wh.handleRemoveAgentFromWorkspace(w, r)
	default:
		orihttp.MethodNotAllowed(w)
	}
}

func (wh *WorkspaceHandler) handleAddAgentToWorkspace(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WorkspaceID string `json:"workspace_id"`
		AgentName   string `json:"agent_name"`
	}

	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	if req.WorkspaceID == "" {
		orihttp.BadRequest(w, "workspace_id is required")
		return
	}
	if req.AgentName == "" {
		orihttp.BadRequest(w, "agent_name is required")
		return
	}

	// Verify agent exists
	_, ok := wh.agentStore.GetAgent(req.AgentName)
	if !ok {
		orihttp.NotFound(w, "agent not found: "+req.AgentName)
		return
	}

	// Get workspace
	ws, err := wh.workspaceStore.Get(req.WorkspaceID)
	if err != nil {
		logger.Error("Error getting workspace", logger.Fields{"workspace_id": req.WorkspaceID, "error": err})
		orihttp.NotFound(w, "Workspace not found: "+err.Error())
		return
	}

	// Add agent
	if err := ws.AddAgent(req.AgentName); err != nil {
		logger.Error("Error adding agent to workspace", logger.Fields{"error": err})
		orihttp.BadRequest(w, err.Error())
		return
	}

	// Save workspace
	if err := wh.workspaceStore.Save(ws); err != nil {
		logger.Error("Error saving workspace", logger.Fields{"error": err})
		orihttp.InternalError(w, "Failed to save workspace: "+err.Error())
		return
	}

	logger.Info("Added agent to workspace", logger.Fields{"agent": req.AgentName, "workspace_id": req.WorkspaceID})

	// Publish event
	if wh.eventBus != nil {
		event := workspace.NewWorkspaceEvent(
			workspace.EventWorkspaceUpdated,
			req.WorkspaceID,
			"api",
			map[string]any{
				"action": "agent_added",
				"agent":  req.AgentName,
			},
		)
		wh.eventBus.Publish(event)
	}

	w.WriteHeader(http.StatusOK)
	orihttp.WriteJSON(w, map[string]any{
		"success": true,
		"message": "Agent added successfully",
		"agent":   req.AgentName,
		"agents":  ws.AgentNames(),
	})
}

// handleRemoveAgentFromWorkspace removes an agent from a workspace
func (wh *WorkspaceHandler) handleRemoveAgentFromWorkspace(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.URL.Query().Get("workspace_id")
	agentName := r.URL.Query().Get("agent_name")

	if workspaceID == "" {
		orihttp.BadRequest(w, "workspace_id parameter required")
		return
	}
	if agentName == "" {
		orihttp.BadRequest(w, "agent_name parameter required")
		return
	}

	// Get workspace
	ws, err := wh.workspaceStore.Get(workspaceID)
	if err != nil {
		logger.Error("Error getting workspace", logger.Fields{"workspace_id": workspaceID, "error": err})
		orihttp.NotFound(w, "Workspace not found: "+err.Error())
		return
	}

	// Remove agent
	if err := ws.RemoveAgent(agentName); err != nil {
		logger.Error("Error removing agent from workspace", logger.Fields{"error": err})
		orihttp.NotFound(w, err.Error())
		return
	}

	// Save workspace
	if err := wh.workspaceStore.Save(ws); err != nil {
		logger.Error("Error saving workspace", logger.Fields{"error": err})
		orihttp.InternalError(w, "Failed to save workspace: "+err.Error())
		return
	}

	logger.Info("Removed agent from workspace", logger.Fields{"agent": agentName, "workspace_id": workspaceID})

	// Publish event
	if wh.eventBus != nil {
		event := workspace.NewWorkspaceEvent(
			workspace.EventWorkspaceUpdated,
			workspaceID,
			"api",
			map[string]any{
				"action": "agent_removed",
				"agent":  agentName,
			},
		)
		wh.eventBus.Publish(event)
	}

	w.WriteHeader(http.StatusOK)
	orihttp.WriteJSON(w, map[string]any{
		"success": true,
		"message": "Agent removed successfully",
		"agent":   agentName,
		"agents":  ws.AgentNames(),
	})
}

// SaveStationLayoutHandler saves command-map station positions for any
// workspace (the HQ's stations, capability stations such as File Janitor, and
// every workspace's Memory station), scoped to ONLY Layout.StationPositions.
// Layout is mutated in place, so whatever else a stored layout still carries
// (the retired workspace canvas's task, agent, attachment and store positions,
// connections and zoom) is left exactly as it was. The canvas and its layout
// save endpoint are gone; this is now the only writer of a workspace's Layout.
// PUT: Save HQ station positions (fractional [0,1] coordinates, keyed by
// station registry key).
func (wh *WorkspaceHandler) SaveStationLayoutHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		orihttp.MethodNotAllowed(w)
		return
	}

	var req struct {
		WorkspaceID      string                        `json:"workspace_id"`
		StationPositions map[string]workspace.Position `json:"station_positions"`
	}

	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	if req.WorkspaceID == "" {
		orihttp.BadRequest(w, "workspace_id is required")
		return
	}

	ws, err := wh.workspaceStore.Get(req.WorkspaceID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Failed to get workspace: %v", err))
		return
	}

	// Re-hydrate the folder-store-only Designation before Save so persisting a
	// station drag never un-designates the Personal HQ: workspaceStore.Get is
	// SQLite-primary and never carries Designation (no SQLite column), so saving
	// the workspace back would write an empty one. This matters most here:
	// stations only render on the designated HQ, so a clobber would make them
	// vanish on the next reload — exactly the surface this handler exists to
	// persist.
	wh.hydrateDesignation(ws)

	if ws.Layout == nil {
		ws.Layout = &workspace.CanvasLayout{}
	}
	ws.Layout.StationPositions = req.StationPositions

	if err := wh.workspaceStore.Save(ws); err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to save workspace: %v", err))
		return
	}

	wh.eventBus.Publish(workspace.Event{
		WorkspaceID: req.WorkspaceID,
		Type:        workspace.EventWorkspaceUpdated,
		Timestamp:   time.Now(),
	})

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]any{
		"success": true,
		"message": "Station layout saved successfully",
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// ActivateHandler handles POST /api/orchestration/workspace/activate?id={id}
// It starts watching all directory references in the workspace so that
// file change events are available while the user is on the workspace page.
func (wh *WorkspaceHandler) ActivateHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}

	wsID := r.URL.Query().Get("id")
	if wsID == "" {
		orihttp.BadRequest(w, "workspace id is required")
		return
	}

	ws, err := wh.workspaceStore.Get(wsID)
	if err != nil || ws == nil {
		orihttp.NotFound(w, "workspace not found")
		return
	}

	result := workspace.DirectorySyncWatchResult{WorkspaceID: wsID, TotalDirectories: len(ws.DirectoryReferences)}
	if wh.directorySync != nil {
		var err error
		result, err = wh.directorySync.WatchWorkspace(wsID)
		if err != nil {
			logger.Warn("Failed to watch activated workspace directories", logger.Fields{
				"workspace_id": wsID,
				"error":        err,
			})
		}
	} else if wh.fileWatcher != nil && len(ws.DirectoryReferences) > 0 {
		for _, dir := range ws.DirectoryReferences {
			if dir.Path == "" {
				continue
			}
			watchKey := fmt.Sprintf("workspace:%s:dir:%s", wsID, dir.ID)
			if wh.fileWatcher.IsWatching(watchKey) {
				result.AlreadyWatching++
				result.Watched++
				continue
			}
			if err := wh.fileWatcher.Watch(watchKey, dir.Path); err != nil {
				logger.Warn("Failed to watch workspace directory", logger.Fields{
					"workspace_id": wsID,
					"directory":    dir.Name,
					"path":         dir.Path,
					"error":        err,
				})
			} else {
				result.Watched++
			}
		}
	}

	logger.Info("Workspace activated", logger.Fields{
		"workspace_id":        wsID,
		"directories_watched": result.Watched,
		"already_watching":    result.AlreadyWatching,
		"skipped_invalid":     result.SkippedInvalid,
		"skipped_over_limit":  result.SkippedOverLimit,
		"total_directories":   result.TotalDirectories,
	})

	orihttp.WriteJSON(w, map[string]any{
		"success":             true,
		"workspace_id":        wsID,
		"directories_watched": result.Watched,
		"already_watching":    result.AlreadyWatching,
		"skipped_invalid":     result.SkippedInvalid,
		"skipped_over_limit":  result.SkippedOverLimit,
		"total_directories":   result.TotalDirectories,
	})
}
