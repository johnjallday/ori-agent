package sessionhttp

import (
	"context"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/session"
)

// SetWorkspaceDesignation writes the workspace-side projection of a personalhq
// designation onto the target workspace's workspace.json — the canonical store
// for the Designation field, which has no SQLite column. designation is ""
// (clear) or "personal_hq"; unknown values normalize to "". This implements
// personalhq.DesignationSyncer, wired post-startup once the folder store
// exists (builder wiring-order gotcha).
//
// It is idempotent (a no-op when the field already matches) and refuses to
// project a non-empty designation onto a group workspace, mirroring
// personalhq.ErrGroupNotEligible. The personalhq record is the source of
// truth; the write here is the mirror the startup backfill heals toward.
func (h *Handler) SetWorkspaceDesignation(ctx context.Context, workspaceID, designation string) error {
	if h == nil || h.workspaceStore == nil {
		// No folder store wired: nothing to project onto. Reads hydrate from
		// disk, so a later startup backfill reconciles once a store exists.
		return nil
	}
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return nil
	}
	normalized := string(session.NormalizeWorkspaceDesignation(designation))

	ws, err := h.workspaceStore.Get(workspaceID)
	if err != nil || ws == nil {
		// A record pointing at a trashed/deleted workspace has nothing on disk
		// to clear; leave it for a future backfill rather than erroring.
		return err
	}

	// Groups can never be a personal HQ (mirrors personalhq.ErrGroupNotEligible).
	isGroup := session.NormalizeWorkspaceKind(ws.Kind) == session.WorkspaceKindGroup
	if normalized == string(session.WorkspaceDesignationPersonalHQ) && isGroup {
		return nil
	}

	if strings.TrimSpace(ws.Designation) == normalized {
		return nil // idempotent no-op
	}

	ws.Designation = normalized
	ws.UpdatedAt = time.Now()
	return h.workspaceStore.Save(ws)
}

// BackfillWorkspaceDesignations reconciles every folder-store workspace's
// Designation field against the authoritative set of designated workspace IDs
// (personalhq records), in both directions: it sets "personal_hq" where a
// record backs the workspace but the field is missing, and clears the field
// where it is present but unbacked. Groups never receive the designation, so a
// designated ID pointing at a group is treated as unbacked (cleared if set).
//
// Idempotent and safe to run on every startup. Precedent:
// BackfillGroupScaffolding. A designated ID whose workspace is absent from disk
// (trashed/deleted) is simply not enumerated, so it never wedges the backfill.
func (h *Handler) BackfillWorkspaceDesignations(ctx context.Context, designated map[string]bool) error {
	if h == nil || h.workspaceStore == nil {
		return nil
	}

	ids, err := h.workspaceStore.List()
	if err != nil {
		return err
	}

	checked, healed := 0, 0
	for _, id := range ids {
		ws, err := h.workspaceStore.Get(id)
		if err != nil || ws == nil {
			continue
		}
		checked++

		isGroup := session.NormalizeWorkspaceKind(ws.Kind) == session.WorkspaceKindGroup
		shouldHave := designated[id] && !isGroup
		has := session.NormalizeWorkspaceDesignation(ws.Designation) == session.WorkspaceDesignationPersonalHQ

		var target string
		switch {
		case shouldHave && !has:
			target = string(session.WorkspaceDesignationPersonalHQ)
		case !shouldHave && has:
			target = ""
		default:
			continue // already reconciled
		}

		if err := h.SetWorkspaceDesignation(ctx, id, target); err != nil {
			logger.Warn("Workspace designation backfill failed for workspace", logger.Fields{"id": id, "target": target, "error": err})
			continue
		}
		healed++
	}

	if checked > 0 {
		logger.Info("Workspace designation backfill complete", logger.Fields{"checked": checked, "healed": healed})
	}
	return nil
}
