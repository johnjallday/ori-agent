package sessionhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/platform"
	"github.com/johnjallday/ori-agent/internal/session"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

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
	restoreCandidates := []*session.Workspace{ws}
	if ws.Kind == session.WorkspaceKindGroup {
		if descendantIDs, err := h.store.GetSubworkspaceIDs(ctx, ws.ID); err == nil {
			for _, memberID := range descendantIDs {
				member, getErr := h.store.GetWorkspace(ctx, memberID)
				if getErr == nil && member.Status == session.WorkspaceStatusTrashed {
					restoreCandidates = append(restoreCandidates, member)
				}
			}
		}
	}
	for _, candidate := range restoreCandidates {
		if owner := h.registeredWorkspaceSlugOwner(ctx, candidate.FolderSlug, candidate.ID); owner != "" {
			writeWorkspaceCreateSlugConflict(w, candidate.Name, h.globalWorkspaceSlugConflict(ctx, candidate.FolderSlug, candidate.ID, filepath.Dir(originalPath)))
			return
		}
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
