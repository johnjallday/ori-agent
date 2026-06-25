package orchestrationhttp

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// parseFlexibleTime parses a datetime string with or without timezone
func parseFlexibleTime(s string) (*time.Time, error) {
	if s == "" {
		return nil, nil
	}

	// Try RFC3339 first (with timezone)
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return &t, nil
	}

	// Try without timezone (assume local)
	if t, err := time.ParseInLocation("2006-01-02T15:04:05", s, time.Local); err == nil {
		return &t, nil
	}

	// Try without seconds
	if t, err := time.ParseInLocation("2006-01-02T15:04", s, time.Local); err == nil {
		return &t, nil
	}

	return nil, fmt.Errorf("unable to parse time: %s", s)
}

// convertScheduleConfig converts frontend schedule format to backend format
func convertScheduleConfig(raw json.RawMessage) *workspace.ScheduleConfig {
	if raw == nil {
		return nil
	}

	// First try parsing with raw strings to handle flexible datetime formats
	var rawConfig FrontendScheduleConfigRaw
	if err := json.Unmarshal(raw, &rawConfig); err != nil {
		logger.Warn("Failed to parse schedule config", logger.Fields{"err": err})
		return nil
	}

	config := &workspace.ScheduleConfig{
		Type:     workspace.ScheduleType(rawConfig.Type),
		MaxRuns:  rawConfig.MaxRuns,
		CronExpr: rawConfig.CronExpr,
	}

	// Handle interval conversion (minutes to time.Duration)
	if rawConfig.IntervalMinutes > 0 {
		config.Interval = time.Duration(rawConfig.IntervalMinutes) * time.Minute
		logger.Debug("Converted interval_minutes to Duration", logger.Fields{
			"interval_minutes": rawConfig.IntervalMinutes,
			"interval":         config.Interval,
		})
	}

	// Handle time_of_day (frontend sends "time" or "time_of_day")
	if rawConfig.Time != "" {
		config.TimeOfDay = rawConfig.Time
	} else if rawConfig.TimeOfDay != "" {
		config.TimeOfDay = rawConfig.TimeOfDay
	}

	// Handle day_of_week
	config.DayOfWeek = rawConfig.DayOfWeek

	// Handle execute_at (frontend sends "run_at" or "execute_at") with flexible parsing
	if rawConfig.RunAt != nil {
		if t, err := parseFlexibleTime(*rawConfig.RunAt); err == nil {
			config.ExecuteAt = t
		} else {
			logger.Warn("Failed to parse run_at time", logger.Fields{"value": *rawConfig.RunAt, "err": err})
		}
	} else if rawConfig.ExecuteAt != nil {
		if t, err := parseFlexibleTime(*rawConfig.ExecuteAt); err == nil {
			config.ExecuteAt = t
		} else {
			logger.Warn("Failed to parse execute_at time", logger.Fields{"value": *rawConfig.ExecuteAt, "err": err})
		}
	}

	// Handle end_date with flexible parsing
	if rawConfig.EndDate != nil {
		if t, err := parseFlexibleTime(*rawConfig.EndDate); err == nil {
			config.EndDate = t
		} else {
			logger.Warn("Failed to parse end_date time", logger.Fields{"value": *rawConfig.EndDate, "err": err})
		}
	}

	logger.Debug("Converted frontend schedule to backend format", logger.Fields{
		"type":        config.Type,
		"interval":    config.Interval,
		"time_of_day": config.TimeOfDay,
		"day_of_week": config.DayOfWeek,
		"execute_at":  config.ExecuteAt,
	})

	return config
}

func normalizeTaskSleepPolicy(policy string) string {
	switch strings.ToLower(strings.TrimSpace(policy)) {
	case "skip", "run_once_on_wake":
		return strings.ToLower(strings.TrimSpace(policy))
	default:
		return "run_once_on_wake"
	}
}

func normalizeWakeFallbackPolicy(policy string) string {
	switch strings.ToLower(strings.TrimSpace(policy)) {
	case "run_on_next_wake", "skip":
		return strings.ToLower(strings.TrimSpace(policy))
	default:
		return "run_on_next_wake"
	}
}

func normalizeWakeLeadMinutes(minutes int) int {
	if minutes <= 0 {
		return 5
	}
	if minutes > 120 {
		return 120
	}
	return minutes
}

// applyScheduleUpdates applies schedule configuration changes to a task
// Returns an error message if validation fails, empty string otherwise
func (th *TaskHandler) applyScheduleUpdates(task *workspace.Task, req *taskUpdateRequest, schedule *workspace.ScheduleConfig, clearSchedule bool) string {
	if schedule != nil {
		task.Schedule = schedule
		if task.ScheduleEnabled {
			task.NextRun = th.calculateNextRun(task)
		}
		logger.Debug("Updated task schedule", logger.Fields{"task_id": req.TaskID})
	}

	if clearSchedule {
		task.Schedule = nil
		task.ScheduleEnabled = false
		task.ScheduleName = ""
		task.SleepPolicy = ""
		task.WakeMacEnabled = false
		task.WakeLeadMinutes = 0
		task.WakeFallback = ""
		task.NextRun = nil
		logger.Debug("Cleared task schedule", logger.Fields{"task_id": req.TaskID})
	}

	if req.ScheduleEnabled != nil {
		if *req.ScheduleEnabled {
			taskTo := task.To
			if req.To != nil {
				taskTo = *req.To
			}
			if taskTo == "" || taskTo == "unassigned" {
				return "Scheduled tasks must be assigned to an agent. Please assign an agent before enabling the schedule."
			}
		}

		task.ScheduleEnabled = *req.ScheduleEnabled
		if *req.ScheduleEnabled && task.Schedule != nil {
			task.NextRun = th.calculateNextRun(task)
		} else if !*req.ScheduleEnabled {
			task.NextRun = nil
		}
		logger.Debug("Updated task schedule enabled", logger.Fields{"task_id": req.TaskID, "enabled": *req.ScheduleEnabled})
	}

	if req.ScheduleName != nil {
		task.ScheduleName = *req.ScheduleName
	}
	if req.SleepPolicy != nil {
		task.SleepPolicy = normalizeTaskSleepPolicy(*req.SleepPolicy)
	}
	if req.WakeMacEnabled != nil {
		task.WakeMacEnabled = *req.WakeMacEnabled
	}
	if req.WakeLeadMinutes != nil {
		task.WakeLeadMinutes = normalizeWakeLeadMinutes(*req.WakeLeadMinutes)
	}
	if req.WakeFallbackPolicy != nil {
		task.WakeFallback = normalizeWakeFallbackPolicy(*req.WakeFallbackPolicy)
	}

	if task.WakeMacEnabled && (task.Schedule == nil || !task.ScheduleEnabled) {
		task.WakeMacEnabled = false
	}

	return ""
}

// calculateNextRun calculates the next run time for a scheduled task
func (th *TaskHandler) calculateNextRun(task *workspace.Task) *time.Time {
	if task.Schedule == nil {
		return nil
	}
	lastRun := time.Time{}
	if task.LastRun != nil {
		lastRun = *task.LastRun
	}
	return workspace.CalculateNextRun(*task.Schedule, lastRun)
}
