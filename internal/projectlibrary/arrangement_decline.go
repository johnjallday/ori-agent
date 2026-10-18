package projectlibrary

import (
	"context"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Decline closes one current unapplied proposal, retaining its inert history.
// It never resolves a model or rolls back already-confirmed effects. The same
// catalog CAS used by review/apply fences late model completions and old tabs.
func (s *CollectionReviewService) Decline(ctx context.Context, owner, homeID, id string, catalogRevision, recordRevision int64) (*ArrangementHistory, error) {
	if ctx.Err() != nil || !collectionLabel(id, 160) || catalogRevision < 1 || recordRevision < 1 {
		return nil, ErrConflict
	}
	authority, err := s.authority(owner, homeID)
	if err != nil {
		return nil, err
	}
	scope, err := s.store.authorizeCollectionOwner(owner, authority)
	if err != nil {
		return nil, err
	}
	doc, state, err := s.store.readSnapshot(scope)
	if err != nil {
		return nil, err
	}
	if doc.Revision != catalogRevision {
		return nil, ErrConflict
	}
	pin := arrangementBinding(state, authority)
	_, _, err = s.store.mutateWithHomePolicy(scope, catalogRevision, operation{key: newID(), action: "decline_arrangement", digest: queueDigest(scope, "decline_arrangement", id, "", recordRevision, nil)},
		func(state *workspace.AssistantProgramState, home *workspace.Workspace) bool {
			return ctx.Err() == nil && boundManager(state, home, authority) && arrangementBinding(state, authority) == pin
		},
		func(current *Document) (string, error) {
			index := arrangementIndex(*current, id)
			if index < 0 {
				return "", ErrConflict
			}
			saved := &current.Arrangements[index]
			if saved.outcomeKind() != "proposal" || saved.Record.Revision != recordRevision || saved.CatalogRevision != current.Revision || saved.BindingDigest != pin || saved.CollectionReceipt != nil {
				return "", ErrConflict
			}
			switch saved.Record.State {
			case "draft", "needs_more_evidence", "awaiting_user_review":
			default:
				return "", ErrConflict
			}
			at := s.store.now().UTC()
			if ctx.Err() != nil || at.Before(saved.Record.UpdatedAt) {
				return "", ErrConflict
			}
			saved.Record.State = "cancelled"
			saved.Record.UpdatedAt = at
			saved.CatalogRevision = current.Revision + 1
			if saved.Record.Validate() != nil {
				return "", ErrConflict
			}
			if _, err := arrangementHistory(*saved, -1); err != nil {
				return "", err
			}
			return id, nil
		})
	if err != nil {
		return nil, err
	}
	return s.store.ReadArrangementHistory(ctx, scope, id)
}
