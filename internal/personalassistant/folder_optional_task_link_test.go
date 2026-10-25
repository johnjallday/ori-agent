package personalassistant

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func TestOptionalSummaryLink_ReplayForeignAndMismatchDoNotWidenOwnership(t *testing.T) {
	r := newReportFixture(t, assessmentReport)
	ctx := context.Background()
	for _, ids := range [][2]string{{"quest-project", "foreign"}, {"foreign-workspace", "look-1"}} {
		if err := r.service.LinkOptionalFirstTask(ctx, "local", ids[0], ids[1]); !errors.Is(err, ErrFirstLookNotFound) {
			t.Fatalf("foreign linkage accepted: %v", err)
		}
	}
	if err := r.service.LinkOptionalFirstTask(ctx, "local", "quest-project", "look-1"); err != nil {
		t.Fatal(err)
	}
	if outcome := r.stored(t).Outcome; outcome.FirstTaskID != "look-1" || outcome.FirstLook != nil {
		t.Fatalf("link pretended setup started a task: %+v", outcome)
	}
	_, err := r.store.Mutate(ctx, "local", func(doc *FolderDigestDocument) error {
		doc.Offer(r.offer.ID).Outcome.FirstTaskID = "other-owned-task"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.service.LinkOptionalFirstTask(ctx, "local", "quest-project", "look-1"); !errors.Is(err, ErrFirstLookNotFound) {
		t.Fatal("mismatched association overwritten")
	}
	if err := r.actions.Dismiss(ctx, "local", "look-1"); !errors.Is(err, ErrFirstLookNotFound) {
		t.Fatalf("mismatched report authorized: %v", err)
	}
	if _, err := r.actions.StartSuggestion(ctx, "local", "look-1"); !errors.Is(err, ErrFirstLookNotFound) {
		t.Fatalf("mismatched suggestion authorized: %v", err)
	}
}

func TestOptionalSummaryLink_StartThisAndNotNowHaveOneAtomicWinner(t *testing.T) {
	r := newReportFixture(t, assessmentReport)
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		_, _ = r.actions.StartSuggestion(context.Background(), "local", "look-1")
	}()
	go func() { defer wg.Done(); <-start; _ = r.actions.Dismiss(context.Background(), "local", "look-1") }()
	close(start)
	wg.Wait()
	look := r.look(t)
	_, dismissed := look.Context[FirstLookReportDismissedKey]
	task, _ := look.Context[FirstLookSuggestionTaskKey].(string)
	if (task != "") == dismissed || len(r.started) > 1 {
		t.Fatalf("decisions raced: task=%s dismissed=%v starts=%v", task, dismissed, r.started)
	}
}

func TestOptionalSummaryLink_PreservesLegacyAutomaticReportOwnership(t *testing.T) {
	r := newReportFixture(t, assessmentReport)
	_, err := r.store.Mutate(context.Background(), "local", func(doc *FolderDigestDocument) error {
		outcome := doc.Offer(r.offer.ID).Outcome
		outcome.FirstTaskID = ""
		outcome.FirstLook = &FolderFirstLookRecord{Started: true, TaskID: "look-1", At: r.now}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.actions.hiredLook(context.Background(), "local", "look-1"); err != nil {
		t.Fatal(err)
	}
	if !r.stored(t).Outcome.FirstLook.Started {
		t.Fatal("legacy record rewritten")
	}
}
