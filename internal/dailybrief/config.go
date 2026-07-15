package dailybrief

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrInvalidTimezone is returned when a non-empty timezone fails
// time.LoadLocation. An empty timezone defaults to UTC rather than erroring.
var ErrInvalidTimezone = errors.New("dailybrief: invalid IANA timezone")

// ErrInvalidScheduleTime is returned when ScheduleTime is not "HH:MM".
var ErrInvalidScheduleTime = errors.New("dailybrief: schedule_time must be HH:MM 24-hour local time")

// ErrWorkspaceIDRequired is returned when a config has no owning workspace.
var ErrWorkspaceIDRequired = errors.New("dailybrief: workspace id is required")

// NormalizeConfig validates and defaults a caller-supplied Config so the
// setup/settings flow can be completed with only the fields the user
// actually chose to change. Returns a new Config; the input is not mutated.
func NormalizeConfig(in Config) (Config, error) {
	out := in
	out.WorkspaceID = strings.TrimSpace(out.WorkspaceID)
	if out.WorkspaceID == "" {
		return Config{}, ErrWorkspaceIDRequired
	}

	tz := strings.TrimSpace(out.Timezone)
	if tz == "" {
		tz = "UTC"
	}
	if _, err := time.LoadLocation(tz); err != nil {
		return Config{}, fmt.Errorf("%w: %q", ErrInvalidTimezone, tz)
	}
	out.Timezone = tz

	out.ScheduleDays = normalizeScheduleDays(out.ScheduleDays)

	scheduleTime := strings.TrimSpace(out.ScheduleTime)
	if scheduleTime == "" {
		scheduleTime = "08:00"
	}
	if _, err := time.Parse("15:04", scheduleTime); err != nil {
		return Config{}, fmt.Errorf("%w: %q", ErrInvalidScheduleTime, scheduleTime)
	}
	out.ScheduleTime = scheduleTime

	if out.Scope != ScopeSelected {
		out.Scope = ScopeAll
	}
	if out.Scope == ScopeAll {
		out.SelectedWorkspaceIDs = nil
	} else {
		out.SelectedWorkspaceIDs = normalizeIDs(out.SelectedWorkspaceIDs)
	}

	return out, nil
}

func normalizeScheduleDays(days []string) []string {
	if len(days) == 0 {
		return append([]string(nil), defaultScheduleDays...)
	}
	seen := make(map[string]bool, len(days))
	out := make([]string, 0, len(days))
	for _, d := range days {
		d = strings.ToLower(strings.TrimSpace(d))
		if !validDays[d] || seen[d] {
			continue
		}
		seen[d] = true
		out = append(out, d)
	}
	if len(out) == 0 {
		return append([]string(nil), defaultScheduleDays...)
	}
	return out
}

func normalizeIDs(ids []string) []string {
	seen := make(map[string]bool, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}
