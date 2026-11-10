package filejanitor

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func draftServiceFixture(t *testing.T, names ...string) (*Service, JanitorBatch, []JanitorCandidate) {
	t.Helper()
	s, root := configuredService(t)
	ws, err := s.workspaces.Get("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	ws.OwnerUserID = "local"
	ws.InstalledCapabilities = []workspace.InstalledCapability{{ID: workspace.CapabilityFileJanitor, Version: 1, InstalledAt: time.Now(), Source: workspace.InstallSourceInPlace}}
	for _, name := range names {
		agedFile(t, root, name, 10)
	}
	batch, created, err := s.ScanNow(ws.ID, ScanSourceManual)
	if err != nil || !created {
		t.Fatalf("fixture scan: %v %v", created, err)
	}
	_, candidates, err := s.BatchDetail(ws.ID, batch.ID)
	if err != nil {
		t.Fatal(err)
	}
	return s, batch, candidates
}

func editDraft(t *testing.T, s *Service, batchID, action string, configure func(*ReviewDraftEdit)) ReviewDraftView {
	t.Helper()
	before, err := s.ReadReviewDraft(t.Context(), "local", "ws-1", batchID)
	if err != nil {
		t.Fatal(err)
	}
	request := ReviewDraftEdit{ExpectedRevision: before.Revision, ExpectedGroupRevision: before.GroupRevision, Action: action}
	if configure != nil {
		configure(&request)
	}
	after, err := s.EditReviewDraft("local", "ws-1", batchID, request)
	if err != nil {
		t.Fatalf("%s: %v", action, err)
	}
	return after
}

func candidateNamed(t *testing.T, candidates []JanitorCandidate, name string) JanitorCandidate {
	t.Helper()
	for _, candidate := range candidates {
		if candidate.Name == name {
			return candidate
		}
	}
	t.Fatalf("missing candidate %s", name)
	return JanitorCandidate{}
}

func TestReviewDraftEdits_ResolveAll206MembersAndKeepOffPageExclusions(t *testing.T) {
	names := make([]string, 206)
	for i := range names {
		names[i] = fmt.Sprintf("image-%04d.png", i)
	}
	s, batch, candidates := draftServiceFixture(t, names...)
	groupID := reviewGroupID("filing-images")
	selected := editDraft(t, s, batch.ID, "select_group", func(r *ReviewDraftEdit) { r.GroupID = groupID })
	if len(selected.Choices) != 206 {
		t.Fatalf("selected only a detail page: %d", len(selected.Choices))
	}
	excluded := []string{candidates[51].ID, candidates[205].ID}
	remaining := editDraft(t, s, batch.ID, "exclude_files", func(r *ReviewDraftEdit) { r.CandidateIDs = excluded })
	if len(remaining.Choices) != 204 {
		t.Fatalf("wrong exception count: %d", len(remaining.Choices))
	}
	for _, choice := range remaining.Choices {
		if choice.CandidateID == excluded[0] || choice.CandidateID == excluded[1] || choice.SourceGroupID != groupID || choice.Operation != OperationMove {
			t.Fatal("off-page exclusion/provenance lost")
		}
	}
	restarted := NewService(NewStore(s.store.resolver), s.workspaces)
	restored, err := restarted.ReadReviewDraft(t.Context(), "local", "ws-1", batch.ID)
	if err != nil || !reflect.DeepEqual(remaining, restored) {
		t.Fatalf("restart lost exact members: %v", err)
	}
	state, _ := s.store.LoadScanState("ws-1")
	if len(state.Approvals) != 0 || len(state.Actions) != 0 {
		t.Fatal("selection issued authority or changed files")
	}
}

func TestReviewDraftEdits_GroupUndoPreservesPriorPerFileChoiceAndDestination(t *testing.T) {
	s, batch, candidates := draftServiceFixture(t, "one.png", "two.png", "three.png")
	prior := candidates[0].ID
	editDraft(t, s, batch.ID, "select_files", func(r *ReviewDraftEdit) { r.CandidateIDs, r.Category = []string{prior}, "data" })
	groupID := reviewGroupID("filing-images")
	selected := editDraft(t, s, batch.ID, "select_group", func(r *ReviewDraftEdit) { r.GroupID = groupID })
	if len(selected.Choices) != 3 {
		t.Fatal("group did not add remaining members")
	}
	changed := editDraft(t, s, batch.ID, "set_category", func(r *ReviewDraftEdit) { r.GroupID, r.Category = groupID, "installers" })
	for _, choice := range changed.Choices {
		if choice.CandidateID == prior && (choice.SourceGroupID != "" || choice.Category != CategoryData) {
			t.Fatal("group overwrote prior per-file exception")
		}
		if choice.CandidateID != prior && choice.Category != CategoryInstallers {
			t.Fatal("group category not applied")
		}
	}
	undone := editDraft(t, s, batch.ID, "clear_group", func(r *ReviewDraftEdit) { r.GroupID = groupID })
	if len(undone.Choices) != 1 || undone.Choices[0].CandidateID != prior || undone.Choices[0].Category != CategoryData {
		t.Fatalf("group Undo removed unrelated choice: %+v", undone)
	}
}

func TestReviewDraftEdits_PerFileChangePromotesExceptionAndCategoryAloneNeverSelects(t *testing.T) {
	s, batch, candidates := draftServiceFixture(t, "one.png", "two.png")
	id := candidates[0].ID
	onlyCategory := editDraft(t, s, batch.ID, "set_category", func(r *ReviewDraftEdit) { r.CandidateIDs, r.Category = []string{id}, "documents" })
	if len(onlyCategory.Choices) != 0 {
		t.Fatal("category edit invented selection")
	}
	groupID := reviewGroupID("filing-images")
	editDraft(t, s, batch.ID, "select_group", func(r *ReviewDraftEdit) { r.GroupID = groupID })
	editDraft(t, s, batch.ID, "set_category", func(r *ReviewDraftEdit) { r.CandidateIDs, r.Category = []string{id}, "audio" })
	undone := editDraft(t, s, batch.ID, "clear_group", func(r *ReviewDraftEdit) { r.GroupID = groupID })
	if len(undone.Choices) != 1 || undone.Choices[0].CandidateID != id || undone.Choices[0].Category != CategoryAudio || undone.Choices[0].SourceGroupID != "" {
		t.Fatal("group Undo lost later explicit exception")
	}
}

func TestReviewDraftEdits_DestinationProvenanceSurvivesDeselectAndReselect(t *testing.T) {
	s, batch, candidates := draftServiceFixture(t, "one.png", "two.png", "three.png")
	prior := candidates[0].ID
	if _, err := s.ApplyDecisions("ws-1", []DecisionUpdate{{CandidateID: prior, Decision: DecisionMove, Category: "data"}}); err != nil {
		t.Fatal(err)
	}
	groupID := reviewGroupID("filing-images")
	editDraft(t, s, batch.ID, "select_group", func(r *ReviewDraftEdit) { r.GroupID = groupID })
	editDraft(t, s, batch.ID, "set_category", func(r *ReviewDraftEdit) { r.GroupID, r.Category = groupID, "archives" })
	cleared := editDraft(t, s, batch.ID, "clear_group", func(r *ReviewDraftEdit) { r.GroupID = groupID })
	if len(cleared.Choices) != 0 {
		t.Fatal("legacy category-only edit became a retained selection after group Undo")
	}
	editDraft(t, s, batch.ID, "select_group", func(r *ReviewDraftEdit) { r.GroupID = groupID })
	changed := editDraft(t, s, batch.ID, "set_category", func(r *ReviewDraftEdit) { r.GroupID, r.Category = groupID, "documents" })
	for _, choice := range changed.Choices {
		want := CategoryDocuments
		if choice.CandidateID == prior {
			want = CategoryData
		}
		if choice.Category != want {
			t.Fatalf("destination origin was lost across reselect: %+v", choice)
		}
	}
	if len(changed.Groups) != 1 || !changed.Groups[0].MixedDestinations || changed.Groups[0].Selected != 3 {
		t.Fatal("mixed whole-group state is inaccurate")
	}
	editDraft(t, s, batch.ID, "mark_trash", func(r *ReviewDraftEdit) { r.CandidateIDs = []string{prior} })
	deselected := editDraft(t, s, batch.ID, "deselect_group", func(r *ReviewDraftEdit) { r.GroupID = groupID })
	if len(deselected.Choices) != 1 || deselected.Choices[0].Operation != OperationTrash {
		t.Fatal("explicit group deselection cleared a separate Trash mark")
	}
}

func TestReviewDraftEdits_SelectAllExcludesQuestionsAndNeverCreatesTrash(t *testing.T) {
	s, batch, candidates := draftServiceFixture(t, "one.png", "two.png", "app.dmg", "passport.pdf", "take.wav", "notes-v1.pdf", "notes-v2.pdf", "unknown.qqq")
	trashID := candidateNamed(t, candidates, "one.png").ID
	editDraft(t, s, batch.ID, "mark_trash", func(r *ReviewDraftEdit) { r.CandidateIDs = []string{trashID} })
	view := editDraft(t, s, batch.ID, "select_filing_groups", nil)
	if len(view.Choices) != 3 {
		t.Fatalf("questions entered routine filing: %+v", view)
	}
	moves, trash := 0, 0
	for _, choice := range view.Choices {
		if choice.Operation == OperationTrash {
			trash++
			if choice.CandidateID != trashID {
				t.Fatal("bulk action invented Trash")
			}
		} else {
			moves++
		}
	}
	if moves != 2 || trash != 1 {
		t.Fatal("select-all overwrote existing per-file Trash")
	}
}

func TestReviewDraftEdits_RejectStaleCASMembershipAndAtomicInvalidRequests(t *testing.T) {
	for _, mode := range []string{"duplicate", "foreign member", "foreign group", "bad category", "question group", "unknown action", "cross batch", "stale revision", "stale members", "bulk trash", "invalid shape"} {
		t.Run(mode, func(t *testing.T) {
			s, batch, candidates := draftServiceFixture(t, "one.png", "two.png", "unknown.qqq")
			view, err := s.ReadReviewDraft(t.Context(), "local", "ws-1", batch.ID)
			if err != nil {
				t.Fatal(err)
			}
			request := ReviewDraftEdit{ExpectedRevision: view.Revision, ExpectedGroupRevision: view.GroupRevision, Action: "select_files", CandidateIDs: []string{candidates[0].ID}}
			switch mode {
			case "duplicate":
				request.CandidateIDs = []string{candidates[0].ID, candidates[0].ID}
			case "foreign member":
				request.CandidateIDs = []string{candidates[0].ID, "foreign"}
			case "foreign group":
				request.Action, request.CandidateIDs, request.GroupID = "select_group", nil, "group-foreign"
			case "bad category":
				request.Category = "../../outside"
			case "question group":
				request.Action, request.CandidateIDs, request.GroupID = "select_group", nil, reviewGroupID("unresolved")
			case "unknown action":
				request.Action = "select_future_matches"
			case "cross batch":
				if _, err := s.store.UpdateScanState("ws-1", func(current *ScanState) error { current.Candidates[0].BatchID = "foreign-batch"; return nil }); err != nil {
					t.Fatal(err)
				}
			case "stale revision":
				request.ExpectedRevision = strings.Repeat("x", 64)
			case "stale members":
				request.ExpectedGroupRevision = strings.Repeat("x", 64)
			case "bulk trash":
				request.Action, request.CandidateIDs = "mark_trash", []string{candidates[0].ID, candidates[1].ID}
			case "invalid shape":
				request.Action, request.GroupID = "clear_all", "group-foreign"
			}
			before, _ := s.store.LoadScanState("ws-1")
			if _, err := s.EditReviewDraft("local", "ws-1", batch.ID, request); err == nil {
				t.Fatal("invalid request succeeded")
			}
			after, _ := s.store.LoadScanState("ws-1")
			if !reflect.DeepEqual(before, after) {
				t.Fatal("invalid edit partially persisted")
			}
		})
	}
}

func TestReviewDraftEdits_NeedsReviewRequiresExplicitCategory(t *testing.T) {
	s, batch, candidates := draftServiceFixture(t, "unknown.qqq")
	before, err := s.ReadReviewDraft(t.Context(), "local", "ws-1", batch.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.EditReviewDraft("local", "ws-1", batch.ID, ReviewDraftEdit{ExpectedRevision: before.Revision, ExpectedGroupRevision: before.GroupRevision, Action: "select_files", CandidateIDs: []string{candidates[0].ID}})
	if !errors.Is(err, ErrCandidateNotActionable) {
		t.Fatalf("unresolved Needs review was selected: %v", err)
	}
	view := editDraft(t, s, batch.ID, "select_files", func(r *ReviewDraftEdit) { r.CandidateIDs, r.Category = []string{candidates[0].ID}, "documents" })
	if len(view.Choices) != 1 || view.Choices[0].Category != CategoryDocuments {
		t.Fatal("explicit classification review not usable")
	}
}

func TestReviewDraftEdits_LegacyCategoryCannotResurrectInvalidatedSelection(t *testing.T) {
	s, batch, candidates := draftServiceFixture(t, "one.png")
	groupID := reviewGroupID("filing-images")
	editDraft(t, s, batch.ID, "select_group", func(r *ReviewDraftEdit) { r.GroupID = groupID })
	editDraft(t, s, batch.ID, "set_category", func(r *ReviewDraftEdit) { r.GroupID, r.Category = groupID, "documents" })
	if _, err := s.store.UpdateScanState("ws-1", func(state *ScanState) error { state.Candidates[0].NeedsReview = true; return nil }); err != nil {
		t.Fatal(err)
	}
	invalid, err := s.ReadReviewDraft(t.Context(), "local", "ws-1", batch.ID)
	if err != nil || len(invalid.Choices) != 0 || invalid.RemovedStale != 1 {
		t.Fatal("new unresolved classification did not invalidate group intent", err)
	}
	if _, err := s.ApplyDecisions("ws-1", []DecisionUpdate{{CandidateID: candidates[0].ID, Decision: DecisionMove, Category: "archives"}}); err != nil {
		t.Fatal(err)
	}
	after, err := s.ReadReviewDraft(t.Context(), "local", "ws-1", batch.ID)
	if err != nil || len(after.Choices) != 0 {
		t.Fatalf("category answer resurrected an old invalid selection: %+v %v", after, err)
	}
}

func TestReviewDraftEdits_LaterArrivalsNeverJoinAnOldDisplayedGroup(t *testing.T) {
	s, batch, candidates := draftServiceFixture(t, "one.png")
	before, err := s.ReadReviewDraft(t.Context(), "local", "ws-1", batch.ID)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := s.store.LoadSettings("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	agedFile(t, settings.RootPath, "later.png", 10)
	newBatch, created, err := s.ScanNow("ws-1", ScanSourceManual)
	if err != nil || !created || newBatch.ID == batch.ID {
		t.Fatal("fixture new arrival did not produce a distinct batch")
	}
	after, err := s.EditReviewDraft("local", "ws-1", batch.ID, ReviewDraftEdit{
		ExpectedRevision: before.Revision, ExpectedGroupRevision: before.GroupRevision,
		Action: "select_group", GroupID: reviewGroupID("filing-images"),
	})
	if err != nil || len(after.Choices) != 1 || after.Choices[0].CandidateID != candidates[0].ID {
		t.Fatalf("old group widened to future files: %+v %v", after, err)
	}
}

func TestReviewDraftEdits_LegacyCategoryClearAndSkipReconcileExactIntent(t *testing.T) {
	for _, decision := range []Decision{DecisionMove, DecisionNone, DecisionSkip} {
		t.Run(string(decision), func(t *testing.T) {
			s, batch, candidates := draftServiceFixture(t, "one.png", "two.png")
			before := editDraft(t, s, batch.ID, "select_group", func(r *ReviewDraftEdit) { r.GroupID = reviewGroupID("filing-images") })
			if _, err := s.ApplyDecisions("ws-1", []DecisionUpdate{{CandidateID: candidates[0].ID, Decision: decision, Category: "documents"}}); err != nil {
				t.Fatal(err)
			}
			after, err := s.ReadReviewDraft(t.Context(), "local", "ws-1", batch.ID)
			if err != nil || after.Revision == before.Revision {
				t.Fatalf("legacy edit did not fence intent: %+v %v", after, err)
			}
			want := 1
			if decision == DecisionMove {
				want = 2
			}
			if len(after.Choices) != want {
				t.Fatalf("legacy decision resurrected/removed unrelated selections: %+v", after)
			}
			for _, choice := range after.Choices {
				if choice.CandidateID == candidates[0].ID && (decision != DecisionMove || choice.Category != CategoryDocuments || choice.SourceGroupID != "") {
					t.Fatal("legacy per-file destination did not become an explicit exception")
				}
			}
		})
	}
}

func TestReviewDraftEdits_OldTabCannotUndoNewSelection(t *testing.T) {
	s, batch, candidates := draftServiceFixture(t, "one.png", "two.png")
	old, err := s.ReadReviewDraft(t.Context(), "local", "ws-1", batch.ID)
	if err != nil {
		t.Fatal(err)
	}
	newer := editDraft(t, s, batch.ID, "select_group", func(r *ReviewDraftEdit) { r.GroupID = reviewGroupID("filing-images") })
	_, err = s.EditReviewDraft("local", "ws-1", batch.ID, ReviewDraftEdit{ExpectedRevision: old.Revision, ExpectedGroupRevision: old.GroupRevision, Action: "exclude_files", CandidateIDs: []string{candidates[0].ID}})
	if !errors.Is(err, ErrReviewConflict) {
		t.Fatalf("stale tab overwrote newer selection: %v", err)
	}
	current, err := s.ReadReviewDraft(t.Context(), "local", "ws-1", batch.ID)
	if err != nil || !reflect.DeepEqual(newer, current) {
		t.Fatal("conflicting edit changed durable choices")
	}
}
