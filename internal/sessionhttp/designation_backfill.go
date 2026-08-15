package sessionhttp

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/personalhq"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/userprofile"
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

	// A profile holding no designation at all is not authority to erase the
	// workspace tree's own marker. The folder tree is shared across data dirs
	// — every git worktree and dev build gets its own database but the same
	// ~/Ori Workspaces — so an empty record almost always means "this database
	// has not met this workspace yet", not "the user has no HQ". Clearing here
	// destroyed the only portable evidence of the designation. Adopt it
	// instead, which is also what makes a fresh worktree recognize the HQ.
	//
	// A deliberate Clear() empties the projection as part of the same
	// transition, so there is normally nothing left for this to re-adopt; a
	// Clear whose projection write failed is the one case that now resurrects
	// rather than settles, which is the accepted trade for not destroying a
	// valid marker on every worktree start.
	if len(designated) == 0 {
		return h.adoptPersonalHQFromFolders(ctx, ids)
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

// adoptPersonalHQFromFolders restores a Personal HQ designation this database
// has never recorded from the canonical marker the workspace folder already
// carries, so a workspace tree shared across data dirs (worktrees, dev builds,
// a reinstall, a restored backup) keeps one HQ identity instead of silently
// losing it.
//
// It adopts only an unambiguous claim: exactly one non-group workspace marked
// personal_hq. Several claims mean the tree itself is inconsistent, which a
// startup path must report rather than resolve by guessing. Adoption goes
// through the authoritative service, so the one-HQ model, eligibility rules,
// and projection sync all still apply. Every failure is non-fatal — startup
// must not depend on it.
func (h *Handler) adoptPersonalHQFromFolders(ctx context.Context, ids []string) error {
	if h == nil || h.workspaceStore == nil {
		return nil
	}

	candidates := make([]string, 0, 1)
	for _, id := range ids {
		ws, err := h.workspaceStore.Get(id)
		if err != nil || ws == nil {
			continue
		}
		if session.NormalizeWorkspaceKind(ws.Kind) == session.WorkspaceKindGroup {
			continue // groups can never be an HQ (personalhq.ErrGroupNotEligible)
		}
		if session.NormalizeWorkspaceDesignation(ws.Designation) == session.WorkspaceDesignationPersonalHQ {
			candidates = append(candidates, id)
		}
	}

	switch {
	case len(candidates) == 0:
		return nil
	case len(candidates) > 1:
		logger.Warn("Skipping personal hq adoption: more than one workspace claims the designation", logger.Fields{
			"workspace_ids": candidates,
		})
		return nil
	case h.personalHQDesignator == nil:
		// Nothing to adopt with, but the marker is left intact so a later
		// start with the dependency wired can still recover it.
		return nil
	}

	if _, err := h.personalHQDesignator.Designate(ctx, userprofile.LocalUserID, candidates[0]); err != nil {
		if errors.Is(err, personalhq.ErrAlreadyDesignated) {
			return nil
		}
		logger.Warn("Failed to adopt personal hq from workspace folder", logger.Fields{
			"workspace_id": candidates[0], "error": err,
		})
		return nil
	}

	logger.Info("Adopted personal hq from workspace folder", logger.Fields{"workspace_id": candidates[0]})
	return nil
}
