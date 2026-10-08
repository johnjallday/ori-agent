package server

import (
	"path/filepath"
	"strings"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// personalAssistantFileRoots is the part of the workspace folder store the
// assistant panel's file readers need: where a workspace's own files and its
// folder live. It lists nothing and opens nothing.
type personalAssistantFileRoots interface {
	GetFilesPath(workspaceID string) string
	GetFolderPath(workspaceID string) (string, error)
}

// personalAssistantFileSource resolves the host-owned folders behind a
// workspace's readable files. Every folder comes from the canonical folder
// store or from the workspace's own typed project-entry locator; no caller can
// supply one. The contained read that follows does the opening and re-checks
// identity at the moment of the read.
type personalAssistantFileSource struct {
	roots personalAssistantFileRoots
}

func (s personalAssistantFileSource) AttachmentRoot(workspaceID string) (string, bool) {
	if s.roots == nil || strings.TrimSpace(workspaceID) == "" {
		return "", false
	}
	root := strings.TrimSpace(s.roots.GetFilesPath(workspaceID))
	if root == "" || !filepath.IsAbs(root) {
		return "", false
	}
	return filepath.Clean(root), true
}

func (s personalAssistantFileSource) ProjectEntry(ws *workspace.Workspace) (string, string, bool) {
	if s.roots == nil || ws == nil {
		return "", "", false
	}
	folder, err := s.roots.GetFolderPath(ws.ID)
	if err != nil || !filepath.IsAbs(folder) {
		return "", "", false
	}
	root, rel, err := workspace.ProjectEntrySource(ws, folder)
	if err != nil {
		return "", "", false
	}
	return root, rel, true
}
