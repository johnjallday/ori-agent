package personalassistant

import (
	"context"
	"github.com/johnjallday/ori-agent/internal/secondbrain"
)

// Only a proven, explicitly opened note is considered. A recent capture
// receipt witnesses pending actions if the index was interrupted; reading
// never starts a look, writes a decision, or touches another folder.
func (s *SecondBrainService) reviewSuggestions(ctx context.Context, doc SecondBrainDocument, note secondbrain.Preview) ([]secondbrain.InboxAction, error) {
	ledger := s.deps.ReviewLedger
	if ledger == nil {
		return nil, nil
	}
	key := secondbrain.ReviewNoteKey(browseReference(doc), note.Rel)
	candidates := secondbrain.InboxPassages(key, note.Body)
	if len(candidates) == 0 {
		return nil, nil
	}
	rev := candidates[0].Revision
	states, err := ledger.Read(ctx, key, rev)
	if err != nil {
		return nil, err
	}
	witnessed := false
	for _, receipt := range doc.Receipts {
		if receipt.Action == secondBrainActionCapture && receipt.Note == note.Rel && receipt.ReviewKey == key && receipt.Revision == rev {
			witnessed = true
			break
		}
	}
	var out []secondbrain.InboxAction
	for _, a := range candidates {
		state, ok := states[a.ID]
		if !ok && !witnessed {
			continue
		}
		if ok {
			a.State, a.Workspace, a.Ticket = state.State, state.Workspace, state.Ticket
		}
		out = append(out, a)
	}
	return out, nil
}
