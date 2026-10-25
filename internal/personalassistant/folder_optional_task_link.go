package personalassistant

import (
	"context"
	"errors"
)

// LinkOptionalFirstTask records only a server-verified exact task association.
// It does not claim that the task ran or grant suggestion execution permission.
func (s *FolderDigestService) LinkOptionalFirstTask(ctx context.Context, userID, workspaceID, taskID string) error {
	if s == nil || s.store == nil || s.deps.FirstTaskView == nil || workspaceID == "" || taskID == "" {
		return ErrFirstLookNotFound
	}
	view, ok := s.deps.FirstTaskView(ctx, workspaceID)
	if !ok || view.WorkspaceID != workspaceID || view.TaskID != taskID {
		return ErrFirstLookNotFound
	}
	_, err := s.store.Mutate(ctx, userID, func(doc *FolderDigestDocument) error {
		found := false
		for i := range doc.Offers {
			offer := &doc.Offers[i]
			if offer.Status != FolderOfferResolved || offer.Outcome == nil || offer.Outcome.Kind != FolderChoiceProject || offer.Outcome.WorkspaceID != workspaceID {
				continue
			}
			found = true
			if offer.Outcome.FirstTaskID != "" && offer.Outcome.FirstTaskID != taskID {
				return ErrFirstLookNotFound
			}
			if prior := offer.Outcome.FirstLook; prior != nil && prior.TaskID != "" && prior.TaskID != taskID {
				return ErrFirstLookNotFound
			}
			offer.Outcome.FirstTaskID = taskID
		}
		if !found {
			return errFolderReplay
		}
		return nil
	})
	if errors.Is(err, errFolderReplay) {
		return nil
	}
	return err
}

func (s *FolderDigestService) linkOptionalLook(ctx context.Context, userID, offerID string) {
	if s.deps.FirstTaskView == nil {
		return
	}
	doc, err := s.store.Read(ctx, userID)
	if err != nil {
		return
	}
	offer := doc.Offer(offerID)
	if offer == nil || offer.Outcome == nil {
		return
	}
	view, ok := s.deps.FirstTaskView(ctx, offer.Outcome.WorkspaceID)
	if ok {
		_ = s.LinkOptionalFirstTask(ctx, userID, offer.Outcome.WorkspaceID, view.TaskID)
	}
}

func ownsFirstLook(offer FolderOffer, taskID string) bool {
	if offer.Outcome == nil || taskID == "" {
		return false
	}
	if offer.Outcome.FirstTaskID != "" {
		return offer.Outcome.FirstTaskID == taskID
	}
	// Old saved reports had only the automatic-start record. Preserve them,
	// but never ignore a recorded mismatch or use this fallback for new links.
	prior := offer.Outcome.FirstLook
	return prior != nil && (prior.TaskID == "" || prior.TaskID == taskID)
}
