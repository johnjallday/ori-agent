package workspace

import (
	"fmt"
	"slices"
	"strings"
)

// ReaperPinService persists per-workspace pinned REAPER quick actions
// (an ordered list of script IDs) with the same store.Update lost-write
// protection as BacklogService.Reorder (backlog.go:447). Pin state
// deliberately lives on the Workspace record — never in the globally shared
// script file itself or its frontmatter (internal/reaper/library.go) —
// because REAPER scripts are shared across every workspace, so per-workspace
// pin state has nowhere else portable to live.
//
// Pruning a pin whose script has since been deleted from the shared library
// is a READ-TIME filter, not a write-time prune: this package has no
// dependency on internal/reaper, and the read path (internal/reaperhttp,
// which already holds both the workspace store and the script library) is
// exactly where "does this script still resolve" gets answered. A stale
// pinned ID left in PinnedReaperScripts is harmless — it is simply skipped
// when rendering — and re-pinning the same ID later (e.g. after a script is
// recreated under the same filename) just works.
type ReaperPinService struct {
	store Store
}

// NewReaperPinService constructs a ReaperPinService over the given store.
func NewReaperPinService(store Store) *ReaperPinService {
	return &ReaperPinService{store: store}
}

// Pin appends scriptID to the workspace's pinned quick actions if not
// already present. A no-op (not an error) when already pinned, so callers
// don't need to check state first.
func (s *ReaperPinService) Pin(workspaceID, scriptID string) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("reaper pin service is not configured")
	}
	scriptID = strings.TrimSpace(scriptID)
	if scriptID == "" {
		return fmt.Errorf("script id is required")
	}
	return s.store.Update(workspaceID, func(ws *Workspace) error {
		if slices.Contains(ws.PinnedReaperScripts, scriptID) {
			return nil
		}
		ws.PinnedReaperScripts = append(ws.PinnedReaperScripts, scriptID)
		return nil
	})
}

// Unpin removes scriptID from the workspace's pinned quick actions,
// preserving the relative order of the rest. A no-op when not pinned.
func (s *ReaperPinService) Unpin(workspaceID, scriptID string) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("reaper pin service is not configured")
	}
	scriptID = strings.TrimSpace(scriptID)
	return s.store.Update(workspaceID, func(ws *Workspace) error {
		if len(ws.PinnedReaperScripts) == 0 {
			return nil
		}
		out := make([]string, 0, len(ws.PinnedReaperScripts))
		for _, id := range ws.PinnedReaperScripts {
			if id != scriptID {
				out = append(out, id)
			}
		}
		ws.PinnedReaperScripts = out
		return nil
	})
}

// Reorder replaces the pinned order wholesale. orderedScriptIDs must be
// exactly the workspace's currently pinned IDs, permuted — this is a
// reorder, not a way to pin or unpin — mirroring the duplicate/membership
// guard in BacklogService.Reorder (backlog.go:447).
func (s *ReaperPinService) Reorder(workspaceID string, orderedScriptIDs []string) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("reaper pin service is not configured")
	}
	return s.store.Update(workspaceID, func(ws *Workspace) error {
		seen := make(map[string]bool, len(orderedScriptIDs))
		for _, id := range orderedScriptIDs {
			if seen[id] {
				return fmt.Errorf("duplicate script id in reorder request: %s", id)
			}
			seen[id] = true
		}
		if len(orderedScriptIDs) != len(ws.PinnedReaperScripts) {
			return fmt.Errorf("reorder must include exactly the currently pinned scripts")
		}
		for _, id := range ws.PinnedReaperScripts {
			if !seen[id] {
				return fmt.Errorf("reorder is missing currently pinned script: %s", id)
			}
		}
		ws.PinnedReaperScripts = append([]string(nil), orderedScriptIDs...)
		return nil
	})
}
