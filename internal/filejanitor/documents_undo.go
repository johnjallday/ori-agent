package filejanitor

import (
	"context"
	"errors"
	"path"
	"path/filepath"
	"sync"

	"github.com/johnjallday/ori-agent/internal/logger"
)

// Putting a placed document back.
//
// A placement is reversed with the same conditional mover that filed it, the roles
// swapped: the file is moved only if it is still the exact file Ori put there (the
// identity recorded right after the move, never a later look), only if the folder it
// went into is still that folder, and only if its original name is free, which the
// kernel enforces. Ori never overwrites, never picks another name and never deletes.
// What was put back and what remains is reported file by file.

// runningUndos holds the placement actions this process is putting back right now,
// so an action found "in progress" is told apart: still running, or cut short by a
// crash.
var runningUndos sync.Map // action ID -> struct{}

// undoPlacement puts one placed file back where it came from.
func (s *Service) undoPlacement(ctx context.Context, settings JanitorSettings, root string, action FileAction) (UndoResult, error) {
	fail := func(message string) (UndoResult, error) {
		return s.finishUndo(settings.WorkspaceID, action, UndoFailed, message)
	}
	if action.Place == nil || action.AfterFingerprint.FileID == "" || action.Place.RootDirID == "" || action.Place.FolderID == "" {
		return fail("Ori has no record of how to put this file back safely.")
	}
	folder := path.Dir(action.DestinationRelative)
	name := path.Base(action.DestinationRelative)
	mover := s.conditionalMoverFor()
	if err := mover.Supported(root, action.Place.RootDirID); err != nil {
		if errors.Is(err, ErrMoveUnsupported) {
			return fail("Ori cannot move files safely in this folder on this computer, so it left the file where it is.")
		}
		return fail("The folder changed since Ori filed this, so it left the file where it is.")
	}

	_, err := mover.MoveIfUnchanged(ctx, ConditionalMove{
		Root: root, RootID: action.Place.RootDirID,
		From: DirRef{Rel: folder, ID: action.Place.FolderID}, To: DirRef{Rel: "", ID: action.Place.RootDirID},
		FromName: name, ToName: action.SourceName, Expect: action.AfterFingerprint,
	})
	if err == nil {
		result, err := s.finishUndo(settings.WorkspaceID, action, UndoDone, "")
		if err != nil {
			return UndoResult{}, err
		}
		result.RestoredTo = action.SourceName
		result.Message = "Put back in the folder."
		// A file the person just recovered must not be re-proposed by the next scan.
		s.suppressRestored(settings.WorkspaceID, root, action.SourceName)
		return result, nil
	}

	var race *MoveRaceError
	switch {
	case errors.As(err, &race) && !race.RolledBack:
		logger.Warn("File Janitor undo could not be reversed", logger.Fields{
			"workspace_id": settings.WorkspaceID, "action_id": action.ID, "reviewed_file_left": race.LeftIsReviewed,
		})
		message := "Ori moved this file and could not put it back where it was."
		if race.LeftAt != "" {
			message += " It is in " + race.LeftAt + "."
		}
		return fail(message)
	case errors.Is(err, ErrMoveDestinationOccupied):
		return fail("Something else is already using the original name, so Ori did not overwrite it.")
	case errors.Is(err, ErrMoveSourceChanged):
		return fail("The filed copy changed since Ori moved it, or is no longer there, so Ori left it alone.")
	case errors.Is(err, ErrMoveParentChanged), errors.Is(err, ErrMoveOutsideRoot), errors.Is(err, ErrMoveCrossDevice):
		return fail("The folder it was filed into changed, so Ori left the file where it is.")
	}
	logger.Warn("File Janitor undo failed", logger.Fields{
		"workspace_id": settings.WorkspaceID, "action_id": action.ID, "error": err.Error(),
	})
	return fail("Ori could not put the file back.")
}

// DocumentsFilingStanding is where a named filing stands now, worked out from the
// journal and never stored: the decision that belongs to it is not edited when a
// file is put back, so history cannot drift from what is on the disk.
type DocumentsFilingStanding struct {
	// Filed counts files that were placed and are still there; Back counts the
	// ones that were put back.
	Filed         int  `json:"filed"`
	Back          int  `json:"back"`
	UndoAvailable bool `json:"undo_available"`
}

// namedFiling finds a filing into a chosen folder that belongs to owner.
func namedFiling(state ScanState, owner, filingID string) (FilingRecord, bool) {
	for _, record := range state.Filings {
		if record.ID == filingID && record.Owner == owner && record.Destination != nil {
			return record, true
		}
	}
	return FilingRecord{}, false
}

// DocumentsFilingStanding reports how many of a filing's files are still filed and
// how many were put back.
func (s *Service) DocumentsFilingStanding(owner, workspaceID, filingID string) (DocumentsFilingStanding, error) {
	if s == nil || s.store == nil {
		return DocumentsFilingStanding{}, ErrUndoUnavailable
	}
	state, err := s.store.LoadScanState(workspaceID)
	if err != nil {
		return DocumentsFilingStanding{}, err
	}
	record, ok := namedFiling(state, owner, filingID)
	if !ok {
		return DocumentsFilingStanding{}, ErrUndoUnavailable
	}
	var standing DocumentsFilingStanding
	actions := filingActions(state, record.ID)
	for _, action := range actions {
		if action.Operation != OperationPlace || action.Result != ResultApplied {
			continue
		}
		if action.Undo == UndoDone {
			standing.Back++
		} else {
			standing.Filed++
		}
	}
	standing.UndoAvailable = undoAvailableFor(record, actions, s.currentRootID(workspaceID))
	return standing, nil
}

// UndoPlacedFile puts one file of a named filing back. The file must belong to that
// filing and to owner; anything else is refused before anything is touched.
func (s *Service) UndoPlacedFile(ctx context.Context, owner, workspaceID, filingID, actionID string) (FilingUndoResult, error) {
	if s == nil || s.store == nil || ctx.Err() != nil {
		return FilingUndoResult{}, ErrUndoUnavailable
	}
	state, err := s.store.LoadScanState(workspaceID)
	if err != nil {
		return FilingUndoResult{}, err
	}
	record, ok := namedFiling(state, owner, filingID)
	if !ok {
		return FilingUndoResult{}, ErrUndoUnavailable
	}
	var action FileAction
	for _, candidate := range filingActions(state, record.ID) {
		if candidate.ID == actionID && candidate.Operation == OperationPlace {
			action = candidate
		}
	}
	if action.ID == "" {
		return FilingUndoResult{}, ErrUndoUnavailable
	}
	result, err := s.Undo(ctx, workspaceID, actionID, owner)
	if err != nil {
		return FilingUndoResult{}, err
	}
	outcome := FilingUndoOutcome{CandidateID: action.CandidateID, ActionID: action.ID, Name: DisplayFileName(action.SourceName), Operation: OperationPlace}
	out := FilingUndoResult{FilingID: filingID}
	if result.Result == "undone" {
		outcome.Result = UndoOutcomeUndone
		out.Back = 1
	} else {
		outcome.Result = UndoOutcomeFailed
		outcome.Message = result.Message
		if settings, err := s.requireConfigured(workspaceID); err == nil {
			if root, err := s.scannerFor().ResolveRoot(settings); err == nil {
				outcome.Location, outcome.Destination = locateAfterUndo(root, action)
			}
		}
		out.NotBack = 1
	}
	out.Outcomes = []FilingUndoOutcome{outcome}
	return out, nil
}

// settlePlacementUndos settles every placement whose undo was cut short (the
// process died or lost its response between the move and its record) by looking at
// where the file is. It never moves anything. The caller holds lockRoot.
func (s *Service) settlePlacementUndos(workspaceID, root string) {
	state, err := s.store.LoadScanState(workspaceID)
	if err != nil {
		return
	}
	for _, action := range state.Actions {
		if action.Operation != OperationPlace || action.Undo != UndoInProgress {
			continue
		}
		if _, running := runningUndos.Load(action.ID); running {
			continue
		}
		s.settlePlacementUndo(workspaceID, root, action)
	}
}

func (s *Service) settlePlacementUndo(workspaceID, root string, action FileAction) {
	original, originalErr := currentFingerprint(root, action.SourceName)
	atOriginal := originalErr == nil && sameFileVersion(action.AfterFingerprint, original)
	filed := filepath.Join(root, filepath.FromSlash(action.DestinationRelative))
	if !withinRoot(root, filed) {
		return
	}
	there, thereErr := currentFingerprintAt(filepath.Dir(filed), filepath.Base(filed))
	atFiled := thereErr == nil && sameFileVersion(action.AfterFingerprint, there)

	switch {
	case atOriginal && !atFiled:
		// It was put back; only the record was lost.
		if _, err := s.finishUndo(workspaceID, action, UndoDone, ""); err == nil {
			s.suppressRestored(workspaceID, root, action.SourceName)
		}
	case atFiled && !atOriginal:
		// Nothing moved. It can safely be tried again.
		now := s.clock()
		_, _ = s.store.UpdateScanState(workspaceID, func(scan *ScanState) error {
			for i := range scan.Actions {
				if scan.Actions[i].ID == action.ID && scan.Actions[i].Undo == UndoInProgress {
					scan.Actions[i].Undo = UndoAvailable
					scan.Actions[i].UndoneAt = now
					scan.Actions[i].UndoError = "Ori stopped before putting this file back. It was not moved."
				}
			}
			return nil
		})
	default:
		_, _ = s.finishUndo(workspaceID, action, UndoFailed, "Ori could not tell whether this file was put back. Check the folder.")
	}
}
