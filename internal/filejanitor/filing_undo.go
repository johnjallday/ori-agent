package filejanitor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Undo all puts back every file of one filing that can still be put back, with the
// checks today's per-file Undo makes and the same refusal to overwrite: a file whose
// original name is now taken, or that changed since it was filed, is left where it
// is and reported. It runs once per filing: the run is claimed in one scan-state
// write, so a second press, or a second window, waits for the run that owns it and
// then reports that run's result.

// What happened to one file of an Undo all.
const (
	UndoOutcomeUndone        = "undone"
	UndoOutcomeAlreadyUndone = "already_undone" // put back by a per-file Undo before this run
	UndoOutcomeFailed        = "failed"
	UndoOutcomeInterrupted   = "interrupted" // the run stopped before reaching it
)

// Where a file is after an Undo all that could not put it back.
const (
	LocationFolder  = "folder"  // back in the folder under its original name
	LocationFiled   = "filed"   // still where it was filed
	LocationTrash   = "trash"   // still in the system Trash
	LocationUnknown = "unknown" // Ori cannot find it
)

// FilingUndoOutcome is one file's result.
type FilingUndoOutcome struct {
	CandidateID string    `json:"candidate_id"`
	ActionID    string    `json:"action_id"`
	Name        string    `json:"name"`
	Operation   Operation `json:"operation"`
	Result      string    `json:"result"`
	Message     string    `json:"message,omitempty"`
	// Location and Destination say where a file that was not put back is now:
	// Destination is relative to the folder ("Filed/Documents/report.pdf").
	Location    string `json:"location,omitempty"`
	Destination string `json:"destination,omitempty"`
}

// FilingUndoResult is what one Undo all did. A replay is identical to the first
// answer apart from Replayed.
type FilingUndoResult struct {
	FilingID string `json:"filing_id"`
	Replayed bool   `json:"replayed,omitempty"`
	// Interrupted means the run that owns this result stopped before it finished.
	Interrupted bool `json:"interrupted,omitempty"`
	// Undo restores files only; these confirmed creations are left alone.
	RetainedFolders []string `json:"retained_folders,omitempty"`
	// Back counts files that are back where they were; NotBack the ones that are not.
	Back     int                 `json:"back"`
	NotBack  int                 `json:"not_back"`
	Outcomes []FilingUndoOutcome `json:"outcomes"`
}

// errUndoReplay is internal: the run was already claimed.
var errUndoReplay = errors.New("undo all was already claimed")

// undoOutcomes builds every file's outcome for a claimed run from the journal. root
// is the managed folder (empty skips the IO that finds where a file is now).
func undoOutcomes(state ScanState, record FilingRecord, root string) []FilingUndoOutcome {
	outcomes := []FilingUndoOutcome{}
	for _, action := range filingActions(state, record.ID) {
		if action.Result != ResultApplied {
			continue // it never moved, so there is nothing to put back
		}
		outcome := FilingUndoOutcome{CandidateID: action.CandidateID, ActionID: action.ID, Name: DisplayFileName(action.SourceName), Operation: action.Operation}
		switch action.Undo {
		case UndoDone:
			outcome.Result = UndoOutcomeUndone
			if action.UndoneAt.Before(record.UndoClaimedAt) {
				outcome.Result = UndoOutcomeAlreadyUndone
			}
		case UndoFailed:
			outcome.Result = UndoOutcomeFailed
			outcome.Message = action.UndoError
			outcome.Location, outcome.Destination = locateAfterUndo(root, action)
		case UndoInProgress:
			outcome.Result = UndoOutcomeInterrupted
			outcome.Message = "Ori stopped before it reached this file."
			outcome.Location, outcome.Destination = locateAfterUndo(root, action)
		default:
			// Available but never reached (the run was refused part way), or not
			// reversible: it is not back.
			outcome.Result = UndoOutcomeInterrupted
			outcome.Message = "Ori did not put this file back."
			outcome.Location, outcome.Destination = locateAfterUndo(root, action)
		}
		outcomes = append(outcomes, outcome)
	}
	return outcomes
}

// locateAfterUndo says where a file that is not back is now, so the person knows
// where to find it. It looks at names inside the managed folder only.
func locateAfterUndo(root string, action FileAction) (location, destination string) {
	if root == "" {
		return LocationUnknown, ""
	}
	if action.Operation == OperationTrash {
		if action.TrashRestoreToken != "" {
			if _, err := os.Lstat(action.TrashRestoreToken); err == nil {
				return LocationTrash, ""
			}
		}
		return LocationUnknown, ""
	}
	if action.Operation == OperationSend {
		// A send has no destination inside this folder: it is back here, or still
		// in the workspace it was sent to (the outcome's message names it).
		original := filepath.Join(root, action.SourceName)
		if withinRoot(root, original) {
			if _, err := os.Lstat(original); err == nil {
				return LocationFolder, ""
			}
		}
		return LocationSent, ""
	}
	filed := filepath.Join(root, filepath.FromSlash(action.DestinationRelative))
	if withinRoot(root, filed) {
		if _, err := os.Lstat(filed); err == nil {
			return LocationFiled, action.DestinationRelative
		}
	}
	original := filepath.Join(root, action.SourceName)
	if withinRoot(root, original) {
		if _, err := os.Lstat(original); err == nil {
			return LocationFolder, ""
		}
	}
	return LocationUnknown, ""
}

func (s *Service) filingUndoResult(state ScanState, record FilingRecord, root string, replayed bool) FilingUndoResult {
	result := FilingUndoResult{RetainedFolders: record.createdFolders(), FilingID: record.ID, Replayed: replayed, Outcomes: undoOutcomes(state, record, root)}
	for _, outcome := range result.Outcomes {
		if outcome.Result == UndoOutcomeUndone || outcome.Result == UndoOutcomeAlreadyUndone {
			result.Back++
		} else {
			result.NotBack++
		}
	}
	result.Interrupted = record.UndoCompletedAt.IsZero() && !record.UndoClaimedAt.IsZero()
	return result
}

// UndoFiling is Undo all for one filing.
func (s *Service) UndoFiling(ctx context.Context, owner, workspaceID, filingID string) (FilingUndoResult, error) {
	filingID = strings.TrimSpace(filingID)
	if s == nil || s.store == nil || ctx.Err() != nil || !managedRootGeneration.MatchString(filingID) {
		return FilingUndoResult{}, fmt.Errorf("%w: no such filing", ErrUndoUnavailable)
	}
	settings, err := s.requireConfigured(workspaceID)
	if err != nil {
		return FilingUndoResult{}, err
	}
	root, err := s.scannerFor().ResolveRoot(settings)
	if err != nil {
		return FilingUndoResult{}, err
	}

	// The same boundary every other writer takes: the root lock first, then one
	// scan-state write. Holding it for the whole run is what makes a second press
	// wait and then find the first run's result. A filing that sent files also
	// holds each receiver's folder (the ones that still manage the folder the
	// files went to), in one fixed order.
	receivers := map[string]string{}
	if before, loadErr := s.store.LoadScanState(workspaceID); loadErr == nil {
		receivers = s.sendReceivers(owner, filingActions(before, filingID))
	}
	unlock := lockRoots(append([]string{root}, receiverPaths(receivers)...)...)
	defer unlock()

	// A placement or a send whose undo was cut short is settled from the disk
	// before this run decides what is left to put back.
	s.settlePlacementUndos(workspaceID, root)
	s.settleSendUndos(workspaceID, root, receivers)

	var claimed FilingRecord
	var targets []FileAction
	_, err = s.store.UpdateScanState(workspaceID, func(state *ScanState) error {
		index := -1
		for i := range state.Filings {
			if state.Filings[i].ID == filingID && state.Filings[i].Owner == owner {
				index = i
				break
			}
		}
		if index < 0 {
			return fmt.Errorf("%w: no such filing", ErrUndoUnavailable)
		}
		record := state.Filings[index]
		if record.RootID != settings.RootID {
			// Its paths are relative to a folder this workspace no longer manages:
			// reversing them now would write into the current folder (FR-57).
			return fmt.Errorf("%w: these files were filed from a folder this workspace no longer manages", ErrUndoUnavailable)
		}
		if !record.UndoClaimedAt.IsZero() {
			claimed = record
			return errUndoReplay
		}
		if record.CompletedAt.IsZero() && record.running() {
			return fmt.Errorf("%w: this filing is still running", ErrUndoUnavailable)
		}
		now := s.clock()
		for i := range state.Actions {
			action := state.Actions[i]
			if action.IdempotencyKey == filingID && action.Undoable() && action.BelongsToRoot(settings.RootID) {
				targets = append(targets, action)
				state.Actions[i].Undo = UndoInProgress
			}
		}
		if len(targets) == 0 {
			return fmt.Errorf("%w: nothing from this filing can be put back", ErrUndoUnavailable)
		}
		state.Filings[index].UndoClaimedAt = now
		state.Filings[index].UndoClaimedBy = owner
		claimed = state.Filings[index]
		return nil
	})
	if errors.Is(err, errUndoReplay) {
		state, loadErr := s.store.LoadScanState(workspaceID)
		if loadErr != nil {
			return FilingUndoResult{}, loadErr
		}
		return s.filingUndoResult(state, claimed, root, true), nil
	}
	if err != nil {
		return FilingUndoResult{}, err
	}

	// Put the files back in reverse order of filing, each through the engine's own
	// per-file undo (which journals every attempt, including the ones that fail).
	// The folder is checked again before each file: a relink mid-run stops the rest.
	for _, target := range targets {
		runningUndos.Store(target.ID, struct{}{})
	}
	defer func() {
		for _, target := range targets {
			runningUndos.Delete(target.ID)
		}
	}()
	for i := len(targets) - 1; i >= 0; i-- {
		action := targets[i]
		if current, loadErr := s.store.LoadSettings(workspaceID); loadErr != nil || current.RootID != claimed.RootID {
			_, _ = s.finishUndo(workspaceID, action, UndoFailed, "The folder changed while Ori was putting files back, so it stopped.")
			continue
		}
		switch action.Operation {
		case OperationMove:
			_, _ = s.undoMove(ctx, settings, root, action, owner)
		case OperationPlace:
			_, _ = s.undoPlacement(ctx, settings, root, action)
		case OperationSend:
			// Put back from the receiver's folder; reported, never touched, when
			// that receiver no longer manages the folder the file went to (FR 34).
			receiverRoot := ""
			if action.Send != nil {
				receiverRoot = receivers[action.Send.ReceiverWorkspaceID]
			}
			_ = s.undoSend(ctx, settings, root, action, receiverRoot)
		case OperationTrash:
			_, _ = s.restoreFromTrash(settings, root, action, owner)
		default:
			_, _ = s.finishUndo(workspaceID, action, UndoFailed, "Ori does not know how to reverse this action.")
		}
	}

	final, err := s.store.UpdateScanState(workspaceID, func(state *ScanState) error {
		for i := range state.Filings {
			if state.Filings[i].ID == filingID {
				state.Filings[i].UndoCompletedAt = s.clock()
				claimed = state.Filings[i]
				return nil
			}
		}
		return fmt.Errorf("%w: the filing record is gone", ErrUndoUnavailable)
	})
	if err != nil {
		return FilingUndoResult{}, err
	}
	return s.filingUndoResult(final, claimed, root, false), nil
}

// ForgetRestored makes Ori forget that the files an Undo all put back were put
// back, so the next scan can offer them again. Today's per-file Undo records each
// restored file as skipped so it is not re-proposed at once; this clears exactly
// those records for one filing, writes nothing else and moves no file. It is
// idempotent, and refused after the folder was relinked. It returns how many files
// it forgot.
func (s *Service) ForgetRestored(ctx context.Context, owner, workspaceID, filingID string) (int, error) {
	filingID = strings.TrimSpace(filingID)
	if s == nil || s.store == nil || ctx.Err() != nil || !managedRootGeneration.MatchString(filingID) {
		return 0, fmt.Errorf("%w: no such filing", ErrUndoUnavailable)
	}
	settings, err := s.requireConfigured(workspaceID)
	if err != nil {
		return 0, err
	}
	root, err := s.scannerFor().ResolveRoot(settings)
	if err != nil {
		return 0, err
	}
	unlock := lockRoot(root)
	defer unlock()

	forgotten := 0
	_, err = s.store.UpdateScanState(workspaceID, func(state *ScanState) error {
		var record FilingRecord
		found := false
		for _, existing := range state.Filings {
			if existing.ID == filingID && existing.Owner == owner {
				record, found = existing, true
			}
		}
		if !found {
			return fmt.Errorf("%w: no such filing", ErrUndoUnavailable)
		}
		if record.RootID != settings.RootID {
			return fmt.Errorf("%w: these files were filed from a folder this workspace no longer manages", ErrUndoUnavailable)
		}
		forgotten = 0 // the mutate may be retried; count once
		keys := map[string]bool{}
		for _, action := range state.Actions {
			if action.IdempotencyKey != filingID || action.Undo != UndoDone || record.UndoClaimedAt.IsZero() || action.UndoneAt.Before(record.UndoClaimedAt) {
				continue
			}
			// The file as it is now (what the restore recorded as skipped) and as it
			// was before filing; either can be the key that keeps it out of a scan.
			if fingerprint, fingerprintErr := currentFingerprint(root, action.SourceName); fingerprintErr == nil {
				if key := fingerprint.Key(); key != "" {
					keys[key] = true
				}
			}
			if key := action.BeforeFingerprint.Key(); key != "" {
				keys[key] = true
			}
			forgotten++
		}
		for key := range keys {
			ClearSkipped(state, key) // never called with an empty key: that would clear every skip
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return forgotten, nil
}
