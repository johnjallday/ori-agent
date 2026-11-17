package filejanitorhttp

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/johnjallday/ori-agent/internal/filejanitor"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
)

// ListFilings serves the recent filings, newest first, for History's "Last filing"
// line. It reads saved state only.
func (h *Handler) ListFilings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	workspaceID, ok := h.resolveWorkspace(w, r)
	if !ok {
		return
	}
	limit := 1
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > filejanitor.MaxListedFilings {
			_ = orihttp.RespondAPIError(w, http.StatusBadRequest,
				orihttp.NewAPIError("invalid_request", "That list request is not valid."))
			return
		}
		limit = parsed
	}
	owner, err := h.currentOwner(r.Context())
	if err != nil {
		_ = orihttp.RespondInternalError(w, "Failed to resolve the current user")
		return
	}
	filings, err := h.service.ListFilings(r.Context(), owner, workspaceID, limit)
	if err != nil {
		h.respondFilingReadError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "filings": filings})
}

// GetFiling serves the receipt of one press: what actually happened, from the
// journal. The not-moved list is paged with ?offset=.
func (h *Handler) GetFiling(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	workspaceID, ok := h.resolveWorkspace(w, r)
	if !ok {
		return
	}
	offset := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("offset")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			_ = orihttp.RespondAPIError(w, http.StatusBadRequest,
				orihttp.NewAPIError("invalid_pagination", "That page request is not valid."))
			return
		}
		offset = parsed
	}
	owner, err := h.currentOwner(r.Context())
	if err != nil {
		_ = orihttp.RespondInternalError(w, "Failed to resolve the current user")
		return
	}
	receipt, err := h.service.ReadFiling(r.Context(), owner, workspaceID, r.PathValue("filingID"), offset)
	if err != nil {
		h.respondFilingReadError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "receipt": receipt})
}

// UndoFiling serves Undo all for one filing. The request has no body: the filing
// is named by the path and nothing the browser sends can widen what is put back.
func (h *Handler) UndoFiling(w http.ResponseWriter, r *http.Request) {
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
	result, err := h.service.UndoFiling(r.Context(), owner, workspaceID, r.PathValue("filingID"))
	if err != nil {
		if errors.Is(err, filejanitor.ErrUndoUnavailable) {
			_ = orihttp.RespondAPIError(w, http.StatusConflict, orihttp.NewAPIError("undo_unavailable", err.Error()))
			return
		}
		h.respondReviewError(w, err, "Failed to put the files back")
		return
	}
	// Files that could not be put back are in the result, not an error: the request
	// was fine and the reasons belong beside each file.
	_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "undo": result})
}

// ForgetRestoredFiles is the first half of "Scan again to review them": it makes
// Ori forget that the files Undo all put back were put back. It moves nothing and
// scans nothing; the browser scans only after this succeeds.
func (h *Handler) ForgetRestoredFiles(w http.ResponseWriter, r *http.Request) {
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
	forgotten, err := h.service.ForgetRestored(r.Context(), owner, workspaceID, r.PathValue("filingID"))
	if err != nil {
		if errors.Is(err, filejanitor.ErrUndoUnavailable) {
			_ = orihttp.RespondAPIError(w, http.StatusConflict, orihttp.NewAPIError("undo_unavailable", err.Error()))
			return
		}
		h.respondReviewError(w, err, "Failed to forget the restored files")
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "forgotten": forgotten})
}

func (h *Handler) respondFilingReadError(w http.ResponseWriter, err error) {
	if errors.Is(err, filejanitor.ErrFilingNotFound) {
		_ = orihttp.RespondNotFound(w, "filing not found")
		return
	}
	_ = orihttp.RespondAPIError(w, http.StatusConflict,
		orihttp.NewAPIError("review_unavailable", "This filing is unavailable. Check Filing setup and reload."))
}
