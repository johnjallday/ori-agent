package economy

import (
	"math"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Cadence tiers (FR15). A Farm always sits on exactly one of them, derived from
// its schedule's period every time it is needed and never stored (FR16), so a
// schedule edited anywhere in the app cannot leave a stale tier behind.
const (
	TierWeekly       = 1 // P >= 7 days (monthly lands here)
	TierDaily        = 2 // 24h <= P < 7 days
	TierEverySixHour = 3 // 6h <= P < 24h
	TierHourly       = 4 // 1h <= P < 6h
	TierQuarterHour  = 5 // P < 1h
)

// dstSnapFloor is the shortest wall-clock period that gets snapped to whole days
// before it is tiered.
//
// A calendar schedule ("every day at 09:00") is built with time.Date, which
// preserves the wall clock across a daylight-saving transition — so the real gap
// between two runs is 23h or 25h on the two days a year a transition happens.
// Without snapping, a Daily Farm would read as tier 3 on those days, its badge
// would change, and an upgrade priced off it would be wrong. Periods shorter
// than this are left exactly as measured.
const dstSnapFloor = 20 * time.Hour

// IsRecurring reports whether a schedule makes a task a Farm candidate (FR14).
// It defers to workspace.IsRecurringSchedule, which the task executor also uses,
// so "recurring" means one thing everywhere.
func IsRecurring(schedule *workspace.ScheduleConfig) bool {
	return workspace.IsRecurringSchedule(schedule)
}

// IsFarm reports whether a task with this schedule and enabled flag is a Farm:
// an enabled, recurring schedule (FR7). A task with a recurring schedule that is
// switched off is not a Farm and earns Craft like any hand-run task.
func IsFarm(schedule *workspace.ScheduleConfig, scheduleEnabled bool) bool {
	return scheduleEnabled && IsRecurring(schedule)
}

// TierFor maps a recurring schedule to its cadence tier (FR15).
//
// Interval and relative-delay schedules carry their period as a duration, so
// they are read directly. Every other type's period is the gap between its next
// two runs, computed with workspace.CalculateNextRun — cron is not reimplemented
// here, and neither is the day-of-month clamping behind monthly schedules.
//
// A period that cannot be computed falls back to tier 1, the cheapest and
// slowest tier, so an unparseable schedule can never make an upgrade look free.
// Monthly deliberately lands there too: CalculateNextRun always returns the
// first tick after now for a monthly schedule regardless of the last run it is
// given, so there is no second run to measure a gap against — and FR15 puts
// monthly in tier 1 anyway.
func TierFor(schedule *workspace.ScheduleConfig, now time.Time) int {
	if !IsRecurring(schedule) {
		return TierWeekly
	}

	period, ok := periodFor(schedule, now)
	if !ok || period <= 0 {
		return TierWeekly
	}

	switch {
	case period >= 7*24*time.Hour:
		return TierWeekly
	case period >= 24*time.Hour:
		return TierDaily
	case period >= 6*time.Hour:
		return TierEverySixHour
	case period >= time.Hour:
		return TierHourly
	default:
		return TierQuarterHour
	}
}

// periodFor resolves a recurring schedule's period P. The bool is false when no
// period can be established, which the caller treats as tier 1.
func periodFor(schedule *workspace.ScheduleConfig, now time.Time) (time.Duration, bool) {
	switch schedule.Type {
	case workspace.ScheduleInterval:
		if schedule.Interval <= 0 {
			return 0, false
		}
		return schedule.Interval, true
	case workspace.ScheduleRelativeDelay:
		if schedule.DelayDuration <= 0 {
			return 0, false
		}
		return schedule.DelayDuration, true
	}

	// Calendar and cron schedules: measure the gap between the next two runs.
	first := workspace.CalculateNextRun(*schedule, now)
	if first == nil {
		return 0, false
	}
	second := workspace.CalculateNextRun(*schedule, *first)
	if second == nil || !second.After(*first) {
		return 0, false
	}
	return snapCalendarPeriod(second.Sub(*first)), true
}

// snapCalendarPeriod rounds a wall-clock period to whole days once it is long
// enough for a daylight-saving transition to have shifted it. See dstSnapFloor.
func snapCalendarPeriod(period time.Duration) time.Duration {
	if period < dstSnapFloor {
		return period
	}
	days := math.Round(period.Hours() / 24)
	if days < 1 {
		return period
	}
	return time.Duration(days) * 24 * time.Hour
}

// TierName is the label the UI shows for a tier: the Farm badge's title, the
// price line's "Daily -> Hourly", and the upgrade toast all read from here.
func TierName(tier int) string {
	switch tier {
	case TierWeekly:
		return "Weekly"
	case TierDaily:
		return "Daily"
	case TierEverySixHour:
		return "Every 6 hours"
	case TierHourly:
		return "Hourly"
	case TierQuarterHour:
		return "Every 15 minutes"
	default:
		return ""
	}
}

// RunsPerDay is the canonical run count for a tier, used by the price line to
// show what an upgrade actually buys (FR39). It reads from the tier rather than
// from the schedule so "Runs per day: 1 -> 24" matches the tier names beside it
// instead of drifting a few percent from them.
//
// Weekly reports a fraction rather than rounding to zero, because "0 runs a day"
// would read as a broken Farm.
func RunsPerDay(tier int) float64 {
	switch tier {
	case TierWeekly:
		return round2(1.0 / 7.0)
	case TierDaily:
		return 1
	case TierEverySixHour:
		return 4
	case TierHourly:
		return 24
	case TierQuarterHour:
		return 96
	default:
		return 0
	}
}

func round2(value float64) float64 {
	return math.Round(value*100) / 100
}
