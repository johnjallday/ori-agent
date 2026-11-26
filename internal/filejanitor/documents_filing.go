package filejanitor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/johnjallday/ori-agent/internal/logger"
)

// Filing a reviewed Documents selection into a folder the person chose.
//
// It follows the same hand-shake as the one-press filing: preview shows the exact
// plan and writes nothing; the press quotes the plan's digest and a retry key;
// under the root lock the server re-derives the selection and the plan, and only
// if the digest still matches does it admit the press in a single scan-state write
// that records a filing and an approval that is already spent. Each file then goes
// through the conditional mover, which moves it only if it is still the reviewed
// file, still going into the reviewed folder, and never replaces an occupied name.
//
// What the person approved are exact names. When a name has been taken since the
// preview, the file stays where it is and is reported; Ori does not pick another.

// Preview states. Anything but "ready" carries no digest and cannot be filed.
const (
	documentsPreviewReady       = "ready"
	documentsPreviewDestination = "destination"
	documentsPreviewUnsupported = "unsupported"
)

// DocumentsDestination names where a selection should go. Folder is a slash path
// from the managed folder. With New set, Folder is the existing parent ("" is the
// managed folder itself) and New is one folder to create inside it; otherwise
// Folder is an existing folder.
type DocumentsDestination struct {
	Folder string `json:"folder"`
	New    string `json:"new,omitempty"`
}

// DocumentsPreviewItem is one file's source and where it will be named.
type DocumentsPreviewItem struct {
	Name      string `json:"name"`
	FinalName string `json:"final_name"`
	To        string `json:"to"`
	Renamed   bool   `json:"renamed,omitempty"`
}

// DocumentsPreview is what filing would do. Building it writes nothing.
type DocumentsPreview struct {
	State  string                 `json:"state"`
	Digest string                 `json:"digest,omitempty"`
	Folder string                 `json:"folder,omitempty"`
	Create []string               `json:"create,omitempty"`
	Items  []DocumentsPreviewItem `json:"items,omitempty"`
}

// DocumentsFileRequest is one press.
type DocumentsFileRequest struct {
	Destination DocumentsDestination
	Digest      string
	ClientKey   string
	// DecisionID is the organizing decision this filing belongs to, fixed by the
	// caller from the retry key so a replay finds the same one.
	DecisionID string
	// shelves is created only by the station, never decoded from JSON. It
	// derives a per-file destination from the same admitted picture.
	shelves *shelfRequest
}

// DocumentsFilingResult is what a press did, built from the journal so the first
// answer and a replay are identical. When the press was refused because the plan
// had changed, Preview is the fresh state and nothing moved.
type DocumentsFilingResult struct {
	FilingID          string            `json:"filing_id,omitempty"`
	DecisionID        string            `json:"decision_id,omitempty"`
	Replayed          bool              `json:"replayed,omitempty"`
	Interrupted       bool              `json:"interrupted,omitempty"`
	Folder            string            `json:"folder,omitempty"`
	FolderCreated     bool              `json:"folder_created,omitempty"`
	FolderUnconfirmed bool              `json:"folder_unconfirmed,omitempty"`
	Filed             int               `json:"filed"`
	Failed            int               `json:"failed"`
	Stale             int               `json:"stale"`
	NotAttempted      int               `json:"not_attempted"`
	Outcomes          []ItemOutcome     `json:"outcomes"`
	Preview           *DocumentsPreview `json:"preview,omitempty"`
}

type documentsPlanItem struct {
	candidate JanitorCandidate
	finalName string
	shelf     DirRef // an existing station shelf; zero for a single Documents destination
}

type documentsPlan struct {
	preview   DocumentsPreview
	rootDirID string
	scope     string
	// folder is the destination folder: existing (ID set) or, with create, the
	// intended path with no ID until it is made.
	folder  DirRef
	parent  DirRef
	newName string
	create  bool
	items   []documentsPlanItem
}

// destinationShape validates a requested destination and returns the final folder
// path. The final folder is never the managed folder itself or the filing folder,
// and at most two levels deep (the depth a review covers).
func destinationShape(dest DocumentsDestination, filedName string) (final string, ok bool) {
	if !validRelPath(dest.Folder) {
		return "", false
	}
	levels := 0
	if dest.Folder != "" {
		levels = len(splitRel(dest.Folder))
	}
	if dest.New != "" {
		if ValidateFileName(dest.New) != nil || levels > 1 {
			return "", false
		}
		final = path.Join(dest.Folder, dest.New)
	} else {
		if dest.Folder == "" || levels > 2 {
			return "", false
		}
		final = dest.Folder
	}
	if strings.EqualFold(splitRel(final)[0], filedName) {
		return "", false
	}
	return final, true
}

// planDocuments works the plan out from stored state and the disk. Every refusal
// is a preview state, not an error: the caller shows it and files nothing.
func (s *Service) planDocuments(owner, workspaceID string, sel DocumentsSelection, dest DocumentsDestination, state ScanState) documentsPlan {
	refused := func(st string) documentsPlan { return documentsPlan{preview: DocumentsPreview{State: st}} }
	if sel.State != "matched" {
		return refused(sel.State)
	}
	if len(sel.CandidateIDs) == 0 || len(sel.CandidateIDs) != len(sel.Names) || sel.RootDirID == "" || sel.Key == "" || sel.Batch == "" {
		return refused("unavailable")
	}
	root, err := s.StoredManagedRoot(owner, workspaceID)
	if err != nil {
		return refused("access")
	}
	settings, err := s.requireConfigured(workspaceID)
	if err != nil {
		return refused("unavailable")
	}
	install, applies, err := s.reviewInstall(owner, workspaceID)
	if err != nil || !applies {
		return refused("install")
	}
	scope := reviewScopeDigest(root, install)
	if scope == "" {
		return refused("unavailable")
	}
	mover := s.conditionalMoverFor()
	if err := mover.Supported(root.Path, sel.RootDirID); err != nil {
		if errors.Is(err, ErrMoveUnsupported) {
			return refused(documentsPreviewUnsupported)
		}
		return refused("changed")
	}

	filedName := strings.TrimSpace(settings.FilingRootName)
	if filedName == "" {
		filedName = DefaultFilingRootName
	}
	final, ok := destinationShape(dest, filedName)
	if !ok {
		return refused(documentsPreviewDestination)
	}

	plan := documentsPlan{rootDirID: sel.RootDirID, scope: scope}
	if dest.New == "" {
		ref, err := mover.Folder(root.Path, sel.RootDirID, final)
		switch {
		case errors.Is(err, fs.ErrNotExist), errors.Is(err, ErrMoveOutsideRoot):
			return refused(documentsPreviewDestination)
		case err != nil:
			return refused("changed")
		}
		plan.folder = ref
	} else {
		parent, err := mover.Folder(root.Path, sel.RootDirID, dest.Folder)
		switch {
		case errors.Is(err, fs.ErrNotExist), errors.Is(err, ErrMoveOutsideRoot):
			return refused(documentsPreviewDestination)
		case err != nil:
			return refused("changed")
		}
		// The new name must be free as the filesystem sees names (case and Unicode
		// composition included): Folder finds an existing entry by those rules.
		if _, err := mover.Folder(root.Path, sel.RootDirID, final); err == nil || errors.Is(err, ErrMoveOutsideRoot) {
			return refused(documentsPreviewDestination)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return refused("changed")
		}
		plan.parent = parent
		plan.newName = dest.New
		plan.create = true
		plan.folder = DirRef{Rel: final}
	}

	dirAbs := filepath.Join(root.Path, filepath.FromSlash(final))
	reserved := map[string]struct{}{}
	for i, id := range sel.CandidateIDs {
		candidate, found := state.Candidate(id)
		if !found || candidate.Name != sel.Names[i] || candidate.WorkspaceID != workspaceID || candidate.BatchID != sel.Batch {
			return refused("changed")
		}
		name, err := resolveAvailableNameReserving(dirAbs, candidate.Name, reserved)
		if err != nil {
			return refused("unavailable")
		}
		plan.items = append(plan.items, documentsPlanItem{candidate: candidate, finalName: name})
	}

	plan.preview = DocumentsPreview{State: documentsPreviewReady, Folder: final}
	if plan.create {
		plan.preview.Create = []string{final}
	}
	for _, item := range plan.items {
		plan.preview.Items = append(plan.preview.Items, DocumentsPreviewItem{
			Name: item.candidate.Name, FinalName: item.finalName, To: path.Join(final, item.finalName), Renamed: item.finalName != item.candidate.Name,
		})
	}
	plan.preview.Digest = documentsDigest(workspaceID, owner, sel, plan)
	return plan
}

// documentsDigest binds a preview to everything a press must still find true: the
// workspace, owner and review scope, the folder identity, the selection (batch and
// the caller's key), each file's reviewed version and its exact final name, the
// destination and what would be created.
func documentsDigest(workspaceID, owner string, sel DocumentsSelection, plan documentsPlan) string {
	parts := []string{
		"file-janitor-documents-v1", workspaceID, owner, plan.scope, plan.rootDirID, sel.Batch, sel.Key,
		plan.folder.Rel, plan.folder.ID, plan.parent.ID, plan.newName, fmt.Sprint(plan.create),
		strings.Join(plan.preview.Create, "\x01"),
	}
	lines := make([]string, 0, len(plan.items))
	for _, item := range plan.items {
		lines = append(lines, strings.Join([]string{item.candidate.ID, item.candidate.Name, item.candidate.Fingerprint.Key(), item.finalName}, "|"))
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(append(parts, lines...), "\x00")))
	return hex.EncodeToString(sum[:])
}

// PreviewDocumentsFiling shows what filing the selection into dest would do. It
// writes nothing: not to the folder, not to the journal, not to the scan state.
func (s *Service) PreviewDocumentsFiling(ctx context.Context, owner, workspaceID string, dest DocumentsDestination, sel DocumentsSelection) (DocumentsPreview, error) {
	if s == nil || s.store == nil || ctx.Err() != nil {
		return DocumentsPreview{State: "unavailable"}, ErrReviewUnavailable
	}
	state, err := s.store.loadScanState(workspaceID, true)
	if err != nil {
		return DocumentsPreview{State: "unavailable"}, err
	}
	return s.planDocuments(owner, workspaceID, sel, dest, state).preview, nil
}

// documentsFilingFor finds the filing admitted under a retry key.
func documentsFilingFor(state ScanState, clientKey string) (FilingRecord, bool) {
	for _, existing := range state.Filings {
		if existing.ClientKey == clientKey {
			return existing, true
		}
	}
	return FilingRecord{}, false
}

// sameDocumentsIntent reports whether a retry is the same press as the admitted one.
func sameDocumentsIntent(existing FilingRecord, owner string, req DocumentsFileRequest) bool {
	return existing.Owner == owner && (existing.ShelfPicture && req.shelves != nil || existing.Destination != nil && req.shelves == nil) && existing.PlanDigest == req.Digest && existing.DecisionID == req.DecisionID
}

// FileDocumentsSelection files exactly the plan the person last saw, or nothing.
// reselect re-derives the caller's selection (the saved view, kept group and
// chosen files) inside the call, under the root lock.
func (s *Service) FileDocumentsSelection(ctx context.Context, owner, workspaceID string, req DocumentsFileRequest, reselect func(context.Context) (DocumentsSelection, error)) (DocumentsFilingResult, error) {
	if s == nil || s.store == nil || ctx.Err() != nil || reselect == nil {
		return DocumentsFilingResult{}, ErrReviewUnavailable
	}
	if !clientKeyPattern.MatchString(req.ClientKey) || len(req.Digest) != 64 || !managedRootGeneration.MatchString(req.DecisionID) {
		return DocumentsFilingResult{}, ErrInvalidAction
	}
	root, err := s.StoredManagedRoot(owner, workspaceID)
	if err != nil {
		return DocumentsFilingResult{}, ErrReviewUnavailable
	}
	settings, err := s.requireConfigured(workspaceID)
	if err != nil {
		return DocumentsFilingResult{}, err
	}
	resolved, err := s.scannerFor().ResolveRoot(settings)
	if err != nil || resolved != root.Path || settings.RootID != root.Generation {
		return DocumentsFilingResult{}, ErrReviewUnavailable
	}

	unlock := lockRoot(root.Path)
	defer unlock()

	// A retry of an admitted press reports the first result; the same key for
	// anything else is refused. An unfinished run that is not running is reconciled
	// against the disk first.
	state, err := s.store.LoadScanState(workspaceID)
	if err != nil {
		return DocumentsFilingResult{}, err
	}
	if existing, found := documentsFilingFor(state, req.ClientKey); found {
		if !sameDocumentsIntent(existing, owner, req) {
			return DocumentsFilingResult{}, ErrFilingKeyReused
		}
		s.reconcilePlacements(workspaceID, root.Path)
		state, err = s.store.LoadScanState(workspaceID)
		if err != nil {
			return DocumentsFilingResult{}, err
		}
		existing, _ = documentsFilingFor(state, req.ClientKey)
		return s.documentsResult(state, existing, true), nil
	}
	s.reconcilePlacements(workspaceID, root.Path)

	sel, err := reselect(ctx)
	if err != nil {
		return DocumentsFilingResult{}, err
	}
	if sel.State != "matched" {
		return DocumentsFilingResult{Outcomes: []ItemOutcome{}, Preview: &DocumentsPreview{State: sel.State}}, ErrReviewPlanChanged
	}
	if err := s.ConditionalMoveAvailable(root.Path, sel.RootDirID); err != nil {
		return DocumentsFilingResult{Outcomes: []ItemOutcome{}, Preview: &DocumentsPreview{State: documentsPreviewUnsupported}}, err
	}

	var admitted struct {
		record   FilingRecord
		approval ApprovalRecord
		plan     documentsPlan
		fresh    DocumentsPreview
	}
	_, err = s.store.UpdateScanState(workspaceID, func(state *ScanState) error {
		if existing, found := documentsFilingFor(*state, req.ClientKey); found {
			if !sameDocumentsIntent(existing, owner, req) {
				return ErrFilingKeyReused
			}
			admitted.record = existing
			return errFilingReplay
		}
		for _, existing := range state.Filings {
			if existing.DecisionID == req.DecisionID {
				return ErrFilingKeyReused
			}
		}
		for i := range state.Filings {
			if state.Filings[i].CompletedAt.IsZero() && !state.Filings[i].running() {
				state.Filings[i].Interrupted = true // the process died after admitting it
			}
		}
		var plan documentsPlan
		if req.shelves != nil {
			plan, err = s.planShelves(ctx, owner, workspaceID, sel.Batch, *req.shelves, *state)
			if err != nil {
				return err
			}
		} else {
			plan = s.planDocuments(owner, workspaceID, sel, req.Destination, *state)
		}
		admitted.fresh = plan.preview
		if plan.preview.State != documentsPreviewReady || plan.preview.Digest != req.Digest {
			return ErrReviewPlanChanged
		}
		admitted.plan = plan

		now := s.clock()
		invalidateBatchApprovals(state, sel.Batch)
		filingID := "filing-" + uuid.New().String()
		_, tokenHash, err := newApprovalToken() // thrown away: this approval is spent at birth
		if err != nil {
			return err
		}
		admitted.approval = ApprovalRecord{
			ID: "approval-" + uuid.New().String(), TokenHash: tokenHash, UserID: owner,
			WorkspaceID: workspaceID, BatchID: sel.Batch, PayloadHash: plan.preview.Digest,
			ReviewRevision: sel.Key, ReviewScope: plan.scope,
			IdempotencyKey: filingID, IssuedAt: now, ExpiresAt: now.Add(ApprovalTTL), ConsumedAt: now,
		}
		destination := &FilingDestination{Folder: plan.folder.Rel, RootDirID: plan.rootDirID, FolderID: plan.folder.ID, Create: plan.create}
		if plan.create {
			destination.ParentID = plan.parent.ID
		}
		admitted.record = FilingRecord{
			ID: filingID, Owner: owner, BatchID: sel.Batch, RootID: root.Generation, ClientKey: req.ClientKey,
			PlanDigest: plan.preview.Digest, PlannedMoves: len(plan.items), AdmittedAt: now,
			Destination: destination, DecisionID: req.DecisionID,
		}
		if req.shelves != nil {
			admitted.record.Destination = nil
			admitted.record.ShelfPicture = true
		}
		state.Approvals = append(state.Approvals, admitted.approval)
		state.Filings = append(state.Filings, admitted.record)
		return nil
	})
	switch {
	case errors.Is(err, errFilingReplay):
		latest, loadErr := s.store.LoadScanState(workspaceID)
		if loadErr != nil {
			return DocumentsFilingResult{}, loadErr
		}
		return s.documentsResult(latest, admitted.record, true), nil
	case errors.Is(err, ErrReviewPlanChanged):
		return DocumentsFilingResult{Outcomes: []ItemOutcome{}, Preview: &admitted.fresh}, err
	case err != nil:
		return DocumentsFilingResult{}, err
	}

	// Admitted. From here it finishes even if the caller goes away: a retry with
	// the same key then reports the result.
	runCtx := context.WithoutCancel(ctx)
	runningFilings.Store(admitted.record.ID, struct{}{})
	defer runningFilings.Delete(admitted.record.ID)

	plan := admitted.plan
	folder := plan.folder
	var unrecorded []FilingUnrecorded
	refuseAll := func(reason string) {
		for _, item := range plan.items {
			unrecorded = append(unrecorded, FilingUnrecorded{CandidateID: item.candidate.ID, Reason: reason})
		}
	}
	ready := true
	if plan.create {
		made, makeErr := s.conditionalMoverFor().MakeFolder(runCtx, root.Path, plan.rootDirID, plan.parent, plan.newName)
		if makeErr == nil {
			_, makeErr = s.store.UpdateScanState(workspaceID, func(state *ScanState) error {
				for i := range state.Filings {
					if state.Filings[i].ID == admitted.record.ID && state.Filings[i].Destination != nil {
						state.Filings[i].Destination.FolderID = made.ID
						state.Filings[i].Destination.CreatedAt = s.clock()
						return nil
					}
				}
				return fmt.Errorf("%w: the filing record is gone", ErrReviewUnavailable)
			})
		}
		if makeErr != nil {
			logger.Warn("File Janitor could not make the destination folder", logger.Fields{
				"workspace_id": workspaceID, "filing_id": admitted.record.ID, "error": makeErr.Error(),
			})
			ready = false
			refuseAll("The new folder could not be made, so nothing was moved.")
		} else {
			folder = made
			folder.Rel = plan.folder.Rel
		}
	}
	if ready {
		for _, item := range plan.items {
			planItem := PlanItem{CandidateID: item.candidate.ID, Operation: OperationPlace, FingerprintKey: item.candidate.Fingerprint.Key()}
			target := folder
			if admitted.record.ShelfPicture {
				target = item.shelf
				planItem.explicitShelf = true
			}
			outcome := s.placeOne(runCtx, settings, root.Path, admitted.approval, planItem, item, target, plan.rootDirID)
			if outcome.ActionID == "" {
				unrecorded = append(unrecorded, FilingUnrecorded{CandidateID: item.candidate.ID, Reason: outcome.Message})
			}
		}
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
		return DocumentsFilingResult{}, err
	}
	return s.documentsResult(final, admitted.record, false), nil
}

// documentsResult builds the answer from the journal.
func (s *Service) documentsResult(state ScanState, record FilingRecord, replayed bool) DocumentsFilingResult {
	r := s.filingResult(state, record, replayed)
	out := DocumentsFilingResult{
		FilingID: r.FilingID, DecisionID: record.DecisionID, Replayed: r.Replayed, Interrupted: r.Interrupted,
		Filed: r.Filed, Failed: r.Failed, Stale: r.Stale, NotAttempted: r.NotAttempted, Outcomes: r.Outcomes,
	}
	if d := record.Destination; d != nil {
		out.Folder = d.Folder
		out.FolderCreated = d.Create && !d.CreatedAt.IsZero()
		out.FolderUnconfirmed = d.Create && d.CreatedAt.IsZero() && (!record.CompletedAt.IsZero() || r.Interrupted)
	}
	return out
}

// newPlacedAction builds the journal entry for one placement.
func newPlacedAction(id string, candidate JanitorCandidate, destination string, approval ApprovalRecord, rootID string, placed PlacedInto) (FileAction, error) {
	action := FileAction{
		ID: id, WorkspaceID: candidate.WorkspaceID, RootID: strings.TrimSpace(rootID), BatchID: candidate.BatchID, CandidateID: candidate.ID,
		Operation: OperationPlace, SourceName: candidate.Name, DestinationRelative: destination, Place: &placed,
		BeforeFingerprint: candidate.Fingerprint, ApprovedAt: approval.ConsumedAt, ApprovedBy: approval.UserID,
		IdempotencyKey: approval.IdempotencyKey, Result: ResultPending, Undo: UndoUnavailable,
	}
	if err := action.Validate(); err != nil {
		return FileAction{}, err
	}
	return action, nil
}

// placementRefusal explains a move the conditional mover refused or reversed.
func placementRefusal(err error) (message string, nothingMoved bool) {
	switch {
	case errors.Is(err, ErrMoveDestinationOccupied):
		return "That name was taken before Ori could move the file, so it was left where it was. Preview the filing again.", true
	case errors.Is(err, ErrMoveSourceChanged):
		return "This file changed after you reviewed it, so Ori left it where it was.", true
	case errors.Is(err, ErrMoveParentChanged), errors.Is(err, ErrMoveOutsideRoot), errors.Is(err, ErrMoveCrossDevice):
		return "A folder changed after you reviewed it, so Ori left the file where it was.", true
	}
	return "Ori could not move this file.", false
}

// placeOne files one reviewed file: validate, journal "applying", hand to the
// conditional mover, then record the verified outcome. It never returns an error;
// every outcome, including a refusal, is that file's result.
func (s *Service) placeOne(ctx context.Context, settings JanitorSettings, root string, approval ApprovalRecord, planItem PlanItem, item documentsPlanItem, folder DirRef, rootDirID string) ItemOutcome {
	candidate := item.candidate
	failed := func(message string) ItemOutcome {
		return ItemOutcome{CandidateID: candidate.ID, Name: candidate.Display(), Operation: OperationPlace, Result: ResultFailed, Message: message}
	}
	state, err := s.store.LoadScanState(settings.WorkspaceID)
	if err != nil {
		return failed("Ori could not read this workspace's state.")
	}
	current, ok := state.Candidate(candidate.ID)
	if !ok {
		return failed("That file is no longer in this batch.")
	}
	candidate = current
	if err := s.validateExecutionScope(settings, root, approval); err != nil || state.IsSkipped(candidate.Fingerprint) || !validExecutionCandidate(candidate, planItem, approval, false) {
		return ItemOutcome{CandidateID: candidate.ID, Name: candidate.Display(), Operation: OperationPlace, Result: ResultStale, Message: "The folder, access or saved review changed. Nothing was moved; request a fresh review."}
	}

	relative := path.Join(folder.Rel, item.finalName)
	action, err := newPlacedAction("action-"+uuid.New().String(), candidate, relative, approval, settings.RootID, PlacedInto{RootDirID: rootDirID, FolderID: folder.ID})
	if err != nil {
		return failed("Ori could not record this action, so it did not perform it.")
	}
	action = action.MarkApplying(s.clock())
	if err := s.appendApprovedAction(settings, root, approval, planItem, action); err != nil {
		return failed("Ori could not record this action, so it did not perform it.")
	}
	if err := s.executionHandoff(settings, root, approval, planItem); err != nil {
		action = action.MarkStale("The folder, access or file changed before execution. Nothing was moved; request a fresh review.", s.clock())
		_ = s.updateAction(settings.WorkspaceID, action, CandidateStale)
		return outcomeFor(action)
	}

	after, moveErr := s.conditionalMoverFor().MoveIfUnchanged(ctx, ConditionalMove{
		Root: root, RootID: rootDirID,
		From: DirRef{Rel: "", ID: rootDirID}, To: DirRef{Rel: folder.Rel, ID: folder.ID},
		FromName: candidate.Name, ToName: item.finalName, Expect: candidate.Fingerprint,
	})
	if moveErr == nil {
		action = action.MarkApplied(after, s.clock())
		_ = s.updateAction(settings.WorkspaceID, action, CandidateApplied)
		return outcomeFor(action)
	}

	var race *MoveRaceError
	if errors.As(moveErr, &race) && !race.RolledBack {
		// The file went somewhere and could not be moved back. Say where.
		p := *action.Place
		p.LeftAt = race.LeftAt
		action.Place = &p
		summary := "Ori moved this file and could not put it back where it was."
		if race.LeftAt != "" {
			summary += " It is in " + race.LeftAt + "."
		}
		action = action.MarkFailed(summary, s.clock())
		_ = s.updateAction(settings.WorkspaceID, action, CandidateFailed)
		logger.Warn("File Janitor placement could not be reversed", logger.Fields{
			"workspace_id": settings.WorkspaceID, "action_id": action.ID, "candidate_id": candidate.ID, "reviewed_file_left": race.LeftIsReviewed,
		})
		return outcomeFor(action)
	}
	message, nothingMoved := placementRefusal(moveErr)
	if nothingMoved {
		action = action.MarkStale(message, s.clock())
		_ = s.updateAction(settings.WorkspaceID, action, CandidateStale)
		return outcomeFor(action)
	}
	logger.Warn("File Janitor placement failed", logger.Fields{
		"workspace_id": settings.WorkspaceID, "action_id": action.ID, "candidate_id": candidate.ID, "error": moveErr.Error(),
	})
	action = action.MarkFailed(message, s.clock())
	_ = s.updateAction(settings.WorkspaceID, action, CandidateFailed)
	return outcomeFor(action)
}

// sameFileVersion reports whether two fingerprints describe the same file by
// identity, size and modification time, ignoring the name (a placement may rename).
func sameFileVersion(a, b Fingerprint) bool {
	return a.FileID != "" && a.FileID == b.FileID && a.Size == b.Size && a.ModTime.Equal(b.ModTime)
}

// reconcilePlacements settles every placement left "applying" by a process that
// died or lost its response, by looking at where the file is. It never moves
// anything. The caller holds lockRoot.
func (s *Service) reconcilePlacements(workspaceID, root string) {
	state, err := s.store.LoadScanState(workspaceID)
	if err != nil {
		return
	}
	for _, action := range state.Actions {
		if action.Operation != OperationPlace || action.Result != ResultApplying {
			continue
		}
		if _, running := runningFilings.Load(action.IdempotencyKey); running {
			continue
		}
		s.reconcilePlacement(workspaceID, root, action)
	}
}

func (s *Service) reconcilePlacement(workspaceID, root string, action FileAction) {
	destination := filepath.Join(root, filepath.FromSlash(action.DestinationRelative))
	if !withinRoot(root, destination) {
		return
	}
	source, sourceErr := currentFingerprint(root, action.SourceName)
	atSource := sourceErr == nil && sameFileVersion(action.BeforeFingerprint, source)
	moved, movedErr := currentFingerprintAt(filepath.Dir(destination), filepath.Base(destination))
	atDestination := movedErr == nil && sameFileVersion(action.BeforeFingerprint, moved)

	now := s.clock()
	switch {
	case atDestination && !atSource:
		action = action.MarkApplied(moved, now)
		_ = s.updateAction(workspaceID, action, CandidateApplied)
	case atSource && !atDestination:
		action = action.MarkStale("Ori stopped before moving this file. It was not moved.", now)
		_, _ = s.store.UpdateScanState(workspaceID, func(state *ScanState) error {
			for i := range state.Actions {
				if state.Actions[i].ID == action.ID {
					state.Actions[i] = action
				}
			}
			for i := range state.Candidates {
				if state.Candidates[i].ID == action.CandidateID {
					state.Candidates[i].State = CandidatePending
					state.Candidates[i].StateReason = ""
					resummarizeBatch(state, state.Candidates[i].BatchID)
				}
			}
			return nil
		})
	default:
		action = action.MarkFailed("Ori could not tell whether this file moved. Check the folder.", now)
		_ = s.updateAction(workspaceID, action, CandidateFailed)
	}
}
