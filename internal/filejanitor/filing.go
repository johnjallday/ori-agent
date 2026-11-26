package filejanitor

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// One press files what the picture shows, or nothing. The request quotes the
// saved choices' revisions, the plan digest it saw, how many files it believes
// are going to the Trash, and a retry key. Under the root lock the server works
// the plan out again; only if every one of those still matches does it admit the
// press, in a single scan-state write that records a filing and an approval that
// is already spent. The existing per-file engine (validate, journal, move, verify)
// then runs under that approval unchanged.
//
// The limit of "nothing moves": it holds up to admission. After it, each file is
// still checked at the moment it is touched (a file changed since is reported
// stale, a new name clash gets another free name), so a receipt can differ from
// the picture, and says what happened.

var (
	// ErrTrashNotAcknowledged means files are going to the Trash and the request
	// did not state how many. Nothing was done.
	ErrTrashNotAcknowledged = errors.New("the number of files going to the Trash was not acknowledged")

	clientKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16,128}$`)

	// errFilingReplay is internal: the retry key was already admitted, so the first
	// result is reported and nothing is written.
	errFilingReplay = errors.New("this press was already admitted")

	// ErrFilingKeyReused means a retry key was already used for a different filing.
	// Nothing was done.
	ErrFilingKeyReused = errors.New("that retry key was already used for a different filing")

	// runningFilings holds the filings this process is executing right now, so a
	// record with no completion time is told apart: still running, or interrupted
	// by a crash.
	runningFilings sync.Map
)

// FilingUnrecorded is a planned file that has no journal entry because the
// engine refused it before it could be journaled.
type FilingUnrecorded struct {
	CandidateID string `json:"candidate_id"`
	Reason      string `json:"reason"`
}

// FilingRecord is one admitted press. It carries no path, token or authority.
type FilingRecord struct {
	ID           string    `json:"id"`
	Owner        string    `json:"owner"`
	BatchID      string    `json:"batch_id"`
	RootID       string    `json:"root_id"`
	ClientKey    string    `json:"client_key"`
	PlanDigest   string    `json:"plan_digest"`
	PlannedMoves int       `json:"planned_moves"`
	PlannedTrash int       `json:"planned_trash"`
	AdmittedAt   time.Time `json:"admitted_at"`
	CompletedAt  time.Time `json:"completed_at,omitzero"`
	// Interrupted is set when a later admission found this run unfinished and not
	// running: the process died after admitting it.
	Interrupted bool               `json:"interrupted,omitempty"`
	Unrecorded  []FilingUnrecorded `json:"unrecorded,omitempty"`
	// Undo all claims one run per filing (task 5.2).
	UndoClaimedAt   time.Time `json:"undo_claimed_at,omitzero"`
	UndoClaimedBy   string    `json:"undo_claimed_by,omitempty"`
	UndoCompletedAt time.Time `json:"undo_completed_at,omitzero"`

	// Destination and DecisionID are set only on a filing into a folder the person
	// chose (the Documents path). Additive: an older build drops them on rewrite and
	// then cannot treat the filing as one of its own category filings.
	Destination *FilingDestination `json:"destination,omitempty"`
	// A station picture may have a different shelf for each file. The exact
	// paths/identities live in each placement's existing journal entry.
	ShelfPicture bool `json:"shelf_picture,omitempty"`
	// DecisionID is the one organizing decision this filing belongs to, fixed at
	// admission so the decision can be written after the move and backfilled.
	DecisionID string `json:"decision_id,omitempty"`
}

// FilingDestination is the folder a named filing put files into, with the
// identities that were verified. Folder is relative to the managed folder.
type FilingDestination struct {
	Folder    string `json:"folder"`
	RootDirID string `json:"root_dir_id"`
	// FolderID is the destination folder's identity. When Create is set it is empty
	// until the folder exists, and CreatedAt is zero until it is recorded.
	FolderID string `json:"folder_id,omitempty"`
	// ParentID is the identity of the folder the new folder is made in (Create only).
	ParentID  string    `json:"parent_id,omitempty"`
	Create    bool      `json:"create,omitempty"`
	CreatedAt time.Time `json:"created_at,omitzero"`
}

func (f FilingRecord) validate() error {
	if !managedRootGeneration.MatchString(f.ID) || strings.TrimSpace(f.Owner) == "" ||
		!managedRootGeneration.MatchString(f.BatchID) || !managedRootGeneration.MatchString(f.RootID) ||
		!clientKeyPattern.MatchString(f.ClientKey) || f.AdmittedAt.IsZero() ||
		f.PlannedMoves < 0 || f.PlannedTrash < 0 || f.PlannedMoves+f.PlannedTrash > PlanMaxOperations {
		return fmt.Errorf("%w: invalid filing record", ErrInvalidCandidate)
	}
	if f.ShelfPicture {
		if f.Destination != nil || f.PlannedTrash != 0 || !managedRootGeneration.MatchString(f.DecisionID) {
			return fmt.Errorf("%w: invalid shelf filing", ErrInvalidCandidate)
		}
	} else if (f.Destination == nil) != (f.DecisionID == "") {
		return fmt.Errorf("%w: invalid filing record", ErrInvalidCandidate)
	}
	if f.Destination != nil {
		d := f.Destination
		validFolder := d.Folder != "" && validRelPath(d.Folder)
		created := d.Create && d.ParentID != "" && (d.FolderID == "") == d.CreatedAt.IsZero()
		existing := !d.Create && d.FolderID != "" && d.ParentID == ""
		if !managedRootGeneration.MatchString(f.DecisionID) || f.PlannedTrash != 0 || !validFolder || d.RootDirID == "" || (!created && !existing) {
			return fmt.Errorf("%w: invalid filing record", ErrInvalidCandidate)
		}
	}
	return nil
}

// running reports whether this process is executing the filing right now.
func (f FilingRecord) running() bool {
	_, running := runningFilings.Load(f.ID)
	return running
}

// FileRequest is one press: the plan the picture last showed, and a retry key.
type FileRequest struct {
	ExpectedRevision      string
	ExpectedGroupRevision string
	PlanDigest            string
	// TrashAcknowledged is how many files the person was shown going to the Trash
	// and ticked "Yes, move N files to Trash" for. It must equal the plan's count.
	TrashAcknowledged int
	// ClientKey makes a retry report the first result instead of filing twice.
	ClientKey string
}

// FilingResult is what a press did, built from the journal so the first answer
// and a replay are identical. When the press was refused because the plan had
// changed, Plan is the fresh one and nothing moved.
type FilingResult struct {
	FilingID    string `json:"filing_id,omitempty"`
	Replayed    bool   `json:"replayed,omitempty"`
	Interrupted bool   `json:"interrupted,omitempty"`
	Filed       int    `json:"filed"`
	// Sent counts the filed files that went to another workspace; they are also
	// counted in Filed.
	Sent         int           `json:"sent,omitempty"`
	Trashed      int           `json:"trashed"`
	Failed       int           `json:"failed"`
	Stale        int           `json:"stale"`
	NotAttempted int           `json:"not_attempted"`
	Outcomes     []ItemOutcome `json:"outcomes"`
	Plan         ReviewPlan    `json:"plan,omitempty"`
}

// FileReviewPlan applies exactly the plan the person last saw, or nothing.
func (s *Service) FileReviewPlan(ctx context.Context, owner, workspaceID, batchID string, req FileRequest) (FilingResult, error) {
	if s == nil || s.store == nil || ctx.Err() != nil || batchID == "latest" || !managedRootGeneration.MatchString(batchID) {
		return FilingResult{}, ErrReviewUnavailable
	}
	if !clientKeyPattern.MatchString(req.ClientKey) || len(req.ExpectedRevision) != 64 ||
		len(req.ExpectedGroupRevision) != 64 || req.TrashAcknowledged < 0 {
		return FilingResult{}, ErrInvalidAction
	}
	if s.mover == nil {
		return FilingResult{}, fmt.Errorf("%w: no move mechanism is configured", ErrInvalidAction)
	}
	root, err := s.StoredManagedRoot(owner, workspaceID)
	if err != nil {
		return FilingResult{}, ErrReviewUnavailable
	}
	settings, err := s.requireConfigured(workspaceID)
	if err != nil {
		return FilingResult{}, err
	}
	resolved, err := s.scannerFor().ResolveRoot(settings)
	if err != nil || resolved != root.Path || settings.RootID != root.Generation {
		return FilingResult{}, ErrReviewUnavailable
	}

	// The same boundary every review edit takes before its scan-state write: an
	// edit that wins first changes the plan this press quoted; an apply already
	// admitted finishes before a later edit. A press that sends also holds each
	// receiver's folder, taken in one fixed order (lockRoots), so a send and that
	// folder's own filing never overlap.
	routes0 := s.liveSendRoutes(owner, workspaceID)
	unlock := lockRoots(append([]string{root.Path}, routes0.receiverRoots()...)...)
	defer unlock()
	s.reconcileSends(workspaceID, root.Path, routes0)
	// Worked out again under the locks: a connection turned off, or either folder
	// changed, while this press waited is a changed plan, and nothing moves.
	routes := s.liveSendRoutes(owner, workspaceID)
	if !sameRoutes(routes0, routes) {
		fresh := ReviewPlan{}
		if state, err := s.store.LoadScanState(workspaceID); err == nil {
			if computed, err := s.computeReviewPlan(owner, workspaceID, batchID, state, routes); err == nil {
				fresh = computed.plan
			}
		}
		return FilingResult{Plan: fresh, Outcomes: []ItemOutcome{}}, ErrReviewPlanChanged
	}

	var admitted struct {
		record   FilingRecord
		approval ApprovalRecord
		items    []PlanItem
		fresh    ReviewPlan
	}
	_, err = s.store.UpdateScanState(workspaceID, func(state *ScanState) error {
		for _, existing := range state.Filings {
			if existing.ClientKey == req.ClientKey {
				if existing.Owner != owner || existing.BatchID != batchID || existing.DecisionID != "" {
					return ErrReviewConflict
				}
				admitted.record = existing
				return errFilingReplay
			}
		}
		for i := range state.Filings {
			if state.Filings[i].CompletedAt.IsZero() && !state.Filings[i].running() {
				state.Filings[i].Interrupted = true // the process died after admitting it
			}
		}
		computed, err := s.computeReviewPlan(owner, workspaceID, batchID, *state, routes)
		if err != nil {
			return err
		}
		admitted.fresh = computed.plan
		if computed.plan.OverLimit {
			return ErrReviewBounds
		}
		if req.ExpectedRevision != computed.plan.Revision || req.ExpectedGroupRevision != computed.plan.GroupRevision ||
			req.PlanDigest != computed.plan.PlanDigest {
			return ErrReviewPlanChanged
		}
		if computed.plan.Total == 0 {
			return fmt.Errorf("%w: there is nothing to file", ErrInvalidAction)
		}
		if req.TrashAcknowledged != computed.plan.TrashCount {
			return ErrTrashNotAcknowledged
		}

		// Admission. Everything above only read; this is the one write that lets the
		// press go ahead: any edit that committed before it won, any attempted after
		// it waits on the root lock and then fails its compare-and-swap.
		now := s.clock()
		invalidateBatchApprovals(state, batchID)
		filingID := "filing-" + uuid.New().String()
		_, tokenHash, err := newApprovalToken() // the token is thrown away: this approval is spent at birth
		if err != nil {
			return err
		}
		admitted.approval = ApprovalRecord{
			ID: "approval-" + uuid.New().String(), TokenHash: tokenHash, UserID: owner,
			WorkspaceID: workspaceID, BatchID: batchID, PayloadHash: PayloadHash(workspaceID, batchID, computed.items),
			ReviewRevision: computed.plan.Revision, ReviewScope: computed.binding.Digest,
			IdempotencyKey: filingID, IssuedAt: now, ExpiresAt: now.Add(ApprovalTTL), ConsumedAt: now,
		}
		admitted.record = FilingRecord{
			ID: filingID, Owner: owner, BatchID: batchID, RootID: root.Generation, ClientKey: req.ClientKey,
			PlanDigest: computed.plan.PlanDigest, PlannedMoves: computed.plan.MoveCount + computed.plan.SendCount, PlannedTrash: computed.plan.TrashCount,
			AdmittedAt: now,
		}
		admitted.items = computed.items
		state.Approvals = append(state.Approvals, admitted.approval)
		state.Filings = append(state.Filings, admitted.record)
		return nil
	})
	switch {
	case errors.Is(err, errFilingReplay):
		state, loadErr := s.store.LoadScanState(workspaceID)
		if loadErr != nil {
			return FilingResult{}, loadErr
		}
		return s.filingResult(state, admitted.record, true), nil
	case errors.Is(err, ErrReviewPlanChanged), errors.Is(err, ErrTrashNotAcknowledged):
		return FilingResult{Plan: admitted.fresh, Outcomes: []ItemOutcome{}}, err
	case err != nil:
		return FilingResult{}, err
	}

	// The press was admitted. From here it finishes even if the caller goes away: a
	// retry with the same key then reports the result.
	runCtx := context.WithoutCancel(ctx)
	runningFilings.Store(admitted.record.ID, struct{}{})
	defer runningFilings.Delete(admitted.record.ID)
	var unrecorded []FilingUnrecorded
	for _, item := range admitted.items {
		var outcome ItemOutcome
		switch item.Operation {
		case OperationTrash:
			outcome = s.trashOne(runCtx, settings, root.Path, admitted.approval, item)
		case OperationSend:
			route, _ := routes.byID(item.ConnectionID)
			outcome = s.sendOne(runCtx, settings, root.Path, admitted.approval, item, route)
		default:
			outcome = s.applyOne(runCtx, settings, root.Path, admitted.approval, item)
		}
		if outcome.ActionID == "" {
			unrecorded = append(unrecorded, FilingUnrecorded{CandidateID: item.CandidateID, Reason: outcome.Message})
		}
	}
	// Each receiver records what arrived, still under both folders' locks, in a
	// write of its own (FR 36).
	if len(routes) > 0 {
		s.recordArrivals(workspaceID, admitted.record.ID, routes)
	}
	final, err := s.store.UpdateScanState(workspaceID, func(state *ScanState) error {
		for i := range state.Filings {
			if state.Filings[i].ID == admitted.record.ID {
				state.Filings[i].CompletedAt = s.clock()
				state.Filings[i].Unrecorded = unrecorded
				admitted.record = state.Filings[i]
				return nil
			}
		}
		return fmt.Errorf("%w: the filing record is gone", ErrReviewUnavailable)
	})
	if err != nil {
		return FilingResult{}, err
	}
	return s.filingResult(final, admitted.record, false), nil
}

// filingResult builds the answer from the journal. A replay is identical to the
// first answer apart from the Replayed flag.
func (s *Service) filingResult(state ScanState, record FilingRecord, replayed bool) FilingResult {
	result := FilingResult{FilingID: record.ID, Replayed: replayed, Outcomes: []ItemOutcome{}}
	for _, action := range state.Actions {
		if action.IdempotencyKey == record.ID {
			result.Outcomes = append(result.Outcomes, outcomeFor(action))
		}
	}
	for _, missing := range record.Unrecorded {
		name := ""
		if candidate, found := state.Candidate(missing.CandidateID); found {
			name = candidate.Display()
		}
		result.Outcomes = append(result.Outcomes, ItemOutcome{CandidateID: missing.CandidateID, Name: name, Result: ResultFailed, Message: missing.Reason})
	}
	for _, outcome := range result.Outcomes {
		switch {
		case outcome.Result == ResultApplied && outcome.Operation == OperationTrash:
			result.Trashed++
		case outcome.Result == ResultApplied:
			result.Filed++
			if outcome.Operation == OperationSend {
				result.Sent++
			}
		case outcome.Result == ResultFailed:
			result.Failed++
		case outcome.Result == ResultStale:
			result.Stale++
		}
	}
	if !record.CompletedAt.IsZero() {
		return result
	}
	// Not completed: still running here, or the process died. Say which, and count
	// the files that were never attempted.
	result.Interrupted = !record.running()
	if left := record.PlannedMoves + record.PlannedTrash - len(result.Outcomes); left > 0 {
		result.NotAttempted = left
	}
	return result
}
