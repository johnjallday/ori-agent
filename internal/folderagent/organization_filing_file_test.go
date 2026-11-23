//go:build darwin || linux || freebsd

package folderagent

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/filejanitor"
)

type filingHarness struct {
	t    *testing.T
	h    *harness
	id   string
	root string
	req  FilingSelection
	// calls counts what reached each File Janitor dependency.
	matches, previews, files int
	preview                  filejanitor.DocumentsPreview
	lastSelection            filejanitor.DocumentsSelection
}

// newFilingHarness seeds a reviewed saved view with one kept group of two loose
// files and an example, and fakes File Janitor at the three seams folderagent uses.
func newFilingHarness(t *testing.T) *filingHarness {
	t.Helper()
	root := build(t, "one.pdf", "two.pdf")
	h := newHarness(t, root)
	h.svc.deps.KnownFolderRoot = knownOnly("documents", root)
	id := h.start(t, "").WorkspaceID
	h.svc.Wait()
	record, _ := h.svc.deps.Store.Read(id)
	h.svc.deps.DocumentsAccess = func(string, string) (DocumentsAccess, error) {
		return DocumentsAccess{root, record.AgentName, "binding"}, nil
	}
	_, auth, err := h.svc.latestReview("u1", id)
	if err != nil {
		t.Fatal(err)
	}
	_, err = h.svc.deps.Store.Update(id, func(r *Record) error {
		r.OrganizationRuns = []OrganizationRun{{Authority: auth, Result: ReviewResult{
			ID: "review", Status: "reviewed", Revision: 3,
			Files: []ReviewedDocument{
				{Source: ReviewSource{Path: "one.pdf", Ref: "r1"}, Status: "read"},
				{Source: ReviewSource{Path: "two.pdf", Ref: "r2"}, Status: "read"},
				{Source: ReviewSource{Path: "Examples/ref.pdf", Ref: "ex", Example: true}, Status: "read"},
			},
			Groups: []DocumentGroup{{ID: "group", Title: "Tax papers", Kept: true, Members: []string{"r1", "r2", "ex"}}},
		}}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	f := &filingHarness{t: t, h: h, id: id, root: root, req: FilingSelection{Review: "review", Revision: 3, Group: "group", Sources: []string{"r1", "r2"}}}
	f.preview = filejanitor.DocumentsPreview{State: "ready", Digest: fmt.Sprintf("%064d", 1), Folder: "Taxes"}
	h.svc.deps.MatchFiling = func(_ context.Context, _, _, _, _ string, selected []filejanitor.ReviewedLooseFile) (filejanitor.DocumentsSelection, error) {
		f.matches++
		names := make([]string, 0, len(selected))
		for _, s := range selected {
			names = append(names, s.Name)
		}
		return filejanitor.DocumentsSelection{State: "matched", Names: names, Batch: "batch", CandidateIDs: names, RootDirID: "1:1"}, nil
	}
	h.svc.deps.PreviewFiling = func(_ context.Context, _, _ string, dest filejanitor.DocumentsDestination, sel filejanitor.DocumentsSelection) (filejanitor.DocumentsPreview, error) {
		f.previews++
		f.lastSelection = sel
		return f.preview, nil
	}
	return f
}

func (f *filingHarness) view() ReviewResult {
	f.t.Helper()
	record, err := f.h.svc.deps.Store.Read(f.id)
	if err != nil {
		f.t.Fatal(err)
	}
	return record.OrganizationRuns[0].Result
}

func TestPreviewOrganizationFiling_RefusesDestinationsBeforeFileJanitorSeesThem(t *testing.T) {
	f := newFilingHarness(t)
	f.h.svc.deps.DocumentsProtected = func() []string { return []string{filepath.Join(f.root, "Protected")} }
	for name, target := range map[string]FilingTarget{
		"empty":            {},
		"a hidden folder":  {Folder: ".hidden"},
		"a skipped folder": {Folder: "node_modules"},
		"a parent segment": {Folder: "../Taxes"},
		"an empty segment": {Folder: "Taxes//2024"},
		"a colon":          {Folder: "Taxes", New: "a:b"},
		"a path as a name": {Folder: "Taxes", New: "a/b"},
		"a protected one":  {Folder: "Protected"},
		"inside protected": {Folder: "Protected", New: "Inner"},
	} {
		before := f.previews
		got, err := f.h.svc.PreviewOrganizationFiling(t.Context(), "u1", f.id, FilingPreviewRequest{FilingSelection: f.req, Destination: target})
		if err != nil || got.State != "destination" || got.Digest != "" {
			t.Fatalf("%s: want a refused destination, got %+v %v", name, got, err)
		}
		if f.previews != before {
			t.Fatalf("%s: File Janitor was asked about a destination folderagent refuses", name)
		}
	}
}

func TestPreviewOrganizationFiling_BindsTheSelectionAndRefusesWhatTheViewDoesNotHold(t *testing.T) {
	f := newFilingHarness(t)
	ask := func(req FilingSelection) (filejanitor.DocumentsPreview, error) {
		return f.h.svc.PreviewOrganizationFiling(t.Context(), "u1", f.id, FilingPreviewRequest{FilingSelection: req, Destination: FilingTarget{Folder: "Taxes"}})
	}
	if got, err := ask(f.req); err != nil || got.State != "ready" {
		t.Fatalf("preview: %+v %v", got, err)
	}
	key := f.lastSelection.Key
	if key == "" {
		t.Fatal("the selection must carry a binding key")
	}

	// The key changes with the files chosen and with the view revision, and not
	// with the order the files were ticked.
	one := f.req
	one.Sources = []string{"r1"}
	_, _ = ask(one)
	if f.lastSelection.Key == key {
		t.Fatal("a different selection must change the key")
	}
	reordered := f.req
	reordered.Sources = []string{"r2", "r1"}
	_, _ = ask(reordered)
	if f.lastSelection.Key != key {
		t.Fatal("the order files were ticked must not change the key")
	}

	for name, req := range map[string]FilingSelection{
		"an example":      {Review: "review", Revision: 3, Group: "group", Sources: []string{"ex"}},
		"an unknown ref":  {Review: "review", Revision: 3, Group: "group", Sources: []string{"nope"}},
		"a stale view":    {Review: "review", Revision: 2, Group: "group", Sources: []string{"r1"}},
		"a missing group": {Review: "review", Revision: 3, Group: "other", Sources: []string{"r1"}},
		"too many":        {Review: "review", Revision: 3, Group: "group", Sources: []string{"r1", "r2", "r1", "r2", "r1", "r2", "r1", "r2", "r1"}},
	} {
		if _, err := ask(req); !errors.Is(err, ErrWrongState) {
			t.Fatalf("%s: want wrong state, got %v", name, err)
		}
	}

	// A view that is not in a state to take a decision is refused for filing even
	// though the read-only check would accept it.
	if _, err := f.h.svc.deps.Store.Update(f.id, func(r *Record) error { r.OrganizationRuns[0].Result.Status = "reading"; return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := ask(f.req); !errors.Is(err, ErrWrongState) {
		t.Fatalf("a view that is still reading must not preview a filing, got %v", err)
	}
	if got, err := f.h.svc.CheckOrganizationFiling(t.Context(), "u1", f.id, f.req); err != nil || got.State != "matched" {
		t.Fatalf("the read-only check keeps working as before: %+v %v", got, err)
	}
}

func TestFileOrganizationSelection_RecordsExactlyOneDecisionPerFiling(t *testing.T) {
	f := newFilingHarness(t)
	var reselected int
	admitted := map[string]bool{}
	f.h.svc.deps.FileFiling = func(ctx context.Context, owner, id string, req filejanitor.DocumentsFileRequest, reselect func(context.Context) (filejanitor.DocumentsSelection, error)) (filejanitor.DocumentsFilingResult, error) {
		f.files++
		if admitted[req.ClientKey] {
			// Like File Janitor: a key it already admitted is answered from its
			// journal, without re-deriving the selection.
			return filejanitor.DocumentsFilingResult{FilingID: "filing-1", DecisionID: req.DecisionID, Folder: "Taxes", Filed: 2, Replayed: true}, nil
		}
		admitted[req.ClientKey] = true
		sel, err := reselect(ctx)
		if err != nil {
			return filejanitor.DocumentsFilingResult{}, err
		}
		reselected++
		if sel.State != "matched" || sel.Key == "" || req.Destination.Folder != "Taxes" || req.DecisionID == "" {
			t.Fatalf("what File Janitor was given: %+v %+v", sel, req)
		}
		return filejanitor.DocumentsFilingResult{FilingID: "filing-1", DecisionID: req.DecisionID, Folder: "Taxes", Filed: 2}, nil
	}
	req := FilingRequest{
		FilingPreviewRequest: FilingPreviewRequest{FilingSelection: f.req, Destination: FilingTarget{Folder: "Taxes"}},
		Digest:               fmt.Sprintf("%064d", 1), ClientKey: "key-aaaaaaaaaaaaaaaa",
	}

	result, err := f.h.svc.FileOrganizationSelection(t.Context(), "u1", f.id, req)
	if err != nil || result.Filed != 2 || !result.DecisionRecorded {
		t.Fatalf("filing: %+v %v", result, err)
	}
	view := f.view()
	if len(view.Decisions) != 1 || view.Revision != 4 {
		t.Fatalf("one decision and a new revision, got %+v", view)
	}
	d := view.Decisions[0]
	if d.Kind != "file" || d.Filing != "filing-1" || d.ID != result.DecisionID || d.Choice != "Taxes" || d.Group != "group" || d.Proposed != "Tax papers" || len(d.Sources) != 2 || d.Outcome != "Filed 2 into Taxes" {
		t.Fatalf("decision: %+v", d)
	}

	// A retry with the same key (a lost response) must not write a second decision,
	// even though the view's revision has moved on since the first press.
	again, err := f.h.svc.FileOrganizationSelection(t.Context(), "u1", f.id, req)
	if err != nil || !again.DecisionRecorded || len(f.view().Decisions) != 1 || f.view().Revision != 4 {
		t.Fatalf("a replay must be idempotent: %+v %v %+v", again, err, f.view())
	}
}

func TestFileOrganizationSelection_BackfillsALostDecisionAndSaysWhenItCannot(t *testing.T) {
	f := newFilingHarness(t)
	f.h.svc.deps.FileFiling = func(ctx context.Context, _, _ string, req filejanitor.DocumentsFileRequest, _ func(context.Context) (filejanitor.DocumentsSelection, error)) (filejanitor.DocumentsFilingResult, error) {
		return filejanitor.DocumentsFilingResult{FilingID: "filing-1", DecisionID: req.DecisionID, Folder: "Taxes", Filed: 1, Replayed: true}, nil
	}
	req := FilingRequest{
		FilingPreviewRequest: FilingPreviewRequest{FilingSelection: f.req, Destination: FilingTarget{Folder: "Taxes"}},
		Digest:               fmt.Sprintf("%064d", 1), ClientKey: "key-aaaaaaaaaaaaaaaa",
	}

	// The process died after the move and before the decision: the replay writes it.
	if got := len(f.view().Decisions); got != 0 {
		t.Fatalf("setup: %d decisions", got)
	}
	result, err := f.h.svc.FileOrganizationSelection(t.Context(), "u1", f.id, req)
	if err != nil || !result.DecisionRecorded || len(f.view().Decisions) != 1 {
		t.Fatalf("backfill: %+v %v", result, err)
	}

	// A full view cannot take another decision: the filing is reported, and the
	// missing note is said out loud and not hidden.
	second := req
	second.ClientKey = "key-bbbbbbbbbbbbbbbb"
	if _, err := f.h.svc.deps.Store.Update(f.id, func(r *Record) error {
		v := &r.OrganizationRuns[0].Result
		for len(v.Decisions) < 256 {
			v.Decisions = append(v.Decisions, OrganizingDecision{ID: fmt.Sprintf("filler-%d", len(v.Decisions)), Kind: "keep"})
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	full, err := f.h.svc.FileOrganizationSelection(t.Context(), "u1", f.id, second)
	if err != nil || full.FilingID == "" || full.DecisionRecorded {
		t.Fatalf("a full view must report the filing but not claim a decision: %+v %v", full, err)
	}
}

func TestFileOrganizationSelection_NothingIsRecordedWhenFileJanitorRefuses(t *testing.T) {
	f := newFilingHarness(t)
	changed := filejanitor.ErrReviewPlanChanged
	f.h.svc.deps.FileFiling = func(context.Context, string, string, filejanitor.DocumentsFileRequest, func(context.Context) (filejanitor.DocumentsSelection, error)) (filejanitor.DocumentsFilingResult, error) {
		return filejanitor.DocumentsFilingResult{Preview: &filejanitor.DocumentsPreview{State: "changed"}}, changed
	}
	req := FilingRequest{
		FilingPreviewRequest: FilingPreviewRequest{FilingSelection: f.req, Destination: FilingTarget{Folder: "Taxes"}},
		Digest:               fmt.Sprintf("%064d", 1), ClientKey: "key-aaaaaaaaaaaaaaaa",
	}
	result, err := f.h.svc.FileOrganizationSelection(t.Context(), "u1", f.id, req)
	if !errors.Is(err, filejanitor.ErrReviewPlanChanged) || result.Preview == nil || result.DecisionRecorded {
		t.Fatalf("want the changed plan passed through: %+v %v", result, err)
	}
	if len(f.view().Decisions) != 0 || f.view().Revision != 3 {
		t.Fatal("a refused press must leave the saved view alone")
	}

	// The view moving on between the preview and the press is refused inside the
	// filing, through reselect, with nothing recorded.
	f.h.svc.deps.FileFiling = func(ctx context.Context, _, _ string, _ filejanitor.DocumentsFileRequest, reselect func(context.Context) (filejanitor.DocumentsSelection, error)) (filejanitor.DocumentsFilingResult, error) {
		_, err := reselect(ctx)
		return filejanitor.DocumentsFilingResult{}, err
	}
	if _, err := f.h.svc.deps.Store.Update(f.id, func(r *Record) error { r.OrganizationRuns[0].Result.Revision++; return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := f.h.svc.FileOrganizationSelection(t.Context(), "u1", f.id, req); !errors.Is(err, ErrWrongState) {
		t.Fatalf("a view that changed since the preview must be refused, got %v", err)
	}
	if len(f.view().Decisions) != 0 {
		t.Fatal("a refused press recorded a decision")
	}
}

func TestUndoOrganizationFiling_AsksForTheWholeFilingOrOneFileAndRefusesTheRest(t *testing.T) {
	f := newFilingHarness(t)
	var bulk, single []string
	f.h.svc.deps.UndoFiling = func(_ context.Context, owner, id, filingID string) (filejanitor.FilingUndoResult, error) {
		bulk = append(bulk, owner+"|"+id+"|"+filingID)
		return filejanitor.FilingUndoResult{FilingID: filingID, Back: 2}, nil
	}
	f.h.svc.deps.UndoFilingFile = func(_ context.Context, owner, id, filingID, actionID string) (filejanitor.FilingUndoResult, error) {
		single = append(single, owner+"|"+id+"|"+filingID+"|"+actionID)
		return filejanitor.FilingUndoResult{FilingID: filingID, Back: 1}, nil
	}

	all, err := f.h.svc.UndoOrganizationFiling(t.Context(), "u1", f.id, FilingUndoRequest{Filing: "filing-1"})
	if err != nil || all.Back != 2 || len(bulk) != 1 || bulk[0] != "u1|"+f.id+"|filing-1" || len(single) != 0 {
		t.Fatalf("all: %+v %v %v %v", all, err, bulk, single)
	}
	one, err := f.h.svc.UndoOrganizationFiling(t.Context(), "u1", f.id, FilingUndoRequest{Filing: "filing-1", Action: "action-9"})
	if err != nil || one.Back != 1 || len(single) != 1 || single[0] != "u1|"+f.id+"|filing-1|action-9" || len(bulk) != 1 {
		t.Fatalf("one: %+v %v %v %v", one, err, bulk, single)
	}
	if _, err := f.h.svc.UndoOrganizationFiling(t.Context(), "u1", f.id, FilingUndoRequest{}); !errors.Is(err, ErrWrongState) {
		t.Fatalf("a request that names no filing must be refused, got %v", err)
	}
	if _, err := f.h.svc.UndoOrganizationFiling(t.Context(), "u1", "no-such-workspace", FilingUndoRequest{Filing: "filing-1"}); err == nil {
		t.Fatal("a workspace that is not this owner's was asked to undo")
	}
	if len(bulk) != 1 || len(single) != 1 {
		t.Fatal("a refused request reached File Janitor")
	}
	f.h.svc.deps.UndoFiling = nil
	if _, err := f.h.svc.UndoOrganizationFiling(t.Context(), "u1", f.id, FilingUndoRequest{Filing: "filing-1"}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("want unavailable, got %v", err)
	}
}

func TestOrganizationResults_ShowWhereAFilingStandsWithoutStoringIt(t *testing.T) {
	f := newFilingHarness(t)
	if _, err := f.h.svc.deps.Store.Update(f.id, func(r *Record) error {
		v := &r.OrganizationRuns[0].Result
		v.Decisions = []OrganizingDecision{
			{ID: "d1", Kind: "file", Filing: "filing-1", Choice: "Taxes", Outcome: "Filed 2 into Taxes"},
			{ID: "d2", Kind: "keep", Choice: "Tax papers"},
			{ID: "d3", Kind: "file", Filing: "filing-gone", Choice: "Taxes"},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	f.h.svc.deps.FilingStanding = func(owner, id, filingID string) (filejanitor.DocumentsFilingStanding, error) {
		if filingID == "filing-1" {
			return filejanitor.DocumentsFilingStanding{Filed: 1, Back: 1, UndoAvailable: true}, nil
		}
		return filejanitor.DocumentsFilingStanding{}, errors.New("no such filing")
	}

	reviews, err := f.h.svc.OrganizationResults("u1", f.id)
	if err != nil || len(reviews) != 1 {
		t.Fatalf("results: %+v %v", reviews, err)
	}
	d := reviews[0].Decisions
	if d[0].FilingState == nil || d[0].FilingState.Filed != 1 || d[0].FilingState.Back != 1 || !d[0].FilingState.UndoAvailable {
		t.Fatalf("a filing decision shows where it stands now: %+v", d[0])
	}
	if d[1].FilingState != nil || d[2].FilingState != nil {
		t.Fatalf("other decisions, and a filing the journal no longer knows, show nothing: %+v", d)
	}
	if d[0].Outcome != "Filed 2 into Taxes" {
		t.Fatal("the decision's own words must not be rewritten")
	}
	for _, stored := range f.view().Decisions {
		if stored.FilingState != nil {
			t.Fatal("standing is computed for display and must never be stored")
		}
	}
}

func TestFileOrganizationSelection_RefusesBadRequestsBeforeFileJanitor(t *testing.T) {
	f := newFilingHarness(t)
	f.h.svc.deps.FileFiling = func(context.Context, string, string, filejanitor.DocumentsFileRequest, func(context.Context) (filejanitor.DocumentsSelection, error)) (filejanitor.DocumentsFilingResult, error) {
		f.files++
		return filejanitor.DocumentsFilingResult{FilingID: "filing-1"}, nil
	}
	good := FilingRequest{
		FilingPreviewRequest: FilingPreviewRequest{FilingSelection: f.req, Destination: FilingTarget{Folder: "Taxes"}},
		Digest:               fmt.Sprintf("%064d", 1), ClientKey: "key-aaaaaaaaaaaaaaaa",
	}
	for name, mutate := range map[string]func(*FilingRequest){
		"a short digest":   func(r *FilingRequest) { r.Digest = "abc" },
		"no key":           func(r *FilingRequest) { r.ClientKey = "" },
		"a hidden folder":  func(r *FilingRequest) { r.Destination = FilingTarget{Folder: ".hidden"} },
		"no destination":   func(r *FilingRequest) { r.Destination = FilingTarget{} },
		"a protected path": func(r *FilingRequest) { r.Destination = FilingTarget{Folder: "Protected"} },
	} {
		f.h.svc.deps.DocumentsProtected = func() []string { return []string{filepath.Join(f.root, "Protected")} }
		req := good
		mutate(&req)
		if _, err := f.h.svc.FileOrganizationSelection(t.Context(), "u1", f.id, req); !errors.Is(err, ErrWrongState) {
			t.Fatalf("%s: want wrong state, got %v", name, err)
		}
	}
	if f.files != 0 {
		t.Fatal("a bad request reached File Janitor")
	}
}
