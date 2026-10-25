package personalassistant

import (
	"context"
	"fmt"
)

// FolderPrerequisiteInput selects only a package role, never a source, path or
// executable input. Integration consent is the existing journey's envelope;
// provider consent is the exact reviewed release/disclosure witness.
type FolderPrerequisiteInput struct {
	Kind            string `json:"kind"`
	Confirm         bool   `json:"confirm,omitempty"`
	RequestID       string `json:"request_id"`
	ReviewedVersion string `json:"reviewed_version,omitempty"`
	Digest          string `json:"digest,omitempty"`
	RunID           string `json:"run_id,omitempty"`
	Revision        int64  `json:"revision,omitempty"`
	ReviewToken     string `json:"review_token,omitempty"`
	Action          string `json:"action,omitempty"`
}

type FolderPrerequisiteReview struct {
	Kind             string `json:"kind"`
	Operation        string `json:"operation"`
	Name             string `json:"name"`
	Version          string `json:"version,omitempty"`
	InstalledVersion string `json:"installed_version,omitempty"`
	Source           string `json:"source,omitempty"`
	EnabledAfter     bool   `json:"enabled_after"`
	Disclosure       any    `json:"disclosure,omitempty"`
	Details          any    `json:"details,omitempty"`
	Digest           string `json:"digest,omitempty"`
	RunID            string `json:"run_id,omitempty"`
	Revision         int64  `json:"revision,omitempty"`
	ReviewToken      string `json:"review_token,omitempty"`
	Action           string `json:"action,omitempty"`
	Completed        bool   `json:"completed,omitempty"`
}

// FolderPrerequisiteSetup adapts the existing reviewed package lifecycles, not
// the folder setup runner. Nothing here accepts setup/staffing/model consent.
type FolderPrerequisiteSetup interface {
	Prerequisite(context.Context, FolderSetupRequest, FolderPrerequisiteInput) (FolderPrerequisiteReview, error)
}

func libraryReview(offer FolderOffer) bool {
	return offer.Portfolio != nil && offer.Structure != nil && offer.Structure.Group.Type == FolderGroupLibrary
}

// Prerequisite requires this pending canonical creation review and live folder
// even though installing a package itself never reads the folder's contents.
func (s *FolderDigestService) Prerequisite(ctx context.Context, userID, offerID string, input FolderPrerequisiteInput) (FolderPrerequisiteReview, error) {
	if s == nil || s.store == nil {
		return FolderPrerequisiteReview{}, ErrFolderOutcomeUnavailable
	}
	host, ok := s.deps.Setup.(FolderPrerequisiteSetup)
	if !ok || (input.Kind != FolderPlanIntegration && input.Kind != FolderPlanProvider) || input.RequestID == "" || len(input.RequestID) > folderRequestIDMax ||
		len(input.ReviewedVersion) > 80 || len(input.Digest) > 64 || len(input.RunID) > 80 || len(input.ReviewToken) > 256 || len(input.Action) > 32 {
		return FolderPrerequisiteReview{}, fmt.Errorf("%w: prerequisite review", ErrValidation)
	}
	doc, err := s.store.Read(ctx, userID)
	if err != nil {
		return FolderPrerequisiteReview{}, err
	}
	offer := doc.Offer(offerID)
	if offer == nil {
		return FolderPrerequisiteReview{}, ErrFolderOfferNotFound
	}
	review := offer.ConversationReview
	if offer.Status != FolderOfferPending || offer.Setup != nil || review == nil || review.Operation != FolderOperationCreate ||
		offer.CapabilitySuppressed || doc.Tombstoned(offer.Subject.Key) || ((offer.Portfolio != nil || offer.Structure != nil) && !libraryReview(*offer)) || IsBuildReview(review.Target) {
		return FolderPrerequisiteReview{}, ErrFolderOfferDecided
	}
	// An expected missing-Home prerequisite is repairable; a changed concrete
	// destination, or an unnamed destination failure, is not.
	if review.DestinationUnavailable {
		if review.DestinationBlocker == nil || (review.DestinationBlocker.IntegrationKey == "" && !(libraryReview(*offer) && review.DestinationBlocker.HomeProviderKey == offer.Portfolio.ProviderKey && offer.Portfolio.ProviderKey != "")) {
			return FolderPrerequisiteReview{}, ErrFolderPlanChanged
		}
	} else if err := s.checkReviewDestination(ctx, *offer); err != nil {
		return FolderPrerequisiteReview{}, ErrFolderPlanChanged
	}
	if s.conversationLease == nil {
		return FolderPrerequisiteReview{}, ErrFolderSelection
	}
	release, err := s.conversationLease(ctx, *offer)
	if err != nil {
		return FolderPrerequisiteReview{}, err
	}
	defer release()
	// The lease fences confirmation, replacement and detach. Re-read acceptance
	// after acquiring it; no accepted run can be repaired through this adapter.
	fresh, err := s.store.Read(ctx, userID)
	if err != nil {
		return FolderPrerequisiteReview{}, err
	}
	current := fresh.Offer(offerID)
	if current == nil || current.Status != FolderOfferPending || current.Setup != nil {
		return FolderPrerequisiteReview{}, ErrFolderOfferDecided
	}
	if _, err := s.subjectPathOf(*current); err != nil {
		return FolderPrerequisiteReview{}, err
	}
	return host.Prerequisite(ctx, s.setupRequest(ctx, userID, *current), input)
}
