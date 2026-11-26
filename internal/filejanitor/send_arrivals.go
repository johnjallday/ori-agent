package filejanitor

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/johnjallday/ori-agent/internal/logger"
)

// What arrived: the receiving workspace's side of a send (FR 36 to 39).
//
// The sender's journal holds everything that proves or reverses a send. The
// receiver keeps only a display list, written once per press: which files arrived
// from which workspace and when. Drawing it reads only the receiver's own state and
// one no-follow stat per name at its top level, never the sender's state. Losing
// the list (an older build rewriting the receiver's state drops it) loses the
// strip and nothing else.

// MaxArrivalNames bounds the names one arrivals read returns for one send.
const MaxArrivalNames = 200

// ErrArrivalNotFound means the receiving workspace has no such arrival, or it was
// dismissed or undone.
var ErrArrivalNotFound = errors.New("no such arrival")

// SendArrival is one press's files as the receiver records them. No path.
type SendArrival struct {
	ID                string        `json:"id"`
	SenderWorkspaceID string        `json:"sender_workspace_id"`
	ConnectionID      string        `json:"connection_id"`
	FilingID          string        `json:"filing_id"`
	RootID            string        `json:"root_id"` // the receiver's folder generation when they arrived
	ArrivedAt         time.Time     `json:"arrived_at"`
	Files             []ArrivedFile `json:"files"`
	DismissedAt       time.Time     `json:"dismissed_at,omitzero"`
	UndoneAt          time.Time     `json:"undone_at,omitzero"`
}

// ArrivedFile names one sent file: the sender's journal entry and the file as it
// was right after it arrived (its name is After.Name).
type ArrivedFile struct {
	ActionID string      `json:"action_id"`
	After    Fingerprint `json:"after"`
}

func (a SendArrival) validate(workspaceID string) error {
	if !strings.HasPrefix(a.ID, "arrival-") || !managedRootGeneration.MatchString(a.ID) ||
		strings.TrimSpace(a.SenderWorkspaceID) == "" || strings.EqualFold(a.SenderWorkspaceID, workspaceID) ||
		!validConnectionID(a.ConnectionID) || !managedRootGeneration.MatchString(a.FilingID) ||
		!managedRootGeneration.MatchString(a.RootID) || a.ArrivedAt.IsZero() ||
		len(a.Files) == 0 || len(a.Files) > PlanMaxOperations {
		return fmt.Errorf("%w: invalid arrival record", ErrInvalidCandidate)
	}
	for _, file := range a.Files {
		if strings.TrimSpace(file.ActionID) == "" || ValidateFileName(file.After.Name) != nil || file.After.FileID == "" {
			return fmt.Errorf("%w: invalid arrival record", ErrInvalidCandidate)
		}
	}
	return nil
}

// recordArrivals writes one arrival per receiver for the sends of a filing that
// were applied. The caller holds both folders' locks; each receiver's state is
// one write of its own, never nested in the sender's. A failure is logged: the
// files arrived either way, and the sender's receipt and Undo all do not depend
// on this list.
func (s *Service) recordArrivals(senderID, filingID string, routes sendRoutes) {
	state, err := s.store.LoadScanState(senderID)
	if err != nil {
		return
	}
	byReceiver := map[string][]ArrivedFile{}
	connectionOf := map[string]string{}
	for _, action := range filingActions(state, filingID) {
		if action.Operation != OperationSend || action.Result != ResultApplied || action.Send == nil {
			continue
		}
		receiver := action.Send.ReceiverWorkspaceID
		byReceiver[receiver] = append(byReceiver[receiver], ArrivedFile{ActionID: action.ID, After: action.AfterFingerprint})
		connectionOf[receiver] = action.Send.ConnectionID
	}
	now := s.clock()
	for receiver, files := range byReceiver {
		route, found := routes.byID(connectionOf[receiver])
		if !found {
			continue
		}
		arrival := SendArrival{
			ID: "arrival-" + uuid.New().String(), SenderWorkspaceID: senderID, ConnectionID: route.ConnectionID,
			FilingID: filingID, RootID: route.ReceiverRootID, ArrivedAt: now, Files: files,
		}
		if _, err := s.store.UpdateScanState(receiver, func(scan *ScanState) error {
			scan.Arrivals = append(scan.Arrivals, arrival)
			return nil
		}); err != nil {
			logger.Warn("Filing could not record what arrived in the receiving workspace", logger.Fields{
				"workspace_id": senderID, "receiver_workspace_id": receiver, "error": err.Error(),
			})
		}
	}
}

// ReceiverNames maps each receiving workspace named by the sends among actions to
// its display name, for History. Names only, never a path.
func (s *Service) ReceiverNames(actions []FileAction) map[string]string {
	names := map[string]string{}
	for _, action := range actions {
		if action.Operation == OperationSend && action.Send != nil {
			if _, done := names[action.Send.ReceiverWorkspaceID]; !done {
				names[action.Send.ReceiverWorkspaceID] = s.workspaceName(action.Send.ReceiverWorkspaceID)
			}
		}
	}
	return names
}

// ArrivalView is one arrival as the receiver's Review tab shows it (FR 36, 37).
// Count and Names are the files of that send still at the top level.
type ArrivalView struct {
	ID                string    `json:"id"`
	SenderWorkspaceID string    `json:"sender_workspace_id"`
	SenderName        string    `json:"sender_name"`
	ArrivedAt         time.Time `json:"arrived_at"`
	Count             int       `json:"count"`
	Names             []string  `json:"names"`
}

// arrivedHere reports which of an arrival's files are still at the top level of
// root, as the same file: one no-follow stat per name.
func arrivedHere(root string, arrival SendArrival) []ArrivedFile {
	var here []ArrivedFile
	for _, file := range arrival.Files {
		current, err := currentFingerprint(root, file.After.Name)
		if err == nil && sameFileVersion(file.After, current) {
			here = append(here, file)
		}
	}
	return here
}

// ListArrivals is what arrived in this workspace and is still loose at its top
// level, newest first. An arrival is gone once dismissed, undone, or when none of
// its files is still there (FR 39).
func (s *Service) ListArrivals(owner, workspaceID string) ([]ArrivalView, error) {
	if s == nil || s.store == nil {
		return nil, ErrManagedRootUnavailable
	}
	out := []ArrivalView{}
	self, err := s.loadSide(owner, workspaceID, false)
	if err != nil {
		return out, nil // no Filing on a folder here: nothing can have arrived
	}
	state, err := s.store.LoadScanState(workspaceID)
	if err != nil {
		return nil, err
	}
	for i := len(state.Arrivals) - 1; i >= 0; i-- {
		arrival := state.Arrivals[i]
		if !arrival.DismissedAt.IsZero() || !arrival.UndoneAt.IsZero() || arrival.RootID != self.settings.RootID {
			continue
		}
		here := arrivedHere(self.root.Path, arrival)
		if len(here) == 0 {
			continue
		}
		view := ArrivalView{
			ID: arrival.ID, SenderWorkspaceID: arrival.SenderWorkspaceID, SenderName: s.workspaceName(arrival.SenderWorkspaceID),
			ArrivedAt: arrival.ArrivedAt, Count: len(here), Names: []string{},
		}
		for _, file := range here {
			if len(view.Names) == MaxArrivalNames {
				break
			}
			view.Names = append(view.Names, DisplayFileName(file.After.Name))
		}
		out = append(out, view)
	}
	return out, nil
}

// DismissArrival hides an arrival's strip. It moves nothing.
func (s *Service) DismissArrival(owner, workspaceID, arrivalID string) error {
	if s == nil || s.store == nil || !strings.HasPrefix(arrivalID, "arrival-") || !managedRootGeneration.MatchString(arrivalID) {
		return ErrArrivalNotFound
	}
	if _, err := s.loadSide(owner, workspaceID, false); err != nil {
		return ErrArrivalNotFound
	}
	_, err := s.store.UpdateScanState(workspaceID, func(state *ScanState) error {
		for i := range state.Arrivals {
			if state.Arrivals[i].ID == arrivalID {
				if state.Arrivals[i].DismissedAt.IsZero() {
					state.Arrivals[i].DismissedAt = s.clock()
				}
				return nil
			}
		}
		return ErrArrivalNotFound
	})
	return err
}

// UndoArrival is "Undo this send" (FR 38): from the receiving workspace, it puts
// back into the sender's folder the files of that press that are still at the
// receiver's top level, and nothing else. The sender's Filed files of the same
// press are not touched, the filing's once-only Undo all claim is never taken,
// and a later Undo all reports these files as already put back.
func (s *Service) UndoArrival(ctx context.Context, owner, receiverID, arrivalID string) (FilingUndoResult, error) {
	if s == nil || s.store == nil || ctx.Err() != nil || !strings.HasPrefix(arrivalID, "arrival-") || !managedRootGeneration.MatchString(arrivalID) {
		return FilingUndoResult{}, ErrArrivalNotFound
	}
	receiver, err := s.loadSide(owner, receiverID, false)
	if err != nil {
		return FilingUndoResult{}, ErrArrivalNotFound
	}
	receiverState, err := s.store.LoadScanState(receiverID)
	if err != nil {
		return FilingUndoResult{}, err
	}
	var arrival SendArrival
	for _, candidate := range receiverState.Arrivals {
		if candidate.ID == arrivalID {
			arrival = candidate
		}
	}
	if arrival.ID == "" || !arrival.UndoneAt.IsZero() {
		return FilingUndoResult{}, ErrArrivalNotFound
	}
	if arrival.RootID != receiver.settings.RootID {
		return FilingUndoResult{}, fmt.Errorf("%w: this workspace now points at a different folder", ErrUndoUnavailable)
	}
	sender, err := s.loadSide(owner, arrival.SenderWorkspaceID, false)
	if err != nil {
		return FilingUndoResult{}, fmt.Errorf("%w: %s no longer has Filing on its folder, so Ori cannot put these files back", ErrUndoUnavailable, s.workspaceName(arrival.SenderWorkspaceID))
	}
	senderState, err := s.store.LoadScanState(sender.workspaceID)
	if err != nil {
		return FilingUndoResult{}, err
	}
	record, found := FilingRecord{}, false
	for _, candidate := range senderState.Filings {
		if candidate.ID == arrival.FilingID {
			record, found = candidate, true
		}
	}
	if !found || record.RootID != sender.settings.RootID {
		return FilingUndoResult{}, fmt.Errorf("%w: %s now points at a different folder, so Ori cannot put these files back", ErrUndoUnavailable, sender.name)
	}
	if record.CompletedAt.IsZero() && record.running() {
		return FilingUndoResult{}, fmt.Errorf("%w: that filing is still running", ErrUndoUnavailable)
	}
	senderRoot, err := s.scannerFor().ResolveRoot(sender.settings)
	if err != nil || senderRoot != sender.root.Path {
		return FilingUndoResult{}, fmt.Errorf("%w: Ori cannot reach %s's folder", ErrUndoUnavailable, sender.name)
	}

	// Both folders, in the one fixed order, for the whole run.
	unlock := lockRoots(senderRoot, receiver.root.Path)
	defer unlock()
	receivers := map[string]string{receiverID: receiver.root.Path}
	s.settleSendUndos(sender.workspaceID, senderRoot, receivers)

	ids := make([]string, 0, len(arrival.Files))
	for _, file := range arrival.Files {
		ids = append(ids, file.ActionID)
	}
	var targets []FileAction
	_, err = s.store.UpdateScanState(sender.workspaceID, func(state *ScanState) error {
		targets = targets[:0]
		for i := range state.Actions {
			action := state.Actions[i]
			if !slices.Contains(ids, action.ID) || action.IdempotencyKey != arrival.FilingID || action.Operation != OperationSend ||
				action.Send == nil || !strings.EqualFold(action.Send.ReceiverWorkspaceID, receiverID) ||
				!action.Undoable() || !action.BelongsToRoot(sender.settings.RootID) {
				continue
			}
			state.Actions[i].Undo = UndoInProgress
			targets = append(targets, state.Actions[i])
		}
		return nil
	})
	if err != nil {
		return FilingUndoResult{}, err
	}
	for _, target := range targets {
		runningUndos.Store(target.ID, struct{}{})
	}
	defer func() {
		for _, target := range targets {
			runningUndos.Delete(target.ID)
		}
	}()
	for i := len(targets) - 1; i >= 0; i-- {
		_, _ = s.undoSend(ctx, sender.settings, senderRoot, targets[i], receiver.root.Path)
	}

	// The strip is done with: what was put back is back, and what could not be is
	// reported below with where it is.
	if _, err := s.store.UpdateScanState(receiverID, func(state *ScanState) error {
		for i := range state.Arrivals {
			if state.Arrivals[i].ID == arrivalID && state.Arrivals[i].UndoneAt.IsZero() {
				state.Arrivals[i].UndoneAt = s.clock()
			}
		}
		return nil
	}); err != nil {
		logger.Warn("Filing could not mark a send as undone in the receiving workspace", logger.Fields{"workspace_id": receiverID, "error": err.Error()})
	}

	final, err := s.store.LoadScanState(sender.workspaceID)
	if err != nil {
		return FilingUndoResult{}, err
	}
	result := FilingUndoResult{FilingID: arrival.FilingID, Outcomes: []FilingUndoOutcome{}}
	// A file this run put back is "undone"; one put back before it (by Undo all) is
	// "already undone".
	ran := map[string]bool{}
	for _, target := range targets {
		ran[target.ID] = true
	}
	for _, id := range ids {
		action, found := final.Action(id)
		if !found {
			continue
		}
		outcome := FilingUndoOutcome{CandidateID: action.CandidateID, ActionID: action.ID, Name: DisplayFileName(action.SourceName), Operation: action.Operation}
		switch {
		case action.Undo == UndoDone && ran[id]:
			outcome.Result = UndoOutcomeUndone
		case action.Undo == UndoDone:
			outcome.Result = UndoOutcomeAlreadyUndone
		default:
			outcome.Result = UndoOutcomeFailed
			outcome.Message = action.UndoError
			if outcome.Message == "" {
				outcome.Message = fmt.Sprintf("This file is no longer at the top level of %s, so Ori left it where it is.", receiver.name)
			}
			outcome.Location, outcome.Destination = locateAfterUndo(senderRoot, action)
		}
		if outcome.Result == UndoOutcomeUndone || outcome.Result == UndoOutcomeAlreadyUndone {
			result.Back++
		} else {
			result.NotBack++
		}
		result.Outcomes = append(result.Outcomes, outcome)
	}
	return result, nil
}
