package agenthttp

import (
	"net/http"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
)

// CancelResearchReviewHandler only revokes an ephemeral offer. It performs no
// read, model invocation, cache refresh, transcript write or privileged action.
func (h *HomeAssistantAskHandler) CancelResearchReviewHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}
	var request struct {
		Conversation  *HomeAssistantConversationRef `json:"conversation"`
		Context       *HomeAssistantRouteContext    `json:"context"`
		FolderContext *HomeAssistantFolderRef       `json:"folder_context,omitempty"`
		Token         string                        `json:"token"`
	}
	if !strictResearchBody(w, r, &request) {
		return
	}
	scope, err := h.ResolveResearchScope(r.Context(), request.Conversation, request.Context, request.FolderContext)
	if err != nil || h.ResearchReviews == nil || h.ResearchReviews.Cancel(r.Context(), scope, request.Token) != nil {
		orihttp.RespondErrorWithErr(w, http.StatusConflict, "Research review expired or changed. Nothing was sent.", nil)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	orihttp.WriteJSON(w, map[string]any{"cancelled": true, "sent": false})
}
