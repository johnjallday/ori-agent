package personalassistant

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/secondbrain"
)

// weekHoldings is one fact, one person and one project: what a first week of
// approving gives the notes.
func weekHoldings() secondbrain.Holdings {
	holdings := fillInHoldings()
	holdings.Projects = []secondbrain.HeldProject{{ID: "ws-launch", Name: "Launch", Goal: "Ship the album."}}
	return holdings
}

func captureTwice(t *testing.T, h *secondBrainHarness) {
	t.Helper()
	for _, id := range []string{"week-capture-1", "week-capture-2"} {
		if _, err := h.service.Capture(context.Background(), "local", "Call Dana "+id, id); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSecondBrainWeek_CountsTheGainsOnceSevenDaysAfterSetup(t *testing.T) {
	ctx := context.Background()
	h, _ := createdHarness(t)
	h.service.Wait()
	h.service.SetHoldings(func(context.Context, string) (secondbrain.Holdings, error) { return weekHoldings(), nil })
	if _, err := h.service.Sync(ctx, "local", "test"); err != nil {
		t.Fatal(err)
	}
	captureTwice(t, h)

	// Not before the seventh local day.
	*h.clock = h.clock.Add(5 * 24 * time.Hour)
	if _, found := h.service.WeekReport(ctx, "local"); found {
		t.Fatal("the recap came before the week was up")
	}

	*h.clock = h.clock.Add(3 * 24 * time.Hour)
	report, found := h.service.WeekReport(ctx, "local")
	want := "This week your second brain gained 1 fact, 1 person, 1 project and 2 captures."
	if !found || report.Sentence != want || report.ID == "" {
		t.Fatalf("report = %+v, %v; want %q", report, found, want)
	}

	// It is the same row on every load, and showing it again saves nothing.
	doc, err := h.service.read(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	if doc.Connected().FillIn.WeekShownAt == nil {
		t.Fatal("the first showing was not recorded")
	}
	again, found := h.service.WeekReport(ctx, "local")
	if !found || again != report {
		t.Fatalf("second load = %+v, %v", again, found)
	}
	unchanged, err := h.service.read(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Version != doc.Version {
		t.Fatalf("a second load saved the record: %d → %d", doc.Version, unchanged.Version)
	}

	// It does not nag: a week after it was first shown it is gone for good.
	*h.clock = h.clock.Add(secondbrain.WeekShowsFor + time.Hour)
	if _, still := h.service.WeekReport(ctx, "local"); still {
		t.Fatal("the recap stayed on Today past its week")
	}
}

func TestSecondBrainWeek_DismissingIsRecordedAndNeverComesBack(t *testing.T) {
	ctx := context.Background()
	h, _ := createdHarness(t)
	h.service.Wait()
	*h.clock = h.clock.Add(8 * 24 * time.Hour)
	if _, found := h.service.WeekReport(ctx, "local"); !found {
		t.Fatal("no recap after a week")
	}
	if _, err := h.service.DismissWeek(ctx, "local", "week-1"); err != nil {
		t.Fatal(err)
	}
	if _, found := h.service.WeekReport(ctx, "local"); found {
		t.Fatal("a dismissed recap came back")
	}
	doc, err := h.service.read(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	fillIn := doc.Connected().FillIn
	if fillIn.WeekDismissedAt == nil || fillIn.WeekShownAt == nil {
		t.Fatalf("the dismissal is not in the record: %+v", fillIn)
	}

	// The same request again is the same dismissal; the id is for this action only.
	if _, err := h.service.DismissWeek(ctx, "local", "week-1"); err != nil {
		t.Fatalf("replay: %v", err)
	}
	if _, err := h.service.Connect(ctx, "local", SecondBrainConnectInput{DetectedID: h.vaultID, RequestID: "week-1"}); !errors.Is(err, secondbrain.ErrRequestReused) {
		t.Fatalf("the id reused for another action: %v", err)
	}
	if _, err := h.service.DismissWeek(ctx, "local", "has a space"); !errors.Is(err, ErrValidation) {
		t.Fatalf("a bad id: %v", err)
	}
}

// Dismissing before it was ever shown (a stale tab) still ends it.
func TestSecondBrainWeek_DismissingBeforeItWasShownStillEndsIt(t *testing.T) {
	ctx := context.Background()
	h, _ := createdHarness(t)
	h.service.Wait()
	if _, err := h.service.DismissWeek(ctx, "local", "week-early"); err != nil {
		t.Fatal(err)
	}
	*h.clock = h.clock.Add(8 * 24 * time.Hour)
	if _, found := h.service.WeekReport(ctx, "local"); found {
		t.Fatal("a recap dismissed in advance still came")
	}
}

func TestSecondBrainWeek_ASetupPastItsFirstWeekIsNeverGivenARecap(t *testing.T) {
	ctx := context.Background()
	h, _ := createdHarness(t)
	h.service.Wait()
	before, err := h.service.read(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	*h.clock = h.clock.Add(secondbrain.WeekDays*24*time.Hour + secondbrain.WeekShowsFor + 48*time.Hour)
	if _, found := h.service.WeekReport(ctx, "local"); found {
		t.Fatal("a month-old setup got a 'this week' recap")
	}
	after, err := h.service.read(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	if after.Version != before.Version {
		t.Fatalf("saying nothing saved the record: %d → %d", before.Version, after.Version)
	}
}

func TestSecondBrainWeek_ASlowFactCountWaitsRatherThanSayingTheWrongNumber(t *testing.T) {
	ctx := context.Background()
	h, _ := createdHarness(t)
	h.service.Wait()
	fail := false
	h.service.SetHoldings(func(context.Context, string) (secondbrain.Holdings, error) {
		if fail {
			return secondbrain.Holdings{}, errors.New("HQ is busy")
		}
		return weekHoldings(), nil
	})
	if _, err := h.service.Sync(ctx, "local", "test"); err != nil {
		t.Fatal(err)
	}
	*h.clock = h.clock.Add(8 * 24 * time.Hour)
	fail = true
	if _, found := h.service.WeekReport(ctx, "local"); found {
		t.Fatal("a recap with an unknown fact count was shown")
	}
	doc, err := h.service.read(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	if doc.Connected().FillIn.WeekShownAt != nil {
		t.Fatal("a row that was not shown was recorded as shown")
	}
	fail = false
	if report, found := h.service.WeekReport(ctx, "local"); !found || report.Sentence == "" {
		t.Fatalf("the next load = %+v, %v", report, found)
	}
}

func TestSecondBrainWeek_ANothingWeekIsSaidPlainly(t *testing.T) {
	ctx := context.Background()
	h, _ := createdHarness(t)
	h.service.Wait()
	*h.clock = h.clock.Add(8 * 24 * time.Hour)
	report, found := h.service.WeekReport(ctx, "local")
	if !found || report.Sentence != "Your second brain had a quiet week: nothing new was added." {
		t.Fatalf("report = %+v, %v", report, found)
	}
}

// A folder the user connected is theirs: Ori did not set it up, so it has no
// first week to report.
func TestSecondBrainWeek_OnlyForASecondBrainOriSetUp(t *testing.T) {
	ctx := context.Background()
	if _, found := newSecondBrainHarness(t).service.WeekReport(ctx, "local"); found {
		t.Fatal("a recap with nothing connected")
	}
	if _, err := newSecondBrainHarness(t).service.DismissWeek(ctx, "local", "week-1"); !errors.Is(err, ErrSecondBrainNotConnected) {
		t.Fatalf("nothing connected: %v", err)
	}
	connected := newSecondBrainHarness(t)
	connected.connectVault(t)
	*connected.clock = connected.clock.Add(8 * 24 * time.Hour)
	if _, found := connected.service.WeekReport(ctx, "local"); found {
		t.Fatal("a recap for a vault Ori does not write to")
	}
	if _, err := connected.service.DismissWeek(ctx, "local", "week-2"); !errors.Is(err, ErrSecondBrainNotOwned) {
		t.Fatalf("a connected vault: %v", err)
	}
}

func TestSecondBrainWeek_TheRowReachesTodayInDoneWithADismiss(t *testing.T) {
	ctx := context.Background()
	h, _ := createdHarness(t)
	h.service.Wait()
	*h.clock = h.clock.Add(8 * 24 * time.Hour)
	today := &TodayService{secondBrain: h.service}
	_, _, done := today.secondBrainItems(ctx, "local", "personal-hq", *h.clock)
	var row *TodayItem
	for index := range done {
		if done[index].Kind == "second_brain_week" {
			row = &done[index]
		}
	}
	if row == nil {
		t.Fatalf("no second_brain_week row in Done: %+v", done)
	}
	if len(row.Actions) != 1 || row.Actions[0] != "dismiss" || row.Route != "/workspaces/personal-hq?station=second-brain" || row.Title == "" {
		t.Fatalf("row = %+v", *row)
	}
}
