package sessionhttp

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/session"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// BackfillGroupScaffolding upgrades group workspaces created when groups were
// organization-only containers. For every non-trashed group it ensures the
// on-disk folder exists with sub-workspaces/, files/ and notes/, and adds the
// scoped default directory reference and workspace-files MCP binding when
// missing. Pieces that already exist are left untouched, so the backfill is
// idempotent and safe to run on every startup.
func (h *Handler) BackfillGroupScaffolding(ctx context.Context) error {
	if h == nil || h.store == nil || h.workspaceStore == nil {
		return nil
	}

	workspaces, err := h.store.ListWorkspaces(ctx)
	if err != nil {
		return err
	}

	groups := make([]session.Workspace, 0)
	parents := make(map[string]string, len(workspaces))
	for _, ws := range workspaces {
		parents[ws.ID] = ws.ParentID
		if ws.IsGroup() && ws.Status != session.WorkspaceStatusTrashed {
			groups = append(groups, ws)
		}
	}

	// Parents before children so a legacy DB-only group nested inside another
	// legacy group gets its folder created under an existing parent folder.
	sort.SliceStable(groups, func(i, j int) bool {
		return workspaceTreeDepth(parents, groups[i].ID) < workspaceTreeDepth(parents, groups[j].ID)
	})

	checked, backfilled := 0, 0
	for i := range groups {
		checked++
		changed, err := h.backfillGroupScaffolding(ctx, &groups[i])
		if err != nil {
			logger.Warn("Group scaffolding backfill failed", logger.Fields{"id": groups[i].ID, "error": err})
			continue
		}
		if changed {
			backfilled++
		}
	}

	if checked > 0 {
		logger.Info("Group scaffolding backfill complete", logger.Fields{"checked": checked, "backfilled": backfilled})
	}
	return nil
}

// workspaceTreeDepth counts ancestors via the parent map, bailing out on
// cycles or dangling parents.
func workspaceTreeDepth(parents map[string]string, id string) int {
	depth := 0
	for current := parents[id]; current != "" && depth <= len(parents); depth++ {
		current = parents[current]
	}
	return depth
}

// backfillGroupScaffolding provisions a single group, reporting whether
// anything changed.
func (h *Handler) backfillGroupScaffolding(ctx context.Context, ws *session.Workspace) (bool, error) {
	folderPath, err := h.workspaceStore.GetFolderPath(ws.ID)
	if err != nil {
		// Legacy DB-only group: create its folder via the standard save path.
		folderWS, buildErr := buildFileStoreWorkspace(ws)
		if buildErr != nil {
			return false, buildErr
		}
		if saveErr := h.workspaceStore.Save(folderWS); saveErr != nil {
			return false, saveErr
		}
		if folderPath, err = h.workspaceStore.GetFolderPath(ws.ID); err != nil {
			return false, err
		}
	}

	dirs, err := ensureGroupContentDirs(folderPath)
	if err != nil {
		return false, err
	}

	hydrated := h.hydrateWorkspaceMetadataFromFileStore(ws)
	now := time.Now()
	changed := false

	refs, err := decodeDirectoryReferences(hydrated.DirectoryReferencesJSON)
	if err != nil {
		return false, err
	}
	if len(refs) == 0 {
		dirRef := newWorkspaceDirectoryReference(hydrated, dirs.files, now)
		data, err := json.Marshal([]workspaceDirectoryReference{dirRef})
		if err != nil {
			return false, err
		}
		hydrated.DirectoryReferencesJSON = data
		setWorkspacePrimaryDirectoryID(hydrated, dirRef.ID)
		changed = true
	}

	bindings, err := decodeWorkspaceMCPBindings(hydrated.MCPBindingsJSON)
	if err != nil {
		return false, err
	}
	if !hasWorkspaceFilesBinding(bindings) {
		bindings = append(bindings, newWorkspaceFilesMCPBinding(dirs.mcpRoots(), now))
		data, err := json.Marshal(bindings)
		if err != nil {
			return false, err
		}
		hydrated.MCPBindingsJSON = data
		changed = true
	}

	if !changed {
		return false, nil
	}

	hydrated.UpdatedAt = now
	if err := h.store.UpdateWorkspace(ctx, hydrated); err != nil {
		return false, err
	}
	if err := h.syncWorkspacePortableStateToFileStore(hydrated); err != nil {
		logger.Warn("Failed to sync workspace.json after group backfill", logger.Fields{"id": hydrated.ID, "error": err})
	}
	return true, nil
}

func hasWorkspaceFilesBinding(bindings []agentworkspace.WorkspaceMCPBinding) bool {
	for _, binding := range bindings {
		if strings.EqualFold(strings.TrimSpace(binding.Alias), workspaceFilesMCPAlias) {
			return true
		}
	}
	return false
}
