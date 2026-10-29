package personalassistant

import (
	"context"
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
)

// isHandoffSetup says the offer's run was a single song's: the Home lists it
// and its manager opens it under the one Set up.
func isHandoffSetup(offer FolderOffer) bool {
	return offer.Portfolio == nil && offer.Setup != nil && offer.Setup.Intent.Portfolio && len(offer.Setup.Intent.OpensProjects) == 1
}

// ResolveHandoff completes a single song's setup once its run reported done,
// and only after the host re-read, from canonical state, that the Home is the
// one the run recorded, a child of it is this folder's, the two hold their
// reciprocal link, the Home keeps this offer's setup-open receipt, and the
// child's page resolves (FR16). The outcome is a project outcome, recorded as
// a single song's is, so the card, Home Today, the hire record and Needs you
// read it unchanged; it also names who opened the song.
func (s *FolderDigestService) ResolveHandoff(ctx context.Context, userID, offerID, requestID string) (FolderOfferView, error) {
	if s == nil || s.store == nil || s.deps.Handoff == nil {
		return FolderOfferView{}, ErrFolderOutcomeUnavailable
	}
	requestID = strings.TrimSpace(requestID)
	if requestID == "" || len(requestID) > folderRequestIDMax {
		return FolderOfferView{}, fmt.Errorf("%w: handoff request", ErrValidation)
	}
	binding, err := s.store.Binding(ctx, userID)
	if err != nil {
		return FolderOfferView{}, err
	}
	doc, err := s.store.Read(ctx, userID)
	if err != nil {
		return FolderOfferView{}, err
	}
	offer := doc.Offer(offerID)
	if offer == nil {
		return FolderOfferView{}, ErrFolderOfferNotFound
	}
	if receipt := doc.Receipt(requestID); receipt != nil {
		if receipt.OfferID != offerID || receipt.Action != "handoff" {
			return FolderOfferView{}, ErrFolderOfferDecided
		}
		return s.view(ctx, *offer, binding.Paused), nil
	}
	if offer.Status != FolderOfferAwaitingOutcome || offer.Choice != FolderChoiceProject || offer.DecidedAt == nil ||
		offer.CapabilitySuppressed || !isHandoffSetup(*offer) || strings.TrimSpace(offer.Setup.HomeID) == "" {
		return FolderOfferView{}, ErrFolderOfferDecided
	}
	row, ok := folderdigest.ProjectCapabilityFor(folderdigest.Shape(offer.Subject.Shape), offer.Subject.MarkerName, offer.Subject.DominantExtension)
	if !ok || row.Offer == nil {
		return FolderOfferView{}, ErrFolderOfferDecided
	}
	path, err := s.subjectPathOf(*offer)
	if err != nil {
		return FolderOfferView{}, err
	}
	verified, err := s.deps.Handoff.VerifiedHandoff(ctx, userID, offerID, offer.Setup.HomeID, path, *offer.DecidedAt)
	if err != nil || verified.WorkspaceID == "" || !strings.HasPrefix(verified.Route, "/workspaces/") ||
		!strings.HasPrefix(verified.HomeRoute, "/workspaces/") {
		return FolderOfferView{}, ErrFolderWorkspaceRefused
	}
	now := s.now()
	var resolved FolderOffer
	_, err = s.store.Mutate(ctx, userID, func(d *FolderDigestDocument) error {
		item := d.Offer(offerID)
		if item == nil || item.Status != FolderOfferAwaitingOutcome || item.Choice != FolderChoiceProject || !isHandoffSetup(*item) {
			return ErrFolderOfferDecided
		}
		item.Status, item.ResolvedAt = FolderOfferResolved, &now
		// The run's own receipt was read back from canonical state when it
		// finished (FR17); the verifier's is the fallback.
		receipt := finishedSetupReceipt(item)
		if len(receipt) == 0 {
			receipt = append([]FolderReceiptRow(nil), verified.Receipt...)
		}
		item.Outcome = &FolderOutcome{Kind: FolderChoiceProject, WorkspaceID: verified.WorkspaceID, Route: verified.Route,
			HomeRoute: verified.HomeRoute, Blueprint: row.Blueprint.BlueprintID, Parent: verified.Parent,
			Receipt: receipt, OpenedBy: verified.OpenedBy}
		d.Receipts = append(d.Receipts, FolderReceipt{RequestID: requestID, OfferID: offerID, Action: "handoff", At: now})
		pruneFolderDigest(d)
		resolved = *item
		return nil
	})
	if err != nil {
		return FolderOfferView{}, err
	}
	resolved = s.afterOutcome(ctx, userID, resolved)
	return s.view(ctx, resolved, binding.Paused), nil
}
