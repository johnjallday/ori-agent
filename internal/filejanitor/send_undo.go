package filejanitor

import (
	"context"
	"errors"
	"fmt"

	"github.com/johnjallday/ori-agent/internal/logger"
)

// Putting a sent file back.
//
// A send is reversed with the same conditional mover that sent it, the folders
// swapped: from the receiver's top level back to the sender's, under the same
// name, only if the file is still the exact file Ori put there, only if both
// folders are still the folders it recorded, and only if the name is free in the
// sender's folder, which the kernel enforces. It needs no connection: a send can
// be put back after the connection was turned off. It does need the receiver's
// Filing to still manage the folder the file went to; otherwise the file is
// reported as not put back and nothing is touched (FR 34).

// LocationSent is where a sent file that was not put back is: still at the top
// level of the workspace it was sent to.
const LocationSent = "sent"

// sendReceivers works out, for the sends among actions, each receiver's managed
// folder as it is now, but only while that receiver still manages the folder the
// file was sent to. It reads saved settings only.
func (s *Service) sendReceivers(owner string, actions []FileAction) map[string]string {
	receivers := map[string]string{}
	for _, action := range actions {
		if action.Operation != OperationSend || action.Send == nil {
			continue
		}
		id := action.Send.ReceiverWorkspaceID
		if _, seen := receivers[id]; seen {
			continue
		}
		receivers[id] = ""
		if side, err := s.loadSide(owner, id, false); err == nil && side.settings.RootID == action.Send.ReceiverRootID {
			receivers[id] = side.root.Path
		}
	}
	return receivers
}

// receiverPaths are the folders to lock, without the receivers that are gone.
func receiverPaths(receivers map[string]string) []string {
	var paths []string
	for _, path := range receivers {
		if path != "" {
			paths = append(paths, path)
		}
	}
	return paths
}

// undoSend puts one sent file back. The caller holds both folders' locks and has
// claimed the action (UndoInProgress). receiverRoot is "" when the receiver no
// longer manages the folder the file went to. Every outcome is journaled on the
// action; the returned error is only a failure to record it.
func (s *Service) undoSend(ctx context.Context, settings JanitorSettings, root string, action FileAction, receiverRoot string) error {
	fail := func(message string) error {
		_, err := s.finishUndo(settings.WorkspaceID, action, UndoFailed, message)
		return err
	}
	sent := action.Send
	if sent == nil || action.AfterFingerprint.FileID == "" {
		return fail("Ori has no record of how to put this file back safely.")
	}
	receiverName := s.workspaceName(sent.ReceiverWorkspaceID)
	if receiverRoot == "" {
		return fail(fmt.Sprintf("%s no longer has Filing on the folder this file went to, so Ori left it there.", receiverName))
	}
	if err := s.ConditionalMoveBetween(receiverRoot, sent.ReceiverRootDirID, root, sent.RootDirID); err != nil {
		return fail(fmt.Sprintf("A folder changed since Ori sent this file, so it left the file in %s.", receiverName))
	}
	_, err := s.conditionalMoverFor().MoveIfUnchanged(ctx, ConditionalMove{
		Root: receiverRoot, RootID: sent.ReceiverRootDirID,
		From: DirRef{Rel: "", ID: sent.ReceiverRootDirID}, To: DirRef{Rel: "", ID: sent.RootDirID},
		FromName: action.SourceName, ToName: action.SourceName, Expect: action.AfterFingerprint,
		DestRoot: root, DestRootID: sent.RootDirID,
	})
	if err == nil {
		if _, err := s.finishUndo(settings.WorkspaceID, action, UndoDone, ""); err != nil {
			return err
		}
		// A file the person just took back must not be re-proposed by the next scan.
		s.suppressRestored(settings.WorkspaceID, root, action.SourceName)
		return nil
	}
	var race *MoveRaceError
	switch {
	case errors.As(err, &race) && !race.RolledBack:
		logger.Warn("File Janitor undo of a send could not be reversed", logger.Fields{
			"workspace_id": settings.WorkspaceID, "action_id": action.ID, "reviewed_file_left": race.LeftIsReviewed,
		})
		message := "Ori moved this file and could not put it back where it was."
		if race.LeftAt != "" {
			message += " It is in this folder, named " + race.LeftAt + "."
		}
		return fail(message)
	case errors.Is(err, ErrMoveDestinationOccupied):
		return fail("Something else is already using the original name, so Ori did not overwrite it.")
	case errors.Is(err, ErrMoveSourceChanged):
		return fail(fmt.Sprintf("The file in %s changed since Ori sent it, or is no longer at its top level, so Ori left it alone.", receiverName))
	case errors.Is(err, ErrMoveParentChanged), errors.Is(err, ErrMoveOutsideRoot), errors.Is(err, ErrMoveCrossDevice), errors.Is(err, ErrMoveUnsupported):
		return fail(fmt.Sprintf("A folder changed since Ori sent this file, so it left the file in %s.", receiverName))
	}
	logger.Warn("File Janitor undo of a send failed", logger.Fields{
		"workspace_id": settings.WorkspaceID, "action_id": action.ID, "error": err.Error(),
	})
	return fail("Ori could not put the file back.")
}

// settleSendUndos settles every send whose undo was cut short, by looking at
// where the file is. It never moves anything. The caller holds the locks.
func (s *Service) settleSendUndos(workspaceID, root string, receivers map[string]string) {
	state, err := s.store.LoadScanState(workspaceID)
	if err != nil {
		return
	}
	for _, action := range state.Actions {
		if action.Operation != OperationSend || action.Undo != UndoInProgress || action.Send == nil {
			continue
		}
		if _, running := runningUndos.Load(action.ID); running {
			continue
		}
		original, originalErr := currentFingerprint(root, action.SourceName)
		atOriginal := originalErr == nil && sameFileVersion(action.AfterFingerprint, original)
		atReceiver := false
		if receiverRoot := receivers[action.Send.ReceiverWorkspaceID]; receiverRoot != "" {
			there, thereErr := currentFingerprintAt(receiverRoot, action.SourceName)
			atReceiver = thereErr == nil && sameFileVersion(action.AfterFingerprint, there)
		}
		switch {
		case atOriginal && !atReceiver:
			if _, err := s.finishUndo(workspaceID, action, UndoDone, ""); err == nil {
				s.suppressRestored(workspaceID, root, action.SourceName)
			}
		case atReceiver && !atOriginal:
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
			_, _ = s.finishUndo(workspaceID, action, UndoFailed, "Ori could not tell whether this file was put back. Check both folders.")
		}
	}
}
