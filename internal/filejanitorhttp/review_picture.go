package filejanitorhttp

import (
	"errors"
	"net/http"
	"strings"

	"github.com/johnjallday/ori-agent/internal/filejanitor"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
)

// GetReviewPicture serves the one bounded read that draws the review as the
// folder will look after filing. Like the group and draft reads it only reads
// saved Ori state: it writes nothing, creates no folder and issues no approval.
func (h *Handler) GetReviewPicture(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	workspaceID, ok := h.resolveWorkspace(w, r)
	if !ok {
		return
	}
	owner, err := h.currentOwner(r.Context())
	if err != nil {
		_ = orihttp.RespondInternalError(w, "Failed to resolve the current user")
		return
	}
	picture, err := h.service.ReadReviewPicture(r.Context(), owner, workspaceID, r.PathValue("batchID"))
	if err != nil {
		h.respondPictureError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "picture": picture, "can_reveal": localFinderAvailable(r)})
}

// GetReviewPictureFiles pages one list of the picture: "and N more" on a folder
// card, or the full list of a loose row. The revisions pin the page to what the
// person was looking at; a mismatch is a 409, never a page from different data.
func (h *Handler) GetReviewPictureFiles(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	workspaceID, ok := h.resolveWorkspace(w, r)
	if !ok {
		return
	}
	limit, offset, valid := candidatePagination(r)
	if !valid {
		_ = orihttp.RespondAPIError(w, http.StatusBadRequest,
			orihttp.NewAPIError("invalid_pagination", "That page request is not valid."))
		return
	}
	owner, err := h.currentOwner(r.Context())
	if err != nil {
		_ = orihttp.RespondInternalError(w, "Failed to resolve the current user")
		return
	}
	query := r.URL.Query()
	page, err := h.service.ReadReviewPictureFiles(r.Context(), owner, workspaceID, r.PathValue("batchID"), filejanitor.PictureFilesQuery{
		Section: strings.TrimSpace(query.Get("section")), Key: strings.TrimSpace(query.Get("key")),
		Revision: query.Get("revision"), DraftRevision: query.Get("draft_revision"),
		Limit: limit, Offset: offset,
	})
	if err != nil {
		h.respondPictureError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "page": page})
}

func (h *Handler) respondPictureError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, filejanitor.ErrBatchNotFound):
		_ = orihttp.RespondNotFound(w, "batch not found")
	case errors.Is(err, filejanitor.ErrReviewConflict):
		_ = orihttp.RespondAPIError(w, http.StatusConflict,
			orihttp.NewAPIError("review_changed", "This saved review changed. Reload the picture before continuing."))
	case errors.Is(err, filejanitor.ErrInvalidAction):
		_ = orihttp.RespondAPIError(w, http.StatusBadRequest,
			orihttp.NewAPIError("invalid_request", "That list is not part of the picture."))
	default:
		_ = orihttp.RespondAPIError(w, http.StatusConflict,
			orihttp.NewAPIError("review_unavailable", "This saved review is unavailable. Check Filing setup and reload the current review."))
	}
}
