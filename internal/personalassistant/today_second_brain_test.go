package personalassistant

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/secondbrain"
)

type fakeTodayNotes struct {
	view SecondBrainView
	look SecondBrainLookView
	err  error
}

func (f fakeTodayNotes) Status(context.Context, string) (SecondBrainView, error) {
	return f.view, f.err
}
func (f fakeTodayNotes) Look(context.Context, string) (SecondBrainLookView, error) {
	return f.look, f.err
}

func TestTodaySecondBrain_WorkingNeedsAndDone(t *testing.T) {
	now := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	connected := SecondBrainView{Status: SecondBrainConnected, Source: &SecondBrainSourceView{Name: "Second Brain"}}

	t.Run("a running look is Working on", func(t *testing.T) {
		s := &TodayService{secondBrain: fakeTodayNotes{view: connected, look: SecondBrainLookView{State: secondbrain.LookRunning, LookID: "look-2"}}}
		working, needs, done := s.secondBrainItems(context.Background(), "local", now)
		if len(working) != 1 || working[0].Title != "Reading Second Brain…" || working[0].Kind != "second_brain_look" || len(needs) != 0 || len(done) != 0 {
			t.Fatalf("working %+v needs %+v done %+v", working, needs, done)
		}
	})

	t.Run("a result waiting for a decision is Needs you, and Done for a week", func(t *testing.T) {
		view := connected
		view.LastLook = &SecondBrainLookSummary{ID: "look-1", At: now.Add(-time.Hour), OpenItems: 12, Headline: "12 open items across 6 notes, 4 with dates."}
		s := &TodayService{secondBrain: fakeTodayNotes{view: view, look: SecondBrainLookView{State: secondbrain.LookFinished, LookID: "look-1"}}}
		_, needs, done := s.secondBrainItems(context.Background(), "local", now)
		if len(needs) != 1 || needs[0].Kind != "second_brain_result" || needs[0].Detail != view.LastLook.Headline {
			t.Fatalf("needs = %+v", needs)
		}
		if len(done) != 1 || done[0].Title != "Looked at Second Brain · 12 open items" {
			t.Fatalf("done = %+v", done)
		}

		view.LastLook.Acted = true
		s = &TodayService{secondBrain: fakeTodayNotes{view: view, look: SecondBrainLookView{State: secondbrain.LookFinished, LookID: "look-1"}}}
		if _, needs, _ := s.secondBrainItems(context.Background(), "local", now); len(needs) != 0 {
			t.Fatalf("an acted result still needs the user: %+v", needs)
		}
		if _, _, done := s.secondBrainItems(context.Background(), "local", now.Add(8*24*time.Hour)); len(done) != 0 {
			t.Fatalf("a week-old look is still Done: %+v", done)
		}
	})

	t.Run("nothing connected or unreadable leaves Today alone", func(t *testing.T) {
		for _, reader := range []fakeTodayNotes{{view: SecondBrainView{Status: SecondBrainNotConnected}}, {err: errors.New("down")}} {
			s := &TodayService{secondBrain: reader}
			if w, n, d := s.secondBrainItems(context.Background(), "local", now); len(w)+len(n)+len(d) != 0 {
				t.Fatalf("items = %+v %+v %+v", w, n, d)
			}
		}
		if w, n, d := (&TodayService{}).secondBrainItems(context.Background(), "local", now); len(w)+len(n)+len(d) != 0 {
			t.Fatal("no reader produced items")
		}
	})
}
