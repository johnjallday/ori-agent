package filejanitor

import (
	"context"
	"sort"
)

// ShelfPlacement identifies one ticked file and the exact existing shelf it
// was placed on. Neither a path nor an identity supplied by the client can
// become a mover operand. Parent names the top-level shelf key for a nested one.
type ShelfPlacement struct {
	File   string `json:"file"`
	Shelf  string `json:"shelf"`
	Parent string `json:"parent,omitempty"`
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
		if !managedRootGeneration.MatchString(row.File) || len(row.Shelf) != 64 || (row.Parent != "" && len(row.Parent) != 64) || (i > 0 && p.Placements[i-1].File == row.File) {
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
	plan := documentsPlan{rootDirID: scope.witness, scope: scope.scope}
	for _, row := range p.Placements {
		if ctx.Err() != nil {
			return no, ctx.Err()
		}
		if _, ok := folders[row.Parent]; !ok {
			parent, ok := folders[""][row.Parent]
			if !ok {
				return no, ErrReviewPlanChanged
			}
			children, _, err := scope.folders(parent)
			if err != nil {
				return no, err
			}
			index(row.Parent, children)
		}
		dir, ok := folders[row.Parent][row.Shelf]
		if !ok || !files[row.File] {
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
