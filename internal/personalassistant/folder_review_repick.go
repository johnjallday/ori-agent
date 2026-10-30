package personalassistant

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
	"github.com/johnjallday/ori-agent/internal/folderdigest"
)

// ResumeReviewPick is called under the owner's selection lease, before retiring
// its canonical folder binding. A fresh, explicit pick may relocate the same
// directory; it cannot adopt a replacement. Nil leaves the pick to the existing
// selection workflow: a different folder, or a separately fenced review owner.
func (s *FolderObservationService) ResumeReviewPick(ctx context.Context, target foldercontext.Target, offerID, observationID string) (*FolderOfferView, error) {
	held, _, err := s.resolveHeld(ctx, target, observationID)
	if err != nil {
		return nil, err
	}
	prior, err := s.digest.StoredOffer(ctx, target.UserID, offerID)
	if err != nil {
		return nil, err
	}
	// Arrangements and placement handoffs have their own recovery fences.
	// Leave the fresh preview to that existing workflow; do not rewrite its
	// canonical owner or prevent it from re-attaching an unchanged folder.
	if prior.Structure.Arranged() || prior.PlacementHandoff != nil {
		return nil, nil
	}
	return s.resumeReviewPick(ctx, held, target, prior, "")
}

func (s *FolderObservationService) resumeReviewPick(ctx context.Context, held heldFolderObservation, target foldercontext.Target, prior FolderOffer, candidateID string) (*FolderOfferView, error) {
	review := prior.ConversationReview
	if held.reheld != nil || !openFolderReview(prior) || review == nil || review.Target != target || held.observation.ID == review.ObservationID {
		return nil, nil
	}
	candidate, matched, err := s.matchReviewPick(held, prior, candidateID)
	if err != nil || !matched {
		return nil, err
	}
	// These have another canonical intention/placement owner; a plain re-pick
	// must not rewrite its fence or manufacture a second confirmation path.
	if prior.Structure.Arranged() || prior.PlacementHandoff != nil {
		return nil, ErrFolderReviewPending
	}
	guard, _ := s.reviewAuthority(target)
	if guard == nil || guard(ctx, prior) != nil {
		return nil, ErrFolderSelection
	}
	if s.digest.isRunning(prior.ID) {
		return nil, ErrFolderScanBusy
	}
	s.mu.Lock()
	other, exists := s.selections[review.ObservationID]
	s.mu.Unlock()
	if exists && other.target != target {
		return nil, ErrFolderSelection
	}
	rel := ""
	if !candidate.IsRoot {
		rel, err = filepath.Rel(held.root, candidate.Path)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, ErrFolderSelection
		}
	}
	entry := folderPathEntry{RootPath: held.root, RootIdentity: held.identity, CandidatePath: candidate.Path,
		CandidateIdentity: review.CandidateIdentity, SavedAt: s.digest.now()}
	updated, err := s.digest.store.Mutate(ctx, target.UserID, func(doc *FolderDigestDocument) error {
		current := doc.Offer(prior.ID)
		if current == nil || !reflect.DeepEqual(*current, prior) {
			return ErrConflict
		}
		current.FolderKey, current.Subject.Key = FolderKey(held.root), FolderKey(candidate.Path)
		current.Subject.RelPath = rel
		// Queue entries remain relative to this very same root. No scan facts,
		// names, choices, destination or confirmed run are replaced by the pick.
		for i := range current.Queue {
			current.Queue[i].Key = FolderKey(filepath.Join(held.root, current.Queue[i].RelPath))
		}
		if reason := s.digest.validateReviewPath(*current, entry); reason != "" {
			return folderPickError{reason: reason, cause: ErrFolderPathLost}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	result := *updated.Offer(prior.ID)
	if err := s.digest.store.saveFolderPath(ctx, target.UserID, result.ID, entry); err != nil {
		return nil, err
	}
	// Keep the original canonical observation reference. The newly scanned
	// preview is not substituted into old history, and the restored authority
	// is deliberately not fresh discussion evidence.
	s.mu.Lock()
	s.pruneLocked()
	old, exists := s.selections[review.ObservationID]
	if exists && old.target != target {
		s.mu.Unlock()
		return nil, ErrFolderSelection
	}
	// Consume only this request's fresh preview, never another owner's slot.
	if fresh, ok := s.selections[held.observation.ID]; ok && fresh.target == held.target {
		delete(s.selections, held.observation.ID)
	}
	if !exists && len(s.selections) >= folderSelectionsMax {
		s.mu.Unlock()
		return nil, ErrFolderSelectionLimit
	}
	s.selections[review.ObservationID] = heldFolderObservation{
		target: target, binding: held.binding, root: entry.RootPath, identity: entry.RootIdentity, saved: true,
		observation: foldercontext.Observation{ID: review.ObservationID, ScannedAt: s.digest.now()},
		reheld:      &heldReviewPath{offerID: result.ID, candidateID: review.CandidateID, candidatePath: entry.CandidatePath},
	}
	s.mu.Unlock()
	view := s.digest.viewFor(ctx, target.UserID, result, held.binding.Paused)
	return &view, nil
}

func (s *FolderObservationService) matchReviewPick(held heldFolderObservation, prior FolderOffer, candidateID string) (folderdigest.Candidate, bool, error) {
	changed := func() (folderdigest.Candidate, bool, error) {
		return folderdigest.Candidate{}, false, folderPickError{reason: FolderContinuationChanged, cause: ErrFolderPathLost}
	}
	if prior.RootIdentity == "" || held.identity != prior.RootIdentity {
		if FolderKey(held.root) == prior.FolderKey {
			return changed()
		}
		return folderdigest.Candidate{}, false, nil
	}
	var found folderdigest.Candidate
	matches := 0
	for _, shown := range held.observation.Projects {
		if candidateID != "" && shown.ID != candidateID {
			continue
		}
		candidate, ok := observedReviewCandidate(held, shown.ID)
		if !ok {
			continue
		}
		canonical, err := s.digest.deps.ValidateRoot(candidate.Path)
		identity, identityErr := portfolioDirectoryIdentity(candidate.Path)
		if err != nil || canonical != candidate.Path || identityErr != nil {
			return changed()
		}
		if prior.ConversationReview.CandidateIdentity != "" && identity == prior.ConversationReview.CandidateIdentity && candidate.IsRoot == prior.Subject.IsRoot {
			found, matches = candidate, matches+1
		} else if FolderKey(candidate.Path) == prior.Subject.Key {
			return changed()
		}
	}
	if matches == 1 {
		return found, true, nil
	}
	if matches > 1 || candidateID == "" {
		return changed()
	}
	return folderdigest.Candidate{}, false, nil
}
