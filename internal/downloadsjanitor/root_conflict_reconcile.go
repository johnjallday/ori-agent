package downloadsjanitor

import (
	"sort"
	"strings"

	"github.com/johnjallday/ori-agent/internal/logger"
)

// ReconcileResult summarizes one overlap-reconciliation pass.
type ReconcileResult struct {
	// Configured is how many workspaces had a completed setup.
	Configured int
	// Paused is how many were paused because an earlier owner already manages
	// an overlapping folder.
	Paused int
	// Cleared is how many previously-flagged workspaces no longer conflict.
	Cleared int
}

// ReconcileOverlappingRoots resolves folder overlaps that already existed
// before this release.
//
// Cross-workspace conflict prevention is new (FR-49), so an existing install
// can legitimately have two workspaces tidying the same folder — nothing ever
// stopped it. Migration must not resolve that by picking a winner and silently
// repointing the loser at a different folder: the user chose both, and a folder
// is exactly the thing this feature must never change on its own.
//
// So it does the conservative thing (task 3.14):
//
//   - Preserves all data. No settings, batch, decision, journal entry, or file
//     is touched.
//   - Keeps the EARLIEST valid owner active, by setup time. Whoever configured
//     the folder first keeps running against it.
//   - Pauses later conflicting workspaces and records which workspace holds
//     their folder, so readiness reports a repairable `Needs attention` and the
//     user makes the call.
//
// Pausing is reversible and stops only unattended work; the user can still open
// the workspace, review pending files, and scan manually.
func (s *Service) ReconcileOverlappingRoots(workspaceIDs []string) ReconcileResult {
	var result ReconcileResult
	if s == nil || s.store == nil {
		return result
	}

	type owner struct {
		workspaceID string
		settings    JanitorSettings
	}

	owners := make([]owner, 0, len(workspaceIDs))
	for _, id := range workspaceIDs {
		settings, err := s.store.LoadSettings(id)
		if err != nil || !settings.IsSetUp() {
			continue
		}
		owners = append(owners, owner{workspaceID: id, settings: settings})
	}
	result.Configured = len(owners)
	ids := make([]string, 0, len(owners))
	for _, o := range owners {
		ids = append(ids, o.workspaceID)
	}

	if len(owners) < 2 {
		// Nothing can conflict with itself. Still clear any recorded conflict
		// that has since resolved (e.g. the other workspace revoked access).
		result.Cleared = s.clearResolvedConflicts(ids, map[string]string{})
		return result
	}

	// Earliest setup wins. Ties break on workspace ID so the outcome is
	// deterministic across restarts rather than depending on map order.
	sort.SliceStable(owners, func(i, j int) bool {
		left, right := owners[i].settings.SetupCompletedAt, owners[j].settings.SetupCompletedAt
		if left.Equal(right) {
			return owners[i].workspaceID < owners[j].workspaceID
		}
		return left.Before(right)
	})

	accepted := make([]owner, 0, len(owners))
	conflicts := make(map[string]string, len(owners))

	for _, candidate := range owners {
		conflictedWith := ""
		for _, held := range accepted {
			if RootsOverlap(held.settings.RootPath, candidate.settings.RootPath) {
				conflictedWith = held.workspaceID
				break
			}
		}
		if conflictedWith == "" {
			accepted = append(accepted, candidate)
			continue
		}
		conflicts[candidate.workspaceID] = conflictedWith
		if s.markRootConflict(candidate.workspaceID, conflictedWith) {
			result.Paused++
		}
	}

	result.Cleared = s.clearResolvedConflicts(ids, conflicts)

	if result.Paused > 0 {
		logger.Warn("File Janitor paused workspaces whose folders overlap an earlier one", logger.Fields{
			"paused":     result.Paused,
			"configured": result.Configured,
		})
	}
	return result
}

// markRootConflict pauses a workspace and records which workspace holds its
// folder. It never changes the root.
func (s *Service) markRootConflict(workspaceID, conflictsWith string) bool {
	changed := false
	if _, err := s.store.UpdateSettings(workspaceID, func(settings *JanitorSettings) error {
		if settings.RootConflictWorkspaceID == conflictsWith && settings.Paused {
			return nil
		}
		settings.RootConflictWorkspaceID = conflictsWith
		// Pause unattended work only. Pending review, history, and the folder
		// grant itself are all left exactly as they were.
		settings.Paused = true
		changed = true
		return nil
	}); err != nil {
		logger.Warn("File Janitor could not pause a conflicting workspace", logger.Fields{
			"workspace_id": workspaceID,
			"error":        err.Error(),
		})
		return false
	}
	return changed
}

// clearResolvedConflicts drops a recorded conflict from any workspace that no
// longer has one — for example because the other workspace revoked access.
//
// It deliberately does NOT un-pause: pausing may have been the user's own
// choice afterwards, and resuming unattended file work on their behalf is not
// something a reconciliation pass should decide.
func (s *Service) clearResolvedConflicts(candidates []string, conflicts map[string]string) int {
	cleared := 0
	for _, id := range candidates {
		if _, stillConflicted := conflicts[id]; stillConflicted {
			continue
		}
		if _, err := s.store.UpdateSettings(id, func(settings *JanitorSettings) error {
			if settings.RootConflictWorkspaceID == "" {
				return nil
			}
			settings.RootConflictWorkspaceID = ""
			cleared++
			return nil
		}); err != nil {
			logger.Warn("File Janitor could not clear a resolved folder conflict", logger.Fields{
				"workspace_id": id,
				"error":        err.Error(),
			})
		}
	}
	return cleared
}

// conflictOrAccessCheck reports the folder-overlap failure when there is one,
// and the ordinary directory-access result otherwise.
//
// A conflicted workspace's folder is usually perfectly readable, so the access
// check would pass and the workspace would look Ready while being paused for a
// reason nothing on screen explained.
func conflictOrAccessCheck(conflict, access ComponentCheck) ComponentCheck {
	if conflict.Status == ComponentFailed {
		return conflict
	}
	return access
}

// checkRootConflict reports a recorded overlap as a failing readiness component
// with a repair the user can act on. It is the surface that turns a silent
// pause into a visible, explainable `Needs attention` (task 3.14).
func (s *Service) checkRootConflict(settings JanitorSettings) ComponentCheck {
	check := ComponentCheck{Component: ComponentDirectoryAccess, Status: ComponentOK}
	if strings.TrimSpace(settings.RootConflictWorkspaceID) == "" {
		return check
	}
	check.Status = ComponentFailed
	check.Code = CodeFolderConflict
	check.Message = "Another workspace is already tidying this folder, so File Janitor paused here. Choose a different folder, or stop the other workspace from managing it."
	check.Repair = RepairChooseFolder
	return check
}
