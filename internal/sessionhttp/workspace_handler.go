package sessionhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/session"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacesettings"
)

var (
	errParentWorkspaceMustBeGroup  = errors.New("parent workspace must be a group")
	errWorkspaceDisallowsDirectUse = errors.New("groups cannot hold sessions, notes, or direct work")
)

const workspaceSharedDataPrimaryDirectoryIDKey = "primary_directory_id"

// HandleWorkspaces routes requests to /api/workspaces (also supports legacy /api/folders).
func (h *Handler) HandleWorkspaces(w http.ResponseWriter, r *http.Request) {
	// Normalize path for both /api/folders and /api/workspaces
	path := r.URL.Path
	path = strings.TrimPrefix(path, "/api/folders")
	path = strings.TrimPrefix(path, "/api/workspaces")
	path = strings.TrimPrefix(path, "/")

	// Import routes must be handled before generic workspace-id routing.
	switch path {
	case "import":
		h.handleWorkspaceImport(w, r)
		return
	case "import/check":
		h.handleWorkspaceImportCheck(w, r)
		return
	case "import/duplicate-action":
		h.handleWorkspaceImportDuplicateAction(w, r)
		return
	case "sync-status":
		h.handleWorkspaceSyncStatus(w, r)
		return
	case "sync":
		h.handleWorkspaceSync(w, r)
		return
	}

	// Handle sub-paths like {id}/agents, {id}/layout
	if path != "" && strings.Contains(path, "/") {
		parts := strings.SplitN(path, "/", 3)
		id := parts[0]
		subPath := parts[1]

		switch subPath {
		case "settings":
			h.handleWorkspaceSettings(w, r, id)
			return
		case "agents":
			h.handleWorkspaceAgents(w, r, id, parts)
			return
		case "layout":
			h.handleWorkspaceLayout(w, r, id)
			return
		case "board":
			h.handleWorkspaceBoard(w, r, id)
			return
		case "rename":
			h.handleWorkspaceRename(w, r, id)
			return
		}
	}

	if path != "" && !strings.Contains(path, "/") {
		// This is a request for a specific workspace
		h.handleWorkspace(w, r, path)
		return
	}

	// Handle collection-level requests
	switch r.Method {
	case http.MethodGet:
		h.listWorkspaces(w, r)
	case http.MethodPost:
		h.createWorkspace(w, r)
	default:
		_ = orihttp.RespondMethodNotAllowed(w)
	}
}

// handleWorkspace handles requests for a specific workspace.
func (h *Handler) handleWorkspace(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		h.getWorkspace(w, r, id)
	case http.MethodPut:
		h.updateWorkspace(w, r, id)
	case http.MethodPatch:
		h.updateWorkspace(w, r, id)
	case http.MethodDelete:
		h.deleteWorkspace(w, r, id)
	default:
		_ = orihttp.RespondMethodNotAllowed(w)
	}
}

type workspaceBootstrapRequest struct {
	Goal         string `json:"goal,omitempty"`
	Systems      string `json:"systems,omitempty"`
	Capabilities string `json:"capabilities,omitempty"`
	Context      string `json:"context,omitempty"`
}

func normalizeWorkspaceBootstrap(input *workspaceBootstrapRequest) map[string]interface{} {
	if input == nil {
		return nil
	}

	goal := strings.TrimSpace(input.Goal)
	systems := strings.TrimSpace(input.Systems)
	capabilities := strings.TrimSpace(input.Capabilities)
	contextValue := strings.TrimSpace(input.Context)
	if goal == "" && systems == "" && capabilities == "" && contextValue == "" {
		return nil
	}

	systemsList := splitWorkspaceBootstrapValues(systems)
	if systemsList == nil {
		systemsList = []string{}
	}

	return map[string]interface{}{
		"version":      1,
		"goal":         goal,
		"systems":      systems,
		"capabilities": capabilities,
		"systems_list": systemsList,
		"context":      contextValue,
		"captured_at":  time.Now().UTC().Format(time.RFC3339),
	}
}

func splitWorkspaceBootstrapValues(raw string) []string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}

	parts := strings.FieldsFunc(trimmed, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r'
	})

	values := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		value := strings.TrimSpace(part)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		values = append(values, value)
	}
	return values
}

// createWorkspace handles POST /api/workspaces.
func (h *Handler) createWorkspace(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name               string                     `json:"name"`
		Kind               string                     `json:"kind,omitempty"`
		WorkspacePreset    string                     `json:"workspace_preset,omitempty"`
		Description        string                     `json:"description,omitempty"`
		ParentID           string                     `json:"parent_id,omitempty"`
		OrderIndex         *int                       `json:"order_index,omitempty"`
		Color              string                     `json:"color,omitempty"`
		ProjectPath        string                     `json:"project_path,omitempty"`
		FolderSlug         string                     `json:"folder_slug,omitempty"`
		Location           string                     `json:"location,omitempty"`         // Optional custom directory for workspace folder (overrides default root)
		EntryAgentName     string                     `json:"entry_agent_name,omitempty"` // Optional existing agent name; otherwise a workspace manager is created automatically
		WorkspaceBootstrap *workspaceBootstrapRequest `json:"workspace_bootstrap,omitempty"`
	}

	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	if req.Name == "" {
		_ = orihttp.RespondBadRequest(w, "name is required")
		return
	}

	kind, err := parseWorkspaceKind(req.Kind)
	if err != nil {
		_ = orihttp.RespondBadRequest(w, err.Error())
		return
	}
	if kind == session.WorkspaceKindGroup {
		switch {
		case strings.TrimSpace(req.ProjectPath) != "":
			_ = orihttp.RespondBadRequest(w, "groups cannot have a project path")
			return
		case strings.TrimSpace(req.Location) != "":
			_ = orihttp.RespondBadRequest(w, "groups cannot have a folder location")
			return
		case strings.TrimSpace(req.EntryAgentName) != "":
			_ = orihttp.RespondBadRequest(w, "groups cannot have an entry agent")
			return
		case req.WorkspaceBootstrap != nil:
			_ = orihttp.RespondBadRequest(w, "groups cannot have workspace bootstrap settings")
			return
		}
	}
	if _, err := h.requireGroupParent(r.Context(), req.ParentID); err != nil {
		handleWorkspaceParentError(w, err)
		return
	}

	ws := &session.Workspace{
		Name:        req.Name,
		Kind:        kind,
		Description: req.Description,
		ParentID:    req.ParentID,
		Color:       req.Color,
		FolderSlug:  agentworkspace.Slugify(req.Name),
		ProjectPath: req.ProjectPath,
	}
	if requestedSlug := strings.TrimSpace(req.FolderSlug); requestedSlug != "" {
		ws.FolderSlug = agentworkspace.Slugify(requestedSlug)
	}
	if req.OrderIndex != nil {
		ws.OrderIndex = *req.OrderIndex
	}
	if kind != session.WorkspaceKindGroup {
		ws.SharedData = workspacesettings.Store(ws.SharedData, workspacesettings.ProfileDefaults(req.WorkspacePreset))
	}
	if bootstrapData := normalizeWorkspaceBootstrap(req.WorkspaceBootstrap); bootstrapData != nil {
		if ws.SharedData == nil {
			ws.SharedData = make(map[string]interface{})
		}
		ws.SharedData["workspace_bootstrap"] = bootstrapData
	}

	// If an existing entry agent was specified, validate and set it.
	// Otherwise the workspace is created without an entry agent;
	// the UI will prompt the user to create one with their choice of model/provider.
	if req.EntryAgentName != "" {
		entryAgentName, err := h.validateWorkspaceEntryAgent(req.EntryAgentName)
		if err != nil {
			logger.Error("Failed to validate workspace entry agent", logger.Fields{"name": req.Name, "error": err})
			_ = orihttp.RespondBadRequest(w, err.Error())
			return
		}
		if entryAgentName != "" {
			setWorkspaceEntryAgent(ws, entryAgentName)
		}
	}

	if err := h.store.CreateWorkspace(r.Context(), ws); err != nil {
		logger.Error("Failed to create workspace", logger.Fields{"error": err})
		_ = orihttp.RespondInternalError(w, "Failed to create workspace")
		return
	}

	// Create workspace folder on disk if folder-based store is available
	if h.workspaceStore != nil && ws.Kind != session.WorkspaceKindGroup {
		folderWS := &agentworkspace.Workspace{
			ID:             ws.ID,
			Name:           ws.Name,
			Kind:           string(ws.Kind),
			Description:    ws.Description,
			FolderSlug:     ws.FolderSlug,
			ProjectPath:    ws.ProjectPath,
			ParentID:       ws.ParentID,
			Agents:         append([]string{}, ws.Agents...),
			AgentInstances: toWorkspaceAgentInstances(ws.AgentInstances),
			SharedData:     ws.SharedData,
			Status:         agentworkspace.StatusActive,
			CreatedAt:      ws.CreatedAt,
			UpdatedAt:      ws.UpdatedAt,
		}

		targetLocation := strings.TrimSpace(req.Location)
		if targetLocation == "" && h.workspaceRootResolver != nil {
			targetLocation = strings.TrimSpace(h.workspaceRootResolver())
		}

		var folderErr error
		switch {
		case targetLocation != "" && !workspacePathsEqual(targetLocation, h.workspaceStore.BasePath()):
			// Custom location or updated default root outside the original file store base.
			folderErr = h.workspaceStore.SaveAt(folderWS, targetLocation)
		default:
			// Default location inside the file store base path.
			folderErr = h.workspaceStore.Save(folderWS)
		}

		if folderErr != nil {
			var slugConflict *agentworkspace.FolderSlugConflictError
			if errors.As(folderErr, &slugConflict) {
				if delErr := h.store.DeleteWorkspace(r.Context(), ws.ID); delErr != nil {
					logger.Error("Failed to rollback workspace after slug conflict", logger.Fields{"id": ws.ID, "error": delErr})
					_ = orihttp.RespondInternalError(w, "Failed to rollback workspace after folder conflict")
					return
				}
				writeWorkspaceCreateSlugConflict(w, req.Name, slugConflict)
				return
			}
			logger.Warn("Failed to create workspace folder on disk", logger.Fields{"id": ws.ID, "error": folderErr})
			// Non-fatal: SQLite creation succeeded, folder is supplementary
		} else if folderPath, err := h.workspaceStore.GetFolderPath(ws.ID); err == nil {
			now := time.Now()

			// Add the workspace folder as the initial directory reference
			dirRef := workspaceDirectoryReference{
				ID:          uuid.New().String(),
				WorkspaceID: ws.ID,
				Name:        ws.FolderSlug,
				Path:        folderPath,
				X:           400,
				Y:           300,
				CreatedAt:   now,
				UpdatedAt:   now,
			}
			if data, err := json.Marshal([]workspaceDirectoryReference{dirRef}); err == nil {
				ws.DirectoryReferencesJSON = data
			}
			setWorkspacePrimaryDirectoryID(ws, dirRef.ID)

			// Auto-provision a filesystem MCP binding scoped to the workspace folder
			mcpBinding := agentworkspace.WorkspaceMCPBinding{
				ID:         uuid.New().String(),
				ServerName: "filesystem",
				Alias:      "workspace-files",
				Enabled:    true,
				Config: map[string]interface{}{
					"roots": []string{folderPath},
				},
				CreatedAt: now,
				UpdatedAt: now,
			}
			if data, err := json.Marshal([]agentworkspace.WorkspaceMCPBinding{mcpBinding}); err == nil {
				ws.MCPBindingsJSON = data
			}

			ws.UpdatedAt = now
			if err := h.store.UpdateWorkspace(r.Context(), ws); err != nil {
				logger.Warn("Failed to set initial workspace config", logger.Fields{"id": ws.ID, "error": err})
			}

			// Resync workspace.json to include directory reference and MCP binding
			folderWS.SharedData = ws.SharedData
			folderWS.Agents = append([]string{}, ws.Agents...)
			folderWS.AgentInstances = toWorkspaceAgentInstances(ws.AgentInstances)
			folderWS.DirectoryReferences = []agentworkspace.DirectoryReference{
				{
					ID:          dirRef.ID,
					WorkspaceID: dirRef.WorkspaceID,
					Name:        dirRef.Name,
					Path:        dirRef.Path,
					X:           dirRef.X,
					Y:           dirRef.Y,
					CreatedAt:   dirRef.CreatedAt,
					UpdatedAt:   dirRef.UpdatedAt,
				},
			}
			folderWS.MCPBindings = []agentworkspace.WorkspaceMCPBinding{mcpBinding}
			folderWS.UpdatedAt = now
			if err := h.workspaceStore.Save(folderWS); err != nil {
				logger.Warn("Failed to resync workspace.json after creation", logger.Fields{"id": ws.ID, "error": err})
			}

			logger.Info("Workspace folder created on disk", logger.Fields{"id": ws.ID, "path": folderPath})
		}
	}

	logger.Info("Workspace created", logger.Fields{"id": ws.ID, "name": req.Name, "folder_slug": ws.FolderSlug, "kind": ws.Kind})

	_ = orihttp.RespondCreated(w, map[string]interface{}{
		"success": true,
		"folder":  ws,
	})
}

func workspacePathsEqual(a, b string) bool {
	if strings.TrimSpace(a) == "" || strings.TrimSpace(b) == "" {
		return false
	}

	absA, err := filepath.Abs(strings.TrimSpace(a))
	if err != nil {
		absA = strings.TrimSpace(a)
	}
	absB, err := filepath.Abs(strings.TrimSpace(b))
	if err != nil {
		absB = strings.TrimSpace(b)
	}

	if runtime.GOOS == "windows" {
		return strings.EqualFold(filepath.Clean(absA), filepath.Clean(absB))
	}

	return filepath.Clean(absA) == filepath.Clean(absB)
}

func parseWorkspaceKind(value string) (session.WorkspaceKind, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return session.WorkspaceKindWorkspace, nil
	}

	switch session.WorkspaceKind(trimmed) {
	case session.WorkspaceKindWorkspace:
		return session.WorkspaceKindWorkspace, nil
	case session.WorkspaceKindGroup:
		return session.WorkspaceKindGroup, nil
	default:
		return "", fmt.Errorf("invalid workspace kind %q", trimmed)
	}
}

func (h *Handler) requireGroupParent(ctx context.Context, parentID string) (*session.Workspace, error) {
	parentID = strings.TrimSpace(parentID)
	if parentID == "" {
		return nil, nil
	}

	parent, err := h.store.GetWorkspace(ctx, parentID)
	if err != nil {
		return nil, err
	}
	if !parent.IsGroup() {
		return nil, errParentWorkspaceMustBeGroup
	}
	return parent, nil
}

func handleWorkspaceParentError(w http.ResponseWriter, err error) {
	switch {
	case err == nil:
		return
	case errors.Is(err, session.ErrWorkspaceNotFound):
		_ = orihttp.RespondBadRequest(w, "Parent group not found")
	case errors.Is(err, errParentWorkspaceMustBeGroup):
		_ = orihttp.RespondBadRequest(w, err.Error())
	default:
		_ = orihttp.RespondInternalError(w, "Failed to validate parent group")
	}
}

func filterConcreteWorkspaces(workspaces []session.Workspace) []session.Workspace {
	if len(workspaces) == 0 {
		return workspaces
	}

	filtered := make([]session.Workspace, 0, len(workspaces))
	for _, ws := range workspaces {
		if ws.IsGroup() {
			continue
		}
		filtered = append(filtered, ws)
	}
	return filtered
}

func (h *Handler) requireConcreteWorkspace(ctx context.Context, workspaceID string) (*session.Workspace, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return nil, nil
	}

	ws, err := h.store.GetWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	if ws.IsGroup() {
		return nil, errWorkspaceDisallowsDirectUse
	}
	return ws, nil
}

// getWorkspace handles GET /api/workspaces/{id}.
func (h *Handler) getWorkspace(w http.ResponseWriter, r *http.Request, id string) {
	workspace, err := h.store.GetWorkspace(r.Context(), id)
	if err == session.ErrWorkspaceNotFound {
		_ = orihttp.RespondNotFound(w, "Workspace not found")
		return
	}
	if err != nil {
		logger.Error("Failed to get workspace", logger.Fields{"id": id, "error": err})
		_ = orihttp.RespondInternalError(w, "Failed to get workspace")
		return
	}
	workspace = h.hydrateWorkspaceMetadataFromFileStore(workspace)

	orihttp.WriteJSON(w, h.buildWorkspaceDetailResponse(workspace))
}

// updateWorkspace handles PUT/PATCH /api/workspaces/{id}.
func (h *Handler) updateWorkspace(w http.ResponseWriter, r *http.Request, id string) {
	workspace, err := h.store.GetWorkspace(r.Context(), id)
	if err == session.ErrWorkspaceNotFound {
		_ = orihttp.RespondNotFound(w, "Workspace not found")
		return
	}
	if err != nil {
		_ = orihttp.RespondInternalError(w, "Failed to get workspace")
		return
	}

	var req struct {
		Name               *string                    `json:"name,omitempty"`
		Description        *string                    `json:"description,omitempty"`
		ParentID           *string                    `json:"parent_id,omitempty"`
		OrderIndex         *int                       `json:"order_index,omitempty"`
		Color              *string                    `json:"color,omitempty"`
		ProjectPath        *string                    `json:"project_path,omitempty"`
		PrimaryDirectoryID *string                    `json:"primary_directory_id,omitempty"`
		WorkspaceBootstrap *workspaceBootstrapRequest `json:"workspace_bootstrap,omitempty"`
	}

	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	// Apply partial updates
	if req.Name != nil {
		workspace.Name = *req.Name
		workspace.FolderSlug = agentworkspace.Slugify(*req.Name)
	}
	if req.Description != nil {
		workspace.Description = *req.Description
	}
	if req.Description != nil || req.WorkspaceBootstrap != nil {
		bootstrapData := mergeWorkspaceBootstrapForUpdate(
			workspace.SharedData,
			workspace.Description,
			req.Description != nil,
			req.WorkspaceBootstrap,
		)
		if bootstrapData != nil {
			if workspace.SharedData == nil {
				workspace.SharedData = make(map[string]interface{})
			}
			workspace.SharedData["workspace_bootstrap"] = bootstrapData
		} else if workspace.SharedData != nil {
			delete(workspace.SharedData, "workspace_bootstrap")
		}
	}
	if req.ProjectPath != nil {
		if workspace.IsGroup() && strings.TrimSpace(*req.ProjectPath) != "" {
			_ = orihttp.RespondBadRequest(w, "groups cannot have a project path")
			return
		}
		workspace.ProjectPath = *req.ProjectPath
	}
	if req.PrimaryDirectoryID != nil {
		setWorkspacePrimaryDirectoryID(workspace, *req.PrimaryDirectoryID)
	}
	if req.ParentID != nil {
		// Check for circular reference
		if *req.ParentID == workspace.ID {
			_ = orihttp.RespondBadRequest(w, "Workspace cannot be its own parent")
			return
		}
		if *req.ParentID != "" {
			descendants, err := h.store.GetSubworkspaceIDs(r.Context(), workspace.ID)
			if err != nil {
				logger.Error("Failed to load workspace descendants", logger.Fields{"id": id, "error": err})
				_ = orihttp.RespondInternalError(w, "Failed to update workspace")
				return
			}
			for _, descendantID := range descendants {
				if descendantID == *req.ParentID {
					_ = orihttp.RespondBadRequest(w, "Workspace cannot be moved under its descendant")
					return
				}
			}
		}
		if _, err := h.requireGroupParent(r.Context(), *req.ParentID); err != nil {
			handleWorkspaceParentError(w, err)
			return
		}
		workspace.ParentID = *req.ParentID
	}
	if req.OrderIndex != nil {
		workspace.OrderIndex = *req.OrderIndex
	}
	if req.Color != nil {
		workspace.Color = *req.Color
	}

	if err := h.store.UpdateWorkspace(r.Context(), workspace); err != nil {
		logger.Error("Failed to update workspace", logger.Fields{"id": id, "error": err})
		_ = orihttp.RespondInternalError(w, "Failed to update workspace")
		return
	}
	if req.Name == nil && req.ParentID == nil {
		if err := h.syncWorkspacePortableStateToFileStore(workspace); err != nil {
			logger.Warn("Failed to sync workspace.json after workspace update", logger.Fields{"id": id, "error": err})
		}
	}
	if req.Description != nil || req.WorkspaceBootstrap != nil {
		if err := h.syncWorkspaceDescriptionNote(r.Context(), workspace); err != nil {
			logger.Warn("Failed to sync canonical workspace description note", logger.Fields{"id": id, "error": err})
		}
	}

	logger.Info("Workspace updated", logger.Fields{"id": id})

	orihttp.WriteJSON(w, map[string]interface{}{
		"success": true,
		"folder":  workspace,
	})
}

// deleteWorkspace handles DELETE /api/workspaces/{id}.
// Query params:
//   - delete_sessions=true: also delete all sessions belonging to this workspace.
//     If false or absent, sessions are unlinked (workspace_id set to NULL).
//   - confirm=true: required to proceed with deletion (if absent, returns session count for confirmation).
func (h *Handler) deleteWorkspace(w http.ResponseWriter, r *http.Request, id string) {
	ctx := r.Context()

	// Check workspace exists
	ws, err := h.store.GetWorkspace(ctx, id)
	if err == session.ErrWorkspaceNotFound {
		_ = orihttp.RespondNotFound(w, "Workspace not found")
		return
	}
	if err != nil {
		logger.Error("Failed to get workspace", logger.Fields{"id": id, "error": err})
		_ = orihttp.RespondInternalError(w, "Failed to delete workspace")
		return
	}

	// If confirm is not set, return session count for UI confirmation prompt
	if r.URL.Query().Get("confirm") != "true" {
		sessionCount := ws.SessionCount
		orihttp.WriteJSON(w, map[string]interface{}{
			"workspace_id":     id,
			"name":             ws.Name,
			"session_count":    sessionCount,
			"confirm_required": true,
			"message":          fmt.Sprintf("Workspace %q has %d sessions. Delete the workspace?", ws.Name, sessionCount),
		})
		return
	}

	deleteSessions := r.URL.Query().Get("delete_sessions") == "true"

	// Capture the entry agent name before deletion so it can be cleaned up.
	entryAgentName := ""
	if h.workspaceStore != nil && ws.Kind != session.WorkspaceKindGroup {
		if folderWS, ferr := h.workspaceStore.Get(id); ferr == nil && folderWS != nil {
			entryAgentName = strings.TrimSpace(folderWS.EntryAgentName())
		}
	}

	// Handle session cleanup
	if deleteSessions {
		if err := h.store.DeleteSessionsByWorkspace(ctx, id); err != nil {
			logger.Error("Failed to delete sessions for workspace", logger.Fields{"id": id, "error": err})
			_ = orihttp.RespondInternalError(w, "Failed to delete workspace sessions")
			return
		}
	} else {
		if err := h.store.UnlinkSessionsFromWorkspace(ctx, id); err != nil {
			logger.Error("Failed to unlink sessions from workspace", logger.Fields{"id": id, "error": err})
			_ = orihttp.RespondInternalError(w, "Failed to unlink workspace sessions")
			return
		}
	}

	// Delete the workspace
	if err := h.store.DeleteWorkspace(ctx, id); err != nil {
		logger.Error("Failed to delete workspace", logger.Fields{"id": id, "error": err})
		_ = orihttp.RespondInternalError(w, "Failed to delete workspace")
		return
	}

	// Also delete from folder-based store if available
	if h.workspaceStore != nil && ws.Kind != session.WorkspaceKindGroup {
		if err := h.workspaceStore.Delete(id); err != nil {
			logger.Warn("Failed to delete workspace folder", logger.Fields{"id": id, "error": err})
			// Non-fatal: SQLite deletion succeeded
		}
	}

	// Delete the workspace's entry agent so it no longer lingers in the agent
	// store after its parent workspace is gone. Non-fatal on failure.
	if entryAgentName != "" && h.agentStore != nil {
		if _, exists := h.agentStore.GetAgent(entryAgentName); exists {
			if err := h.agentStore.DeleteAgent(entryAgentName); err != nil {
				logger.Warn("Failed to delete workspace entry agent", logger.Fields{
					"workspace_id": id,
					"agent":        entryAgentName,
					"error":        err,
				})
			} else {
				logger.Info("Deleted workspace entry agent", logger.Fields{
					"workspace_id": id,
					"agent":        entryAgentName,
				})
			}
		}
	}

	logger.Info("Workspace deleted", logger.Fields{"id": id, "delete_sessions": deleteSessions})

	orihttp.RespondNoContent(w)
}

// listWorkspaces handles GET /api/workspaces.
func (h *Handler) listWorkspaces(w http.ResponseWriter, r *http.Request) {
	tree := r.URL.Query().Get("tree") == "true"

	if tree {
		workspaces, err := h.store.GetWorkspaceTree(r.Context())
		if err != nil {
			// Don't log context canceled - it's normal when client disconnects
			if errors.Is(err, context.Canceled) {
				return
			}
			logger.Error("Failed to get workspace tree", logger.Fields{"error": err})
			_ = orihttp.RespondInternalError(w, "Failed to get workspaces")
			return
		}
		workspaces = h.hydrateWorkspaceListFromFileStore(workspaces)

		orihttp.WriteJSON(w, map[string]interface{}{
			"folders":    workspaces,
			"workspaces": workspaces,
		})
		return
	}

	workspaces, err := h.store.ListWorkspaces(r.Context())
	if err != nil {
		// Don't log context canceled - it's normal when client disconnects
		if errors.Is(err, context.Canceled) {
			return
		}
		logger.Error("Failed to list workspaces", logger.Fields{"error": err})
		_ = orihttp.RespondInternalError(w, "Failed to list workspaces")
		return
	}

	workspaces = filterConcreteWorkspaces(workspaces)
	workspaces = h.hydrateWorkspaceListFromFileStore(workspaces)

	orihttp.WriteJSON(w, map[string]interface{}{
		"folders":    workspaces,
		"workspaces": workspaces,
	})
}

func (h *Handler) hydrateWorkspaceListFromFileStore(workspaces []session.Workspace) []session.Workspace {
	if len(workspaces) == 0 {
		return workspaces
	}

	hydrated := make([]session.Workspace, len(workspaces))
	for i := range workspaces {
		hydrated[i] = workspaces[i]
		hydrated[i].Children = h.hydrateWorkspaceListFromFileStore(workspaces[i].Children)
		h.hydrateWorkspaceMetadataInto(&hydrated[i])
	}

	return hydrated
}

func (h *Handler) hydrateWorkspaceMetadataFromFileStore(workspace *session.Workspace) *session.Workspace {
	if workspace == nil {
		return nil
	}

	copy := *workspace
	h.hydrateWorkspaceMetadataInto(&copy)
	return &copy
}

func (h *Handler) hydrateWorkspaceMetadataInto(workspace *session.Workspace) {
	if h == nil || h.workspaceStore == nil || workspace == nil || workspace.IsGroup() {
		return
	}

	diskWorkspace, err := h.workspaceStore.Get(workspace.ID)
	if err != nil || diskWorkspace == nil {
		return
	}

	fallback := session.ConvertAgentWorkspace(diskWorkspace)
	if fallback == nil {
		return
	}

	if strings.TrimSpace(workspace.FolderSlug) == "" {
		workspace.FolderSlug = fallback.FolderSlug
	}
	if workspace.SharedData == nil && fallback.SharedData != nil {
		workspace.SharedData = fallback.SharedData
	}
	mergeWorkspaceJSONField(&workspace.DirectoryReferencesJSON, fallback.DirectoryReferencesJSON)
	mergeWorkspaceJSONField(&workspace.MCPBindingsJSON, fallback.MCPBindingsJSON)
	mergeWorkspaceJSONField(&workspace.AgentMCPAccessJSON, fallback.AgentMCPAccessJSON)
	mergeWorkspaceJSONField(&workspace.SkillBindingsJSON, fallback.SkillBindingsJSON)
	mergeWorkspaceJSONField(&workspace.AgentSkillAccessJSON, fallback.AgentSkillAccessJSON)
}

func mergeWorkspaceJSONField(target *json.RawMessage, fallback json.RawMessage) {
	if target == nil || len(*target) > 0 || len(fallback) == 0 {
		return
	}
	*target = append(json.RawMessage(nil), fallback...)
}

func workspacePrimaryDirectoryID(workspace *session.Workspace) string {
	if workspace == nil || workspace.SharedData == nil {
		return ""
	}

	raw, ok := workspace.SharedData[workspaceSharedDataPrimaryDirectoryIDKey]
	if !ok {
		return ""
	}

	value, _ := raw.(string)
	return strings.TrimSpace(value)
}

func setWorkspacePrimaryDirectoryID(workspace *session.Workspace, directoryID string) {
	if workspace == nil {
		return
	}

	trimmed := strings.TrimSpace(directoryID)
	if workspace.SharedData == nil {
		workspace.SharedData = make(map[string]interface{})
	}

	if trimmed == "" {
		delete(workspace.SharedData, workspaceSharedDataPrimaryDirectoryIDKey)
		return
	}

	workspace.SharedData[workspaceSharedDataPrimaryDirectoryIDKey] = trimmed
}

// handleWorkspaceRename handles POST /api/workspaces/{id}/rename.
func (h *Handler) handleWorkspaceRename(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		_ = orihttp.RespondMethodNotAllowed(w)
		return
	}

	var req struct {
		Name       string `json:"name"`
		FolderSlug string `json:"folder_slug,omitempty"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	if req.Name == "" {
		_ = orihttp.RespondBadRequest(w, "name is required")
		return
	}

	ctx := r.Context()

	// Update in session store
	ws, err := h.store.GetWorkspace(ctx, id)
	if err == session.ErrWorkspaceNotFound {
		_ = orihttp.RespondNotFound(w, "Workspace not found")
		return
	}
	if err != nil {
		_ = orihttp.RespondInternalError(w, "Failed to get workspace")
		return
	}

	oldName := ws.Name
	oldFolderSlug := ws.FolderSlug
	targetSlug := ""
	if requestedSlug := strings.TrimSpace(req.FolderSlug); requestedSlug != "" {
		targetSlug = agentworkspace.Slugify(requestedSlug)
	}
	if targetSlug == "" {
		targetSlug = agentworkspace.Slugify(req.Name)
	}

	ws.Name = req.Name
	ws.FolderSlug = targetSlug
	ws.UpdatedAt = time.Now()

	if err := h.store.UpdateWorkspace(ctx, ws); err != nil {
		_ = orihttp.RespondInternalError(w, "Failed to rename workspace")
		return
	}

	// Rename the folder if folder-based store is available
	if h.workspaceStore != nil && ws.Kind != session.WorkspaceKindGroup {
		if err := h.workspaceStore.RenameWithSlug(id, req.Name, targetSlug); err != nil {
			ws.Name = oldName
			ws.FolderSlug = oldFolderSlug
			ws.UpdatedAt = time.Now()
			if rollbackErr := h.store.UpdateWorkspace(ctx, ws); rollbackErr != nil {
				logger.Error("Failed to rollback workspace rename after folder rename error", logger.Fields{"id": id, "error": rollbackErr})
				_ = orihttp.RespondInternalError(w, "Failed to rollback workspace rename")
				return
			}

			var slugConflict *agentworkspace.FolderSlugConflictError
			if errors.As(err, &slugConflict) {
				writeWorkspaceCreateSlugConflict(w, req.Name, slugConflict)
				return
			}

			logger.Error("Failed to rename workspace folder", logger.Fields{"id": id, "error": err})
			_ = orihttp.RespondInternalError(w, "Failed to rename workspace folder")
			return
		}
	}

	logger.Info("Workspace renamed", logger.Fields{"id": id, "new_name": req.Name})

	orihttp.WriteJSON(w, map[string]interface{}{
		"success": true,
		"folder":  ws,
	})
}

// =============================================================================
// Workspace Folder Import
// =============================================================================

type createWorkspaceImportRequest struct {
	Name               string                     `json:"name,omitempty"`
	WorkspacePreset    string                     `json:"workspace_preset,omitempty"`
	Description        string                     `json:"description,omitempty"`
	ParentID           string                     `json:"parent_id,omitempty"`
	OrderIndex         *int                       `json:"order_index,omitempty"`
	Color              string                     `json:"color,omitempty"`
	Path               string                     `json:"path"`
	AllowDuplicate     bool                       `json:"allow_duplicate,omitempty"`
	EntryPoint         string                     `json:"entry_point,omitempty"`
	EntryAgentName     string                     `json:"entry_agent_name,omitempty"`
	WorkspaceBootstrap *workspaceBootstrapRequest `json:"workspace_bootstrap,omitempty"`
}

type workspaceImportDuplicate struct {
	Found         bool   `json:"found"`
	WorkspaceID   string `json:"workspace_id,omitempty"`
	WorkspaceName string `json:"workspace_name,omitempty"`
	DirectoryID   string `json:"directory_id,omitempty"`
	Path          string `json:"path,omitempty"`
}

type workspaceCreateConflict struct {
	Type          string `json:"type"`
	RequestedSlug string `json:"requested_slug,omitempty"`
	SuggestedSlug string `json:"suggested_slug,omitempty"`
	Location      string `json:"location,omitempty"`
}

type workspaceSyncLocateRequest struct {
	ID   string `json:"id"`
	Path string `json:"path"`
}

type workspaceDirectoryReference struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	Name        string    `json:"name"`
	Path        string    `json:"path"`
	X           float64   `json:"x"`
	Y           float64   `json:"y"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (h *Handler) handleWorkspaceImportCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		_ = orihttp.RespondMethodNotAllowed(w)
		return
	}

	pathValue := strings.TrimSpace(r.URL.Query().Get("path"))
	if r.Method == http.MethodPost {
		var req struct {
			Path string `json:"path"`
		}
		if !orihttp.ParseJSONBody(w, r, &req) {
			return
		}
		pathValue = strings.TrimSpace(req.Path)
	}

	if pathValue == "" {
		_ = orihttp.RespondBadRequest(w, "path is required")
		return
	}

	normalizedPath, err := normalizeImportPath(pathValue)
	if err != nil {
		_ = orihttp.RespondBadRequest(w, fmt.Sprintf("invalid path: %v", err))
		return
	}

	duplicate, err := h.findDuplicateImportedWorkspace(r.Context(), normalizedPath)
	if err != nil {
		logger.Error("Failed duplicate check for workspace import", logger.Fields{"error": err})
		_ = orihttp.RespondInternalError(w, "Failed to check folder import status")
		return
	}

	orihttp.WriteJSON(w, map[string]interface{}{
		"success":         true,
		"normalized_path": normalizedPath,
		"duplicate":       duplicate,
	})
}

func (h *Handler) handleWorkspaceImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		_ = orihttp.RespondMethodNotAllowed(w)
		return
	}

	var req createWorkspaceImportRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	if strings.TrimSpace(req.Path) == "" {
		_ = orihttp.RespondBadRequest(w, "path is required")
		return
	}

	normalizedPath, err := normalizeImportPath(req.Path)
	if err != nil {
		_ = orihttp.RespondBadRequest(w, fmt.Sprintf("invalid path: %v", err))
		return
	}

	info, err := os.Stat(normalizedPath)
	if err != nil {
		_ = orihttp.RespondBadRequest(w, fmt.Sprintf("path is not accessible: %v", err))
		return
	}
	if !info.IsDir() {
		_ = orihttp.RespondBadRequest(w, "path must be a directory")
		return
	}

	duplicate, err := h.findDuplicateImportedWorkspace(r.Context(), normalizedPath)
	if err != nil {
		logger.Error("Failed duplicate check for workspace import", logger.Fields{"error": err})
		_ = orihttp.RespondInternalError(w, "Failed to check existing imported workspaces")
		return
	}

	if duplicate.Found && !req.AllowDuplicate {
		recordWorkspaceImportTelemetry("duplicate_detected", logger.Fields{
			"path_hash":      hashPathForTelemetry(normalizedPath),
			"entry_point":    req.EntryPoint,
			"workspace_id":   duplicate.WorkspaceID,
			"workspace_name": duplicate.WorkspaceName,
		})
		writeWorkspaceImportConflict(w, "Folder is already imported in another workspace", duplicate)
		return
	}

	workspaceName := strings.TrimSpace(req.Name)
	if workspaceName == "" {
		workspaceName = filepath.Base(normalizedPath)
	}
	if _, err := h.requireGroupParent(r.Context(), req.ParentID); err != nil {
		handleWorkspaceParentError(w, err)
		return
	}

	if h.workspaceStore != nil && workspaceImportHasConfig(normalizedPath) {
		workspace, warning, err := h.restoreImportedWorkspace(r.Context(), normalizedPath, req)
		if err != nil {
			logger.Error("Failed to restore exported workspace", logger.Fields{
				"path_hash": hashPathForTelemetry(normalizedPath),
				"error":     err,
			})
			recordWorkspaceImportTelemetry("import_failed", logger.Fields{
				"path_hash":   hashPathForTelemetry(normalizedPath),
				"entry_point": req.EntryPoint,
				"reason":      "workspace_restore_failed",
			})
			if strings.Contains(strings.ToLower(err.Error()), "already exists") {
				_ = orihttp.RespondConflict(w, err.Error())
				return
			}
			_ = orihttp.RespondInternalError(w, "Failed to restore exported workspace")
			return
		}

		logger.Info("Workspace restored from exported folder", logger.Fields{
			"workspace_id": workspace.ID,
			"name":         workspace.Name,
			"path_hash":    hashPathForTelemetry(normalizedPath),
			"entry_point":  req.EntryPoint,
		})
		recordWorkspaceImportTelemetry("import_success", logger.Fields{
			"workspace_id": workspace.ID,
			"path_hash":    hashPathForTelemetry(normalizedPath),
			"entry_point":  req.EntryPoint,
			"mode":         "workspace_restore",
		})

		response := map[string]interface{}{
			"success":              true,
			"folder":               workspace,
			"duplicate":            workspaceImportDuplicate{Found: false},
			"restored_from_config": true,
		}
		if strings.TrimSpace(warning) != "" {
			response["warning"] = warning
		}

		_ = orihttp.RespondCreated(w, response)
		return
	}

	workspace := &session.Workspace{
		Name:        workspaceName,
		Kind:        session.WorkspaceKindWorkspace,
		Description: req.Description,
		ParentID:    req.ParentID,
		Color:       req.Color,
	}
	if req.OrderIndex != nil {
		workspace.OrderIndex = *req.OrderIndex
	}
	workspace.SharedData = map[string]interface{}{
		"folder_import": map[string]interface{}{
			"enabled":         true,
			"path":            normalizedPath,
			"path_hash":       hashPathForTelemetry(normalizedPath),
			"entry_point":     req.EntryPoint,
			"allow_duplicate": req.AllowDuplicate,
			"imported_at":     time.Now().UTC().Format(time.RFC3339),
		},
	}
	workspace.SharedData = workspacesettings.Store(workspace.SharedData, workspacesettings.ProfileDefaults(req.WorkspacePreset))
	if bootstrapData := normalizeWorkspaceBootstrap(req.WorkspaceBootstrap); bootstrapData != nil {
		workspace.SharedData["workspace_bootstrap"] = bootstrapData
	}

	// If an existing entry agent was specified, validate and set it.
	if req.EntryAgentName != "" {
		entryAgentName, err := h.validateWorkspaceEntryAgent(req.EntryAgentName)
		if err != nil {
			logger.Error("Failed to validate imported workspace entry agent", logger.Fields{"name": workspaceName, "error": err})
			_ = orihttp.RespondBadRequest(w, err.Error())
			return
		}
		if entryAgentName != "" {
			setWorkspaceEntryAgent(workspace, entryAgentName)
		}
	}

	recordWorkspaceImportTelemetry("import_attempt", logger.Fields{
		"path_hash":       hashPathForTelemetry(normalizedPath),
		"entry_point":     req.EntryPoint,
		"allow_duplicate": req.AllowDuplicate,
	})

	if err := h.store.CreateWorkspace(r.Context(), workspace); err != nil {
		logger.Error("Failed to create workspace from folder import", logger.Fields{"error": err})
		recordWorkspaceImportTelemetry("import_failed", logger.Fields{
			"path_hash":   hashPathForTelemetry(normalizedPath),
			"entry_point": req.EntryPoint,
			"reason":      "workspace_create_failed",
		})
		_ = orihttp.RespondInternalError(w, "Failed to create workspace from folder")
		return
	}

	dirRef := workspaceDirectoryReference{
		ID:          uuid.New().String(),
		WorkspaceID: workspace.ID,
		Name:        filepath.Base(normalizedPath),
		Path:        normalizedPath,
		X:           400,
		Y:           300,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	var refs []workspaceDirectoryReference
	if len(workspace.DirectoryReferencesJSON) > 0 {
		if existingRefs, err := decodeDirectoryReferences(workspace.DirectoryReferencesJSON); err == nil {
			refs = existingRefs
		}
	}
	refs = append(refs, dirRef)

	data, err := json.Marshal(refs)
	if err != nil {
		logger.Error("Failed to marshal directory references for workspace import", logger.Fields{"workspace_id": workspace.ID, "error": err})
		_ = h.store.DeleteWorkspace(r.Context(), workspace.ID)
		recordWorkspaceImportTelemetry("import_failed", logger.Fields{
			"path_hash":    hashPathForTelemetry(normalizedPath),
			"workspace_id": workspace.ID,
			"entry_point":  req.EntryPoint,
			"reason":       "directory_reference_marshal_failed",
		})
		_ = orihttp.RespondInternalError(w, "Failed to attach imported folder")
		return
	}
	workspace.DirectoryReferencesJSON = data
	setWorkspacePrimaryDirectoryID(workspace, dirRef.ID)
	workspace.UpdatedAt = time.Now()

	if err := h.store.UpdateWorkspace(r.Context(), workspace); err != nil {
		logger.Error("Failed to attach imported folder reference to workspace", logger.Fields{"workspace_id": workspace.ID, "error": err})
		if delErr := h.store.DeleteWorkspace(r.Context(), workspace.ID); delErr != nil {
			logger.Warn("Failed to rollback workspace after import attach failure", logger.Fields{"workspace_id": workspace.ID, "error": delErr})
		}
		recordWorkspaceImportTelemetry("import_failed", logger.Fields{
			"path_hash":    hashPathForTelemetry(normalizedPath),
			"workspace_id": workspace.ID,
			"entry_point":  req.EntryPoint,
			"reason":       "directory_reference_save_failed",
		})
		_ = orihttp.RespondInternalError(w, "Failed to attach imported folder")
		return
	}

	logger.Info("Workspace imported from folder", logger.Fields{
		"workspace_id": workspace.ID,
		"name":         workspaceName,
		"path_hash":    hashPathForTelemetry(normalizedPath),
		"entry_point":  req.EntryPoint,
	})
	recordWorkspaceImportTelemetry("import_success", logger.Fields{
		"workspace_id": workspace.ID,
		"path_hash":    hashPathForTelemetry(normalizedPath),
		"entry_point":  req.EntryPoint,
	})

	_ = orihttp.RespondCreated(w, map[string]interface{}{
		"success": true,
		"folder":  workspace,
		"directory": map[string]interface{}{
			"id":           dirRef.ID,
			"workspace_id": dirRef.WorkspaceID,
			"name":         dirRef.Name,
			"path":         dirRef.Path,
		},
		"duplicate": workspaceImportDuplicate{Found: false},
	})
}

func workspaceImportHasConfig(folderPath string) bool {
	info, err := os.Stat(filepath.Join(folderPath, agentworkspace.WorkspaceConfigFile))
	return err == nil && !info.IsDir()
}

func (h *Handler) restoreImportedWorkspace(ctx context.Context, folderPath string, req createWorkspaceImportRequest) (*session.Workspace, string, error) {
	importTree, err := loadWorkspaceImportTree(folderPath, strings.TrimSpace(req.ParentID))
	if err != nil {
		return nil, "", err
	}
	if len(importTree) == 0 {
		return nil, "", fmt.Errorf("no workspace configuration found in %s", folderPath)
	}

	rootWorkspace := importTree[0]
	if trimmedName := strings.TrimSpace(req.Name); trimmedName != "" {
		rootWorkspace.Name = trimmedName
	}
	if trimmedDescription := strings.TrimSpace(req.Description); trimmedDescription != "" {
		rootWorkspace.Description = trimmedDescription
	}
	if _, ok := rootWorkspace.SharedData[workspacesettings.SharedDataKey]; !ok {
		rootWorkspace.SharedData = workspacesettings.Store(rootWorkspace.SharedData, workspacesettings.ProfileDefaults(req.WorkspacePreset))
	}
	if bootstrapData := normalizeWorkspaceBootstrap(req.WorkspaceBootstrap); bootstrapData != nil {
		if rootWorkspace.SharedData == nil {
			rootWorkspace.SharedData = make(map[string]interface{})
		}
		rootWorkspace.SharedData["workspace_bootstrap"] = bootstrapData
	}

	ensuredEntryAgentName := ""
	if strings.TrimSpace(req.EntryAgentName) != "" {
		agentName, err := h.validateWorkspaceEntryAgent(req.EntryAgentName)
		if err != nil {
			return nil, "", err
		}
		if agentName != "" {
			if err := ensureImportedWorkspaceEntryAgent(rootWorkspace, agentName); err != nil {
				return nil, "", err
			}
			ensuredEntryAgentName = agentName
		}
	}

	_, warning, err := h.workspaceStore.Import(folderPath)
	if err != nil {
		return nil, "", err
	}

	adapter := session.NewWorkspaceStoreAdapter(h.store)
	for _, item := range importTree {
		if item.Status == "" {
			item.Status = agentworkspace.StatusActive
		}

		if err := h.workspaceStore.Save(item); err != nil {
			return nil, warning, fmt.Errorf("persist imported workspace %s: %w", item.ID, err)
		}
		if err := adapter.Save(item); err != nil {
			return nil, warning, fmt.Errorf("sync imported workspace %s: %w", item.ID, err)
		}

		sessionWorkspace, err := h.store.GetWorkspace(ctx, item.ID)
		if err != nil {
			return nil, warning, fmt.Errorf("load imported workspace %s: %w", item.ID, err)
		}

		needsUpdate := false
		if sessionWorkspace.ParentID != item.ParentID {
			sessionWorkspace.ParentID = item.ParentID
			needsUpdate = true
		}

		if item.ID == rootWorkspace.ID {
			if req.OrderIndex != nil && sessionWorkspace.OrderIndex != *req.OrderIndex {
				sessionWorkspace.OrderIndex = *req.OrderIndex
				needsUpdate = true
			}
			if trimmedColor := strings.TrimSpace(req.Color); trimmedColor != "" && sessionWorkspace.Color != trimmedColor {
				sessionWorkspace.Color = trimmedColor
				needsUpdate = true
			}
			if ensuredEntryAgentName != "" {
				setWorkspaceEntryAgent(sessionWorkspace, ensuredEntryAgentName)
				needsUpdate = true
			}
		}

		if needsUpdate {
			sessionWorkspace.UpdatedAt = time.Now()
			if err := h.store.UpdateWorkspace(ctx, sessionWorkspace); err != nil {
				return nil, warning, fmt.Errorf("update imported workspace %s: %w", item.ID, err)
			}
		}
	}

	rootSessionWorkspace, err := h.store.GetWorkspace(ctx, rootWorkspace.ID)
	if err != nil {
		return nil, warning, fmt.Errorf("load restored root workspace %s: %w", rootWorkspace.ID, err)
	}
	return rootSessionWorkspace, warning, nil
}

func loadWorkspaceImportTree(folderPath string, parentID string) ([]*agentworkspace.Workspace, error) {
	result := make([]*agentworkspace.Workspace, 0, 1)
	if err := appendWorkspaceImportTree(&result, folderPath, parentID); err != nil {
		return nil, err
	}
	return result, nil
}

func appendWorkspaceImportTree(result *[]*agentworkspace.Workspace, folderPath string, parentID string) error {
	configPath := filepath.Join(folderPath, agentworkspace.WorkspaceConfigFile)
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", configPath, err)
	}

	ws, err := agentworkspace.FromJSON(data)
	if err != nil {
		return fmt.Errorf("decode %s: %w", configPath, err)
	}
	if strings.TrimSpace(ws.FolderSlug) == "" {
		ws.FolderSlug = filepath.Base(folderPath)
	}
	ws.ParentID = strings.TrimSpace(parentID)
	*result = append(*result, ws)

	subDir := filepath.Join(folderPath, agentworkspace.SubWorkspacesDir)
	entries, err := os.ReadDir(subDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read sub-workspaces for %s: %w", folderPath, err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if err := appendWorkspaceImportTree(result, filepath.Join(subDir, entry.Name()), ws.ID); err != nil {
			return err
		}
	}

	return nil
}

func ensureImportedWorkspaceEntryAgent(ws *agentworkspace.Workspace, agentName string) error {
	if ws == nil {
		return fmt.Errorf("workspace is required")
	}

	if err := ws.SetEntryAgentName(agentName); err == nil {
		return nil
	}

	if err := ws.AddAgent(agentName); err != nil && !errors.Is(err, agentworkspace.ErrAgentAlreadyInWorkspace) {
		return err
	}
	return ws.SetEntryAgentName(agentName)
}

func (h *Handler) handleWorkspaceImportDuplicateAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		_ = orihttp.RespondMethodNotAllowed(w)
		return
	}

	var req struct {
		Action      string `json:"action"`
		WorkspaceID string `json:"workspace_id,omitempty"`
		EntryPoint  string `json:"entry_point,omitempty"`
		Path        string `json:"path,omitempty"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	action := strings.TrimSpace(req.Action)
	switch action {
	case "suggestion_accepted", "override_confirmed":
	default:
		_ = orihttp.RespondBadRequest(w, "action must be one of: suggestion_accepted, override_confirmed")
		return
	}

	fields := logger.Fields{
		"entry_point": strings.TrimSpace(req.EntryPoint),
	}
	if strings.TrimSpace(req.WorkspaceID) != "" {
		fields["workspace_id"] = strings.TrimSpace(req.WorkspaceID)
	}

	if trimmedPath := strings.TrimSpace(req.Path); trimmedPath != "" {
		if normalizedPath, err := normalizeImportPath(trimmedPath); err == nil {
			fields["path_hash"] = hashPathForTelemetry(normalizedPath)
		} else {
			fields["path_hash"] = hashPathForTelemetry(filepath.Clean(trimmedPath))
			fields["path_normalization"] = "failed"
		}
	}

	recordWorkspaceImportTelemetry(action, fields)

	orihttp.WriteJSON(w, map[string]interface{}{
		"success": true,
	})
}

func (h *Handler) findDuplicateImportedWorkspace(ctx context.Context, normalizedPath string) (workspaceImportDuplicate, error) {
	workspaces, err := h.store.ListWorkspaces(ctx)
	if err != nil {
		return workspaceImportDuplicate{}, err
	}

	for _, wsSummary := range workspaces {
		ws, err := h.store.GetWorkspace(ctx, wsSummary.ID)
		if err != nil {
			continue
		}

		refs, err := decodeDirectoryReferences(ws.DirectoryReferencesJSON)
		if err != nil {
			logger.Warn("Failed to decode directory references while checking duplicates", logger.Fields{
				"workspace_id": ws.ID,
				"error":        err,
			})
			continue
		}

		for _, ref := range refs {
			refPath, err := normalizeImportPath(ref.Path)
			if err != nil {
				continue
			}
			if refPath == normalizedPath {
				return workspaceImportDuplicate{
					Found:         true,
					WorkspaceID:   ws.ID,
					WorkspaceName: ws.Name,
					DirectoryID:   ref.ID,
					Path:          ref.Path,
				}, nil
			}
		}
	}

	return workspaceImportDuplicate{Found: false}, nil
}

func decodeDirectoryReferences(raw json.RawMessage) ([]workspaceDirectoryReference, error) {
	if len(raw) == 0 {
		return []workspaceDirectoryReference{}, nil
	}
	var refs []workspaceDirectoryReference
	if err := json.Unmarshal(raw, &refs); err != nil {
		return nil, err
	}
	return refs, nil
}

func normalizeImportPath(input string) (string, error) {
	cleaned := strings.TrimSpace(input)
	if cleaned == "" {
		return "", fmt.Errorf("path is empty")
	}
	cleaned = filepath.Clean(cleaned)

	absPath, err := filepath.Abs(cleaned)
	if err != nil {
		return "", err
	}

	normalized := absPath
	if resolved, err := filepath.EvalSymlinks(absPath); err == nil {
		normalized = resolved
	}

	normalized = filepath.Clean(normalized)
	if runtime.GOOS == "windows" {
		normalized = strings.ToLower(normalized)
	}
	return normalized, nil
}

func writeWorkspaceImportConflict(w http.ResponseWriter, message string, duplicate workspaceImportDuplicate) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusConflict)
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"success":   false,
		"error":     message,
		"duplicate": duplicate,
	}); err != nil {
		logger.Error("Failed to encode workspace import conflict response", logger.Fields{"error": err})
	}
}

func writeWorkspaceCreateSlugConflict(w http.ResponseWriter, workspaceName string, conflict *agentworkspace.FolderSlugConflictError) {
	var message string
	if conflict != nil && conflict.SuggestedSlug != "" {
		message = fmt.Sprintf("A workspace folder named %q already exists. Create %q instead?", conflict.Slug, conflict.SuggestedSlug)
	} else if conflict != nil {
		message = fmt.Sprintf("A workspace folder named %q already exists.", conflict.Slug)
	} else {
		message = "A workspace folder with that name already exists."
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusConflict)
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"success": false,
		"error":   message,
		"conflict": workspaceCreateConflict{
			Type:          "folder_slug",
			RequestedSlug: conflict.Slug,
			SuggestedSlug: conflict.SuggestedSlug,
			Location:      conflict.ParentDir,
		},
		"workspace_name": workspaceName,
	}); err != nil {
		logger.Error("Failed to encode workspace create conflict response", logger.Fields{"error": err})
	}
}

func recordWorkspaceImportTelemetry(event string, fields logger.Fields) {
	if fields == nil {
		fields = logger.Fields{}
	}
	fields["event"] = event
	fields["scope"] = "workspace.folder_import"
	logger.Info("Workspace folder import telemetry", fields)
}

func hashPathForTelemetry(path string) string {
	if path == "" {
		return ""
	}
	// Keep telemetry path-safe while remaining deterministic across runs.
	parts := strings.Split(path, string(filepath.Separator))
	if len(parts) == 0 {
		return ""
	}
	tail := parts[len(parts)-1]
	return fmt.Sprintf("%x", uuid.NewSHA1(uuid.NameSpaceOID, []byte(path)))[:16] + ":" + tail
}

// =============================================================================
// Workspace Agent Management
// =============================================================================

// handleWorkspaceAgents handles requests to /api/workspaces/{id}/agents.
func (h *Handler) handleWorkspaceAgents(w http.ResponseWriter, r *http.Request, workspaceID string, parts []string) {
	switch r.Method {
	case http.MethodPost:
		h.addWorkspaceAgent(w, r, workspaceID)
	case http.MethodDelete:
		// Extract agent name or instance ID from path
		var agentIdentifier string
		if len(parts) > 2 {
			agentIdentifier = parts[2]
		}
		h.removeWorkspaceAgent(w, r, workspaceID, agentIdentifier)
	default:
		_ = orihttp.RespondMethodNotAllowed(w)
	}
}

// addWorkspaceAgent handles POST /api/workspaces/{id}/agents.
func (h *Handler) addWorkspaceAgent(w http.ResponseWriter, r *http.Request, workspaceID string) {
	var req struct {
		AgentName string `json:"agent_name"`
	}

	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	if req.AgentName == "" {
		_ = orihttp.RespondBadRequest(w, "agent_name is required")
		return
	}

	// Get the workspace
	workspace, err := h.store.GetWorkspace(r.Context(), workspaceID)
	if err == session.ErrWorkspaceNotFound {
		_ = orihttp.RespondNotFound(w, "Workspace not found")
		return
	}
	if err != nil {
		logger.Error("Failed to get workspace", logger.Fields{"id": workspaceID, "error": err})
		_ = orihttp.RespondInternalError(w, "Failed to get workspace")
		return
	}

	// Count existing instances of this agent type
	instanceCount := 0
	for _, inst := range workspace.AgentInstances {
		if inst.Name == req.AgentName {
			instanceCount++
		}
	}

	// Create new agent instance
	instanceNumber := instanceCount + 1
	nodeID := req.AgentName + "-node-" + uuid.New().String()[:8]
	if instanceNumber > 1 {
		nodeID = fmt.Sprintf("%s-%d-node-%s", req.AgentName, instanceNumber, uuid.New().String()[:8])
	}

	newInstance := session.AgentInstance{
		ID:             uuid.New().String(),
		Name:           req.AgentName,
		InstanceNumber: instanceNumber,
		NodeID:         nodeID,
		CreatedAt:      time.Now(),
	}

	// Add to workspace
	workspace.AgentInstances = append(workspace.AgentInstances, newInstance)

	// Also add to legacy agents array for backward compatibility
	found := false
	for _, a := range workspace.Agents {
		if a == req.AgentName {
			found = true
			break
		}
	}
	if !found {
		workspace.Agents = append(workspace.Agents, req.AgentName)
	}

	if strings.TrimSpace(currentWorkspaceEntryAgentName(workspace)) == "" {
		setWorkspaceEntryAgent(workspace, req.AgentName)
	}

	workspace.UpdatedAt = time.Now()

	if err := h.store.UpdateWorkspace(r.Context(), workspace); err != nil {
		logger.Error("Failed to update workspace", logger.Fields{"id": workspaceID, "error": err})
		_ = orihttp.RespondInternalError(w, "Failed to add agent")
		return
	}
	if err := h.syncWorkspacePortableStateToFileStore(workspace); err != nil {
		logger.Warn("Failed to sync workspace.json after adding workspace agent", logger.Fields{"id": workspaceID, "error": err})
	}

	logger.Info("Agent added to workspace", logger.Fields{
		"workspace_id":    workspaceID,
		"agent_name":      req.AgentName,
		"instance_id":     newInstance.ID,
		"instance_number": instanceNumber,
	})

	_ = orihttp.RespondCreated(w, map[string]interface{}{
		"success":        true,
		"agent_instance": newInstance,
		"workspace":      workspace,
	})
}

// removeWorkspaceAgent handles DELETE /api/workspaces/{id}/agents/{name}.
func (h *Handler) removeWorkspaceAgent(w http.ResponseWriter, r *http.Request, workspaceID, agentIdentifier string) {
	if agentIdentifier == "" {
		_ = orihttp.RespondBadRequest(w, "agent name or instance ID is required")
		return
	}

	// Get the workspace
	workspace, err := h.store.GetWorkspace(r.Context(), workspaceID)
	if err == session.ErrWorkspaceNotFound {
		_ = orihttp.RespondNotFound(w, "Workspace not found")
		return
	}
	if err != nil {
		logger.Error("Failed to get workspace", logger.Fields{"id": workspaceID, "error": err})
		_ = orihttp.RespondInternalError(w, "Failed to get workspace")
		return
	}

	agentIdentifier = resolveWorkspaceAgentIdentifier(workspace, agentIdentifier)

	// Try to find and remove by instance ID first, then by node ID, then by name.
	removed := false
	removedNames := make(map[string]struct{})
	removedNodeIDs := make(map[string]struct{})
	removedInstanceIDs := make(map[string]struct{})
	newInstances := make([]session.AgentInstance, 0, len(workspace.AgentInstances))
	for _, inst := range workspace.AgentInstances {
		if inst.ID == agentIdentifier || inst.NodeID == agentIdentifier || inst.Name == agentIdentifier {
			removed = true
			if name := strings.TrimSpace(inst.Name); name != "" {
				removedNames[name] = struct{}{}
			}
			if nodeID := strings.TrimSpace(inst.NodeID); nodeID != "" {
				removedNodeIDs[nodeID] = struct{}{}
			}
			if instanceID := strings.TrimSpace(inst.ID); instanceID != "" {
				removedInstanceIDs[instanceID] = struct{}{}
			}
		} else {
			newInstances = append(newInstances, inst)
		}
	}

	if !removed && strings.TrimSpace(agentIdentifier) != "" {
		for _, name := range workspace.Agents {
			if name == agentIdentifier {
				removed = true
				removedNames[agentIdentifier] = struct{}{}
				break
			}
		}
	}

	if !removed {
		_ = orihttp.RespondNotFound(w, "Agent not found in workspace")
		return
	}

	entryAgentName := strings.TrimSpace(currentWorkspaceEntryAgentName(workspace))
	entryAgentRemoved := false
	for removedName := range removedNames {
		if strings.EqualFold(strings.TrimSpace(removedName), entryAgentName) {
			entryAgentRemoved = true
			break
		}
	}
	if entryAgentRemoved && len(newInstances) == 0 {
		_ = orihttp.RespondBadRequest(w, "workspace must keep an entry agent")
		return
	}

	workspace.AgentInstances = newInstances

	// Update legacy agents array - remove names that no longer have active instances.
	if len(removedNames) > 0 {
		remainingNames := make(map[string]struct{}, len(workspace.AgentInstances))
		for _, inst := range workspace.AgentInstances {
			if name := strings.TrimSpace(inst.Name); name != "" {
				remainingNames[name] = struct{}{}
			}
		}

		newAgents := make([]string, 0, len(workspace.Agents))
		for _, name := range workspace.Agents {
			if _, wasRemoved := removedNames[name]; wasRemoved {
				if _, stillPresent := remainingNames[name]; !stillPresent {
					continue
				}
			}
			newAgents = append(newAgents, name)
		}
		workspace.Agents = newAgents
	}

	if entryAgentRemoved && len(newInstances) > 0 {
		setWorkspaceEntryAgent(workspace, newInstances[0].Name)
	} else if entryAgentName != "" {
		setWorkspaceEntryAgent(workspace, entryAgentName)
	}

	if err := cleanupRemovedAgentWorkspaceState(workspace, removedNames, removedNodeIDs, removedInstanceIDs); err != nil {
		logger.Error("Failed to cleanup workspace state after removing agent", logger.Fields{"id": workspaceID, "error": err})
		_ = orihttp.RespondInternalError(w, "Failed to remove agent")
		return
	}

	workspace.UpdatedAt = time.Now()

	if err := h.store.UpdateWorkspace(r.Context(), workspace); err != nil {
		logger.Error("Failed to update workspace", logger.Fields{"id": workspaceID, "error": err})
		_ = orihttp.RespondInternalError(w, "Failed to remove agent")
		return
	}
	if err := h.syncWorkspacePortableStateToFileStore(workspace); err != nil {
		logger.Warn("Failed to sync workspace.json after removing workspace agent", logger.Fields{"id": workspaceID, "error": err})
	}

	logger.Info("Agent removed from workspace", logger.Fields{
		"workspace_id": workspaceID,
		"agent":        agentIdentifier,
	})

	orihttp.WriteJSON(w, map[string]interface{}{
		"success":   true,
		"workspace": workspace,
	})
}

func (h *Handler) buildWorkspaceDetailResponse(workspace *session.Workspace) map[string]interface{} {
	if workspace == nil {
		return map[string]interface{}{}
	}

	payload := make(map[string]interface{})
	if data, err := json.Marshal(workspace); err == nil {
		if err := json.Unmarshal(data, &payload); err != nil {
			logger.Warn("Failed to decode workspace payload for response", logger.Fields{"workspace_id": workspace.ID, "error": err})
		}
	} else {
		logger.Warn("Failed to encode workspace payload for response", logger.Fields{"workspace_id": workspace.ID, "error": err})
	}

	analyticsWorkspace := buildWorkspaceAnalyticsView(workspace)
	settings := workspacesettings.Extract(workspace.SharedData)
	payload["attachments"] = h.buildWorkspaceResponseAttachments(workspace)
	payload["primary_directory_id"] = workspacePrimaryDirectoryID(workspace)
	payload["entry_agent_name"] = availableWorkspaceEntryAgentName(workspace, h.agentStore)
	payload["agent_stats"] = analyticsWorkspace.GetAgentStats()
	payload["workspace_progress"] = analyticsWorkspace.GetWorkspaceProgress()
	payload["workspace_settings"] = settings
	payload["workspace_settings_effective_behavior"] = workspacesettings.BuildEffectiveBehavior(settings)

	return payload
}

func (h *Handler) buildWorkspaceResponseAttachments(workspace *session.Workspace) []agentworkspace.Attachment {
	if workspace == nil || len(workspace.AttachmentsJSON) == 0 {
		return []agentworkspace.Attachment{}
	}

	var attachments []agentworkspace.Attachment
	if err := json.Unmarshal(workspace.AttachmentsJSON, &attachments); err != nil {
		logger.Warn("Failed to decode workspace attachments for response", logger.Fields{"workspace_id": workspace.ID, "error": err})
		return []agentworkspace.Attachment{}
	}

	for i := range attachments {
		attachments[i] = agentworkspace.HydrateAttachment(attachments[i], h.workspaceStore)
	}

	return attachments
}

func buildWorkspaceAnalyticsView(workspace *session.Workspace) *agentworkspace.Workspace {
	analyticsWorkspace := &agentworkspace.Workspace{
		ID:          workspace.ID,
		Name:        workspace.Name,
		Kind:        string(workspace.Kind),
		Description: workspace.Description,
		FolderSlug:  workspace.FolderSlug,
		ProjectPath: workspace.ProjectPath,
		Agents:      append([]string{}, workspace.Agents...),
		SharedData:  workspace.SharedData,
		Status:      agentworkspace.WorkspaceStatus(workspace.Status),
		CreatedAt:   workspace.CreatedAt,
		UpdatedAt:   workspace.UpdatedAt,
		Layout:      toWorkspaceAnalyticsLayout(workspace.Layout),
	}

	if analyticsWorkspace.Status == "" {
		analyticsWorkspace.Status = agentworkspace.StatusActive
	}

	if len(workspace.AgentInstances) > 0 {
		analyticsWorkspace.AgentInstances = make([]agentworkspace.AgentInstance, len(workspace.AgentInstances))
		for i, inst := range workspace.AgentInstances {
			analyticsWorkspace.AgentInstances[i] = agentworkspace.AgentInstance{
				ID:             inst.ID,
				Name:           inst.Name,
				InstanceNumber: inst.InstanceNumber,
				NodeID:         inst.NodeID,
				Role:           inst.Role,
				Description:    inst.Description,
				EntryPoint:     inst.EntryPoint,
				CreatedAt:      inst.CreatedAt,
			}
		}
	}

	if len(workspace.TasksJSON) > 0 {
		if err := json.Unmarshal(workspace.TasksJSON, &analyticsWorkspace.Tasks); err != nil {
			logger.Warn("Failed to decode workspace tasks for analytics response", logger.Fields{"workspace_id": workspace.ID, "error": err})
		}
	}
	if analyticsWorkspace.Tasks == nil {
		analyticsWorkspace.Tasks = []agentworkspace.Task{}
	}

	return analyticsWorkspace
}

func toWorkspaceAnalyticsLayout(layout *session.CanvasLayout) *agentworkspace.CanvasLayout {
	if layout == nil {
		return nil
	}

	converted := &agentworkspace.CanvasLayout{
		Scale:   layout.Scale,
		OffsetX: layout.OffsetX,
		OffsetY: layout.OffsetY,
	}

	if len(layout.TaskPositions) > 0 {
		converted.TaskPositions = make(map[string]agentworkspace.Position, len(layout.TaskPositions))
		for key, value := range layout.TaskPositions {
			converted.TaskPositions[key] = agentworkspace.Position{X: value.X, Y: value.Y}
		}
	}
	if len(layout.AgentPositions) > 0 {
		converted.AgentPositions = make(map[string]agentworkspace.Position, len(layout.AgentPositions))
		for key, value := range layout.AgentPositions {
			converted.AgentPositions[key] = agentworkspace.Position{X: value.X, Y: value.Y}
		}
	}
	if len(layout.AttachmentPositions) > 0 {
		converted.AttachmentPositions = make(map[string]agentworkspace.Position, len(layout.AttachmentPositions))
		for key, value := range layout.AttachmentPositions {
			converted.AttachmentPositions[key] = agentworkspace.Position{X: value.X, Y: value.Y}
		}
	}
	if len(layout.SchedulerPositions) > 0 {
		converted.SchedulerPositions = make(map[string]agentworkspace.Position, len(layout.SchedulerPositions))
		for key, value := range layout.SchedulerPositions {
			converted.SchedulerPositions[key] = agentworkspace.Position{X: value.X, Y: value.Y}
		}
	}
	if len(layout.StorePositions) > 0 {
		converted.StorePositions = make(map[string]agentworkspace.Position, len(layout.StorePositions))
		for key, value := range layout.StorePositions {
			converted.StorePositions[key] = agentworkspace.Position{X: value.X, Y: value.Y}
		}
	}
	if len(layout.WorkflowConnections) > 0 {
		converted.WorkflowConnections = make([]agentworkspace.WorkflowConnectionLayout, len(layout.WorkflowConnections))
		for i, connection := range layout.WorkflowConnections {
			converted.WorkflowConnections[i] = agentworkspace.WorkflowConnectionLayout{
				ID:       connection.ID,
				From:     connection.From,
				FromPort: connection.FromPort,
				To:       connection.To,
				ToPort:   connection.ToPort,
				Color:    connection.Color,
				Animated: connection.Animated,
			}
		}
	}

	return converted
}

func resolveWorkspaceAgentIdentifier(workspace *session.Workspace, agentIdentifier string) string {
	trimmed := strings.TrimSpace(agentIdentifier)
	if workspace == nil || trimmed == "" || !strings.Contains(trimmed, ":") {
		return trimmed
	}

	parts := strings.SplitN(trimmed, ":", 2)
	agentName := strings.TrimSpace(parts[0])
	instanceNumber, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil {
		return trimmed
	}

	for _, inst := range workspace.AgentInstances {
		if strings.EqualFold(strings.TrimSpace(inst.Name), agentName) && inst.InstanceNumber == instanceNumber {
			if id := strings.TrimSpace(inst.ID); id != "" {
				return id
			}
			if nodeID := strings.TrimSpace(inst.NodeID); nodeID != "" {
				return nodeID
			}
		}
	}

	return trimmed
}

func cleanupRemovedAgentWorkspaceState(
	workspace *session.Workspace,
	removedNames map[string]struct{},
	removedNodeIDs map[string]struct{},
	removedInstanceIDs map[string]struct{},
) error {
	if workspace == nil {
		return nil
	}

	if workspace.Layout != nil {
		if len(removedNodeIDs) > 0 && workspace.Layout.AgentPositions != nil {
			for nodeID := range removedNodeIDs {
				delete(workspace.Layout.AgentPositions, nodeID)
			}
		}
		if len(removedNodeIDs) > 0 && len(workspace.Layout.WorkflowConnections) > 0 {
			filteredConnections := workspace.Layout.WorkflowConnections[:0]
			for _, connection := range workspace.Layout.WorkflowConnections {
				if _, removedFrom := removedNodeIDs[connection.From]; removedFrom {
					continue
				}
				if _, removedTo := removedNodeIDs[connection.To]; removedTo {
					continue
				}
				filteredConnections = append(filteredConnections, connection)
			}
			workspace.Layout.WorkflowConnections = filteredConnections
		}
	}

	if len(workspace.TasksJSON) > 0 && (len(removedNames) > 0 || len(removedNodeIDs) > 0) {
		var tasks []agentworkspace.Task
		if err := json.Unmarshal(workspace.TasksJSON, &tasks); err != nil {
			return fmt.Errorf("decode tasks: %w", err)
		}

		changed := false
		for i := range tasks {
			if _, removedNode := removedNodeIDs[strings.TrimSpace(tasks[i].AssignedNodeID)]; removedNode {
				tasks[i].To = "unassigned"
				tasks[i].AssignedNodeID = ""
				tasks[i].InputTaskIDs = nil
				changed = true
			} else if tasks[i].AssignedNodeID == "" {
				if _, removedName := removedNames[strings.TrimSpace(tasks[i].To)]; removedName {
					tasks[i].To = "unassigned"
					changed = true
				}
			}

			if _, removedName := removedNames[strings.TrimSpace(tasks[i].From)]; removedName {
				tasks[i].From = ""
				changed = true
			}
		}

		if changed {
			data, err := json.Marshal(tasks)
			if err != nil {
				return fmt.Errorf("encode tasks: %w", err)
			}
			workspace.TasksJSON = data
		}
	}

	if len(workspace.AgentMCPAccessJSON) > 0 && len(removedInstanceIDs) > 0 {
		var accessEntries []agentworkspace.WorkspaceAgentMCPAccess
		if err := json.Unmarshal(workspace.AgentMCPAccessJSON, &accessEntries); err != nil {
			return fmt.Errorf("decode agent mcp access: %w", err)
		}

		filteredAccess := accessEntries[:0]
		for _, entry := range accessEntries {
			if _, removed := removedInstanceIDs[strings.TrimSpace(entry.AgentInstanceID)]; removed {
				continue
			}
			filteredAccess = append(filteredAccess, entry)
		}

		data, err := json.Marshal(filteredAccess)
		if err != nil {
			return fmt.Errorf("encode agent mcp access: %w", err)
		}
		workspace.AgentMCPAccessJSON = data
	}

	return nil
}

// =============================================================================
// Workspace Layout Management
// =============================================================================

// handleWorkspaceLayout handles requests to /api/workspaces/{id}/layout.
func (h *Handler) handleWorkspaceLayout(w http.ResponseWriter, r *http.Request, workspaceID string) {
	switch r.Method {
	case http.MethodGet:
		h.getWorkspaceLayout(w, r, workspaceID)
	case http.MethodPut:
		h.saveWorkspaceLayout(w, r, workspaceID)
	case http.MethodPatch:
		h.saveWorkspaceLayout(w, r, workspaceID)
	default:
		_ = orihttp.RespondMethodNotAllowed(w)
	}
}

// getWorkspaceLayout handles GET /api/workspaces/{id}/layout.
func (h *Handler) getWorkspaceLayout(w http.ResponseWriter, r *http.Request, workspaceID string) {
	workspace, err := h.store.GetWorkspace(r.Context(), workspaceID)
	if err == session.ErrWorkspaceNotFound {
		_ = orihttp.RespondNotFound(w, "Workspace not found")
		return
	}
	if err != nil {
		logger.Error("Failed to get workspace", logger.Fields{"id": workspaceID, "error": err})
		_ = orihttp.RespondInternalError(w, "Failed to get workspace")
		return
	}

	layout := workspace.Layout
	if layout == nil {
		layout = &session.CanvasLayout{}
	}

	orihttp.WriteJSON(w, map[string]interface{}{
		"layout": layout,
	})
}

// saveWorkspaceLayout handles PUT/PATCH /api/workspaces/{id}/layout.
func (h *Handler) saveWorkspaceLayout(w http.ResponseWriter, r *http.Request, workspaceID string) {
	var layout session.CanvasLayout
	if !orihttp.ParseJSONBody(w, r, &layout) {
		return
	}

	// Get the workspace
	workspace, err := h.store.GetWorkspace(r.Context(), workspaceID)
	if err == session.ErrWorkspaceNotFound {
		_ = orihttp.RespondNotFound(w, "Workspace not found")
		return
	}
	if err != nil {
		logger.Error("Failed to get workspace", logger.Fields{"id": workspaceID, "error": err})
		_ = orihttp.RespondInternalError(w, "Failed to get workspace")
		return
	}

	workspace.Layout = &layout
	workspace.UpdatedAt = time.Now()

	if err := h.store.UpdateWorkspace(r.Context(), workspace); err != nil {
		logger.Error("Failed to update workspace layout", logger.Fields{"id": workspaceID, "error": err})
		_ = orihttp.RespondInternalError(w, "Failed to save layout")
		return
	}

	logger.Info("Workspace layout saved", logger.Fields{"workspace_id": workspaceID})

	orihttp.WriteJSON(w, map[string]interface{}{
		"success": true,
		"layout":  layout,
	})
}

// handleWorkspaceSyncStatus compares workspaces on disk (FileStore cache) against
// the primary SQLite store and returns any mismatches.
func (h *Handler) handleWorkspaceSyncStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		_ = orihttp.RespondMethodNotAllowed(w)
		return
	}

	if h.workspaceStore == nil {
		orihttp.WriteJSON(w, agentworkspace.SyncStatus{InSync: true})
		return
	}

	ctx := r.Context()

	// Get all workspaces from the primary SQLite store.
	sqliteWorkspaces, err := h.store.ListWorkspaces(ctx)
	if err != nil {
		logger.Error("Sync status: failed to list workspaces from store", logger.Fields{"error": err})
		_ = orihttp.RespondInternalError(w, "Failed to list workspaces")
		return
	}

	sqliteIDs := make(map[string]session.Workspace, len(sqliteWorkspaces))
	for _, ws := range sqliteWorkspaces {
		sqliteIDs[ws.ID] = ws
	}

	// Get all workspaces from the FileStore disk cache.
	diskCache := h.workspaceStore.CachedWorkspaces()

	var unregistered []agentworkspace.SyncWorkspaceInfo
	var orphaned []agentworkspace.SyncWorkspaceInfo

	// Disk → Store: on disk but not in SQLite.
	for id, ws := range diskCache {
		path, err := h.workspaceStore.GetFolderPath(id)
		if err != nil {
			continue
		}
		existsOnDisk, err := workspaceFolderExists(path)
		if err != nil || !existsOnDisk {
			continue
		}
		if _, exists := sqliteIDs[id]; !exists {
			unregistered = append(unregistered, agentworkspace.SyncWorkspaceInfo{
				ID:   id,
				Name: ws.Name,
				Path: path,
			})
		}
	}

	// Store → Disk: in SQLite but not on disk.
	for id, ws := range sqliteIDs {
		path, managed := h.syncManagedWorkspacePath(ws)
		if !managed {
			continue
		}
		existsOnDisk, err := workspaceFolderExists(path)
		if err != nil || existsOnDisk {
			continue
		}
		orphaned = append(orphaned, agentworkspace.SyncWorkspaceInfo{
			ID:   id,
			Name: ws.Name,
			Path: path,
		})
	}

	orihttp.WriteJSON(w, agentworkspace.SyncStatus{
		InSync:       len(unregistered) == 0 && len(orphaned) == 0,
		Unregistered: unregistered,
		Orphaned:     orphaned,
	})
}

// handleWorkspaceSync imports unregistered disk workspaces and/or cleans up orphaned entries.
func (h *Handler) handleWorkspaceSync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		_ = orihttp.RespondMethodNotAllowed(w)
		return
	}

	var req struct {
		Import   []string                     `json:"import"`
		Cleanup  []string                     `json:"cleanup"`
		Locate   []workspaceSyncLocateRequest `json:"locate"`
		Recreate []string                     `json:"recreate"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	if h.workspaceStore == nil && (len(req.Locate) > 0 || len(req.Recreate) > 0) {
		_ = orihttp.RespondBadRequest(w, "workspace folder store is unavailable")
		return
	}

	validatedLocate := make([]workspaceSyncLocateRequest, 0, len(req.Locate))
	for _, item := range req.Locate {
		item.ID = strings.TrimSpace(item.ID)
		item.Path = strings.TrimSpace(item.Path)
		if item.ID == "" {
			_ = orihttp.RespondBadRequest(w, "locate action requires workspace id")
			return
		}
		if item.Path == "" {
			_ = orihttp.RespondBadRequest(w, "locate action requires a folder path")
			return
		}

		normalizedPath, err := normalizeImportPath(item.Path)
		if err != nil {
			_ = orihttp.RespondBadRequest(w, fmt.Sprintf("invalid locate path for workspace %s: %v", item.ID, err))
			return
		}
		info, err := os.Stat(normalizedPath)
		if err != nil {
			_ = orihttp.RespondBadRequest(w, fmt.Sprintf("locate path is not accessible for workspace %s: %v", item.ID, err))
			return
		}
		if !info.IsDir() {
			_ = orihttp.RespondBadRequest(w, fmt.Sprintf("locate path must be a directory for workspace %s", item.ID))
			return
		}

		item.Path = normalizedPath
		validatedLocate = append(validatedLocate, item)
	}
	req.Locate = validatedLocate

	validatedRecreate := make([]string, 0, len(req.Recreate))
	for _, id := range req.Recreate {
		trimmedID := strings.TrimSpace(id)
		if trimmedID == "" {
			_ = orihttp.RespondBadRequest(w, "recreate action requires workspace id")
			return
		}
		validatedRecreate = append(validatedRecreate, trimmedID)
	}
	req.Recreate = validatedRecreate

	ctx := r.Context()
	var imported, cleaned, located, recreated int
	warnings := make([]string, 0)

	if h.workspaceStore != nil {
		for _, item := range req.Locate {
			sessionWS, err := h.store.GetWorkspace(ctx, item.ID)
			if err == session.ErrWorkspaceNotFound {
				warnings = append(warnings, fmt.Sprintf("Workspace %s was not found", item.ID))
				continue
			}
			if err != nil {
				logger.Warn("Sync locate: failed to load workspace", logger.Fields{"id": item.ID, "error": err})
				warnings = append(warnings, fmt.Sprintf("Failed to load workspace %s", item.ID))
				continue
			}
			if sessionWS.IsGroup() {
				warnings = append(warnings, fmt.Sprintf("Workspace %s is a group and cannot be rebound", sessionWS.Name))
				continue
			}
			if isFolderImportedWorkspace(*sessionWS) {
				warnings = append(warnings, fmt.Sprintf("Workspace %s is a folder import and cannot be rebound", sessionWS.Name))
				continue
			}

			oldPath, _ := h.syncManagedWorkspacePath(*sessionWS)
			if err := updateManagedWorkspaceReferences(sessionWS, oldPath, item.Path); err != nil {
				logger.Warn("Sync locate: failed to update workspace folder references", logger.Fields{"id": item.ID, "error": err})
				warnings = append(warnings, fmt.Sprintf("Failed to update workspace references for %s", sessionWS.Name))
				continue
			}

			sessionWS.UpdatedAt = time.Now()
			folderWS, err := buildFileStoreWorkspace(sessionWS)
			if err != nil {
				logger.Warn("Sync locate: failed to build workspace payload", logger.Fields{"id": item.ID, "error": err})
				warnings = append(warnings, fmt.Sprintf("Failed to rebuild workspace folder for %s", sessionWS.Name))
				continue
			}
			if err := h.workspaceStore.RebindExistingFolder(folderWS, item.Path); err != nil {
				logger.Warn("Sync locate: failed to rebind workspace folder", logger.Fields{"id": item.ID, "error": err})
				warnings = append(warnings, fmt.Sprintf("Failed to locate folder for %s", sessionWS.Name))
				continue
			}
			if err := h.store.UpdateWorkspace(ctx, sessionWS); err != nil {
				logger.Warn("Sync locate: failed to persist workspace metadata", logger.Fields{"id": item.ID, "error": err})
				warnings = append(warnings, fmt.Sprintf("Located %s but failed to save updated paths", sessionWS.Name))
				continue
			}
			located++
		}
	}

	if h.workspaceStore != nil {
		for _, id := range req.Recreate {
			sessionWS, err := h.store.GetWorkspace(ctx, id)
			if err == session.ErrWorkspaceNotFound {
				warnings = append(warnings, fmt.Sprintf("Workspace %s was not found", id))
				continue
			}
			if err != nil {
				logger.Warn("Sync recreate: failed to load workspace", logger.Fields{"id": id, "error": err})
				warnings = append(warnings, fmt.Sprintf("Failed to load workspace %s", id))
				continue
			}
			if sessionWS.IsGroup() {
				warnings = append(warnings, fmt.Sprintf("Workspace %s is a group and cannot be recreated", sessionWS.Name))
				continue
			}
			if isFolderImportedWorkspace(*sessionWS) {
				warnings = append(warnings, fmt.Sprintf("Workspace %s is a folder import and cannot be recreated", sessionWS.Name))
				continue
			}

			targetPath, managed := h.syncManagedWorkspacePath(*sessionWS)
			if !managed || strings.TrimSpace(targetPath) == "" {
				warnings = append(warnings, fmt.Sprintf("Workspace %s does not have a recoverable folder path", sessionWS.Name))
				continue
			}

			if err := updateManagedWorkspaceReferences(sessionWS, targetPath, targetPath); err != nil {
				logger.Warn("Sync recreate: failed to update workspace folder references", logger.Fields{"id": id, "error": err})
				warnings = append(warnings, fmt.Sprintf("Failed to update workspace references for %s", sessionWS.Name))
				continue
			}

			sessionWS.UpdatedAt = time.Now()
			folderWS, err := buildFileStoreWorkspace(sessionWS)
			if err != nil {
				logger.Warn("Sync recreate: failed to build workspace payload", logger.Fields{"id": id, "error": err})
				warnings = append(warnings, fmt.Sprintf("Failed to rebuild workspace folder for %s", sessionWS.Name))
				continue
			}

			if err := os.MkdirAll(targetPath, 0755); err != nil {
				logger.Warn("Sync recreate: failed to create workspace folder", logger.Fields{"id": id, "path": targetPath, "error": err})
				warnings = append(warnings, fmt.Sprintf("Failed to recreate folder for %s", sessionWS.Name))
				continue
			}
			if err := h.workspaceStore.RebindExistingFolder(folderWS, targetPath); err != nil {
				logger.Warn("Sync recreate: failed to rebuild workspace folder", logger.Fields{"id": id, "path": targetPath, "error": err})
				warnings = append(warnings, fmt.Sprintf("Failed to recreate folder for %s", sessionWS.Name))
				continue
			}
			if err := h.restoreWorkspaceNoteFiles(ctx, sessionWS.ID); err != nil {
				logger.Warn("Sync recreate: failed to restore note files", logger.Fields{"id": id, "error": err})
				warnings = append(warnings, fmt.Sprintf("Recreated %s but failed to restore note files", sessionWS.Name))
			}
			if err := h.store.UpdateWorkspace(ctx, sessionWS); err != nil {
				logger.Warn("Sync recreate: failed to persist workspace metadata", logger.Fields{"id": id, "error": err})
				warnings = append(warnings, fmt.Sprintf("Recreated %s but failed to save workspace metadata", sessionWS.Name))
				continue
			}
			recreated++
		}
	}

	// Import: read workspace from FileStore cache, create in SQLite.
	if h.workspaceStore != nil {
		for _, id := range req.Import {
			diskWS, err := h.workspaceStore.Get(id)
			if err != nil {
				logger.Warn("Sync import: workspace not found on disk", logger.Fields{"id": id, "error": err})
				continue
			}
			sessionWS := session.ConvertAgentWorkspace(diskWS)
			if sessionWS == nil {
				warnings = append(warnings, fmt.Sprintf("Failed to convert %s", diskWS.Name))
				continue
			}
			if err := h.store.CreateWorkspace(ctx, sessionWS); err != nil {
				logger.Warn("Sync import: failed to create workspace in store",
					logger.Fields{"id": id, "name": diskWS.Name, "error": err})
				warnings = append(warnings, fmt.Sprintf("Failed to import %s", diskWS.Name))
				continue
			}
			imported++
		}
	}

	// Cleanup: delete orphaned entries from SQLite.
	for _, id := range req.Cleanup {
		if err := h.store.DeleteWorkspace(ctx, id); err != nil {
			logger.Warn("Sync cleanup: failed to delete orphaned workspace",
				logger.Fields{"id": id, "error": err})
			warnings = append(warnings, fmt.Sprintf("Failed to remove workspace %s", id))
			continue
		}
		cleaned++
	}

	orihttp.WriteJSON(w, map[string]any{
		"imported":  imported,
		"cleaned":   cleaned,
		"located":   located,
		"recreated": recreated,
		"warnings":  warnings,
	})
}

func workspaceFolderExists(path string) (bool, error) {
	info, err := os.Stat(strings.TrimSpace(path))
	if err == nil {
		return info.IsDir(), nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func isFolderImportedWorkspace(ws session.Workspace) bool {
	if ws.SharedData == nil {
		return false
	}

	raw, ok := ws.SharedData["folder_import"]
	if !ok || raw == nil {
		return false
	}

	meta, ok := raw.(map[string]interface{})
	if !ok {
		return false
	}

	if enabled, exists := meta["enabled"]; exists {
		switch value := enabled.(type) {
		case bool:
			if value {
				return true
			}
		case string:
			if strings.EqualFold(strings.TrimSpace(value), "true") {
				return true
			}
		}
	}

	return strings.TrimSpace(fmt.Sprint(meta["path"])) != ""
}

func cleanWorkspaceSyncPath(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return ""
	}
	if absPath, err := filepath.Abs(trimmed); err == nil {
		trimmed = absPath
	}
	return filepath.Clean(trimmed)
}

func (h *Handler) syncManagedWorkspacePath(ws session.Workspace) (string, bool) {
	if h == nil || h.workspaceStore == nil || ws.IsGroup() || isFolderImportedWorkspace(ws) {
		return "", false
	}

	if path, err := h.workspaceStore.GetFolderPath(ws.ID); err == nil {
		if cleaned := cleanWorkspaceSyncPath(path); cleaned != "" {
			return cleaned, true
		}
	}

	refs, err := decodeDirectoryReferences(ws.DirectoryReferencesJSON)
	if err != nil {
		return "", false
	}

	folderSlug := strings.TrimSpace(ws.FolderSlug)
	for _, ref := range refs {
		if folderSlug != "" && strings.EqualFold(strings.TrimSpace(ref.Name), folderSlug) {
			if cleaned := cleanWorkspaceSyncPath(ref.Path); cleaned != "" {
				return cleaned, true
			}
		}
	}

	return "", false
}

func updateManagedWorkspaceReferences(workspace *session.Workspace, oldPath string, newPath string) error {
	if workspace == nil {
		return fmt.Errorf("workspace is required")
	}

	now := time.Now()
	normalizedOld := cleanWorkspaceSyncPath(oldPath)
	normalizedNew := cleanWorkspaceSyncPath(newPath)
	if normalizedNew == "" {
		return fmt.Errorf("new workspace folder path is required")
	}

	refs, err := decodeDirectoryReferences(workspace.DirectoryReferencesJSON)
	if err != nil {
		return fmt.Errorf("failed to decode directory references: %w", err)
	}

	matchedReference := false
	for i := range refs {
		refPath := cleanWorkspaceSyncPath(refs[i].Path)
		if refPath == "" {
			continue
		}
		if refPath == normalizedOld || (strings.TrimSpace(workspace.FolderSlug) != "" && strings.EqualFold(strings.TrimSpace(refs[i].Name), strings.TrimSpace(workspace.FolderSlug))) {
			refs[i].Path = normalizedNew
			refs[i].UpdatedAt = now
			matchedReference = true
		}
	}
	if !matchedReference {
		refs = append(refs, workspaceDirectoryReference{
			ID:          uuid.New().String(),
			WorkspaceID: workspace.ID,
			Name:        strings.TrimSpace(workspace.FolderSlug),
			Path:        normalizedNew,
			X:           400,
			Y:           300,
			CreatedAt:   now,
			UpdatedAt:   now,
		})
	}

	refData, err := json.Marshal(refs)
	if err != nil {
		return fmt.Errorf("failed to encode directory references: %w", err)
	}
	workspace.DirectoryReferencesJSON = refData

	bindings, err := decodeWorkspaceMCPBindings(workspace.MCPBindingsJSON)
	if err != nil {
		return fmt.Errorf("failed to decode workspace MCP bindings: %w", err)
	}

	matchedBinding := false
	for i := range bindings {
		if strings.EqualFold(strings.TrimSpace(bindings[i].Alias), "workspace-files") || workspaceBindingHasRoot(bindings[i].Config, normalizedOld) {
			if bindings[i].Config == nil {
				bindings[i].Config = make(map[string]interface{})
			}
			bindings[i].Config["roots"] = []string{normalizedNew}
			bindings[i].UpdatedAt = now
			bindings[i].Enabled = true
			matchedBinding = true
		}
	}
	if !matchedBinding {
		bindings = append(bindings, agentworkspace.WorkspaceMCPBinding{
			ID:         uuid.New().String(),
			ServerName: "filesystem",
			Alias:      "workspace-files",
			Enabled:    true,
			Config: map[string]interface{}{
				"roots": []string{normalizedNew},
			},
			CreatedAt: now,
			UpdatedAt: now,
		})
	}

	bindingData, err := json.Marshal(bindings)
	if err != nil {
		return fmt.Errorf("failed to encode workspace MCP bindings: %w", err)
	}
	workspace.MCPBindingsJSON = bindingData

	return nil
}

func (h *Handler) restoreWorkspaceNoteFiles(ctx context.Context, workspaceID string) error {
	if h == nil || h.workspaceStore == nil {
		return nil
	}

	noteItems, err := h.store.ListNotesByWorkspace(ctx, workspaceID)
	if err != nil {
		return fmt.Errorf("list notes: %w", err)
	}

	for _, item := range noteItems {
		note, err := h.store.GetNote(ctx, item.ID)
		if err != nil {
			return fmt.Errorf("get note %s: %w", item.ID, err)
		}
		h.syncNoteToFile(note)
	}

	return nil
}

func workspaceBindingHasRoot(config map[string]interface{}, path string) bool {
	if len(config) == 0 || strings.TrimSpace(path) == "" {
		return false
	}

	rawRoots, ok := config["roots"]
	if !ok || rawRoots == nil {
		return false
	}

	switch roots := rawRoots.(type) {
	case []string:
		for _, root := range roots {
			if cleanWorkspaceSyncPath(root) == path {
				return true
			}
		}
	case []interface{}:
		for _, root := range roots {
			if cleanWorkspaceSyncPath(fmt.Sprint(root)) == path {
				return true
			}
		}
	}

	return false
}

func decodeWorkspaceMCPBindings(raw json.RawMessage) ([]agentworkspace.WorkspaceMCPBinding, error) {
	if len(raw) == 0 {
		return []agentworkspace.WorkspaceMCPBinding{}, nil
	}
	var bindings []agentworkspace.WorkspaceMCPBinding
	if err := json.Unmarshal(raw, &bindings); err != nil {
		return nil, err
	}
	return bindings, nil
}

func buildFileStoreWorkspace(workspace *session.Workspace) (*agentworkspace.Workspace, error) {
	if workspace == nil {
		return nil, fmt.Errorf("workspace is required")
	}

	folderWS := &agentworkspace.Workspace{
		ID:             workspace.ID,
		Name:           workspace.Name,
		Kind:           string(workspace.Kind),
		Description:    workspace.Description,
		FolderSlug:     workspace.FolderSlug,
		ProjectPath:    workspace.ProjectPath,
		ParentID:       workspace.ParentID,
		Agents:         append([]string{}, workspace.Agents...),
		AgentInstances: toWorkspaceAgentInstances(workspace.AgentInstances),
		SharedData:     workspace.SharedData,
		Status:         agentworkspace.WorkspaceStatus(workspace.Status),
		CreatedAt:      workspace.CreatedAt,
		UpdatedAt:      workspace.UpdatedAt,
	}

	if folderWS.Status == "" {
		folderWS.Status = agentworkspace.StatusActive
	}

	if workspace.Layout != nil {
		layoutData, err := json.Marshal(workspace.Layout)
		if err != nil {
			return nil, fmt.Errorf("failed to encode workspace layout: %w", err)
		}
		var layout agentworkspace.CanvasLayout
		if err := json.Unmarshal(layoutData, &layout); err != nil {
			return nil, fmt.Errorf("failed to decode workspace layout: %w", err)
		}
		folderWS.Layout = &layout
	}

	if err := decodeSessionWorkspaceJSONField(workspace.MessagesJSON, &folderWS.Messages); err != nil {
		return nil, fmt.Errorf("failed to decode workspace messages: %w", err)
	}
	if err := decodeSessionWorkspaceJSONField(workspace.TasksJSON, &folderWS.Tasks); err != nil {
		return nil, fmt.Errorf("failed to decode workspace tasks: %w", err)
	}
	if err := decodeSessionWorkspaceJSONField(workspace.AttachmentsJSON, &folderWS.Attachments); err != nil {
		return nil, fmt.Errorf("failed to decode workspace attachments: %w", err)
	}
	if err := decodeSessionWorkspaceJSONField(workspace.ScheduledTasksJSON, &folderWS.ScheduledTasks); err != nil {
		return nil, fmt.Errorf("failed to decode workspace schedules: %w", err)
	}
	if err := decodeSessionWorkspaceJSONField(workspace.StoreNodesJSON, &folderWS.StoreNodes); err != nil {
		return nil, fmt.Errorf("failed to decode workspace store nodes: %w", err)
	}
	if err := decodeSessionWorkspaceJSONField(workspace.WorkflowsJSON, &folderWS.Workflows); err != nil {
		return nil, fmt.Errorf("failed to decode workspace workflows: %w", err)
	}
	if err := decodeSessionWorkspaceJSONField(workspace.DirectoryReferencesJSON, &folderWS.DirectoryReferences); err != nil {
		return nil, fmt.Errorf("failed to decode workspace directory references: %w", err)
	}
	if err := decodeSessionWorkspaceJSONField(workspace.MCPBindingsJSON, &folderWS.MCPBindings); err != nil {
		return nil, fmt.Errorf("failed to decode workspace MCP bindings: %w", err)
	}
	if err := decodeSessionWorkspaceJSONField(workspace.AgentMCPAccessJSON, &folderWS.AgentMCPAccess); err != nil {
		return nil, fmt.Errorf("failed to decode workspace agent MCP access: %w", err)
	}
	if err := decodeSessionWorkspaceJSONField(workspace.SkillBindingsJSON, &folderWS.SkillBindings); err != nil {
		return nil, fmt.Errorf("failed to decode workspace skill bindings: %w", err)
	}
	if err := decodeSessionWorkspaceJSONField(workspace.AgentSkillAccessJSON, &folderWS.AgentSkillAccess); err != nil {
		return nil, fmt.Errorf("failed to decode workspace agent skill access: %w", err)
	}

	return folderWS, nil
}

func decodeSessionWorkspaceJSONField(raw json.RawMessage, target any) error {
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, target)
}
