package personalassistant

import (
	"context"
	"errors"
	"os"
	"slices"
	"time"

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/secondbrain"
)

// secondBrainSyncTimeout bounds one background sync: reads of at most 400 small
// notes and at most 40 writes.
const secondBrainSyncTimeout = 30 * time.Second

// SecondBrainHoldings reads what HQ holds that belongs in the notes, fresh for
// each sync. An error means the holdings are unknown, never that they are empty:
// a sync that cannot read them does nothing, so a passing failure can never make
// every note look forgotten.
type SecondBrainHoldings func(ctx context.Context, userID string) (secondbrain.Holdings, error)

// SetHoldings binds the fill-in's source. The learning services it reads are
// built after this service, so this is startup wiring.
func (s *SecondBrainService) SetHoldings(holdings SecondBrainHoldings) {
	if s != nil {
		s.holdings = holdings
	}
}

// SecondBrainSyncResult is what one sync did.
type SecondBrainSyncResult struct {
	Written     map[string]int
	MorePending bool
}

// Sync copies what HQ holds into a second brain Ori set up, as notes it marks as
// its own (FR4, FR6). It is the one entry point every trigger reaches, it never
// runs on a timer, and running it twice in a row writes nothing the second time.
// A folder the user connected is never written, and neither is a note the user
// wrote or changed.
func (s *SecondBrainService) Sync(ctx context.Context, userID, reason string) (SecondBrainSyncResult, error) {
	if s == nil || s.holdings == nil {
		return SecondBrainSyncResult{}, nil
	}
	s.syncMu.Lock()
	defer s.syncMu.Unlock()

	doc, err := s.read(ctx, userID)
	if err != nil {
		return SecondBrainSyncResult{}, err
	}
	source := doc.Connected()
	if source == nil || !source.CreatedByOri() {
		return SecondBrainSyncResult{}, nil
	}
	writer, err := secondbrain.NewWriter(source)
	if err != nil {
		return SecondBrainSyncResult{}, err
	}
	// A folder that is gone is reported by the status read. Writing here would
	// recreate only the owned folders, in a place the user no longer has a brain.
	if info, err := os.Stat(source.Path); err != nil || !info.IsDir() {
		return SecondBrainSyncResult{}, nil
	}
	holdings, err := s.holdings(ctx, userID)
	if err != nil {
		return SecondBrainSyncResult{}, err
	}
	// Personal HQ is not a project (FR4). The binding names it; a workspace's own
	// designation is only a best-effort projection of that.
	binding, err := s.store.Binding(ctx, userID)
	if err != nil {
		return SecondBrainSyncResult{}, err
	}
	holdings.Projects = slices.DeleteFunc(slices.Clone(holdings.Projects), func(project secondbrain.HeldProject) bool {
		return project.ID == binding.HQWorkspaceID
	})
	var ledger []secondbrain.Mirror
	var wasPending bool
	if source.FillIn != nil {
		ledger, wasPending = source.FillIn.Mirrors, source.FillIn.MorePending
	}
	now := s.deps.Now()
	outcome := secondbrain.Reconcile(writer, ledger, secondbrain.PlanNotes(holdings), now)
	result := SecondBrainSyncResult{Written: outcome.Written, MorePending: outcome.MorePending}

	written := outcome.WrittenTotal()
	if written > 0 || outcome.MorePending != wasPending || !slices.Equal(ledger, outcome.Mirrors) {
		if err := s.saveLedger(ctx, userID, source.ID, outcome, now); err != nil {
			return result, err
		}
	}
	if written > 0 {
		s.dropInboxCache()
		logger.Info("Filled in the second brain", logger.Fields{"reason": reason, "written": written, "more_pending": outcome.MorePending})
	}
	return result, outcome.Err
}

func (s *SecondBrainService) saveLedger(ctx context.Context, userID, sourceID string, outcome secondbrain.SyncOutcome, now time.Time) error {
	_, err := s.mutate(ctx, userID, func(doc *SecondBrainDocument) error {
		connected := doc.Connected()
		// Disconnected or replaced while the sync ran: the ledger went with it.
		if connected == nil || connected.ID != sourceID {
			return nil
		}
		fillIn := connected.FillIn
		if fillIn == nil {
			fillIn = &secondbrain.FillIn{}
		}
		fillIn.Mirrors, fillIn.MorePending = outcome.Mirrors, outcome.MorePending
		if outcome.WrittenTotal() > 0 {
			at := now.UTC()
			fillIn.LastAt = &at
		}
		fillIn.AddToReport(outcome.Added, now)
		connected.FillIn = fillIn
		return nil
	})
	return err
}

// fillInStateAllowed says Ori may keep every kind of note up to date.
const fillInStateAllowed = "allowed"

func fillInView(fillIn *secondbrain.FillIn) *SecondBrainFillInView {
	view := &SecondBrainFillInView{State: fillInStateAllowed}
	if fillIn == nil {
		return view
	}
	view.LastAt = fillIn.LastAt
	for _, mirror := range fillIn.Mirrors {
		switch mirror.State {
		case secondbrain.MirrorUserOwned:
			view.UserOwned++
		case secondbrain.MirrorStale:
			view.Stale++
			view.Written++
		default:
			view.Written++
		}
	}
	return view
}

// secondBrainReportLasts is how long the Today row stays once it has been shown.
const secondBrainReportLasts = 24 * time.Hour

// SecondBrainWrittenReport is the Today row's content.
type SecondBrainWrittenReport struct {
	Sentence string
	At       time.Time
}

// WrittenReport returns what the Today row should say, recording the first time
// it was asked, so the row collapses every write since it was last seen, leaves
// after a day, and never nags (FR9). Anything written later starts a new row.
func (s *SecondBrainService) WrittenReport(ctx context.Context, userID string) (SecondBrainWrittenReport, bool) {
	doc, err := s.read(ctx, userID)
	if err != nil {
		return SecondBrainWrittenReport{}, false
	}
	source := doc.Connected()
	if source == nil || source.FillIn == nil || source.FillIn.Report == nil {
		return SecondBrainWrittenReport{}, false
	}
	report := source.FillIn.Report
	now := s.deps.Now()
	if report.ShownAt != nil && now.Sub(*report.ShownAt) > secondBrainReportLasts {
		return SecondBrainWrittenReport{}, false
	}
	sentence := secondbrain.ReportSentence(report.Counts)
	if sentence == "" {
		return SecondBrainWrittenReport{}, false
	}
	if report.ShownAt == nil {
		if _, err := s.mutate(ctx, userID, func(doc *SecondBrainDocument) error {
			if connected := doc.Connected(); connected != nil && connected.FillIn != nil && connected.FillIn.Report != nil && connected.FillIn.Report.ShownAt == nil {
				at := now.UTC()
				connected.FillIn.Report.ShownAt = &at
			}
			return nil
		}); err != nil {
			logger.Warn("The notes row could not be marked as seen", logger.Fields{"error": err.Error()})
		}
	}
	return SecondBrainWrittenReport{Sentence: sentence, At: report.At}, true
}

// secondBrainSyncTrigger says a background sync is running for a user, and
// whether another trigger arrived while it ran.
type secondBrainSyncTrigger struct{ again bool }

// SyncSoon runs Sync in the background for a trigger whose own write has already
// succeeded, so it never blocks that write or fails it (FR6). Triggers that land
// during a run share one more run, not one each. A failure is logged and the next
// trigger retries.
func (s *SecondBrainService) SyncSoon(userID, reason string) {
	if s == nil || s.holdings == nil {
		return
	}
	s.mu.Lock()
	if trigger, running := s.syncState[userID]; running {
		trigger.again = true
		s.mu.Unlock()
		return
	}
	trigger := &secondBrainSyncTrigger{}
	s.syncState[userID] = trigger
	s.syncs.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.syncs.Done()
		for {
			ctx, cancel := context.WithTimeout(context.Background(), secondBrainSyncTimeout)
			// No Personal HQ yet, or none to repair, means no notes to fill in: not a
			// failure, and the first trigger after the HQ exists catches up.
			if _, err := s.Sync(ctx, userID, reason); err != nil && !errors.Is(err, ErrNeedsHQ) && !errors.Is(err, ErrNotFound) {
				logger.Warn("The second brain could not be filled in", logger.Fields{"reason": reason, "error": err.Error()})
			}
			cancel()
			s.mu.Lock()
			if !trigger.again {
				delete(s.syncState, userID)
				s.mu.Unlock()
				return
			}
			trigger.again = false
			s.mu.Unlock()
		}
	}()
}
