package filejanitor

import (
	"errors"
	"os"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestReviewDraftRace_ExactlyOneConcurrentTabWinsCAS(t *testing.T) {
	s, batch, candidates := draftServiceFixture(t, "one.png", "two.png")
	before, err := s.ReadReviewDraft(t.Context(), "local", "ws-1", batch.ID)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for _, candidate := range candidates {
		workers.Add(1)
		go func(id string) {
			defer workers.Done()
			<-start
			_, err := s.EditReviewDraft("local", "ws-1", batch.ID, ReviewDraftEdit{ExpectedRevision: before.Revision, ExpectedGroupRevision: before.GroupRevision, Action: "select_files", CandidateIDs: []string{id}})
			results <- err
		}(candidate.ID)
	}
	close(start)
	workers.Wait()
	close(results)
	won, conflicts := 0, 0
	for err := range results {
		if err == nil {
			won++
		} else if errors.Is(err, ErrReviewConflict) {
			conflicts++
		} else {
			t.Fatalf("unexpected concurrent edit error: %v", err)
		}
	}
	if won != 1 || conflicts != 1 {
		t.Fatalf("lost-write CAS: won=%d conflicts=%d", won, conflicts)
	}
	view, err := s.ReadReviewDraft(t.Context(), "local", "ws-1", batch.ID)
	if err != nil || len(view.Choices) != 1 {
		t.Fatalf("concurrent edits merged or lost exact intent: %+v %v", view, err)
	}
}

func TestReviewDraftRace_FailedPersistenceLeavesPreviousChoicesIntact(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory mode restrictions")
	}
	s, batch, candidates := draftServiceFixture(t, "one.png", "two.png")
	before := editDraft(t, s, batch.ID, "select_files", func(r *ReviewDraftEdit) { r.CandidateIDs = []string{candidates[0].ID} })
	dir, err := s.store.StateDir("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(dir, 0o750); err != nil {
			t.Errorf("restore state dir: %v", err)
		}
	})
	_, err = s.EditReviewDraft("local", "ws-1", batch.ID, ReviewDraftEdit{ExpectedRevision: before.Revision, ExpectedGroupRevision: before.GroupRevision, Action: "select_files", CandidateIDs: []string{candidates[1].ID}})
	if err == nil {
		t.Fatal("read-only state directory unexpectedly accepted a write")
	}
	if err := os.Chmod(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	after, err := s.ReadReviewDraft(t.Context(), "local", "ws-1", batch.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("failed write leaked a falsely saved choice: %+v %v", after, err)
	}
}

func TestReviewDraftRace_AdmittedApplyFinishesBeforeALaterDeselect(t *testing.T) {
	s, batch, candidates := draftServiceFixture(t, "one.png")
	before := editDraft(t, s, batch.ID, "select_group", func(r *ReviewDraftEdit) { r.GroupID = reviewGroupID("filing-images") })
	preview, err := s.PreviewMoves(PreviewRequest{WorkspaceID: "ws-1", UserID: "local", Items: moveItems(candidates, ""), ReviewRevision: before.Revision})
	if err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	mover := &realMover{beforeMove: func(string, string) { close(started); <-release }}
	s.SetMover(mover)
	applied := make(chan error, 1)
	go func() {
		result, err := s.ConfirmMoves(t.Context(), ConfirmRequest{WorkspaceID: "ws-1", UserID: "local", BatchID: batch.ID, Token: preview.Token, Items: moveItems(candidates, "")})
		if err == nil && result.Applied != 1 {
			err = errors.New("apply did not report its actual move")
		}
		applied <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("apply did not reach the admitted move")
	}
	edited := make(chan error, 1)
	go func() {
		_, err := s.EditReviewDraft("local", "ws-1", batch.ID, ReviewDraftEdit{ExpectedRevision: before.Revision, ExpectedGroupRevision: before.GroupRevision, Action: "clear_all"})
		edited <- err
	}()
	select {
	case err := <-edited:
		t.Fatalf("edit bypassed admitted apply boundary: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	unblock()
	select {
	case err := <-applied:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("admitted apply did not finish")
	}
	select {
	case err := <-edited:
		if !errors.Is(err, ErrReviewConflict) {
			t.Fatalf("late edit overwrote apply result: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("edit did not join apply boundary")
	}
	if mover.calls != 1 {
		t.Fatal("execution was replayed")
	}
}
