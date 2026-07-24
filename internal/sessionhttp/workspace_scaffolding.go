package sessionhttp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/session"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// workspaceFilesMCPAlias is the alias of the filesystem MCP binding that every
// workspace (groups included) gets provisioned with.
const workspaceFilesMCPAlias = "workspace-files"

// groupContentDirs holds the group-owned content directories that scaffolding
// exposes. sub-workspaces/ (member folders) is deliberately absent: group
// agents and the linked-folder UI must never see member content.
type groupContentDirs struct {
	files string
	notes string
}

func (d groupContentDirs) mcpRoots() []string {
	return []string{d.files, d.notes}
}

// ensureGroupContentDirs creates the standard directory set inside a group
// folder: sub-workspaces/ for members plus files/ and notes/ for the group's
// own content.
func ensureGroupContentDirs(folderPath string) (groupContentDirs, error) {
	dirs := groupContentDirs{
		files: filepath.Join(folderPath, agentworkspace.FilesDir),
		notes: filepath.Join(folderPath, agentworkspace.NotesDir),
	}
	for _, dir := range []string{
		filepath.Join(folderPath, agentworkspace.SubWorkspacesDir),
		dirs.files,
		dirs.notes,
	} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return dirs, err
		}
	}
	return dirs, nil
}

// newWorkspaceDirectoryReference builds the default linked-folder reference
// provisioned alongside a workspace.
func newWorkspaceDirectoryReference(ws *session.Workspace, referencePath string, now time.Time) workspaceDirectoryReference {
	return workspaceDirectoryReference{
		ID:          uuid.New().String(),
		WorkspaceID: ws.ID,
		Name:        ws.FolderSlug,
		Path:        referencePath,
		X:           400,
		Y:           300,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

// newWorkspaceFilesMCPBinding builds the auto-provisioned filesystem MCP
// binding rooted at the given directories.
func newWorkspaceFilesMCPBinding(roots []string, now time.Time) agentworkspace.MCPBinding {
	return agentworkspace.MCPBinding{
		ID:         uuid.New().String(),
		ServerName: "filesystem",
		Alias:      workspaceFilesMCPAlias,
		Enabled:    true,
		Config: map[string]any{
			"roots": append([]string(nil), roots...),
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// provisionWorkspaceScaffolding seeds a newly created workspace with its
// default directory reference (referencePath) and workspace-files MCP binding
// (mcpRoots), persists both to the session store, and mirrors them into the
// folder store's workspace.json. Concrete workspaces are scoped to the whole
// workspace folder; groups are scoped to their own files/ and notes/ so member
// sub-workspaces stay hidden.
func (h *Handler) provisionWorkspaceScaffolding(ctx context.Context, ws *session.Workspace, folderWS *agentworkspace.Workspace, referencePath string, mcpRoots []string) {
	now := time.Now()

	dirRef := newWorkspaceDirectoryReference(ws, referencePath, now)
	if data, err := json.Marshal([]workspaceDirectoryReference{dirRef}); err == nil {
		ws.DirectoryReferencesJSON = data
	}
	setWorkspacePrimaryDirectoryID(ws, dirRef.ID)

	mcpBinding := newWorkspaceFilesMCPBinding(mcpRoots, now)
	if data, err := json.Marshal([]agentworkspace.MCPBinding{mcpBinding}); err == nil {
		ws.MCPBindingsJSON = data
	}

	ws.UpdatedAt = now
	if err := h.store.UpdateWorkspace(ctx, ws); err != nil {
		logger.Warn("Failed to set initial workspace config", logger.Fields{"id": ws.ID, "error": err})
	}

	// Resync workspace.json to include the directory reference and MCP binding
	folderWS.SharedData = ws.SharedData
	folderWS.AgentInstances = toWorkspaceAgentInstances(ws.AgentInstances)
	folderWS.DirectoryReferences = []agentworkspace.DirectoryReference{
		{
			ID:          dirRef.ID,
			WorkspaceID: dirRef.WorkspaceID,
			Name:        dirRef.Name,
			Path:        dirRef.Path,
			X:           dirRef.X,
			Y:           dirRef.Y,
			CreatedAt:   dirRef.CreatedAt,
			UpdatedAt:   dirRef.UpdatedAt,
		},
	}
	folderWS.MCPBindings = []agentworkspace.MCPBinding{mcpBinding}
	folderWS.UpdatedAt = now
	if err := h.workspaceStore.Save(folderWS); err != nil {
		logger.Warn("Failed to resync workspace.json after creation", logger.Fields{"id": ws.ID, "error": err})
	}

	// Every newly created managed workspace gets an Ori-managed BACKLOG.md
	// (PRD workspace-backlog FR67). A collision (a pre-existing unmanaged
	// file at the target path) is left untouched — vanishingly unlikely for
	// a brand-new folder, but scaffolding must never overwrite user content.
	if _, err := agentworkspace.NewFileBacklogSynchronizer(h.workspaceStore).EnsureBacklogMarkdownFile(ws.ID); err != nil {
		logger.Warn("Failed to create BACKLOG.md during workspace scaffolding", logger.Fields{"id": ws.ID, "error": err})
	}
}

// rewriteWorkspaceContentPath maps path to its new location when it sits at or
// inside oldRoot after that root moved to newRoot. Paths outside oldRoot (or
// an empty oldRoot) report false.
func rewriteWorkspaceContentPath(path, oldRoot, newRoot string) (string, bool) {
	if path == "" || oldRoot == "" || newRoot == "" {
		return "", false
	}
	if path == oldRoot {
		return newRoot, true
	}
	rel, err := filepath.Rel(oldRoot, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	if rel == "." {
		return newRoot, true
	}
	return filepath.Join(newRoot, rel), true
}

// rewriteWorkspaceBindingRoots maps every MCP root at or inside oldRoot to the
// same relative location under newRoot — keeping scoped roots (a group's
// files/ and notes/) scoped across moves instead of collapsing them onto the
// folder root. Roots already inside newRoot are kept; anything else is stale
// (e.g. a path from the machine a workspace was exported from) and dropped.
// Results are deduplicated; when nothing survives, fallback is returned.
func rewriteWorkspaceBindingRoots(raw any, oldRoot, newRoot string, fallback []string) []string {
	var roots []string
	switch typed := raw.(type) {
	case []string:
		roots = typed
	case []any:
		for _, value := range typed {
			if text, ok := value.(string); ok {
				roots = append(roots, text)
			}
		}
	}

	rewritten := make([]string, 0, len(roots))
	seen := make(map[string]struct{}, len(roots))
	add := func(path string) {
		path = cleanWorkspaceSyncPath(path)
		if path == "" {
			return
		}
		key := strings.ToLower(path)
		if _, dup := seen[key]; dup {
			return
		}
		seen[key] = struct{}{}
		rewritten = append(rewritten, path)
	}

	for _, root := range roots {
		cleaned := cleanWorkspaceSyncPath(root)
		if cleaned == "" {
			continue
		}
		if mapped, ok := rewriteWorkspaceContentPath(cleaned, oldRoot, newRoot); ok {
			add(mapped)
			continue
		}
		if _, ok := rewriteWorkspaceContentPath(cleaned, newRoot, newRoot); ok {
			add(cleaned)
		}
	}

	if len(rewritten) == 0 {
		return append([]string(nil), fallback...)
	}
	return rewritten
}

// defaultWorkspaceReferencePath returns where a workspace's default linked
// folder should point after its folder lands at folderPath: groups expose only
// their own files/, everything else the folder root.
func defaultWorkspaceReferencePath(isGroup bool, folderPath string) string {
	if isGroup {
		return filepath.Join(folderPath, agentworkspace.FilesDir)
	}
	return folderPath
}

// defaultWorkspaceMCPRoots returns the default workspace-files roots for a
// workspace whose folder lives at folderPath.
func defaultWorkspaceMCPRoots(isGroup bool, folderPath string) []string {
	if isGroup {
		return []string{
			filepath.Join(folderPath, agentworkspace.FilesDir),
			filepath.Join(folderPath, agentworkspace.NotesDir),
		}
	}
	return []string{folderPath}
}
