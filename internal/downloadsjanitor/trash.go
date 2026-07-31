package downloadsjanitor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/platform"
)

// Trash removal is recoverable removal, and nothing else.
//
// Ori has no permanent delete: a file the user marks for Trash goes to the
// operating system's Trash, where the user can restore it by hand even if Ori
// never runs again. That is the whole reason this path does not go through
// filesystem MCP — `delete_file` unlinks, and an unlinked file has no restore
// token and no recovery story (FR-90, FR-91).
//
// If the platform has no Trash, the operation fails closed. There is
// deliberately no fallback: os.Remove is not a worse Trash, it is a different
// and irreversible operation the user did not ask for.

// TrashRemover moves a file to the recoverable system Trash and returns a token
// that can restore it. Injected so tests can exercise the path without touching
// the developer's real Trash.
type TrashRemover interface {
	Supported() bool
	MoveToTrash(path string) (token string, err error)
	RestoreFromTrash(originalPath, token string) error
}

// platformTrash is the production implementation, backed by Ori's existing
// cross-platform Trash abstraction.
type platformTrash struct{}

func (platformTrash) Supported() bool { return platform.TrashSupported() }

func (platformTrash) MoveToTrash(path string) (string, error) { return platform.MoveToTrash(path) }

func (platformTrash) RestoreFromTrash(originalPath, token string) error {
	return platform.RestoreFromTrash(originalPath, token)
}

// NewPlatformTrash returns the production Trash mechanism.
func NewPlatformTrash() TrashRemover { return platformTrash{} }

// ErrTrashUnsupported reports a platform with no recoverable Trash. The
// operation stops here rather than falling back to deletion.
var ErrTrashUnsupported = errors.New("this system has no recoverable Trash, so Ori will not remove files")

// ErrUndoUnavailable reports an action that cannot be reversed.
var ErrUndoUnavailable = errors.New("this action cannot be undone")

// SetTrash injects the Trash mechanism.
func (s *Service) SetTrash(trash TrashRemover) {
	if s != nil && trash != nil {
		s.trash = trash
	}
}

func (s *Service) trashRemover() TrashRemover {
	if s.trash == nil {
		s.trash = platformTrash{}
	}
	return s.trash
}

// trashOne performs the Trash half of an apply. It runs the identical
// validation sequence as a move — containment, regular-file, symlink,
// fingerprint, journal-before-action — because "send to Trash" is no less a
// mutation than "move" and deserves no weaker checks (FR-90).
func (s *Service) trashOne(_ context.Context, settings JanitorSettings, root string, approval ApprovalRecord, item PlanItem) ItemOutcome {
	now := s.clock()

	state, err := s.store.LoadScanState(settings.WorkspaceID)
	if err != nil {
		return ItemOutcome{CandidateID: item.CandidateID, Result: ResultFailed, Message: "Ori could not read this workspace's state."}
	}
	candidate, ok := state.Candidate(item.CandidateID)
	if !ok {
		return ItemOutcome{CandidateID: item.CandidateID, Result: ResultFailed, Message: "That file is no longer in this batch."}
	}

	remover := s.trashRemover()
	if !remover.Supported() {
		// Fail closed. A system without Trash gets no removal at all.
		return ItemOutcome{
			CandidateID: candidate.ID, Name: candidate.Display(), Operation: OperationTrash,
			Result: ResultFailed, Message: "This system has no recoverable Trash, so Ori did not remove the file.",
		}
	}

	sourcePath := filepath.Join(root, candidate.Name)
	current, err := currentFingerprint(root, candidate.Name)
	if err != nil {
		return s.recordTrashStale(candidate, approval, "This file is no longer there. Rescan to see what is in the folder now.", now)
	}
	if !candidate.Fingerprint.Matches(current) {
		return s.recordTrashStale(candidate, approval, "This file changed after you approved it, so Ori left it alone. Rescan to review it again.", now)
	}
	if !withinRoot(root, sourcePath) {
		return s.recordTrashStale(candidate, approval, "This file is outside the folder Ori manages.", now)
	}

	action, err := NewApprovedAction(
		"action-"+uuid.New().String(), settings.WorkspaceID, candidate, OperationTrash,
		"", approval.UserID, approval.ConsumedAt, approval.IdempotencyKey, settings.RootID,
	)
	if err != nil {
		return ItemOutcome{
			CandidateID: candidate.ID, Name: candidate.Display(), Operation: OperationTrash,
			Result: ResultFailed, Message: "Ori could not record this action, so it did not perform it.",
		}
	}
	action = action.MarkApplying(now)
	if err := s.appendAction(settings.WorkspaceID, action, CandidateApplying); err != nil {
		return ItemOutcome{
			CandidateID: candidate.ID, Name: candidate.Display(), Operation: OperationTrash,
			Result: ResultFailed, Message: "Ori could not record this action, so it did not perform it.",
		}
	}

	token, trashErr := remover.MoveToTrash(sourcePath)
	_, statErr := os.Lstat(sourcePath)
	gone := os.IsNotExist(statErr)

	switch {
	case trashErr == nil && gone:
		// The restore token is the only handle a later restore has. Recording
		// it — with the original path — is what makes Trash reversible from
		// inside Ori rather than only from the Finder.
		action.TrashRestoreToken = token
		action = action.MarkApplied(candidate.Fingerprint, s.clock())
		_ = s.updateAction(settings.WorkspaceID, action, CandidateApplied)
		return ItemOutcome{
			CandidateID: candidate.ID, ActionID: action.ID, Name: candidate.Display(),
			Operation: OperationTrash, Result: ResultApplied, Undoable: true,
			Message: "Moved to Trash. You can restore it from here or from your system Trash.",
		}
	case trashErr != nil:
		summary := "Ori could not move this file to Trash."
		action = action.MarkFailed(summary, s.clock())
		_ = s.updateAction(settings.WorkspaceID, action, CandidateFailed)
		return ItemOutcome{
			CandidateID: candidate.ID, ActionID: action.ID, Name: candidate.Display(),
			Operation: OperationTrash, Result: ResultFailed, Message: summary,
		}
	default:
		// No error, but the file is still there. Reporting success would tell
		// the user a file was removed when it was not.
		summary := "Ori could not confirm this file reached the Trash, so it is reported as not removed."
		action = action.MarkFailed(summary, s.clock())
		_ = s.updateAction(settings.WorkspaceID, action, CandidateFailed)
		return ItemOutcome{
			CandidateID: candidate.ID, ActionID: action.ID, Name: candidate.Display(),
			Operation: OperationTrash, Result: ResultFailed, Message: summary,
		}
	}
}

func (s *Service) recordTrashStale(candidate JanitorCandidate, approval ApprovalRecord, message string, now time.Time) ItemOutcome {
	action, err := NewApprovedAction(
		"action-"+uuid.New().String(), candidate.WorkspaceID, candidate, OperationTrash,
		"", approval.UserID, approval.ConsumedAt, approval.IdempotencyKey, s.currentRootID(candidate.WorkspaceID),
	)
	if err == nil {
		action = action.MarkStale(message, now)
		_ = s.appendAction(candidate.WorkspaceID, action, CandidateStale)
	}
	return ItemOutcome{
		CandidateID: candidate.ID, ActionID: action.ID, Name: candidate.Display(),
		Operation: OperationTrash, Result: ResultStale, Message: message,
	}
}

// ------------------------------------------------------------- undo, restore

// UndoResult is the outcome of one undo attempt.
type UndoResult struct {
	ActionID string `json:"action_id"`
	Name     string `json:"name"`
	Result   string `json:"result"`
	Message  string `json:"message,omitempty"`
	// RestoredTo is where the file ended up, relative to the configured folder.
	RestoredTo string `json:"restored_to,omitempty"`
}

// Undo reverses one completed action: a move goes back to where it came from, a
// Trash action is restored from the system Trash.
//
// It is single-use and idempotent. Every attempt is journaled — including the
// ones that fail — because "I tried to get my file back and could not" is
// exactly the history a user needs to see (FR-101).
func (s *Service) Undo(ctx context.Context, workspaceID, actionID, userID string) (UndoResult, error) {
	settings, err := s.requireConfigured(workspaceID)
	if err != nil {
		return UndoResult{}, err
	}
	root, err := s.scannerFor().ResolveRoot(settings)
	if err != nil {
		return UndoResult{}, err
	}

	// Claim the undo inside one atomic write, so two clicks cannot both start
	// one. The loser is told it is already running or already done.
	var action FileAction
	_, err = s.store.UpdateScanState(workspaceID, func(state *ScanState) error {
		index := -1
		for i := range state.Actions {
			if state.Actions[i].ID == strings.TrimSpace(actionID) {
				index = i
				break
			}
		}
		if index < 0 {
			return fmt.Errorf("%w: no such action", ErrUndoUnavailable)
		}
		stored := state.Actions[index]
		if stored.WorkspaceID != workspaceID {
			return fmt.Errorf("%w: no such action", ErrUndoUnavailable)
		}
		switch {
		case stored.Undo == UndoDone:
			return fmt.Errorf("%w: this was already undone", ErrUndoUnavailable)
		case stored.Undo == UndoInProgress:
			return fmt.Errorf("%w: an undo is already running for this file", ErrUndoUnavailable)
		case !stored.Undoable():
			return fmt.Errorf("%w: only a completed action can be undone", ErrUndoUnavailable)
		case !stored.BelongsToRoot(settings.RootID):
			// The action was performed against a folder this workspace no longer
			// manages. Its paths are relative, so reversing it now would write
			// into the CURRENT folder — restoring a file into somewhere it never
			// came from (FR-57).
			return fmt.Errorf("%w: it was filed from a folder this workspace no longer manages", ErrUndoUnavailable)
		}
		state.Actions[index].Undo = UndoInProgress
		action = state.Actions[index]
		return nil
	})
	if err != nil {
		return UndoResult{}, err
	}

	unlock := lockRoot(root)
	defer unlock()

	switch action.Operation {
	case OperationMove:
		return s.undoMove(ctx, settings, root, action, userID)
	case OperationTrash:
		return s.restoreFromTrash(settings, root, action, userID)
	default:
		return s.finishUndo(workspaceID, action, UndoFailed, "Ori does not know how to reverse this action.")
	}
}

// undoMove puts a filed file back where it came from.
//
// Three things are checked first, and each of them can legitimately stop the
// undo: the moved file must still be where Ori put it and unchanged since, the
// original name must be free, and both paths must still be inside the
// configured folder. Overwriting whatever now occupies the original path would
// turn an undo into data loss (FR-98).
func (s *Service) undoMove(ctx context.Context, settings JanitorSettings, root string, action FileAction, userID string) (UndoResult, error) {
	destinationPath := filepath.Join(root, filepath.FromSlash(action.DestinationRelative))
	if !withinRoot(root, destinationPath) {
		return s.finishUndo(settings.WorkspaceID, action, UndoFailed, "That file is no longer inside the folder Ori manages.")
	}
	originalPath := filepath.Join(root, action.SourceName)
	if !withinRoot(root, originalPath) {
		return s.finishUndo(settings.WorkspaceID, action, UndoFailed, "The original location is no longer inside the folder Ori manages.")
	}

	info, err := os.Lstat(destinationPath)
	if err != nil {
		return s.finishUndo(settings.WorkspaceID, action, UndoFailed, "Ori cannot find the filed copy — it may have been moved or renamed since.")
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return s.finishUndo(settings.WorkspaceID, action, UndoFailed, "The filed copy is no longer a regular file.")
	}
	current := fingerprintFor(filepath.Base(destinationPath), info)
	if !action.AfterFingerprint.Zero() && !action.AfterFingerprint.Matches(current) {
		return s.finishUndo(settings.WorkspaceID, action, UndoFailed, "The filed copy changed since Ori moved it, so Ori left it alone.")
	}
	if _, err := os.Lstat(originalPath); err == nil {
		return s.finishUndo(settings.WorkspaceID, action, UndoFailed, "Something else is already using the original name, so Ori did not overwrite it.")
	}

	if s.mover == nil {
		return s.finishUndo(settings.WorkspaceID, action, UndoFailed, "Ori has no way to move files right now.")
	}
	moveErr := s.mover.Move(ctx, settings.WorkspaceID, destinationPath, originalPath)
	restored, _ := verifyMove(destinationPath, originalPath, action.SourceName)
	if !restored {
		summary := "Ori could not put the file back."
		if moveErr == nil {
			summary = "Ori could not confirm the file was put back, so it is reported as not restored."
		}
		return s.finishUndo(settings.WorkspaceID, action, UndoFailed, summary)
	}

	result, err := s.finishUndo(settings.WorkspaceID, action, UndoDone, "")
	if err != nil {
		return UndoResult{}, err
	}
	result.RestoredTo = action.SourceName
	result.Message = "Put back in the folder."
	// A file the user just recovered must not be re-proposed by the next scan.
	s.suppressRestored(settings.WorkspaceID, root, action.SourceName)
	return result, nil
}

// restoreFromTrash brings a trashed file back through the platform Trash.
func (s *Service) restoreFromTrash(settings JanitorSettings, root string, action FileAction, userID string) (UndoResult, error) {
	remover := s.trashRemover()
	if !remover.Supported() {
		return s.finishUndo(settings.WorkspaceID, action, UndoFailed, "This system has no recoverable Trash.")
	}
	// Deliberately no check that a restore token exists. Whether one is needed
	// is the platform's business: macOS and Linux hand back the trashed
	// location and cannot restore without it, but the Windows Recycle Bin
	// exposes no stable path and always returns an empty token, restoring by
	// original path instead. Refusing on an empty token here would make undo
	// permanently impossible on Windows. Each platform rejects what it cannot
	// do, and the failure below reports it in the same words.
	originalPath := filepath.Join(root, action.SourceName)
	if !withinRoot(root, originalPath) {
		return s.finishUndo(settings.WorkspaceID, action, UndoFailed, "The original location is no longer inside the folder Ori manages.")
	}
	if _, err := os.Lstat(originalPath); err == nil {
		return s.finishUndo(settings.WorkspaceID, action, UndoFailed,
			"Something else is already using that name, so Ori did not overwrite it.")
	}

	if err := remover.RestoreFromTrash(originalPath, action.TrashRestoreToken); err != nil {
		// The commonest cause is an emptied Trash. The journal entry stays
		// either way — the record of what happened outlives the file (FR-100).
		return s.finishUndo(settings.WorkspaceID, action, UndoFailed,
			"Ori could not restore this file. It may have been removed from the Trash already. "+
				"You may still be able to restore it from your system Trash.")
	}
	if _, err := os.Lstat(originalPath); err != nil {
		return s.finishUndo(settings.WorkspaceID, action, UndoFailed,
			"Ori could not confirm the file came back, so it is reported as not restored.")
	}

	result, err := s.finishUndo(settings.WorkspaceID, action, UndoDone, "")
	if err != nil {
		return UndoResult{}, err
	}
	result.RestoredTo = action.SourceName
	result.Message = "Restored from Trash."
	s.suppressRestored(settings.WorkspaceID, root, action.SourceName)
	return result, nil
}

// finishUndo records an undo's outcome and returns it.
func (s *Service) finishUndo(workspaceID string, action FileAction, state UndoState, message string) (UndoResult, error) {
	now := s.clock()
	_, err := s.store.UpdateScanState(workspaceID, func(scan *ScanState) error {
		for i := range scan.Actions {
			if scan.Actions[i].ID != action.ID {
				continue
			}
			scan.Actions[i].Undo = state
			scan.Actions[i].UndoneAt = now
			scan.Actions[i].UndoError = message
			break
		}
		if state == UndoDone {
			for i := range scan.Candidates {
				if scan.Candidates[i].ID == action.CandidateID {
					// The file is back, so the candidate is no longer an applied
					// one. It is not re-proposed until it changes or the user
					// asks for a scan that includes restored items.
					scan.Candidates[i].State = CandidateSkipped
					scan.Candidates[i].StateReason = "Put back by you"
					resummarizeBatch(scan, scan.Candidates[i].BatchID)
					break
				}
			}
		}
		return nil
	})
	if err != nil {
		return UndoResult{}, err
	}
	result := UndoResult{ActionID: action.ID, Name: DisplayFileName(action.SourceName), Message: message}
	if state == UndoDone {
		result.Result = "undone"
	} else {
		result.Result = "failed"
	}
	return result, nil
}

// suppressRestored records the recovered file's current state as skipped, so the
// next scan does not immediately propose filing the file the user just took
// back. It reappears if the file changes, or when the user resets skipped items
// (FR-102).
func (s *Service) suppressRestored(workspaceID, root, name string) {
	fingerprint, err := currentFingerprint(root, name)
	if err != nil {
		return
	}
	_, _ = s.store.UpdateScanState(workspaceID, func(state *ScanState) error {
		MarkSkipped(state, fingerprint, s.clock())
		return nil
	})
}
