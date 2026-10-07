package personalassistant

import (
	"context"
	"strings"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
)

const (
	FolderOperationCreate  = "create_project_workspace"
	FolderOperationSupport = "link_supporting_folder"
)

// FolderReviewPlacement is resolved by the HTTP host from canonical references.
// It is request-local input to explicit Review, never natural-language consent.
type FolderReviewPlacement struct {
	Operation     string
	DestinationID string
}

// SetupHomeIdentity reads the canonical resulting-parent receipt. It is not a
// browser/model grant and never falls back to a Home name or creation timestamp.
func (s *FolderDigestService) SetupHomeIdentity(ctx context.Context, userID, offerID, digest string) (string, error) {
	if s == nil || s.store == nil || digest == "" {
		return "", ErrFolderPlanChanged
	}
	doc, err := s.store.Read(ctx, userID)
	if err != nil {
		return "", ErrFolderPlanChanged
	}
	offer := doc.Offer(offerID)
	if offer == nil || offer.Setup == nil || offer.Setup.PlanDigest != digest {
		return "", ErrFolderPlanChanged
	}
	return offer.Setup.HomeID, nil
}

type folderReviewPlacementKey struct{}

func WithFolderReviewPlacement(ctx context.Context, placement FolderReviewPlacement) context.Context {
	return context.WithValue(ctx, folderReviewPlacementKey{}, placement)
}

func folderReviewPlacement(ctx context.Context) FolderReviewPlacement {
	placement, _ := ctx.Value(folderReviewPlacementKey{}).(FolderReviewPlacement)
	return placement
}

// PendingElsewhere returns only a verified local review owned by the same
// relationship. A caller may focus its conversation, never execute it here.
func (s *FolderObservationService) PendingElsewhere(ctx context.Context, target foldercontext.Target) (*FolderOfferView, error) {
	if s == nil || s.digest == nil || !target.Valid() {
		return nil, foldercontext.ErrInvalid
	}
	binding, err := s.digest.store.Binding(ctx, target.UserID)
	if err != nil {
		return nil, err
	}
	if binding.HQWorkspaceID != target.WorkspaceID {
		return nil, foldercontext.ErrInvalid
	}
	doc, err := s.digest.store.Read(ctx, target.UserID)
	if err != nil {
		return nil, err
	}
	for _, offer := range doc.Offers {
		review := offer.ConversationReview
		if review == nil || review.Target.UserID != target.UserID || review.Target.WorkspaceID != target.WorkspaceID || review.Target.AgentName != target.AgentName || review.Target.ConversationID == target.ConversationID || (offer.Status != FolderOfferPending && offer.Status != FolderOfferAwaitingOutcome) {
			continue
		}
		if s.reviewGuard == nil || s.reviewGuard(ctx, offer) != nil {
			return nil, foldercontext.ErrInvalid
		}
		view := s.digest.viewFor(ctx, target.UserID, offer, binding.Paused)
		return &view, nil
	}
	return nil, nil
}

// CompletedProject returns this relationship's completed project for the exact
// observed candidate, so Review points at it instead of preparing a second
// one. It matches the canonical folder key and its directory identity, never a
// name: a namesake or a folder replaced at the same path is a different folder.
// A supporting link is a different operation and never matches. The host must
// confirm the project still holds the folder; an unproven outcome is not reuse.
func (s *FolderObservationService) CompletedProject(ctx context.Context, target foldercontext.Target, observationID, candidateID string) (*FolderOfferView, error) {
	if s == nil || s.digest == nil || !target.Valid() {
		return nil, foldercontext.ErrInvalid
	}
	linked := s.digest.deps.ProjectLinked
	if linked == nil {
		return nil, nil
	}
	held, _, err := s.resolve(ctx, target, observationID)
	if err != nil {
		return nil, err
	}
	candidate, ok := observedReviewCandidate(held, candidateID)
	if !ok {
		return nil, ErrFolderSelection
	}
	identity, err := portfolioDirectoryIdentity(candidate.Path)
	if err != nil {
		return nil, ErrFolderPathLost
	}
	doc, err := s.digest.store.Read(ctx, target.UserID)
	if err != nil {
		return nil, err
	}
	key := FolderKey(candidate.Path)
	placement := folderReviewPlacement(ctx)
	for i := len(doc.Offers) - 1; i >= 0; i-- {
		offer := doc.Offers[i]
		review := offer.ConversationReview
		if offer.Status != FolderOfferResolved || offer.Portfolio != nil || offer.Subject.Key != key || offer.Outcome == nil ||
			offer.Outcome.Kind != FolderChoiceProject || strings.TrimSpace(offer.Outcome.WorkspaceID) == "" {
			continue
		}
		// An offer made outside a conversation recorded only its root's identity.
		witness := offer.RootIdentity
		if review != nil {
			if review.Operation == FolderOperationSupport || review.Target.UserID != target.UserID || review.Target.WorkspaceID != target.WorkspaceID || review.Target.AgentName != target.AgentName {
				continue
			}
			// Review reuses this conversation's own outcome for the same
			// operation and destination in place, with its receipt.
			if review.Target == target && review.Operation == placement.Operation && review.DestinationID == placement.DestinationID {
				continue
			}
			witness = review.CandidateIdentity
		} else if !offer.Subject.IsRoot {
			continue
		}
		if witness == "" || witness != identity || !linked(ctx, target.UserID, offer.Outcome.WorkspaceID, candidate.Path) {
			continue
		}
		view := s.digest.viewFor(ctx, target.UserID, offer, held.binding.Paused)
		return &view, nil
	}
	return nil, nil
}

// PreviewReview has no offer allocation or persistence and shares the exact
// observed-candidate and canonical-destination readers with explicit Review.
func (s *FolderObservationService) PreviewReview(ctx context.Context, target foldercontext.Target, observationID, candidateID string) (FolderOfferView, error) {
	held, _, err := s.resolve(ctx, target, observationID)
	if err != nil {
		return FolderOfferView{}, err
	}
	// Provenance requires a conversation-shaped target, but this placeholder
	// is never stored, leased or accepted as an executable reference.
	if target.ConversationID == "" {
		target.ConversationID, target.DraftID = "preview", ""
	}
	offer, err := s.prepareReviewOffer(ctx, held, target, candidateID)
	if err != nil {
		return FolderOfferView{}, err
	}
	view := s.digest.describeOffer(ctx, offer, held.binding.Paused)
	if offer.ConversationReview.Destination != nil {
		copy := *offer.ConversationReview.Destination
		view.Destination = &copy
	}
	if offer.ConversationReview.DestinationUnavailable {
		view.DestinationStatus = "unavailable"
	}
	return view, nil
}
