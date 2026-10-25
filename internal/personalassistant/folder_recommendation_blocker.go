package personalassistant

import (
	"context"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
)

// FolderRecommendationBlocker is navigation-only. Only a canonical guard may
// supply a conversation ID; no caller can confirm or close that review here.
type FolderRecommendationBlocker struct {
	Kind           string            `json:"kind"`
	Subject        string            `json:"subject"`
	Status         FolderOfferStatus `json:"status"`
	Blocks         bool              `json:"blocks"`
	ConversationID string            `json:"conversation_id,omitempty"`
	OfferID        string            `json:"offer_id,omitempty"`
	Message        string            `json:"message"`
}

func (s *FolderObservationService) recommendationBlocker(ctx context.Context, target foldercontext.Target, doc FolderDigestDocument) *FolderRecommendationBlocker {
	// A pending offer always owns the single pending slot, even when its
	// navigation is no longer available. Confirmed build work is deliberately
	// excluded, matching PendingElsewhere and its abandoned-build contract.
	offer := doc.Pending()
	if offer == nil {
		for i := range doc.Offers {
			candidate := &doc.Offers[i]
			review := candidate.ConversationReview
			if candidate.Status == FolderOfferAwaitingOutcome && review != nil && !IsBuildReview(review.Target) &&
				review.Target.UserID == target.UserID && review.Target.WorkspaceID == target.WorkspaceID && review.Target.AgentName == target.AgentName &&
				review.Target.ConversationID != target.ConversationID && s.reviewGuard != nil && s.reviewGuard(ctx, *candidate) == nil {
				offer = candidate
				break
			}
		}
	}
	if offer == nil {
		return nil
	}
	b := &FolderRecommendationBlocker{Kind: "unavailable", Subject: "Another setup", Status: offer.Status, Blocks: true,
		Message: "Another setup review is open. Its navigation is unavailable; finish or close it from its original entry."}
	review := offer.ConversationReview
	if review == nil {
		b.Kind, b.Subject = "home", foldercontext.DisplayName(offer.Subject.Name)
		b.Message = "This review was opened from Home. Finish or close it there, then refresh this recommendation."
		return b
	}
	if review.Target.UserID != target.UserID || review.Target.WorkspaceID != target.WorkspaceID || review.Target.AgentName != target.AgentName {
		return b
	}
	if IsBuildReview(review.Target) {
		b.Kind = "build"
		b.Message = "A workspace creation review is open. Return to Create Workspace to finish or close it, then refresh this recommendation."
		if s.buildGuard != nil && s.buildGuard(ctx, *offer) == nil {
			b.Subject = foldercontext.DisplayName(offer.Subject.Name)
		}
		return b
	}
	if review.Target.ConversationID == "" || s.reviewGuard == nil || s.reviewGuard(ctx, *offer) != nil {
		return b
	}
	b.Kind, b.Subject = "conversation", foldercontext.DisplayName(offer.Subject.Name)
	b.ConversationID, b.OfferID = review.Target.ConversationID, offer.ID
	b.Blocks = !ownConversationReview(offer, target)
	b.Message = "Finish or close this setup in its owning conversation, then return to this folder."
	if offer.Structure.Arranged() {
		b.Kind = "arranged"
	}
	return b
}
