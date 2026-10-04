package personalassistant

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestKnowledgeInterviewRowReceiptsAndExplicitCompletionAreIdempotent(t *testing.T) {
	f := newKnowledgeFixture(t)
	ctx := context.Background()
	s := NewKnowledgeInterviewService(NewKnowledgeStore(f.resolver(), f.folder))
	if _, err := s.Offer(ctx, "local"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Complete(ctx, "local", "save-1", []string{"priority"}); err == nil {
		t.Fatal("interview completed without a saved canonical row receipt")
	}
	receipt := KnowledgeInterviewRowReceipt{
		RequestID: "save-1", RowID: "priority", TextHash: hashKnowledgeLine("Reviewed priority"),
		CanonicalRef: "memory:item-1",
	}
	if err := s.RecordSavedRow(ctx, "local", receipt); err != nil {
		t.Fatal(err)
	}
	doc, err := s.store.Read(ctx, "local")
	if err != nil || len(doc.Interview.RowReceipts) != 1 || doc.Interview.RowReceipts[0].Status != "saved" {
		t.Fatalf("row receipt not durable: %+v %v", doc, err)
	}
	if err := s.RecordSavedRow(ctx, "local", receipt); err != nil {
		t.Fatalf("row replay failed: %v", err)
	}
	changed := receipt
	changed.TextHash = hashKnowledgeLine("Unreviewed different text")
	if err := s.RecordSavedRow(ctx, "local", changed); err == nil {
		t.Fatal("retry key accepted different reviewed text")
	}
	completed, err := s.Complete(ctx, "local", "save-1", []string{"priority"})
	if err != nil || completed.Status != KnowledgeInterviewCompleted || completed.CompletedAt.IsZero() {
		t.Fatalf("explicit completion=%+v %v", completed, err)
	}
	restarted := NewKnowledgeInterviewService(NewKnowledgeStore(f.resolver(), f.folder))
	if _, err := restarted.Complete(ctx, "local", "save-1", []string{"priority"}); err != nil {
		t.Fatalf("completion replay failed: %v", err)
	}
	if err := restarted.RecordSavedRow(ctx, "local", receipt); err != nil {
		t.Fatalf("saved row retry after lost completion response failed: %v", err)
	}
	if _, err := restarted.Complete(ctx, "local", "save-1", nil); err == nil {
		t.Fatal("same retry key accepted a different selected-row set")
	}
	if _, err := restarted.Complete(ctx, "local", "save-2", []string{"priority"}); err == nil {
		t.Fatal("different request repeated a completed interview")
	}
	memory, err := f.memory.Read("hq-local")
	if err != nil || len(memory.Entries()) != 0 {
		t.Fatalf("receipts invented canonical memory: %+v %v", memory.Entries(), err)
	}
}

func TestKnowledgeInterviewPriorityHintUsesPlainFocusAreaWords(t *testing.T) {
	// Written out rather than taken from the package constant, so a change to
	// the wording has to be made on purpose in both places.
	const fallback = "Name one project or goal, for example “Launch the portfolio site by March”. I'll keep it in mind when I help you."
	tests := []struct {
		name  string
		areas []FocusArea
		want  string
	}{
		{"plan my day", []FocusArea{FocusPlanMyDay}, "You mentioned planning your day. " + fallback},
		{"commitments", []FocusArea{FocusTrackCommitments}, "You mentioned tracking commitments and follow-ups. " + fallback},
		{"meetings", []FocusArea{FocusPrepareForMeetings}, "You mentioned preparing for meetings. " + fallback},
		{"projects", []FocusArea{FocusKeepProjectsMoving}, "You mentioned keeping projects moving. " + fallback},
		{"email", []FocusArea{FocusHelpWithEmail}, "You mentioned help with email. " + fallback},
		{"songs", []FocusArea{FocusTrackSongsInProgress}, "You mentioned tracking songs in progress. " + fallback},
		{"handoffs", []FocusArea{FocusChaseCollaboratorHandoffs}, "You mentioned chasing collaborator handoffs. " + fallback},
		{"release dates", []FocusArea{FocusKeepReleaseDatesVisible}, "You mentioned keeping release dates visible. " + fallback},
		{"project files", []FocusArea{FocusOrganizeProjectFiles}, "You mentioned organizing project files. " + fallback},
		{"first area wins", []FocusArea{FocusHelpWithEmail, FocusPlanMyDay}, "You mentioned help with email. " + fallback},
		{"something else", []FocusArea{FocusSomethingElse}, fallback},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newKnowledgeFixture(t)
			ctx := context.Background()
			state, err := f.relationships.GetState(ctx, "local")
			if err != nil {
				t.Fatal(err)
			}
			state.FocusAreas = tc.areas
			if _, err := f.relationships.UpdateState(ctx, state, state.StateVersion); err != nil {
				t.Fatal(err)
			}
			s := NewKnowledgeInterviewService(NewKnowledgeStore(f.resolver(), f.folder))
			questions, err := s.Questions(ctx, "local")
			if err != nil || len(questions) == 0 {
				t.Fatalf("questions=%+v %v", questions, err)
			}
			if got := questions[0].Hint; got != tc.want {
				t.Fatalf("hint = %q, want %q", got, tc.want)
			}
			// A question the user cannot picture an answer to gets skipped, so
			// every question has to show what an answer looks like.
			for _, question := range questions {
				if !strings.Contains(question.Hint, "for example “") && !strings.Contains(question.Hint, "For example “") {
					t.Fatalf("question %q has no example answer in its hint %q", question.ID, question.Hint)
				}
			}
		})
	}
	t.Run("working agreement when no focus area is set", func(t *testing.T) {
		f := newKnowledgeFixture(t)
		ctx := context.Background()
		state, err := f.relationships.GetState(ctx, "local")
		if err != nil {
			t.Fatal(err)
		}
		state.FocusAreas = nil
		state.Mandate = "Keep my priorities visible."
		if _, err := f.relationships.UpdateState(ctx, state, state.StateVersion); err != nil {
			t.Fatal(err)
		}
		s := NewKnowledgeInterviewService(NewKnowledgeStore(f.resolver(), f.folder))
		questions, err := s.Questions(ctx, "local")
		if err != nil || len(questions) == 0 {
			t.Fatalf("questions=%+v %v", questions, err)
		}
		want := "Your working agreement says “Keep my priorities visible”. " + fallback
		if got := questions[0].Hint; got != want {
			t.Fatalf("hint = %q, want %q", got, want)
		}
	})
	// The state store rejects unknown focus areas, so the fallback for one is
	// checked on the lookup the hint is built from.
	for _, area := range []FocusArea{FocusSomethingElse, FocusArea("plan_my_decade"), FocusArea("")} {
		if phrase, ok := focusAreaPhrases[area]; ok {
			t.Fatalf("focus area %q has a hint phrase %q; it must use the default hint", area, phrase)
		}
	}
}

func TestKnowledgeInterviewIsReadOnlyUntilOfferedAndDeferSurvivesRestart(t *testing.T) {
	f := newKnowledgeFixture(t)
	ctx := context.Background()
	s := NewKnowledgeInterviewService(NewKnowledgeStore(f.resolver(), f.folder))
	if state, err := s.Read(ctx, "local"); err != nil || state != nil {
		t.Fatalf("initial interview=%+v %v", state, err)
	}
	doc, err := s.store.Read(ctx, "local")
	if err != nil || doc.Present {
		t.Fatalf("GET created interview metadata: %+v %v", doc, err)
	}
	questions, err := s.Questions(ctx, "local")
	if err != nil || len(questions) != 3 || questions[0].ID != "priority" || questions[1].ID != "communication" || questions[2].ID != "person_or_routine" {
		t.Fatalf("questions=%+v %v", questions, err)
	}
	at := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return at }
	offered, err := s.Offer(ctx, "local")
	if err != nil || offered.Status != KnowledgeInterviewOffered || !offered.OfferedAt.Equal(at) || len(offered.RowReceipts) != 0 {
		t.Fatalf("offer=%+v %v", offered, err)
	}
	deferred, err := s.Defer(ctx, "local")
	if err != nil || deferred.Status != KnowledgeInterviewDeferred || !deferred.OfferedAt.Equal(at) {
		t.Fatalf("defer=%+v %v", deferred, err)
	}
	restart := NewKnowledgeInterviewService(NewKnowledgeStore(f.resolver(), f.folder))
	again, err := restart.Offer(ctx, "local")
	if err != nil || again.Status != KnowledgeInterviewDeferred {
		t.Fatalf("restart replayed an interview offer: %+v %v", again, err)
	}
	doc, err = restart.store.Read(ctx, "local")
	if err != nil || doc.Version != 2 {
		t.Fatalf("replayed offer changed durable state: %+v %v", doc, err)
	}
	memory, err := f.memory.Read("hq-local")
	if err != nil || len(memory.Entries()) != 0 {
		t.Fatalf("offering interview saved a fact: %+v %v", memory.Entries(), err)
	}
	if _, err := restart.Questions(ctx, "foreign"); err == nil {
		t.Fatal("foreign relationship read interview hints")
	}
}
