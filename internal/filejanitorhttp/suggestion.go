package filejanitorhttp

import (
	"errors"
	"net/http"
	"strings"

	"github.com/johnjallday/ori-agent/internal/filejanitor"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
)

// AskForSuggestion is "Ask <agent> what to send" (FR 43): one bounded turn of the
// workspace's Filing agent. The request has no body: the review is named by the
// path, and nothing the browser sends reaches the model. The answer is the
// suggestion as the picture draws it; it moves nothing and ticks nothing.
func (h *Handler) AskForSuggestion(w http.ResponseWriter, r *http.Request) {
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
	suggestion, err := h.service.AskForSuggestion(r.Context(), owner, workspaceID, r.PathValue("batchID"))
	if err != nil {
		if errors.Is(err, filejanitor.ErrSuggestionUnavailable) {
			message := "The Filing agent cannot be asked right now."
			if detail := strings.TrimPrefix(err.Error(), filejanitor.ErrSuggestionUnavailable.Error()+": "); detail != err.Error() {
				message = detail + "."
			}
			_ = orihttp.RespondAPIError(w, http.StatusConflict, orihttp.NewAPIError("suggestion_unavailable", message))
			return
		}
		h.respondReviewError(w, err, "Failed to ask the Filing agent")
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "suggestion": suggestion})
}
