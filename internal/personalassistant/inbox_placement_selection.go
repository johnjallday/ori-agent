package personalassistant

import (
	"context"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
)

// copyInboxPlacementHold is a host-only transfer, called after canonical source
// admission under its conversation lease. It neither scans nor extends a hold's
// TTL. The caller must persist the returned handle in the bound build; this
// method alone is not permission to create, grant, or run anything.
func (s *FolderObservationService) copyInboxPlacementHold(ctx context.Context, source foldercontext.Target, observationID, buildID string) (*foldercontext.Observation, error) {
	if source.ConversationID == "" || source.DraftID != "" || IsBuildReview(source) || !placementReference(buildID) {
		return nil, ErrFolderSelection
	}
	held, _, err := s.resolve(ctx, source, observationID)
	if err != nil {
		return nil, err
	}
	target := source
	target.ConversationID, target.DraftID = "", "build:"+buildID
	s.mu.Lock()
	s.pruneLocked()
	current, exists := s.selections[observationID]
	if !exists || current.target != source || current.binding != held.binding || current.identity != held.identity {
		s.mu.Unlock()
		return nil, ErrFolderSelection
	}
	count := 0
	for id, selection := range s.selections {
		if selection.target == target && selection.placementSourceID == observationID {
			s.mu.Unlock()
			return s.Resolve(ctx, target, id)
		}
		if selection.target.UserID == source.UserID {
			count++
		}
	}
	if count >= folderSelectionsPerOwner || len(s.selections) >= folderSelectionsMax {
		s.mu.Unlock()
		return nil, ErrFolderSelectionLimit
	}
	id := s.digest.deps.NewID()
	if _, exists := s.selections[id]; exists || !placementReference(id) {
		s.mu.Unlock()
		return nil, ErrFolderSelection
	}
	held.target, held.placementSourceID = target, observationID
	held.observation.ID = id
	held.saved = true
	s.selections[id] = held
	s.mu.Unlock()
	return s.Resolve(ctx, target, id)
}
