package personalassistanthttp

import (
	"io"
	"net/http"
	"strings"

	"github.com/google/uuid"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
)

// ResumeKnowledgeForget is a narrowly scoped recovery action for a prepared
// Forget after an interrupted HTTP response. No browser-supplied version,
// prior text, retry key, owner, source or canonical path is trusted.
func (h *Handler) ResumeKnowledgeForget(w http.ResponseWriter, r *http.Request) {
	h.resumeKnowledge(w, r, true)
}

// ResumeKnowledgeOperation finishes a previously confirmed pending approval,
// edit or verified contradiction suspension; the server reads both the retry
// key and any reviewed text from its sidecar.
func (h *Handler) ResumeKnowledgeOperation(w http.ResponseWriter, r *http.Request) {
	h.resumeKnowledge(w, r, false)
}

func (h *Handler) resumeKnowledge(w http.ResponseWriter, r *http.Request, forget bool) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	if h == nil || h.knowledgeReview == nil || h.provider == nil {
		orihttp.ServiceUnavailable(w, "Personal HQ review is unavailable")
		return
	}
	itemID := r.PathValue("item_id")
	if _, err := uuid.Parse(itemID); err != nil {
		orihttp.BadRequest(w, "Check the review item and try again")
		return
	}
	if r.Body != nil {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 32))
		if err != nil || strings.TrimSpace(string(body)) != "" {
			orihttp.BadRequest(w, "This recovery action does not accept source data")
			return
		}
	}
	userID, ok := h.currentUserID(w, r)
	if !ok {
		return
	}
	var resumeErr error
	if forget {
		_, resumeErr = h.knowledgeReview.ResumePreparedForget(r.Context(), userID, itemID)
	} else {
		_, resumeErr = h.knowledgeReview.ResumePreparedOperation(r.Context(), userID, itemID)
	}
	if resumeErr != nil {
		writeKnowledgeReviewError(w, resumeErr)
		return
	}
	orihttp.Success(w, map[string]any{"status": "resumed"})
}
