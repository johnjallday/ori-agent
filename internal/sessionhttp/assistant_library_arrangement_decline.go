package sessionhttp

import (
	"net/http"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/projectlibrary"
)

func (h *Handler) DeclineAssistantLibraryArrangement(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	scope, _, ok := h.assistantLibraryScope(w, r)
	if !ok {
		return
	}
	in, err := decodeCollectionChoice(w, r, false)
	if err != nil || r.URL.RawQuery != "" {
		_ = orihttp.RespondBadRequest(w, "Invalid proposal decline")
		return
	}
	if h.collectionReview == nil {
		collectionReviewFailure(w, projectlibrary.ErrUnavailable)
		return
	}
	view, err := h.collectionReview.Decline(r.Context(), scope.OwnerUserID, scope.HomeID, r.PathValue("arrangementID"), in["expected_revision"].(int64), in["expected_record_revision"].(int64))
	if err != nil {
		collectionReviewFailure(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, view)
}
