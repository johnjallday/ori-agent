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
// so instead.
func (s *TodayService) secondBrainItems(ctx context.Context, userID string, now time.Time) (working, needs, done []TodayItem) {
	if s.secondBrain == nil {
		return nil, nil, nil
	}
	view, err := s.secondBrain.Status(ctx, userID)
	if err != nil || view.Source == nil {
		return nil, nil, nil
	}
	name := view.Source.Name
	if look, err := s.secondBrain.Look(ctx, userID); err == nil {
		switch {
		case look.State == secondbrain.LookRunning:
			working = append(working, TodayItem{ID: "second-brain-look-" + look.LookID, Kind: "second_brain_look", Title: "Reading " + name + "…"})
		case look.State == secondbrain.LookFinished && view.LastLook != nil && !view.LastLook.Acted && look.LookID == view.LastLook.ID:
			needs = append(needs, TodayItem{ID: view.LastLook.ID, Kind: "second_brain_result",
				Title: "What's open in " + name, Detail: view.LastLook.Headline, SourceAt: view.LastLook.At})
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
	return working, needs, done
}
