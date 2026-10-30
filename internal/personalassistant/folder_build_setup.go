package personalassistant

import (
	"context"
	"errors"
	"strings"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
)

// A workspace build's Set it up runs Home's one-card setup journey on the
// folder the build holds. The build is a conversation review of its own: the
// canonical offer's review target is the build's selection target with
// ConversationID "build:<session>", so the setup reads the folder through the
// server's live held selection (reviewSource) and no path leaves the server.
// The build's guard and lease vouch for those reviews; a conversation's never
// do, and the build's never vouch for a conversation's.

// BuildReviewPrefix starts the conversation id of a review a workspace build
// owns. No canonical conversation id has it.
const BuildReviewPrefix = "build:"

// IsBuildReview reports whether a review target belongs to a workspace build.
func IsBuildReview(target foldercontext.Target) bool {
	return strings.HasPrefix(target.ConversationID, BuildReviewPrefix)
}

// BuildReviewTarget is a build's review target: its selection target (the
// build as the draft) with the build as the conversation instead.
func BuildReviewTarget(draft foldercontext.Target, sessionID string) foldercontext.Target {
	target := draft
	target.ConversationID, target.DraftID = BuildReviewPrefix+strings.TrimSpace(sessionID), ""
	return target
}

// ConfigureBuildReviews is wired once at host construction, beside
// ConfigureConversationReviews. The guard validates the build session behind a
// build review; the lease is the build's own one-request-at-a-time gate.
func (s *FolderObservationService) ConfigureBuildReviews(
	guard func(context.Context, FolderOffer) error,
	lease func(foldercontext.Target) (func(), bool),
) {
	s.buildGuard, s.buildLease = guard, lease
	s.digest.conversationSource = s.reviewSourceReason
	s.digest.conversationLease = s.acquireReview
}

type folderReviewWorkspaceNameKey struct{}

// WithFolderReviewWorkspaceName names the new workspace for a review prepared
// with ctx: the plan, its digest and the run all use it. Empty names it after
// the folder, as Home's card does.
func WithFolderReviewWorkspaceName(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, folderReviewWorkspaceNameKey{}, strings.TrimSpace(name))
}

func folderReviewWorkspaceName(ctx context.Context) string {
	name, _ := ctx.Value(folderReviewWorkspaceNameKey{}).(string)
	return name
}

// ReviewBuild makes or reuses the canonical offer a build's Set it up runs on.
// draft is the build's selection target. The first time, the held selection is
// moved to the build's review target, exactly as a composer draft's selection
// moves to its saved conversation; a later Set it up (a Retry) reviews from
// there, and a folder attached again re-binds a confirmed run to the new pick.
// It decides nothing: StartBuildSetup does.
func (s *FolderObservationService) ReviewBuild(ctx context.Context, draft foldercontext.Target, sessionID, observationID, candidateID, workspaceName string) (FolderOfferView, error) {
	if !draft.Valid() || draft.DraftID == "" || strings.TrimSpace(sessionID) == "" {
		return FolderOfferView{}, ErrFolderSelection
	}
	review := BuildReviewTarget(draft, sessionID)
	source := draft
	if _, _, err := s.resolve(ctx, draft, observationID); err != nil {
		// Already moved by an earlier Set it up; anything else is lost.
		if _, _, boundErr := s.resolve(ctx, review, observationID); boundErr != nil {
			return FolderOfferView{}, err
		}
		source = review
	}
	view, err := s.Review(WithFolderReviewWorkspaceName(ctx, workspaceName), source, review.ConversationID, observationID, candidateID)
	if err != nil {
		return view, err
	}
	if source == draft {
		s.BindSaved(draft, observationID, review.ConversationID)
	}
	return view, nil
}

// ResumeBuildFolderPick keeps a build's original folder snapshot on a verified
// re-pick. The caller holds the build attachment lease and has not replaced its
// canonical folder yet. Nil means a different folder, not recovery authority.
func (s *FolderObservationService) ResumeBuildFolderPick(ctx context.Context, draft foldercontext.Target, sessionID, originalID, pickedID string) (*FolderOfferView, error) {
	held, _, err := s.resolveHeld(ctx, draft, pickedID)
	if err != nil {
		return nil, err
	}
	target := BuildReviewTarget(draft, sessionID)
	doc, err := s.digest.store.Read(ctx, draft.UserID)
	if err != nil {
		return nil, err
	}
	var found *FolderOffer
	for _, offer := range doc.Offers {
		review := offer.ConversationReview
		if openFolderReview(offer) && review != nil && review.Target == target && review.ObservationID == originalID {
			if found != nil {
				return nil, ErrFolderReviewPending
			}
			copy := offer
			found = &copy
		}
	}
	if found == nil {
		return nil, nil
	}
	return s.resumeReviewPick(ctx, held, target, *found, "")
}

// BuildFolderReviewState exposes only whether this draft has a canonical open
// review and a safe loss reason. Its private paths never enter the pane's view.
func (s *FolderObservationService) BuildFolderReviewState(ctx context.Context, target foldercontext.Target, observationID string) (bool, string) {
	if !IsBuildReview(target) {
		if !strings.HasPrefix(target.DraftID, BuildReviewPrefix) {
			return false, ""
		}
		target = BuildReviewTarget(target, strings.TrimPrefix(target.DraftID, BuildReviewPrefix))
	}
	doc, err := s.digest.store.Read(ctx, target.UserID)
	if err != nil {
		return false, ""
	}
	for _, offer := range doc.Offers {
		if openFolderReview(offer) && offer.ConversationReview != nil && offer.ConversationReview.Target == target && offer.ConversationReview.ObservationID == observationID {
			_, reason := s.reviewSourceReason(offer)
			return true, string(reason)
		}
	}
	return false, ""
}

// CloseBuildFolderReviews retires undecided reviews after the draft owner has
// detached, discarded or created its workspace. Accepted work is never undone.
func (s *FolderObservationService) CloseBuildFolderReviews(ctx context.Context, userID, sessionID, observationID string) error {
	doc, err := s.digest.store.Read(ctx, userID)
	if err != nil {
		return err
	}
	for _, offer := range doc.Offers {
		if BuildSessionOf(offer) == sessionID && offer.Status == FolderOfferPending && offer.DecidedAt == nil &&
			(observationID == "" || offer.ConversationReview.ObservationID == observationID) {
			if err := s.CloseReview(ctx, offer.ConversationReview.Target, offer.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

// StartBuildSetup is Set it up: Home's own StartSetup on the build's offer,
// held to the plan digest the user confirmed.
func (s *FolderObservationService) StartBuildSetup(ctx context.Context, userID, offerID, planDigest, requestID, entryName, brief string) (FolderOfferView, error) {
	if s == nil || s.digest == nil {
		return FolderOfferView{}, ErrFolderOutcomeUnavailable
	}
	return s.digest.StartSetup(ctx, userID, offerID, FolderSetupInput{RequestID: requestID, PlanDigest: planDigest, EntryName: entryName, Brief: brief})
}

// ReadBuildSetup projects a build's offer and its run, for the build that owns
// it only.
func (s *FolderObservationService) ReadBuildSetup(ctx context.Context, review foldercontext.Target, offerID string) (*FolderOfferView, error) {
	if !IsBuildReview(review) {
		return nil, ErrFolderOfferNotFound
	}
	return s.ReadReview(ctx, review, offerID)
}

// BuildSessionOf names the workspace build that owns an offer's review, or ""
// for an offer no build owns.
func BuildSessionOf(offer FolderOffer) string {
	if offer.ConversationReview == nil || !IsBuildReview(offer.ConversationReview.Target) {
		return ""
	}
	return strings.TrimPrefix(offer.ConversationReview.Target.ConversationID, BuildReviewPrefix)
}

// ErrBuildSetupNotOwned refuses a build guard's question about a review no
// build session vouches for.
var ErrBuildSetupNotOwned = errors.New("personal assistant: this setup does not belong to the build")
