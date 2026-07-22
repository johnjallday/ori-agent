package workspace

import (
	"errors"
	"testing"
	"time"
)

// TestExecuteTaskSchedule_RejectsBacklogTask covers task-list 1.9: a Backlog
// task must never be reset/queued by the schedule-execution path, even if it
// somehow carries schedule fields (defense in depth alongside
// ValidateBacklogTaskInvariants, which normally prevents this state).
func TestExecuteTaskSchedule_RejectsBacklogTask(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer func() { _ = store.Close() }()

	ws := memberWorkspace("Writer")
	ws.ID = "ws-backlog-sched"
	ws.Status = StatusActive
	task := Task{
		ID:              "t1",
		Status:          TaskStatusBacklog,
		To:              "Writer",
		ScheduleEnabled: true,
		Schedule:        &ScheduleConfig{Type: ScheduleDaily},
	}
	ws.Tasks = []Task{task}
	if err := store.Save(ws); err != nil {
		t.Fatalf("save workspace: %v", err)
	}

	ts := NewTaskScheduler(store, SchedulerConfig{})
	loaded, err := store.Get(ws.ID)
	if err != nil {
		t.Fatalf("get workspace: %v", err)
	}
	got, err := loaded.GetTask("t1")
	if err != nil {
		t.Fatalf("get task: %v", err)
	}

	ts.executeTaskSchedule(loaded, got, time.Now())

	after, err := store.Get(ws.ID)
	if err != nil {
		t.Fatalf("get workspace after: %v", err)
	}
	afterTask, err := after.GetTask("t1")
	if err != nil {
		t.Fatalf("get task after: %v", err)
	}
	if afterTask.Status != TaskStatusBacklog {
		t.Fatalf("Status = %q, want unchanged Backlog", afterTask.Status)
	}
	if afterTask.FailureCount == 0 {
		t.Fatalf("expected the rejection to be recorded as a schedule failure")
	}
}

// TestRerunTargetTask_RejectsBacklogTask mirrors the above for the legacy
// ScheduledTask entity path (rerunTargetTask).
func TestRerunTargetTask_RejectsBacklogTask(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer func() { _ = store.Close() }()

	ws := memberWorkspace("Writer")
	ws.ID = "ws-backlog-rerun"
	ws.Status = StatusActive
	ws.Tasks = []Task{{ID: "t1", Status: TaskStatusBacklog, To: "Writer"}}
	if err := store.Save(ws); err != nil {
		t.Fatalf("save workspace: %v", err)
	}

	ts := NewTaskScheduler(store, SchedulerConfig{})
	loaded, err := store.Get(ws.ID)
	if err != nil {
		t.Fatalf("get workspace: %v", err)
	}
	st := &ScheduledTask{ID: "st1", WorkspaceID: ws.ID, TargetTaskID: "t1"}

	ts.rerunTargetTask(loaded, st, time.Now())

	after, err := store.Get(ws.ID)
	if err != nil {
		t.Fatalf("get workspace after: %v", err)
	}
	afterTask, err := after.GetTask("t1")
	if err != nil {
		t.Fatalf("get task after: %v", err)
	}
	if afterTask.Status != TaskStatusBacklog {
		t.Fatalf("Status = %q, want unchanged Backlog", afterTask.Status)
	}
}

func TestRequireTaskNotBacklog_UsedByScheduler(t *testing.T) {
	// Sanity check that the shared guard is wired with the sentinel error the
	// rest of the package expects (coordinator/executor share this check).
	task := &Task{ID: "t", Status: TaskStatusBacklog}
	if err := RequireTaskNotBacklog(task, "cannot schedule task"); !errors.Is(err, ErrBacklogTaskNotRunnable) {
		t.Fatalf("expected ErrBacklogTaskNotRunnable, got %v", err)
	}
}

// TestValidateCronExpression tests cron expression validation
func TestValidateCronExpression(t *testing.T) {
	tests := []struct {
		name    string
		expr    string
		wantErr bool
	}{
		{
			name:    "valid daily at 9am",
			expr:    "0 9 * * *",
			wantErr: false,
		},
		{
			name:    "valid every 5 minutes",
			expr:    "*/5 * * * *",
			wantErr: false,
		},
		{
			name:    "valid weekdays at 2pm",
			expr:    "0 14 * * 1-5",
			wantErr: false,
		},
		{
			name:    "valid every hour",
			expr:    "0 * * * *",
			wantErr: false,
		},
		{
			name:    "empty expression",
			expr:    "",
			wantErr: true,
		},
		{
			name:    "invalid format - too few fields",
			expr:    "0 9 * *",
			wantErr: true,
		},
		{
			name:    "invalid format - too many fields",
			expr:    "0 0 9 * * * *",
			wantErr: true,
		},
		{
			name:    "invalid minute value",
			expr:    "60 9 * * *",
			wantErr: true,
		},
		{
			name:    "invalid hour value",
			expr:    "0 25 * * *",
			wantErr: true,
		},
		{
			name:    "invalid characters",
			expr:    "abc def * * *",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateCronExpression(tt.expr)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateCronExpression() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestCalculateNextRun_Cron tests cron expression scheduling
func TestCalculateNextRun_Cron(t *testing.T) {
	// Use a fixed reference time for consistent testing
	refTime := time.Date(2025, 12, 4, 10, 30, 0, 0, time.UTC) // Thursday, Dec 4, 2025 at 10:30 AM
	// Pin "now" to refTime so catch-up logic does not skip ahead.
	prevNow := nowFunc
	nowFunc = func() time.Time { return refTime }
	t.Cleanup(func() { nowFunc = prevNow })

	tests := []struct {
		name           string
		cronExpr       string
		lastRun        time.Time
		endDate        *time.Time
		wantNextIsNil  bool
		wantNextHour   int // Expected hour of next run (if not nil)
		wantNextMinute int // Expected minute of next run (if not nil)
	}{
		{
			name:           "daily at 9am - next day",
			cronExpr:       "0 9 * * *",
			lastRun:        refTime,
			endDate:        nil,
			wantNextIsNil:  false,
			wantNextHour:   9,
			wantNextMinute: 0,
		},
		{
			name:           "every 5 minutes - next occurrence",
			cronExpr:       "*/5 * * * *",
			lastRun:        refTime,
			endDate:        nil,
			wantNextIsNil:  false,
			wantNextHour:   10,
			wantNextMinute: 35, // Next 5-minute mark after 10:30
		},
		{
			name:     "exceeds end date",
			cronExpr: "0 9 * * *",
			lastRun:  refTime,
			endDate: func() *time.Time {
				t := refTime.Add(1 * time.Hour) // End date before next run
				return &t
			}(),
			wantNextIsNil: true,
		},
		{
			name:           "weekdays at 2pm",
			cronExpr:       "0 14 * * 1-5",
			lastRun:        refTime,
			endDate:        nil,
			wantNextIsNil:  false,
			wantNextHour:   14,
			wantNextMinute: 0,
		},
		{
			name:          "empty cron expression",
			cronExpr:      "",
			lastRun:       refTime,
			endDate:       nil,
			wantNextIsNil: true,
		},
		{
			name:          "invalid cron expression",
			cronExpr:      "invalid cron",
			lastRun:       refTime,
			endDate:       nil,
			wantNextIsNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := ScheduleConfig{
				Type:     ScheduleCron,
				CronExpr: tt.cronExpr,
				EndDate:  tt.endDate,
			}

			ts := &TaskScheduler{}
			next := ts.calculateNextRun(config, tt.lastRun)

			if tt.wantNextIsNil {
				if next != nil {
					t.Errorf("calculateNextRun() returned non-nil, want nil")
				}
				return
			}

			if next == nil {
				t.Errorf("calculateNextRun() returned nil, want non-nil")
				return
			}

			if next.Hour() != tt.wantNextHour || next.Minute() != tt.wantNextMinute {
				t.Errorf("calculateNextRun() next time = %v, want hour=%d minute=%d",
					next, tt.wantNextHour, tt.wantNextMinute)
			}

			// Verify next run is after lastRun
			if !next.After(tt.lastRun) {
				t.Errorf("calculateNextRun() next time %v is not after lastRun %v", next, tt.lastRun)
			}
		})
	}
}

// TestCalculateNextRun_RelativeDelay tests relative delay scheduling
func TestCalculateNextRun_RelativeDelay(t *testing.T) {
	refTime := time.Date(2025, 12, 4, 10, 30, 0, 0, time.UTC)

	tests := []struct {
		name            string
		delayDuration   time.Duration
		triggerOnce     bool
		lastRun         time.Time
		endDate         *time.Time
		maxRuns         int
		executionCount  int
		wantNextIsNil   bool
		wantNextMinutes int // Expected minutes from lastRun to next run
	}{
		{
			name:            "repeating 5 minute delay",
			delayDuration:   5 * time.Minute,
			triggerOnce:     false,
			lastRun:         refTime,
			endDate:         nil,
			wantNextIsNil:   false,
			wantNextMinutes: 5,
		},
		{
			name:            "repeating 30 second delay",
			delayDuration:   30 * time.Second,
			triggerOnce:     false,
			lastRun:         refTime,
			endDate:         nil,
			wantNextIsNil:   false,
			wantNextMinutes: 0, // Less than a minute
		},
		{
			name:            "one-time delay",
			delayDuration:   10 * time.Minute,
			triggerOnce:     true,
			lastRun:         refTime,
			endDate:         nil,
			wantNextIsNil:   true, // TriggerOnce = true means no next run after first
			wantNextMinutes: 0,
		},
		{
			name:          "zero delay duration",
			delayDuration: 0,
			triggerOnce:   false,
			lastRun:       refTime,
			endDate:       nil,
			wantNextIsNil: true,
		},
		{
			name:          "repeating delay exceeds end date",
			delayDuration: 1 * time.Hour,
			triggerOnce:   false,
			lastRun:       refTime,
			endDate: func() *time.Time {
				t := refTime.Add(30 * time.Minute) // End date before next run
				return &t
			}(),
			wantNextIsNil: true,
		},
		{
			name:            "repeating 1 hour delay",
			delayDuration:   1 * time.Hour,
			triggerOnce:     false,
			lastRun:         refTime,
			endDate:         nil,
			wantNextIsNil:   false,
			wantNextMinutes: 60,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := ScheduleConfig{
				Type:          ScheduleRelativeDelay,
				DelayDuration: tt.delayDuration,
				TriggerOnce:   tt.triggerOnce,
				EndDate:       tt.endDate,
			}

			ts := &TaskScheduler{}
			next := ts.calculateNextRun(config, tt.lastRun)

			if tt.wantNextIsNil {
				if next != nil {
					t.Errorf("calculateNextRun() returned non-nil, want nil")
				}
				return
			}

			if next == nil {
				t.Errorf("calculateNextRun() returned nil, want non-nil")
				return
			}

			// Verify next run is after lastRun
			if !next.After(tt.lastRun) {
				t.Errorf("calculateNextRun() next time %v is not after lastRun %v", next, tt.lastRun)
			}

			// Check delay duration
			expectedNext := tt.lastRun.Add(tt.delayDuration)
			if !next.Equal(expectedNext) {
				t.Errorf("calculateNextRun() next time = %v, want %v", next, expectedNext)
			}

			// Verify minutes difference if specified
			if tt.wantNextMinutes > 0 {
				minutesDiff := int(next.Sub(tt.lastRun).Minutes())
				if minutesDiff != tt.wantNextMinutes {
					t.Errorf("calculateNextRun() minutes difference = %d, want %d", minutesDiff, tt.wantNextMinutes)
				}
			}
		})
	}
}

// TestCalculateNextRun_RelativeDelay_WithMaxRuns tests max runs constraint
func TestCalculateNextRun_RelativeDelay_WithMaxRuns(t *testing.T) {
	refTime := time.Date(2025, 12, 4, 10, 30, 0, 0, time.UTC)

	// Note: MaxRuns is checked before calling calculateNextRun in the actual scheduler
	// This test documents expected behavior when used correctly
	config := ScheduleConfig{
		Type:          ScheduleRelativeDelay,
		DelayDuration: 5 * time.Minute,
		TriggerOnce:   false,
		MaxRuns:       3,
	}

	ts := &TaskScheduler{}

	// First run - should get next run
	next := ts.calculateNextRun(config, refTime)
	if next == nil {
		t.Error("First run: calculateNextRun() returned nil, want non-nil")
	}

	// Second run - should get next run
	next = ts.calculateNextRun(config, refTime.Add(5*time.Minute))
	if next == nil {
		t.Error("Second run: calculateNextRun() returned nil, want non-nil")
	}

	// Third run - should get next run
	next = ts.calculateNextRun(config, refTime.Add(10*time.Minute))
	if next == nil {
		t.Error("Third run: calculateNextRun() returned nil, want non-nil")
	}

	// Note: The actual enforcement of MaxRuns happens in executeScheduledTask()
	// calculateNextRun() doesn't track execution count, it only calculates the next time
}

// TestCalculateNextRun_ExistingScheduleTypes tests that existing schedule types still work
func TestCalculateNextRun_ExistingScheduleTypes(t *testing.T) {
	refTime := time.Date(2025, 12, 4, 10, 30, 0, 0, time.UTC) // Thursday
	// Pin "now" to refTime so catch-up logic does not skip ahead.
	prevNow := nowFunc
	nowFunc = func() time.Time { return refTime }
	t.Cleanup(func() { nowFunc = prevNow })

	t.Run("once schedule", func(t *testing.T) {
		config := ScheduleConfig{
			Type: ScheduleOnce,
		}
		ts := &TaskScheduler{}
		next := ts.calculateNextRun(config, refTime)
		if next != nil {
			t.Error("ScheduleOnce should return nil for next run")
		}
	})

	t.Run("interval schedule", func(t *testing.T) {
		config := ScheduleConfig{
			Type:     ScheduleInterval,
			Interval: 10 * time.Minute,
		}
		ts := &TaskScheduler{}
		next := ts.calculateNextRun(config, refTime)
		if next == nil {
			t.Fatal("ScheduleInterval returned nil")
			return
		}
		expected := refTime.Add(10 * time.Minute)
		if !next.Equal(expected) {
			t.Errorf("ScheduleInterval next = %v, want %v", next, expected)
		}
	})

	t.Run("daily schedule", func(t *testing.T) {
		config := ScheduleConfig{
			Type:      ScheduleDaily,
			TimeOfDay: "14:00",
		}
		ts := &TaskScheduler{}
		next := ts.calculateNextRun(config, refTime)
		if next == nil {
			t.Fatal("ScheduleDaily returned nil")
			return
		}
		// Should be next day at 14:00
		if next.Hour() != 14 || next.Minute() != 0 {
			t.Errorf("ScheduleDaily next time = %v, want 14:00", next)
		}
	})

	t.Run("weekly schedule", func(t *testing.T) {
		config := ScheduleConfig{
			Type:      ScheduleWeekly,
			TimeOfDay: "09:00",
			DayOfWeek: 1, // Monday
		}
		ts := &TaskScheduler{}
		next := ts.calculateNextRun(config, refTime)
		if next == nil {
			t.Fatal("ScheduleWeekly returned nil")
			return
		}
		// Should be next Monday at 09:00
		if next.Weekday() != time.Monday {
			t.Errorf("ScheduleWeekly next weekday = %v, want Monday", next.Weekday())
		}
		if next.Hour() != 9 || next.Minute() != 0 {
			t.Errorf("ScheduleWeekly next time = %v, want 09:00", next)
		}
	})
}

func TestTaskScheduler_ShouldSkipMissedTaskRun(t *testing.T) {
	ts := &TaskScheduler{pollInterval: time.Minute}
	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)

	missedAt := now.Add(-10 * time.Minute)
	task := &Task{
		SleepPolicy: "skip",
		NextRun:     &missedAt,
	}

	if !ts.shouldSkipMissedTaskRun(task, now) {
		t.Fatal("expected overdue skip-policy task to be skipped")
	}

	recentlyDue := now.Add(-30 * time.Second)
	task.NextRun = &recentlyDue
	if ts.shouldSkipMissedTaskRun(task, now) {
		t.Fatal("did not expect recently due task to be treated as missed sleep")
	}

	task.SleepPolicy = "run_once_on_wake"
	task.NextRun = &missedAt
	if ts.shouldSkipMissedTaskRun(task, now) {
		t.Fatal("did not expect run-on-wake policy to skip missed task")
	}
}

func TestCollectWakeCandidates(t *testing.T) {
	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	nextRun := now.Add(2 * time.Hour)
	pastRun := now.Add(-time.Hour)

	ws := &Workspace{
		ID:     "workspace-1",
		Status: StatusActive,
		Tasks: []Task{
			{
				ID:              "task-1",
				ScheduleName:    "Daily report",
				Schedule:        &ScheduleConfig{Type: ScheduleDaily, TimeOfDay: "09:00"},
				ScheduleEnabled: true,
				WakeMacEnabled:  true,
				WakeLeadMinutes: 10,
				NextRun:         &nextRun,
			},
			{
				ID:              "task-2",
				Schedule:        &ScheduleConfig{Type: ScheduleDaily, TimeOfDay: "10:00"},
				ScheduleEnabled: true,
				WakeMacEnabled:  true,
				NextRun:         &pastRun,
			},
		},
	}

	candidates := collectWakeCandidates(ws, now)
	if len(candidates) != 1 {
		t.Fatalf("expected 1 wake candidate, got %d", len(candidates))
	}
	if candidates[0].TaskID != "task-1" || candidates[0].LeadMinutes != 10 {
		t.Fatalf("unexpected candidate: %#v", candidates[0])
	}
}

// TestCalculateNextRun_Monthly verifies monthly cadence including the
// short-month clamp (DayOfMonth=31 in February fires on Feb 28/29).
func TestCalculateNextRun_Monthly(t *testing.T) {
	// Mid-January reference time so "next" is straightforward.
	refTime := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	prevNow := nowFunc
	nowFunc = func() time.Time { return refTime }
	t.Cleanup(func() { nowFunc = prevNow })

	tests := []struct {
		name        string
		dayOfMonth  int
		timeOfDay   string
		lastRun     time.Time
		wantYear    int
		wantMonth   time.Month
		wantDay     int
		wantNilNext bool
	}{
		{
			name:       "later this month",
			dayOfMonth: 28,
			timeOfDay:  "09:00",
			lastRun:    refTime,
			wantYear:   2026, wantMonth: time.January, wantDay: 28,
		},
		{
			name:       "day already passed this month rolls to next",
			dayOfMonth: 5,
			timeOfDay:  "09:00",
			lastRun:    refTime,
			wantYear:   2026, wantMonth: time.February, wantDay: 5,
		},
		{
			name:       "day 31 clamps to Feb 28 in a non-leap year",
			dayOfMonth: 31,
			timeOfDay:  "09:00",
			// Pin lastRun + now to late January 2026 so the next monthly tick lands in Feb 2026.
			lastRun:  time.Date(2026, 1, 31, 12, 0, 0, 0, time.UTC),
			wantYear: 2026, wantMonth: time.February, wantDay: 28,
		},
		{
			name:        "empty time_of_day returns nil",
			dayOfMonth:  15,
			timeOfDay:   "",
			lastRun:     refTime,
			wantNilNext: true,
		},
		{
			name:        "out-of-range day returns nil",
			dayOfMonth:  0,
			timeOfDay:   "09:00",
			lastRun:     refTime,
			wantNilNext: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// For tests that change "now" (e.g. clamp test), repin nowFunc.
			localNow := tc.lastRun
			if localNow.IsZero() {
				localNow = refTime
			}
			nowFunc = func() time.Time { return localNow }
			defer func() { nowFunc = func() time.Time { return refTime } }()

			next := CalculateNextRun(ScheduleConfig{
				Type:       ScheduleMonthly,
				TimeOfDay:  tc.timeOfDay,
				DayOfMonth: tc.dayOfMonth,
			}, tc.lastRun)

			if tc.wantNilNext {
				if next != nil {
					t.Fatalf("expected nil next; got %v", *next)
				}
				return
			}
			if next == nil {
				t.Fatalf("expected non-nil next")
			}
			if next.Year() != tc.wantYear || next.Month() != tc.wantMonth || next.Day() != tc.wantDay {
				t.Errorf("next = %v; want %d-%s-%02d", *next, tc.wantYear, tc.wantMonth, tc.wantDay)
			}
		})
	}
}

func TestClampDayOfMonth(t *testing.T) {
	cases := []struct {
		year, day int
		month     time.Month
		want      int
	}{
		{2026, 31, time.February, 28}, // non-leap February
		{2024, 31, time.February, 29}, // leap February
		{2026, 31, time.April, 30},    // 30-day month
		{2026, 15, time.July, 15},     // within range
		{2026, 31, time.January, 31},  // 31-day month
	}
	for _, c := range cases {
		got := clampDayOfMonth(c.year, c.month, c.day)
		if got != c.want {
			t.Errorf("clampDayOfMonth(%d, %s, %d) = %d; want %d", c.year, c.month, c.day, got, c.want)
		}
	}
}
