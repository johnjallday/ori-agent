package workspace

import (
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	// AdoptedProjectFolder is the folder a workspace was created from or
	// connected to.
	AdoptedProjectFolder = "project"
	// AdoptedLibraryRoot is an active project-library root of a Home.
	AdoptedLibraryRoot = "library_root"

	maxAdoptedLibraryRoots = 64
)

// primaryDirectoryKeys mirror projecttemplates.PrimaryDirectoryIDKey and
// ProjectDirectoryIDKey; that package imports this one, so the names are
// repeated here and a projecttemplates test keeps them in step.
var primaryDirectoryKeys = []string{"primary_directory_id", "project_directory_id"}

// AdoptedFolder is a folder the user showed Ori as this workspace's own work.
// Path is the stored absolute path: server-only, never projected to a client,
// a prompt or a brief.
type AdoptedFolder struct {
	Kind string
	Path string
}

// AdoptedFolders lists the workspace's adopted folders from stored metadata
// alone, without touching the filesystem: the directory reference its project
// entry locator or primary project directory names, and a Home's active
// library roots. References with a Purpose (capability-owned) or recorded by
// File Janitor are never adopted, nor is any reference no project entry names.
func (w *Workspace) AdoptedFolders() []AdoptedFolder {
	if w == nil {
		return nil
	}
	w.mu.RLock()
	named := []string{}
	if locator, err := GetProjectEntryLocator(w.SharedData); err == nil && locator != nil && locator.Kind == ProjectEntryDirectoryReference {
		named = append(named, locator.DirectoryReferenceID)
	}
	for _, key := range primaryDirectoryKeys {
		if value, ok := w.SharedData[key].(string); ok {
			named = append(named, strings.TrimSpace(value))
		}
	}
	refs := append([]DirectoryReference(nil), w.DirectoryReferences...)
	id := w.ID
	w.mu.RUnlock()

	var out []AdoptedFolder
	seen := map[string]bool{}
	add := func(kind, path string) {
		if !adoptablePath(path) || seen[path] {
			return
		}
		seen[path] = true
		out = append(out, AdoptedFolder{Kind: kind, Path: path})
	}

	janitor, hasJanitor := w.GetInstalledCapability(CapabilityFileJanitor)
	for _, refID := range named {
		if refID == "" {
			continue
		}
		for _, ref := range refs {
			if ref.ID != refID || strings.TrimSpace(ref.Purpose) != "" || (ref.WorkspaceID != "" && ref.WorkspaceID != id) {
				continue
			}
			if hasJanitor {
				if _, recorded := janitor.Owns(ResourceDirectoryReference, ref.ID); recorded {
					continue
				}
			}
			add(AdoptedProjectFolder, ref.Path)
		}
	}

	if w.GetAssistantProjectLink() == nil {
		roots := activeLibraryRoots(w.GetAssistantProgramState())
		sort.Strings(roots)
		for _, path := range roots {
			add(AdoptedLibraryRoot, path)
		}
	}
	return out
}

// activeLibraryRoots decodes only the root list of a Home's metadata-only
// project-library document: roots neither revoked nor frozen inactive. A
// malformed or oversized document yields none rather than a guess.
func activeLibraryRoots(state *AssistantProgramState) []string {
	if state == nil || len(state.ProjectLibrary) == 0 || len(state.ProjectLibrary) > 8<<20 {
		return nil
	}
	var document struct {
		Roots []struct {
			ID        string     `json:"id"`
			Path      string     `json:"path"`
			RevokedAt *time.Time `json:"revoked_at"`
		} `json:"roots"`
	}
	if err := json.Unmarshal(state.ProjectLibrary, &document); err != nil || len(document.Roots) > maxAdoptedLibraryRoots {
		return nil
	}
	inactive := map[string]bool{}
	for _, rootID := range state.ProjectLibraryInactiveRoots {
		inactive[rootID] = true
	}
	var out []string
	for _, root := range document.Roots {
		if root.ID == "" || root.RevokedAt != nil || inactive[root.ID] {
			continue
		}
		out = append(out, root.Path)
	}
	return out
}

func adoptablePath(path string) bool {
	return path != "" && path != "/" && filepath.IsAbs(path) && filepath.Clean(path) == path
}
