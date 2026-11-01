package personalassistant

import (
	"context"
	"time"

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/secondbrain"
)

const secondBrainActionWeekDismiss = "week_dismiss"

// SecondBrainWeekReport is the first-week recap row (first-week FR25).
type SecondBrainWeekReport struct {
	// ID is stable for one setup, so the row is the same row on every load.
	ID       string
	Sentence string
	// At is the day the recap became due.
	At time.Time
}

// WeekReport says what the second brain gained in its first week, once, seven
// local days after Ori set it up. The counts are the ledger's notes by kind and
// the written-by-Ori listing's captures and journal entries, plus the facts the
// facts note lists; no note is ever opened. It is for a second brain Ori set up:
// a folder the user connected is not Ori's to report on. found is false before
// the week is up, after the row was dismissed, and once it has had its week.
func (s *SecondBrainService) WeekReport(ctx context.Context, userID string) (SecondBrainWeekReport, bool) {
	doc, err := s.read(ctx, userID)
	if err != nil {
		return SecondBrainWeekReport{}, false
	}
	source := doc.Connected()
	if source == nil || !source.CreatedByOri() || source.ConnectedAt.IsZero() {
		return SecondBrainWeekReport{}, false
	}
	now := s.deps.Now()
	due := secondbrain.WeekDue(source.ConnectedAt, time.Local)
	fillIn := source.FillIn
	switch {
	case now.Before(due):
		return SecondBrainWeekReport{}, false
	case fillIn != nil && fillIn.WeekDismissedAt != nil:
		return SecondBrainWeekReport{}, false
	case fillIn != nil && fillIn.WeekShownAt != nil && now.Sub(*fillIn.WeekShownAt) > secondbrain.WeekShowsFor:
		return SecondBrainWeekReport{}, false
	case (fillIn == nil || fillIn.WeekShownAt == nil) && now.Sub(due) > secondbrain.WeekShowsFor:
		// A setup older than two weeks is not in its first week any more.
		return SecondBrainWeekReport{}, false
	}
	counts, ok := s.weekCounts(ctx, userID, source)
	if !ok {
		return SecondBrainWeekReport{}, false
	}
	if fillIn == nil || fillIn.WeekShownAt == nil {
		if _, err := s.mutate(ctx, userID, func(doc *SecondBrainDocument) error {
			connected := doc.Connected()
			if connected == nil {
				return nil
			}
			if connected.FillIn == nil {
				connected.FillIn = &secondbrain.FillIn{}
			}
			if connected.FillIn.WeekShownAt == nil {
				at := now.UTC()
				connected.FillIn.WeekShownAt = &at
			}
			return nil
		}); err != nil {
			logger.Warn("The first-week row could not be marked as seen", logger.Fields{"error": err.Error()})
		}
	}
	return SecondBrainWeekReport{
		ID:       "second-brain-week-" + due.Format("20060102"),
		Sentence: secondbrain.WeekSentence(counts),
		At:       due,
	}, true
}

// weekCounts reads the gains: the ledger for people, projects and follow-ups,
// the listing for captures and journal entries, and HQ's approved facts for the
// facts note, which is only counted once Ori has written it. ok is false when a
// count that matters cannot be had, so the row waits for the next load rather
// than telling the user a number that is wrong.
func (s *SecondBrainService) weekCounts(ctx context.Context, userID string, source *secondbrain.Source) (secondbrain.WeekCounts, bool) {
	written, err := secondbrain.CountWritten(source.Path)
	if err != nil {
		return secondbrain.WeekCounts{}, false
	}
	counts := secondbrain.WeekCounts{
		People:    source.FillIn.MirrorCount(secondbrain.NoteKindPerson),
		Projects:  source.FillIn.MirrorCount(secondbrain.NoteKindProject),
		FollowUps: source.FillIn.MirrorCount(secondbrain.NoteKindFollowUp),
		Captures:  written.ByKind[secondbrain.NoteKindCapture],
		Journals:  written.ByKind[secondbrain.NoteKindJournal],
	}
	if source.FillIn.MirrorCount(secondbrain.NoteKindFacts) > 0 {
		if s.holdings == nil {
			return secondbrain.WeekCounts{}, false
		}
		holdings, err := s.holdings(ctx, userID)
		if err != nil {
			return secondbrain.WeekCounts{}, false
		}
		counts.Facts = len(holdings.Facts)
	}
	return counts, true
}

// DismissWeek records that the user dismissed the recap row. Dismissing twice is
// dismissing once, and a replay of the same request returns what the first did.
func (s *SecondBrainService) DismissWeek(ctx context.Context, userID, requestID string) (SecondBrainView, error) {
	if !secondbrain.ValidRequestID(requestID) {
		return SecondBrainView{}, ErrValidation
	}
	_, err := s.mutate(ctx, userID, func(doc *SecondBrainDocument) error {
		if _, replayed, err := doc.Replay(requestID, secondBrainActionWeekDismiss); err != nil || replayed {
			return err
		}
		connected := doc.Connected()
		if connected == nil {
			return ErrSecondBrainNotConnected
		}
		if !connected.CreatedByOri() {
			return ErrSecondBrainNotOwned
		}
		if connected.FillIn == nil {
			connected.FillIn = &secondbrain.FillIn{}
		}
		at := s.deps.Now().UTC()
		if connected.FillIn.WeekShownAt == nil {
			connected.FillIn.WeekShownAt = &at
		}
		if connected.FillIn.WeekDismissedAt == nil {
			connected.FillIn.WeekDismissedAt = &at
		}
		doc.Remember(secondbrain.Receipt{RequestID: requestID, Action: secondBrainActionWeekDismiss, At: s.deps.Now()})
		return nil
	})
	if err != nil {
		return SecondBrainView{}, err
	}
	return s.Status(ctx, userID)
}
