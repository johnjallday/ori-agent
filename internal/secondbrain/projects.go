package secondbrain

import (
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

const (
	// FolderProjects is where the user keeps things with an end date; Ori never
	// writes there, and only lists it so a project can become a workspace.
	FolderProjects = "Projects"
	// MaxProjects bounds the listing.
	MaxProjects = 50

	maxProjectsListed = 500
)

// Project is one folder directly under Projects: a candidate for an anchored
// workspace, offered only when the user asks (FR18).
type Project struct {
	// Name is the folder's name, which is also the workspace's suggested name.
	Name string
	// Rel is "Projects/<name>".
	Rel string
	// Path is the folder's absolute path on this machine.
	Path string
}

// ListProjects lists the folders directly under Projects, by name, at most
// MaxProjects. It reads directory entries only: no note is opened, a link or
// hidden or non-folder entry is skipped, and a missing Projects is no
// projects, not an error.
func ListProjects(root string) ([]Project, error) {
	pinned, err := pinFolder(root)
	if err != nil {
		return nil, err
	}
	defer func() { _ = pinned.Close() }()
	info, err := pinned.Lstat(FolderProjects)
	if err != nil || !info.IsDir() {
		return nil, nil
	}
	dir, err := pinned.OpenRoot(FolderProjects)
	if err != nil {
		return nil, nil
	}
	defer func() { _ = dir.Close() }()
	listing, err := dir.Open(".")
	if err != nil {
		return nil, nil
	}
	entries, err := listing.ReadDir(-1)
	_ = listing.Close()
	if err != nil {
		return nil, nil
	}
	var projects []Project
	for index, entry := range entries {
		if index == maxProjectsListed {
			break
		}
		name := entry.Name()
		if entry.Type()&fs.ModeSymlink != 0 || !entry.IsDir() || !validProjectName(name) {
			continue
		}
		projects = append(projects, Project{Name: name, Rel: FolderProjects + "/" + name, Path: filepath.Join(root, FolderProjects, name)})
	}
	sort.Slice(projects, func(i, j int) bool {
		return strings.ToLower(projects[i].Name) < strings.ToLower(projects[j].Name)
	})
	if len(projects) > MaxProjects {
		projects = projects[:MaxProjects]
	}
	return projects, nil
}

// validProjectName keeps a folder out of the list when its name is hidden or
// holds a control character, since the name travels in a link.
func validProjectName(name string) bool {
	return name != "" && !strings.HasPrefix(name, ".") && !skippedFolders[name] &&
		strings.IndexFunc(name, unicode.IsControl) < 0 && !strings.ContainsAny(name, `/\`)
}
