package sessionhttp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// TrashRetention is how long a workspace stays in Trash before it is eligible for
// automatic permanent deletion. It is shared by the Trash listing (to show
// time-until-purge) and the background auto-purger (to decide what to remove).
const TrashRetention = 30 * 24 * time.Hour

// WorkspacePurger performs the permanent, destructive teardown of a workspace:
// its sessions (optionally), its database row, its on-disk folder, and its entry
// agent. It is the single source of truth shared by the HTTP "delete permanently"
// path (deleteWorkspace with ?permanent=true) and the background trash
// auto-purger, so both behave identically.
type WorkspacePurger struct {
	store          session.HybridStore
	workspaceStore *workspace.FileStore // optional folder-based store
	agentStore     store.Store          // optional agent store
}

// NewWorkspacePurger builds a purger from the underlying stores. workspaceStore
// and agentStore may be nil, in which case folder and entry-agent cleanup are
// skipped (the database row is still removed).
func NewWorkspacePurger(s session.HybridStore, ws *workspace.FileStore, agents store.Store) *WorkspacePurger {
	return &WorkspacePurger{store: s, workspaceStore: ws, agentStore: agents}
}

// Purge permanently removes ws. When deleteSessions is true the workspace's
// sessions (and their messages/tool calls) are deleted; otherwise they are
// unlinked to root. Folder and entry-agent cleanup are best-effort and logged
// (non-fatal) once the database row has been removed. Group workspaces have no
// folder or entry agent, so those steps are skipped for them.
func (p *WorkspacePurger) Purge(ctx context.Context, ws *session.Workspace, deleteSessions bool) error {
	id := ws.ID

	// Capture the entry agent name before deletion so it can be cleaned up.
	entryAgentName := ""
	if p.workspaceStore != nil && ws.Kind != session.WorkspaceKindGroup {
		if folderWS, ferr := p.workspaceStore.Get(id); ferr == nil && folderWS != nil {
			entryAgentName = strings.TrimSpace(folderWS.EntryAgentName())
		}
	}

	// Handle session cleanup.
	if deleteSessions {
		if err := p.store.DeleteSessionsByWorkspace(ctx, id); err != nil {
			return fmt.Errorf("failed to delete workspace sessions: %w", err)
		}
	} else {
		if err := p.store.UnlinkSessionsFromWorkspace(ctx, id); err != nil {
			return fmt.Errorf("failed to unlink workspace sessions: %w", err)
		}
	}

	// Permanently delete the workspace row.
	if err := p.store.DeleteWorkspace(ctx, id); err != nil {
		return fmt.Errorf("failed to delete workspace: %w", err)
	}

	// Delete the on-disk folder. Best-effort: the row deletion already succeeded,
	// and the folder store never removes folders outside the workspace root.
	if p.workspaceStore != nil && ws.Kind != session.WorkspaceKindGroup {
		if err := p.workspaceStore.Delete(id); err != nil {
			logger.Warn("Failed to delete workspace folder", logger.Fields{"id": id, "error": err})
		}
	}

	// Delete the workspace's entry agent so it no longer lingers in the agent
	// store. Best-effort on failure.
	if entryAgentName != "" && p.agentStore != nil {
		if _, exists := p.agentStore.GetAgent(entryAgentName); exists {
			if err := p.agentStore.DeleteAgent(entryAgentName); err != nil {
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

	return nil
}
