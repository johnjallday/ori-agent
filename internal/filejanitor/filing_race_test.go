package filejanitor

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// Press versus edit: released together from the same view, exactly one wins. If
// the press wins the file moves and the edit loses its compare-and-swap; if the
// edit wins nothing moves and the press is refused as changed. Never both, and
// never a file moved that the person had already left out.
func TestFilingRace_PressVersusUntick(t *testing.T) {
	const rounds = 25
	pressWins, editWins := 0, 0
	for round := range rounds {
		t.Run(fmt.Sprintf("round %d", round), func(t *testing.T) {
			s, batch, candidates, root := planFixture(t, "a.png", "b.png")
			mover := &realMover{}
			s.SetMover(mover)
			plan := readPlan(t, s, batch.ID, ReviewPlanQuery{})
			target := candidateNamed(t, candidates, "a.png").ID

			start := make(chan struct{})
			var pressErr, editErr error
			var wg sync.WaitGroup
			wg.Add(2)
			go func() {
				defer wg.Done()
				<-start
				_, pressErr = s.FileReviewPlan(t.Context(), "local", "ws-1", batch.ID, FileRequest{
					ExpectedRevision: plan.Revision, ExpectedGroupRevision: plan.GroupRevision,
					PlanDigest: plan.PlanDigest, ClientKey: fmt.Sprintf("race-press-%010d", round),
				})
			}()
			// The press does more setup before it reaches the root lock, so a bare race
			// is always won by the edit. Staggering the edit by different amounts lets
			// both orderings happen; the assertion is the same for every one of them.
			editDelay := []time.Duration{0, 150 * time.Microsecond, 600 * time.Microsecond, 3 * time.Millisecond}[round%4]
			go func() {
				defer wg.Done()
				<-start
				time.Sleep(editDelay)
				_, editErr = s.EditReviewDraft("local", "ws-1", batch.ID, ReviewDraftEdit{
					ExpectedRevision: plan.Revision, ExpectedGroupRevision: plan.GroupRevision,
					Action: "exclude_files", CandidateIDs: []string{target},
				})
			}()
			close(start)
			wg.Wait()

			switch {
			case pressErr == nil && errors.Is(editErr, ErrReviewConflict):
				pressWins++
				if mover.calls != 2 || exists(t, root, "a.png") {
					t.Fatalf("the press won but filed %d files", mover.calls)
				}
			case editErr == nil && errors.Is(pressErr, ErrReviewPlanChanged):
				editWins++
				if mover.calls != 0 || !exists(t, root, "a.png") || !exists(t, root, "b.png") {
					t.Fatalf("the edit won but %d files moved", mover.calls)
				}
			default:
				t.Fatalf("exactly one may win: press=%v edit=%v", pressErr, editErr)
			}
		})
	}
	t.Logf("press won %d, edit won %d of %d rounds", pressWins, editWins, rounds)
}

// An edit attempted after a press has been admitted waits for it and then loses;
// the press is not undone halfway.
func TestFilingRace_AnAdmittedPressFinishesBeforeALaterUntick(t *testing.T) {
	s, batch, candidates, root := planFixture(t, "a.png", "b.png")
	started, release := make(chan struct{}), make(chan struct{})
	var startedOnce, releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	mover := &realMover{beforeMove: func(string, string) {
		startedOnce.Do(func() { close(started) })
		<-release
	}}
	s.SetMover(mover)
	plan := readPlan(t, s, batch.ID, ReviewPlanQuery{})

	pressed := make(chan error, 1)
	go func() {
		_, err := s.FileReviewPlan(t.Context(), "local", "ws-1", batch.ID, FileRequest{
			ExpectedRevision: plan.Revision, ExpectedGroupRevision: plan.GroupRevision,
			PlanDigest: plan.PlanDigest, ClientKey: keyOne,
		})
		pressed <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the press never reached its first move")
	}

	edited := make(chan error, 1)
	go func() {
		_, err := s.EditReviewDraft("local", "ws-1", batch.ID, ReviewDraftEdit{
			ExpectedRevision: plan.Revision, ExpectedGroupRevision: plan.GroupRevision,
			Action: "exclude_files", CandidateIDs: []string{candidateNamed(t, candidates, "a.png").ID},
		})
		edited <- err
	}()
	select {
	case err := <-edited:
		t.Fatalf("an edit got past an admitted press: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	unblock()
	select {
	case err := <-pressed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the admitted press did not finish")
	}
	select {
	case err := <-edited:
		if !errors.Is(err, ErrReviewConflict) {
			t.Fatalf("the late edit must lose its compare-and-swap: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the edit never returned")
	}
	if mover.calls != 2 || exists(t, root, "a.png") || exists(t, root, "b.png") {
		t.Fatalf("the press must finish what it admitted: %d moves", mover.calls)
	}
}

// Two presses of the same plan under different keys (two windows) file once: the
// second finds the folder already filed and is refused as changed.
func TestFilingRace_TwoPressesWithDifferentKeysFileOnce(t *testing.T) {
	s, batch, _, _ := planFixture(t, "a.png", "b.png", "c.pdf")
	mover := &realMover{}
	s.SetMover(mover)
	plan := readPlan(t, s, batch.ID, ReviewPlanQuery{})

	start := make(chan struct{})
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, errs[i] = s.FileReviewPlan(t.Context(), "local", "ws-1", batch.ID, FileRequest{
				ExpectedRevision: plan.Revision, ExpectedGroupRevision: plan.GroupRevision,
				PlanDigest: plan.PlanDigest, ClientKey: fmt.Sprintf("two-windows-%010d", i),
			})
		}()
	}
	close(start)
	wg.Wait()
	won, refused := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			won++
		case errors.Is(err, ErrReviewPlanChanged):
			refused++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if won != 1 || refused != 1 || mover.calls != 3 {
		t.Fatalf("one press files, the other is refused: won=%d refused=%d moves=%d", won, refused, mover.calls)
	}
}
