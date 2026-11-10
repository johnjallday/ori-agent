package filejanitorhttp

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/johnjallday/ori-agent/internal/filejanitor"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
)

func (h *Handler) GetReviewDraft(w http.ResponseWriter, r *http.Request) {
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
	view, err := h.service.ReadReviewDraft(r.Context(), owner, workspaceID, r.PathValue("batchID"))
	if err != nil {
		h.respondDraftError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "draft": view})
}

func (h *Handler) EditReviewDraft(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	workspaceID, ok := h.resolveWorkspace(w, r)
	if !ok {
		return
	}
	if r.ContentLength > filejanitor.MaxReviewEditBytes {
		h.respondDraftError(w, filejanitor.ErrReviewBounds)
		return
	}
	var request filejanitor.ReviewDraftEdit
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, filejanitor.MaxReviewEditBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		var sizeError *http.MaxBytesError
		if errors.As(err, &sizeError) {
			h.respondDraftError(w, filejanitor.ErrReviewBounds)
		} else {
			_ = orihttp.RespondBadRequest(w, "That saved-choice edit is not valid.")
		}
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		_ = orihttp.RespondBadRequest(w, "Send exactly one saved-choice edit.")
		return
	}
	owner, err := h.currentOwner(r.Context())
	if err != nil {
		_ = orihttp.RespondInternalError(w, "Failed to resolve the current user")
		return
	}
	view, err := h.service.EditReviewDraft(owner, workspaceID, r.PathValue("batchID"), request)
	if err != nil {
		h.respondDraftError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "draft": view})
}

func (h *Handler) respondDraftError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, filejanitor.ErrReviewBounds):
		_ = orihttp.RespondAPIError(w, http.StatusRequestEntityTooLarge,
			orihttp.NewAPIError("review_bounds", "These saved choices exceed the review limit. Choose a smaller exact selection; nothing was partially saved."))
	case errors.Is(err, filejanitor.ErrReviewConflict):
		_ = orihttp.RespondAPIError(w, http.StatusConflict,
			orihttp.NewAPIError("review_conflict", "This review or its saved choices changed. Reload before making that choice again; nothing was overwritten."))
	case errors.Is(err, filejanitor.ErrReviewDraftInvalid):
		_ = orihttp.RespondAPIError(w, http.StatusConflict,
			orihttp.NewAPIError("review_draft_invalid", "Saved choices are unavailable for this review. Reload and review the current state; unknown data was left intact and no old approval was restored."))
	case errors.Is(err, filejanitor.ErrCandidateNotFound), errors.Is(err, filejanitor.ErrBatchNotFound):
		_ = orihttp.RespondNotFound(w, "review member or batch not found")
	case errors.Is(err, filejanitor.ErrCandidateNotActionable):
		_ = orihttp.RespondAPIError(w, http.StatusConflict,
			orihttp.NewAPIError("review_member_changed", "A file changed or still needs a category decision. Reload this batch, choose a category where needed, or use Scan now for a fresh review."))
	case errors.Is(err, filejanitor.ErrUnknownCategory), errors.Is(err, filejanitor.ErrInvalidAction):
		_ = orihttp.RespondBadRequest(w, "That edit is not a choice Ori offers for this review.")
	default:
		_ = orihttp.RespondAPIError(w, http.StatusConflict,
			orihttp.NewAPIError("review_unavailable", "This saved review is unavailable. Check File Janitor setup and reload the current review."))
	}
}
