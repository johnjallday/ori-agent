package downloadsjanitor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// This file is the only place in the feature that changes a file, and every
// safeguard converges here.
//
// The order is not incidental. For each approved item, in this sequence:
//
//	 1. Re-derive the source from the stored candidate — never from the request.
//	 2. Verify the source is still a top-level regular file, not a symlink,
//	    inside the configured root, and matching the approved fingerprint.
//	 3. Re-derive the destination from configured root + allowlisted category,
//	    and re-resolve a free name (the one previewed may have been taken since).
//	 4. Journal the action, flushed to disk, *before* the mutation.
//	 5. Issue the mutation and verify the result against the filesystem.
//	 6. Record the verified outcome.
//
// Step 4 before step 5 is what makes a crash recoverable: an action found in
// "applying" at startup is reconciled against the filesystem rather than
// assumed either way. Step 6 is why a tool that reports success without moving
// anything cannot produce a false "applied".

// Mover performs the underlying move. It exists so the Janitor can call the
// workspace's root-scoped filesystem MCP binding in production while tests
// exercise the same code with a fake — including fakes that lie about success.
//
// A Mover is an execution mechanism, not an authorization boundary: everything
// that decides whether a move is allowed has already happened by the time one
// is called.
type Mover interface {
	Move(ctx context.Context, workspaceID, sourcePath, destinationPath string) error
}

// applyLocks serializes applies per configured root. Two applies against one
// folder would otherwise race on collision resolution and could both resolve
// the same free name.
var applyLocks sync.Map // root path -> *sync.Mutex

func lockRoot(root string) func() {
	value, _ := applyLocks.LoadOrStore(strings.ToLower(filepath.Clean(root)), &sync.Mutex{})
	lock := value.(*sync.Mutex)
	lock.Lock()
	return lock.Unlock
}

// SetMover injects the component that performs moves.
func (s *Service) SetMover(mover Mover) {
	if s != nil {
		s.mover = mover
	}
}

// ConfirmRequest applies a previously previewed plan.
type ConfirmRequest struct {
	WorkspaceID string
	UserID      string
	BatchID     string
	// Token is the approval issued by the preview.
	Token string
	// Items must match the previewed plan exactly; any difference invalidates
	// the approval.
	Items []PreviewRequestItem
}

// ApplyResult is the outcome of one confirmed batch.
type ApplyResult struct {
	Outcomes []ItemOutcome `json:"outcomes"`
	Applied  int           `json:"applied"`
	Failed   int           `json:"failed"`
	Stale    int           `json:"stale"`
	// Replayed reports that this confirm matched an apply that had already been
	// performed, so nothing was done a second time.
	Replayed bool `json:"replayed,omitempty"`
}

// ConfirmMoves consumes the approval and applies the plan, one item at a time.
//
// Per-item application is deliberate: one failure must not roll back unrelated
// successes, and the caller gets an ordered outcome per file rather than a
// single verdict for the batch (FR-88, FR-72).
func (s *Service) ConfirmMoves(ctx context.Context, req ConfirmRequest) (ApplyResult, error) {
	workspaceID := strings.TrimSpace(req.WorkspaceID)
	settings, err := s.requireConfigured(workspaceID)
	if err != nil {
		return ApplyResult{}, err
	}
	root, err := s.scannerFor().ResolveRoot(settings)
	if err != nil {
		return ApplyResult{}, err
	}
	if s.mover == nil {
		return ApplyResult{}, fmt.Errorf("%w: no move mechanism is configured", ErrInvalidAction)
	}

	// Rebuild the plan from stored state. The request supplies IDs and
	// categories; the fingerprints come from what Ori recorded, so a client
	// cannot claim a file is in a state it is not.
	state, err := s.store.LoadScanState(workspaceID)
	if err != nil {
		return ApplyResult{}, err
	}
	plan := make([]PlanItem, 0, len(req.Items))
	for _, item := range req.Items {
		candidate, ok := state.Candidate(strings.TrimSpace(item.CandidateID))
		if !ok {
			return ApplyResult{}, fmt.Errorf("%w: %s", ErrCandidateNotFound, item.CandidateID)
		}
		entry := PlanItem{
			CandidateID:    candidate.ID,
			Operation:      item.Operation,
			FingerprintKey: candidate.Fingerprint.Key(),
		}
		// A Trash item has no category — it does not land anywhere inside the
		// folder — and the plan must be reconstructed exactly as the preview
		// built it, or the approval hash will not match what the user approved.
		if item.Operation != OperationTrash {
			category := candidate.EffectiveCategory()
			if requested := strings.TrimSpace(item.Category); requested != "" {
				definition, err := LookupCategory(requested)
				if err != nil {
					return ApplyResult{}, err
				}
				category = definition.ID
			}
			entry.Category = category
		}
		plan = append(plan, entry)
	}

	// Spend the approval before anything is touched. A replay, an expired or
	// tampered token, or a concurrent confirm stops here.
	approval, err := s.ConsumeApproval(workspaceID, req.UserID, req.Token, plan, strings.TrimSpace(req.BatchID))
	if err != nil {
		if errors.Is(err, ErrApprovalConsumed) {
			// The honest retry: report what the original apply did rather than
			// doing it again.
			if replayed, ok, replayErr := s.replayApply(workspaceID, req.Token); replayErr == nil && ok {
				return replayed, nil
			}
		}
		return ApplyResult{}, err
	}

	unlock := lockRoot(root)
	defer unlock()

	result := ApplyResult{}
	for _, item := range plan {
		var outcome ItemOutcome
		if item.Operation == OperationTrash {
			outcome = s.trashOne(ctx, settings, root, approval, item)
		} else {
			outcome = s.applyOne(ctx, settings, root, approval, item)
		}
		result.Outcomes = append(result.Outcomes, outcome)
	}
	result.Applied, result.Failed, result.Stale = SummarizeOutcomes(result.Outcomes)
	return result, nil
}

// replayApply reconstructs the outcome of an apply that already ran under this
// approval, so a retried confirm is answered from the journal instead of
// repeating the mutation (FR-86).
func (s *Service) replayApply(workspaceID, token string) (ApplyResult, bool, error) {
	state, err := s.store.LoadScanState(workspaceID)
	if err != nil {
		return ApplyResult{}, false, err
	}
	index, found := findApproval(state, hashToken(token))
	if !found {
		return ApplyResult{}, false, nil
	}
	key := state.Approvals[index].IdempotencyKey
	result := ApplyResult{Replayed: true}
	for _, action := range state.Actions {
		if action.IdempotencyKey != key {
			continue
		}
		result.Outcomes = append(result.Outcomes, outcomeFor(action))
	}
	if len(result.Outcomes) == 0 {
		return ApplyResult{}, false, nil
	}
	result.Applied, result.Failed, result.Stale = SummarizeOutcomes(result.Outcomes)
	return result, true, nil
}

func outcomeFor(action FileAction) ItemOutcome {
	return ItemOutcome{
		CandidateID: action.CandidateID,
		ActionID:    action.ID,
		Name:        DisplayFileName(action.SourceName),
		Operation:   action.Operation,
		Result:      action.Result,
		Destination: action.DestinationRelative,
		Message:     action.ErrorSummary,
		Undoable:    action.Undoable(),
	}
}

// applyOne performs the full validate → journal → mutate → verify sequence for
// a single approved item. It never returns an error: every outcome, including a
// refusal, is reported as that item's result so the rest of the batch continues.
func (s *Service) applyOne(ctx context.Context, settings JanitorSettings, root string, approval ApprovalRecord, item PlanItem) ItemOutcome {
	now := s.clock()

	state, err := s.store.LoadScanState(settings.WorkspaceID)
	if err != nil {
		return ItemOutcome{CandidateID: item.CandidateID, Result: ResultFailed, Message: "Ori could not read this workspace's state."}
	}
	candidate, ok := state.Candidate(item.CandidateID)
	if !ok {
		return ItemOutcome{CandidateID: item.CandidateID, Result: ResultFailed, Message: "That file is no longer in this batch."}
	}

	// 1-2. The source is derived from the stored candidate and revalidated
	// against the filesystem immediately before the mutation. This is the check
	// that actually protects the file: everything earlier can go stale.
	sourcePath := filepath.Join(root, candidate.Name)
	current, err := currentFingerprint(root, candidate.Name)
	if err != nil {
		return s.recordStale(candidate, item, approval, "This file is no longer there. Rescan to see what is in the folder now.", now)
	}
	if !candidate.Fingerprint.Matches(current) {
		return s.recordStale(candidate, item, approval, "This file changed after you approved it, so Ori left it alone. Rescan to review it again.", now)
	}
	if !withinRoot(root, sourcePath) {
		// Cannot happen while Name is a validated top-level name; kept as the
		// last line of defence if that ever changes.
		return s.recordStale(candidate, item, approval, "This file is outside the folder Ori manages.", now)
	}

	// 3. Destination is re-derived from server state, and a free name is
	// re-resolved: the name shown in the preview may have been taken since.
	destinationDir, err := DestinationDir(settings, item.Category)
	if err != nil {
		return ItemOutcome{CandidateID: candidate.ID, Name: candidate.Display(), Operation: item.Operation, Result: ResultFailed, Message: "That category is not one Ori files into."}
	}
	if err := os.MkdirAll(destinationDir, 0o750); err != nil {
		return ItemOutcome{CandidateID: candidate.ID, Name: candidate.Display(), Operation: item.Operation, Result: ResultFailed, Message: "Ori could not prepare the destination folder."}
	}
	finalName, err := resolveAvailableName(destinationDir, candidate.Name)
	if err != nil {
		return ItemOutcome{CandidateID: candidate.ID, Name: candidate.Display(), Operation: item.Operation, Result: ResultFailed, Message: "Ori could not find a free name in the destination folder."}
	}
	destinationPath := filepath.Join(destinationDir, finalName)
	if !withinRoot(settings.FilingRootPath(), destinationPath) {
		return ItemOutcome{CandidateID: candidate.ID, Name: candidate.Display(), Operation: item.Operation, Result: ResultFailed, Message: "Ori refused a destination outside the filing folder."}
	}
	relative, err := DestinationRelativeFor(settings.FilingRootName, item.Category, finalName)
	if err != nil {
		return ItemOutcome{CandidateID: candidate.ID, Name: candidate.Display(), Operation: item.Operation, Result: ResultFailed, Message: "Ori could not work out where to file this."}
	}

	// 4. Journal before mutating, flushed to disk. If the process dies between
	// here and the next line, the record says an action was in flight.
	action, err := NewApprovedAction(
		"action-"+uuid.New().String(), settings.WorkspaceID, candidate, OperationMove,
		relative, approval.UserID, approval.ConsumedAt, approval.IdempotencyKey,
	)
	if err != nil {
		return ItemOutcome{CandidateID: candidate.ID, Name: candidate.Display(), Operation: item.Operation, Result: ResultFailed, Message: "Ori could not record this action, so it did not perform it."}
	}
	action = action.MarkApplying(now)
	if err := s.appendAction(settings.WorkspaceID, action, CandidateApplying); err != nil {
		return ItemOutcome{CandidateID: candidate.ID, Name: candidate.Display(), Operation: item.Operation, Result: ResultFailed, Message: "Ori could not record this action, so it did not perform it."}
	}

	// 5. Mutate, then verify. The tool's own report is not the answer.
	moveErr := s.mover.Move(ctx, settings.WorkspaceID, sourcePath, destinationPath)
	applied, verifyErr := verifyMove(sourcePath, destinationPath, finalName)

	switch {
	case applied:
		// The move happened. A non-nil error from an ambiguous tool response is
		// reconciled in favour of what the filesystem shows.
		after, _ := currentFingerprintAt(destinationDir, finalName)
		action = action.MarkApplied(after, s.clock())
		_ = s.updateAction(settings.WorkspaceID, action, CandidateApplied)
		return ItemOutcome{
			CandidateID: candidate.ID, ActionID: action.ID, Name: candidate.Display(),
			Operation: OperationMove, Result: ResultApplied, Destination: relative, Undoable: true,
		}
	case moveErr != nil:
		summary := "Ori could not move this file."
		action = action.MarkFailed(summary, s.clock())
		_ = s.updateAction(settings.WorkspaceID, action, CandidateFailed)
		return ItemOutcome{
			CandidateID: candidate.ID, ActionID: action.ID, Name: candidate.Display(),
			Operation: OperationMove, Result: ResultFailed, Message: summary,
		}
	default:
		// No error, but the file did not move. Reporting success here would be
		// the worst possible lie: the user would believe their folder was
		// tidied when nothing happened.
		summary := "Ori could not confirm this file moved, so it is reported as not moved."
		if verifyErr != nil {
			summary = "Ori could not confirm this file moved."
		}
		action = action.MarkFailed(summary, s.clock())
		_ = s.updateAction(settings.WorkspaceID, action, CandidateFailed)
		return ItemOutcome{
			CandidateID: candidate.ID, ActionID: action.ID, Name: candidate.Display(),
			Operation: OperationMove, Result: ResultFailed, Message: summary,
		}
	}
}

// verifyMove reports whether the move actually happened, judged from the
// filesystem rather than from what the tool said.
func verifyMove(sourcePath, destinationPath, finalName string) (bool, error) {
	destinationInfo, destErr := os.Lstat(destinationPath)
	_, sourceErr := os.Lstat(sourcePath)

	destinationExists := destErr == nil && destinationInfo.Mode().IsRegular()
	sourceGone := os.IsNotExist(sourceErr)

	if destinationExists && sourceGone {
		return true, nil
	}
	if destErr != nil && !os.IsNotExist(destErr) {
		return false, destErr
	}
	return false, nil
}

// currentFingerprintAt fingerprints a file in an arbitrary directory (used for
// the post-move destination).
func currentFingerprintAt(dir, name string) (Fingerprint, error) {
	info, err := os.Lstat(filepath.Join(dir, name))
	if err != nil {
		return Fingerprint{}, err
	}
	return fingerprintFor(name, info), nil
}

// recordStale journals a refusal: the file changed, so nothing was done.
func (s *Service) recordStale(candidate JanitorCandidate, item PlanItem, approval ApprovalRecord, message string, now time.Time) ItemOutcome {
	action, err := NewApprovedAction(
		"action-"+uuid.New().String(), candidate.WorkspaceID, candidate, OperationMove,
		"Filed/Other/"+candidate.Name, approval.UserID, approval.ConsumedAt, approval.IdempotencyKey,
	)
	if err == nil {
		action = action.MarkStale(message, now)
		_ = s.appendAction(candidate.WorkspaceID, action, CandidateStale)
	}
	return ItemOutcome{
		CandidateID: candidate.ID, ActionID: action.ID, Name: candidate.Display(),
		Operation: item.Operation, Result: ResultStale, Message: message,
	}
}

// appendAction journals a new action and moves its candidate to the matching
// state, in one atomic write.
func (s *Service) appendAction(workspaceID string, action FileAction, candidateState CandidateState) error {
	_, err := s.store.UpdateScanState(workspaceID, func(state *ScanState) error {
		state.Actions = append(state.Actions, action)
		for i := range state.Candidates {
			if state.Candidates[i].ID == action.CandidateID {
				state.Candidates[i].State = candidateState
				if action.ErrorSummary != "" {
					state.Candidates[i].StateReason = action.ErrorSummary
				}
				resummarizeBatch(state, state.Candidates[i].BatchID)
				break
			}
		}
		return nil
	})
	return err
}

// updateAction records an action's final outcome and its candidate's state.
func (s *Service) updateAction(workspaceID string, action FileAction, candidateState CandidateState) error {
	_, err := s.store.UpdateScanState(workspaceID, func(state *ScanState) error {
		for i := range state.Actions {
			if state.Actions[i].ID == action.ID {
				state.Actions[i] = action
				break
			}
		}
		for i := range state.Candidates {
			if state.Candidates[i].ID == action.CandidateID {
				state.Candidates[i].State = candidateState
				state.Candidates[i].StateReason = action.ErrorSummary
				resummarizeBatch(state, state.Candidates[i].BatchID)
				break
			}
		}
		return nil
	})
	return err
}

// ListActions returns the workspace's journal, newest first.
func (s *Service) ListActions(workspaceID string) ([]FileAction, error) {
	state, err := s.store.LoadScanState(workspaceID)
	if err != nil {
		return nil, err
	}
	out := make([]FileAction, 0, len(state.Actions))
	for i := len(state.Actions) - 1; i >= 0; i-- {
		out = append(out, state.Actions[i])
	}
	return out, nil
}
