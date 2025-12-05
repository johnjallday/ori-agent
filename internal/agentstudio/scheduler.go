package agentstudio

import (
	"fmt"

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/robfig/cron/v3"
	"sync"
	"time"
)

// TaskScheduler handles automatic execution of scheduled tasks
type TaskScheduler struct {
	workspaceStore Store
	eventBus       *EventBus
	pollInterval   time.Duration

	stopChan chan struct{}
	wg       sync.WaitGroup
}

// SchedulerConfig contains configuration for the task scheduler
type SchedulerConfig struct {
	PollInterval time.Duration // How often to check for scheduled tasks
}

// NewTaskScheduler creates a new task scheduler
func NewTaskScheduler(store Store, config SchedulerConfig) *TaskScheduler {
	if config.PollInterval == 0 {
		config.PollInterval = 1 * time.Minute // Default: check every minute
	}

	return &TaskScheduler{
		workspaceStore: store,
		pollInterval:   config.PollInterval,
		stopChan:       make(chan struct{}),
	}
}

// SetEventBus sets the event bus for publishing events
func (ts *TaskScheduler) SetEventBus(eventBus *EventBus) {
	ts.eventBus = eventBus
}

// Start begins the scheduler polling loop
func (ts *TaskScheduler) Start() {
	logger.Debug("📅 Task scheduler started (poll interval: )", logger.Fields{"task_id": ts.pollInterval})

	ts.wg.Add(1)
	go ts.pollLoop()
}

// Stop gracefully stops the scheduler
func (ts *TaskScheduler) Stop() {
	logger.Debug("⏹️ Stopping task scheduler...", logger.Fields{})
	close(ts.stopChan)
	ts.wg.Wait()
	logger.Info("Task scheduler stopped", logger.Fields{})
}

// pollLoop continuously polls for scheduled tasks
func (ts *TaskScheduler) pollLoop() {
	defer ts.wg.Done()

	ticker := time.NewTicker(ts.pollInterval)
	defer ticker.Stop()

	// Run immediately on start
	ts.checkScheduledTasks()

	for {
		select {
		case <-ts.stopChan:
			return
		case <-ticker.C:
			ts.checkScheduledTasks()
		}
	}
}

// checkScheduledTasks checks all workspaces for scheduled tasks that need to run.
// This is the main polling function called on every tick (default: every minute).
//
// Execution flow:
//  1. List all workspaces
//  2. Filter to active workspaces only
//  3. For each enabled scheduled task in each workspace:
//     a. Check if NextRun time has arrived (now >= NextRun)
//     b. Validate max_runs limit not exceeded
//     c. Validate end_date not passed
//     d. Execute task if all checks pass
//
// Note: Uses pointer iteration (&ws.ScheduledTasks[i]) to allow in-place modifications
func (ts *TaskScheduler) checkScheduledTasks() {
	workspaceIDs, err := ts.workspaceStore.List()
	if err != nil {
		logger.Error("Failed to list workspaces", logger.Fields{"workspace_id": err})
		return
	}

	now := time.Now()

	for _, wsID := range workspaceIDs {
		ws, err := ts.workspaceStore.Get(wsID)
		if err != nil {
			continue
		}

		// Only process active workspaces (skip paused/archived workspaces)
		if ws.Status != StatusActive {
			continue
		}

		// Check each enabled scheduled task
		// Use pointer iteration to allow in-place modifications of task state
		for i := range ws.ScheduledTasks {
			st := &ws.ScheduledTasks[i]

			// Skip disabled tasks (already completed, failed, or manually disabled)
			if !st.Enabled {
				continue
			}

			// Check if it's time to run (NextRun must be set and in the past/present)
			if st.NextRun == nil || st.NextRun.After(now) {
				continue
			}

			// VALIDATION 1: Check if max runs reached
			// max_runs=0 means unlimited executions
			if st.Schedule.MaxRuns > 0 && st.ExecutionCount >= st.Schedule.MaxRuns {
				logger.Debug("📅 Scheduled task reached max runs (), disabling", logger.Fields{"task_id": st.ID, "maxruns": st.Schedule.MaxRuns})
				st.Enabled = false
				st.NextRun = nil
				if err := ws.UpdateScheduledTask(*st); err != nil {
					logger.Error("Failed to update scheduled task", logger.Fields{"task_id": err})
				}
				if err := ts.workspaceStore.Save(ws); err != nil {
					logger.Error("Failed to save workspace", logger.Fields{"workspace_id": err})
				}
				continue
			}

			// VALIDATION 2: Check if end date passed
			// end_date is optional; nil means no end date
			if st.Schedule.EndDate != nil && now.After(*st.Schedule.EndDate) {
				logger.Debug("📅 Scheduled task passed end date, disabling", logger.Fields{"task_id": st.ID})
				st.Enabled = false
				st.NextRun = nil
				if err := ws.UpdateScheduledTask(*st); err != nil {
					logger.Error("Failed to update scheduled task", logger.Fields{"task_id": err})
				}
				if err := ts.workspaceStore.Save(ws); err != nil {
					logger.Error("Failed to save workspace", logger.Fields{"workspace_id": err})
				}
				continue
			}

			// All checks passed - execute the scheduled task
			ts.executeScheduledTask(ws, st)
		}
	}
}

// executeScheduledTask creates a Task from a ScheduledTask and updates the schedule.
// This function handles:
// 1. Task creation from scheduled task template
// 2. Execution tracking (success/failure counts, history)
// 3. Next run calculation
// 4. Auto-disable on failure threshold (5 consecutive failures)
// 5. Event publishing for UI updates
func (ts *TaskScheduler) executeScheduledTask(ws *Workspace, st *ScheduledTask) {
	logger.Debug("📅 Executing scheduled task", logger.Fields{"task_id": st.ID, "name": st.Name})

	// Create a regular Task from the ScheduledTask template
	// This converts the scheduled task definition into an executable task
	task := Task{
		WorkspaceID: ws.ID,
		From:        st.From,
		To:          st.To,
		Description: st.Prompt,
		Priority:    st.Priority,
		Context:     st.Context,
		Status:      TaskStatusPending,
	}

	// Add task to workspace
	if err := ws.AddTask(task); err != nil {
		// FAILURE PATH: Task creation failed
		logger.Error("Failed to create task from scheduled task", logger.Fields{"task_id": st.ID, "err": err})
		st.FailureCount++
		st.LastError = err.Error()

		// Record failed execution in history for debugging/monitoring
		execution := TaskExecution{
			TaskID:     "", // No task was created
			ExecutedAt: time.Now(),
			Status:     "failed",
			Error:      err.Error(),
		}
		st.ExecutionHistory = append(st.ExecutionHistory, execution)

		// Limit history size to prevent unbounded growth (last 20 executions only)
		if len(st.ExecutionHistory) > 20 {
			st.ExecutionHistory = st.ExecutionHistory[len(st.ExecutionHistory)-20:]
		}

		// Auto-disable after 5 consecutive failures to prevent runaway errors
		// This prevents a broken scheduled task from spamming errors indefinitely
		if st.FailureCount >= 5 {
			logger.Warn("Scheduled task disabled after consecutive failures", logger.Fields{"task_id": st.ID, "failurecount": st.FailureCount})
			st.Enabled = false
		}

		if err := ws.UpdateScheduledTask(*st); err != nil {
			logger.Error("Failed to update scheduled task", logger.Fields{"task_id": err})
		}
		if err := ts.workspaceStore.Save(ws); err != nil {
			logger.Error("Failed to save workspace", logger.Fields{"workspace_id": err})
		}

		// Publish failure event
		if ts.eventBus != nil {
			event := NewScheduledTaskEvent(EventScheduledTaskFailed, ws.ID, st.ID, st.Name, map[string]interface{}{
				"error":         st.LastError,
				"failure_count": st.FailureCount,
				"timestamp":     time.Now(),
				"disabled":      !st.Enabled, // true if disabled after 5 failures
			})
			ts.eventBus.Publish(event)
		}

		return
	}

	// SUCCESS PATH: Task created successfully
	// Get the created task ID (it's the last task in the list)
	var createdTaskID string
	if len(ws.Tasks) > 0 {
		createdTaskID = ws.Tasks[len(ws.Tasks)-1].ID
	}

	// Update execution tracking with success metrics
	now := time.Now()
	st.LastRun = &now
	st.ExecutionCount++
	st.FailureCount = 0 // Reset failure count on successful task creation (allows recovery)

	// Record successful execution in history for monitoring/debugging
	execution := TaskExecution{
		TaskID:     createdTaskID,
		ExecutedAt: now,
		Status:     "success",
	}
	st.ExecutionHistory = append(st.ExecutionHistory, execution)

	// Limit history size to prevent unbounded growth (last 20 executions only)
	if len(st.ExecutionHistory) > 20 {
		st.ExecutionHistory = st.ExecutionHistory[len(st.ExecutionHistory)-20:]
	}

	// Calculate next run time based on schedule type and configuration
	// This respects end_date, max_runs, and schedule-specific logic
	nextRun := ts.calculateNextRun(st.Schedule, now)
	st.NextRun = nextRun

	// Auto-disable if no next run is scheduled
	// This handles: once schedules, end_date exceeded, max_runs reached, trigger_once=true
	if nextRun == nil {
		st.Enabled = false
		logger.Info("📅 Scheduled task completed (one-time execution), disabling", logger.Fields{"duration": st.ID})
	}

	// Update the scheduled task
	if err := ws.UpdateScheduledTask(*st); err != nil {
		logger.Error("Failed to update scheduled task", logger.Fields{"task_id": err})
		return
	}

	// Save workspace
	if err := ts.workspaceStore.Save(ws); err != nil {
		logger.Error("Failed to save workspace", logger.Fields{"workspace_id": err})
		return
	}

	logger.Info("Scheduled task executed successfully (next run: )", logger.Fields{"task_id": st.ID, "nextRun": nextRun})

	// Publish triggered event
	if ts.eventBus != nil {
		event := NewScheduledTaskEvent(EventScheduledTaskTriggered, ws.ID, st.ID, st.Name, map[string]interface{}{
			"task_id":         createdTaskID,
			"task_created":    true,
			"execution_count": st.ExecutionCount,
			"next_run":        nextRun,
			"timestamp":       now,
		})
		ts.eventBus.Publish(event)

		// Also publish workspace updated event for backward compatibility
		workspaceEvent := NewWorkspaceEvent(EventWorkspaceUpdated, ws.ID, "scheduler", map[string]interface{}{
			"scheduled_task_id": st.ID,
			"task_created":      true,
			"execution_count":   st.ExecutionCount,
			"next_run":          nextRun,
		})
		ts.eventBus.Publish(workspaceEvent)
	}
}

// calculateNextRun calculates the next execution time based on the schedule configuration.
// It handles multiple schedule types (once, interval, daily, weekly, cron, relative_delay)
// and respects end_date constraints. Returns nil if no future execution should occur.
//
// Schedule type behaviors:
// - ScheduleOnce: Returns nil (single execution only)
// - ScheduleInterval: Adds interval duration to lastRun
// - ScheduleDaily: Schedules for same time next day
// - ScheduleWeekly: Schedules for same time on target weekday next week
// - ScheduleCron: Uses cron expression to calculate next occurrence
// - ScheduleRelativeDelay: Adds delay_duration to lastRun (respects trigger_once flag)
func (ts *TaskScheduler) calculateNextRun(config ScheduleConfig, lastRun time.Time) *time.Time {
	switch config.Type {
	case ScheduleOnce:
		// One-time execution, no next run
		return nil

	case ScheduleInterval:
		// Validate interval is non-zero
		if config.Interval == 0 {
			logger.Warn("Invalid interval schedule: interval is 0", logger.Fields{})
			return nil
		}
		// Simple arithmetic: lastRun + interval duration
		next := lastRun.Add(config.Interval)

		// Check if next run exceeds end date
		if config.EndDate != nil && next.After(*config.EndDate) {
			return nil
		}

		return &next

	case ScheduleDaily:
		// Validate time_of_day is provided (format: "HH:MM")
		if config.TimeOfDay == "" {
			logger.Warn("Invalid daily schedule: time_of_day is empty", logger.Fields{})
			return nil
		}

		// Parse time of day (format: "HH:MM")
		var hour, minute int
		if _, err := fmt.Sscanf(config.TimeOfDay, "%d:%d", &hour, &minute); err != nil {
			logger.Warn("Invalid time_of_day format", logger.Fields{"err": err, "duration": config.TimeOfDay})
			return nil
		}

		// Schedule for same time tomorrow (day + 1)
		// We use time.Date to handle month/year transitions automatically
		next := time.Date(lastRun.Year(), lastRun.Month(), lastRun.Day()+1, hour, minute, 0, 0, lastRun.Location())

		// Check if next run exceeds end date
		if config.EndDate != nil && next.After(*config.EndDate) {
			return nil
		}

		return &next

	case ScheduleWeekly:
		// Validate time_of_day is provided
		if config.TimeOfDay == "" {
			logger.Warn("Invalid weekly schedule: time_of_day is empty", logger.Fields{})
			return nil
		}

		// Parse time of day
		var hour, minute int
		if _, err := fmt.Sscanf(config.TimeOfDay, "%d:%d", &hour, &minute); err != nil {
			logger.Warn("Invalid time_of_day format", logger.Fields{"duration": config.TimeOfDay, "err": err})
			return nil
		}

		// Calculate next occurrence of target weekday
		// Example: If today is Tuesday (2) and target is Friday (5), daysUntil = 3
		// Example: If today is Friday (5) and target is Tuesday (2), daysUntil = 4 (next week)
		targetWeekday := time.Weekday(config.DayOfWeek)
		currentWeekday := lastRun.Weekday()

		// Calculate days until next occurrence
		daysUntil := int(targetWeekday - currentWeekday)
		if daysUntil <= 0 {
			daysUntil += 7 // Next week (always schedule at least 1 week ahead)
		}

		next := time.Date(
			lastRun.Year(),
			lastRun.Month(),
			lastRun.Day()+daysUntil,
			hour,
			minute,
			0,
			0,
			lastRun.Location(),
		)

		// Check if next run exceeds end date
		if config.EndDate != nil && next.After(*config.EndDate) {
			return nil
		}

		return &next

	case ScheduleCron:
		// Validate cron expression is provided
		if config.CronExpr == "" {
			logger.Warn("Invalid cron schedule: cron_expr is empty", logger.Fields{})
			return nil
		}

		// Parse cron expression using robfig/cron library
		// Format: minute hour day-of-month month day-of-week
		// Example: "0 9 * * *" = daily at 9:00 AM
		parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
		schedule, err := parser.Parse(config.CronExpr)
		if err != nil {
			logger.Warn("Invalid cron expression", logger.Fields{"cron_expr": config.CronExpr, "err": err})
			return nil
		}

		// Calculate next execution time from lastRun
		// The cron library handles all complex cases (month transitions, leap years, etc.)
		next := schedule.Next(lastRun)

		// Check if next run exceeds end date
		if config.EndDate != nil && next.After(*config.EndDate) {
			return nil
		}

		return &next

	case ScheduleRelativeDelay:
		// Validate delay_duration is non-zero
		if config.DelayDuration == 0 {
			logger.Warn("Invalid relative delay schedule: delay_duration is 0", logger.Fields{})
			return nil
		}

		// If TriggerOnce is true, don't schedule again after first execution
		// This allows "run X minutes after task completion" semantics without repetition
		if config.TriggerOnce {
			return nil
		}

		// Calculate next run as lastRun + DelayDuration
		// This creates a repeating schedule based on when task last ran
		next := lastRun.Add(config.DelayDuration)

		// Check if next run exceeds end date
		if config.EndDate != nil && next.After(*config.EndDate) {
			return nil
		}

		return &next

	default:
		logger.Warn("Unknown schedule type", logger.Fields{"type": config.Type})
		return nil
	}
}

// ValidateCronExpression validates a cron expression
func ValidateCronExpression(expr string) error {
	if expr == "" {
		return fmt.Errorf("cron expression is empty")
	}

	// Parse using standard cron format (minute, hour, day, month, weekday)
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	_, err := parser.Parse(expr)
	if err != nil {
		return fmt.Errorf("invalid cron expression: %w", err)
	}

	return nil
}
