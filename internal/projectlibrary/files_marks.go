package projectlibrary

import (
	"path/filepath"
	"slices"

	"github.com/johnjallday/ori-agent/internal/directoryid"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// FilesMarks are optional annotations, never directory entries or grants. A
// client can display one only on a row returned by the ordinary Files read.
type FilesMarks struct {
	Projects map[string]string `json:"projects"` // opaque listing stamp -> saved entry ID
	Songs    map[string]string `json:"songs"`    // song workspace -> authoritative project link
	Feeds    map[string]string `json:"feeds"`    // fed station -> its anchor link
	CanSetUp bool              `json:"can_set_up"`
}

func (s *Store) ReadFilesMarks(scope Scope, home *workspace.Workspace) (FilesMarks, error) {
	view := FilesMarks{Projects: map[string]string{}, Songs: map[string]string{}, Feeds: map[string]string{}}
	state, err := s.home(scope, home)
	if err != nil {
		return view, err
	}
	members, err := workspace.GroupDescendants(s.workspaces, scope.OwnerUserID, scope.HomeID)
	if err != nil {
		return view, err
	}
	// Same fed stations as the map. Exact links may exist before the shelf.
	feeds := mapFolderRows(scope, members, state).Folders
	for _, feed := range feeds {
		view.Feeds[feed.WorkspaceID] = members[feed.WorkspaceID].AnchorDirectoryReferenceID()
	}
	if len(feeds) == 0 {
		return view, nil
	}
	for _, id := range state.LinkedProjectIDs {
		member := members[id]
		if member == nil {
			continue
		}
		_, ref, _, _, err := s.exactLinkedChild(scope, state, id)
		if err != nil {
			continue
		}
		view.Songs[id] = ref.ID
	}
	if len(state.ProjectLibrary) == 0 {
		return view, nil
	}
	doc, err := decodeDocument(state.ProjectLibrary, scope)
	if err != nil || !inactiveRootsValid(state, doc) {
		return view, ErrCorrupt
	}
	view.CanSetUp = s.providerWritable(scope, home)
	for _, feed := range feeds {
		for _, root := range doc.Roots {
			if root.Path != feed.anchor || root.RevokedAt != nil || slices.Contains(state.ProjectLibraryInactiveRoots, root.ID) {
				continue
			}
			addFilesProjects(view.Projects, feed.WorkspaceID, members[feed.WorkspaceID].AnchorDirectoryReferenceID(), root, doc.Entries)
		}
	}
	return view, nil
}

func addFilesProjects(marks map[string]string, owner, link string, root Root, entries []Entry) {
	for _, entry := range entries {
		for _, obs := range entry.Observations {
			rel := filepath.ToSlash(obs.RelativeFolder)
			if obs.RootID != root.ID || (obs.Availability != "available" && obs.Availability != "ambiguous") {
				continue
			}
			if clean, err := workspace.ContainedRelativePath(rel); err != nil || clean != rel {
				continue
			}
			stamp := directoryid.ListingStamp(owner, link, rel, root.FileIdentity, obs.FileIdentity)
			if stamp == "" {
				continue
			}
			// Conflicting records or an existing link are not a setup target.
			if old, ok := marks[stamp]; entry.Link != nil || (ok && old != entry.ID) {
				marks[stamp] = ""
			} else {
				marks[stamp] = entry.ID
			}
		}
	}
}
