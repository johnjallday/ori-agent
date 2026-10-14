package agenthttp

import (
	"context"
	"net/http"
	"strings"

	"github.com/johnjallday/ori-agent/internal/assistantcontext"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/workspacearrangement"
)

// PersonalAssistantArrangementHistory is a read-only projection of one saved
// record. ContextCurrent is a conversation pin, not a live source check.
type PersonalAssistantArrangementHistory struct {
	Arrangement    workspacearrangement.Summary `json:"arrangement"`
	ConversationID string                       `json:"conversation_id"`
	MessageID      string                       `json:"message_id"`
	ContextCurrent bool                         `json:"context_current"`
}

type personalAssistantArrangementHistoryReader interface {
	ReadArrangementSummary(context.Context, string, assistantcontext.SaveOwner, string) (*PersonalAssistantArrangementHistory, error)
}

// ArrangementHistoryHandler reads only canonical storage. Unlike the ordinary
// folder drawer it must not call Status, resolve an attachment or probe a root.
func (h *HomeAssistantAskHandler) ArrangementHistoryHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	scope, ok := h.readScope(w, r)
	if !ok {
		return
	}
	owner, err := h.canonicalConversationOwner(r.Context())
	if err != nil || owner.WorkspaceID != scope.workspaceID || !strings.EqualFold(owner.AgentName, scope.agentName) {
		writeConversationError(w, http.StatusConflict, PersonalAssistantConversationOutOfScope, "The conversation owner changed. Nothing was read.")
		return
	}
	conversationID, recordID := strings.TrimSpace(r.PathValue("id")), strings.TrimSpace(r.PathValue("arrangement_id"))
	if r.URL.RawQuery != "" || conversationID == "" || len(conversationID) > 96 || recordID == "" || len(recordID) > 160 {
		writeConversationError(w, http.StatusBadRequest, "invalid_arrangement_reference", "Use a saved arrangement from this conversation.")
		return
	}
	reader, ok := h.Conversations.(personalAssistantArrangementHistoryReader)
	if !ok {
		writeConversationError(w, http.StatusServiceUnavailable, "arrangement_unavailable", "Saved arrangements are unavailable right now.")
		return
	}
	history, err := reader.ReadArrangementSummary(r.Context(), conversationID, owner, recordID)
	if err != nil || history == nil {
		// Do not distinguish missing, foreign and unavailable record identities.
		writeConversationError(w, http.StatusNotFound, "arrangement_unavailable", "That saved arrangement could not be read in this conversation.")
		return
	}
	orihttp.WriteJSON(w, history)
}
