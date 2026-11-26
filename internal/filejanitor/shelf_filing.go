package filejanitor

import (
	"context"
	"errors"
	"io/fs"
	"path"
	"sort"
)

// ShelfPlacement identifies one ticked file and the exact existing shelf it
// was placed on. Neither a path nor an identity supplied by the client can
// become a mover operand. Parent names the top-level shelf key for a nested one.
type ShelfPlacement struct {
	File    string `json:"file"`
	Shelf   string `json:"shelf"`
	Parent  string `json:"parent,omitempty"`
	Located string `json:"located,omitempty"`  // lookup hint for an explicitly found folder, never authority
	NewName string `json:"new_name,omitempty"` // mutually exclusive with Shelf
}
type shelfRequest struct {
	Batch      string
	Revision   string
	Placements []ShelfPlacement
}

func (p shelfRequest) digest() (string, error) {
	return setupDigest([]any{"file-janitor-shelves-v1", p.Batch, p.Revision, p.Placements})
}

// FileShelfPicture goes through the reviewed Documents placement engine. All
// shelves share one admission, journal key, receipt, and Undo all action.
func (s *Service) FileShelfPicture(ctx context.Context, owner, id, batch, revision, key string, placements []ShelfPlacement) (DocumentsFilingResult, error) {
	var no DocumentsFilingResult
	if s == nil || s.store == nil || ctx.Err() != nil || len(revision) != 64 || batch == "latest" || !managedRootGeneration.MatchString(batch) || !clientKeyPattern.MatchString(key) || len(placements) == 0 {
		return no, ErrInvalidAction
	}
	if len(placements) > PlanMaxOperations {
		return no, ErrReviewBounds
	}
	p := shelfRequest{Batch: batch, Revision: revision, Placements: append([]ShelfPlacement(nil), placements...)}
	sort.Slice(p.Placements, func(i, j int) bool { return p.Placements[i].File < p.Placements[j].File })
	for i, row := range p.Placements {
		existing := len(row.Shelf) == 64 && row.NewName == ""
		newFolder := row.Shelf == "" && validNewShelfName(row.NewName)
		if len(row.Located) > 511 || (row.NewName != "" && row.Parent == "" && row.Located != "") || !managedRootGeneration.MatchString(row.File) || (!existing && !newFolder) || (row.Parent != "" && len(row.Parent) != 64) || (i > 0 && p.Placements[i-1].File == row.File) {
			return no, ErrInvalidAction
		}
	}
	digest, err := p.digest()
	if err != nil {
		return no, err
	}
	decision, err := setupDigest([]string{"station", id, owner, key})
	if err != nil {
		return no, err
	}
	return s.FileDocumentsSelection(ctx, owner, id, DocumentsFileRequest{Digest: digest, ClientKey: key, DecisionID: "station-" + decision, shelves: &p}, func(context.Context) (DocumentsSelection, error) {
		scope, err := s.shelfScope(owner, id)
		if err != nil {
			return DocumentsSelection{}, err
		}
		return DocumentsSelection{State: "matched", Batch: batch, RootDirID: scope.witness, Key: revision}, nil
	})
}

// makeReviewedShelf remembers a confirmed exclusive creation before handing
// its identity to the file mover. An uncertain creation is never adopted.
func (s *Service) makeReviewedShelf(ctx context.Context, settings JanitorSettings, root string, approval ApprovalRecord, witness string, parent DirRef, rel string) DirRef {
	if err := s.validateExecutionScope(settings, root, approval); err != nil {
		return DirRef{}
	}
	made, err := s.conditionalMoverFor().MakeFolder(ctx, root, witness, parent, path.Base(rel))
	if err != nil || made.Rel != rel || made.ID == "" {
		return DirRef{}
	}
	_, err = s.store.UpdateScanState(settings.WorkspaceID, func(state *ScanState) error {
		for i := range state.Filings {
			f := &state.Filings[i]
			if f.ID != approval.IdempotencyKey || !f.ShelfPicture {
				continue
			}
			for j := range f.NewShelves {
				d := &f.NewShelves[j]
				if d.Folder == rel && d.Create && d.ParentID == parent.ID && d.RootDirID == witness && d.FolderID == "" {
					d.FolderID = made.ID
					d.CreatedAt = s.clock()
					return nil
				}
			}
		}
		return ErrReviewUnavailable
	})
	if err != nil {
		return DirRef{}
	}
	return made
}

func validNewShelfName(name string) bool { return len(name) <= 255 && safeShelfName(name) }

// Probing a folder name is read-only. At File, MakeFolder performs the
// final exclusive creation and never adopts an entry that arrived since.
func (s *Service) checkNewShelf(scope shelfScope, parent DirRef, name string) error {
	if !validNewShelfName(name) {
		return ErrInvalidAction
	}
	rel, ok := destinationShape(DocumentsDestination{Folder: parent.Rel, New: name}, scope.filedName)
	if !ok {
		return ErrInvalidAction
	}
	m := s.conditionalMoverFor()
	current, err := m.Folder(scope.root.Path, scope.witness, parent.Rel)
	if err != nil || current.ID != parent.ID {
		return ErrReviewPlanChanged
	}
	if _, err = m.Folder(scope.root.Path, scope.witness, rel); !errors.Is(err, fs.ErrNotExist) {
		return ErrReviewConflict
	}
	return nil
}

func (s *Service) CheckShelfName(ctx context.Context, owner, id, batch, revision, parentKey, name, located string) error {
	view, err := s.ReadShelfPicture(ctx, owner, id, batch, "")
	if err != nil {
		return err
	}
	if !view.Supported || view.Revision != revision {
		return ErrReviewPlanChanged
	}
	scope, err := s.shelfScope(owner, id)
	if err != nil {
		return err
	}
	parent := DirRef{ID: scope.witness}
	if located != "" {
		sh, err := scope.exactFolder(located)
		if err != nil || sh.Parent != "" || sh.Key != parentKey {
			return ErrReviewPlanChanged
		}
		parent = sh.dir
	} else if parentKey != "" {
		parent = DirRef{}
		for _, folder := range view.Folders {
			if folder.Key == parentKey {
				parent = folder.dir
				break
			}
		}
		if parent.ID == "" {
			return ErrReviewPlanChanged
		}
	}
	return s.checkNewShelf(scope, parent, name)
}

// planShelves is called under the root and scan-state admission locks, from
// exactly the same press that spent the approval. A changed destination
// invalidates the plan. A source changed on disk since its scan is left to
// placeOne's per-file check, so other unchanged files can still be filed.
func (s *Service) planShelves(ctx context.Context, owner, id, batch string, p shelfRequest, state ScanState) (documentsPlan, error) {
	var no documentsPlan
	scope, err := s.shelfScope(owner, id)
	if err != nil {
		return no, err
	}
	view, err := s.shelfPicture(ctx, owner, id, batch, "", scope, state)
	if err != nil {
		return no, err
	}
	if !view.Supported {
		return no, ErrMoveUnsupported
	}
	if view.Revision != p.Revision {
		return no, ErrReviewPlanChanged
	}
	files := map[string]bool{}
	for _, file := range view.Files {
		files[file.ID] = true
	}
	folders := map[string]map[string]DirRef{}
	index := func(parent string, shelves []PictureShelf) {
		dirs := map[string]DirRef{}
		for _, sh := range shelves {
			dirs[sh.Key] = sh.dir
		}
		folders[parent] = dirs
	}
	index("", view.Folders)
	located := map[string]PictureShelf{}
	resolve := func(key, parentKey, hint string) (DirRef, error) {
		if hint != "" {
			sh, ok := located[hint]
			if !ok {
				var err error
				sh, err = scope.exactFolder(hint)
				if err != nil {
					return DirRef{}, err
				}
				located[hint] = sh
			}
			if sh.Key != key || sh.Parent != parentKey {
				return DirRef{}, ErrReviewPlanChanged
			}
			return sh.dir, nil
		}
		if _, ok := folders[parentKey]; !ok {
			parent, ok := folders[""][parentKey]
			if !ok {
				return DirRef{}, ErrReviewPlanChanged
			}
			children, _, err := scope.folders(parent)
			if err != nil {
				return DirRef{}, err
			}
			index(parentKey, children)
		}
		dir, ok := folders[parentKey][key]
		if !ok {
			return DirRef{}, ErrReviewPlanChanged
		}
		return dir, nil
	}
	plan := documentsPlan{rootDirID: scope.witness, scope: scope.scope}
	for _, row := range p.Placements {
		if ctx.Err() != nil {
			return no, ctx.Err()
		}
		var dir DirRef
		if row.NewName != "" {
			parent := DirRef{ID: scope.witness}
			if row.Parent != "" {
				parent, err = resolve(row.Parent, "", row.Located)
				if err != nil {
					return no, err
				}
			}
			rel := path.Join(parent.Rel, row.NewName)
			if plan.newShelves == nil {
				plan.newShelves = map[string]DirRef{}
			}
			if _, checked := plan.newShelves[rel]; !checked {
				if err := s.checkNewShelf(scope, parent, row.NewName); err != nil {
					return no, err
				}
				plan.newShelves[rel] = parent
			}
			dir = DirRef{Rel: rel}
		} else {
			dir, err = resolve(row.Shelf, row.Parent, row.Located)
			if err != nil {
				return no, err
			}
		}
		if !files[row.File] {
			return no, ErrReviewPlanChanged
		}
		c, ok := state.Candidate(row.File)
		if !ok {
			return no, ErrReviewPlanChanged
		}
		plan.items = append(plan.items, documentsPlanItem{candidate: c, finalName: c.Name, shelf: dir})
	}
	digest, err := p.digest()
	plan.preview = DocumentsPreview{State: documentsPreviewReady, Digest: digest}
	return plan, err
}
