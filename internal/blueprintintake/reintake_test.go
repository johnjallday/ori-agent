package blueprintintake

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestClassifyReintakeOneMovedDateYieldsExactlyOneChangedItem(t *testing.T) {
	oldQuiz := time.Date(2026, 10, 10, 17, 0, 0, 0, time.UTC)
	newQuiz := oldQuiz.AddDate(0, 0, 1)
	problemSet := time.Date(2026, 9, 1, 17, 0, 0, 0, time.UTC)
	ledger := []LedgerEntry{
		{Kind: ProposalKindTicket, Key: "quiz-2", SourceID: "syllabus", RecordID: "ticket-1", Title: "Quiz 2", DueAt: &oldQuiz},
		{Kind: ProposalKindTicket, Key: "pset-1", SourceID: "syllabus", RecordID: "ticket-2", Title: "Problem Set 1", DueAt: &problemSet},
	}
	items := []ProposalItem{
		{Kind: ProposalKindTicket, Key: "quiz-2", Title: "Quiz 2", DueAt: &newQuiz, Source: ProposalSource{SourceID: "syllabus"}},
		{Kind: ProposalKindTicket, Key: "pset-1", Title: "Problem Set 1", DueAt: &problemSet, Source: ProposalSource{SourceID: "syllabus"}},
	}
	classified := classifyReintake(items, ledger, map[string]bool{"syllabus": true})
	changed, unchanged := 0, 0
	keys := map[string]bool{}
	for _, item := range classified {
		if keys[item.Key] {
			t.Fatalf("duplicate key %q", item.Key)
		}
		keys[item.Key] = true
		switch item.Classification {
		case ProposalClassificationChanged:
			changed++
			if len(item.Changes) != 1 || item.Changes[0].Field != "due date" {
				t.Fatalf("changed fields = %+v", item.Changes)
			}
		case ProposalClassificationUnchanged:
			unchanged++
		}
	}
	if changed != 1 || unchanged != 1 || len(classified) != 2 {
		t.Fatalf("classification = %+v", classified)
	}
}

func TestClassifyReintakeNoLongerFoundIsInformationOnly(t *testing.T) {
	items := classifyReintake(nil, []LedgerEntry{{Kind: ProposalKindTicket, Key: "old", SourceID: "removed", RecordID: "ticket-1", Title: "Old"}}, map[string]bool{"removed": true})
	if len(items) != 1 || items[0].Classification != ProposalClassificationNoLongerFound || items[0].DisabledReason == "" {
		t.Fatalf("items = %+v", items)
	}
}

func TestReintakeAutomationCoalescesBurstAndQueuesOneFollowUp(t *testing.T) {
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	var calls atomic.Int32
	automation := NewReintakeAutomation(nil, nil, nil, nil)
	automation.refreshFn = func(context.Context, string, string, RefreshMode, *time.Location) (Proposal, bool, error) {
		if calls.Add(1) == 1 {
			close(firstStarted)
			<-releaseFirst
		}
		return Proposal{}, false, nil
	}
	automation.RunCoalesced("ws", "course", RefreshMode{Folders: true})
	<-firstStarted
	var burst sync.WaitGroup
	for range 20 {
		burst.Add(1)
		go func() {
			defer burst.Done()
			automation.RunCoalesced("ws", "course", RefreshMode{Folders: true})
		}()
	}
	burst.Wait()
	close(releaseFirst)
	automation.Stop()
	if got := calls.Load(); got != 2 {
		t.Fatalf("refresh calls = %d, want one active run and one coalesced follow-up", got)
	}
}
