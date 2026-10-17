package personalassistanthttp

import (
	"strings"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// GlobalMemoryWorkspaces is what the global-memory routes read about
// workspaces: a source workspace's current name and page slug.
type GlobalMemoryWorkspaces interface {
	Get(id string) (*workspace.Workspace, error)
}

// SetGlobalMemoryWorkspaces wires the workspace reader. Without it, review
// items keep the workspace name recorded when an agent proposed the fact.
func (h *Handler) SetGlobalMemoryWorkspaces(workspaces GlobalMemoryWorkspaces) {
	if h != nil {
		h.workspaces = workspaces
	}
}

// attributeReviewItems refreshes each workspace proposal's workspace name and
// adds the page slug the Profile chip links to. A workspace that is gone,
// trashed or missing keeps its recorded name and gets no link.
func (h *Handler) attributeReviewItems(items []personalassistant.KnowledgeReviewItem) {
	if h == nil || h.workspaces == nil {
		return
	}
	for i := range items {
		source := &items[i].Source
		if source.WorkspaceID == "" {
			continue
		}
		ws, err := h.workspaces.Get(source.WorkspaceID)
		if err != nil || ws == nil || ws.Status == workspace.StatusTrashed || ws.Status == workspace.StatusMissing {
			continue
		}
		if name := strings.TrimSpace(ws.Name); name != "" {
			source.WorkspaceName = name
		}
		source.WorkspaceSlug = ws.FolderSlug
	}
}
