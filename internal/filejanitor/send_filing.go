package filejanitor

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/google/uuid"

	"github.com/johnjallday/ori-agent/internal/logger"
)

// Sending a reviewed file to another workspace, on the one press.
//
// A send is a file of the press like any other: it is in the plan the person saw,
// it is admitted with the rest, and it is journaled before the move and recorded
// after it. What differs is the destination: the top level of the receiver's
// folder under the file's own name, reached through the conditional mover with
// both folders checked by identity. Ori never renames a sent file and never
// replaces anything already there: a used name leaves the file here and the
// receipt says so. Before each file, the connection is read again from the
// sender's saved settings: turning it off stops the sends that have not started.

// planSendLine is planLine for a send: the source must still be the file that was
// reviewed; nothing at the receiver is looked at here (the name check is one stat
// per name, done once per receiver by computeReviewPlan).
func planSendLine(root string, candidate JanitorCandidate, category Category, route sendRoute) (PreviewItem, PlanItem, error) {
	if route.ConnectionID == "" || !route.Carries(category) {
		return PreviewItem{}, PlanItem{}, fmt.Errorf("%w: that connection does not carry %s", ErrInvalidAction, category)
	}
	current, err := currentFingerprint(root, candidate.Name)
	if err != nil {
		return PreviewItem{}, PlanItem{}, sourceMissing(candidate)
	}
	if !candidate.Fingerprint.Matches(current) {
		return PreviewItem{}, PlanItem{}, sourceChanged(candidate)
	}
	item := PreviewItem{
		CandidateID: candidate.ID, Name: candidate.Display(), Operation: OperationSend, Category: category,
		Size: candidate.Size, ConnectionID: route.ConnectionID,
	}
	planned := PlanItem{
		CandidateID: candidate.ID, Operation: OperationSend, Category: category,
		FingerprintKey: candidate.Fingerprint.Key(), ConnectionID: route.ConnectionID,
	}
	return item, planned, nil
}

// newSentAction is the journal entry for one send. It carries no path.
func newSentAction(id string, candidate JanitorCandidate, category Category, approval ApprovalRecord, rootID string, route sendRoute) (FileAction, error) {
	action := FileAction{
		ID: id, WorkspaceID: candidate.WorkspaceID, RootID: rootID, BatchID: candidate.BatchID, CandidateID: candidate.ID,
		Operation: OperationSend, SourceName: candidate.Name, DestinationCategory: category,
		Send: &SentTo{
			ConnectionID: route.ConnectionID, ReceiverWorkspaceID: route.ReceiverWorkspaceID,
			ReceiverRootID: route.ReceiverRootID, ReceiverRootDirID: route.ReceiverRootDirID, RootDirID: route.SenderRootDirID,
		},
		BeforeFingerprint: candidate.Fingerprint, ApprovedAt: approval.ConsumedAt, ApprovedBy: approval.UserID,
		IdempotencyKey: approval.IdempotencyKey, Result: ResultPending, Undo: UndoUnavailable,
	}
	if err := action.Validate(); err != nil {
		return FileAction{}, err
	}
	return action, nil
}

// sendStillOn reports whether the connection is still saved exactly as the press
// admitted it: same receiver, same folders, still carrying this kind.
func (s *Service) sendStillOn(workspaceID string, route sendRoute, kind Category) bool {
	settings, err := s.store.LoadSettings(workspaceID)
	if err != nil {
		return false
	}
	for _, connection := range settings.SendsTo {
		if connection.ID == route.ConnectionID {
			return connection.ReceiverWorkspaceID == route.ReceiverWorkspaceID && connection.ReceiverRootID == route.ReceiverRootID &&
				connection.ReceiverRootDirID == route.ReceiverRootDirID && connection.SenderRootDirID == route.SenderRootDirID &&
				connection.Carries(kind)
		}
	}
	return false
}

// sendOne sends one reviewed file: check the connection, validate, journal
// "applying", hand to the conditional mover, record the verified outcome. Like
// placeOne it never returns an error; every outcome is that file's result. The
// caller holds both folders' locks.
func (s *Service) sendOne(ctx context.Context, settings JanitorSettings, root string, approval ApprovalRecord, planItem PlanItem, route sendRoute) ItemOutcome {
	failed := func(name, message string) ItemOutcome {
		return ItemOutcome{CandidateID: planItem.CandidateID, Name: name, Operation: OperationSend, Result: ResultFailed, Message: message}
	}
	state, err := s.store.LoadScanState(settings.WorkspaceID)
	if err != nil {
		return failed("", "Ori could not read this workspace's state.")
	}
	candidate, ok := state.Candidate(planItem.CandidateID)
	if !ok {
		return failed("", "That file is no longer in this review.")
	}
	stale := func(message string) ItemOutcome {
		return ItemOutcome{CandidateID: candidate.ID, Name: candidate.Display(), Operation: OperationSend, Result: ResultStale, Message: message}
	}
	if !s.sendStillOn(settings.WorkspaceID, route, planItem.Category) {
		return stale(fmt.Sprintf("Sending to %s was turned off, so Ori left this file here.", route.ReceiverName))
	}
	if err := s.validateExecutionScope(settings, root, approval); err != nil || state.IsSkipped(candidate.Fingerprint) || !validExecutionCandidate(candidate, planItem, approval, false) {
		return stale("The folder, access or saved review changed. Nothing was moved; request a fresh review.")
	}

	action, err := newSentAction("action-"+uuid.New().String(), candidate, planItem.Category, approval, settings.RootID, route)
	if err != nil {
		return failed(candidate.Display(), "Ori could not record this action, so it did not perform it.")
	}
	action = action.MarkApplying(s.clock())
	if err := s.appendApprovedAction(settings, root, approval, planItem, action); err != nil {
		return failed(candidate.Display(), "Ori could not record this action, so it did not perform it.")
	}
	if err := s.executionHandoff(settings, root, approval, planItem); err != nil {
		action = action.MarkStale("The folder, access or file changed before execution. Nothing was moved; request a fresh review.", s.clock())
		_ = s.updateAction(settings.WorkspaceID, action, CandidateStale)
		return outcomeFor(action)
	}

	after, moveErr := s.conditionalMoverFor().MoveIfUnchanged(ctx, ConditionalMove{
		Root: root, RootID: route.SenderRootDirID,
		From: DirRef{Rel: "", ID: route.SenderRootDirID}, To: DirRef{Rel: "", ID: route.ReceiverRootDirID},
		FromName: candidate.Name, ToName: candidate.Name, Expect: candidate.Fingerprint,
		DestRoot: route.receiverRoot, DestRootID: route.ReceiverRootDirID,
	})
	if moveErr == nil {
		// Not a category move: nothing in the assistant's filing knowledge cites a
		// send, so there is nothing to notify.
		action = action.MarkApplied(after, s.clock())
		_ = s.updateAction(settings.WorkspaceID, action, CandidateApplied)
		return outcomeFor(action)
	}

	var race *MoveRaceError
	switch {
	case errors.As(moveErr, &race) && !race.RolledBack:
		sent := *action.Send
		sent.LeftAt = race.LeftAt
		action.Send = &sent
		summary := "Ori moved this file and could not put it back where it was."
		if race.LeftAt != "" {
			summary += " It is in " + route.ReceiverName + ", named " + race.LeftAt + "."
		}
		action = action.MarkFailed(summary, s.clock())
		_ = s.updateAction(settings.WorkspaceID, action, CandidateFailed)
		logger.Warn("File Janitor send could not be reversed", logger.Fields{
			"workspace_id": settings.WorkspaceID, "action_id": action.ID, "candidate_id": candidate.ID, "reviewed_file_left": race.LeftIsReviewed,
		})
		return outcomeFor(action)
	case errors.Is(moveErr, ErrMoveDestinationOccupied):
		action = action.MarkStale(fmt.Sprintf("The name is already used in %s, so Ori left the file here.", route.ReceiverName), s.clock())
		_ = s.updateAction(settings.WorkspaceID, action, CandidateStale)
		return outcomeFor(action)
	}
	message, nothingMoved := placementRefusal(moveErr)
	if nothingMoved {
		action = action.MarkStale(message, s.clock())
		_ = s.updateAction(settings.WorkspaceID, action, CandidateStale)
		return outcomeFor(action)
	}
	logger.Warn("File Janitor send failed", logger.Fields{
		"workspace_id": settings.WorkspaceID, "action_id": action.ID, "candidate_id": candidate.ID, "error": moveErr.Error(),
	})
	action = action.MarkFailed(message, s.clock())
	_ = s.updateAction(settings.WorkspaceID, action, CandidateFailed)
	return outcomeFor(action)
}

// reconcileSends settles every send left "applying" by a process that died or
// lost its response, by looking at where the file is: still at the sender's top
// level, or at the receiver's. It never moves anything. The caller holds the
// sender's lock and those of the receivers in routes.
func (s *Service) reconcileSends(workspaceID, root string, routes sendRoutes) {
	state, err := s.store.LoadScanState(workspaceID)
	if err != nil {
		return
	}
	for _, action := range state.Actions {
		if action.Operation != OperationSend || action.Result != ResultApplying || action.Send == nil {
			continue
		}
		if _, running := runningFilings.Load(action.IdempotencyKey); running {
			continue
		}
		receiverRoot := ""
		if route, found := routes.byID(action.Send.ConnectionID); found && route.ReceiverRootDirID == action.Send.ReceiverRootDirID {
			receiverRoot = route.receiverRoot
		}
		source, sourceErr := currentFingerprint(root, action.SourceName)
		atSource := sourceErr == nil && sameFileVersion(action.BeforeFingerprint, source)
		atReceiver := false
		var moved Fingerprint
		if receiverRoot != "" {
			var movedErr error
			moved, movedErr = currentFingerprintAt(receiverRoot, filepath.Base(action.SourceName))
			atReceiver = movedErr == nil && sameFileVersion(action.BeforeFingerprint, moved)
		}
		now := s.clock()
		switch {
		case atReceiver && !atSource:
			_ = s.updateAction(workspaceID, action.MarkApplied(moved, now), CandidateApplied)
		case atSource && !atReceiver:
			stopped := action.MarkStale("Ori stopped before sending this file. It was not moved.", now)
			_, _ = s.store.UpdateScanState(workspaceID, func(scan *ScanState) error {
				for i := range scan.Actions {
					if scan.Actions[i].ID == stopped.ID {
						scan.Actions[i] = stopped
					}
				}
				for i := range scan.Candidates {
					if scan.Candidates[i].ID == stopped.CandidateID {
						scan.Candidates[i].State = CandidatePending
						scan.Candidates[i].StateReason = ""
						resummarizeBatch(scan, scan.Candidates[i].BatchID)
					}
				}
				return nil
			})
		default:
			_ = s.updateAction(workspaceID, action.MarkFailed("Ori could not tell whether this file was sent. Check both folders.", now), CandidateFailed)
		}
	}
}
