package filejanitor

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// Write-side rules of the "Picture review" contract in
// docs/architecture/file-janitor-group-review.md: the plan check, the one File
// request, and Undo all. The APIs they call do not exist yet, so the stubs below
// stand in for them and make every test here SKIP with the task that owns it.
//
//	FILING_PENDING_STRICT=1 go test ./internal/filejanitor -run 'TestReviewPlan|TestFilingUndo'
//
// turns each skip into a failure, which is how the rules were shown red when
// they were written. Each task that adds the real API (2.1, 2.2, 5.2) must
// delete its stub block here: the duplicate declaration will not compile until
// it does, so a stub cannot outlive the feature. By delivery no stub, no
// pendingUntil call and no skip may remain (task 6.9 checks).

// ---- stubs: delete the block when the named task adds the real API ----

var errNotBuiltYet = errors.New("not built yet")

// Task 2.1 replaces these with the real plan check in review_plan.go.
type ReviewPlanQuery struct{ Revision, GroupRevision, Destination string }
type ReviewPlanChanged struct{ CandidateID, Name, Reason string }
type ReviewPlan struct {
	PlanDigest       string
	MoveCount, Total int
	OverLimit        bool
	Changed          []ReviewPlanChanged
}

func (s *Service) PlanReview(context.Context, string, string, string, ReviewPlanQuery) (ReviewPlan, error) {
	return ReviewPlan{}, errNotBuiltYet
}

// Task 2.2 replaces these with the real File request in review_plan.go.
var ErrReviewPlanChanged = errors.New("the plan changed since it was shown")

type FileRequest struct {
	ExpectedRevision, ExpectedGroupRevision, PlanDigest, ClientKey string
	TrashAcknowledged                                              int
}
type FilingResult struct {
	FilingID string
	Replayed bool
	Filed    int
	Plan     ReviewPlan
}

func (s *Service) FileReviewPlan(context.Context, string, string, string, FileRequest) (FilingResult, error) {
	return FilingResult{}, errNotBuiltYet
}

// Task 5.2 replaces these with Undo all in filing_undo.go.
type FilingUndoResult struct {
	FilingID string
	Replayed bool
	Back     int
}

func (s *Service) UndoFiling(context.Context, string, string, string) (FilingUndoResult, error) {
	return FilingUndoResult{}, errNotBuiltYet
}

// ---- end of stubs ----

// pendingTask skips (or, with FILING_PENDING_STRICT=1, fails) a test whose rule
// belongs to a later task and cannot pass yet. The task that builds the rule
// deletes the call; task 6.9 checks that none is left.
func pendingTask(t *testing.T, task string) {
	t.Helper()
	if os.Getenv("FILING_PENDING_STRICT") == "1" {
		return // run for real: the test is expected to fail until the task lands
	}
	t.Skipf("pending task %s: the rule is specified and the test is written; the code is not", task)
}

// pendingUntil skips (or, with FILING_PENDING_STRICT=1, fails) while the API a
// test needs is still a stub.
func pendingUntil(t *testing.T, task string, err error) {
	t.Helper()
	if !errors.Is(err, errNotBuiltYet) {
		return
	}
	if os.Getenv("FILING_PENDING_STRICT") == "1" {
		t.Fatalf("task %s has not built this yet", task)
	}
	t.Skipf("pending task %s: the rule is specified, the API is not built yet", task)
}

// pressFixture ticks every image of a small scan and returns the saved view, so
// these tests do not depend on how task 1.2 ticks a scan on open.
func pressFixture(t *testing.T, names ...string) (*Service, JanitorBatch, ReviewDraftView, *realMover) {
	t.Helper()
	s, batch, _ := draftServiceFixture(t, names...)
	mover := &realMover{}
	s.SetMover(mover)
	view := editDraft(t, s, batch.ID, "select_group", func(r *ReviewDraftEdit) { r.GroupID = reviewGroupID("filing-images") })
	if len(view.Choices) != len(names) {
		t.Fatalf("setup: expected %d ticked files, got %d", len(names), len(view.Choices))
	}
	return s, batch, view, mover
}

func planOf(t *testing.T, s *Service, batchID string, view ReviewDraftView) ReviewPlan {
	t.Helper()
	plan, err := s.PlanReview(t.Context(), "local", "ws-1", batchID, ReviewPlanQuery{Revision: view.Revision, GroupRevision: view.GroupRevision})
	pendingUntil(t, "2.1", err)
	if err != nil {
		t.Fatalf("plan check: %v", err)
	}
	return plan
}

func pressRequest(view ReviewDraftView, plan ReviewPlan, key string) FileRequest {
	return FileRequest{ExpectedRevision: view.Revision, ExpectedGroupRevision: view.GroupRevision, PlanDigest: plan.PlanDigest, ClientKey: key}
}

const pressKey = "press-key-0123456789"

// R34: the plan check draws the picture and gives no authority. It leaves the
// saved scan byte-identical, issues no approval, creates no folder, and reads no
// contents (a file nobody may open is still planned from its metadata).
func TestReviewPlan_CheckIsReadOnly(t *testing.T) {
	s, batch, view, _ := pressFixture(t, "one.png", "two.png")
	settings, _ := s.store.LoadSettings("ws-1")
	unreadable := filepath.Join(settings.RootPath, "two.png")
	if os.Geteuid() != 0 {
		if err := os.Chmod(unreadable, 0o000); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(unreadable, 0o600) })
	}
	before := scanStateBytes(t, s)

	plan := planOf(t, s, batch.ID, view)

	if plan.MoveCount != 2 || plan.PlanDigest == "" || plan.OverLimit {
		t.Fatalf("the plan must cover both files, an unreadable one included: %+v", plan)
	}
	if after := scanStateBytes(t, s); !bytes.Equal(before, after) {
		t.Fatal("the plan check changed the saved scan state")
	}
	state, _ := s.store.LoadScanState("ws-1")
	if len(state.Approvals) != 0 || len(state.Actions) != 0 {
		t.Fatal("the plan check issued authority or acted")
	}
	// Setup creates the Filed folder itself; planning must not add a category
	// folder under it.
	images, err := DestinationDir(settings, CategoryImages)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(images); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the plan check created a destination folder: %v", err)
	}
}

// R36, A2: if anything differs when the press arrives, nothing moves, nothing is
// journaled, and the fresh plan comes back.
func TestReviewPlan_StalePlanPressMovesNothing(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(t *testing.T, s *Service, root string)
	}{
		{"a ticked file changed", func(t *testing.T, _ *Service, root string) {
			f, err := os.OpenFile(filepath.Join(root, "one.png"), os.O_APPEND|os.O_WRONLY, 0)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.Write([]byte("x")); err != nil {
				t.Fatal(err)
			}
			_ = f.Close()
		}},
		{"a ticked file was deleted", func(t *testing.T, _ *Service, root string) {
			if err := os.Remove(filepath.Join(root, "one.png")); err != nil {
				t.Fatal(err)
			}
		}},
		{"a name clash appeared in the destination", func(t *testing.T, s *Service, root string) {
			settings, _ := s.store.LoadSettings("ws-1")
			dir, err := DestinationDir(settings, CategoryImages)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(dir, 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "one.png"), []byte("already here"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, batch, view, mover := pressFixture(t, "one.png", "two.png")
			settings, _ := s.store.LoadSettings("ws-1")
			plan := planOf(t, s, batch.ID, view)
			tc.change(t, s, settings.RootPath)
			before := scanStateBytes(t, s)

			_, err := s.FileReviewPlan(t.Context(), "local", "ws-1", batch.ID, pressRequest(view, plan, pressKey))
			pendingUntil(t, "2.2", err)
			if !errors.Is(err, ErrReviewPlanChanged) {
				t.Fatalf("a stale plan must be refused as changed, got %v", err)
			}
			if mover.calls != 0 {
				t.Fatalf("a stale plan moved %d files", mover.calls)
			}
			if after := scanStateBytes(t, s); !bytes.Equal(before, after) {
				t.Fatal("a refused press left an approval, a filing record or a state change behind")
			}
		})
	}
}

// R36: a change committed in another window before the press is admitted wins.
func TestReviewPlan_EditBeforeAdmissionWins(t *testing.T) {
	s, batch, view, mover := pressFixture(t, "one.png", "two.png")
	plan := planOf(t, s, batch.ID, view)
	_, candidates, _ := s.BatchDetail("ws-1", batch.ID)
	editDraft(t, s, batch.ID, "exclude_files", func(r *ReviewDraftEdit) {
		r.CandidateIDs = []string{candidateNamed(t, candidates, "one.png").ID}
	})

	_, err := s.FileReviewPlan(t.Context(), "local", "ws-1", batch.ID, pressRequest(view, plan, pressKey))
	pendingUntil(t, "2.2", err)
	if !errors.Is(err, ErrReviewPlanChanged) && !errors.Is(err, ErrReviewConflict) {
		t.Fatalf("a press built on a replaced choice set must be refused, got %v", err)
	}
	if mover.calls != 0 {
		t.Fatalf("the press filed %d files after the person had left one out", mover.calls)
	}
	state, _ := s.store.LoadScanState("ws-1")
	if len(state.Approvals) != 0 || len(state.Actions) != 0 {
		t.Fatal("a refused press issued authority")
	}
}

// R41: two presses with the same client key (a double click, or a retry after a
// lost connection) file once; the second reports the first result.
func TestReviewPlan_SameClientKeyFilesOnce(t *testing.T) {
	s, batch, view, mover := pressFixture(t, "one.png", "two.png")
	plan := planOf(t, s, batch.ID, view)
	request := pressRequest(view, plan, pressKey)

	start := make(chan struct{})
	results := make([]FilingResult, 2)
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results[i], errs[i] = s.FileReviewPlan(t.Context(), "local", "ws-1", batch.ID, request)
		}()
	}
	close(start)
	wg.Wait()
	for _, err := range errs {
		pendingUntil(t, "2.2", err)
		if err != nil {
			t.Fatalf("a repeated press must report the first result, got %v", err)
		}
	}
	if mover.calls != 2 {
		t.Fatalf("two presses filed %d times; each of the two files must move exactly once", mover.calls)
	}
	if results[0].FilingID == "" || results[0].FilingID != results[1].FilingID {
		t.Fatalf("both presses must report one filing: %q %q", results[0].FilingID, results[1].FilingID)
	}
	if results[0].Replayed == results[1].Replayed {
		t.Fatalf("exactly one press is the original: replayed = %v %v", results[0].Replayed, results[1].Replayed)
	}
	again, err := s.FileReviewPlan(t.Context(), "local", "ws-1", batch.ID, request)
	if err != nil || !again.Replayed || again.FilingID != results[0].FilingID || mover.calls != 2 {
		t.Fatalf("a later retry must replay without moving: %+v %v (calls %d)", again, err, mover.calls)
	}
}

// R50: Undo all runs once. A second press, or a second window, waits for the run
// that owns it and then reports that run's result; it never starts a second one.
func TestFilingUndo_SecondRunCannotStart(t *testing.T) {
	s, batch, view, mover := pressFixture(t, "one.png", "two.png")
	plan := planOf(t, s, batch.ID, view)
	filed, err := s.FileReviewPlan(t.Context(), "local", "ws-1", batch.ID, pressRequest(view, plan, pressKey))
	pendingUntil(t, "2.2", err)
	if err != nil || filed.Filed != 2 {
		t.Fatalf("setup: filing failed: %+v %v", filed, err)
	}
	filedCalls := mover.calls

	started, release := make(chan struct{}), make(chan struct{})
	var once, releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	mover.beforeMove = func(string, string) {
		once.Do(func() { close(started) })
		<-release
	}

	first := make(chan FilingUndoResult, 1)
	firstErr := make(chan error, 1)
	go func() {
		result, err := s.UndoFiling(t.Context(), "local", "ws-1", filed.FilingID)
		first <- result
		firstErr <- err
	}()
	pendingUndo := func() {
		select {
		case <-started:
		case err := <-firstErr:
			pendingUntil(t, "5.2", err)
			t.Fatalf("Undo all ended before it moved anything: %v", err)
		case <-time.After(5 * time.Second):
			t.Fatal("Undo all did not start")
		}
	}
	pendingUndo()

	second := make(chan FilingUndoResult, 1)
	go func() {
		result, _ := s.UndoFiling(t.Context(), "local", "ws-1", filed.FilingID)
		second <- result
	}()
	select {
	case <-second:
		t.Fatal("a second Undo all returned while the first was still running")
	case <-time.After(20 * time.Millisecond):
	}
	if undoCalls := mover.calls - filedCalls; undoCalls != 1 {
		t.Fatalf("a second run started: %d undo moves while the first is blocked", undoCalls)
	}
	unblock()
	one, two := <-first, <-second
	if err := <-firstErr; err != nil {
		t.Fatal(err)
	}
	if one.Back != 2 || two.Back != 2 || one.Replayed || !two.Replayed {
		t.Fatalf("the second press must replay the first run's result: %+v %+v", one, two)
	}
	if undoCalls := mover.calls - filedCalls; undoCalls != 2 {
		t.Fatalf("each file must be put back exactly once, got %d undo moves", undoCalls)
	}
}
