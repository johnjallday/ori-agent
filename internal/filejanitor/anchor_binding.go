package filejanitor

import (
	"path/filepath"
	"strings"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// defaultAnchorBinding is the one creation-time grant File Janitor replaces.
// User-added grants and a Janitor linked to a different folder are untouched.
func defaultAnchorBinding(ws *workspace.Workspace, binding workspace.MCPBinding, root string) bool {
	if !ws.IsAnchored() || binding.Alias != workspace.AnchorFolderMCPAlias || !strings.EqualFold(binding.ServerName, filesystemServerName) {
		return false
	}
	ref, err := ws.GetDirectoryReference(ws.AnchorDirectoryReferenceID())
	if err != nil || ref == nil || filepath.Clean(ref.Path) != root {
		return false
	}
	switch roots := binding.Config["roots"].(type) {
	case []string:
		return len(roots) == 1 && filepath.Clean(roots[0]) == root
	case []any:
		if len(roots) == 1 {
			path, ok := roots[0].(string)
			return ok && filepath.Clean(path) == root
		}
	}
	return false
}
