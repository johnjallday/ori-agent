package folderagent

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/johnjallday/ori-agent/internal/filejanitor"
	"github.com/johnjallday/ori-agent/internal/folderdigest"
	"github.com/johnjallday/ori-agent/internal/secondbrain"
	"github.com/johnjallday/ori-agent/internal/sensitive"
)

type MatchDocumentsFiling func(context.Context, string, string, string, string, []filejanitor.ReviewedLooseFile) (filejanitor.DocumentsSelection, error)

// PreviewDocumentsFiling shows what filing a matched selection into a folder would
// do. It writes nothing.
type PreviewDocumentsFiling func(ctx context.Context, owner, id string, dest filejanitor.DocumentsDestination, sel filejanitor.DocumentsSelection) (filejanitor.DocumentsPreview, error)

// FileDocumentsFiling files a previewed selection. reselect re-derives the
// selection inside the call, under the filing lock.
type FileDocumentsFiling func(ctx context.Context, owner, id string, req filejanitor.DocumentsFileRequest, reselect func(context.Context) (filejanitor.DocumentsSelection, error)) (filejanitor.DocumentsFilingResult, error)

type FilingSelection struct {
	Review   string   `json:"review"`
	Group    string   `json:"group"`
	Revision int      `json:"revision"`
	Sources  []string `json:"sources"`
}

// FilingTarget is where the person wants the selection to go: an existing folder
// under Documents, or one new folder (New) made inside an existing one (Folder).
type FilingTarget struct {
	Folder string `json:"folder"`
	New    string `json:"new,omitempty"`
}

type FilingPreviewRequest struct {
	FilingSelection
	Destination FilingTarget `json:"destination"`
}

// FilingRequest is one press: the preview's digest the person approved and a retry key.
type FilingRequest struct {
	FilingPreviewRequest
	Digest    string `json:"digest"`
	ClientKey string `json:"client_key"`
}

// FilingResult is what a press did. DecisionRecorded is false when the move
// happened but the note in the saved view could not be written; pressing again
// with the same key writes it.
type FilingResult struct {
	filejanitor.DocumentsFilingResult
	DecisionRecorded bool `json:"decision_recorded"`
}

// UndoDocumentsFiling puts every file of a named filing back that can still be put
// back, and UndoDocumentsFile puts one back.
type UndoDocumentsFiling func(ctx context.Context, owner, id, filingID string) (filejanitor.FilingUndoResult, error)
type UndoDocumentsFile func(ctx context.Context, owner, id, filingID, actionID string) (filejanitor.FilingUndoResult, error)

// DocumentsFilingStanding says how many of a filing's files are still filed and how
// many were put back, from File Janitor's journal.
type DocumentsFilingStanding func(owner, id, filingID string) (filejanitor.DocumentsFilingStanding, error)

// FilingUndoRequest puts back a whole named filing, or one file of it when Action
// names the file's journal entry.
type FilingUndoRequest struct {
	Filing string `json:"filing"`
	Action string `json:"action,omitempty"`
}

// UndoOrganizationFiling puts a named filing's files back. File Janitor checks the
// filing belongs to this workspace and owner, and that the file belongs to the
// filing, before anything moves; the original name is never overwritten.
func (s *Service) UndoOrganizationFiling(ctx context.Context, owner, id string, req FilingUndoRequest) (filejanitor.FilingUndoResult, error) {
	if s.deps.UndoFiling == nil || s.deps.UndoFilingFile == nil {
		return filejanitor.FilingUndoResult{}, ErrUnavailable
	}
	if _, _, err := s.latestReview(owner, id); err != nil {
		return filejanitor.FilingUndoResult{}, err
	}
	if req.Filing == "" {
		return filejanitor.FilingUndoResult{}, ErrWrongState
	}
	if req.Action != "" {
		return s.deps.UndoFilingFile(ctx, owner, id, req.Filing, req.Action)
	}
	return s.deps.UndoFiling(ctx, owner, id, req.Filing)
}

// withFilingStanding fills in, for display only, where each filing decision stands
// now. It works on a copy and never stores anything: the decision is a record of
// what the person chose, and what happened to the files comes from the journal.
func (s *Service) withFilingStanding(owner, id string, v ReviewResult) ReviewResult {
	if s.deps.FilingStanding == nil || len(v.Decisions) == 0 {
		return v
	}
	v.Decisions = slices.Clone(v.Decisions)
	for i := range v.Decisions {
		d := &v.Decisions[i]
		if d.Kind != "file" || d.Filing == "" {
			continue
		}
		if standing, err := s.deps.FilingStanding(owner, id, d.Filing); err == nil {
			d.FilingState = &standing
		}
	}
	return v
}

// matchedFiling is a selection re-derived from the current review.
type matchedFiling struct {
	selection filejanitor.DocumentsSelection
	auth      string
	group     DocumentGroup
	root      string
}

// matchOrganizationFiling re-derives the selection from the saved view and asks
// File Janitor whether those exact files are still the reviewed, eligible ones.
// It never edits a Filing draft or grants move authority. forFiling additionally
// requires the view to be in a state that can take a new decision.
func (s *Service) matchOrganizationFiling(ctx context.Context, owner, id string, req FilingSelection, forFiling bool) (matchedFiling, error) {
	var empty matchedFiling
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
		if forFiling && (v.Status != "reviewed" || len(v.Decisions) >= 256) {
			return empty, ErrWrongState
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
	// The key binds a preview to this view revision, the kept group and the exact
	// files chosen (in a stable order), so a press after any of them changed is
	// refused with a fresh preview instead of filing what was not shown.
	refs := slices.Clone(req.Sources)
	slices.Sort(refs)
	projection.Key = documentsRef(append([]string{"filing-selection-v1", auth, req.Review, req.Group, strconv.Itoa(req.Revision)}, refs...)...)
	return matchedFiling{selection: projection, auth: auth, group: *group, root: access.Root}, nil
}

// CheckOrganizationFiling never edits a Filing draft or grants move authority.
func (s *Service) CheckOrganizationFiling(ctx context.Context, owner, id string, req FilingSelection) (filejanitor.DocumentsSelection, error) {
	m, err := s.matchOrganizationFiling(ctx, owner, id, req, false)
	return m.selection, err
}

// filingDestination checks a requested destination more strictly than File
// Janitor does: plain, visible, non-sensitive names that no protected folder
// overlaps. ok is false for anything else, and then nothing is previewed.
func (s *Service) filingDestination(root string, target FilingTarget) (filejanitor.DocumentsDestination, bool) {
	var parts []string
	if target.Folder != "" {
		parts = append(parts, strings.Split(target.Folder, "/")...)
	}
	if target.New != "" {
		parts = append(parts, target.New)
	}
	if len(parts) == 0 {
		return filejanitor.DocumentsDestination{}, false
	}
	for _, part := range parts {
		if !secondbrain.ValidFolderName(part) || folderdigest.IsSkippedFolder(part) || sensitive.ContainsSecretLikeText(part) {
			return filejanitor.DocumentsDestination{}, false
		}
	}
	if s.deps.DocumentsProtected != nil {
		candidate := filepath.Join(root, filepath.Join(parts...))
		for _, protected := range s.deps.DocumentsProtected() {
			if filejanitor.RootsOverlap(candidate, protected) {
				return filejanitor.DocumentsDestination{}, false
			}
		}
	}
	return filejanitor.DocumentsDestination{Folder: target.Folder, New: target.New}, true
}

// PreviewOrganizationFiling shows what filing the chosen files into a folder
// would do. It moves nothing and writes nothing.
func (s *Service) PreviewOrganizationFiling(ctx context.Context, owner, id string, req FilingPreviewRequest) (filejanitor.DocumentsPreview, error) {
	if s.deps.PreviewFiling == nil {
		return filejanitor.DocumentsPreview{}, ErrUnavailable
	}
	m, err := s.matchOrganizationFiling(ctx, owner, id, req.FilingSelection, true)
	if err != nil {
		return filejanitor.DocumentsPreview{}, err
	}
	dest, ok := s.filingDestination(m.root, req.Destination)
	if !ok {
		return filejanitor.DocumentsPreview{State: "destination"}, nil
	}
	preview, err := s.deps.PreviewFiling(ctx, owner, id, dest, m.selection)
	if err != nil {
		return filejanitor.DocumentsPreview{}, ErrWrongState
	}
	return preview, nil
}

// FileOrganizationSelection files the previewed selection, then writes the one
// organizing decision that belongs to the filing.
func (s *Service) FileOrganizationSelection(ctx context.Context, owner, id string, req FilingRequest) (FilingResult, error) {
	if s.deps.FileFiling == nil || s.deps.MatchFiling == nil {
		return FilingResult{}, ErrUnavailable
	}
	rec, _, err := s.latestReview(owner, id)
	if err != nil {
		return FilingResult{}, err
	}
	access, err := s.organizationAccess(owner, rec)
	if err != nil {
		return FilingResult{}, err
	}
	dest, ok := s.filingDestination(access.Root, req.Destination)
	if !ok || len(req.Digest) != 64 || req.ClientKey == "" {
		return FilingResult{}, ErrWrongState
	}
	decisionID := documentsRef("organization-filing-v1", owner, id, req.Review, req.Group, req.ClientKey)
	reselect := func(ctx context.Context) (filejanitor.DocumentsSelection, error) {
		m, err := s.matchOrganizationFiling(ctx, owner, id, req.FilingSelection, true)
		return m.selection, err
	}
	result, err := s.deps.FileFiling(ctx, owner, id, filejanitor.DocumentsFileRequest{
		Destination: dest, Digest: req.Digest, ClientKey: req.ClientKey, DecisionID: decisionID,
	}, reselect)
	out := FilingResult{DocumentsFilingResult: result}
	if err != nil || result.FilingID == "" {
		return out, err
	}
	out.DecisionRecorded = s.recordFilingDecision(owner, id, req, decisionID, result) == nil
	return out, nil
}

// recordFilingDecision writes the one organizing decision for a filing. It runs
// after the files moved, never before: a decision with no filing would be false
// evidence. It is idempotent on the decision ID, so pressing again with the same
// key backfills a decision that a crash or a full view left unwritten.
func (s *Service) recordFilingDecision(owner, id string, req FilingRequest, decisionID string, result filejanitor.DocumentsFilingResult) error {
	_, err := s.deps.Store.Update(id, func(r *Record) error {
		a, err := s.organizationAccess(owner, r)
		if err != nil {
			return err
		}
		root, _ := r.Map.Folder("")
		auth := documentsRef(owner, id, a.Root, root.BranchWitness, a.Lead, a.Binding)
		for i := range r.OrganizationRuns {
			run := &r.OrganizationRuns[i]
			if run.Authority != auth || run.Result.ID != req.Review {
				continue
			}
			v := &run.Result
			for _, d := range v.Decisions {
				if d.ID == decisionID {
					return nil
				}
			}
			idx := slices.IndexFunc(v.Groups, func(g DocumentGroup) bool { return g.ID == req.Group })
			if idx < 0 || len(v.Decisions) >= 256 {
				return ErrWrongState
			}
			g := v.Groups[idx]
			v.Decisions = append(v.Decisions, OrganizingDecision{
				ID: decisionID, Kind: "file", Time: s.deps.Now().UTC(), Group: g.ID,
				Sources: slices.Clone(req.Sources), Proposed: g.Title, Choice: result.Folder,
				Outcome: filingOutcomeText(result), Filing: result.FilingID,
			})
			v.Revision++
			return nil
		}
		return ErrWrongState
	})
	return err
}

// filingOutcomeText says what happened in the words the saved view shows.
func filingOutcomeText(r filejanitor.DocumentsFilingResult) string {
	switch {
	case r.Filed > 0 && r.Failed == 0 && r.Stale == 0 && r.NotAttempted == 0:
		return fmt.Sprintf("Filed %d into %s", r.Filed, r.Folder)
	case r.Filed > 0:
		return fmt.Sprintf("Filed %d into %s; %d not moved", r.Filed, r.Folder, r.Failed+r.Stale+r.NotAttempted)
	default:
		return "Nothing moved"
	}
}
