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
	"github.com/johnjallday/ori-agent/internal/personalhq"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacesettings"
)

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
	if bindingData, err := json.Marshal([]agentworkspace.MCPBinding{mcpBinding}); err == nil {
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

	// A supplied entry agent owns any tasks the imported folder carried that were
	// created before a coordinator existed. No-op when none was supplied/resolved.
	h.claimUnassignedTasksForEntryAgentLogged(workspace.ID)

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

	// An exported workspace.json can say it was its owner's Personal HQ. That is
	// import *intent*, never authority: the designation lives on the local
	// user's profile record, so the copied marker is stripped here and only
	// written back once the authoritative service accepts it below (#290).
	// Only the recognized marker is touched; any other value the snapshot
	// carries is persisted exactly as it is today.
	importedRootIsPersonalHQ := session.NormalizeWorkspaceDesignation(rootWorkspace.Designation) == session.WorkspaceDesignationPersonalHQ
	if importedRootIsPersonalHQ {
		rootWorkspace.Designation = ""
	}

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

		// Imported folders can carry tasks created before this machine knew the
		// workspace's entry agent; claim any unassigned ones for the coordinator
		// resolved from the just-saved folder workspace. No-op without one.
		h.claimUnassignedTasksForEntryAgentLogged(item.ID)

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

	// The restore is durable and the root now resolves through the same store
	// the Personal HQ service reads, so it is safe to offer the imported marker
	// to the authoritative service. Everything past this point is a bounded
	// follow-up: the workspace is imported either way.
	if importedRootIsPersonalHQ {
		h.designateImportedPersonalHQ(ctx, rootWorkspace.ID)
		// Designation has no SQLite column, so the object read above cannot
		// show the result. Re-read the canonical folder record the syncer just
		// wrote, which also reports an undesignated workspace after a conflict.
		rootSessionWorkspace = h.hydrateWorkspaceMetadataFromFileStore(rootSessionWorkspace)
	}

	return rootSessionWorkspace, warning, nil
}

// designateImportedPersonalHQ offers an imported workspace to the authoritative
// Personal HQ service, restoring the marker its exported workspace.json carried
// (#290). It never reports failure to the caller: the workspace is already
// durably imported, so a refused or failed designation leaves an ordinary
// undesignated workspace rather than rolling anything back.
//
// Designate — never Replace: a user who already has a valid HQ keeps it, and
// the imported workspace stays eligible for the existing explicit "use as my
// HQ" action (decision 1A).
func (h *Handler) designateImportedPersonalHQ(ctx context.Context, workspaceID string) {
	if h == nil || h.personalHQDesignator == nil {
		return
	}
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return
	}

	_, err := h.personalHQDesignator.Designate(ctx, userprofile.LocalUserID, workspaceID)
	switch {
	case err == nil:
		logger.Info("Imported workspace restored as personal hq", logger.Fields{"workspace_id": workspaceID})
	case errors.Is(err, personalhq.ErrAlreadyDesignated):
		// Expected, not a failure: the existing HQ wins and the import stands.
		logger.Info("Imported workspace kept undesignated: a personal hq is already designated", logger.Fields{"workspace_id": workspaceID})
	default:
		logger.Warn("Failed to restore imported workspace as personal hq", logger.Fields{"workspace_id": workspaceID, "error": err})
	}
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
	data, err := os.ReadFile(configPath) // #nosec G304 -- folderPath is an operator-selected workspace import directory; the filename is the fixed WorkspaceConfigFile constant
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

// rebaseImportedWorkspaceFolderReferences rewrites an imported workspace's
// directory references and MCP roots from the source path onto the local
// folder path. An empty newPath is a silent no-op (nothing imported).
func rebaseImportedWorkspaceFolderReferences(ws *agentworkspace.Workspace, oldPath string, newPath string) {
	if ws == nil {
		return
	}
	id := folderRebaseIdentity{
		workspaceID: ws.ID,
		folderSlug:  ws.FolderSlug,
		name:        ws.Name,
		isGroup:     session.NormalizeWorkspaceKind(ws.Kind) == session.WorkspaceKindGroup,
	}
	refs, bindings, _ := rebaseWorkspaceFolderReferences(id, ws.DirectoryReferences, ws.MCPBindings, oldPath, newPath, time.Now())
	ws.DirectoryReferences = refs
	ws.MCPBindings = bindings
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
