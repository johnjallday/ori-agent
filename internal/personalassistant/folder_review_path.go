package personalassistant

import (
	"context"
	"errors"
	"reflect"
	"strings"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
)

// folderPickError carries only a bounded reason, never a filesystem error/path.
type folderPickError struct {
	reason FolderContinuationReason
	cause  error
}

func (e folderPickError) Error() string { return e.cause.Error() }
func (e folderPickError) Unwrap() error { return e.cause }

// FolderPickReason is the safe reason code for a needs_pick response. Older
// errors carry no witness, and must not invent an expiry or restart explanation.
func FolderPickReason(err error) FolderContinuationReason {
	var pick folderPickError
	if errors.As(err, &pick) {
		switch pick.reason {
		case FolderContinuationMoved, FolderContinuationChanged, FolderContinuationExpired:
			return pick.reason
		}
	}
	return FolderContinuationLost
}

// A reconstructed hold proves just one previously reviewed candidate. It cannot
// supply a tree, select another candidate, or manufacture a fresh observation.
type heldReviewPath struct {
	offerID       string
	candidateID   string
	candidatePath string
}

func (s *FolderObservationService) resolve(ctx context.Context, target foldercontext.Target, id string) (heldFolderObservation, FolderContinuationReason, error) {
	held, reason, err := s.resolveForReview(ctx, target, id, "")
	if err != nil && target.ConversationID == "" && strings.HasPrefix(target.DraftID, BuildReviewPrefix) {
		// A build's Create/plan still names its draft target after its explicit
		// review moved the hold. Only that exact build's guard may restore it.
		review := BuildReviewTarget(target, strings.TrimPrefix(target.DraftID, BuildReviewPrefix))
		if s != nil && s.digest != nil {
			if doc, readErr := s.digest.store.Read(ctx, target.UserID); readErr == nil {
				for _, offer := range doc.Offers {
					if !openFolderReview(offer) || offer.ConversationReview == nil || offer.ConversationReview.Target != review || offer.ConversationReview.ObservationID != id {
						continue
					}
					if _, loss := s.reviewSourceReason(offer); loss != "" {
						return heldFolderObservation{}, loss, folderPickError{reason: loss, cause: ErrFolderSelection}
					}
					return s.resolveForReview(ctx, review, id, offer.ID)
				}
			}
		}
	}
	return held, reason, err
}

func (s *FolderObservationService) resolveForReview(ctx context.Context, target foldercontext.Target, id, offerID string) (heldFolderObservation, FolderContinuationReason, error) {
	held, reason, err := s.resolveHeld(ctx, target, id)
	if err == nil && held.reheld == nil {
		return held, reason, nil
	}
	if s == nil || s.digest == nil || s.digest.store == nil || !target.Valid() {
		return heldFolderObservation{}, FolderContinuationLost, ErrFolderSelection
	}
	// Never borrow a selection belonging to another owner, even with the same ID.
	s.mu.Lock()
	existing, exists := s.selections[id]
	s.mu.Unlock()
	if exists && existing.target != target {
		return heldFolderObservation{}, FolderContinuationLost, ErrFolderSelection
	}
	doc, readErr := s.digest.store.Read(ctx, target.UserID)
	if readErr != nil {
		return heldFolderObservation{}, FolderContinuationLost, ErrFolderSelection
	}
	for _, offer := range doc.Offers {
		review := offer.ConversationReview
		if !openFolderReview(offer) || review == nil || review.Target != target || review.ObservationID != id || (offerID != "" && offer.ID != offerID) {
			continue
		}
		guard, _ := s.reviewAuthority(target)
		if guard == nil || guard(ctx, offer) != nil {
			return heldFolderObservation{}, FolderContinuationLost, ErrFolderSelection
		}
		entry, found, pathErr := s.digest.store.readFolderPath(ctx, target.UserID, offer)
		if pathErr != nil || !found {
			return heldFolderObservation{}, FolderContinuationLost, ErrFolderSelection
		}
		if loss := s.digest.validateReviewPath(offer, entry); loss != "" {
			return heldFolderObservation{}, loss, folderPickError{reason: loss, cause: ErrFolderPathLost}
		}
		binding, bindingErr := s.digest.store.Binding(ctx, target.UserID)
		if bindingErr != nil || binding.HQWorkspaceID != target.WorkspaceID {
			return heldFolderObservation{}, FolderContinuationLost, ErrFolderSelection
		}
		held = heldFolderObservation{
			target: target, binding: binding, root: entry.RootPath, identity: entry.RootIdentity, saved: true,
			observation: foldercontext.Observation{ID: id, ScannedAt: s.digest.now()},
			reheld:      &heldReviewPath{offerID: offer.ID, candidateID: review.CandidateID, candidatePath: entry.CandidatePath},
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if other, ok := s.selections[id]; ok && other.target != target {
			return heldFolderObservation{}, FolderContinuationLost, ErrFolderSelection
		}
		// Do not evict any other owner's live preview to restore this review.
		s.pruneLocked()
		if _, exists := s.selections[id]; !exists && len(s.selections) >= folderSelectionsMax {
			return heldFolderObservation{}, FolderContinuationLost, ErrFolderSelectionLimit
		}
		s.selections[id] = held
		return held, "", nil
	}
	if err == nil { // a cached reconstructed hold whose review has since ended
		return heldFolderObservation{}, FolderContinuationLost, ErrFolderSelection
	}
	return heldFolderObservation{}, reason, err
}

func (s *FolderDigestService) validateReviewPath(offer FolderOffer, entry folderPathEntry) FolderContinuationReason {
	for _, directory := range []struct{ path, identity, key string }{
		{entry.RootPath, entry.RootIdentity, offer.FolderKey},
		{entry.CandidatePath, entry.CandidateIdentity, offer.Subject.Key},
	} {
		canonical, err := s.deps.ValidateRoot(directory.path)
		if err != nil || canonical != directory.path {
			return FolderContinuationMoved
		}
		identity, err := portfolioDirectoryIdentity(canonical)
		if err != nil || identity != directory.identity || FolderKey(canonical) != directory.key {
			return FolderContinuationChanged
		}
	}
	return ""
}

// prepareReheldReview uses the canonical disclosure, never fabricated scan
// evidence. A new structure or another candidate requires an explicit pick.
func (s *FolderObservationService) prepareReheldReview(ctx context.Context, held heldFolderObservation, target foldercontext.Target, candidateID string) (FolderOffer, error) {
	if held.target != target || held.reheld.candidateID != candidateID {
		return FolderOffer{}, ErrFolderSelection
	}
	offer, err := s.digest.StoredOffer(ctx, target.UserID, held.reheld.offerID)
	if err != nil {
		return FolderOffer{}, err
	}
	if !openFolderReview(offer) || offer.ConversationReview == nil || offer.ConversationReview.Target != target ||
		offer.ConversationReview.ObservationID != held.observation.ID {
		return FolderOffer{}, ErrFolderSelection
	}
	if adjustment := folderStructureAdjustment(ctx); adjustment != nil && !reflect.DeepEqual(adjustment, offer.ConversationReview.Structure) {
		return FolderOffer{}, ErrFolderSelection
	}
	placement := folderReviewPlacement(ctx)
	if (placement.Operation != "" && placement.Operation != offer.ConversationReview.Operation) ||
		(placement.DestinationID != "" && placement.DestinationID != offer.ConversationReview.DestinationID) {
		return FolderOffer{}, ErrFolderSelection
	}
	if name := folderReviewWorkspaceName(ctx); name != "" {
		offer.ConversationReview.WorkspaceName = name
	}
	if offer.Status == FolderOfferPending {
		s.refreshReviewDestination(ctx, target.UserID, &offer)
		if offer.Structure != nil && offer.Structure.Group.Type == FolderGroupPlain {
			offer.ConversationReview.Destination, offer.ConversationReview.DestinationBlocker = nil, nil
			offer.ConversationReview.DestinationUnavailable = false
		}
	}
	return offer, nil
}
