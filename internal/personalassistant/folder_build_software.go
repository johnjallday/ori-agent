package personalassistant

import (
	"context"
	"strings"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
)

// ReviewBuildSoftware is an explicit request to the existing software review
// owner. Unlike BuildPlan, it may inspect the bounded project evidence. It
// decides no offer and starts no setup, task, model call or resource creation.
func (s *FolderObservationService) ReviewBuildSoftware(ctx context.Context, draft foldercontext.Target, sessionID, observationID, candidateID, workspaceName, requestID string, inspect bool) (FolderOfferView, error) {
	view, err := s.ReviewBuild(ctx, draft, sessionID, observationID, candidateID, workspaceName)
	if err != nil {
		return view, err
	}
	input := FolderSoftwareReviewInput{RequestID: requestID}
	if name := strings.TrimSpace(workspaceName); name != "" {
		input.WorkspaceName = &name
	}
	if !inspect {
		return s.digest.AdjustSoftware(ctx, draft.UserID, view.ID, input)
	}
	return s.digest.ReviewSoftware(ctx, draft.UserID, view.ID, input)
}

// CloseBuildSoftwareReviews retires only undecided software reviews belonging
// to this exact build. It records no decline or preference and cannot close an
// accepted run. The build controller calls it after abandoning that draft.
func (s *FolderObservationService) CloseBuildSoftwareReviews(ctx context.Context, userID, sessionID string) error {
	doc, err := s.digest.store.Read(ctx, userID)
	if err != nil {
		return err
	}
	for _, offer := range doc.Offers {
		if BuildSessionOf(offer) != sessionID || !isSoftwareOffer(offer) || offer.Status != FolderOfferPending || offer.DecidedAt != nil {
			continue
		}
		if err := s.CloseReview(ctx, offer.ConversationReview.Target, offer.ID); err != nil {
			return err
		}
	}
	return nil
}

// ReadBuildSoftwareResult recovers an exact outcome from the existing source
// journal after a settled build has dropped its live Setup claim. Names,
// reserved IDs and a resource marker alone are never completion proof.
func (s *FolderObservationService) ReadBuildSoftwareResult(ctx context.Context, userID, sessionID, workspaceID string) (*FolderOfferView, error) {
	doc, err := s.digest.store.Read(ctx, userID)
	if err != nil {
		return nil, err
	}
	var matched *FolderOffer
	for i := range doc.Offers {
		offer := &doc.Offers[i]
		if BuildSessionOf(*offer) != sessionID || !isSoftwareOffer(*offer) || offer.Status != FolderOfferResolved || offer.Outcome == nil || offer.Outcome.WorkspaceID != workspaceID || offer.DecidedAt == nil {
			continue
		}
		if matched != nil {
			return nil, ErrFolderOutcomeUnavailable
		}
		matched = offer
	}
	if matched == nil {
		return nil, ErrFolderOutcomeUnavailable
	}
	return s.ReadBuildSetup(ctx, matched.ConversationReview.Target, matched.ID)
}

// BuildFolderPlanFromReview projects a proven, owner-read offer. In particular
// it does not fabricate software evidence on an ephemeral metadata-only offer.
func BuildFolderPlanFromReview(view *FolderOfferView) *BuildFolderPlan {
	if view == nil || view.Plan == nil || view.Plan.Digest == "" {
		return nil
	}
	p := view.Plan
	out := &BuildFolderPlan{
		SoftwareReview:    p.Intent.Software != nil,
		FirstTaskDeferred: p.OptionalTask || p.Intent.Software != nil,
		Lines:             append([]FolderPlanLine(nil), p.Lines...), Digest: p.Digest,
		Placement: p.Intent.Placement, Evidence: view.Reason, Ready: true,
		Visual: CloneFolderSetupVisual(p.Visual), Readiness: CloneFolderSetupVisual(view.Readiness),
		BriefDefault: p.BriefDefault,
	}
	if p.Hire != nil {
		hire := *p.Hire
		out.Hire = &hire
	}
	if destination := p.Destination; destination != nil && (destination.Status == "new" || destination.Status == "existing") {
		out.Home = &BuildFolderHome{Name: destination.Name, Status: destination.Status}
	}
	return out
}
