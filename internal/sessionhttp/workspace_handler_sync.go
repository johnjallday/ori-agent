package sessionhttp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/session"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

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

			if err := os.MkdirAll(targetPath, 0755); err != nil { // #nosec G301 -- preserves the 0755 permissions used for user-facing workspace folders prior to this refactor
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

	stats, warnings, err := h.reconcileWorkspacesFromDisk(r.Context(), true)
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

// reconcileWorkspacesFromDisk reloads the folder store from disk and updates the
// session store so its structure matches the on-disk layout. Returns reconcile
// stats plus any non-fatal warnings. Safe to call on startup and on demand;
// concurrent calls are serialized.
//
// reload controls whether the folder store is refreshed from disk first. On-demand
// rescans pass true so out-of-band changes (git pull, cloud sync) are picked up.
// The startup caller passes false: NewFileStore has just loaded the cache + index
// from disk, so an immediate Reload would redo that full scan/parse for nothing.
func (h *Handler) reconcileWorkspacesFromDisk(ctx context.Context, reload bool) (stats workspaceReconcileStats, warnings []string, err error) {
	if h.workspaceStore == nil {
		return stats, nil, nil
	}

	h.rescanMu.Lock()
	defer func() {
		h.finishReconcileLocked(err)
		h.rescanMu.Unlock()
	}()

	return h.reconcileWorkspacesFromDiskLocked(ctx, workspaceReconcileOptions{reload: reload})
}

// workspaceReconcileOptions selects how one reconcile pass behaves. The zero
// value is the ordinary same-root pass used by startup and explicit Rescan.
type workspaceReconcileOptions struct {
	// reload re-walks the folder tree before reconciling. On-demand rescans set
	// it so out-of-band changes are picked up; startup and live-root
	// application do not, because the cache was just loaded from disk.
	reload bool
	// previousRoot is the workspace root a live switch just replaced, already
	// normalized. Empty for an ordinary same-root pass.
	previousRoot string
	// previousFolders maps workspace ID → the absolute folder path the store
	// resolved for it under previousRoot. It is what lets the sweep tell a
	// workspace that was managed by the old root apart from an explicitly
	// imported or recovery-located folder that merely sits somewhere else.
	previousFolders map[string]string
}

// isRootSwitch reports whether this pass follows a genuine root change.
func (o workspaceReconcileOptions) isRootSwitch() bool {
	return o.previousRoot != ""
}

// finishReconcileLocked records a successful reconcile for the background
// cooldown. Callers must hold h.rescanMu.
func (h *Handler) finishReconcileLocked(err error) {
	if err == nil {
		h.lastRescanAt = time.Now()
	}
}

// reconcileWorkspacesFromDiskLocked is the single reconcile implementation
// shared by startup, the explicit Rescan endpoint, and live workspace-root
// application. Callers must hold h.rescanMu; the live-root path holds it across
// the folder-store re-point as well, so a rescan can never observe a
// half-switched store.
func (h *Handler) reconcileWorkspacesFromDiskLocked(ctx context.Context, opts workspaceReconcileOptions) (stats workspaceReconcileStats, warnings []string, err error) {
	if h.workspaceStore == nil {
		return stats, nil, nil
	}

	// Refresh the file-store cache + index from disk; physical layout wins.
	if opts.reload {
		if err := h.workspaceStore.Reload(); err != nil {
			return stats, nil, err
		}
	}

	warnings = make([]string, 0)
	diskWorkspaces := h.workspaceStore.CachedWorkspaces()
	for _, id := range orderWorkspacesParentFirst(diskWorkspaces) {
		diskWS := diskWorkspaces[id]
		sessionWS, getErr := h.store.GetWorkspace(ctx, id)
		if getErr == session.ErrWorkspaceNotFound {
			// CachedWorkspaces returns metadata-only structs (item 2.0); Get reads
			// the full record so the imported workspace keeps its history and tasks.
			fullWS, loadErr := h.workspaceStore.Get(id)
			if loadErr != nil {
				warnings = append(warnings, fmt.Sprintf("Failed to load %s for import", diskWS.Name))
				continue
			}
			converted := session.ConvertAgentWorkspace(fullWS)
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
	orphaned, sweepWarnings := h.sweepMissingWorkspaces(ctx, diskWorkspaces, opts)
	stats.Orphaned = orphaned
	warnings = append(warnings, sweepWarnings...)

	return stats, warnings, nil
}

// orderWorkspacesParentFirst orders disk workspaces so a parent is always
// reconciled before its children. The session store enforces a parent_id
// foreign key, so creating a nested workspace before its group is rejected —
// and because Go randomizes map iteration, that failure would otherwise strike
// intermittently when a root containing groups is scanned for the first time.
// Workspaces whose parent is not part of the set (a group that failed to load,
// or a stale reference) are still attempted, after the ones that can be ordered.
func orderWorkspacesParentFirst(diskWorkspaces map[string]*agentworkspace.Workspace) []string {
	ids := make([]string, 0, len(diskWorkspaces))
	for id := range diskWorkspaces {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	ordered := make([]string, 0, len(ids))
	emitted := make(map[string]bool, len(ids))
	for len(ordered) < len(ids) {
		progressed := false
		for _, id := range ids {
			if emitted[id] {
				continue
			}
			if parentID := diskWorkspaces[id].ParentID; parentID != "" && !emitted[parentID] {
				if _, known := diskWorkspaces[parentID]; known {
					continue // wait for the parent to be emitted first
				}
			}
			ordered = append(ordered, id)
			emitted[id] = true
			progressed = true
		}
		if !progressed {
			// A parent cycle, only reachable from hand-edited files: emit the
			// remainder in a stable order rather than spinning.
			for _, id := range ids {
				if !emitted[id] {
					ordered = append(ordered, id)
					emitted[id] = true
				}
			}
		}
	}
	return ordered
}

// pathWithinRoot reports whether path lies strictly inside root. Containment is
// computed with filepath.Rel rather than a string prefix so a sibling directory
// whose name merely starts with the root's ("…/Roots" vs "…/Root") is not
// mistaken for a child.
func pathWithinRoot(root, path string) bool {
	cleanRoot := cleanWorkspaceSyncPath(root)
	cleanPath := cleanWorkspaceSyncPath(path)
	if cleanRoot == "" || cleanPath == "" {
		return false
	}
	rel, err := filepath.Rel(cleanRoot, cleanPath)
	if err != nil {
		return false
	}
	if rel == "." || rel == ".." {
		return false
	}
	return !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// leftBehindByRootSwitch reports whether a session workspace was a workspace of
// the root a live switch just replaced, and so should stop being listed even
// though its folder is still sitting on disk under that old root.
//
// Explicit folder imports are excluded wherever they live: their visibility is
// owned by the import flow, not by which directory happens to be active. So are
// workspaces the store had registered by an absolute path outside the previous
// root (recovery-located folders), and rows the previous root never held at all
// (legacy database-only workspaces).
func leftBehindByRootSwitch(opts workspaceReconcileOptions, ws session.Workspace) bool {
	if !opts.isRootSwitch() || isFolderImportedWorkspace(ws) {
		return false
	}
	folder, ok := opts.previousFolders[ws.ID]
	if !ok {
		return false
	}
	return pathWithinRoot(opts.previousRoot, folder)
}

// sweepMissingWorkspaces marks folder-managed session workspaces as missing when
// their backing folder is gone from disk, has been recreated as a different
// workspace (same path, different ID), or — after a live root switch — belongs
// to the root that was just replaced. Returns the number of workspaces marked.
func (h *Handler) sweepMissingWorkspaces(ctx context.Context, diskWorkspaces map[string]*agentworkspace.Workspace, opts workspaceReconcileOptions) (int, []string) {
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

		// A live root switch is the one case where a folder that is still on
		// disk must stop being listed: it belongs to the root the user just
		// navigated away from. Nothing is deleted — the folder, the chat
		// history, and the tasks all stay exactly as they are, and selecting
		// that root again brings the workspace back.
		leftBehind := leftBehindByRootSwitch(opts, *sessionWS)

		switch {
		case leftBehind:
			if !managed {
				// The row carries no directory reference to fall back on, but
				// the switch knows precisely which folder it occupied.
				path = opts.previousFolders[sessionWS.ID]
			}
		case !managed:
			continue // legacy DB-only workspace; nothing on disk to compare
		default:
			exists, statErr := workspaceFolderExists(path)
			if statErr != nil {
				continue // unreadable path: leave the workspace alone
			}
			if exists {
				// The folder is still there but this ID is not in the disk
				// cache: either the folder now belongs to a different workspace
				// (deleted and recreated externally — mark the stale row
				// missing), or it lives outside the workspaces root
				// (located/imported — leave it).
				diskID := h.workspaceIDOnDisk(path)
				if diskID == "" || diskID == sessionWS.ID {
					continue
				}
			}
		}

		sessionWS.Status = session.WorkspaceStatusMissing
		sessionWS.UpdatedAt = time.Now()
		if updateErr := h.store.UpdateWorkspace(ctx, sessionWS); updateErr != nil {
			warnings = append(warnings, fmt.Sprintf("Failed to mark %s as missing", sessionWS.Name))
			continue
		}
		if leftBehind {
			logger.Info("Workspace belongs to the previous workspace directory; hiding workspace", logger.Fields{
				"workspace_id": sessionWS.ID,
				"name":         sessionWS.Name,
			})
		} else {
			logger.Info("Workspace folder missing from disk; hiding workspace", logger.Fields{
				"workspace_id": sessionWS.ID,
				"name":         sessionWS.Name,
				"path":         path,
			})
		}
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
//
// Skips the folder-store reload: at startup NewFileStore has already loaded the
// cache + index from disk moments earlier, so reloading here would repeat that
// full scan/parse (the second "Workspace index rebuilt" at boot) for nothing.
func (h *Handler) ReconcileWorkspacesFromDisk(ctx context.Context) error {
	if h == nil || h.workspaceStore == nil {
		return nil
	}
	stats, _, err := h.reconcileWorkspacesFromDisk(ctx, false)
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

// WorkspaceRootRefresh summarizes a live workspace-root application: what the
// newly active root added, re-grouped, hid, or brought back into view.
type WorkspaceRootRefresh struct {
	Imported   int
	Reparented int
	Orphaned   int
	Restored   int
	Warnings   []string
}

// ApplyWorkspaceRoot re-points the live folder store at root and reconciles the
// session store against it, producing the same visible workspace set a restart
// against that root would produce. Nothing on disk is moved, copied, or deleted,
// and no unrelated startup maintenance is replayed.
//
// The folder-store re-point and the reconcile are performed under the same
// rescanMu the explicit Rescan endpoint uses, so the two can never interleave.
// SetBasePath has already loaded the target root's cache, so the reconcile does
// not walk the tree a second time.
//
// A build with no folder store reports an empty refresh rather than failing: the
// directory is still persisted for the next start.
func (h *Handler) ApplyWorkspaceRoot(ctx context.Context, root string) (refresh WorkspaceRootRefresh, err error) {
	if h == nil || h.workspaceStore == nil {
		return WorkspaceRootRefresh{Warnings: []string{}}, nil
	}
	if strings.TrimSpace(root) == "" {
		return WorkspaceRootRefresh{}, fmt.Errorf("workspace directory is required")
	}

	h.rescanMu.Lock()
	defer func() {
		h.finishReconcileLocked(err)
		h.rescanMu.Unlock()
	}()

	change, err := h.workspaceStore.SetBasePath(root)
	if err != nil {
		return WorkspaceRootRefresh{}, err
	}

	// SetBasePath has already loaded the target root's cache, so the reconcile
	// must not walk it again. On a genuine change it also reports which folders
	// the previous root held, which is how the sweep tells a workspace left
	// behind by the switch from one that was explicitly imported from elsewhere.
	opts := workspaceReconcileOptions{reload: false}
	if change.Switched {
		opts.previousRoot = change.PreviousRoot
		opts.previousFolders = change.PreviousFolders
	}

	stats, warnings, err := h.reconcileWorkspacesFromDiskLocked(ctx, opts)
	if err != nil {
		return WorkspaceRootRefresh{}, err
	}
	if warnings == nil {
		warnings = []string{}
	}

	logger.Info("Workspace directory applied without restart", logger.Fields{
		"imported":   stats.Imported,
		"reparented": stats.Reparented,
		"orphaned":   stats.Orphaned,
		"restored":   stats.Restored,
		"warnings":   len(warnings),
	})

	return WorkspaceRootRefresh{
		Imported:   stats.Imported,
		Reparented: stats.Reparented,
		Orphaned:   stats.Orphaned,
		Restored:   stats.Restored,
		Warnings:   warnings,
	}, nil
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

// updateManagedWorkspaceReferences rebases a managed session workspace's
// directory references and MCP roots after its folder moves from oldPath to
// newPath, re-encoding the JSON mirror fields. An empty newPath is an error
// (the sync path always has a concrete destination).
func updateManagedWorkspaceReferences(workspace *session.Workspace, oldPath string, newPath string) error {
	if workspace == nil {
		return fmt.Errorf("workspace is required")
	}

	refs, err := decodeDirectoryReferences(workspace.DirectoryReferencesJSON)
	if err != nil {
		return fmt.Errorf("failed to decode directory references: %w", err)
	}
	bindings, err := decodeWorkspaceMCPBindings(workspace.MCPBindingsJSON)
	if err != nil {
		return fmt.Errorf("failed to decode workspace MCP bindings: %w", err)
	}

	id := folderRebaseIdentity{
		workspaceID: workspace.ID,
		folderSlug:  workspace.FolderSlug,
		name:        workspace.Name,
		isGroup:     workspace.IsGroup(),
	}
	refs, bindings, ok := rebaseWorkspaceFolderReferences(id, refs, bindings, oldPath, newPath, time.Now())
	if !ok {
		return fmt.Errorf("new workspace folder path is required")
	}

	refData, err := json.Marshal(refs)
	if err != nil {
		return fmt.Errorf("failed to encode directory references: %w", err)
	}
	workspace.DirectoryReferencesJSON = refData

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
