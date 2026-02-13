package workspace

import (
	"testing"
	"time"
)

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
