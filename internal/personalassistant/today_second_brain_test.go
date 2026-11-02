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

type fakeTodayNotesWithReport struct {
	fakeTodayNotes
	report SecondBrainWrittenReport
	found  bool
}

func (f fakeTodayNotesWithReport) WrittenReport(context.Context, string) (SecondBrainWrittenReport, bool) {
	return f.report, f.found
}

type fakeTodayNotesWithNudge struct {
	fakeTodayNotes
	report secondbrain.NudgeReport
}

func (f fakeTodayNotesWithNudge) Nudge(context.Context, string) (secondbrain.NudgeReport, bool) {
	return f.report, f.report.Any()
}

func TestTodaySecondBrain_StaleNotesNudge(t *testing.T) {
	now := time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)
	lookAt := now.Add(-8 * 24 * time.Hour)
	view := SecondBrainView{Status: SecondBrainConnected, Source: &SecondBrainSourceView{Name: "Second Brain"},
		LastLook: &SecondBrainLookSummary{ID: "look-1", At: lookAt, OpenItems: 4, Acted: true}}
	finished := SecondBrainLookView{State: secondbrain.LookFinished, LookID: "look-1"}

	for name, tc := range map[string]struct {
		report secondbrain.NudgeReport
		title  string
	}{
		"stale":        {secondbrain.NudgeReport{Stale: true, Days: 8}, "Your notes were last read 8 days ago"},
		"came due":     {secondbrain.NudgeReport{CameDue: 1}, "1 item in your notes came due"},
		"several due":  {secondbrain.NudgeReport{CameDue: 3}, "3 items in your notes came due"},
		"both":         {secondbrain.NudgeReport{Stale: true, Days: 8, CameDue: 2}, "Your notes were last read 8 days ago · 2 items came due"},
		"one day long": {secondbrain.NudgeReport{Stale: true, Days: 1}, "Your notes were last read 1 day ago"},
	} {
		t.Run(name, func(t *testing.T) {
			tc.report.LookID, tc.report.LookAt = "look-1", lookAt
			s := &TodayService{secondBrain: fakeTodayNotesWithNudge{fakeTodayNotes{view: view, look: finished}, tc.report}}
			_, needs, _ := s.secondBrainItems(context.Background(), "local", "my-hq", now)
			if len(needs) != 1 {
				t.Fatalf("needs = %+v", needs)
			}
			got := needs[0]
			if got.Kind != "second_brain_stale" || got.Title != tc.title || got.ID != "second-brain-stale-look-1" ||
				got.Route != "/workspaces/my-hq?station=second-brain" || !got.SourceAt.Equal(lookAt) ||
				len(got.Actions) != 1 || got.Actions[0] != "not_now" {
				t.Fatalf("nudge = %+v", got)
			}
		})
	}

	t.Run("an unacted result card wins over the nudge", func(t *testing.T) {
		waiting := view
		summary := *view.LastLook
		summary.Acted = false
		waiting.LastLook = &summary
		s := &TodayService{secondBrain: fakeTodayNotesWithNudge{fakeTodayNotes{view: waiting, look: finished}, secondbrain.NudgeReport{Stale: true, Days: 8}}}
		_, needs, _ := s.secondBrainItems(context.Background(), "local", "my-hq", now)
		if len(needs) != 1 || needs[0].Kind != "second_brain_result" {
			t.Fatalf("needs = %+v, want only the result card", needs)
		}
	})

	t.Run("a running look holds the nudge back", func(t *testing.T) {
		s := &TodayService{secondBrain: fakeTodayNotesWithNudge{fakeTodayNotes{view: view, look: SecondBrainLookView{State: secondbrain.LookRunning, LookID: "look-2"}}, secondbrain.NudgeReport{Stale: true, Days: 8}}}
		if _, needs, _ := s.secondBrainItems(context.Background(), "local", "my-hq", now); len(needs) != 0 {
			t.Fatalf("needs = %+v", needs)
		}
	})

	t.Run("nothing to nudge about is no row", func(t *testing.T) {
		s := &TodayService{secondBrain: fakeTodayNotesWithNudge{fakeTodayNotes{view: view, look: finished}, secondbrain.NudgeReport{}}}
		if _, needs, _ := s.secondBrainItems(context.Background(), "local", "my-hq", now); len(needs) != 0 {
			t.Fatalf("needs = %+v", needs)
		}
	})
}

func TestTodaySecondBrain_WorkingNeedsAndDone(t *testing.T) {
	now := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	connected := SecondBrainView{Status: SecondBrainConnected, Source: &SecondBrainSourceView{Name: "Second Brain"}}

	t.Run("a running look is Working on", func(t *testing.T) {
		s := &TodayService{secondBrain: fakeTodayNotes{view: connected, look: SecondBrainLookView{State: secondbrain.LookRunning, LookID: "look-2"}}}
		working, needs, done := s.secondBrainItems(context.Background(), "local", "my-hq", now)
		if len(working) != 1 || working[0].Title != "Reading Second Brain…" || working[0].Kind != "second_brain_look" || len(needs) != 0 || len(done) != 0 {
			t.Fatalf("working %+v needs %+v done %+v", working, needs, done)
		}
	})

	t.Run("a result waiting for a decision is Needs you, and Done for a week", func(t *testing.T) {
		view := connected
		view.LastLook = &SecondBrainLookSummary{ID: "look-1", At: now.Add(-time.Hour), OpenItems: 12, Headline: "12 open items across 6 notes, 4 with dates."}
		s := &TodayService{secondBrain: fakeTodayNotes{view: view, look: SecondBrainLookView{State: secondbrain.LookFinished, LookID: "look-1"}}}
		_, needs, done := s.secondBrainItems(context.Background(), "local", "my-hq", now)
		if len(needs) != 1 || needs[0].Kind != "second_brain_result" || needs[0].Detail != view.LastLook.Headline ||
			needs[0].Route != "/workspaces/my-hq?station=second-brain" {
			t.Fatalf("needs = %+v", needs)
		}
		if got := notesPanelRoute("../elsewhere"); got != "" {
			t.Fatalf("an unsafe slug made a route: %q", got)
		}
		if len(done) != 1 || done[0].Title != "Looked at Second Brain · 12 open items" {
			t.Fatalf("done = %+v", done)
		}

		view.LastLook.Acted = true
		s = &TodayService{secondBrain: fakeTodayNotes{view: view, look: SecondBrainLookView{State: secondbrain.LookFinished, LookID: "look-1"}}}
		if _, needs, _ := s.secondBrainItems(context.Background(), "local", "my-hq", now); len(needs) != 0 {
			t.Fatalf("an acted result still needs the user: %+v", needs)
		}
		if _, _, done := s.secondBrainItems(context.Background(), "local", "my-hq", now.Add(8*24*time.Hour)); len(done) != 0 {
			t.Fatalf("a week-old look is still Done: %+v", done)
		}
	})

	t.Run("what the fill-in wrote is one Done row that leads to the Notes panel", func(t *testing.T) {
		wrote := fakeTodayNotesWithReport{
			fakeTodayNotes: fakeTodayNotes{view: connected},
			report:         SecondBrainWrittenReport{Sentence: "Ori added 2 people and a project to your notes", At: now.Add(-time.Minute)},
			found:          true,
		}
		s := &TodayService{secondBrain: wrote}
		_, _, done := s.secondBrainItems(context.Background(), "local", "my-hq", now)
		if len(done) != 1 || done[0].Kind != "second_brain_written" || done[0].Title != wrote.report.Sentence ||
			done[0].Route != "/workspaces/my-hq?station=second-brain" || done[0].ID == "" {
			t.Fatalf("done = %+v", done)
		}
		wrote.found = false
		s = &TodayService{secondBrain: wrote}
		if _, _, done := s.secondBrainItems(context.Background(), "local", "my-hq", now); len(done) != 0 {
			t.Fatalf("a row with nothing written: %+v", done)
		}
	})

	t.Run("nothing connected or unreadable leaves Today alone", func(t *testing.T) {
		for _, reader := range []fakeTodayNotes{{view: SecondBrainView{Status: SecondBrainNotConnected}}, {err: errors.New("down")}} {
			s := &TodayService{secondBrain: reader}
			if w, n, d := s.secondBrainItems(context.Background(), "local", "my-hq", now); len(w)+len(n)+len(d) != 0 {
				t.Fatalf("items = %+v %+v %+v", w, n, d)
			}
		}
		if w, n, d := (&TodayService{}).secondBrainItems(context.Background(), "local", "my-hq", now); len(w)+len(n)+len(d) != 0 {
			t.Fatal("no reader produced items")
		}
	})
}
