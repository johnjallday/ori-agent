package personalassistant

import "context"

// RecordInboxPlacementOutcome records a host-verified preparation receipt, never
// client input. The build/preparation owner must verify the actual workspace,
// membership and first scan before calling this seam. This is bookkeeping only:
// it does not resolve a selection, resume setup, learn a dossier or start a task.
// The transferred source stays fenced, including after build history is pruned.
func (s *FolderDigestService) RecordInboxPlacementOutcome(ctx context.Context, owner string, build WorkspaceBuildSession, outcome FolderOutcome) error {
	source, claim := build.PlacementSource, build.Preparation
	if s == nil || s.store == nil || source == nil || source.validate() != nil || source.Target.UserID != owner ||
		claim == nil || claim.Validate() != nil || outcome.WorkspaceID != claim.WorkspaceID || outcome.Kind != FolderChoiceProject ||
		outcome.Blueprint != "file-janitor" || outcome.Parent != nil || outcome.HomeRoute != "" || outcome.Remembered || outcome.Existing ||
		outcome.FirstTaskID != "" || outcome.FirstLook == nil || outcome.FirstLook.Started || outcome.FirstLook.TaskID != "" || outcome.FirstLook.At.IsZero() || outcome.FirstLook.Batch == nil ||
		outcome.FirstLook.Batch.validate() != nil || outcome.FirstLook.Batch.Outcome == FolderFirstBatchFailed {
		return ErrFolderWorkspaceRefused
	}
	matches := func(offer *FolderOffer) bool {
		if offer == nil || offer.PlacementHandoff == nil || offer.ConversationReview == nil {
			return false
		}
		handoff, review := offer.PlacementHandoff, offer.ConversationReview
		return handoff.DraftSaved && handoff.BuildID == build.ID && handoff.Revision == source.Revision && handoff.PlanDigest == source.PlanDigest &&
			review.Target == source.Target && review.ObservationID == source.ObservationID &&
			(offer.Status == FolderOfferClosed || offer.Status == FolderOfferResolved)
	}
	prior, err := s.StoredOffer(ctx, owner, source.OfferID)
	if err != nil {
		return err
	}
	if !matches(&prior) {
		return ErrFolderWorkspaceRefused
	}
	// A receipt is historical; do not rewrite it or produce another message on
	// repeated reads, including after a rename or a later filing decision.
	if prior.Status == FolderOfferResolved && prior.Outcome != nil {
		if prior.Outcome.WorkspaceID != claim.WorkspaceID {
			return ErrFolderWorkspaceRefused
		}
		return nil
	}
	now := s.now()
	_, err = s.store.Mutate(ctx, owner, func(doc *FolderDigestDocument) error {
		current := doc.Offer(source.OfferID)
		if !matches(current) {
			return ErrFolderWorkspaceRefused
		}
		if current.Outcome != nil {
			if current.Status != FolderOfferResolved || current.Outcome.WorkspaceID != claim.WorkspaceID {
				return ErrFolderWorkspaceRefused
			}
			return nil
		}
		current.Status, current.ResolvedAt, current.Choice = FolderOfferResolved, &now, FolderChoiceProject
		current.Outcome = &outcome
		// No DecidedAt/Setup run is invented for the original one-click route.
		doc.Receipts = append(doc.Receipts, FolderReceipt{RequestID: "placement-" + build.ID, OfferID: source.OfferID, Action: "inbox", At: now})
		pruneFolderDigest(doc)
		return nil
	})
	return err
}
