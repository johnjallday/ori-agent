package economy

import (
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestIsRecurringCoversEveryScheduleType(t *testing.T) {
	cases := []struct {
		name     string
		schedule *workspace.ScheduleConfig
		want     bool
	}{
		{"nil schedule", nil, false},
		{"once", &workspace.ScheduleConfig{Type: workspace.ScheduleOnce}, false},
		{"interval", &workspace.ScheduleConfig{Type: workspace.ScheduleInterval, Interval: time.Hour}, true},
		{"daily", &workspace.ScheduleConfig{Type: workspace.ScheduleDaily, TimeOfDay: "09:00"}, true},
		{"weekly", &workspace.ScheduleConfig{Type: workspace.ScheduleWeekly, TimeOfDay: "09:00", DayOfWeek: 3}, true},
		{"monthly", &workspace.ScheduleConfig{Type: workspace.ScheduleMonthly, TimeOfDay: "09:00", DayOfMonth: 15}, true},
		{"cron", &workspace.ScheduleConfig{Type: workspace.ScheduleCron, CronExpr: "0 9 * * *"}, true},
		{
			"relative delay repeating",
			&workspace.ScheduleConfig{Type: workspace.ScheduleRelativeDelay, DelayDuration: 2 * time.Hour},
			true,
		},
		{
			"relative delay trigger once",
			&workspace.ScheduleConfig{Type: workspace.ScheduleRelativeDelay, DelayDuration: 2 * time.Hour, TriggerOnce: true},
			false,
		},
		{"unknown type", &workspace.ScheduleConfig{Type: workspace.ScheduleType("wishful")}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsRecurring(tc.schedule); got != tc.want {
				t.Fatalf("IsRecurring(%s) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

// A Farm is an ENABLED recurring schedule. A recurring schedule that is switched
// off is a hand-run task and earns Craft (FR7).
func TestIsFarmRequiresBothRecurringAndEnabled(t *testing.T) {
	recurring := &workspace.ScheduleConfig{Type: workspace.ScheduleDaily, TimeOfDay: "09:00"}
	once := &workspace.ScheduleConfig{Type: workspace.ScheduleOnce}

	if !IsFarm(recurring, true) {
		t.Fatal("an enabled daily schedule is a Farm")
	}
	if IsFarm(recurring, false) {
		t.Fatal("a disabled daily schedule is not a Farm")
	}
	if IsFarm(once, true) {
		t.Fatal("an enabled one-shot schedule is never a Farm")
	}
	if IsFarm(nil, true) {
		t.Fatal("no schedule is never a Farm")
	}
}

func TestTierForEachScheduleType(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name     string
		schedule *workspace.ScheduleConfig
		want     int
	}{
		{
			"interval every 15 minutes",
			&workspace.ScheduleConfig{Type: workspace.ScheduleInterval, Interval: 15 * time.Minute},
			TierQuarterHour,
		},
		{
			"interval hourly",
			&workspace.ScheduleConfig{Type: workspace.ScheduleInterval, Interval: time.Hour},
			TierHourly,
		},
		{
			"interval every 6 hours",
			&workspace.ScheduleConfig{Type: workspace.ScheduleInterval, Interval: 6 * time.Hour},
			TierEverySixHour,
		},
		{
			"interval daily",
			&workspace.ScheduleConfig{Type: workspace.ScheduleInterval, Interval: 24 * time.Hour},
			TierDaily,
		},
		{
			"interval weekly",
			&workspace.ScheduleConfig{Type: workspace.ScheduleInterval, Interval: 7 * 24 * time.Hour},
			TierWeekly,
		},
		{
			"daily at a time of day",
			&workspace.ScheduleConfig{Type: workspace.ScheduleDaily, TimeOfDay: "09:00"},
			TierDaily,
		},
		{
			"weekly on a weekday",
			&workspace.ScheduleConfig{Type: workspace.ScheduleWeekly, TimeOfDay: "09:00", DayOfWeek: 3},
			TierWeekly,
		},
		{
			// FR15 puts monthly in tier 1 explicitly. It reaches it through the
			// uncomputable-period fallback: CalculateNextRun returns the first
			// tick after now for any lastRun, so there is no second gap to
			// measure. Documented in TierFor.
			"monthly",
			&workspace.ScheduleConfig{Type: workspace.ScheduleMonthly, TimeOfDay: "09:00", DayOfMonth: 15},
			TierWeekly,
		},
		{
			"cron daily at 9",
			&workspace.ScheduleConfig{Type: workspace.ScheduleCron, CronExpr: "0 9 * * *"},
			TierDaily,
		},
		{
			"cron every 15 minutes",
			&workspace.ScheduleConfig{Type: workspace.ScheduleCron, CronExpr: "*/15 * * * *"},
			TierQuarterHour,
		},
		{
			"cron every 6 hours",
			&workspace.ScheduleConfig{Type: workspace.ScheduleCron, CronExpr: "0 */6 * * *"},
			TierEverySixHour,
		},
		{
			"relative delay every 90 minutes",
			&workspace.ScheduleConfig{Type: workspace.ScheduleRelativeDelay, DelayDuration: 90 * time.Minute},
			TierHourly,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := TierFor(tc.schedule, now); got != tc.want {
				t.Fatalf("TierFor(%s) = %d (%s), want %d (%s)",
					tc.name, got, TierName(got), tc.want, TierName(tc.want))
			}
		})
	}
}

// "Weekdays at 9" is the cron shape PRD open question 4 calls out: its gaps are
// 24h Monday through Friday and 72h across the weekend. Both land in tier 2, so
// the answer is that the tier is stable whichever day it is computed on.
func TestTierForWeekdayCronIsStableAcrossTheWeek(t *testing.T) {
	schedule := &workspace.ScheduleConfig{Type: workspace.ScheduleCron, CronExpr: "0 9 * * 1-5"}
	base := time.Date(2026, time.September, 7, 12, 0, 0, 0, time.Local) // a Monday

	for day := 0; day < 7; day++ {
		now := base.AddDate(0, 0, day)
		if got := TierFor(schedule, now); got != TierDaily {
			t.Fatalf("TierFor(weekday cron) on %s = %d (%s), want %d (Daily)",
				now.Weekday(), got, TierName(got), TierDaily)
		}
	}
}

// The boundaries are inclusive at the bottom of each tier (FR15).
func TestTierForBoundaries(t *testing.T) {
	cases := []struct {
		period time.Duration
		want   int
	}{
		{7 * 24 * time.Hour, TierWeekly},
		{7*24*time.Hour - time.Minute, TierDaily},
		{24 * time.Hour, TierDaily},
		{24*time.Hour - time.Minute, TierEverySixHour},
		{6 * time.Hour, TierEverySixHour},
		{6*time.Hour - time.Minute, TierHourly},
		{time.Hour, TierHourly},
		{time.Hour - time.Minute, TierQuarterHour},
		{time.Minute, TierQuarterHour},
	}

	now := time.Now()
	for _, tc := range cases {
		schedule := &workspace.ScheduleConfig{Type: workspace.ScheduleInterval, Interval: tc.period}
		if got := TierFor(schedule, now); got != tc.want {
			t.Fatalf("TierFor(interval %s) = %d (%s), want %d (%s)",
				tc.period, got, TierName(got), tc.want, TierName(tc.want))
		}
	}
}

// An unusable schedule must never look like a fast, cheap-to-upgrade Farm.
func TestTierForFallsBackToWeeklyWhenThePeriodIsUnknown(t *testing.T) {
	cases := []struct {
		name     string
		schedule *workspace.ScheduleConfig
	}{
		{"zero interval", &workspace.ScheduleConfig{Type: workspace.ScheduleInterval}},
		{"zero delay", &workspace.ScheduleConfig{Type: workspace.ScheduleRelativeDelay}},
		{"daily without a time", &workspace.ScheduleConfig{Type: workspace.ScheduleDaily}},
		{"cron without an expression", &workspace.ScheduleConfig{Type: workspace.ScheduleCron}},
		{"unparseable cron", &workspace.ScheduleConfig{Type: workspace.ScheduleCron, CronExpr: "not a cron"}},
		{"not recurring at all", &workspace.ScheduleConfig{Type: workspace.ScheduleOnce}},
		{"nil", nil},
	}

	now := time.Now()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := TierFor(tc.schedule, now); got != TierWeekly {
				t.Fatalf("TierFor(%s) = %d (%s), want %d (Weekly)", tc.name, got, TierName(got), TierWeekly)
			}
		})
	}
}

// A daily schedule crossing a daylight-saving transition measures 23h or 25h of
// wall clock. Both must still read as Daily.
func TestSnapCalendarPeriodAbsorbsDaylightSavingShifts(t *testing.T) {
	for _, period := range []time.Duration{23 * time.Hour, 25 * time.Hour} {
		if got := snapCalendarPeriod(period); got != 24*time.Hour {
			t.Fatalf("snapCalendarPeriod(%s) = %s, want 24h", period, got)
		}
	}
	// A weekly schedule shifts the same way.
	if got := snapCalendarPeriod(7*24*time.Hour - time.Hour); got != 7*24*time.Hour {
		t.Fatalf("snapCalendarPeriod(167h) = %s, want 168h", got)
	}
	// Sub-daily periods are reported exactly as measured.
	if got := snapCalendarPeriod(6 * time.Hour); got != 6*time.Hour {
		t.Fatalf("snapCalendarPeriod(6h) = %s, want 6h", got)
	}
}

func TestTierNameAndRunsPerDay(t *testing.T) {
	cases := []struct {
		tier       int
		name       string
		runsPerDay float64
	}{
		{TierWeekly, "Weekly", 0.14},
		{TierDaily, "Daily", 1},
		{TierEverySixHour, "Every 6 hours", 4},
		{TierHourly, "Hourly", 24},
		{TierQuarterHour, "Every 15 minutes", 96},
		{0, "", 0},
		{9, "", 0},
	}

	for _, tc := range cases {
		if got := TierName(tc.tier); got != tc.name {
			t.Fatalf("TierName(%d) = %q, want %q", tc.tier, got, tc.name)
		}
		if got := RunsPerDay(tc.tier); got != tc.runsPerDay {
			t.Fatalf("RunsPerDay(%d) = %v, want %v", tc.tier, got, tc.runsPerDay)
		}
	}
}

func TestUpgradeCostSumsEveryStepCrossed(t *testing.T) {
	cases := []struct {
		from, to int
		want     int64
	}{
		{TierDaily, TierHourly, 50},        // 20 + 30 (PRD FR19 example)
		{TierWeekly, TierDaily, 10},        // one step
		{TierWeekly, TierQuarterHour, 100}, // 10 + 20 + 30 + 40
		{TierHourly, TierQuarterHour, 40},  // one step at the top
		{TierHourly, TierDaily, 0},         // a decrease is free (FR20)
		{TierDaily, TierDaily, 0},          // no change is free
		{0, TierDaily, 0},                  // no current tier: nothing to step from
	}

	for _, tc := range cases {
		if got := UpgradeCost(tc.from, tc.to); got != tc.want {
			t.Fatalf("UpgradeCost(%d, %d) = %d, want %d", tc.from, tc.to, got, tc.want)
		}
	}
}

func TestUpgradeStepCostIsTenTimesTheTier(t *testing.T) {
	for tier := 1; tier <= 4; tier++ {
		if got, want := UpgradeStepCost(tier), int64(10*tier); got != want {
			t.Fatalf("UpgradeStepCost(%d) = %d, want %d", tier, got, want)
		}
	}
	// Tier 5 is the top of the ladder and tier 0 is not a tier: no step, no cost.
	for _, tier := range []int{0, 5, 6, -1} {
		if got := UpgradeStepCost(tier); got != 0 {
			t.Fatalf("UpgradeStepCost(%d) = %d, want 0", tier, got)
		}
	}
}
