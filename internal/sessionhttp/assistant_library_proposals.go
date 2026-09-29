package sessionhttp

import (
	"net/http"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
)

// Only the authenticated Home owner can see suggestions, obtain a real field
// review, or confirm it. There is deliberately no agent-callable HTTP route.
func (h *Handler) ListAssistantLibraryProposals(w http.ResponseWriter, r *http.Request) {
	scope, _, ok := h.assistantLibraryScope(w, r)
	if !ok {
		return
	}
	page, err := h.assistantLibraryStore().ListManagerProposals(scope)
	if err != nil {
		respondLibraryReadError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, page)
}

func (h *Handler) ReviewAssistantLibraryProposal(w http.ResponseWriter, r *http.Request) {
	scope, _, ok := h.assistantLibraryScope(w, r)
	if !ok || !emptyLibraryAction(w, r) {
		return
	}
	review, err := h.assistantLibraryStore().ReviewProposedNextAction(scope, r.PathValue("proposalID"), scope.OwnerUserID)
	if err != nil {
		respondLibraryActionError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, review)
}

func (h *Handler) CommitAssistantLibraryProposal(w http.ResponseWriter, r *http.Request) {
	scope, _, ok := h.assistantLibraryScope(w, r)
	if !ok {
		return
	}
	var request libraryCommitRequest
	if !decodeLibraryAction(w, r, &request) {
		return
	}
	if !request.Confirm {
		_ = orihttp.RespondBadRequest(w, "Confirm the reviewed Home note")
		return
	}
	entry, replay, err := h.assistantLibraryStore().CommitProposedNextAction(scope, r.PathValue("proposalID"),
		request.ReviewToken, request.IdempotencyKey, scope.OwnerUserID)
	if err != nil {
		respondLibraryActionError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"entry_id": entry.ID, "fields": entry.Fields, "replay": replay})
}
