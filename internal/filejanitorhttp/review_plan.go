package filejanitorhttp

import (
	"errors"
	"net/http"
	"strings"

	"github.com/johnjallday/ori-agent/internal/filejanitor"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
)

// GetReviewPlan serves the read-only plan check: what filing would do right now
// for the ticked, placed and Trash-marked files, with a digest a later press can
// quote. It issues no approval, creates no folder and writes no state.
func (h *Handler) GetReviewPlan(w http.ResponseWriter, r *http.Request) {
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
	send, ok := h.handoffScope(w, r, owner, workspaceID)
	if !ok {
		return
	}
	query := r.URL.Query()
	var ids []string
	if raw := strings.TrimSpace(query.Get("ids")); raw != "" {
		ids = strings.Split(raw, ",")
	}
	plan, err := h.service.PlanReview(r.Context(), owner, workspaceID, r.PathValue("batchID"), filejanitor.ReviewPlanQuery{
		Revision: query.Get("revision"), GroupRevision: query.Get("group_revision"),
		Destination: strings.TrimSpace(query.Get("destination")), Limit: limit, Offset: offset,
		CandidateIDs: ids, SingleSend: send,
	})
	if err != nil {
		if send != nil {
			refuseHandoff(w)
			return
		}
		h.respondPlanError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "plan": plan})
}

// Only the per-download route carries these path segments. An invalid scope
// must never be interpreted as the ordinary bulk plan.
func (h *Handler) handoffScope(w http.ResponseWriter, r *http.Request, owner, id string) (*filejanitor.SingleSend, bool) {
	if r.PathValue("connectionID") == "" {
		return nil, true
	}
	status, err := h.service.DownloadsReviewStatus(owner, id, h.verifiedDownloads(owner, id))
	if err != nil || status.DownloadsReview != "ready" {
		refuseHandoff(w)
		return nil, false
	}
	return &filejanitor.SingleSend{CandidateID: r.PathValue("candidateID"), ConnectionID: r.PathValue("connectionID")}, true
}

func refuseHandoff(w http.ResponseWriter) {
	_ = orihttp.RespondAPIError(w, http.StatusConflict, orihttp.NewAPIError("handoff_changed", "The file, connection, or destination is no longer available as reviewed. Nothing was sent. Review Downloads again."))
}

func (h *Handler) respondPlanError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, filejanitor.ErrBatchNotFound):
		_ = orihttp.RespondNotFound(w, "batch not found")
	case errors.Is(err, filejanitor.ErrReviewConflict):
		_ = orihttp.RespondAPIError(w, http.StatusConflict,
			orihttp.NewAPIError("review_conflict", "This saved review changed. Reload it before continuing."))
	case errors.Is(err, filejanitor.ErrInvalidAction):
		_ = orihttp.RespondAPIError(w, http.StatusBadRequest,
			orihttp.NewAPIError("invalid_request", "That plan request is not valid."))
	default:
		_ = orihttp.RespondAPIError(w, http.StatusConflict,
			orihttp.NewAPIError("review_unavailable", "This saved review is unavailable. Check Filing setup and reload the current review."))
	}
}
