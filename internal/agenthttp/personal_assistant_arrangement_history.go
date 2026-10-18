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
	// Execution lists each proposed group and what its own bound setup review
	// became. It is read from canonical state only and grants nothing.
	Execution *PersonalAssistantArrangementExecution `json:"execution,omitempty"`
}

type personalAssistantArrangementHistoryReader interface {
	ReadArrangementSummary(context.Context, string, assistantcontext.SaveOwner, string) (*PersonalAssistantArrangementHistory, error)
}

// ArrangementHistoryHandler reads only canonical storage: the record, its
// group-review journal, the conversation's folder events and stored offers.
// Unlike the ordinary folder drawer it never calls the folder service's
// Status, resolves an attachment or probes a root. Its one write records, by
// compare-and-swap, what a bound review's own offer already shows; the same
// monotonic reconciliation runs again under the lease before any POST acts.
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
	// Each group's state comes from its own bound review, read from stored
	// offers and the canonical folder event: no attachment is resolved and no
	// folder is probed. Recording what a review already shows is correlation,
	// never a new effect.
	if store, ok := h.Conversations.(personalAssistantArrangementExecutionStore); ok {
		if target, err := h.folderTarget(scope, conversationID, ""); err == nil {
			if saved, err := store.ReadArrangementExecution(r.Context(), conversationID, owner, recordID); err == nil && saved != nil {
				h.reconcileArrangementEffects(r.Context(), target, owner, saved)
				history.Execution = h.arrangementExecution(r.Context(), target, owner, saved)
			}
		}
	}
	orihttp.WriteJSON(w, history)
}
