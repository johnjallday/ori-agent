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
	"github.com/johnjallday/ori-agent/internal/platform"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/session"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacesettings"
)

var errParentWorkspaceMustBeGroup = errors.New("parent workspace must be a group")

// workspaceSharedDataPrimaryDirectoryIDKey mirrors projecttemplates.PrimaryDirectoryIDKey
// so this package and the workspace_create_project chat tool agree on the
// SharedData key used to record a workspace's primary linked directory.
const workspaceSharedDataPrimaryDirectoryIDKey = projecttemplates.PrimaryDirectoryIDKey

// workspaceTrashSharedDataKey is the SharedData key under which trash metadata
// ({original_path, trashed_path, deleted_at}) is stored while a workspace is
// trashed, so its folder can be moved back on restore.
const workspaceTrashSharedDataKey = "_trash"

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
	case "rescan":
		h.handleWorkspaceRescan(w, r)
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
		case "project":
			h.handleWorkspaceProject(w, r, id)
			return
		case "rename":
			h.handleWorkspaceRename(w, r, id)
			return
		case "restore":
			h.restoreWorkspace(w, r, id)
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

func normalizeWorkspaceBootstrap(input *workspaceBootstrapRequest) map[string]any {
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

	return map[string]any{
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
		TemplateID         string                     `json:"template_id,omitempty"`   // Optional project template from the library
		TemplatePath       string                     `json:"template_path,omitempty"` // Optional arbitrary folder used as a project template. NOT restricted to the templates library: resolveProjectTemplate/LoadFolder will stat and copy from any path the caller supplies. Acceptable for this admin-facing, local-first, single-user app; do not expose this endpoint to untrusted callers without adding a path allowlist.
		ProjectName        string                     `json:"project_name,omitempty"`  // Project name for template instantiation (defaults to the workspace name)
		Tags               []string                   `json:"tags,omitempty"`          // Optional initial tags; merged with template tags
	}

	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	if req.Name == "" {
		_ = orihttp.RespondBadRequest(w, "name is required")
		return
	}

	requestedTags, err := agentworkspace.ValidateWorkspaceTags(req.Tags)
	if err != nil {
		_ = orihttp.RespondBadRequest(w, err.Error())
		return
	}

	kind, err := parseWorkspaceKind(req.Kind)
	if err != nil {
		_ = orihttp.RespondBadRequest(w, err.Error())
		return
	}
	if err := h.requireGroupParent(r.Context(), req.ParentID); err != nil {
		handleWorkspaceParentError(w, err)
		return
	}

	wantsProject := strings.TrimSpace(req.TemplateID) != "" || strings.TrimSpace(req.TemplatePath) != ""
	if strings.TrimSpace(req.TemplateID) != "" && strings.TrimSpace(req.TemplatePath) != "" {
		_ = orihttp.RespondBadRequest(w, "specify either template_id or template_path, not both")
		return
	}
	if wantsProject && kind == session.WorkspaceKindGroup {
		_ = orihttp.RespondBadRequest(w, "group workspaces cannot be created from a project template")
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
		Tags:        requestedTags,
	}
	if wantsProject {
		if tpl, tplErr := h.resolveProjectTemplate(req.TemplateID, req.TemplatePath); tplErr == nil {
			ws.Tags = agentworkspace.MergeWorkspaceTags(ws.Tags, tpl.Tags)
		}
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
			ws.SharedData = make(map[string]any)
		}
		ws.SharedData["workspace_bootstrap"] = bootstrapData
	}

	// If an existing entry agent was specified, validate and set it.
	// Groups without one get a "<Name> Manager" agent auto-created so they are
	// chat-ready immediately. Concrete workspaces are created without an entry
	// agent; the UI prompts the user to create one with their choice of
	// model/provider.
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
	} else if kind == session.WorkspaceKindGroup {
		if agentName := h.autoCreateGroupEntryAgent(ws); agentName != "" {
			setWorkspaceEntryAgent(ws, agentName)
		}
	}

	if err := h.store.CreateWorkspace(r.Context(), ws); err != nil {
		logger.Error("Failed to create workspace", logger.Fields{"error": err})
		_ = orihttp.RespondInternalError(w, "Failed to create workspace")
		return
	}

	// Create workspace folder on disk if folder-based store is available.
	// Groups get the same executable scaffolding as real workspaces, scoped to
	// their own content directories so member sub-workspaces stay hidden.
	projectWarning := ""
	if wantsProject {
		// Default/fallback message for every path below that does not reach
		// (or does not succeed in) instantiateWorkspaceProject: workspaceStore
		// being nil, folder creation failing, or GetFolderPath failing all
		// leave the workspace without a usable folder, so "workspace folder
		// unavailable" is accurate for each of them. It is overwritten with a
		// more specific message (or cleared on success) only inside the
		// `if wantsProject` branch nested under the non-group folder-creation
		// success path below. Set here, ahead of the workspaceStore nil-check,
		// so it covers every one of those early-exit paths without each of
		// them having to remember to set it.
		projectWarning = "workspace was created, but the project template was not applied: workspace folder unavailable"
	}
	if h.workspaceStore != nil {
		folderWS := &agentworkspace.Workspace{
			ID:             ws.ID,
			Name:           ws.Name,
			Kind:           string(ws.Kind),
			Description:    ws.Description,
			FolderSlug:     ws.FolderSlug,
			ProjectPath:    ws.ProjectPath,
			Tags:           append([]string(nil), ws.Tags...),
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
			if ws.Kind == session.WorkspaceKindGroup {
				// Groups physically nest members under sub-workspaces/, so
				// their linked folder and MCP roots are scoped to the group's
				// own files/ and notes/ — never the folder root — keeping
				// member content hidden from group agents.
				if dirs, mkErr := ensureGroupContentDirs(folderPath); mkErr != nil {
					logger.Warn("Failed to create group content directories", logger.Fields{"id": ws.ID, "error": mkErr})
				} else {
					h.provisionWorkspaceScaffolding(r.Context(), ws, folderWS, dirs.files, dirs.mcpRoots())
				}
				logger.Info("Group folder created on disk", logger.Fields{"id": ws.ID, "path": folderPath})
			} else {
				h.provisionWorkspaceScaffolding(r.Context(), ws, folderWS, folderPath, []string{folderPath})
				logger.Info("Workspace folder created on disk", logger.Fields{"id": ws.ID, "path": folderPath})

				if wantsProject {
					// Non-fatal by design: a failed instantiation must not fail
					// workspace creation. The warning is surfaced to the user.
					if err := h.instantiateWorkspaceProject(r.Context(), ws, folderWS, req.TemplateID, req.TemplatePath, req.ProjectName); err != nil {
						projectWarning = fmt.Sprintf("workspace was created, but the project template was not applied: %v", err)
						logger.Warn("Project template instantiation failed", logger.Fields{"id": ws.ID, "error": err})
					} else {
						projectWarning = ""
					}
				}
			}
		}
	}

	logger.Info("Workspace created", logger.Fields{"id": ws.ID, "name": req.Name, "folder_slug": ws.FolderSlug, "kind": ws.Kind})

	response := map[string]any{
		"success": true,
		"folder":  ws,
	}
	if projectWarning != "" {
		response["project_warning"] = projectWarning
	}
	_ = orihttp.RespondCreated(w, response)
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

func (h *Handler) requireGroupParent(ctx context.Context, parentID string) error {
	parentID = strings.TrimSpace(parentID)
	if parentID == "" {
		return nil
	}

	parent, err := h.store.GetWorkspace(ctx, parentID)
	if err != nil {
		return err
	}
	if !parent.IsGroup() {
		return errParentWorkspaceMustBeGroup
	}
	return nil
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

// handleWorkspaceMoveError maps a FileStore folder-move failure onto an HTTP
// response.
func handleWorkspaceMoveError(w http.ResponseWriter, err error) {
	var slugConflict *agentworkspace.FolderSlugConflictError
	switch {
	case errors.As(err, &slugConflict):
		_ = orihttp.RespondConflict(w, "A workspace with the same folder name already exists in the destination group. Rename one of them and try again.")
	case errors.Is(err, agentworkspace.ErrMaxNestingDepthExceeded),
		errors.Is(err, agentworkspace.ErrMoveCreatesCycle),
		errors.Is(err, agentworkspace.ErrSelfParent):
		_ = orihttp.RespondBadRequest(w, err.Error())
	default:
		_ = orihttp.RespondInternalError(w, "Failed to move workspace")
	}
}

// workspaceHasActiveWork reports whether a workspace has durable in-flight work
// (a task in progress or awaiting a choice). Moving such a workspace is
// hard-blocked so a running task's working directory is not pulled out from
// under it. Completed/historical tasks do not count.
func workspaceHasActiveWork(ws *session.Workspace) bool {
	if ws == nil || len(ws.TasksJSON) == 0 {
		return false
	}
	var tasks []agentworkspace.Task
	if err := json.Unmarshal(ws.TasksJSON, &tasks); err != nil {
		// Unparseable task data: be conservative and treat as active so a
		// destructive move is not performed on uncertain state.
		logger.Warn("Active-work check: failed to parse tasks", logger.Fields{"id": ws.ID, "error": err})
		return true
	}
	for _, task := range tasks {
		switch task.Status {
		case agentworkspace.TaskStatusInProgress, agentworkspace.TaskStatusWaitingForChoice:
			return true
		}
	}
	return false
}

// firstActiveWorkBlocker returns the name of the first workspace — the target or
// any workspace nested within it — that has active work, or "" if none. Used to
// hard-block a move while work is in flight (req 12).
func (h *Handler) firstActiveWorkBlocker(ctx context.Context, id string) (string, error) {
	ids := []string{id}
	descendants, err := h.store.GetSubworkspaceIDs(ctx, id)
	if err != nil {
		return "", err
	}
	ids = append(ids, descendants...)

	for _, wid := range ids {
		ws, err := h.store.GetWorkspace(ctx, wid)
		if err != nil {
			if errors.Is(err, session.ErrWorkspaceNotFound) {
				continue
			}
			return "", err
		}
		if workspaceHasActiveWork(ws) {
			return ws.Name, nil
		}
	}
	return "", nil
}

// applyMoveReferenceUpdates fixes path-keyed references (directory references,
// MCP roots) and project_path for every workspace whose folder moved, groups
// included (groups carry scoped references to their own files/ and notes/).
// The moved node itself is updated in place on self so the caller's pending
// UpdateWorkspace persists it; descendants are reloaded and saved individually.
func (h *Handler) applyMoveReferenceUpdates(ctx context.Context, self *session.Workspace, moved []agentworkspace.MovedWorkspace) {
	for _, m := range moved {
		if self != nil && m.ID == self.ID {
			if err := updateManagedWorkspaceReferences(self, m.OldPath, m.NewPath); err != nil {
				logger.Warn("Move: failed to update references", logger.Fields{"id": m.ID, "error": err})
			}
			rewriteWorkspaceProjectPath(self, m.OldPath, m.NewPath)
			continue
		}
		descWS, err := h.store.GetWorkspace(ctx, m.ID)
		if err != nil {
			logger.Warn("Move: failed to load descendant for reference update", logger.Fields{"id": m.ID, "error": err})
			continue
		}
		refErr := updateManagedWorkspaceReferences(descWS, m.OldPath, m.NewPath)
		if refErr != nil {
			logger.Warn("Move: failed to update descendant references", logger.Fields{"id": m.ID, "error": refErr})
		}
		pathChanged := rewriteWorkspaceProjectPath(descWS, m.OldPath, m.NewPath)
		if refErr == nil || pathChanged {
			if err := h.store.UpdateWorkspace(ctx, descWS); err != nil {
				logger.Warn("Move: failed to persist descendant references", logger.Fields{"id": m.ID, "error": err})
			}
		}
	}
}

// rewriteWorkspaceProjectPath adjusts a workspace's project_path after its folder
// moved from oldPath to newPath. Only an absolute project_path that pointed
// *inside* the old workspace folder is rewritten to the new location; relative
// paths (resolved against a projects root that did not move) and external
// absolute paths are left unchanged. Returns true if the value changed.
func rewriteWorkspaceProjectPath(ws *session.Workspace, oldPath, newPath string) bool {
	if ws == nil {
		return false
	}
	p := strings.TrimSpace(ws.ProjectPath)
	if p == "" || !filepath.IsAbs(p) {
		return false
	}
	rel, err := filepath.Rel(oldPath, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	ws.ProjectPath = filepath.Join(newPath, rel)
	return true
}

// pruneHiddenWorkspaces removes workspaces that have been moved to the trash
// or whose folder is missing from disk, recursing into children so hidden
// sub-workspaces don't leak into the tree.
func pruneHiddenWorkspaces(workspaces []session.Workspace) []session.Workspace {
	if len(workspaces) == 0 {
		return workspaces
	}

	filtered := make([]session.Workspace, 0, len(workspaces))
	for _, ws := range workspaces {
		if ws.Status == session.WorkspaceStatusTrashed || ws.Status == session.WorkspaceStatusMissing {
			continue
		}
		ws.Children = pruneHiddenWorkspaces(ws.Children)
		filtered = append(filtered, ws)
	}
	return filtered
}

// requireWorkspace validates that workspaceID, when provided, refers to an
// existing workspace of any kind (groups hold sessions, notes, and direct work
// just like concrete workspaces).
func (h *Handler) requireWorkspace(ctx context.Context, workspaceID string) (*session.Workspace, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return nil, nil
	}

	return h.store.GetWorkspace(ctx, workspaceID)
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
		Tags               *[]string                  `json:"tags,omitempty"`
		PrimaryDirectoryID *string                    `json:"primary_directory_id,omitempty"`
		WorkspaceBootstrap *workspaceBootstrapRequest `json:"workspace_bootstrap,omitempty"`
	}

	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	h.hydrateWorkspaceMetadataInto(workspace)

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
				workspace.SharedData = make(map[string]any)
			}
			workspace.SharedData["workspace_bootstrap"] = bootstrapData
		} else if workspace.SharedData != nil {
			delete(workspace.SharedData, "workspace_bootstrap")
		}
	}
	if req.ProjectPath != nil {
		workspace.ProjectPath = *req.ProjectPath
	}
	if req.Tags != nil {
		tags, err := agentworkspace.ValidateWorkspaceTags(*req.Tags)
		if err != nil {
			_ = orihttp.RespondBadRequest(w, err.Error())
			return
		}
		workspace.Tags = tags
	}
	if req.PrimaryDirectoryID != nil {
		setWorkspacePrimaryDirectoryID(workspace, *req.PrimaryDirectoryID)
	}
	if req.ParentID != nil {
		newParentID := strings.TrimSpace(*req.ParentID)
		if newParentID != workspace.ParentID {
			// Self-parent guard.
			if newParentID == workspace.ID {
				_ = orihttp.RespondBadRequest(w, "Workspace cannot be its own parent")
				return
			}
			// Cycle guard: cannot move under one of our own descendants.
			if newParentID != "" {
				descendants, err := h.store.GetSubworkspaceIDs(r.Context(), workspace.ID)
				if err != nil {
					logger.Error("Failed to load workspace descendants", logger.Fields{"id": id, "error": err})
					_ = orihttp.RespondInternalError(w, "Failed to update workspace")
					return
				}
				for _, descendantID := range descendants {
					if descendantID == newParentID {
						_ = orihttp.RespondBadRequest(w, "Workspace cannot be moved under its descendant")
						return
					}
				}
			}
			// Destination must be a group; an empty parent moves to the root (ungroup).
			if err := h.requireGroupParent(r.Context(), newParentID); err != nil {
				handleWorkspaceParentError(w, err)
				return
			}
			// Eligibility: only managed workspaces can be grouped. A workspace
			// linked to an external folder can't be physically nested (req 23).
			if newParentID != "" && isFolderImportedWorkspace(*workspace) {
				_ = orihttp.RespondBadRequest(w, "This workspace is linked to an external folder and can't be grouped. Rebind it into the managed workspaces root first.")
				return
			}
			// Active-work hard block: never move a workspace (or, for a group,
			// any workspace nested inside it) while it has in-flight work (req 12).
			if blocker, err := h.firstActiveWorkBlocker(r.Context(), workspace.ID); err != nil {
				logger.Error("Failed to check workspace active work", logger.Fields{"id": id, "error": err})
				_ = orihttp.RespondInternalError(w, "Failed to update workspace")
				return
			} else if blocker != "" {
				_ = orihttp.RespondConflict(w, fmt.Sprintf("Stop the running task in %q before grouping this workspace.", blocker))
				return
			}
			// Physically move the folder tree when a folder store is available;
			// disk location is the source of truth for grouping. Falls back to a
			// metadata-only parent change when no folder store is configured.
			if h.workspaceStore != nil {
				moved, err := h.workspaceStore.MoveWorkspaceFolder(workspace.ID, newParentID)
				if err != nil {
					handleWorkspaceMoveError(w, err)
					return
				}
				h.applyMoveReferenceUpdates(r.Context(), workspace, moved)
			}
			workspace.ParentID = newParentID
		}
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
	} else if req.Tags != nil {
		if err := h.syncWorkspaceTagsToFileStore(workspace); err != nil {
			logger.Warn("Failed to sync workspace tags after workspace update", logger.Fields{"id": id, "error": err})
		}
	}

	logger.Info("Workspace updated", logger.Fields{"id": id})

	hydrated := h.hydrateWorkspaceMetadataFromFileStore(workspace)
	orihttp.WriteJSON(w, map[string]any{
		"success": true,
		"folder":  hydrated,
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
		orihttp.WriteJSON(w, map[string]any{
			"workspace_id":     id,
			"name":             ws.Name,
			"session_count":    sessionCount,
			"confirm_required": true,
			"message":          fmt.Sprintf("Workspace %q has %d sessions. Delete the workspace?", ws.Name, sessionCount),
		})
		return
	}

	deleteSessions := r.URL.Query().Get("delete_sessions") == "true"

	// Groups physically contain their members, so deletion has its own two-mode
	// flow (delete contents vs un-nest members to the root, then remove the
	// empty group). Handle it separately from regular workspaces.
	if ws.Kind == session.WorkspaceKindGroup {
		h.deleteGroup(w, r, ws, deleteSessions)
		return
	}

	// Soft delete (default): move the folder-backed workspace to the system
	// trash and mark it trashed so it can be restored from the hub. Explicit
	// delete_sessions=true requests, and platforms without system-trash
	// support fall through to a permanent delete below.
	if ws.Kind != session.WorkspaceKindGroup && !deleteSessions && h.workspaceStore != nil && platform.TrashSupported() {
		if _, ferr := h.workspaceStore.Get(id); ferr == nil {
			if err := h.trashWorkspace(ctx, ws); err != nil {
				logger.Error("Failed to move workspace to trash", logger.Fields{"id": id, "error": err})
				_ = orihttp.RespondInternalError(w, "Failed to move workspace to trash")
				return
			}
			logger.Info("Workspace moved to trash", logger.Fields{"id": id})
			orihttp.WriteJSON(w, map[string]any{"success": true, "id": id, "trashed": true})
			return
		}
	}

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

// deleteGroup handles deletion of a group workspace, which (unlike a regular
// workspace) physically contains its members. Modes (delete_mode query param):
//   - "contents": remove the entire group folder tree (all members and nested
//     groups). On platforms with system-trash support this is a reversible soft
//     delete (the folder tree moves to the Trash and can be restored from Undo);
//     with delete_sessions=true or no trash support it is a permanent delete.
//   - "group_only" (default): move each direct child out to the workspaces root
//     (un-nest) via the move op, then remove the now-empty group folder.
func (h *Handler) deleteGroup(w http.ResponseWriter, r *http.Request, ws *session.Workspace, deleteSessions bool) {
	ctx := r.Context()
	mode := strings.TrimSpace(r.URL.Query().Get("delete_mode"))
	if mode == "" {
		// Safe default: never destroy member data implicitly.
		mode = "group_only"
	}

	switch mode {
	case "contents":
		h.deleteGroupWithContents(w, ctx, ws, deleteSessions)
	case "group_only":
		h.deleteGroupOnly(w, ctx, ws, deleteSessions)
	default:
		_ = orihttp.RespondBadRequest(w, "delete_mode must be 'contents' or 'group_only'")
	}
}

// deleteGroupWithContents removes the group and every workspace nested inside it.
// When the platform supports a system trash (and the caller didn't ask to also
// delete sessions), the whole folder tree is moved to the Trash in one shot and
// the group + descendants are marked trashed so the entire group can be restored
// from Undo. Otherwise it falls through to a permanent delete from disk and the
// session store.
func (h *Handler) deleteGroupWithContents(w http.ResponseWriter, ctx context.Context, ws *session.Workspace, deleteSessions bool) {
	descendants, err := h.store.GetSubworkspaceIDs(ctx, ws.ID)
	if err != nil {
		logger.Error("Failed to load group descendants", logger.Fields{"id": ws.ID, "error": err})
		_ = orihttp.RespondInternalError(w, "Failed to delete group")
		return
	}

	// Soft delete (default): move the group's entire folder tree to the system
	// Trash and mark the group + every descendant trashed, preserving their rows
	// and sessions so the whole group can be restored from Undo. Explicit
	// delete_sessions=true requests and platforms without trash support fall
	// through to the permanent delete below.
	if !deleteSessions && h.workspaceStore != nil && platform.TrashSupported() {
		if _, ferr := h.workspaceStore.Get(ws.ID); ferr == nil {
			if err := h.trashGroupWithContents(ctx, ws, descendants); err != nil {
				logger.Error("Failed to move group to trash", logger.Fields{"id": ws.ID, "error": err})
				_ = orihttp.RespondInternalError(w, "Failed to move group to trash")
				return
			}
			logger.Info("Group moved to trash with contents", logger.Fields{"id": ws.ID, "members": len(descendants)})
			orihttp.WriteJSON(w, map[string]any{"success": true, "id": ws.ID, "trashed": true})
			return
		}
	}

	// Remove every member (sessions, entry agent, SQLite row). The group's own
	// folder removal below also clears the on-disk tree in one shot.
	for _, memberID := range descendants {
		entryAgent := ""
		if member, mErr := h.store.GetWorkspace(ctx, memberID); mErr == nil && !member.IsGroup() && h.workspaceStore != nil {
			if fws, ferr := h.workspaceStore.Get(memberID); ferr == nil && fws != nil {
				entryAgent = strings.TrimSpace(fws.EntryAgentName())
			}
		}
		if deleteSessions {
			_ = h.store.DeleteSessionsByWorkspace(ctx, memberID)
		} else {
			_ = h.store.UnlinkSessionsFromWorkspace(ctx, memberID)
		}
		if err := h.store.DeleteWorkspace(ctx, memberID); err != nil {
			logger.Warn("Failed to delete group member", logger.Fields{"id": memberID, "error": err})
		}
		h.cleanupEntryAgent(entryAgent, memberID)
	}

	// Handle the group's own sessions and entry agent, then its SQLite row.
	groupEntryAgent := ""
	if h.workspaceStore != nil {
		if fws, ferr := h.workspaceStore.Get(ws.ID); ferr == nil && fws != nil {
			groupEntryAgent = strings.TrimSpace(fws.EntryAgentName())
		}
	}
	if deleteSessions {
		_ = h.store.DeleteSessionsByWorkspace(ctx, ws.ID)
	} else {
		_ = h.store.UnlinkSessionsFromWorkspace(ctx, ws.ID)
	}
	if err := h.store.DeleteWorkspace(ctx, ws.ID); err != nil {
		logger.Error("Failed to delete group", logger.Fields{"id": ws.ID, "error": err})
		_ = orihttp.RespondInternalError(w, "Failed to delete group")
		return
	}

	// Remove the whole folder tree from disk + cache in one call.
	if h.workspaceStore != nil {
		if err := h.workspaceStore.Delete(ws.ID); err != nil {
			logger.Warn("Failed to delete group folder tree", logger.Fields{"id": ws.ID, "error": err})
		}
	}
	h.cleanupEntryAgent(groupEntryAgent, ws.ID)

	logger.Info("Group deleted with contents", logger.Fields{"id": ws.ID, "members": len(descendants)})
	orihttp.RespondNoContent(w)
}

// deleteGroupOnly moves the group's direct children out to the workspaces
// root, then soft-deletes the now member-less group like a regular workspace
// (system trash + trashed row) so its own sessions, notes, and files stay
// restorable. Explicit delete_sessions=true requests and platforms without
// trash support remove it permanently instead. Hard-blocked if any workspace
// in the group has active work.
func (h *Handler) deleteGroupOnly(w http.ResponseWriter, ctx context.Context, ws *session.Workspace, deleteSessions bool) {
	// Active-work hard block across the whole subtree (req 25): un-nesting moves
	// folders, which must not happen while work is in flight.
	if blocker, err := h.firstActiveWorkBlocker(ctx, ws.ID); err != nil {
		logger.Error("Failed to check group active work", logger.Fields{"id": ws.ID, "error": err})
		_ = orihttp.RespondInternalError(w, "Failed to delete group")
		return
	} else if blocker != "" {
		_ = orihttp.RespondConflict(w, fmt.Sprintf("Stop the running task in %q before deleting this group.", blocker))
		return
	}

	// Move each direct child out to the root before removing the group.
	for _, childID := range h.directChildIDs(ctx, ws.ID) {
		if h.workspaceStore != nil {
			moved, err := h.workspaceStore.MoveWorkspaceFolder(childID, "")
			if err != nil {
				logger.Error("Failed to un-nest group member", logger.Fields{"id": childID, "error": err})
				handleWorkspaceMoveError(w, err)
				return
			}
			child, cErr := h.store.GetWorkspace(ctx, childID)
			if cErr == nil {
				child.ParentID = ""
				h.applyMoveReferenceUpdates(ctx, child, moved)
				if err := h.store.UpdateWorkspace(ctx, child); err != nil {
					logger.Warn("Failed to persist un-nested member", logger.Fields{"id": childID, "error": err})
				}
			}
		} else if child, cErr := h.store.GetWorkspace(ctx, childID); cErr == nil {
			child.ParentID = ""
			if err := h.store.UpdateWorkspace(ctx, child); err != nil {
				logger.Warn("Failed to persist un-nested member", logger.Fields{"id": childID, "error": err})
			}
		}
	}

	// Soft delete (default): with members un-nested, the group folder now holds
	// only group-owned content (sessions, notes, files), so trash it like a
	// regular workspace and mark the row trashed — the deletion stays undoable.
	// Explicit delete_sessions=true requests and platforms without trash
	// support fall through to the permanent delete.
	if !deleteSessions && h.workspaceStore != nil && platform.TrashSupported() {
		if _, ferr := h.workspaceStore.Get(ws.ID); ferr == nil {
			if err := h.trashWorkspace(ctx, ws); err != nil {
				logger.Error("Failed to move group to trash", logger.Fields{"id": ws.ID, "error": err})
				_ = orihttp.RespondInternalError(w, "Failed to move group to trash")
				return
			}
			logger.Info("Group moved to trash (members un-nested to root)", logger.Fields{"id": ws.ID})
			orihttp.WriteJSON(w, map[string]any{"success": true, "id": ws.ID, "trashed": true})
			return
		}
	}

	// Permanent removal: delete or unlink the group's own sessions, clean up
	// its entry agent, then drop the SQLite row + folder.
	groupEntryAgent := ""
	if h.workspaceStore != nil {
		if fws, ferr := h.workspaceStore.Get(ws.ID); ferr == nil && fws != nil {
			groupEntryAgent = strings.TrimSpace(fws.EntryAgentName())
		}
	}
	if deleteSessions {
		_ = h.store.DeleteSessionsByWorkspace(ctx, ws.ID)
	} else {
		_ = h.store.UnlinkSessionsFromWorkspace(ctx, ws.ID)
	}
	if err := h.store.DeleteWorkspace(ctx, ws.ID); err != nil {
		logger.Error("Failed to delete group", logger.Fields{"id": ws.ID, "error": err})
		_ = orihttp.RespondInternalError(w, "Failed to delete group")
		return
	}
	if h.workspaceStore != nil {
		if err := h.workspaceStore.Delete(ws.ID); err != nil {
			logger.Warn("Failed to delete group folder", logger.Fields{"id": ws.ID, "error": err})
		}
	}
	h.cleanupEntryAgent(groupEntryAgent, ws.ID)

	logger.Info("Group deleted (members un-nested to root)", logger.Fields{"id": ws.ID})
	orihttp.RespondNoContent(w)
}

// directChildIDs returns the IDs of workspaces whose immediate parent is
// parentID (not deeper descendants).
func (h *Handler) directChildIDs(ctx context.Context, parentID string) []string {
	ids, err := h.store.GetSubworkspaceIDs(ctx, parentID)
	if err != nil {
		logger.Warn("Failed to load group children", logger.Fields{"id": parentID, "error": err})
		return nil
	}
	direct := make([]string, 0, len(ids))
	for _, cid := range ids {
		if cw, err := h.store.GetWorkspace(ctx, cid); err == nil && cw.ParentID == parentID {
			direct = append(direct, cid)
		}
	}
	return direct
}

// cleanupEntryAgent deletes a workspace's entry agent from the global agent
// store, if present. Non-fatal.
func (h *Handler) cleanupEntryAgent(name, workspaceID string) {
	name = strings.TrimSpace(name)
	if name == "" || h.agentStore == nil {
		return
	}
	if _, exists := h.agentStore.GetAgent(name); exists {
		if err := h.agentStore.DeleteAgent(name); err != nil {
			logger.Warn("Failed to delete workspace entry agent", logger.Fields{"workspace_id": workspaceID, "agent": name, "error": err})
		}
	}
}

// trashWorkspace moves a workspace's folder to the system trash and marks the
// SQLite record trashed, stashing the paths needed to restore it. The record and
// its sessions are preserved so a restore is high fidelity.
func (h *Handler) trashWorkspace(ctx context.Context, ws *session.Workspace) error {
	originalPath, trashedPath, err := h.workspaceStore.Trash(ws.ID)
	if err != nil {
		return err
	}

	if ws.SharedData == nil {
		ws.SharedData = map[string]any{}
	}
	ws.SharedData[workspaceTrashSharedDataKey] = map[string]any{
		"original_path": originalPath,
		"trashed_path":  trashedPath,
		"deleted_at":    time.Now().UTC().Format(time.RFC3339),
	}
	ws.Status = session.WorkspaceStatusTrashed

	if err := h.store.UpdateWorkspace(ctx, ws); err != nil {
		// Roll the folder back out of the trash so the workspace isn't stranded.
		if trashedPath != "" {
			if _, rerr := h.workspaceStore.RestoreFromTrash(originalPath, trashedPath); rerr != nil {
				logger.Error("Failed to roll back trash after update error", logger.Fields{"id": ws.ID, "error": rerr})
			}
		}
		return err
	}
	return nil
}

// trashGroupWithContents moves a group's entire folder tree to the system trash
// in a single operation and marks the group and every descendant workspace
// trashed (rows and sessions preserved) so the whole group can be restored from
// Undo. Trash metadata is stashed only on the group: its folder tree physically
// contains the members, so restoring the group brings them back with it.
func (h *Handler) trashGroupWithContents(ctx context.Context, ws *session.Workspace, descendants []string) error {
	originalPath, trashedPath, err := h.workspaceStore.Trash(ws.ID)
	if err != nil {
		return err
	}

	if ws.SharedData == nil {
		ws.SharedData = map[string]any{}
	}
	ws.SharedData[workspaceTrashSharedDataKey] = map[string]any{
		"original_path": originalPath,
		"trashed_path":  trashedPath,
		"deleted_at":    time.Now().UTC().Format(time.RFC3339),
	}
	ws.Status = session.WorkspaceStatusTrashed

	if err := h.store.UpdateWorkspace(ctx, ws); err != nil {
		// Roll the whole tree back out of the trash so the group isn't stranded.
		if trashedPath != "" {
			if _, rerr := h.workspaceStore.RestoreFromTrash(originalPath, trashedPath); rerr != nil {
				// Both the update and its rollback failed: the folder tree is in
				// the Trash but the DB record isn't marked trashed. Surface both so
				// the caller knows the on-disk and DB states have diverged.
				logger.Error("Failed to roll back group trash after update error", logger.Fields{"id": ws.ID, "error": rerr})
				return fmt.Errorf("update failed: %w; rollback also failed: %v", err, rerr)
			}
		}
		return err
	}

	// Mark every descendant trashed too. Their folders moved with the group, so
	// they only need a status flip; the group's restore metadata covers bringing
	// the whole tree back. Failures here are non-fatal — a trashed group is
	// pruned from the launcher wholesale, hiding its subtree regardless.
	for _, memberID := range descendants {
		member, mErr := h.store.GetWorkspace(ctx, memberID)
		if mErr != nil {
			logger.Warn("Failed to load group member for trashing", logger.Fields{"id": memberID, "error": mErr})
			continue
		}
		member.Status = session.WorkspaceStatusTrashed
		if err := h.store.UpdateWorkspace(ctx, member); err != nil {
			logger.Warn("Failed to mark group member trashed", logger.Fields{"id": memberID, "error": err})
		}
	}
	return nil
}

// restoreWorkspace handles POST /api/workspaces/{id}/restore. It moves a trashed
// workspace's folder back out of the system trash and reactivates the record.
func (h *Handler) restoreWorkspace(w http.ResponseWriter, r *http.Request, id string) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	ctx := r.Context()

	ws, err := h.store.GetWorkspace(ctx, id)
	if err == session.ErrWorkspaceNotFound {
		_ = orihttp.RespondNotFound(w, "Workspace not found")
		return
	}
	if err != nil {
		logger.Error("Failed to get workspace", logger.Fields{"id": id, "error": err})
		_ = orihttp.RespondInternalError(w, "Failed to restore workspace")
		return
	}

	if ws.Status != session.WorkspaceStatusTrashed {
		_ = orihttp.RespondBadRequest(w, "Workspace is not in the trash")
		return
	}
	if h.workspaceStore == nil {
		_ = orihttp.RespondInternalError(w, "Workspace folder store unavailable")
		return
	}

	originalPath, trashedPath := workspaceTrashPaths(ws)
	if originalPath == "" {
		_ = orihttp.RespondBadRequest(w, "Workspace is missing trash metadata; cannot restore")
		return
	}

	if _, err := h.workspaceStore.RestoreFromTrash(originalPath, trashedPath); err != nil {
		logger.Error("Failed to restore workspace from trash", logger.Fields{"id": id, "error": err})
		_ = orihttp.RespondBadRequest(w, "Failed to restore workspace: "+err.Error())
		return
	}

	ws.Status = session.WorkspaceStatusActive
	delete(ws.SharedData, workspaceTrashSharedDataKey)
	if err := h.store.UpdateWorkspace(ctx, ws); err != nil {
		logger.Error("Failed to reactivate restored workspace", logger.Fields{"id": id, "error": err})
		_ = orihttp.RespondInternalError(w, "Failed to restore workspace")
		return
	}

	// Restoring a group's folder tree brings its nested members' folders back
	// with it, so flip every descendant row from trashed to active to make the
	// whole group reappear. Member rows kept their parent_id while trashed, so
	// the subtree query still resolves them.
	if ws.Kind == session.WorkspaceKindGroup {
		descendants, derr := h.store.GetSubworkspaceIDs(ctx, ws.ID)
		if derr != nil {
			logger.Warn("Failed to load group descendants for restore", logger.Fields{"id": id, "error": derr})
		}
		for _, memberID := range descendants {
			member, mErr := h.store.GetWorkspace(ctx, memberID)
			if mErr != nil || member.Status != session.WorkspaceStatusTrashed {
				continue
			}
			member.Status = session.WorkspaceStatusActive
			delete(member.SharedData, workspaceTrashSharedDataKey)
			if err := h.store.UpdateWorkspace(ctx, member); err != nil {
				logger.Warn("Failed to reactivate restored group member", logger.Fields{"id": memberID, "error": err})
			}
		}
	}

	logger.Info("Workspace restored from trash", logger.Fields{"id": id})
	orihttp.WriteJSON(w, map[string]any{"success": true, "id": id})
}

// workspaceTrashPaths extracts the original and trashed folder locations stashed
// in a trashed workspace's SharedData.
func workspaceTrashPaths(ws *session.Workspace) (originalPath, trashedPath string) {
	if ws == nil || ws.SharedData == nil {
		return "", ""
	}
	raw, ok := ws.SharedData[workspaceTrashSharedDataKey].(map[string]any)
	if !ok {
		return "", ""
	}
	originalPath, _ = raw["original_path"].(string)
	trashedPath, _ = raw["trashed_path"].(string)
	return originalPath, trashedPath
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
		workspaces = pruneHiddenWorkspaces(workspaces)
		workspaces = h.hydrateWorkspaceListFromFileStore(workspaces)

		orihttp.WriteJSON(w, map[string]any{
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

	workspaces = pruneHiddenWorkspaces(workspaces)
	workspaces = h.hydrateWorkspaceListFromFileStore(workspaces)

	orihttp.WriteJSON(w, map[string]any{
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
	if h == nil || h.workspaceStore == nil || workspace == nil {
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
	// project_path has no SQLite column: workspace.json is its canonical
	// store, so reads always hydrate it from disk.
	if strings.TrimSpace(workspace.ProjectPath) == "" {
		workspace.ProjectPath = fallback.ProjectPath
	}
	// len()==0 rather than nil: SQLite deserializes the '[]' column default to
	// an empty non-nil slice, which must not shadow tags that live only in
	// workspace.json (e.g. a workspace imported from another machine).
	if len(workspace.Tags) == 0 {
		workspace.Tags = append([]string(nil), fallback.Tags...)
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
		workspace.SharedData = make(map[string]any)
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

	// Rename the backing folder when this workspace is tracked by the folder
	// store. This now includes groups (a group is a folder that may physically
	// contain members); RenameWithSlug rewrites nested members' paths. DB-only
	// workspaces have no folder to rename and are skipped.
	folderTracked := false
	if h.workspaceStore != nil {
		if existing, getErr := h.workspaceStore.Get(id); getErr == nil && existing != nil {
			folderTracked = true
		}
	}
	if folderTracked {
		moved, err := h.workspaceStore.RenameWithSlug(id, req.Name, targetSlug)
		if err != nil {
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

		// The folder (and any nested members) changed paths: rewrite
		// path-keyed references (directory references, MCP roots,
		// project_path) and persist them.
		if len(moved) > 0 {
			h.applyMoveReferenceUpdates(ctx, ws, moved)
			if err := h.store.UpdateWorkspace(ctx, ws); err != nil {
				logger.Warn("Failed to persist renamed workspace references", logger.Fields{"id": id, "error": err})
			}
			if err := h.syncWorkspacePortableStateToFileStore(ws); err != nil {
				logger.Warn("Failed to sync workspace.json after rename", logger.Fields{"id": id, "error": err})
			}
		}
	}

	logger.Info("Workspace renamed", logger.Fields{"id": id, "new_name": req.Name})

	orihttp.WriteJSON(w, map[string]any{
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

	orihttp.WriteJSON(w, map[string]any{
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
	if err := h.requireGroupParent(r.Context(), req.ParentID); err != nil {
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

		response := map[string]any{
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
		FolderSlug:  agentworkspace.Slugify(filepath.Base(normalizedPath)),
	}
	if req.OrderIndex != nil {
		workspace.OrderIndex = *req.OrderIndex
	}
	workspace.SharedData = map[string]any{
		"folder_import": map[string]any{
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

	mcpBinding := newWorkspaceFilesMCPBinding([]string{normalizedPath}, time.Now())
	if bindingData, err := json.Marshal([]agentworkspace.WorkspaceMCPBinding{mcpBinding}); err == nil {
		workspace.MCPBindingsJSON = bindingData
	} else {
		logger.Error("Failed to marshal MCP binding for workspace import", logger.Fields{"workspace_id": workspace.ID, "error": err})
		_ = h.store.DeleteWorkspace(r.Context(), workspace.ID)
		recordWorkspaceImportTelemetry("import_failed", logger.Fields{
			"path_hash":    hashPathForTelemetry(normalizedPath),
			"workspace_id": workspace.ID,
			"entry_point":  req.EntryPoint,
			"reason":       "mcp_binding_marshal_failed",
		})
		_ = orihttp.RespondInternalError(w, "Failed to scaffold imported folder")
		return
	}

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

	if h.workspaceStore != nil {
		folderWS, err := buildFileStoreWorkspace(workspace)
		if err != nil {
			logger.Error("Failed to build workspace file metadata for import", logger.Fields{"workspace_id": workspace.ID, "error": err})
			if delErr := h.store.DeleteWorkspace(r.Context(), workspace.ID); delErr != nil {
				logger.Warn("Failed to rollback workspace after import metadata failure", logger.Fields{"workspace_id": workspace.ID, "error": delErr})
			}
			recordWorkspaceImportTelemetry("import_failed", logger.Fields{
				"path_hash":    hashPathForTelemetry(normalizedPath),
				"workspace_id": workspace.ID,
				"entry_point":  req.EntryPoint,
				"reason":       "workspace_file_metadata_failed",
			})
			_ = orihttp.RespondInternalError(w, "Failed to scaffold imported folder")
			return
		}
		if err := h.workspaceStore.RebindExistingFolder(folderWS, normalizedPath); err != nil {
			logger.Error("Failed to scaffold imported folder as workspace", logger.Fields{"workspace_id": workspace.ID, "error": err})
			if delErr := h.store.DeleteWorkspace(r.Context(), workspace.ID); delErr != nil {
				logger.Warn("Failed to rollback workspace after import scaffold failure", logger.Fields{"workspace_id": workspace.ID, "error": delErr})
			}
			recordWorkspaceImportTelemetry("import_failed", logger.Fields{
				"path_hash":    hashPathForTelemetry(normalizedPath),
				"workspace_id": workspace.ID,
				"entry_point":  req.EntryPoint,
				"reason":       "workspace_folder_rebind_failed",
			})
			_ = orihttp.RespondInternalError(w, "Failed to scaffold imported folder")
			return
		}
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

	_ = orihttp.RespondCreated(w, map[string]any{
		"success": true,
		"folder":  workspace,
		"directory": map[string]any{
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

	rootWorkspace := importTree[0].Workspace
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
			rootWorkspace.SharedData = make(map[string]any)
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

	// Restore any workspace-local agent snapshots into the global agent store
	// so the imported workspace's entry agent (and any other referenced agents)
	// resolve cleanly even if the importing instance had never seen them before.
	if h.agentStore != nil {
		for _, importItem := range importTree {
			item := importItem.Workspace
			if registered, restoreErr := agentworkspace.RestoreWorkspaceAgents(h.workspaceStore, item, h.agentStore); restoreErr != nil {
				logger.Warn("Restore workspace agents during import failed", logger.Fields{
					"workspace_id": item.ID,
					"error":        restoreErr.Error(),
				})
			} else if len(registered) > 0 {
				logger.Info("Imported workspace registered agents into global store", logger.Fields{
					"workspace_id": item.ID,
					"agents":       registered,
				})
			}
		}
	}

	// Record each imported workspace in the per-data-dir allowlist so its agent
	// snapshots will be re-hydrated on subsequent server starts. Without this,
	// the workspaces would appear once on import and vanish from /agents after
	// the next restart.
	if h.workspaceAllowlist != nil {
		for _, importItem := range importTree {
			item := importItem.Workspace
			if err := h.workspaceAllowlist.Add(item.ID); err != nil {
				logger.Warn("Failed to add workspace to allowlist", logger.Fields{
					"workspace_id": item.ID,
					"error":        err.Error(),
				})
			}
		}
	}

	adapter := session.NewWorkspaceStoreAdapter(h.store)
	for _, importItem := range importTree {
		item := importItem.Workspace
		if item.Status == "" {
			item.Status = agentworkspace.StatusActive
		}

		localFolderPath, err := h.workspaceStore.GetFolderPath(item.ID)
		if err != nil {
			return nil, warning, fmt.Errorf("locate imported workspace %s: %w", item.ID, err)
		}
		rebaseImportedWorkspaceFolderReferences(item, importItem.SourcePath, localFolderPath)

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

		if importedNotes, err := h.importWorkspaceNoteFiles(ctx, item.ID, localFolderPath); err != nil {
			return nil, warning, fmt.Errorf("import notes for workspace %s: %w", item.ID, err)
		} else if importedNotes > 0 {
			logger.Info("Imported workspace note files", logger.Fields{
				"workspace_id": item.ID,
				"count":        importedNotes,
			})
		}
	}

	rootSessionWorkspace, err := h.store.GetWorkspace(ctx, rootWorkspace.ID)
	if err != nil {
		return nil, warning, fmt.Errorf("load restored root workspace %s: %w", rootWorkspace.ID, err)
	}
	return rootSessionWorkspace, warning, nil
}

type workspaceImportItem struct {
	Workspace  *agentworkspace.Workspace
	SourcePath string
}

func loadWorkspaceImportTree(folderPath string, parentID string) ([]workspaceImportItem, error) {
	result := make([]workspaceImportItem, 0, 1)
	if err := appendWorkspaceImportTree(&result, folderPath, parentID); err != nil {
		return nil, err
	}
	return result, nil
}

func appendWorkspaceImportTree(result *[]workspaceImportItem, folderPath string, parentID string) error {
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
	*result = append(*result, workspaceImportItem{
		Workspace:  ws,
		SourcePath: folderPath,
	})

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

func rebaseImportedWorkspaceFolderReferences(ws *agentworkspace.Workspace, oldPath string, newPath string) {
	if ws == nil {
		return
	}

	normalizedNew := cleanWorkspaceSyncPath(newPath)
	if normalizedNew == "" {
		return
	}

	now := time.Now()
	normalizedOld := cleanWorkspaceSyncPath(oldPath)
	folderSlug := strings.TrimSpace(ws.FolderSlug)
	newBaseName := filepath.Base(normalizedNew)
	isGroup := session.NormalizeWorkspaceKind(ws.Kind) == session.WorkspaceKindGroup
	defaultRefPath := defaultWorkspaceReferencePath(isGroup, normalizedNew)
	defaultRoots := defaultWorkspaceMCPRoots(isGroup, normalizedNew)
	matchedReference := false

	for i := range ws.DirectoryReferences {
		refPath := cleanWorkspaceSyncPath(ws.DirectoryReferences[i].Path)
		if refPath == "" {
			continue
		}
		if rewritten, ok := rewriteWorkspaceContentPath(refPath, normalizedOld, normalizedNew); ok {
			ws.DirectoryReferences[i].WorkspaceID = ws.ID
			if strings.TrimSpace(ws.DirectoryReferences[i].Name) == "" {
				ws.DirectoryReferences[i].Name = workspaceReferenceName(ws, normalizedNew)
			}
			ws.DirectoryReferences[i].Path = rewritten
			ws.DirectoryReferences[i].UpdatedAt = now
			matchedReference = true
			continue
		}
		if _, ok := rewriteWorkspaceContentPath(refPath, normalizedNew, normalizedNew); ok {
			matchedReference = true
			continue
		}
		if (folderSlug != "" && strings.EqualFold(strings.TrimSpace(ws.DirectoryReferences[i].Name), folderSlug)) ||
			(newBaseName != "" && strings.EqualFold(strings.TrimSpace(ws.DirectoryReferences[i].Name), newBaseName)) {
			ws.DirectoryReferences[i].WorkspaceID = ws.ID
			if strings.TrimSpace(ws.DirectoryReferences[i].Name) == "" {
				ws.DirectoryReferences[i].Name = workspaceReferenceName(ws, normalizedNew)
			}
			ws.DirectoryReferences[i].Path = defaultRefPath
			ws.DirectoryReferences[i].UpdatedAt = now
			matchedReference = true
		}
	}

	if !matchedReference {
		ws.DirectoryReferences = append(ws.DirectoryReferences, agentworkspace.DirectoryReference{
			ID:          uuid.New().String(),
			WorkspaceID: ws.ID,
			Name:        workspaceReferenceName(ws, normalizedNew),
			Path:        defaultRefPath,
			X:           400,
			Y:           300,
			CreatedAt:   now,
			UpdatedAt:   now,
		})
	}
	ws.DirectoryReferences = compactAgentWorkspaceDirectoryReferences(ws.DirectoryReferences)

	matchedBinding := false
	for i := range ws.MCPBindings {
		if strings.EqualFold(strings.TrimSpace(ws.MCPBindings[i].Alias), workspaceFilesMCPAlias) ||
			workspaceBindingHasRoot(ws.MCPBindings[i].Config, normalizedOld) {
			if ws.MCPBindings[i].Config == nil {
				ws.MCPBindings[i].Config = make(map[string]any)
			}
			ws.MCPBindings[i].Config["roots"] = rewriteWorkspaceBindingRoots(ws.MCPBindings[i].Config["roots"], normalizedOld, normalizedNew, defaultRoots)
			ws.MCPBindings[i].UpdatedAt = now
			ws.MCPBindings[i].Enabled = true
			matchedBinding = true
		}
	}

	if !matchedBinding {
		ws.MCPBindings = append(ws.MCPBindings, newWorkspaceFilesMCPBinding(defaultRoots, now))
	}
}

func workspaceReferenceName(ws *agentworkspace.Workspace, path string) string {
	if ws != nil {
		if name := strings.TrimSpace(ws.FolderSlug); name != "" {
			return name
		}
		if name := strings.TrimSpace(ws.Name); name != "" {
			return name
		}
	}
	return filepath.Base(path)
}

func compactAgentWorkspaceDirectoryReferences(refs []agentworkspace.DirectoryReference) []agentworkspace.DirectoryReference {
	if len(refs) < 2 {
		return refs
	}

	seen := make(map[string]int, len(refs))
	compact := make([]agentworkspace.DirectoryReference, 0, len(refs))
	for _, ref := range refs {
		key := cleanWorkspaceSyncPath(ref.Path)
		if key == "" {
			compact = append(compact, ref)
			continue
		}
		if existingIndex, ok := seen[key]; ok {
			if strings.TrimSpace(compact[existingIndex].Name) == "" && strings.TrimSpace(ref.Name) != "" {
				compact[existingIndex].Name = ref.Name
			}
			continue
		}
		seen[key] = len(compact)
		compact = append(compact, ref)
	}
	return compact
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

	orihttp.WriteJSON(w, map[string]any{
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
	if err := json.NewEncoder(w).Encode(map[string]any{
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
	if err := json.NewEncoder(w).Encode(map[string]any{
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

	_ = orihttp.RespondCreated(w, map[string]any{
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

	orihttp.WriteJSON(w, map[string]any{
		"success":   true,
		"workspace": workspace,
	})
}

func (h *Handler) buildWorkspaceDetailResponse(workspace *session.Workspace) map[string]any {
	if workspace == nil {
		return map[string]any{}
	}

	payload := make(map[string]any)
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

	orihttp.WriteJSON(w, map[string]any{
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

	orihttp.WriteJSON(w, map[string]any{
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

	// Store → Disk: in SQLite but not on disk. Rows already marked missing are
	// always listed — their folder may have been recreated as a different
	// workspace, in which case the path exists but no longer belongs to them.
	for id, ws := range sqliteIDs {
		isMissing := ws.Status == session.WorkspaceStatusMissing
		resolved := ws
		if isMissing {
			// The flat listing omits the JSON columns that hold directory
			// references; hydrate the full row so the last-known path resolves.
			if full, err := h.store.GetWorkspace(ctx, id); err == nil && full != nil {
				resolved = *full
			}
		}

		path, managed := h.syncManagedWorkspacePath(resolved)
		if managed {
			existsOnDisk, err := workspaceFolderExists(path)
			if err != nil {
				continue
			}
			if existsOnDisk && !isMissing {
				continue
			}
		} else if !isMissing {
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

			// Locating a folder recovers a workspace hidden as missing.
			if sessionWS.Status == session.WorkspaceStatusMissing {
				sessionWS.Status = session.WorkspaceStatusActive
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
			if isFolderImportedWorkspace(*sessionWS) {
				warnings = append(warnings, fmt.Sprintf("Workspace %s is a folder import and cannot be recreated", sessionWS.Name))
				continue
			}

			targetPath, managed := h.syncManagedWorkspacePath(*sessionWS)
			if !managed || strings.TrimSpace(targetPath) == "" {
				warnings = append(warnings, fmt.Sprintf("Workspace %s does not have a recoverable folder path", sessionWS.Name))
				continue
			}

			// Refuse to overwrite a folder that was recreated externally as a
			// different workspace; cleanup is the right action for this row.
			if diskID := h.workspaceIDOnDisk(targetPath); diskID != "" && diskID != sessionWS.ID {
				warnings = append(warnings, fmt.Sprintf("Folder for %s now belongs to a different workspace; remove this entry instead", sessionWS.Name))
				continue
			}

			if err := updateManagedWorkspaceReferences(sessionWS, targetPath, targetPath); err != nil {
				logger.Warn("Sync recreate: failed to update workspace folder references", logger.Fields{"id": id, "error": err})
				warnings = append(warnings, fmt.Sprintf("Failed to update workspace references for %s", sessionWS.Name))
				continue
			}

			// Recreating the folder recovers a workspace hidden as missing.
			if sessionWS.Status == session.WorkspaceStatusMissing {
				sessionWS.Status = session.WorkspaceStatusActive
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
			if folderPath, err := h.workspaceStore.GetFolderPath(id); err == nil {
				if _, err := h.importWorkspaceNoteFiles(ctx, id, folderPath); err != nil {
					logger.Warn("Sync import: failed to import note files", logger.Fields{"id": id, "error": err})
					warnings = append(warnings, fmt.Sprintf("Imported %s but failed to import note files", diskWS.Name))
				}
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

// handleWorkspaceRescan re-reads the workspace folder tree from disk and
// reconciles the session store's structure (existence, kind, derived parent,
// order) to match — disk is the source of truth for grouping. It imports
// workspaces newly present on disk, re-parents existing ones whose folder
// moved (e.g. via git pull or a cloud-sync client), marks folder-managed
// workspaces whose folder disappeared as missing (hidden from listings), and
// restores previously-missing ones whose folder reappeared. It never deletes
// session-only data such as chat history; missing workspaces remain available
// through the sync-status / cleanup flow.
func (h *Handler) handleWorkspaceRescan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		_ = orihttp.RespondMethodNotAllowed(w)
		return
	}
	if h.workspaceStore == nil {
		_ = orihttp.RespondBadRequest(w, "workspace folder store is unavailable")
		return
	}

	// Background rescans (fired on every hub page load) honor a cooldown so
	// several tabs opening at once don't each trigger a full filesystem walk.
	// Explicit user-initiated rescans always run.
	if r.URL.Query().Get("background") == "1" {
		h.rescanMu.Lock()
		recent := time.Since(h.lastRescanAt) < workspaceRescanCooldown
		h.rescanMu.Unlock()
		if recent {
			orihttp.WriteJSON(w, map[string]any{
				"success":    true,
				"skipped":    true,
				"imported":   0,
				"reparented": 0,
				"orphaned":   0,
				"restored":   0,
				"warnings":   []string{},
			})
			return
		}
	}

	stats, warnings, err := h.reconcileWorkspacesFromDisk(r.Context())
	if err != nil {
		logger.Error("Rescan: failed to reconcile workspaces from disk", logger.Fields{"error": err})
		_ = orihttp.RespondInternalError(w, "Failed to rescan workspaces")
		return
	}

	logger.Info("Workspaces rescanned from disk", logger.Fields{
		"imported":   stats.Imported,
		"reparented": stats.Reparented,
		"orphaned":   stats.Orphaned,
		"restored":   stats.Restored,
	})
	orihttp.WriteJSON(w, map[string]any{
		"success":    true,
		"imported":   stats.Imported,
		"reparented": stats.Reparented,
		"orphaned":   stats.Orphaned,
		"restored":   stats.Restored,
		"warnings":   warnings,
	})
}

// workspaceReconcileStats summarizes the outcome of a disk reconcile pass.
type workspaceReconcileStats struct {
	// Imported counts disk workspaces newly created in the session store.
	Imported int
	// Reparented counts session workspaces whose parent changed to match disk.
	Reparented int
	// Orphaned counts session workspaces marked missing because their folder
	// is gone from disk (or was recreated as a different workspace).
	Orphaned int
	// Restored counts previously-missing workspaces whose folder reappeared.
	Restored int
}

// workspaceRescanCooldown is the minimum interval between background-initiated
// disk reconciles (page loads); explicit rescans are exempt.
const workspaceRescanCooldown = 30 * time.Second

// reconcileWorkspacesFromDisk reloads the folder store from disk and updates the
// session store so its structure matches the on-disk layout. Returns reconcile
// stats plus any non-fatal warnings. Safe to call on startup and on demand;
// concurrent calls are serialized.
func (h *Handler) reconcileWorkspacesFromDisk(ctx context.Context) (stats workspaceReconcileStats, warnings []string, err error) {
	if h.workspaceStore == nil {
		return stats, nil, nil
	}

	h.rescanMu.Lock()
	defer func() {
		if err == nil {
			h.lastRescanAt = time.Now()
		}
		h.rescanMu.Unlock()
	}()

	// Refresh the file-store cache + index from disk; physical layout wins.
	if err := h.workspaceStore.Reload(); err != nil {
		return stats, nil, err
	}

	warnings = make([]string, 0)
	diskWorkspaces := h.workspaceStore.CachedWorkspaces()
	for id, diskWS := range diskWorkspaces {
		sessionWS, getErr := h.store.GetWorkspace(ctx, id)
		if getErr == session.ErrWorkspaceNotFound {
			converted := session.ConvertAgentWorkspace(diskWS)
			if converted == nil {
				warnings = append(warnings, fmt.Sprintf("Failed to convert %s", diskWS.Name))
				continue
			}
			if createErr := h.store.CreateWorkspace(ctx, converted); createErr != nil {
				warnings = append(warnings, fmt.Sprintf("Failed to import %s", diskWS.Name))
				continue
			}
			stats.Imported++
			continue
		}
		if getErr != nil {
			warnings = append(warnings, fmt.Sprintf("Failed to load %s", id))
			continue
		}

		// Disk wins for structure: reconcile parent_id / kind / order_index.
		parentChanged := sessionWS.ParentID != diskWS.ParentID
		changed := parentChanged
		sessionWS.ParentID = diskWS.ParentID
		if diskKind := session.NormalizeWorkspaceKind(diskWS.Kind); sessionWS.Kind != diskKind {
			sessionWS.Kind = diskKind
			changed = true
		}
		if sessionWS.OrderIndex != diskWS.OrderIndex {
			sessionWS.OrderIndex = diskWS.OrderIndex
			changed = true
		}
		// Disk reappearance heals a workspace previously marked missing.
		if sessionWS.Status == session.WorkspaceStatusMissing {
			sessionWS.Status = session.WorkspaceStatusActive
			changed = true
			stats.Restored++
		}
		if changed {
			if updateErr := h.store.UpdateWorkspace(ctx, sessionWS); updateErr != nil {
				warnings = append(warnings, fmt.Sprintf("Failed to update %s", sessionWS.Name))
				continue
			}
			if parentChanged {
				stats.Reparented++
			}
		}
	}

	// Disk is the source of truth for existence too: folder-managed session
	// workspaces whose folder is no longer on disk are marked missing so they
	// drop out of listings. Chat history is preserved on the hidden row and the
	// sync-status / cleanup flow can recover or remove it.
	orphaned, sweepWarnings := h.sweepMissingWorkspaces(ctx, diskWorkspaces)
	stats.Orphaned = orphaned
	warnings = append(warnings, sweepWarnings...)

	return stats, warnings, nil
}

// sweepMissingWorkspaces marks folder-managed session workspaces as missing when
// their backing folder is gone from disk or has been recreated as a different
// workspace (same path, different ID). Returns the number of workspaces marked.
func (h *Handler) sweepMissingWorkspaces(ctx context.Context, diskWorkspaces map[string]*agentworkspace.Workspace) (int, []string) {
	sessionWorkspaces, listErr := h.store.ListWorkspaces(ctx)
	if listErr != nil {
		return 0, []string{"Failed to list workspaces for missing-folder sweep"}
	}

	orphaned := 0
	warnings := make([]string, 0)
	for _, listed := range sessionWorkspaces {
		if _, onDisk := diskWorkspaces[listed.ID]; onDisk {
			continue
		}
		// Trashed rows are owned by the trash/undo flow; missing rows are
		// already hidden.
		if listed.Status == session.WorkspaceStatusTrashed || listed.Status == session.WorkspaceStatusMissing {
			continue
		}

		// The flat listing omits JSON columns; load the full row so the
		// managed-path resolution can inspect directory references.
		sessionWS, getErr := h.store.GetWorkspace(ctx, listed.ID)
		if getErr != nil {
			warnings = append(warnings, fmt.Sprintf("Failed to load %s", listed.ID))
			continue
		}

		path, managed := h.syncManagedWorkspacePath(*sessionWS)
		if !managed {
			continue // legacy DB-only workspace; nothing on disk to compare
		}

		exists, statErr := workspaceFolderExists(path)
		if statErr != nil {
			continue // unreadable path: leave the workspace alone
		}
		if exists {
			// The folder is still there but this ID is not in the disk cache:
			// either the folder now belongs to a different workspace (deleted
			// and recreated externally — mark the stale row missing), or it
			// lives outside the workspaces root (located/imported — leave it).
			diskID := h.workspaceIDOnDisk(path)
			if diskID == "" || diskID == sessionWS.ID {
				continue
			}
		}

		sessionWS.Status = session.WorkspaceStatusMissing
		sessionWS.UpdatedAt = time.Now()
		if updateErr := h.store.UpdateWorkspace(ctx, sessionWS); updateErr != nil {
			warnings = append(warnings, fmt.Sprintf("Failed to mark %s as missing", sessionWS.Name))
			continue
		}
		logger.Info("Workspace folder missing from disk; hiding workspace", logger.Fields{
			"workspace_id": sessionWS.ID,
			"name":         sessionWS.Name,
			"path":         path,
		})
		orphaned++
	}

	return orphaned, warnings
}

// workspaceIDOnDisk reads the workspace ID recorded in workspace.json at dir.
// Managed paths may point at the folder root or at a scoped content directory
// inside it (groups), so the parent directory is checked as a fallback. The
// workspaces root itself is never probed, bounding the fallback so it cannot
// walk above workspace folders. Returns "" when no workspace.json is readable.
func (h *Handler) workspaceIDOnDisk(dir string) string {
	root := ""
	if h.workspaceStore != nil {
		root = cleanWorkspaceSyncPath(h.workspaceStore.BasePath())
	}

	for _, candidate := range []string{dir, filepath.Dir(dir)} {
		cleaned := cleanWorkspaceSyncPath(candidate)
		if cleaned == "" || (root != "" && cleaned == root) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(cleaned, agentworkspace.WorkspaceConfigFile)) // #nosec G304 -- cleaned is a stored workspace directory reference bounded above, not raw user input; filename is the fixed WorkspaceConfigFile constant
		if err != nil {
			continue
		}
		var meta struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(data, &meta) == nil && meta.ID != "" {
			return meta.ID
		}
	}
	return ""
}

// ReconcileWorkspacesFromDisk reconciles the session store's workspace structure
// with the on-disk folder layout. Intended for a one-time run at startup so
// groupings that arrived via git/cloud sync are reflected without a manual
// rescan. No-op when no folder store is configured.
func (h *Handler) ReconcileWorkspacesFromDisk(ctx context.Context) error {
	if h == nil || h.workspaceStore == nil {
		return nil
	}
	stats, _, err := h.reconcileWorkspacesFromDisk(ctx)
	if err != nil {
		return err
	}
	if stats.Imported > 0 || stats.Reparented > 0 || stats.Orphaned > 0 || stats.Restored > 0 {
		logger.Info("Startup workspace reconcile from disk", logger.Fields{
			"imported":   stats.Imported,
			"reparented": stats.Reparented,
			"orphaned":   stats.Orphaned,
			"restored":   stats.Restored,
		})
	}
	return nil
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

	meta, ok := raw.(map[string]any)
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
	if h == nil || h.workspaceStore == nil || isFolderImportedWorkspace(ws) {
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

	// Prefer the primary linked-folder reference recorded at scaffolding time.
	// FolderSlug has no SQLite column (it is hydrated from disk, which may be
	// gone), so the primary directory ID in shared_data is the reliable link
	// between a DB row and its last-known folder path.
	if primaryID := workspacePrimaryDirectoryID(&ws); primaryID != "" {
		for _, ref := range refs {
			if ref.ID == primaryID {
				if cleaned := cleanWorkspaceSyncPath(ref.Path); cleaned != "" {
					return cleaned, true
				}
			}
		}
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
	folderSlug := strings.TrimSpace(workspace.FolderSlug)
	newBaseName := filepath.Base(normalizedNew)

	// Groups keep their linked folder and MCP roots scoped to their own
	// content directories; rewriting onto the folder root would expose member
	// sub-workspaces.
	isGroup := workspace.IsGroup()
	defaultRefPath := defaultWorkspaceReferencePath(isGroup, normalizedNew)
	defaultRoots := defaultWorkspaceMCPRoots(isGroup, normalizedNew)

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
		// References at or inside the moved folder keep their relative
		// location (a group's files/ stays files/).
		if rewritten, ok := rewriteWorkspaceContentPath(refPath, normalizedOld, normalizedNew); ok {
			refs[i].WorkspaceID = workspace.ID
			if strings.TrimSpace(refs[i].Name) == "" {
				refs[i].Name = sessionWorkspaceReferenceName(workspace, normalizedNew)
			}
			refs[i].Path = rewritten
			refs[i].UpdatedAt = now
			matchedReference = true
			continue
		}
		if _, ok := rewriteWorkspaceContentPath(refPath, normalizedNew, normalizedNew); ok {
			// Already pointing into the new location.
			matchedReference = true
			continue
		}
		// Name-keyed rebind for stale references whose path matches neither
		// location.
		if (folderSlug != "" && strings.EqualFold(strings.TrimSpace(refs[i].Name), folderSlug)) ||
			(newBaseName != "" && strings.EqualFold(strings.TrimSpace(refs[i].Name), newBaseName)) {
			refs[i].WorkspaceID = workspace.ID
			if strings.TrimSpace(refs[i].Name) == "" {
				refs[i].Name = sessionWorkspaceReferenceName(workspace, normalizedNew)
			}
			refs[i].Path = defaultRefPath
			refs[i].UpdatedAt = now
			matchedReference = true
		}
	}
	if !matchedReference {
		refs = append(refs, workspaceDirectoryReference{
			ID:          uuid.New().String(),
			WorkspaceID: workspace.ID,
			Name:        sessionWorkspaceReferenceName(workspace, normalizedNew),
			Path:        defaultRefPath,
			X:           400,
			Y:           300,
			CreatedAt:   now,
			UpdatedAt:   now,
		})
	}
	refs = compactWorkspaceDirectoryReferences(refs)

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
		if strings.EqualFold(strings.TrimSpace(bindings[i].Alias), workspaceFilesMCPAlias) || workspaceBindingHasRoot(bindings[i].Config, normalizedOld) {
			if bindings[i].Config == nil {
				bindings[i].Config = make(map[string]any)
			}
			bindings[i].Config["roots"] = rewriteWorkspaceBindingRoots(bindings[i].Config["roots"], normalizedOld, normalizedNew, defaultRoots)
			bindings[i].UpdatedAt = now
			bindings[i].Enabled = true
			matchedBinding = true
		}
	}
	if !matchedBinding {
		bindings = append(bindings, newWorkspaceFilesMCPBinding(defaultRoots, now))
	}

	bindingData, err := json.Marshal(bindings)
	if err != nil {
		return fmt.Errorf("failed to encode workspace MCP bindings: %w", err)
	}
	workspace.MCPBindingsJSON = bindingData

	return nil
}

func sessionWorkspaceReferenceName(ws *session.Workspace, path string) string {
	if ws != nil {
		if name := strings.TrimSpace(ws.FolderSlug); name != "" {
			return name
		}
		if name := strings.TrimSpace(ws.Name); name != "" {
			return name
		}
	}
	return filepath.Base(path)
}

func compactWorkspaceDirectoryReferences(refs []workspaceDirectoryReference) []workspaceDirectoryReference {
	if len(refs) < 2 {
		return refs
	}

	seen := make(map[string]int, len(refs))
	compact := make([]workspaceDirectoryReference, 0, len(refs))
	for _, ref := range refs {
		key := cleanWorkspaceSyncPath(ref.Path)
		if key == "" {
			compact = append(compact, ref)
			continue
		}
		if existingIndex, ok := seen[key]; ok {
			if strings.TrimSpace(compact[existingIndex].Name) == "" && strings.TrimSpace(ref.Name) != "" {
				compact[existingIndex].Name = ref.Name
			}
			continue
		}
		seen[key] = len(compact)
		compact = append(compact, ref)
	}
	return compact
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

func workspaceBindingHasRoot(config map[string]any, path string) bool {
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
	case []any:
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
		Tags:           append([]string(nil), workspace.Tags...),
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
