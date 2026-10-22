package agenthttp

import (
	"errors"
	"net/http"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

// What the card's Not now and Adjust record on the offer. Hire and start is
// not here: it is POST /api/agents/hire, which marks the offer hired itself.

// Refusal codes for the offer's state routes.
const (
	PersonalAssistantHireProposalNotFound    = "hire_proposal_not_found"
	PersonalAssistantHireProposalSettled     = "hire_proposal_settled"
	PersonalAssistantHireProposalUnavailable = "hire_proposals_unavailable"
)

// DeclineHireProposalHandler puts an offer aside: the card loses its buttons
// and the assistant is told, for the rest of that conversation, not to offer
// the same work again. POST /api/personal-assistant/hire-proposals/{proposalID}/decline
func (h *HomeAssistantAskHandler) DeclineHireProposalHandler(w http.ResponseWriter, r *http.Request) {
	h.settleHireProposal(w, r, personalassistant.HireProposalDeclined)
}

// AdjustHireProposalHandler records that the offer was taken to the workspace
// form by hand; nothing is created here. POST /api/personal-assistant/hire-proposals/{proposalID}/adjust
func (h *HomeAssistantAskHandler) AdjustHireProposalHandler(w http.ResponseWriter, r *http.Request) {
	h.settleHireProposal(w, r, personalassistant.HireProposalAdjusted)
}

// settleHireProposal moves one offered proposal of the current user, in a
// conversation of their assistant's Personal HQ, to state. The same move
// twice answers the same way; a proposal that went another way is refused
// with its state, so the card can show what happened.
func (h *HomeAssistantAskHandler) settleHireProposal(w http.ResponseWriter, r *http.Request, state personalassistant.HireProposalState) {
	if h.HireProposals == nil {
		writeConversationError(w, http.StatusServiceUnavailable, PersonalAssistantHireProposalUnavailable, "Hire offers are unavailable in this build. Nothing was changed.")
		return
	}
	scope, ok := h.readScope(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	userID, err := h.currentAssistantUser(ctx)
	if err != nil {
		writeConversationError(w, http.StatusConflict, PersonalAssistantConversationOutOfScope, "Your personal assistant changed. Nothing was changed; reopen the conversation.")
		return
	}
	id := strings.TrimSpace(r.PathValue("proposalID"))
	proposal, err := h.HireProposals.Get(ctx, id)
	if errors.Is(err, personalassistant.ErrHireProposalNotFound) || (err == nil && proposal.UserID != userID) {
		writeConversationError(w, http.StatusNotFound, PersonalAssistantHireProposalNotFound, "That offer is no longer here. Nothing was changed.")
		return
	}
	if err != nil {
		logger.Warn("Hire proposal could not be read", logger.Fields{"proposal": id, "error": err})
		writeConversationError(w, http.StatusServiceUnavailable, PersonalAssistantHireProposalUnavailable, "That offer could not be read right now. Nothing was changed.")
		return
	}
	// The offer belongs to a conversation of this assistant's Personal HQ.
	record, err := h.Conversations.Get(ctx, proposal.ConversationID)
	if err != nil || !scope.owns(record) {
		writeConversationError(w, http.StatusConflict, PersonalAssistantConversationOutOfScope, "That offer belongs to a conversation outside your assistant's Personal HQ. Nothing was changed.")
		return
	}
	switch proposal.State {
	case state:
		orihttp.WriteJSON(w, map[string]any{"proposal": hireProposalView(proposal), "changed": false})
		return
	case personalassistant.HireProposalOffered:
	default:
		_ = orihttp.RespondJSON(w, http.StatusConflict, map[string]any{
			"error": PersonalAssistantHireProposalSettled, "message": "That offer was already " + hireProposalStateWord(proposal.State) + ". Nothing was changed.",
			"proposal": hireProposalView(proposal),
		})
		return
	}
	updated, err := h.HireProposals.Update(ctx, id, func(p *personalassistant.HireProposal) error {
		p.State = state
		return nil
	})
	if err != nil {
		logger.Warn("Hire proposal could not be settled", logger.Fields{"proposal": id, "state": string(state), "error": err})
		writeConversationError(w, http.StatusServiceUnavailable, PersonalAssistantHireProposalUnavailable, "That offer could not be updated right now. Nothing was changed.")
		return
	}
	orihttp.WriteJSON(w, map[string]any{"proposal": hireProposalView(updated), "changed": true})
}

// hireProposalStateWord is a state as a sentence says it.
func hireProposalStateWord(state personalassistant.HireProposalState) string {
	switch state {
	case personalassistant.HireProposalHired:
		return "hired"
	case personalassistant.HireProposalDeclined:
		return "put aside"
	case personalassistant.HireProposalAdjusted:
		return "taken to the workspace form"
	default:
		return string(state)
	}
}
