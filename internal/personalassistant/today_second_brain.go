package personalassistant

import (
	"context"
	"fmt"
	"time"

	"github.com/johnjallday/ori-agent/internal/secondbrain"
)

// todaySecondBrainDoneWindow is how long a finished look stays under Done
// (second-brain FR32).
const todaySecondBrainDoneWindow = 7 * 24 * time.Hour

// secondBrainItems places the connected notes folder's look on Today: Working
// on while a look runs, Needs you while a finished result waits for a
// decision (the drawer card renders it there itself), and Done for a week. A
// notes folder that cannot be read leaves Today as it was; the Notes slot says
// so instead. The result row leads to the HQ map with the Notes panel open.
func (s *TodayService) secondBrainItems(ctx context.Context, userID, hqSlug string, now time.Time) (working, needs, done []TodayItem) {
	if s.secondBrain == nil {
		return nil, nil, nil
	}
	view, err := s.secondBrain.Status(ctx, userID)
	if err != nil || view.Source == nil {
		return nil, nil, nil
	}
	name := view.Source.Name
	// The nudge waits while a look runs or a result card waits: either already
	// answers "look at your notes again".
	nudgeAllowed := false
	if look, err := s.secondBrain.Look(ctx, userID); err == nil {
		switch {
		case look.State == secondbrain.LookRunning:
			working = append(working, TodayItem{ID: "second-brain-look-" + look.LookID, Kind: "second_brain_look", Title: "Reading " + name + "…"})
		case look.State == secondbrain.LookFinished && view.LastLook != nil && !view.LastLook.Acted && look.LookID == view.LastLook.ID:
			needs = append(needs, TodayItem{ID: view.LastLook.ID, Kind: "second_brain_result",
				Title: "What's open in " + name, Detail: view.LastLook.Headline, SourceAt: view.LastLook.At, Route: notesPanelRoute(hqSlug)})
		default:
			nudgeAllowed = true
		}
	}
	if reader, ok := s.secondBrain.(secondBrainNudgeReader); ok && nudgeAllowed {
		if report, found := reader.Nudge(ctx, userID); found {
			needs = append(needs, TodayItem{ID: "second-brain-stale-" + report.LookID, Kind: "second_brain_stale",
				Title: secondBrainNudgeTitle(report), SourceAt: report.LookAt, Route: notesPanelRoute(hqSlug), Actions: []string{"not_now"}})
		}
	}
	if last := view.LastLook; last != nil && !last.At.IsZero() && !last.At.After(now) && now.Sub(last.At) <= todaySecondBrainDoneWindow {
		items := "open items"
		if last.OpenItems == 1 {
			items = "open item"
		}
		done = append(done, TodayItem{ID: "second-brain-" + last.ID, Kind: "second_brain_done",
			Title: fmt.Sprintf("Looked at %s · %d %s", name, last.OpenItems, items), SourceAt: last.At})
	}
	if reporter, ok := s.secondBrain.(secondBrainWrittenReporter); ok {
		if report, found := reporter.WrittenReport(ctx, userID); found {
			done = append(done, TodayItem{ID: "second-brain-written-" + report.At.UTC().Format("20060102T150405"), Kind: "second_brain_written",
				Title: report.Sentence, SourceAt: report.At, Route: notesPanelRoute(hqSlug)})
		}
	}
	if reporter, ok := s.secondBrain.(secondBrainWeekReporter); ok {
		if report, found := reporter.WeekReport(ctx, userID); found {
			done = append(done, TodayItem{ID: report.ID, Kind: "second_brain_week",
				Title: report.Sentence, SourceAt: report.At, Route: notesPanelRoute(hqSlug), Actions: []string{"dismiss"}})
		}
	}
	return working, needs, done
}

// secondBrainNudgeReader is the part of the service that says the notes deserve
// another look (notes-station-panel FR21).
type secondBrainNudgeReader interface {
	Nudge(ctx context.Context, userID string) (secondbrain.NudgeReport, bool)
}

// secondBrainNudgeTitle says why the notes deserve another look: their age, the
// dates that came due since, or both.
func secondBrainNudgeTitle(report secondbrain.NudgeReport) string {
	days := "days"
	if report.Days == 1 {
		days = "day"
	}
	items := "items"
	if report.CameDue == 1 {
		items = "item"
	}
	switch {
	case report.Stale && report.CameDue > 0:
		return fmt.Sprintf("Your notes were last read %d %s ago · %d %s came due", report.Days, days, report.CameDue, items)
	case report.Stale:
		return fmt.Sprintf("Your notes were last read %d %s ago", report.Days, days)
	default:
		return fmt.Sprintf("%d %s in your notes came due", report.CameDue, items)
	}
}

// secondBrainWrittenReporter is the part of the service that says what the fill-in
// wrote since the row was last seen (FR9).
type secondBrainWrittenReporter interface {
	WrittenReport(ctx context.Context, userID string) (SecondBrainWrittenReport, bool)
}

// secondBrainWeekReporter is the part that says what the first week gained
// (first-week FR25).
type secondBrainWeekReporter interface {
	WeekReport(ctx context.Context, userID string) (SecondBrainWeekReport, bool)
}

// notesPanelRoute opens the Notes station's panel on the HQ map, or "" when the
// slug is not one a route can carry.
func notesPanelRoute(hqSlug string) string {
	if !todaySafeSlug.MatchString(hqSlug) {
		return ""
	}
	return "/workspaces/" + hqSlug + "?station=second-brain"
}
