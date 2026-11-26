package filejanitor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/johnjallday/ori-agent/internal/directoryid"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// DeliveredTo is the historical identity of a picked destination. Its private
// locator is not an attached directory. Only File and Undo receive it; the
// journal, History, and receipt carry only a display label and identities.
type DeliveredTo struct {
	Key       string `json:"key"`
	RootDirID string `json:"root_dir_id"`
	FolderID  string `json:"folder_id"`
	path      string
}

func (d DeliveredTo) valid() bool {
	return len(d.Key) == 64 && sameDevice(d.RootDirID, d.FolderID) && d.RootDirID != d.FolderID
}
func (d DeliveredTo) matches(workspaceID, generation, path string) bool {
	return filepath.IsAbs(path) && d.Key == deliveryKey(workspaceID, generation, path, d.RootDirID, d.FolderID)
}
func deliveryKey(ws, generation, path, rootID, destID string) string {
	key, _ := setupDigest([]string{"chosen-folder-v1", ws, generation, path, rootID, destID})
	return key
}

// ChosenSelection identifies exactly the picture the person saw. One file, or
// the current members of one picture section; never a path or a future rule.
type ChosenSelection struct {
	Revision      string `json:"revision"`
	DraftRevision string `json:"draft_revision"`
	CandidateID   string `json:"candidate_id,omitempty"`
	Section       string `json:"section,omitempty"`
	Key           string `json:"key,omitempty"`
}

func (s *Service) chosenSelection(ctx context.Context, owner, id, batch string, req ChosenSelection) (DocumentsSelection, error) {
	var no DocumentsSelection
	if batch == "latest" || len(req.Revision) != 64 || len(req.DraftRevision) != 64 {
		return no, ErrInvalidAction
	}
	snap, err := s.readPictureSnapshot(ctx, owner, id, batch)
	if err != nil {
		return no, err
	}
	if snap.view.Revision != req.DraftRevision || snap.groups.Revision != req.Revision || snap.view.State == "invalid" || snap.view.State == "stale" {
		return no, ErrReviewConflict
	}
	root, err := s.StoredManagedRoot(owner, id)
	if err != nil {
		return no, err
	}
	st, err := os.Lstat(root.Path)
	if err != nil || !st.IsDir() {
		return no, ErrMoveParentChanged
	}
	out := DocumentsSelection{State: "matched", Batch: batch, RootDirID: directoryid.Witness(st)}
	out.Key, _ = setupDigest(req)
	var ids []string
	if req.CandidateID != "" && req.Section == "" && req.Key == "" {
		ids = []string{req.CandidateID}
	} else if req.CandidateID == "" && req.Key != "" && (req.Section == PictureFiled || req.Section == PictureUnfiled || req.Section == PictureLoose) {
		list, e := layoutReviewPicture(snap.groups, snap.view, snap.candidates).pictureList(req.Section, req.Key)
		if e != nil {
			return no, e
		}
		for _, f := range list {
			ids = append(ids, f.ID)
		}
	} else {
		return no, ErrInvalidAction
	}
	if len(ids) < 1 || len(ids) > PlanMaxOperations {
		return no, ErrReviewBounds
	}
	trash := map[string]bool{}
	for _, ch := range snap.view.Choices {
		if ch.Operation == OperationTrash {
			trash[ch.CandidateID] = true
		}
	}
	for _, cid := range ids {
		c, ok := snap.state.Candidate(cid)
		if !ok || c.WorkspaceID != id || c.BatchID != batch || !proposalPending(c) || c.Decision == DecisionTrash || trash[c.ID] || snap.state.IsSkipped(c.Fingerprint) || workspace.ContainedEntryExcluded(c.Name) {
			return no, ErrReviewConflict
		}
		out.CandidateIDs = append(out.CandidateIDs, c.ID)
		out.Names = append(out.Names, c.Name)
	}
	return out, nil
}

// planChosenFolder probes the selected directory and source identities, not
// its contents. It does not enumerate destination entries or reserve a name.
// The conditional mover handles occupied names atomically at File time.
func (s *Service) planChosenFolder(owner, id string, sel DocumentsSelection, path string, state ScanState, root ManagedRoot, scope string) documentsPlan {
	refuse := func(st string) documentsPlan { return documentsPlan{preview: DocumentsPreview{State: st}} }
	guards := DefaultRootGuards()
	canon, err := ValidateRoot(path, guards)
	if err != nil || canon != path || RootsOverlap(path, root.Path) || RootsOverlap(path, guards.WorkspaceRoot) || (guards.DataDir != guards.HomeDir && RootsOverlap(path, guards.DataDir)) {
		return refuse("destination")
	}
	if _, other := s.findConflictingOwner(id, path); other {
		return refuse("destination")
	}
	st, err := os.Lstat(path)
	if err != nil || !st.IsDir() {
		return refuse("destination")
	}
	dirID := directoryid.Witness(st)
	if err = s.ConditionalMoveBetween(root.Path, sel.RootDirID, path, dirID); err != nil {
		return refuse("unsupported")
	}
	label := filepath.Base(path)
	if rel, e := filepath.Rel(guards.HomeDir, path); e == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		label = filepath.ToSlash(rel)
	}
	d := &DeliveredTo{Key: deliveryKey(id, root.Generation, path, sel.RootDirID, dirID), RootDirID: sel.RootDirID, FolderID: dirID, path: path}
	if !d.valid() {
		return refuse("destination")
	}
	plan := documentsPlan{rootDirID: sel.RootDirID, scope: scope, folder: DirRef{Rel: label, ID: dirID}, preview: DocumentsPreview{State: documentsPreviewReady, Folder: label}}
	for i, cid := range sel.CandidateIDs {
		c, ok := state.Candidate(cid)
		if !ok || c.Name != sel.Names[i] || !proposalPending(c) || c.BatchID != sel.Batch || c.WorkspaceID != id {
			return refuse("changed")
		}
		fp, e := currentFingerprint(root.Path, c.Name)
		if e != nil || !sameFileVersion(fp, c.Fingerprint) {
			return refuse("changed")
		}
		plan.items = append(plan.items, documentsPlanItem{candidate: c, finalName: c.Name, delivery: d})
		plan.preview.Items = append(plan.preview.Items, DocumentsPreviewItem{Name: c.Display(), FinalName: c.Display(), To: label + "/" + c.Display()})
	}
	plan.preview.Digest, _ = setupDigest([]string{documentsDigest(id, owner, sel, plan), d.Key})
	return plan
}
func (s *Service) PreviewChosenFolder(ctx context.Context, owner, id, batch, path string, req ChosenSelection) (DocumentsPreview, error) {
	if !filepath.IsAbs(path) {
		return DocumentsPreview{}, ErrInvalidAction
	}
	sel, err := s.chosenSelection(ctx, owner, id, batch, req)
	if err != nil {
		return DocumentsPreview{}, err
	}
	return s.PreviewDocumentsFiling(ctx, owner, id, DocumentsDestination{chosenPath: path}, sel)
}
func (s *Service) FileChosenFolder(ctx context.Context, owner, id, batch, path string, sel ChosenSelection, digest, key string) (DocumentsFilingResult, error) {
	if !filepath.IsAbs(path) {
		return DocumentsFilingResult{}, ErrInvalidAction
	}
	decision, _ := setupDigest([]string{"chosen-press", id, batch, key})
	return s.FileDocumentsSelection(ctx, owner, id, DocumentsFileRequest{Destination: DocumentsDestination{chosenPath: path}, Digest: digest, ClientKey: key, DecisionID: decision}, func(ctx context.Context) (DocumentsSelection, error) {
		return s.chosenSelection(ctx, owner, id, batch, sel)
	})
}

func (s *Service) undoDelivery(ctx context.Context, settings JanitorSettings, root string, a FileAction, owner string) (UndoResult, error) {
	fail := func(why string) (UndoResult, error) { return s.finishUndo(settings.WorkspaceID, a, UndoFailed, why) }
	d := a.Delivery
	state, err := s.store.loadScanState(settings.WorkspaceID, true)
	current, e := s.StoredManagedRoot(owner, settings.WorkspaceID)
	if err != nil || e != nil || current.Path != root || current.Generation != a.RootID || a.WorkspaceID != settings.WorkspaceID || a.ApprovedBy != owner || d == nil || !d.valid() || a.Validate() != nil {
		return fail("The original folders or access changed. Ori did not put the file back.")
	}
	p := state.ChosenPaths[d.Key]
	if !d.matches(a.WorkspaceID, a.RootID, p) || s.ConditionalMoveBetween(p, d.FolderID, root, d.RootDirID) != nil {
		return fail("The chosen folder is unavailable or changed. Ori did not put the file back.")
	}
	_, err = s.conditionalMoverFor().MoveIfUnchanged(ctx, ConditionalMove{Root: p, RootID: d.FolderID, DestRoot: root, DestRootID: d.RootDirID, From: DirRef{ID: d.FolderID}, To: DirRef{ID: d.RootDirID}, FromName: a.SourceName, ToName: a.SourceName, Expect: a.AfterFingerprint})
	if err != nil {
		if errors.Is(err, ErrMoveDestinationOccupied) {
			return fail("The original name is already in use. Nothing was overwritten.")
		}
		return fail("Ori could not verify a safe return. Check the original and chosen folders.")
	}
	result, err := s.finishUndo(settings.WorkspaceID, a, UndoDone, "")
	if err == nil {
		s.suppressRestored(settings.WorkspaceID, root, a.SourceName)
		result.RestoredTo = a.SourceName
		result.Message = "Put back in the original folder."
	}
	return result, err
}
