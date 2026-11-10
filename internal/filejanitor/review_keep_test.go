package filejanitor

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestReviewKeep_ExactGroupCancelsMovesAndTrashWithoutFileActions(t *testing.T) {
	s, batch, candidates := draftServiceFixture(t, "one.png", "two.png", "song.wav")
	images := []JanitorCandidate{candidateNamed(t, candidates, "one.png"), candidateNamed(t, candidates, "two.png")}
	view := editDraft(t, s, batch.ID, "select_group", func(r *ReviewDraftEdit) { r.GroupID = reviewGroupID("filing-images") })
	preview, err := s.PreviewMoves(PreviewRequest{WorkspaceID: "ws-1", UserID: "local", Items: moveItems(images, ""), ReviewRevision: view.Revision})
	if err != nil {
		t.Fatal(err)
	}
	editDraft(t, s, batch.ID, "mark_trash", func(r *ReviewDraftEdit) { r.CandidateIDs = []string{images[1].ID} })
	after := editDraft(t, s, batch.ID, "keep_group", func(r *ReviewDraftEdit) { r.GroupID = reviewGroupID("filing-images") })
	if len(after.Choices) != 0 {
		t.Fatal("kept files retain invisible intent")
	}
	state, err := s.store.LoadScanState("ws-1")
	if err != nil || len(state.Skipped) != 2 || len(state.Actions) != 0 {
		t.Fatalf("keep wrote actions or wrong skips: %+v %v", state, err)
	}
	settings, _ := s.store.LoadSettings("ws-1")
	for _, c := range candidates {
		if _, err := os.Stat(filepath.Join(settings.RootPath, c.Name)); err != nil {
			t.Fatal("managed file moved", err)
		}
	}
	if _, err := s.ConsumeApproval("ws-1", "local", preview.Token, planFrom(preview, images), batch.ID); !errors.Is(err, ErrApprovalInvalid) {
		t.Fatal("keep retained preview authority", err)
	}
	if groups, err := s.ReadReviewGroups(t.Context(), "local", "ws-1", batch.ID); err != nil || groups.Counts.Kept != 2 || groups.Counts.Questions != 1 {
		t.Fatalf("incorrect kept accounting: %+v %v", groups, err)
	}
}

func TestReviewKeep_AloneRevokesAnOtherwiseCurrentPreview(t *testing.T) {
	s, batch, candidates := draftServiceFixture(t, "one.png", "two.png")
	view := editDraft(t, s, batch.ID, "select_group", func(r *ReviewDraftEdit) { r.GroupID = reviewGroupID("filing-images") })
	preview, err := s.PreviewMoves(PreviewRequest{WorkspaceID: "ws-1", UserID: "local", Items: moveItems(candidates, ""), ReviewRevision: view.Revision})
	if err != nil {
		t.Fatal(err)
	}
	editDraft(t, s, batch.ID, "keep_group", func(r *ReviewDraftEdit) { r.GroupID = reviewGroupID("filing-images") })
	if _, err := s.ConsumeApproval("ws-1", "local", preview.Token, planFrom(preview, candidates), batch.ID); !errors.Is(err, ErrApprovalInvalid) {
		t.Fatal("Keep here alone retained authority", err)
	}
}

func TestReviewKeep_UnchangedScanAndTargetedReconsiderationRequireExplicitRescan(t *testing.T) {
	s, batch, candidates := draftServiceFixture(t, "one.png", "two.png")
	editDraft(t, s, batch.ID, "keep_group", func(r *ReviewDraftEdit) { r.GroupID = reviewGroupID("filing-images") })
	if _, created, err := s.ScanNow("ws-1", ScanSourceManual); err != nil || created {
		t.Fatal("unchanged kept file reproposed", created, err)
	}
	before, _ := s.store.LoadScanState("ws-1")
	after := editDraft(t, s, batch.ID, "reconsider_files", func(r *ReviewDraftEdit) { r.CandidateIDs = []string{candidates[0].ID} })
	state, _ := s.store.LoadScanState("ws-1")
	if len(after.Choices) != 0 || len(state.Batches) != len(before.Batches) || len(state.Actions) != 0 || state.IsSkipped(candidates[0].Fingerprint) || !state.IsSkipped(candidates[1].Fingerprint) {
		t.Fatal("targeted reconsideration scanned, selected, or forgot unrelated state")
	}
	old, _ := state.Candidate(candidates[0].ID)
	if old.State != CandidateSkipped {
		t.Fatal("old candidate revived")
	}
	fresh, created, err := s.ScanNow("ws-1", ScanSourceManual)
	if err != nil || !created {
		t.Fatal("explicit scan did not create fresh proposal", err)
	}
	_, proposed, _ := s.BatchDetail("ws-1", fresh.ID)
	if len(proposed) != 1 || proposed[0].Name != candidates[0].Name || proposed[0].ID == candidates[0].ID {
		t.Fatalf("wrong fresh proposal: %+v", proposed)
	}
	view, err := s.ReadReviewDraft(t.Context(), "local", "ws-1", fresh.ID)
	if err != nil || len(view.Choices) != 0 {
		t.Fatal("reconsideration selected a replacement", err)
	}
}

func TestReviewKeep_ChangedMemberAndStaleRetryRejectAtomically(t *testing.T) {
	s, batch, candidates := draftServiceFixture(t, "one.png", "two.png")
	before, _ := s.ReadReviewDraft(t.Context(), "local", "ws-1", batch.ID)
	request := ReviewDraftEdit{ExpectedRevision: before.Revision, ExpectedGroupRevision: before.GroupRevision, Action: "keep_group", GroupID: reviewGroupID("filing-images")}
	settings, _ := s.store.LoadSettings("ws-1")
	if err := os.WriteFile(filepath.Join(settings.RootPath, candidates[1].Name), []byte("replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EditReviewDraft("local", "ws-1", batch.ID, request); !errors.Is(err, ErrCandidateNotActionable) {
		t.Fatal("changed member accepted", err)
	}
	state, _ := s.store.LoadScanState("ws-1")
	if len(state.Skipped) != 0 || len(state.ReviewDrafts) != 0 || state.Candidates[0].State != CandidatePending {
		t.Fatal("partial keep persisted")
	}
	editDraft(t, s, batch.ID, "keep_files", func(r *ReviewDraftEdit) { r.CandidateIDs = []string{candidates[0].ID} })
	saved, _ := s.store.LoadScanState("ws-1")
	if _, err := s.EditReviewDraft("local", "ws-1", batch.ID, request); !errors.Is(err, ErrReviewConflict) {
		t.Fatal("stale keep retried against new membership", err)
	}
	state, _ = s.store.LoadScanState("ws-1")
	if !reflect.DeepEqual(saved, state) {
		t.Fatal("rejected retry changed state")
	}
}

func TestReviewKeep_ChangedKeptFileMayBeProposedAgainWithoutSelectingIt(t *testing.T) {
	s, batch, candidates := draftServiceFixture(t, "one.png")
	editDraft(t, s, batch.ID, "keep_files", func(r *ReviewDraftEdit) { r.CandidateIDs = []string{candidates[0].ID} })
	settings, _ := s.store.LoadSettings("ws-1")
	agedFile(t, settings.RootPath, "one.png", 32)
	if _, created, err := s.ScanNow("ws-1", ScanSourceManual); err != nil || created {
		t.Fatal("changed input must settle first", err)
	}
	future := time.Now().Add(SettleInterval + time.Second)
	s.scannerFor().now = func() time.Time { return future }
	fresh, created, err := s.ScanNow("ws-1", ScanSourceManual)
	if err != nil || !created {
		t.Fatal("changed kept state was permanently suppressed", err)
	}
	_, proposed, _ := s.BatchDetail("ws-1", fresh.ID)
	if len(proposed) != 1 || proposed[0].ID == candidates[0].ID {
		t.Fatal("changed file did not get a fresh candidate")
	}
	view, err := s.ReadReviewDraft(t.Context(), "local", "ws-1", fresh.ID)
	if err != nil || len(view.Choices) != 0 {
		t.Fatal("changed replacement inherited choices", err)
	}
}

func TestReviewQuestions_BoundedReasonsAndRelatedMaterialAreNotVerifiedProjects(t *testing.T) {
	batch, candidates := groupFixture("session.wav", "session.zip", "session.pdf", "unrelated.pdf", strings.Repeat("x", 1025)+".png")
	groups := mustGroup(t, batch, candidates)
	for _, c := range candidates[:3] {
		card, _ := groupFor(t, groups, c.ID)
		if card.ID != reviewGroupID("audio") || card.Question == "" || !strings.Contains(card.Reason, "not verified") {
			t.Fatalf("audio relation missing or overstated: %+v", card)
		}
	}
	unrelated, _ := groupFor(t, groups, candidates[3].ID)
	if unrelated.ID == reviewGroupID("audio") {
		t.Fatal("unrelated document invented a project")
	}
	long, _ := groupFor(t, groups, candidates[4].ID)
	if long.ID != reviewGroupID("unmatched") || strings.Contains(long.Reason, "sensitive-looking word") {
		t.Fatal("bounded name claimed a nonexistent sensitivity word")
	}
}
