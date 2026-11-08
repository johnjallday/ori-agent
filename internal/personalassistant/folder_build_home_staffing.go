package personalassistant

import (
	"context"
	"strings"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
)

// ReviewBuildHomeStaffing uses the build's existing canonical review. Preparing
// its owner is explicit and metadata-only, never a side effect of a preview GET.
// Subsequent actions keep the original context/revision supplied by the user.
func (s *FolderObservationService) ReviewBuildHomeStaffing(ctx context.Context, draft foldercontext.Target, sessionID, observationID, candidateID, workspaceName string, input FolderHomeStaffingInput) (FolderOfferView, error) {
	input.RequestID = strings.TrimSpace(input.RequestID)
	if input.Action == "prepare_staffing" {
		if input.RequestID == "" || len(input.RequestID) > folderRequestIDMax || input.Revision != 0 || input.ContextDigest != "" || input.Choice != nil || input.RoleID != "" {
			return FolderOfferView{}, ErrValidation
		}
	} else if err := input.validate(); err != nil {
		return FolderOfferView{}, err
	}
	if s == nil || s.digest == nil {
		return FolderOfferView{}, ErrFolderOutcomeUnavailable
	}
	if input.Action == "clear_staffing" {
		// Clearing needs the original owner and held pick, not an available
		// provider or a successful re-plan. Never create a new owner for clear.
		doc, err := s.digest.store.Read(ctx, draft.UserID)
		if err != nil {
			return FolderOfferView{}, err
		}
		target, offerID := BuildReviewTarget(draft, sessionID), ""
		for _, offer := range doc.Offers {
			review := offer.ConversationReview
			if offer.Status == FolderOfferPending && review != nil && review.Target == target && review.ObservationID == observationID && review.CandidateID == candidateID {
				if offerID != "" {
					return FolderOfferView{}, ErrFolderReviewPending
				}
				offerID = offer.ID
			}
		}
		if offerID == "" {
			return FolderOfferView{}, ErrFolderOfferNotFound
		}
		return s.digest.ReviewHomeStaffing(ctx, draft.UserID, offerID, input)
	}
	source := draft
	if _, _, err := s.resolve(ctx, draft, observationID); err != nil {
		source = BuildReviewTarget(draft, sessionID)
	}
	preview, err := s.BuildPlan(ctx, source, observationID, candidateID, workspaceName)
	if err != nil {
		return FolderOfferView{}, err
	}
	if preview == nil || preview.HomeStaffing == nil {
		return FolderOfferView{}, ErrFolderOutcomeUnavailable
	}
	view, err := s.ReviewBuild(ctx, draft, sessionID, observationID, candidateID, workspaceName)
	if err != nil {
		return FolderOfferView{}, err
	}
	if input.Action == "prepare_staffing" {
		return view, nil
	}
	return s.digest.ReviewHomeStaffing(ctx, draft.UserID, view.ID, input)
}
