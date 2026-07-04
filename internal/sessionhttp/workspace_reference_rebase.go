package sessionhttp

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// folderRebaseIdentity carries the workspace fields the reference-rebase core
// needs to name and scope references, decoupling it from the two concrete
// workspace types (agentworkspace.Workspace on the import path,
// session.Workspace on the folder-sync path).
type folderRebaseIdentity struct {
	workspaceID string
	folderSlug  string
	name        string
	isGroup     bool
}

// rebaseWorkspaceFolderReferences rewrites directory references and MCP binding
// roots after a workspace folder moves from oldPath to newPath, returning the
// updated slices. ok is false when newPath is empty (nothing to do); the caller
// decides whether that is a silent no-op (import) or an error (folder sync).
//
// This is the single implementation shared by rebaseImportedWorkspaceFolderReferences
// (typed agentworkspace.Workspace) and updateManagedWorkspaceReferences
// (JSON-encoded session.Workspace mirror). Both store these as agentworkspace
// types — workspaceDirectoryReference is an alias of agentworkspace.DirectoryReference,
// and the session binding decoder already yields agentworkspace.MCPBinding.
func rebaseWorkspaceFolderReferences(
	id folderRebaseIdentity,
	refs []agentworkspace.DirectoryReference,
	bindings []agentworkspace.MCPBinding,
	oldPath, newPath string,
	now time.Time,
) ([]agentworkspace.DirectoryReference, []agentworkspace.MCPBinding, bool) {
	normalizedNew := cleanWorkspaceSyncPath(newPath)
	if normalizedNew == "" {
		return refs, bindings, false
	}

	normalizedOld := cleanWorkspaceSyncPath(oldPath)
	folderSlug := strings.TrimSpace(id.folderSlug)
	newBaseName := filepath.Base(normalizedNew)
	// Groups keep their linked folder and MCP roots scoped to their own content
	// directories; rewriting onto the folder root would expose member
	// sub-workspaces.
	defaultRefPath := defaultWorkspaceReferencePath(id.isGroup, normalizedNew)
	defaultRoots := defaultWorkspaceMCPRoots(id.isGroup, normalizedNew)
	refName := workspaceReferenceNameFor(id.folderSlug, id.name, normalizedNew)

	matchedReference := false
	for i := range refs {
		refPath := cleanWorkspaceSyncPath(refs[i].Path)
		if refPath == "" {
			continue
		}
		// References at or inside the moved folder keep their relative location
		// (a group's files/ stays files/).
		if rewritten, ok := rewriteWorkspaceContentPath(refPath, normalizedOld, normalizedNew); ok {
			refs[i].WorkspaceID = id.workspaceID
			if strings.TrimSpace(refs[i].Name) == "" {
				refs[i].Name = refName
			}
			refs[i].Path = rewritten
			refs[i].UpdatedAt = now
			matchedReference = true
			continue
		}
		if _, ok := rewriteWorkspaceContentPath(refPath, normalizedNew, normalizedNew); ok {
			// Already pointing into the new location.
			matchedReference = true
			continue
		}
		// Name-keyed rebind for stale references whose path matches neither
		// location.
		if (folderSlug != "" && strings.EqualFold(strings.TrimSpace(refs[i].Name), folderSlug)) ||
			(newBaseName != "" && strings.EqualFold(strings.TrimSpace(refs[i].Name), newBaseName)) {
			refs[i].WorkspaceID = id.workspaceID
			if strings.TrimSpace(refs[i].Name) == "" {
				refs[i].Name = refName
			}
			refs[i].Path = defaultRefPath
			refs[i].UpdatedAt = now
			matchedReference = true
		}
	}
	if !matchedReference {
		refs = append(refs, agentworkspace.DirectoryReference{
			ID:          uuid.New().String(),
			WorkspaceID: id.workspaceID,
			Name:        refName,
			Path:        defaultRefPath,
			X:           defaultDirectoryReferenceX,
			Y:           defaultDirectoryReferenceY,
			CreatedAt:   now,
			UpdatedAt:   now,
		})
	}
	refs = compactDirectoryReferences(refs)

	matchedBinding := false
	for i := range bindings {
		if strings.EqualFold(strings.TrimSpace(bindings[i].Alias), workspaceFilesMCPAlias) ||
			workspaceBindingHasRoot(bindings[i].Config, normalizedOld) {
			if bindings[i].Config == nil {
				bindings[i].Config = make(map[string]any)
			}
			bindings[i].Config["roots"] = rewriteWorkspaceBindingRoots(bindings[i].Config["roots"], normalizedOld, normalizedNew, defaultRoots)
			bindings[i].UpdatedAt = now
			bindings[i].Enabled = true
			matchedBinding = true
		}
	}
	if !matchedBinding {
		bindings = append(bindings, newWorkspaceFilesMCPBinding(defaultRoots, now))
	}

	return refs, bindings, true
}

// workspaceReferenceNameFor picks the display name for a directory reference:
// the folder slug, else the workspace name, else the path's base name.
func workspaceReferenceNameFor(folderSlug, workspaceName, path string) string {
	if name := strings.TrimSpace(folderSlug); name != "" {
		return name
	}
	if name := strings.TrimSpace(workspaceName); name != "" {
		return name
	}
	return filepath.Base(path)
}

// compactDirectoryReferences removes references that resolve to the same
// filesystem path, keeping the first and backfilling a blank name from a later
// duplicate.
func compactDirectoryReferences(refs []agentworkspace.DirectoryReference) []agentworkspace.DirectoryReference {
	if len(refs) < 2 {
		return refs
	}

	seen := make(map[string]int, len(refs))
	compact := make([]agentworkspace.DirectoryReference, 0, len(refs))
	for _, ref := range refs {
		key := cleanWorkspaceSyncPath(ref.Path)
		if key == "" {
			compact = append(compact, ref)
			continue
		}
		if existingIndex, ok := seen[key]; ok {
			if strings.TrimSpace(compact[existingIndex].Name) == "" && strings.TrimSpace(ref.Name) != "" {
				compact[existingIndex].Name = ref.Name
			}
			continue
		}
		seen[key] = len(compact)
		compact = append(compact, ref)
	}
	return compact
}

// defaultDirectoryReferenceX and defaultDirectoryReferenceY are the canvas
// coordinates a freshly-created workspace directory reference is placed at.
const (
	defaultDirectoryReferenceX = 400
	defaultDirectoryReferenceY = 300
)
