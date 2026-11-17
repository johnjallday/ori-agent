package filejanitor

import (
	"context"
	"errors"
	"path"
	"sort"
	"strings"
	"time"
)

// The receipt says what one press actually did. Every number on it comes from the
// journal (the actions written under the filing's identity), never from the plan:
// a file that went stale or failed after admission is not counted as filed, and a
// file nobody attempted is counted as not attempted.

// ReceiptNotMovedLimit bounds the not-moved list of one receipt read.
const ReceiptNotMovedLimit = 50

// MaxListedFilings is how many recent filings the list read returns (and how many
// the scan state retains).
const MaxListedFilings = MaxRetainedFilings

// ErrFilingNotFound means no filing of this owner has that identity.
var ErrFilingNotFound = errors.New("filing not found")

// Filing states a receipt reports.
const (
	FilingRunning     = "running"
	FilingDone        = "done"
	FilingInterrupted = "interrupted"
)

// ReceiptDestination totals the files that landed in one destination folder.
type ReceiptDestination struct {
	Category    Category `json:"category"`
	Destination string   `json:"destination"` // "Filed/Documents", relative to the folder
	Count       int      `json:"count"`
}

// ReceiptNotMoved is one file that was planned and did not move, with the reason in
// words a person can act on.
type ReceiptNotMoved struct {
	CandidateID string       `json:"candidate_id,omitempty"`
	Name        string       `json:"name"`
	Operation   Operation    `json:"operation,omitempty"`
	Result      ActionResult `json:"result"`
	Reason      string       `json:"reason"`
}

// FilingReceipt is one press's account. It carries no path, token or fingerprint.
type FilingReceipt struct {
	FilingID    string    `json:"filing_id"`
	State       string    `json:"state"`
	AdmittedAt  time.Time `json:"admitted_at"`
	CompletedAt time.Time `json:"completed_at,omitzero"`
	// Filed and Trashed count files that actually moved and were verified.
	Filed         int                  `json:"filed"`
	Trashed       int                  `json:"trashed"`
	ByDestination []ReceiptDestination `json:"by_destination"`
	NotMovedTotal int                  `json:"not_moved_total"`
	NotMoved      []ReceiptNotMoved    `json:"not_moved"`
	// NotAttempted counts planned files the run never reached (an interrupted run).
	NotAttempted int `json:"not_attempted"`
	// LooseNow is how many files are still waiting in the folder, in this scan or in
	// another scan of it.
	LooseNow int `json:"loose_now"`
	// UndoAvailable is true while at least one file of this filing can still be put
	// back and the folder is the one that was filed.
	UndoAvailable bool `json:"undo_available"`
	// Undo is set once Undo all has been pressed for this filing.
	Undo *FilingUndoSummary `json:"undo,omitempty"`
	// HistoryTrimmed says older journal entries of this filing were pruned, so the
	// receipt cannot account for every planned file.
	HistoryTrimmed bool `json:"history_trimmed,omitempty"`
}

// FilingUndoSummary is the state of a filing's Undo all.
type FilingUndoSummary struct {
	State       string    `json:"state"` // running | done | interrupted
	Back        int       `json:"back"`
	NotBack     int       `json:"not_back"`
	ClaimedAt   time.Time `json:"claimed_at"`
	CompletedAt time.Time `json:"completed_at,omitzero"`
}

// FilingSummary is one line of the recent-filings list: enough for History's "Last
// filing" line and its Undo all.
type FilingSummary struct {
	FilingID      string    `json:"filing_id"`
	State         string    `json:"state"`
	AdmittedAt    time.Time `json:"admitted_at"`
	CompletedAt   time.Time `json:"completed_at,omitzero"`
	Filed         int       `json:"filed"`
	Trashed       int       `json:"trashed"`
	NotMoved      int       `json:"not_moved"`
	UndoAvailable bool      `json:"undo_available"`
	Undone        bool      `json:"undone"`
}

func filingState(record FilingRecord) string {
	switch {
	case !record.CompletedAt.IsZero():
		return FilingDone
	case record.running():
		return FilingRunning
	default:
		return FilingInterrupted
	}
}

// filingActions are the journal entries written under one filing, in journal order.
func filingActions(state ScanState, filingID string) []FileAction {
	var actions []FileAction
	for _, action := range state.Actions {
		if action.IdempotencyKey == filingID {
			actions = append(actions, action)
		}
	}
	return actions
}

// notMovedReason is the reason a planned file did not move, in plain words.
func notMovedReason(action FileAction) string {
	switch {
	case action.ErrorSummary != "":
		return action.ErrorSummary
	case action.Result == ResultStale:
		return "This file changed after you looked, so Ori left it alone."
	case action.Result == ResultFailed:
		return "Ori could not move this file."
	default:
		return "Ori did not get to this file."
	}
}

// undoAvailableFor reports whether Undo all would still do something: the run has
// finished, has not been claimed, the folder is the one that was filed, and at
// least one filed file can still be reversed.
func undoAvailableFor(record FilingRecord, actions []FileAction, currentRootID string) bool {
	if !record.UndoClaimedAt.IsZero() || record.running() || record.RootID != currentRootID {
		return false
	}
	for _, action := range actions {
		if action.Undoable() && action.BelongsToRoot(currentRootID) {
			return true
		}
	}
	return false
}

// buildReceipt is the pure projection of one filing from saved state.
func buildReceipt(state ScanState, record FilingRecord, currentRootID string, offset int) FilingReceipt {
	receipt := FilingReceipt{
		FilingID: record.ID, State: filingState(record), AdmittedAt: record.AdmittedAt, CompletedAt: record.CompletedAt,
		ByDestination: []ReceiptDestination{}, NotMoved: []ReceiptNotMoved{},
	}
	actions := filingActions(state, record.ID)
	byDestination := map[string]*ReceiptDestination{}
	var notMoved []ReceiptNotMoved
	for _, action := range actions {
		switch {
		case action.Result == ResultApplied && action.Operation == OperationTrash:
			receipt.Trashed++
		case action.Result == ResultApplied:
			receipt.Filed++
			destination := path.Dir(action.DestinationRelative)
			entry := byDestination[destination]
			if entry == nil {
				entry = &ReceiptDestination{Category: action.DestinationCategory, Destination: destination}
				byDestination[destination] = entry
			}
			entry.Count++
		case action.Result == ResultFailed || action.Result == ResultStale:
			notMoved = append(notMoved, ReceiptNotMoved{
				CandidateID: action.CandidateID, Name: DisplayFileName(action.SourceName), Operation: action.Operation,
				Result: action.Result, Reason: notMovedReason(action),
			})
		}
	}
	for _, missing := range record.Unrecorded {
		name := ""
		if candidate, found := state.Candidate(missing.CandidateID); found {
			name = candidate.Display()
		}
		notMoved = append(notMoved, ReceiptNotMoved{CandidateID: missing.CandidateID, Name: name, Result: ResultFailed, Reason: missing.Reason})
	}
	destinations := make([]ReceiptDestination, 0, len(byDestination))
	for _, entry := range byDestination {
		destinations = append(destinations, *entry)
	}
	sort.Slice(destinations, func(i, j int) bool { return destinations[i].Destination < destinations[j].Destination })
	receipt.ByDestination = destinations

	receipt.NotMovedTotal = len(notMoved)
	offset = min(max(offset, 0), len(notMoved))
	end := offset + min(ReceiptNotMovedLimit, len(notMoved)-offset)
	receipt.NotMoved = append(receipt.NotMoved, notMoved[offset:end]...)

	planned := record.PlannedMoves + record.PlannedTrash
	accounted := len(actions) + len(record.Unrecorded)
	if record.CompletedAt.IsZero() {
		receipt.NotAttempted = max(planned-accounted, 0)
	} else if accounted < planned {
		receipt.HistoryTrimmed = true
	}
	receipt.LooseNow = waitingInFolder(state, record)
	receipt.UndoAvailable = undoAvailableFor(record, actions, currentRootID)
	if !record.UndoClaimedAt.IsZero() {
		receipt.Undo = undoSummary(state, record)
	}
	return receipt
}

// waitingInFolder is how many files Ori still has waiting for this folder: the ones in
// the scan that was filed and the ones in any other scan of the same folder. A newer
// scan does not propose a file an earlier pending scan already holds, so counting the
// filed scan alone would say "no files stay loose" while files sit in an earlier one.
// A file is counted once, by its saved state.
func waitingInFolder(state ScanState, record FilingRecord) int {
	rootOf := make(map[string]string, len(state.Batches))
	for _, batch := range state.Batches {
		rootOf[batch.ID] = batch.RootID
	}
	seen := map[string]bool{}
	waiting := 0
	for _, candidate := range state.Candidates {
		if !proposalPending(candidate) {
			continue
		}
		if candidate.BatchID != record.BatchID && (record.RootID == "" || rootOf[candidate.BatchID] != record.RootID) {
			continue
		}
		key := candidate.Fingerprint.Key()
		if key != "" && seen[key] {
			continue
		}
		seen[key] = true
		waiting++
	}
	return waiting
}

func undoSummary(state ScanState, record FilingRecord) *FilingUndoSummary {
	summary := &FilingUndoSummary{State: FilingDone, ClaimedAt: record.UndoClaimedAt, CompletedAt: record.UndoCompletedAt}
	if record.UndoCompletedAt.IsZero() {
		summary.State = FilingInterrupted
	}
	for _, outcome := range undoOutcomes(state, record, "") {
		if outcome.Result == UndoOutcomeUndone || outcome.Result == UndoOutcomeAlreadyUndone {
			summary.Back++
		} else {
			summary.NotBack++
		}
	}
	return summary
}

func (s *Service) filingFor(state ScanState, owner, filingID string) (FilingRecord, bool) {
	for _, record := range state.Filings {
		if record.ID == strings.TrimSpace(filingID) && record.Owner == owner {
			return record, true
		}
	}
	return FilingRecord{}, false
}

// ReadFiling is the receipt of one press. It reads saved state only.
func (s *Service) ReadFiling(ctx context.Context, owner, workspaceID, filingID string, offset int) (FilingReceipt, error) {
	if s == nil || s.store == nil || ctx.Err() != nil || offset < 0 || !managedRootGeneration.MatchString(strings.TrimSpace(filingID)) {
		return FilingReceipt{}, ErrFilingNotFound
	}
	state, err := s.store.LoadScanState(workspaceID)
	if err != nil {
		return FilingReceipt{}, err
	}
	record, found := s.filingFor(state, owner, filingID)
	if !found {
		return FilingReceipt{}, ErrFilingNotFound
	}
	settings, err := s.store.LoadSettings(workspaceID)
	if err != nil {
		return FilingReceipt{}, err
	}
	return buildReceipt(state, record, settings.RootID, offset), nil
}

// ListFilings returns the most recent filings of this owner, newest first, at most
// MaxListedFilings. It feeds History's "Last filing" line.
func (s *Service) ListFilings(ctx context.Context, owner, workspaceID string, limit int) ([]FilingSummary, error) {
	if s == nil || s.store == nil || ctx.Err() != nil {
		return nil, ErrReviewUnavailable
	}
	if limit < 1 || limit > MaxListedFilings {
		limit = MaxListedFilings
	}
	state, err := s.store.LoadScanState(workspaceID)
	if err != nil {
		return nil, err
	}
	settings, err := s.store.LoadSettings(workspaceID)
	if err != nil {
		return nil, err
	}
	summaries := []FilingSummary{}
	for i := len(state.Filings) - 1; i >= 0 && len(summaries) < limit; i-- {
		record := state.Filings[i]
		if record.Owner != owner {
			continue
		}
		receipt := buildReceipt(state, record, settings.RootID, 0)
		summaries = append(summaries, FilingSummary{
			FilingID: record.ID, State: receipt.State, AdmittedAt: record.AdmittedAt, CompletedAt: record.CompletedAt,
			Filed: receipt.Filed, Trashed: receipt.Trashed, NotMoved: receipt.NotMovedTotal,
			UndoAvailable: receipt.UndoAvailable, Undone: !record.UndoClaimedAt.IsZero(),
		})
	}
	return summaries, nil
}
