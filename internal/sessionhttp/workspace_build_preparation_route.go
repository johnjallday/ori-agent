package sessionhttp

import (
	"path/filepath"
	"strings"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// A reserved UUID is not a page route or proof a workspace exists. Recovery
// links use the canonical folder index, only after owner and operation checks.
func (h *Handler) preparedBuildFolderSlug(owner string, build personalassistant.WorkspaceBuildSession) string {
	claim := build.Preparation
	if claim == nil || h.workspaceStore == nil || h.workspaceTaskStore == nil {
		return ""
	}
	for _, get := range []func(string) (*workspace.Workspace, error){h.workspaceTaskStore.Get, h.readReviewedWorkspace} {
		ws, err := get(claim.WorkspaceID)
		if err != nil || ws == nil || (ws.Status != "" && ws.Status != workspace.StatusActive) {
			return ""
		}
		id := strings.TrimSpace(ws.OwnerUserID)
		if id == "" {
			id = userprofile.LocalUserID
		}
		if id != owner {
			return ""
		}
	}
	ws, err := h.readReviewedWorkspace(claim.WorkspaceID)
	if err != nil || ws == nil {
		return ""
	}
	p := ws.GetTemplateProvenance()
	if p == nil || p.AssistantSetup == nil || p.AssistantSetup.RunID != build.ID || p.AssistantSetup.OperationID != claim.ID || p.AssistantSetup.ReviewDigest != claim.ReviewRevision {
		return ""
	}
	path, err := h.workspaceStore.GetFolderPath(claim.WorkspaceID)
	if err != nil || strings.TrimSpace(path) == "" {
		return ""
	}
	slug := filepath.Base(filepath.Clean(path))
	if slug == "." || slug == string(filepath.Separator) {
		return ""
	}
	return slug
}
