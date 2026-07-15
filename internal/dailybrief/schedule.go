package dailybrief

import (
	"fmt"
	"time"
)

// dayIndex maps a ScheduleDays code to time.Weekday.
var dayIndex = map[string]time.Weekday{
	"sun": time.Sunday,
	"mon": time.Monday,
	"tue": time.Tuesday,
	"wed": time.Wednesday,
	"thu": time.Thursday,
	"fri": time.Friday,
	"sat": time.Saturday,
}

// ResolveTimezone loads an IANA location, explicitly rejecting a fallback to
// the server's local zone. This is the only timezone resolver Daily Brief
// scheduling may use — never time.Now().Location() and never
// internal/workspace.CalculateNextRun's lastRun.Location(), both of which
// are server-local rather than the user's persisted zone.
func ResolveTimezone(name string) (*time.Location, error) {
	if name == "" {
		name = "UTC"
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("%w: %q", ErrInvalidTimezone, name)
	}
	return loc, nil
}

// LocalDateKey formats t (already in the target location) as the "YYYY-MM-DD"
// key briefs and claims are keyed by.
func LocalDateKey(t time.Time) string {
	return t.Format("2006-01-02")
}

// NextOccurrence returns the next instant, strictly after `after`, at which
// cfg's schedule should fire, expressed as an absolute time.Time (safe to
// compare across zones/DST). ok is false when the schedule has no enabled
// days (nothing to compute).
//
// DST policy (documented, not just implemented — task 5.6/5.7; verified
// empirically against Go's actual time.Date behavior, not assumed):
//   - Nonexistent wall-clock time (spring-forward gap, e.g. 2:30 AM when
//     clocks jump 2:00->3:00): Go's time.Date resolves the wall-clock
//     reading using the pre-transition (standard-time) offset and does not
//     re-normalize it into the gap, so the resulting absolute instant, when
//     displayed back in local time, reads as an EARLIER standard-time
//     moment than configured (e.g. a configured 02:30 renders as 01:30 EST
//     on the transition day) rather than rolling forward into daylight
//     time. This is still exactly one well-defined instant — never an
//     error, never two firings — just not the nominal clock reading on
//     that one transition day. Every other day of the year fires at the
//     literal configured time.
//   - Repeated wall-clock time (fall-back fold): time.Date resolves to a
//     single, deterministic offset (Go picks one side of the transition
//     consistently). Because NextOccurrence only ever constructs one
//     candidate time.Time per calendar date, the ambiguity can never produce
//     two firings for the same date — "at most one per local date" holds by
//     construction, not by special-casing the fold.
//   - Timezone changes: this function is a pure computation over the
//     current cfg and `after`; it holds no cached "next run" state, so a
//     config change (or the app being off across a zone change) is picked
//     up on the very next call with no migration step required.
//   - App downtime: if `after` is far in the past, the loop below still
//     only returns the single next occurrence after it — callers seeking
//     "catch up on the current local date only" should pass `after` as a
//     recent reference point (e.g. the last known-good local midnight), not
//     rely on this function to enumerate every missed day.
func NextOccurrence(cfg Config, after time.Time) (result time.Time, ok bool, err error) {
	if !cfg.ScheduleEnabled || len(cfg.ScheduleDays) == 0 {
		return time.Time{}, false, nil
	}
	loc, err := ResolveTimezone(cfg.Timezone)
	if err != nil {
		return time.Time{}, false, err
	}
	hour, minute, err := parseScheduleTime(cfg.ScheduleTime)
	if err != nil {
		return time.Time{}, false, err
	}
	enabledDays := make(map[time.Weekday]bool, len(cfg.ScheduleDays))
	for _, d := range cfg.ScheduleDays {
		if wd, ok := dayIndex[d]; ok {
			enabledDays[wd] = true
		}
	}
	if len(enabledDays) == 0 {
		return time.Time{}, false, nil
	}

	afterLocal := after.In(loc)
	// Search forward day by day (bounded to a week plus one, since the
	// schedule repeats weekly) for the first enabled weekday whose
	// configured local time is strictly after `after`.
	for offset := 0; offset <= 7; offset++ {
		day := afterLocal.AddDate(0, 0, offset)
		if !enabledDays[day.Weekday()] {
			continue
		}
		candidate := time.Date(day.Year(), day.Month(), day.Day(), hour, minute, 0, 0, loc)
		if candidate.After(afterLocal) {
			return candidate, true, nil
		}
	}
	// Unreachable when at least one day is enabled: within 7 days every
	// weekday occurs at least once strictly after `after`.
	return time.Time{}, false, nil
}

func parseScheduleTime(s string) (hour, minute int, err error) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, 0, fmt.Errorf("%w: %q", ErrInvalidScheduleTime, s)
	}
	return t.Hour(), t.Minute(), nil
}
