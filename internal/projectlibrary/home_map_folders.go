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
	RootID      string `json:"root_id,omitempty"`
	ToSetUp     *int   `json:"to_set_up,omitempty"`
	Partial     bool   `json:"partial,omitempty"`
	anchor      string
}

type HomeMapFolders struct {
	Folders   []MapFolder `json:"folders"`
	Outside   int         `json:"outside"`
	CanAdd    bool        `json:"can_add_folder"`
	Remainder *int        `json:"remainder,omitempty"`
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

// addShelfCounts matches only the connected root granted for a folder, never
// a display name or an inferred child path. The sheet accepts that same ID.
func (v *HomeMapFolders) addShelfCounts(doc Document, state *workspace.AssistantProgramState, evidence setupEvidence) {
	byPath := map[string]string{}
	for _, root := range doc.Roots {
		if digestRootActive(doc, state, root.ID) {
			byPath[root.Path] = root.ID
		}
	}
	fed := map[string]string{}
	for i := range v.Folders {
		f := &v.Folders[i]
		if id := byPath[f.anchor]; id != "" {
			f.RootID = id
			fed[id] = f.anchor
		}
	}
	tally := countShelfSetup(doc, state, evidence, fed)
	v.Remainder = &tally.remainder
	// A complete scan of one subfolder does not make an incomplete listing
	// complete. Keep the latest saved result per scope of each connected root.
	type key struct{ root, scope string }
	latest := map[key]Scan{}
	for _, scan := range doc.Scans {
		k := key{scan.RootID, scan.Scope}
		if prev, ok := latest[k]; !ok || !scan.StartedAt.Before(prev.StartedAt) {
			latest[k] = scan
		}
	}
	for i := range v.Folders {
		f := &v.Folders[i]
		if f.RootID == "" {
			continue
		}
		n := tally.byRoot[f.RootID]
		f.ToSetUp = &n
		f.Partial = latest[key{f.RootID, ""}].Status != "complete"
		for k, scan := range latest {
			if k.root == f.RootID && scan.Status != "complete" {
				f.Partial = true
				break
			}
		}
	}
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
	view := mapFolderRows(scope, members, state)
	if len(view.Folders) == 0 || len(state.ProjectLibrary) == 0 || s.installed == nil {
		return view, nil
	}
	doc, err := decodeDocument(state.ProjectLibrary, scope)
	if err != nil || !inactiveRootsValid(state, doc) {
		return HomeMapFolders{}, ErrCorrupt
	}
	installed, listErr := s.installed.List()
	if listErr != nil {
		return view, nil
	}
	evidence := s.setupEvidence(scope, state, installed, listErr)
	view.addShelfCounts(doc, state, evidence)
	return view, nil
}
