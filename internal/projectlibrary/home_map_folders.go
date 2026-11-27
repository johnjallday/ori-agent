package projectlibrary

import (
	"os"
	"slices"
	"sort"
	"strings"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// MapFolder identifies a station on the map. Its anchor is server-only: it is
// neither a browser locator nor permission to open or scan the source.
type MapFolder struct {
	WorkspaceID string `json:"workspace_id"`
	Name        string `json:"name"`
	Songs       int    `json:"songs"`
	Missing     bool   `json:"missing,omitempty"`
	anchor      string
}

type HomeMapFolders struct {
	Folders []MapFolder `json:"folders"`
	Outside int         `json:"outside"`
	CanAdd  bool        `json:"can_add_folder"`
}

// homeMapMembers hydrates only descendants of this owner and Home. A listing
// projection cannot decide which rows are anchored or linked projects.
func (s *Store) homeMapMembers(scope Scope) (map[string]*workspace.Workspace, error) {
	listed, err := workspace.ListActiveSummaries(s.workspaces)
	if err != nil {
		return nil, err
	}
	children := map[string][]string{}
	for _, row := range listed {
		if row.OwnerUserID == scope.OwnerUserID {
			children[row.ParentID] = append(children[row.ParentID], row.ID)
		}
	}
	members := map[string]*workspace.Workspace{}
	parents := []string{scope.HomeID}
	for len(parents) > 0 {
		id := parents[0]
		parents = parents[1:]
		for _, child := range children[id] {
			if _, seen := members[child]; seen || child == scope.HomeID {
				continue
			}
			ws, err := s.workspaces.Get(child)
			if err != nil || !ownedVisibleWorkspace(ws, child, scope.OwnerUserID) || ws.ParentID != id {
				return nil, ErrUnavailable
			}
			members[child] = ws
			if ws.Kind == "group" {
				parents = append(parents, child)
			}
		}
	}
	return members, nil
}

func mapFolderRows(scope Scope, members map[string]*workspace.Workspace, state *workspace.AssistantProgramState) HomeMapFolders {
	view := HomeMapFolders{Folders: []MapFolder{}}
	lookup := workspace.LookupInWorkspaceMap(members)
	indices := map[string]int{}
	for _, ws := range members {
		anchor := workspace.StationFolderAnchor(lookup, ws, scope.HomeID)
		if anchor == "" {
			continue
		}
		info, err := os.Stat(anchor) // #nosec G304 G703 -- existence-only check of the owner's stored anchor; no file contents or path returned.
		indices[ws.ID] = len(view.Folders)
		view.Folders = append(view.Folders, MapFolder{WorkspaceID: ws.ID, Name: ws.Name, anchor: anchor, Missing: err != nil || !info.IsDir()})
	}
	for _, ws := range members {
		if _, ok := indices[ws.ID]; ok {
			continue
		}
		inside := false
		for parent := members[ws.ParentID]; parent != nil; parent = members[parent.ParentID] {
			if index, ok := indices[parent.ID]; ok {
				inside = true
				link := ws.AssistantProjectLink
				if link != nil && link.StationWorkspaceID == scope.HomeID && slices.Contains(state.LinkedProjectIDs, ws.ID) && !workspace.AssistantProjectLinkIsResource(link) {
					view.Folders[index].Songs++
				}
				break
			}
		}
		if !inside {
			view.Outside++
		}
	}
	sort.Slice(view.Folders, func(i, j int) bool {
		a, b := view.Folders[i], view.Folders[j]
		if strings.EqualFold(a.Name, b.Name) {
			return a.WorkspaceID < b.WorkspaceID
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
	return view
}

// MapFolders reads the map even when the shelf has not been initialized.
func (s *Store) MapFolders(scope Scope, home *workspace.Workspace) (HomeMapFolders, error) {
	state, err := s.home(scope, home)
	if err != nil {
		return HomeMapFolders{}, err
	}
	members, err := s.homeMapMembers(scope)
	if err != nil {
		return HomeMapFolders{}, err
	}
	return mapFolderRows(scope, members, state), nil
}
