package personalassistant

import (
	"context"
	"errors"

	"github.com/johnjallday/ori-agent/internal/secondbrain"
)

// Nudge says whether the connected notes deserve another look: the last look
// is over a week old, or dated lines came due since it. It reads the record
// only; no folder is read and no look starts. A folder that cannot be found is
// the Notes station's to report, so it never nudges.
func (s *SecondBrainService) Nudge(ctx context.Context, userID string) (secondbrain.NudgeReport, bool) {
	doc, err := s.read(ctx, userID)
	if err != nil {
		return secondbrain.NudgeReport{}, false
	}
	if s.view(doc).Status != SecondBrainConnected {
		return secondbrain.NudgeReport{}, false
	}
	report := secondbrain.Nudge(&doc.Record, s.deps.Now())
	return report, report.Any()
}

// DismissNudge is the nudge's "Not now": it stays away until a look finishes
// after it. A replay of the same request returns what the first did.
func (s *SecondBrainService) DismissNudge(ctx context.Context, userID, requestID string) (SecondBrainView, error) {
	if !secondbrain.ValidRequestID(requestID) {
		return SecondBrainView{}, ErrValidation
	}
	doc, err := s.mutate(ctx, userID, func(doc *SecondBrainDocument) error {
		err := doc.DismissNudge(s.deps.Now(), requestID)
		if errors.Is(err, secondbrain.ErrNotConnected) {
			return ErrSecondBrainNotConnected
		}
		return err
	})
	if err != nil {
		return SecondBrainView{}, err
	}
	return s.view(doc), nil
}
