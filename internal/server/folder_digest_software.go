package server

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// softwareExistingProjects returns the user's active workspaces whose saved
// primary folder is exactly folderPath, the canonical directory of a selection
// the server holds. It reads saved bindings only: no file in the folder is
// opened and no Git metadata is consulted, so a sibling worktree or a clone of
// the same repository, which is a different directory, never matches.
//
// A workspace this feature set up also recorded the directory's witness. When
// the path matches and the witness does not, a different directory now sits at
// that path; the workspace is reported as replaced so the user can decide, and
// is never treated as the same project. A workspace linked some other way has
// no witness and matches on the canonical path alone.
//
// Group workspaces, trashed or missing workspaces, and other owners' workspaces
// are never returned. A folder linked only as a supporting source is not the
// workspace's project and does not match.
func softwareExistingProjects(files *workspace.FileStore, userID, folderPath, identity string) ([]personalassistant.FolderExistingProject, error) {
	if files == nil {
		return nil, errSetupUnavailable
	}
	want := filepath.Clean(strings.TrimSpace(folderPath))
	if want == "" || !filepath.IsAbs(want) {
		return nil, errSetupUnavailable
	}
	var found []personalassistant.FolderExistingProject
	for _, ws := range files.CachedWorkspaces() {
		if ws == nil || ws.Kind == "group" || ws.GetStatus() != workspace.StatusActive ||
			!folderWorkspaceOwnedBy(ws, userID) || strings.TrimSpace(ws.FolderSlug) == "" {
			continue
		}
		primary, _ := ws.SharedData[projecttemplates.PrimaryDirectoryIDKey].(string)
		if strings.TrimSpace(primary) == "" {
			continue
		}
		ref, err := ws.GetDirectoryReference(primary)
		if err != nil || ref == nil || ref.Purpose != "" || !sameCanonicalDirectory(ref.Path, want) {
			continue
		}
		project := personalassistant.FolderExistingProject{
			WorkspaceID: ws.ID, Name: ws.Name, Route: "/workspaces/" + ws.FolderSlug,
		}
		if binding, bindErr := workspace.GetSoftwareProjectBinding(ws.SharedData); bindErr == nil && binding != nil && binding.DirectoryID == primary {
			project.Software = true
			project.Replaced = binding.DirectoryIdentity != identity
		}
		found = append(found, project)
	}
	sort.SliceStable(found, func(i, j int) bool {
		if found[i].Name != found[j].Name {
			return found[i].Name < found[j].Name
		}
		return found[i].WorkspaceID < found[j].WorkspaceID
	})
	return found, nil
}

// sameCanonicalDirectory reports whether a saved linked-directory path names
// the canonical directory want. A saved path that reaches the same directory
// through a symbolic link is the same directory. A saved path that no longer
// resolves is compared as written, so a missing folder cannot match a
// different one.
func sameCanonicalDirectory(saved, want string) bool {
	saved = filepath.Clean(strings.TrimSpace(saved))
	if saved == "" || !filepath.IsAbs(saved) {
		return false
	}
	if saved == want {
		return true
	}
	resolved, err := filepath.EvalSymlinks(saved)
	return err == nil && filepath.Clean(resolved) == want
}
