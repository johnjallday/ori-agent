package personalassistant

import (
	"context"
	"errors"

	"github.com/johnjallday/ori-agent/internal/secondbrain"
)

type InboxWorkspace struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}
type InboxTickets interface {
	Choices(owner string) []InboxWorkspace
	Available(owner, workspaceID string) bool
	Confirm(owner, workspaceID, key, title string) (string, error)
	HasReceipt(owner, workspaceID, key, ticketID string) bool
}

type InboxDecision struct {
	SourceRef string `json:"source_ref"`
	Name      string `json:"name"`
	ActionID  string `json:"action_id"`
	Revision  string `json:"revision"`
	Decision  string `json:"decision"`
	Workspace string `json:"workspace_id,omitempty"`
}

// The sidecar lock pins the owner's current connection through revalidation,
// claim, keyed create and receipt. SQLite arbitrates the durable first choice;
// no DB transaction spans the call into the workspace store. Mutate normally
// replaces the sidecar on nil: a private sentinel releases it *without* a
// sidecar write (the decision belongs in the ledger).
func (s *SecondBrainService) DecideInbox(ctx context.Context, user string, in InboxDecision) (secondbrain.InboxAction, error) {
	var result secondbrain.InboxAction
	if !secondbrain.ValidItemID(in.SourceRef) || !secondbrain.ValidItemID(in.ActionID) || !secondbrain.ValidItemID(in.Revision) || (in.Decision != "accept" && in.Decision != "dismiss") || (in.Decision == "accept") != (in.Workspace != "") || (in.Workspace != "" && !secondbrain.ValidRequestID(in.Workspace)) {
		return result, ErrValidation
	}
	if _, err := secondbrain.PreviewRel(secondbrain.PreviewInbox, in.Name); err != nil {
		return result, ErrValidation
	}
	ledger, bridge := s.deps.ReviewLedger, s.deps.InboxTickets
	if ledger == nil || bridge == nil {
		return result, ErrSecondBrainUnavailable
	}
	complete := errors.New("decision handled; sidecar unchanged")
	_, err := s.mutate(ctx, user, func(doc *SecondBrainDocument) error {
		source := doc.Connected()
		if source == nil || !source.CreatedByOri() || browseReference(*doc) != in.SourceRef {
			return ErrSecondBrainBrowseStale
		}
		note, err := secondbrain.ReadPreview(source.Path, secondbrain.PreviewInbox, in.Name)
		if err != nil || note.Truncated || secondbrain.ReviewRevision(note.Body) != in.Revision {
			return secondbrain.ErrReviewConflict
		}
		suggestions, err := s.reviewSuggestions(ctx, *doc, note)
		if err != nil {
			return err
		}
		var a secondbrain.InboxAction
		for _, item := range suggestions {
			if item.ID == in.ActionID {
				a = item
				break
			}
		}
		if a.ID == "" {
			return secondbrain.ErrReviewConflict
		}
		if a.State == "pending" && in.Decision == "accept" && !bridge.Available(user, in.Workspace) {
			return secondbrain.ErrReviewConflict
		}
		key := secondbrain.ReviewNoteKey(in.SourceRef, note.Rel)
		result, err = ledger.Decide(ctx, key, a, in.Workspace)
		if err != nil {
			return err
		}
		if result.State == "accepted" && !bridge.HasReceipt(user, result.Workspace, result.ID, result.Ticket) {
			return secondbrain.ErrReviewConflict
		}
		if result.State == "claiming" {
			if in.Decision != "accept" || !bridge.Available(user, result.Workspace) {
				return secondbrain.ErrReviewConflict
			}
			// The target returned by the claim, never the retry's requested target.
			id, err := bridge.Confirm(user, result.Workspace, a.ID, a.Title)
			if err != nil {
				return secondbrain.ErrReviewConflict
			}
			if err := ledger.Finish(ctx, key, result, id); err != nil {
				return err
			}
			result.State, result.Ticket = "accepted", id
		}
		return complete
	})
	if errors.Is(err, complete) {
		return result, nil
	}
	return secondbrain.InboxAction{}, err
}
