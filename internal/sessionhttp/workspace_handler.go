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
	"strings"
	"time"

	"github.com/google/uuid"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/session"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

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
	}

	// Handle sub-paths like {id}/agents, {id}/layout
	if path != "" && strings.Contains(path, "/") {
		parts := strings.SplitN(path, "/", 3)
		id := parts[0]
		subPath := parts[1]

		switch subPath {
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

// createWorkspace handles POST /api/workspaces.
func (h *Handler) createWorkspace(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description,omitempty"`
		ParentID    string `json:"parent_id,omitempty"`
		OrderIndex  *int   `json:"order_index,omitempty"`
		Color       string `json:"color,omitempty"`
		ProjectPath string `json:"project_path,omitempty"`
		Location    string `json:"location,omitempty"` // Optional custom directory for workspace folder (overrides default root)
	}

	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	if req.Name == "" {
		_ = orihttp.RespondBadRequest(w, "name is required")
		return
	}

	ws := &session.Workspace{
		Name:        req.Name,
		Description: req.Description,
		ParentID:    req.ParentID,
		Color:       req.Color,
		FolderSlug:  agentworkspace.Slugify(req.Name),
		ProjectPath: req.ProjectPath,
	}
	if req.OrderIndex != nil {
		ws.OrderIndex = *req.OrderIndex
	}

	if err := h.store.CreateWorkspace(r.Context(), ws); err != nil {
		logger.Error("Failed to create workspace", logger.Fields{"error": err})
		_ = orihttp.RespondInternalError(w, "Failed to create workspace")
		return
	}

	// Create workspace folder on disk if folder-based store is available
	if h.workspaceStore != nil {
		folderWS := &agentworkspace.Workspace{
			ID:          ws.ID,
			Name:        ws.Name,
			Description: ws.Description,
			FolderSlug:  ws.FolderSlug,
			ProjectPath: ws.ProjectPath,
			ParentID:    ws.ParentID,
			Status:      agentworkspace.StatusActive,
			CreatedAt:   ws.CreatedAt,
			UpdatedAt:   ws.UpdatedAt,
		}

		var folderErr error
		if req.Location != "" {
			// Custom location — create folder at the user-specified directory
			folderErr = h.workspaceStore.SaveAt(folderWS, req.Location)
		} else {
			// Default location (workspace root from settings)
			folderErr = h.workspaceStore.Save(folderWS)
		}

		if folderErr != nil {
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
			folderWS.SharedData = make(map[string]interface{})
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

	logger.Info("Workspace created", logger.Fields{"id": ws.ID, "name": req.Name, "folder_slug": ws.FolderSlug})

	_ = orihttp.RespondCreated(w, map[string]interface{}{
		"success": true,
		"folder":  ws,
	})
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

	orihttp.WriteJSON(w, workspace)
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
		Name        *string `json:"name,omitempty"`
		Description *string `json:"description,omitempty"`
		ParentID    *string `json:"parent_id,omitempty"`
		OrderIndex  *int    `json:"order_index,omitempty"`
		Color       *string `json:"color,omitempty"`
		ProjectPath *string `json:"project_path,omitempty"`
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
	if req.ProjectPath != nil {
		workspace.ProjectPath = *req.ProjectPath
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
	if h.workspaceStore != nil {
		if err := h.workspaceStore.Delete(id); err != nil {
			logger.Warn("Failed to delete workspace folder", logger.Fields{"id": id, "error": err})
			// Non-fatal: SQLite deletion succeeded
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

		orihttp.WriteJSON(w, map[string]interface{}{
			"folders": workspaces,
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

	orihttp.WriteJSON(w, map[string]interface{}{
		"folders": workspaces,
	})
}

// handleWorkspaceRename handles POST /api/workspaces/{id}/rename.
func (h *Handler) handleWorkspaceRename(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		_ = orihttp.RespondMethodNotAllowed(w)
		return
	}

	var req struct {
		Name string `json:"name"`
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

	ws.Name = req.Name
	ws.FolderSlug = agentworkspace.Slugify(req.Name)
	ws.UpdatedAt = time.Now()

	if err := h.store.UpdateWorkspace(ctx, ws); err != nil {
		_ = orihttp.RespondInternalError(w, "Failed to rename workspace")
		return
	}

	// Rename the folder if folder-based store is available
	if h.workspaceStore != nil {
		if err := h.workspaceStore.Rename(id, req.Name); err != nil {
			logger.Warn("Failed to rename workspace folder", logger.Fields{"id": id, "error": err})
			// Non-fatal: SQLite rename succeeded
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
	Name           string `json:"name,omitempty"`
	Description    string `json:"description,omitempty"`
	ParentID       string `json:"parent_id,omitempty"`
	OrderIndex     *int   `json:"order_index,omitempty"`
	Color          string `json:"color,omitempty"`
	Path           string `json:"path"`
	AllowDuplicate bool   `json:"allow_duplicate,omitempty"`
	EntryPoint     string `json:"entry_point,omitempty"`
}

type workspaceImportDuplicate struct {
	Found         bool   `json:"found"`
	WorkspaceID   string `json:"workspace_id,omitempty"`
	WorkspaceName string `json:"workspace_name,omitempty"`
	DirectoryID   string `json:"directory_id,omitempty"`
	Path          string `json:"path,omitempty"`
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

	workspace := &session.Workspace{
		Name:        workspaceName,
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

	workspace.UpdatedAt = time.Now()

	if err := h.store.UpdateWorkspace(r.Context(), workspace); err != nil {
		logger.Error("Failed to update workspace", logger.Fields{"id": workspaceID, "error": err})
		_ = orihttp.RespondInternalError(w, "Failed to add agent")
		return
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

	logger.Info("Agent removed from workspace", logger.Fields{
		"workspace_id": workspaceID,
		"agent":        agentIdentifier,
	})

	orihttp.WriteJSON(w, map[string]interface{}{
		"success":   true,
		"workspace": workspace,
	})
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
