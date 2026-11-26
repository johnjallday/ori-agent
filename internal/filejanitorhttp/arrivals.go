package filejanitorhttp

import (
	"errors"
	"net/http"

	"github.com/johnjallday/ori-agent/internal/filejanitor"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
)

// ListArrivals serves what other workspaces sent here that is still loose at the
// top level: the receiving side's strip and list (FR 36, 37). It reads this
// workspace's own state and checks each name once; it moves nothing.
func (h *Handler) ListArrivals(w http.ResponseWriter, r *http.Request) {
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
	arrivals, err := h.service.ListArrivals(owner, workspaceID)
	if err != nil {
		h.respondError(w, err, "Failed to read what arrived")
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "arrivals": arrivals})
}

// DismissArrival hides one arrival's strip. No body; it moves nothing.
func (h *Handler) DismissArrival(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	workspaceID, ok := h.resolveWorkspace(w, r)
	if !ok {
		return
	}
	if r.ContentLength > 0 {
		_ = orihttp.RespondBadRequest(w, "That request takes no body.")
		return
	}
	owner, err := h.currentOwner(r.Context())
	if err != nil {
		_ = orihttp.RespondInternalError(w, "Failed to resolve the current user")
		return
	}
	if err := h.service.DismissArrival(owner, workspaceID, r.PathValue("arrivalID")); err != nil {
		if errors.Is(err, filejanitor.ErrArrivalNotFound) {
			_ = orihttp.RespondNotFound(w, "arrival not found")
			return
		}
		h.respondError(w, err, "Failed to dismiss what arrived")
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"success": true})
}

// UndoArrival is "Undo this send": it puts back into the sending workspace the
// files of that press still at this workspace's top level, and nothing else. No
// body; the arrival is named by the path.
func (h *Handler) UndoArrival(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	workspaceID, ok := h.resolveWorkspace(w, r)
	if !ok {
		return
	}
	if r.ContentLength > 0 {
		_ = orihttp.RespondBadRequest(w, "That request takes no body.")
		return
	}
	owner, err := h.currentOwner(r.Context())
	if err != nil {
		_ = orihttp.RespondInternalError(w, "Failed to resolve the current user")
		return
	}
	result, err := h.service.UndoArrival(r.Context(), owner, workspaceID, r.PathValue("arrivalID"))
	if err != nil {
		switch {
		case errors.Is(err, filejanitor.ErrArrivalNotFound):
			_ = orihttp.RespondNotFound(w, "arrival not found")
		case errors.Is(err, filejanitor.ErrUndoUnavailable):
			_ = orihttp.RespondAPIError(w, http.StatusConflict, orihttp.NewAPIError("undo_unavailable", err.Error()))
		default:
			h.respondError(w, err, "Failed to put the files back")
		}
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "undo": result})
}
