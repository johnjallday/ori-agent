package personalassistant

import (
	"context"
	"path/filepath"
	"reflect"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
)

func homePathEntry(offer FolderOffer, root string) folderPathEntry {
	candidate := root
	if !offer.Subject.IsRoot && offer.Subject.RelPath != "" {
		candidate = filepath.Join(root, offer.Subject.RelPath)
	}
	return folderPathEntry{RootPath: root, RootIdentity: offer.RootIdentity, CandidatePath: candidate, CandidateIdentity: offer.SubjectIdentity}
}

func (s *FolderDigestService) rememberHomePath(ctx context.Context, userID string, offer FolderOffer, root string) error {
	s.rememberPath(offer.ID, root)
	if !openFolderReview(offer) || offer.ConversationReview != nil || offer.RootIdentity == "" || offer.SubjectIdentity == "" {
		return nil // platforms without witnesses keep only their live hold
	}
	entry := homePathEntry(offer, root)
	entry.SavedAt = s.now()
	if reason := s.validateReviewPath(offer, entry); reason != "" {
		return folderPickError{reason: reason, cause: ErrFolderPathLost}
	}
	return s.store.saveFolderPath(ctx, userID, offer.ID, entry)
}

// The caller supplies the canonical store's user, never an inferred local user.
func (s *FolderDigestService) homeRootPath(userID string, offer FolderOffer) (string, FolderContinuationReason) {
	s.mu.Lock()
	root, held := s.paths[offer.ID]
	s.mu.Unlock()
	if held {
		// Legacy in-process offers had no directory witnesses. They retain the
		// old live hold, but cannot acquire persistent recovery authority.
		if offer.RootIdentity == "" || offer.SubjectIdentity == "" {
			canonical, err := s.deps.ValidateRoot(root)
			if err == nil && canonical == root && FolderKey(root) == offer.FolderKey {
				return root, ""
			}
			return "", FolderContinuationMoved
		}
		if reason := s.validateReviewPath(offer, homePathEntry(offer, root)); reason != "" {
			return "", reason
		}
		return root, ""
	}
	var chipReason FolderContinuationReason
	if offer.Chip != "" && offer.RootIdentity != "" && offer.SubjectIdentity != "" {
		if path, err := s.chipPath(offer.Chip); err == nil {
			if canonical, err := s.deps.ValidateRoot(path); err == nil {
				chipReason = s.validateReviewPath(offer, homePathEntry(offer, canonical))
				if chipReason == "" {
					s.rememberPath(offer.ID, canonical)
					return canonical, ""
				}
			} else {
				chipReason = FolderContinuationMoved
			}
		}
	}
	entry, found, err := s.store.readFolderPath(context.Background(), userID, offer)
	if err != nil {
		return "", FolderContinuationLost
	}
	if !found {
		if chipReason != "" {
			return "", chipReason
		}
		return "", FolderContinuationLost
	}
	if reason := s.validateReviewPath(offer, entry); reason != "" {
		return "", reason
	}
	s.rememberPath(offer.ID, entry.RootPath)
	return entry.RootPath, ""
}

// A Home re-pick has no conversation reference. Recover only a unique open
// Home offer with both original directory witnesses; never steal a chat/build
// review or reset its run/choices because another surface scanned the folder.
func (s *FolderDigestService) resumeHomePick(ctx context.Context, userID string, doc FolderDigestDocument, result folderdigest.Result, root, identity string, paused bool) (*FolderOfferView, error) {
	var matched *FolderOffer
	var candidatePath string
	changed := folderPickError{reason: FolderContinuationChanged, cause: ErrFolderPathLost}
	for _, prior := range doc.Offers {
		if prior.ConversationReview != nil || !openFolderReview(prior) {
			continue
		}
		if prior.RootIdentity == "" || identity != prior.RootIdentity {
			if FolderKey(root) == prior.FolderKey {
				return nil, changed
			}
			continue
		}
		candidates := append([]folderdigest.Candidate{result.RootCandidate()}, result.Candidates...)
		seen := make(map[string]bool)
		path := ""
		for _, candidate := range candidates {
			if candidate.Path == "" || seen[candidate.Path] {
				continue
			}
			seen[candidate.Path] = true
			canonical, err := s.deps.ValidateRoot(candidate.Path)
			witness, witnessErr := portfolioDirectoryIdentity(candidate.Path)
			if err != nil || canonical != candidate.Path || witnessErr != nil {
				return nil, changed
			}
			if prior.SubjectIdentity != "" && witness == prior.SubjectIdentity && candidate.IsRoot == prior.Subject.IsRoot {
				if path != "" {
					return nil, changed
				}
				path = candidate.Path
			} else if FolderKey(candidate.Path) == prior.Subject.Key {
				return nil, changed
			}
		}
		if path == "" {
			return nil, changed
		}
		if matched != nil {
			return nil, ErrFolderReviewPending
		}
		copy := prior
		matched, candidatePath = &copy, path
	}
	if matched == nil {
		return nil, nil
	}
	if s.isRunning(matched.ID) {
		return nil, ErrFolderScanBusy
	}
	rel := ""
	if candidatePath != root {
		var err error
		rel, err = filepath.Rel(root, candidatePath)
		if err != nil {
			return nil, changed
		}
	}
	updated, err := s.store.Mutate(ctx, userID, func(doc *FolderDigestDocument) error {
		current := doc.Offer(matched.ID)
		if current == nil || !reflect.DeepEqual(*current, *matched) {
			return ErrConflict
		}
		current.FolderKey, current.Subject.Key, current.Subject.RelPath = FolderKey(root), FolderKey(candidatePath), rel
		for i := range current.Queue {
			current.Queue[i].Key = FolderKey(filepath.Join(root, current.Queue[i].RelPath))
		}
		if reason := s.validateReviewPath(*current, homePathEntry(*current, root)); reason != "" {
			return folderPickError{reason: reason, cause: ErrFolderPathLost}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	offer := *updated.Offer(matched.ID)
	if err := s.rememberHomePath(ctx, userID, offer, root); err != nil {
		return nil, err
	}
	view := s.viewFor(ctx, userID, offer, paused)
	return &view, nil
}
