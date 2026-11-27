package workspace

import (
	"path/filepath"
	"sort"
	"strings"
)

// CoveringFolderWorkspace answers where a song set up from a station's shelf
// belongs: the closest folder workspace inside the station whose anchored
// folder contains the song's folder, or nil for the station itself.
//
// "Closest" is the deepest anchor that contains songFolder (FR4, FR5). A
// folder workspace anchored to exactly songFolder is not its own song (FR7),
// and an empty or unresolvable folder maps to the station. A candidate must be
// active, carry no station state and no project link, and sit inside the
// station by the same rule a song does (ParentInsideStation). It may still be
// a plain workspace that joined before nesting existed; the caller promotes
// it before using it as a parent (FR2).
func CoveringFolderWorkspace(workspaces []*Workspace, lookup WorkspaceLookup, stationID, songFolder string) *Workspace {
	stationID = strings.TrimSpace(stationID)
	folder := comparablePath(songFolder)
	if stationID == "" || folder == "" {
		return nil
	}
	anchors := make(map[string]string, len(workspaces))
	byID := make(map[string]*Workspace, len(workspaces))
	for _, candidate := range workspaces {
		if anchor := StationFolderAnchor(lookup, candidate, stationID); anchor != "" {
			anchors[candidate.ID] = anchor
			byID[candidate.ID] = candidate
		}
	}
	if id := deepestCoveringAnchor(folder, anchors); id != "" {
		return byID[id]
	}
	return nil
}

// StationFolderAnchor is the single fed-folder rule for placement and the Home
// map. Its result is a server-only comparison key, never a path grant.
func StationFolderAnchor(lookup WorkspaceLookup, candidate *Workspace, stationID string) string {
	if candidate == nil || candidate.Status == StatusTrashed || candidate.Status == StatusMissing || candidate.AssistantProgramState != nil || candidate.AssistantProjectLink != nil || !WorkspaceInsideStation(lookup, candidate, stationID) {
		return ""
	}
	id := candidate.AnchorDirectoryReferenceID()
	if id == "" {
		return ""
	}
	ref, err := candidate.GetDirectoryReference(id)
	if err != nil || ref == nil {
		return ""
	}
	return comparablePath(ref.Path)
}

// deepestCoveringAnchor picks, among id → anchor path, the deepest anchor
// that strictly contains folder. Both sides are already comparable paths. An
// anchor equal to folder does not cover it. Equal depths (which the one-owner
// rule prevents inside one station) resolve to the smallest id, so the answer
// never depends on map order.
func deepestCoveringAnchor(folder string, anchors map[string]string) string {
	ids := make([]string, 0, len(anchors))
	for id := range anchors {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	best, bestDepth := "", -1
	for _, id := range ids {
		anchor := anchors[id]
		rel, err := filepath.Rel(anchor, folder)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			continue
		}
		if depth := strings.Count(anchor, string(filepath.Separator)); depth > bestDepth {
			best, bestDepth = id, depth
		}
	}
	return best
}

// comparablePath cleans an absolute path and resolves symlinks when the path
// exists, so /var and /private/var (or a linked Music folder) compare equal.
// A relative or empty path is not comparable.
func comparablePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" || !filepath.IsAbs(path) {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return filepath.Clean(resolved)
	}
	return filepath.Clean(path)
}
