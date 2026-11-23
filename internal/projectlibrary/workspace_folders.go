package projectlibrary

import (
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// WorkspaceFolders is label-only, not a library search. A single snapshot
// bounded by the library's document/entry limits avoids joining partial pages
// (or exposing unrelated library records to a child group).
type WorkspaceFolders struct {
	Revision int64                `json:"revision"`
	Rows     []WorkspaceFolderRow `json:"rows"`
}

type WorkspaceFolderRow struct {
	WorkspaceID   string        `json:"workspace_id"`
	FolderSection FolderSection `json:"folder_section"`
}

func ownedVisibleWorkspace(current *workspace.Workspace, id, owner string) bool {
	return current != nil && current.ID == id && current.OwnerUserID == owner &&
		current.Status != workspace.StatusTrashed && current.Status != workspace.StatusMissing
}

// musicFolderScope walks upwards to the nearest station. Only ordinary group
// intermediates are allowed. A caller cannot pick the Home or skip a parent.
func musicFolderScope(lookup workspace.WorkspaceLookup, owner, groupID string) (Scope, error) {
	provider, ok := reviewedintegration.HomeProviderFor("music-project-management")
	if !ok {
		return Scope{}, ErrUnavailable
	}
	seen := map[string]bool{}
	for id, depth := groupID, 0; id != "" && depth <= workspace.MaxNestingDepth && !seen[id]; depth++ {
		seen[id] = true
		group, err := lookup(id)
		if err != nil || !ownedVisibleWorkspace(group, id, owner) || group.Kind != "group" || group.AssistantProjectLink != nil {
			break
		}
		if group.AssistantProgramState != nil {
			state := group.GetAssistantProgramState()
			if state != nil {
				key := state.Key.Normalize()
				if key.OwnerUserID == owner && key.PluginID == provider.PluginID && key.ProgramID == provider.ProgramID {
					return Scope{OwnerUserID: owner, HomeID: id, ProviderID: key.PluginID, ProgramID: key.ProgramID}, nil
				}
			}
			break
		}
		id = group.ParentID
	}
	return Scope{}, ErrUnavailable
}

// ReadWorkspaceFolders projects saved source evidence for a verified group.
// The HTTP caller supplies the authenticated owner, not a value from the URL.
func (s *Store) ReadWorkspaceFolders(owner, groupID string) (WorkspaceFolders, error) {
	if s == nil || s.workspaces == nil || owner == "" || groupID == "" {
		return WorkspaceFolders{}, ErrUnavailable
	}
	// Cache checked groups for this read: many projects can share a chain.
	groups := map[string]*workspace.Workspace{}
	lookup := func(id string) (*workspace.Workspace, error) {
		if group, ok := groups[id]; ok {
			return group, nil
		}
		group, err := s.workspaces.Get(id)
		if err != nil || !ownedVisibleWorkspace(group, id, owner) || group.Kind != "group" {
			return nil, ErrUnavailable
		}
		if !s.folderGroupTopologyAgrees(group) {
			return nil, ErrMirrorDiverged
		}
		groups[id] = group
		return group, nil
	}
	scope, err := musicFolderScope(lookup, owner, groupID)
	if err != nil {
		return WorkspaceFolders{}, err
	}
	doc, state, err := s.readSnapshot(scope)
	if err != nil {
		return WorkspaceFolders{}, err
	}
	linked := make(map[string]bool, len(state.LinkedProjectIDs))
	for _, id := range state.LinkedProjectIDs {
		linked[id] = true
	}
	roots := make(map[string]Root, len(doc.Roots))
	for _, root := range doc.Roots {
		roots[root.ID] = root
	}
	inactive := make(map[string]bool, len(state.ProjectLibraryInactiveRoots))
	for _, id := range state.ProjectLibraryInactiveRoots {
		inactive[id] = true
	}
	counts := make(map[string]int, len(linked))
	for _, entry := range doc.Entries {
		if entry.Link != nil && linked[entry.Link.WorkspaceID] {
			counts[entry.Link.WorkspaceID]++
		}
	}
	view := WorkspaceFolders{Revision: doc.Revision, Rows: []WorkspaceFolderRow{}}
	for _, entry := range doc.Entries {
		exact := entry.Link
		if exact == nil || counts[exact.WorkspaceID] != 1 || exact.WorkspaceID == groupID ||
			exact.LinkID != workspace.AssistantProjectLinkID(scope.HomeID, exact.WorkspaceID) {
			continue
		}
		// Multiple takes in one observed directory aren't ambiguous ancestry.
		// observedSection still refuses competing roots or unusable evidence.
		section := observedSection(entry, roots, inactive, "")
		if section == nil {
			continue
		}
		child, err := s.workspaces.Get(exact.WorkspaceID)
		if err != nil || child == nil || child.Kind == "group" || child.AssistantProgramState != nil ||
			!workspace.WorkspaceInsideStation(lookup, child, groupID) || !s.matchesCatalogProject(scope, child, *exact) ||
			workspace.AssistantProjectLinkIsResource(child.GetAssistantProjectLink()) {
			continue
		}
		view.Rows = append(view.Rows, WorkspaceFolderRow{WorkspaceID: child.ID, FolderSection: *section})
	}
	return view, nil
}

func (s *Store) folderGroupTopologyAgrees(group *workspace.Workspace) bool {
	mirror, ok := s.workspaces.(workspace.MirrorWorkspaceProvider)
	if !ok {
		return true
	}
	folder, mirrored, err := mirror.GetMirrorWorkspace(group.ID)
	if err != nil {
		return false
	}
	return !mirrored || (ownedVisibleWorkspace(folder, group.ID, group.OwnerUserID) &&
		folder.Kind == group.Kind && folder.ParentID == group.ParentID && folder.Status == group.Status &&
		(folder.AssistantProgramState == nil) == (group.AssistantProgramState == nil) &&
		(folder.AssistantProjectLink == nil) == (group.AssistantProjectLink == nil))
}
