package workspace

import (
	"errors"
	"strings"
)

var errStationChainWorkspaceNotFound = errors.New("workspace not found")

// WorkspaceLookup reads one workspace by id. Store.Get satisfies it; callers
// that already hold a store's lock pass LookupInWorkspaceMap over its cache.
type WorkspaceLookup func(id string) (*Workspace, error)

// LookupInWorkspaceMap adapts an id-keyed workspace map to a WorkspaceLookup.
// The map is read, never copied or mutated.
func LookupInWorkspaceMap(workspaces map[string]*Workspace) WorkspaceLookup {
	return func(id string) (*Workspace, error) {
		if ws := workspaces[id]; ws != nil {
			return ws, nil
		}
		return nil, errStationChainWorkspaceNotFound
	}
}

// WorkspaceInsideStation reports whether a linked project sits inside its
// station: its parent is the station itself, or an ordinary group whose chain
// of parents reaches the station without leaving it.
//
// This is the one rule for "a Home's song is where it belongs". A chain group
// must be active, a group, and neither another station nor another linked
// project. The walk is bounded by MaxNestingDepth and refuses a cycle, a
// missing parent, and a trashed group anywhere on the chain.
func WorkspaceInsideStation(lookup WorkspaceLookup, project *Workspace, stationID string) bool {
	if project == nil {
		return false
	}
	return ParentInsideStation(lookup, project.ID, project.ParentID, stationID)
}

// ParentInsideStation is WorkspaceInsideStation for a candidate parent that
// is not (or not yet) the project's recorded one, such as the destination of
// a move or the parent before an update.
func ParentInsideStation(lookup WorkspaceLookup, projectID, parentID, stationID string) bool {
	projectID = strings.TrimSpace(projectID)
	parentID = strings.TrimSpace(parentID)
	stationID = strings.TrimSpace(stationID)
	if stationID == "" || parentID == "" || stationID == projectID {
		return false
	}
	if parentID == stationID {
		return true
	}
	if lookup == nil {
		return false
	}
	seen := map[string]bool{projectID: true, stationID: true}
	current := parentID
	for depth := 0; depth < MaxNestingDepth; depth++ {
		if seen[current] {
			return false
		}
		seen[current] = true
		group, err := lookup(current)
		if err != nil || group == nil || group.ID != current || !plainGroupOnStationChain(group) {
			return false
		}
		next := strings.TrimSpace(group.ParentID)
		if next == stationID {
			return true
		}
		if next == "" {
			return false
		}
		current = next
	}
	return false
}

func plainGroupOnStationChain(group *Workspace) bool {
	return group.Kind == "group" && group.Status != StatusTrashed && group.Status != StatusMissing &&
		group.AssistantProgramState == nil && group.AssistantProjectLink == nil
}

// AssistantLinkHealingMove reports whether moving a linked project under
// newParentID heals its topology rather than changing it: the destination is
// inside the project's station and its current parent is not. A project that
// already sits inside its station (flat or nested) is never moved by healing,
// so a correctly nested song can't be pulled out to the Home this way. A move
// to the parent the project already has is a no-op and is allowed when that
// parent is inside the station, as it always was for a flat song.
func AssistantLinkHealingMove(lookup WorkspaceLookup, project *Workspace, currentParentID, newParentID string) bool {
	if project == nil || project.AssistantProjectLink == nil {
		return false
	}
	stationID := project.AssistantProjectLink.StationWorkspaceID
	if !ParentInsideStation(lookup, project.ID, newParentID, stationID) {
		return false
	}
	if newParentID == currentParentID {
		return true
	}
	return !ParentInsideStation(lookup, project.ID, currentParentID, stationID)
}
