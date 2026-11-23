package personalassistant

import (
	"context"
	"github.com/johnjallday/ori-agent/internal/secondbrain"
)

// WithDocumentsNotebook is a host-only bridge. Connection/setup then sync
// is the single lock order; the callback may access the folder report store,
// never another Notes mutation. No interview state or sync event is emitted.
func (s *SecondBrainService) WithDocumentsNotebook(ctx context.Context, owner, hq string, fn func(secondbrain.Source, string) error) error {
	if s == nil {
		return ErrSecondBrainUnavailable
	}
	s.createMu.Lock()
	defer s.createMu.Unlock()
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	doc, err := s.read(ctx, owner)
	if err != nil {
		return err
	}
	source := doc.Connected()
	if source == nil {
		return ErrSecondBrainNotConnected
	}
	if doc.Owner.HQWorkspaceID != hq || doc.Owner.UserID != owner || !source.CreatedByOri() || !source.FillIn.Allowed() {
		return ErrSecondBrainFillInNotAllowed
	}
	return fn(*source, browseReference(doc))
}
