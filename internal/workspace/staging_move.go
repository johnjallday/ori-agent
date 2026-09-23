package workspace

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/johnjallday/ori-agent/internal/logger"
)

// agentsFolderName and skillsFolderName are the root folders holding the
// user's agents and skills (config.AgentsFolderName, config.SkillsFolderName);
// they are not workspaces.
const (
	agentsFolderName = "Agents"
	skillsFolderName = "Skills"
)

// StagedMoveResult is what the first confirmation of a Workspace Directory
// moved out of the staging folder.
type StagedMoveResult struct {
	// Moved lists every workspace whose folder changed place, nested members
	// included, so callers can rebase path-keyed references kept elsewhere.
	Moved []MovedWorkspace
	// AgentsMoved reports that <staging>/Agents became <root>/Agents.
	AgentsMoved bool
	// SkillsMoved reports that <staging>/Skills became <root>/Skills.
	SkillsMoved bool
	// Warnings says what stayed behind, and why.
	Warnings []string
}

// stagedRename is os.Rename; tests replace it to take the cross-device path.
var stagedRename = os.Rename

// MoveStagedContent moves what a new user created before choosing a Workspace
// Directory — their agents, skills, and workspaces, kept under the staging
// folder until then — into the directory they confirmed. It runs once, on the
// first confirmation, before the workspace store is pointed at the new root.
//
// Nothing is ever overwritten. An Agents or Skills folder already in root
// (synced from another machine, say) wins and the staged one stays where it
// is; so does a folder with a staged workspace's name. A workspace with work
// in progress stays too. Each case is reported in Warnings.
func MoveStagedContent(staging, root string) StagedMoveResult {
	result := StagedMoveResult{Warnings: []string{}}
	if strings.TrimSpace(staging) == "" || strings.TrimSpace(root) == "" || samePath(staging, root) {
		return result
	}
	entries, err := os.ReadDir(staging)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			result.Warnings = append(result.Warnings, fmt.Sprintf("Could not read the staging folder %s: %v", staging, err))
		}
		return result
	}
	if err := os.MkdirAll(root, 0o750); err != nil {
		result.Warnings = append(result.Warnings, fmt.Sprintf("Could not prepare %s: %v", root, err))
		return result
	}

	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		src := filepath.Join(staging, entry.Name())
		dst := filepath.Join(root, entry.Name())
		if strings.EqualFold(entry.Name(), agentsFolderName) {
			result.AgentsMoved = result.moveOwnedFolder(src, filepath.Join(root, agentsFolderName), "an Agents folder", "agents")
			continue
		}
		if strings.EqualFold(entry.Name(), skillsFolderName) {
			result.SkillsMoved = result.moveOwnedFolder(src, filepath.Join(root, skillsFolderName), "a Skills folder", "skills")
			continue
		}
		result.moveWorkspace(src, dst)
	}
	return result
}

// moveOwnedFolder moves the staged Agents or Skills folder into root unless
// root already has one, and reports whether it moved.
func (r *StagedMoveResult) moveOwnedFolder(src, dst, folder, what string) bool {
	if _, err := os.Stat(dst); err == nil {
		r.Warnings = append(r.Warnings, fmt.Sprintf(
			"Your Workspace Directory already has %s, so it is used as it is. The %s you created before choosing it are still in %s.", folder, what, src))
		return false
	}
	if err := moveStagedDir(src, dst); err != nil {
		r.Warnings = append(r.Warnings, fmt.Sprintf("Your %s could not be moved from %s: %v", what, src, err))
		return false
	}
	return true
}

func (r *StagedMoveResult) moveWorkspace(src, dst string) {
	data, err := os.ReadFile(filepath.Join(src, WorkspaceConfigFile)) // #nosec G304 -- a folder directly under the app's staging root
	if err != nil {
		return // not a workspace
	}
	ws, err := FromJSON(data)
	if err != nil {
		r.Warnings = append(r.Warnings, fmt.Sprintf("%s could not be read, so it stayed in %s.", filepath.Base(src), filepath.Dir(src)))
		return
	}
	if subtreeHasActiveWork(src) {
		r.Warnings = append(r.Warnings, fmt.Sprintf(
			"%s has work in progress, so it stayed in %s. Move it after the work finishes.", ws.Name, filepath.Dir(src)))
		return
	}
	if _, err := os.Stat(dst); err == nil {
		r.Warnings = append(r.Warnings, fmt.Sprintf(
			"A folder named %s already exists in your Workspace Directory, so the workspace %s stayed in %s.", filepath.Base(dst), ws.Name, filepath.Dir(src)))
		return
	}
	if err := moveStagedDir(src, dst); err != nil {
		r.Warnings = append(r.Warnings, fmt.Sprintf("%s could not be moved: %v", ws.Name, err))
		return
	}
	moved, err := rebaseMovedWorkspaces(src, dst)
	if err != nil {
		logger.Warn("Moved workspace references could not all be updated", logger.Fields{"workspace": ws.Name, "error": err.Error()})
	}
	r.Moved = append(r.Moved, moved...)
}

// moveStagedDir is moveDir with a replaceable rename, so the cross-device
// fallback can be exercised.
func moveStagedDir(src, dst string) error {
	if err := stagedRename(src, dst); err == nil {
		return nil
	} else if !errors.Is(err, syscall.EXDEV) {
		return err
	}
	return copyThenRemove(src, dst)
}

// workspaceFilesUnder opens folder as an os.Root, so no symbolic link can lead
// outside it, and lists every workspace.json inside, relative to folder.
func workspaceFilesUnder(folder string) (*os.Root, []string, error) {
	root, err := os.OpenRoot(folder)
	if err != nil {
		return nil, nil, err
	}
	var files []string
	walkErr := fs.WalkDir(root.FS(), ".", func(rel string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && d.Name() == WorkspaceConfigFile {
			files = append(files, rel)
		}
		return nil
	})
	if walkErr != nil {
		_ = root.Close()
		return nil, nil, walkErr
	}
	return root, files, nil
}

// subtreeHasActiveWork reports whether any workspace in the folder tree has a
// task running or waiting on a choice; such a workspace is not moved.
func subtreeHasActiveWork(folder string) bool {
	root, files, err := workspaceFilesUnder(folder)
	if err != nil {
		return false
	}
	defer func() { _ = root.Close() }()
	for _, rel := range files {
		data, err := root.ReadFile(rel)
		if err != nil {
			continue
		}
		ws, err := FromJSON(data)
		if err != nil {
			continue
		}
		for _, task := range ws.Tasks {
			if task.Status == TaskStatusInProgress || task.Status == TaskStatusWaitingForChoice {
				return true
			}
		}
	}
	return false
}

// rebaseMovedWorkspaces rewrites the paths every workspace.json in the moved
// tree keeps about itself (directory references, workspace-files MCP roots, an
// absolute project path inside the folder) from src to dst, the way a
// workspace move does, and lists each workspace's old and new folder.
func rebaseMovedWorkspaces(src, dst string) ([]MovedWorkspace, error) {
	root, files, err := workspaceFilesUnder(dst)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	var moved []MovedWorkspace
	var errs []error
	for _, rel := range files {
		data, err := root.ReadFile(rel)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		ws, err := FromJSON(data)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if rebaseWorkspacePaths(ws, src, dst) {
			if err := rewriteInRoot(root, rel, ws); err != nil {
				errs = append(errs, err)
			}
		}
		folder := filepath.Dir(filepath.FromSlash(rel))
		moved = append(moved, MovedWorkspace{ID: ws.ID, OldPath: filepath.Join(src, folder), NewPath: filepath.Join(dst, folder)})
	}
	return moved, errors.Join(errs...)
}

// rewriteInRoot replaces one workspace.json inside root atomically.
func rewriteInRoot(root *os.Root, rel string, ws *Workspace) error {
	out, err := ws.ToJSON()
	if err != nil {
		return err
	}
	tmp := rel + ".tmp"
	if err := root.WriteFile(tmp, out, 0o600); err != nil {
		return err
	}
	if err := root.Rename(tmp, rel); err != nil {
		_ = root.Remove(tmp)
		return err
	}
	return nil
}

// rebaseWorkspacePaths moves every path under oldRoot to the same place under
// newRoot and reports whether anything changed.
func rebaseWorkspacePaths(ws *Workspace, oldRoot, newRoot string) bool {
	changed := false
	rebase := func(p string) string {
		if next, ok := rebasePath(p, oldRoot, newRoot); ok {
			changed = true
			return next
		}
		return p
	}
	for i := range ws.DirectoryReferences {
		ws.DirectoryReferences[i].Path = rebase(ws.DirectoryReferences[i].Path)
	}
	for i := range ws.MCPBindings {
		switch roots := ws.MCPBindings[i].Config["roots"].(type) {
		case []string:
			for j := range roots {
				roots[j] = rebase(roots[j])
			}
		case []any:
			for j := range roots {
				if s, ok := roots[j].(string); ok {
					roots[j] = rebase(s)
				}
			}
		}
	}
	if filepath.IsAbs(ws.ProjectPath) {
		ws.ProjectPath = rebase(ws.ProjectPath)
	}
	return changed
}

// rebasePath maps p from under oldRoot to under newRoot.
func rebasePath(p, oldRoot, newRoot string) (string, bool) {
	if strings.TrimSpace(p) == "" {
		return p, false
	}
	rel, err := filepath.Rel(filepath.Clean(oldRoot), filepath.Clean(p))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return p, false
	}
	return filepath.Join(newRoot, rel), true
}

// samePath reports whether two paths name the same folder.
func samePath(a, b string) bool {
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return filepath.Clean(absA) == filepath.Clean(absB)
}
