package sessionhttp

import (
	"context"
	"errors"
	"net/http"

	"github.com/johnjallday/ori-agent/internal/filesearch"
	"github.com/johnjallday/ori-agent/internal/session"
)

// SetFolderSearch enables the optional filename effect for ordinary, reviewed
// anchored creation. The local Files boundary is narrower than workspace API
// access and is rechecked on both review and confirmation.
func (h *Handler) SetFolderSearch(s *filesearch.Service, allowed func(*http.Request, string) bool) {
	h.folderSearch, h.folderSearchAllowed = s, allowed
}

func (h *Handler) resolveSearchableAnchor(r *http.Request, owner string, req createWorkspaceRequest) (string, error) {
	if h.folderSearch == nil || h.folderSearchAllowed == nil || !h.folderSearchAllowed(r, owner) {
		return "", filesearch.ErrUnavailable
	}
	if req.FolderSelection == "" || req.AnchorPath != "" || req.BuildSessionID != "" || req.Kind == "group" || req.ProjectConnection != nil || req.Location != "" {
		return "", errors.New("choose one folder with the folder controls before review")
	}
	if err := h.folderSearch.CanCreate(r.Context(), owner, req.FolderSelection); err != nil {
		return "", err
	}
	return h.folderSearch.ReviewedRoot(r.Context(), owner, req.FolderSelection)
}

// The ordinary anchor checker may report a foreign workspace name or the
// identity read from a portable file. Neither is an authorized Files match.
func searchableAnchorRefusal(cause *anchorPathError) *anchorPathError {
	return anchorError("folder_unavailable", "This folder cannot be used for a new workspace. No search access was granted.", cause.status)
}

// Workspace creation is already durable. Verify the actual anchor, not the
// predicted name or an arbitrary primary link, before starting the search
// effect. A failed grant is not a reason to repeat or roll back creation.
func (h *Handler) finishSearchableAnchor(ctx context.Context, ws *session.Workspace, req createWorkspaceRequest, response map[string]any) {
	partial := "Workspace created. Its folder connection could not be verified."
	if req.AllowFilenameSearch {
		partial = "Workspace created, but its search connection was not saved. Choose the same folder again in Files to retry the connection without creating another workspace."
	}
	stored, err := h.workspaceStore.Get(ws.ID)
	if err != nil || stored == nil || stored.OwnerUserID != ws.OwnerUserID {
		response["search_warning"] = partial
		return
	}
	ref, err := stored.GetDirectoryReference(stored.AnchorDirectoryReferenceID())
	if err != nil || ref == nil || ref.WorkspaceID != ws.ID || ref.Path != req.AnchorPath {
		response["search_warning"] = partial
		return
	}
	response["directory_reference_id"] = ref.ID
	source, err := h.folderSearch.BindCreated(ctx, ws.OwnerUserID, req.FolderSelection, ws.ID, ref.ID, req.AllowFilenameSearch)
	if err != nil {
		response["search_warning"] = partial
		return
	}
	if req.AllowFilenameSearch {
		response["filename_source_id"] = source.ID
	}
}
