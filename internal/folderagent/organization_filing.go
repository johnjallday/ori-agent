package folderagent

import (
	"context"
	"path"
	"path/filepath"
	"slices"

	"github.com/johnjallday/ori-agent/internal/filejanitor"
)

type MatchDocumentsFiling func(context.Context, string, string, string, string, []filejanitor.ReviewedLooseFile) (filejanitor.DocumentsSelection, error)

type FilingSelection struct {
	Review   string   `json:"review"`
	Group    string   `json:"group"`
	Revision int      `json:"revision"`
	Sources  []string `json:"sources"`
}

// CheckOrganizationFiling never edits a Filing draft or grants move authority.
func (s *Service) CheckOrganizationFiling(ctx context.Context, owner, id string, req FilingSelection) (filejanitor.DocumentsSelection, error) {
	var empty filejanitor.DocumentsSelection
	if len(req.Sources) == 0 || len(req.Sources) > reviewBatchSize || s.deps.MatchFiling == nil {
		return empty, ErrWrongState
	}
	rec, auth, err := s.latestReview(owner, id)
	if err != nil {
		return empty, err
	}
	var group *DocumentGroup
	files := map[string]ReviewSource{}
	for _, run := range rec.OrganizationRuns {
		if run.Authority != auth {
			continue
		}
		v := run.Result
		for _, item := range v.Files {
			if !item.Source.Example && item.Status == "read" {
				files[item.Source.Ref] = item.Source
			}
		}
		if v.ID != req.Review || v.Revision != req.Revision {
			continue
		}
		for _, g := range v.Groups {
			if g.ID == req.Group && g.Kept {
				group = &g
				break
			}
		}
	}
	if group == nil {
		return empty, ErrWrongState
	}
	var selected []filejanitor.ReviewedLooseFile
	seen := map[string]bool{}
	for _, ref := range req.Sources {
		f, ok := files[ref]
		if !ok || seen[ref] || !slices.Contains(group.Members, ref) || path.Base(f.Path) != f.Path {
			return empty, ErrWrongState
		}
		seen[ref] = true
		selected = append(selected, filejanitor.ReviewedLooseFile{Name: f.Path, Version: f.Version})
	}
	root, _ := rec.Map.Folder("")
	access, err := s.organizationAccess(owner, rec)
	if err != nil {
		return empty, err
	}
	if s.deps.DocumentsProtected != nil {
		for _, protected := range s.deps.DocumentsProtected() {
			for _, f := range selected {
				if filejanitor.RootsOverlap(filepath.Join(access.Root, f.Name), protected) {
					return empty, ErrWrongState
				}
			}
		}
	}
	projection, err := s.deps.MatchFiling(ctx, owner, id, access.Root, root.BranchWitness, selected)
	if err != nil {
		return empty, ErrWrongState
	}
	again, actual, err := s.latestReview(owner, id)
	if err != nil || again.Revision != rec.Revision || actual != auth || ctx.Err() != nil {
		return empty, ErrWrongState
	}
	return projection, nil
}
