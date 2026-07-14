package dailybrief

import (
	"testing"
	"time"
)

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Skipf("tzdata for %s unavailable in this environment: %v", name, err)
	}
	return loc
}

// findDSTGap scans forward from start (a date in loc) for the first day
// whose UTC offset increases from the previous day (a spring-forward
// transition), returning that day's weekday and date. Scanning empirically
// (rather than hardcoding a calendar date) keeps the test correct regardless
// of which year it runs against.
func findDSTGap(t *testing.T, loc *time.Location, start time.Time) time.Time {
	t.Helper()
	prevOffset := offsetAt(start, loc)
	for i := 1; i < 400; i++ {
		day := start.AddDate(0, 0, i)
		offset := offsetAt(day, loc)
		if offset > prevOffset {
			return day
		}
		prevOffset = offset
	}
	t.Fatal("no DST spring-forward transition found within a year")
	return time.Time{}
}

// findDSTFold is findDSTGap's mirror: the first day whose UTC offset
// decreases from the previous day (a fall-back transition).
func findDSTFold(t *testing.T, loc *time.Location, start time.Time) time.Time {
	t.Helper()
	prevOffset := offsetAt(start, loc)
	for i := 1; i < 400; i++ {
		day := start.AddDate(0, 0, i)
		offset := offsetAt(day, loc)
		if offset < prevOffset {
			return day
		}
		prevOffset = offset
	}
	t.Fatal("no DST fall-back transition found within a year")
	return time.Time{}
}

func offsetAt(day time.Time, loc *time.Location) int {
	_, offset := time.Date(day.Year(), day.Month(), day.Day(), 12, 0, 0, 0, loc).Zone()
	return offset
}

func TestNextOccurrence_DisabledScheduleReturnsNotOK(t *testing.T) {
	cfg := Config{Timezone: "UTC", ScheduleDays: []string{"mon"}, ScheduleTime: "08:00", ScheduleEnabled: false}
	_, ok, err := NextOccurrence(cfg, time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("expected ok=false for a disabled schedule")
	}
}

func TestNextOccurrence_NoDaysReturnsNotOK(t *testing.T) {
	cfg := Config{Timezone: "UTC", ScheduleDays: nil, ScheduleTime: "08:00", ScheduleEnabled: true}
	_, ok, err := NextOccurrence(cfg, time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("expected ok=false with no configured days")
	}
}

func TestNextOccurrence_RejectsInvalidTimezone(t *testing.T) {
	cfg := Config{Timezone: "Not/AZone", ScheduleDays: []string{"mon"}, ScheduleTime: "08:00", ScheduleEnabled: true}
	if _, _, err := NextOccurrence(cfg, time.Now()); err == nil {
		t.Fatal("expected an error for an invalid timezone")
	}
}

func TestNextOccurrence_SameDayLaterTime(t *testing.T) {
	loc := mustLoc(t, "UTC")
	cfg := Config{Timezone: "UTC", ScheduleDays: []string{"mon"}, ScheduleTime: "08:00", ScheduleEnabled: true}
	// A Monday at 06:00 — the 08:00 occurrence is later the same day.
	after := time.Date(2026, 1, 5, 6, 0, 0, 0, loc) // 2026-01-05 is a Monday.
	got, ok, err := NextOccurrence(cfg, after)
	if err != nil || !ok {
		t.Fatalf("NextOccurrence: ok=%v err=%v", ok, err)
	}
	if got.Day() != 5 || got.Hour() != 8 {
		t.Fatalf("expected same-day 08:00, got %v", got)
	}
}

func TestNextOccurrence_PastConfiguredTimeRollsToNextEnabledDay(t *testing.T) {
	loc := mustLoc(t, "UTC")
	cfg := Config{Timezone: "UTC", ScheduleDays: []string{"mon", "wed"}, ScheduleTime: "08:00", ScheduleEnabled: true}
	// Monday at 09:00 — today's occurrence already passed; next is Wednesday.
	after := time.Date(2026, 1, 5, 9, 0, 0, 0, loc)
	got, ok, err := NextOccurrence(cfg, after)
	if err != nil || !ok {
		t.Fatalf("NextOccurrence: ok=%v err=%v", ok, err)
	}
	if got.Weekday() != time.Wednesday || got.Day() != 7 {
		t.Fatalf("expected Wednesday Jan 7, got %v (%v)", got, got.Weekday())
	}
}

func TestNextOccurrence_EveryDayOfWeekWraps(t *testing.T) {
	loc := mustLoc(t, "UTC")
	cfg := Config{Timezone: "UTC", ScheduleDays: []string{"mon"}, ScheduleTime: "08:00", ScheduleEnabled: true}
	// A Tuesday: the only enabled day (Monday) is nearly a week away.
	after := time.Date(2026, 1, 6, 0, 0, 0, 0, loc)
	got, ok, err := NextOccurrence(cfg, after)
	if err != nil || !ok {
		t.Fatalf("NextOccurrence: ok=%v err=%v", ok, err)
	}
	if got.Weekday() != time.Monday || got.Day() != 12 {
		t.Fatalf("expected the following Monday Jan 12, got %v", got)
	}
}

// TestNextOccurrence_DSTGapProducesExactlyOneWellDefinedInstant covers task
// 5.6/5.7: a nonexistent wall-clock time (spring-forward) must never error
// and must produce exactly one occurrence on the transition day — even
// though (per Go's verified time.Date behavior) it resolves to an earlier
// standard-time instant than the nominal configured clock reading, not a
// forward roll into daylight time. The load-bearing guarantees are: no
// error, no duplicate, and a subsequent call rolls to a later date.
func TestNextOccurrence_DSTGapProducesExactlyOneWellDefinedInstant(t *testing.T) {
	loc := mustLoc(t, "America/New_York")
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, loc)
	gapDay := findDSTGap(t, loc, start)

	cfg := Config{
		Timezone:        "America/New_York",
		ScheduleDays:    allDays(),
		ScheduleTime:    "02:30", // Nominally inside the spring-forward gap (2:00-3:00 AM skipped).
		ScheduleEnabled: true,
	}
	after := gapDay // midnight of the transition day itself, before the configured time
	got, ok, err := NextOccurrence(cfg, after)
	if err != nil || !ok {
		t.Fatalf("NextOccurrence: ok=%v err=%v", ok, err)
	}
	if got.In(loc).Day() != gapDay.Day() || got.In(loc).Month() != gapDay.Month() {
		t.Fatalf("expected the occurrence to land on the transition day %v, got %v", gapDay, got.In(loc))
	}

	// A subsequent call strictly after this occurrence must roll to the
	// NEXT calendar date, proving the gap did not produce a second firing
	// later the same day.
	next, ok, err := NextOccurrence(cfg, got)
	if err != nil || !ok {
		t.Fatalf("NextOccurrence (second): ok=%v err=%v", ok, err)
	}
	if next.In(loc).Day() == got.In(loc).Day() && next.In(loc).Month() == got.In(loc).Month() {
		t.Fatalf("expected the next occurrence on a different calendar date than %v, got %v", got.In(loc), next.In(loc))
	}
}

// TestNextOccurrence_DSTFoldProducesExactlyOneOccurrence covers task
// 5.6/5.7: a repeated wall-clock time (fall-back) must still yield exactly
// one occurrence for that calendar date, not two.
func TestNextOccurrence_DSTFoldProducesExactlyOneOccurrence(t *testing.T) {
	loc := mustLoc(t, "America/New_York")
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, loc)
	foldDay := findDSTFold(t, loc, start)

	cfg := Config{
		Timezone:        "America/New_York",
		ScheduleDays:    allDays(),
		ScheduleTime:    "01:30", // Occurs twice on the fall-back day (1:00-2:00 AM repeats).
		ScheduleEnabled: true,
	}
	after := foldDay // midnight of the transition day itself, before 01:30
	first, ok, err := NextOccurrence(cfg, after)
	if err != nil || !ok {
		t.Fatalf("NextOccurrence: ok=%v err=%v", ok, err)
	}
	if first.In(loc).Day() != foldDay.Day() {
		t.Fatalf("expected the first occurrence on the fold day %v, got %v", foldDay, first.In(loc))
	}
	// Asking again strictly after that first occurrence must roll to the
	// NEXT calendar day's occurrence, not a second one on the fold day —
	// proving at most one per local date holds even across the fold.
	second, ok, err := NextOccurrence(cfg, first)
	if err != nil || !ok {
		t.Fatalf("NextOccurrence (second): ok=%v err=%v", ok, err)
	}
	if second.In(loc).Day() == first.In(loc).Day() && second.In(loc).Month() == first.In(loc).Month() {
		t.Fatalf("expected the second occurrence on a different calendar date than %v, got %v", first.In(loc), second.In(loc))
	}
}

// TestNextOccurrence_TimezoneChangeTakesEffectImmediately covers task 5.7:
// NextOccurrence is a pure function of the current config, so a timezone
// change is picked up on the very next call with no migration step.
func TestNextOccurrence_TimezoneChangeTakesEffectImmediately(t *testing.T) {
	utc := mustLoc(t, "UTC")
	after := time.Date(2026, 1, 5, 0, 0, 0, 0, utc) // Monday 00:00 UTC

	cfgUTC := Config{Timezone: "UTC", ScheduleDays: []string{"mon"}, ScheduleTime: "08:00", ScheduleEnabled: true}
	gotUTC, ok, err := NextOccurrence(cfgUTC, after)
	if err != nil || !ok {
		t.Fatalf("NextOccurrence (UTC): ok=%v err=%v", ok, err)
	}

	cfgTokyo := Config{Timezone: "Asia/Tokyo", ScheduleDays: []string{"mon"}, ScheduleTime: "08:00", ScheduleEnabled: true}
	gotTokyo, ok, err := NextOccurrence(cfgTokyo, after)
	if err != nil || !ok {
		t.Fatalf("NextOccurrence (Tokyo): ok=%v err=%v", ok, err)
	}

	if gotUTC.Equal(gotTokyo) {
		t.Fatalf("expected different absolute instants for different timezones, got the same: %v", gotUTC)
	}
}

func allDays() []string {
	return []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}
}
