package filejanitor

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"
)

// These tests state the read-side rules of the "Picture review" contract in
// docs/architecture/file-janitor-group-review.md. They are written first and
// fail until tasks 1.2 (ticked on open) and 3.1 (a question group to one folder)
// land; they are then the regression guard for those rules.

// pictureFixtureNames covers every group kind the picture draws: one confident
// folder (images), a sensitive-looking name, music, a possible-copy pair and an
// unknown type.
var pictureFixtureNames = []string{"one.png", "passport.pdf", "song.wav", "Budget.pdf", "Budget (2).pdf", "mystery.qqq"}

func scanStateBytes(t *testing.T, s *Service) []byte {
	t.Helper()
	path, err := s.store.scanStatePath("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path) // #nosec G304 -- test-owned state file under a temp dir
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// stripReviewDefaults rewrites the saved scan file the way a scan made by the
// card-grid version looks: no batch carries the on-open marker.
func stripReviewDefaults(t *testing.T, s *Service) {
	t.Helper()
	var raw map[string]any
	if err := json.Unmarshal(scanStateBytes(t, s), &raw); err != nil {
		t.Fatal(err)
	}
	batches, _ := raw["batches"].([]any)
	for _, item := range batches {
		if batch, ok := item.(map[string]any); ok {
			delete(batch, "review_defaults")
		}
	}
	out, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	persistRawDraftState(t, s.store, out)
}

func choiceIDs(view ReviewDraftView) map[string]ReviewChoiceView {
	out := make(map[string]ReviewChoiceView, len(view.Choices))
	for _, choice := range view.Choices {
		out[choice.CandidateID] = choice
	}
	return out
}

// R10, R11, R12: an untouched scan opens with the confident folders ticked and
// no question group ticked, and reading it changes nothing on disk.
func TestTickedOnOpen_UntouchedScanTicksFilingOnly(t *testing.T) {
	s, batch, candidates := draftServiceFixture(t, pictureFixtureNames...)
	before := scanStateBytes(t, s)

	view, err := s.ReadReviewDraft(t.Context(), "local", "ws-1", batch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if view.State != "default" {
		t.Fatalf("an untouched scan must open in the default (ticked) state, got %q", view.State)
	}
	one := candidateNamed(t, candidates, "one.png")
	if len(view.Choices) != 1 || view.Choices[0].CandidateID != one.ID || view.Choices[0].Operation != OperationMove {
		t.Fatalf("only the confident folder's file is ticked: %+v", view.Choices)
	}
	if view.Choices[0].SourceGroupID != reviewGroupID("filing-images") {
		t.Fatalf("a default tick is group-provenanced, like select_filing_groups: %+v", view.Choices[0])
	}
	groups, err := s.ReadReviewGroups(t.Context(), "local", "ws-1", batch.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, choice := range view.Choices {
		if group, _ := groupFor(t, groups, choice.CandidateID); group.Kind != "filing" {
			t.Fatalf("a %s group member was ticked on open", group.Kind)
		}
	}
	// Reads never write: the saved scan file is byte-identical, no approval was
	// issued, nothing was journaled and no destination folder exists.
	if after := scanStateBytes(t, s); string(after) != string(before) {
		t.Fatal("opening the review changed the saved scan state")
	}
	state, _ := s.store.LoadScanState("ws-1")
	if len(state.Approvals) != 0 || len(state.Actions) != 0 || len(state.ReviewDrafts) != 0 {
		t.Fatal("opening the review issued authority, acted, or stored a draft")
	}
	// Setup creates the Filed folder itself; opening must not add a category
	// folder under it.
	settings, _ := s.store.LoadSettings("ws-1")
	images, err := DestinationDir(settings, CategoryImages)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(images); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("opening the review created a destination folder: %v", err)
	}
}

// R14, R15: the first edit starts from what the person saw. Unticking one folder
// keeps the other ticked, and a category edit does not add a selection.
func TestTickedOnOpen_FirstEditKeepsTheOtherTicks(t *testing.T) {
	s, batch, candidates := draftServiceFixture(t, "one.png", "report.pdf")
	opened, err := s.ReadReviewDraft(t.Context(), "local", "ws-1", batch.ID)
	if err != nil || opened.State != "default" || len(opened.Choices) != 2 {
		t.Fatalf("setup: both folders must open ticked: %q %+v %v", opened.State, opened.Choices, err)
	}
	after := editDraft(t, s, batch.ID, "deselect_group", func(r *ReviewDraftEdit) { r.GroupID = reviewGroupID("filing-images") })
	kept := candidateNamed(t, candidates, "report.pdf")
	if after.State != "saved" || len(after.Choices) != 1 || after.Choices[0].CandidateID != kept.ID {
		t.Fatalf("unticking one folder must keep the other ticked: %q %+v", after.State, after.Choices)
	}
	again := editDraft(t, s, batch.ID, "set_category", func(r *ReviewDraftEdit) {
		r.CandidateIDs, r.Category = []string{kept.ID}, "data"
	})
	if len(again.Choices) != 1 || again.Choices[0].CandidateID != kept.ID || again.Choices[0].Category != CategoryData {
		t.Fatalf("a category edit must change the destination without adding a selection: %+v", again.Choices)
	}
}

// R16: a file that arrives after the scan never joins a tick. Later work only
// ever appears in a new scan, which is a new picture.
func TestTickedOnOpen_LateArrivalNeverJoins(t *testing.T) {
	s, batch, _ := draftServiceFixture(t, "one.png", "two.png")
	opened, err := s.ReadReviewDraft(t.Context(), "local", "ws-1", batch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if opened.State != "default" || len(opened.Choices) != 2 {
		t.Fatalf("setup: expected two default ticks, got %q %+v", opened.State, opened.Choices)
	}
	settings, _ := s.store.LoadSettings("ws-1")
	agedFile(t, settings.RootPath, "late.png", 10)
	if _, _, err := s.ScanNow("ws-1", ScanSourceManual); err != nil {
		t.Fatal(err)
	}

	again, err := s.ReadReviewDraft(t.Context(), "local", "ws-1", batch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Choices) != 2 || again.Revision != opened.Revision {
		t.Fatalf("a later arrival changed the open picture: %+v", again)
	}
	groupsAfter := editDraft(t, s, batch.ID, "select_filing_groups", nil)
	if len(groupsAfter.Choices) != 2 {
		t.Fatalf("select_filing_groups widened to a file that arrived later: %+v", groupsAfter.Choices)
	}
}

// R13, R15: once the person has changed anything, Ori never ticks again, even
// after every folder was unticked and after a restart.
func TestTickedOnOpen_UntickAllStaysUnticked(t *testing.T) {
	s, batch, _ := draftServiceFixture(t, "one.png", "report.pdf")
	opened, err := s.ReadReviewDraft(t.Context(), "local", "ws-1", batch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if opened.State != "default" || len(opened.Choices) != 2 {
		t.Fatalf("setup: expected both folders ticked on open, got %q %+v", opened.State, opened.Choices)
	}
	editDraft(t, s, batch.ID, "deselect_group", func(r *ReviewDraftEdit) { r.GroupID = reviewGroupID("filing-images") })
	cleared := editDraft(t, s, batch.ID, "deselect_group", func(r *ReviewDraftEdit) { r.GroupID = reviewGroupID("filing-documents") })
	if cleared.State != "saved" || len(cleared.Choices) != 0 {
		t.Fatalf("unticking every folder must store an empty choice, not re-tick: %q %+v", cleared.State, cleared.Choices)
	}
	restarted := NewService(NewStore(s.store.resolver), s.workspaces)
	reread, err := restarted.ReadReviewDraft(t.Context(), "local", "ws-1", batch.ID)
	if err != nil || reread.State != "saved" || len(reread.Choices) != 0 {
		t.Fatalf("a restart re-ticked an unticked scan: %q %+v %v", reread.State, reread.Choices, err)
	}
}

// The defaults end, for good, the moment anything else has touched the scan: a
// legacy decision on one file, or a file that has already been filed.
func TestTickedOnOpen_EndsWhenAnythingElseTouchesTheScan(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*JanitorCandidate)
	}{
		{"a legacy decision", func(c *JanitorCandidate) {
			c.Decision, c.DecisionCategory, c.DecidedAt = DecisionMove, CategoryData, time.Now()
		}},
		{"a file already applied", func(c *JanitorCandidate) { c.State = CandidateApplied }},
		{"a file kept here", func(c *JanitorCandidate) { c.State, c.Decision = CandidateSkipped, DecisionSkip }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, batch, _ := draftServiceFixture(t, "one.png", "two.png")
			if _, err := s.store.UpdateScanState("ws-1", func(state *ScanState) error {
				for i := range state.Candidates {
					if state.Candidates[i].BatchID == batch.ID {
						tc.change(&state.Candidates[i])
						return nil
					}
				}
				return errors.New("no candidate to change")
			}); err != nil {
				t.Fatal(err)
			}
			view, err := s.ReadReviewDraft(t.Context(), "local", "ws-1", batch.ID)
			if err != nil || view.State != "empty" || len(view.Choices) != 0 {
				t.Fatalf("a touched scan must not tick anything: %q %+v %v", view.State, view.Choices, err)
			}
		})
	}
}

// Until the one-press button replaces it (group 2), today's preview and apply
// still file what the picture shows. A ticked-on-open scan goes through them
// without the person touching anything, files exactly the ticked files, and the
// ticks never come back for what is left.
func TestTickedOnOpen_DefaultViewFilesThroughTodaysPreviewAndApply(t *testing.T) {
	s, batch, _ := draftServiceFixture(t, "one.png", "two.png", "passport.pdf")
	s.SetMover(&realMover{})
	view, err := s.ReadReviewDraft(t.Context(), "local", "ws-1", batch.ID)
	if err != nil || view.State != "default" || len(view.Choices) != 2 {
		t.Fatalf("setup: expected the two images ticked: %q %+v %v", view.State, view.Choices, err)
	}
	items := make([]PreviewRequestItem, 0, len(view.Choices))
	for _, choice := range view.Choices {
		items = append(items, PreviewRequestItem{CandidateID: choice.CandidateID, Operation: choice.Operation, Category: string(choice.Category)})
	}
	preview, err := s.PreviewMoves(PreviewRequest{WorkspaceID: "ws-1", UserID: "local", Items: items, ReviewRevision: view.Revision})
	if err != nil {
		t.Fatalf("the default view must preview without a stored draft: %v", err)
	}
	result, err := s.ConfirmMoves(t.Context(), ConfirmRequest{WorkspaceID: "ws-1", UserID: "local", BatchID: batch.ID, Token: preview.Token, Items: items})
	if err != nil || result.Applied != 2 {
		t.Fatalf("the ticked files must move: %+v %v", result, err)
	}
	settings, _ := s.store.LoadSettings("ws-1")
	for _, name := range []string{"one.png", "two.png"} {
		if _, err := os.Stat(settings.RootPath + "/" + name); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s should have moved: %v", name, err)
		}
	}
	if _, err := os.Stat(settings.RootPath + "/passport.pdf"); err != nil {
		t.Fatalf("a question file must not move: %v", err)
	}
	after, err := s.ReadReviewDraft(t.Context(), "local", "ws-1", batch.ID)
	if err != nil || len(after.Choices) != 0 || after.State == "default" {
		t.Fatalf("ticks must not come back for what is left: %q %+v %v", after.State, after.Choices, err)
	}
}

// A default view can be bigger than one press may file. Reading it is fine; the
// caps do not move, so an edit that would leave more than 1,000 choices is
// refused and changes nothing, while one that gets under the cap is saved.
func TestTickedOnOpen_OverTheLimitNeedsAnUntickToSave(t *testing.T) {
	names := make([]string, MaxReviewChoicesPerBatch+1)
	for i := range names {
		names[i] = fmt.Sprintf("image-%04d.png", i)
	}
	s, batch, candidates := draftServiceFixture(t, names...)
	opened, err := s.ReadReviewDraft(t.Context(), "local", "ws-1", batch.ID)
	if err != nil || opened.State != "default" || len(opened.Choices) != MaxReviewChoicesPerBatch+1 {
		t.Fatalf("an over-limit default view must still be readable: %q %d %v", opened.State, len(opened.Choices), err)
	}
	before := scanStateBytes(t, s)
	_, err = s.EditReviewDraft("local", "ws-1", batch.ID, ReviewDraftEdit{
		ExpectedRevision: opened.Revision, ExpectedGroupRevision: opened.GroupRevision,
		Action: "select_files", CandidateIDs: []string{candidates[0].ID},
	})
	if !errors.Is(err, ErrReviewBounds) {
		t.Fatalf("an edit leaving more than 1,000 choices must be refused, got %v", err)
	}
	if !bytes.Equal(before, scanStateBytes(t, s)) {
		t.Fatal("a refused edit changed the saved scan state")
	}
	saved := editDraft(t, s, batch.ID, "exclude_files", func(r *ReviewDraftEdit) { r.CandidateIDs = []string{candidates[0].ID} })
	if saved.State != "saved" || len(saved.Choices) != MaxReviewChoicesPerBatch {
		t.Fatalf("unticking one file brings it under the cap: %q %d", saved.State, len(saved.Choices))
	}
}

// R71: a scan made by the card-grid version (no marker) loads exactly as it was
// saved. With no saved choices nothing is ticked, and a first edit starts from
// nothing rather than from invented ticks.
func TestTickedOnOpen_CardGridScansLoadUnchanged(t *testing.T) {
	s, batch, candidates := draftServiceFixture(t, "one.png", "two.png")
	stripReviewDefaults(t, s)

	view, err := s.ReadReviewDraft(t.Context(), "local", "ws-1", batch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if view.State != "empty" || len(view.Choices) != 0 {
		t.Fatalf("a scan without the on-open marker must open with nothing ticked: %q %+v", view.State, view.Choices)
	}
	picked := candidateNamed(t, candidates, "one.png")
	saved := editDraft(t, s, batch.ID, "select_files", func(r *ReviewDraftEdit) { r.CandidateIDs = []string{picked.ID} })
	if saved.State != "saved" || len(saved.Choices) != 1 || saved.Choices[0].CandidateID != picked.ID {
		t.Fatalf("an edit on a card-grid scan must add only what was chosen: %q %+v", saved.State, saved.Choices)
	}
}

// Decision (b): one menu choice sends a whole question group to one folder, keeps
// a Trash mark, requires a destination, and never leaks into select_filing_groups.
func TestReviewDraftEdits_QuestionGroupToOneFolderKeepsTrash(t *testing.T) {
	s, batch, candidates := draftServiceFixture(t, "passport-a.pdf", "passport-b.pdf", "tax-return.pdf", "one.png")
	sensitive := reviewGroupID("sensitive")

	filingOnly := editDraft(t, s, batch.ID, "select_filing_groups", nil)
	for _, choice := range filingOnly.Choices {
		if choice.GroupID == sensitive {
			t.Fatal("select_filing_groups put a question-group file in a folder")
		}
	}

	trashed := candidateNamed(t, candidates, "tax-return.pdf")
	editDraft(t, s, batch.ID, "mark_trash", func(r *ReviewDraftEdit) { r.CandidateIDs = []string{trashed.ID} })

	before, err := s.ReadReviewDraft(t.Context(), "local", "ws-1", batch.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.EditReviewDraft("local", "ws-1", batch.ID, ReviewDraftEdit{
		ExpectedRevision: before.Revision, ExpectedGroupRevision: before.GroupRevision,
		Action: "select_group", GroupID: sensitive,
	})
	if !errors.Is(err, ErrInvalidAction) {
		t.Fatalf("placing a question group needs a destination, got %v", err)
	}

	placed := editDraft(t, s, batch.ID, "select_group", func(r *ReviewDraftEdit) {
		r.GroupID, r.Category = sensitive, "documents"
	})
	byID := choiceIDs(placed)
	for _, name := range []string{"passport-a.pdf", "passport-b.pdf"} {
		choice, ok := byID[candidateNamed(t, candidates, name).ID]
		if !ok || choice.Operation != OperationMove || choice.Category != CategoryDocuments || choice.SourceGroupID != sensitive {
			t.Fatalf("%s was not placed in Documents by the row choice: %+v", name, choice)
		}
	}
	if choice, ok := byID[trashed.ID]; !ok || choice.Operation != OperationTrash {
		t.Fatalf("the per-file Trash mark was lost or overwritten: %+v", choice)
	}
}
