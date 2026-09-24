package personalassistanthttp

import (
	"context"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

// InterviewOffer is an optional post-activation presentation record, not a
// condition of successfully building the user's Personal HQ.
type InterviewOffer interface {
	Offer(ctx context.Context, userID string) (*personalassistant.KnowledgeInterview, error)
}

func (h *Handler) SetInterviewOffer(offer InterviewOffer) {
	if h != nil {
		h.interviewOffer = offer
	}
}
