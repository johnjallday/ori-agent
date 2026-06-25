package sessionhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"runtime"
	"strings"
	"time"

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

	var resolvedTemplate projecttemplates.Template
	templateResolved := false
	var templateResolveErr error
	if wantsProject {
		resolvedTemplate, templateResolveErr = h.resolveProjectTemplate(req.TemplateID, req.TemplatePath)
		templateResolved = templateResolveErr == nil
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
	if templateResolved {
		ws.Tags = agentworkspace.MergeWorkspaceTags(ws.Tags, resolvedTemplate.Tags)
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
	var onboardingSummary any
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
					onboardingHandled := false
					if templateResolved && h.templateOnboarding != nil && resolvedTemplate.HasOnboarding() {
						summary, handled, err := h.templateOnboarding.ResolveAndStart(r.Context(), ws, resolvedTemplate)
						onboardingHandled = handled
						if summary != nil {
							onboardingSummary = summary
						}
						if handled {
							projectWarning = ""
							if err != nil {
								projectWarning = fmt.Sprintf("workspace was created, but template onboarding could not start: %v", err)
								logger.Warn("Template onboarding session creation failed", logger.Fields{"id": ws.ID, "template": resolvedTemplate.ID, "error": err})
							} else {
								logger.Info("Template onboarding session created", logger.Fields{"id": ws.ID, "template": resolvedTemplate.ID})
							}
						}
					}
					if !onboardingHandled {
						// Non-fatal by design: a failed instantiation must not fail
						// workspace creation. The warning is surfaced to the user.
						if err := h.instantiateWorkspaceProject(r.Context(), ws, folderWS, req.TemplateID, req.TemplatePath, req.ProjectName); err != nil {
							if templateResolveErr != nil {
								err = templateResolveErr
							}
							projectWarning = fmt.Sprintf("workspace was created, but the project template was not applied: %v", err)
							logger.Warn("Project template instantiation failed", logger.Fields{"id": ws.ID, "error": err})
						} else {
							projectWarning = ""
						}
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
	if onboardingSummary != nil {
		response["onboarding"] = onboardingSummary
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

type workspaceImportItem struct {
	Workspace  *agentworkspace.Workspace
	SourcePath string
}

// =============================================================================
// Workspace Agent Management
// =============================================================================

// =============================================================================
// Workspace Layout Management
// =============================================================================

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
