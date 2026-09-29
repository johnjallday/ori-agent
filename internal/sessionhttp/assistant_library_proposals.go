package sessionhttp

import (
	"errors"
	"net/http"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/projectlibrary"
	"github.com/johnjallday/ori-agent/internal/workspace"
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
	if !replay {
		h.recordLibraryProposalFeedback(scope.HomeID, r.PathValue("proposalID"), "", "accepted")
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"entry_id": entry.ID, "fields": entry.Fields, "replay": replay})
}

type libraryDismissRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
	Confirm        bool   `json:"confirm"`
}

// DismissAssistantLibraryProposal is the owner's separate answer to one
// waiting suggestion. It marks it dismissed and changes nothing else; it
// stays available after provider loss because it only removes.
func (h *Handler) DismissAssistantLibraryProposal(w http.ResponseWriter, r *http.Request) {
	scope, _, ok := h.assistantLibraryScope(w, r)
	if !ok {
		return
	}
	var request libraryDismissRequest
	if !decodeLibraryAction(w, r, &request) {
		return
	}
	if !request.Confirm {
		_ = orihttp.RespondBadRequest(w, "Confirm dismissing the suggestion")
		return
	}
	proposal, replay, err := h.assistantLibraryStore().DismissManagerProposal(scope, r.PathValue("proposalID"),
		request.IdempotencyKey)
	if errors.Is(err, projectlibrary.ErrProposalNotFound) {
		_ = orihttp.RespondNotFound(w, "Suggestion not found")
		return
	}
	if err != nil {
		respondLibraryActionError(w, err)
		return
	}
	if !replay {
		h.recordLibraryProposalFeedback(scope.HomeID, proposal.ID, proposal.Kind, "dismissed")
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"proposal_id": proposal.ID, "dismissed_at": proposal.DismissedAt,
		"replay": replay})
}

// recordLibraryProposalFeedback counts the answer on the Home's assistant
// learning sidecar. It is a reviewed-only signal and best effort: the owner's
// answer already stands in the Home, so a counting failure is only logged.
func (h *Handler) recordLibraryProposalFeedback(homeID, proposalID, kind, outcome string) {
	store, ok := h.assistantLearningStore()
	if !ok {
		return
	}
	if kind == "" {
		kind = "next_action"
	}
	if _, err := store.RecordProposalFeedback(homeID, proposalID, kind, outcome); err != nil {
		logger.Warn("Library suggestion feedback was not counted", logger.Fields{"home_id": homeID,
			"proposal_id": proposalID, "error": err.Error()})
	}
}

// libraryProposalFeedback reads the counts for the summary. A missing or
// unreadable sidecar reads as no feedback.
func (h *Handler) libraryProposalFeedback(homeID string) []workspace.AssistantProposalFeedback {
	store, ok := h.assistantLearningStore()
	if !ok {
		return nil
	}
	document, err := store.Read(homeID)
	if err != nil {
		return nil
	}
	return document.ProposalFeedback
}
