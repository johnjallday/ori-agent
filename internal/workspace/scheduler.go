package workspace

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
	logger.Debug("Task scheduler started", logger.Fields{"poll_interval": ts.pollInterval})

	ts.wg.Add(1)
	go ts.pollLoop()
}

// Stop gracefully stops the scheduler
func (ts *TaskScheduler) Stop() {
	logger.Debug("Stopping task scheduler", logger.Fields{})
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

// checkScheduledTasks checks all workspaces for tasks with schedules that need to run.
// This is the main polling function called on every tick (default: every minute).
//
// Execution flow:
//  1. List all workspaces
//  2. Filter to active workspaces only
//  3. For each task with Schedule != nil && ScheduleEnabled:
//     a. Check if NextRun time has arrived (now >= NextRun)
//     b. Validate max_runs limit not exceeded
//     c. Validate end_date not passed
//     d. Reset and re-execute task if all checks pass
//
// Note: Uses pointer iteration (&ws.Tasks[i]) to allow in-place modifications
func (ts *TaskScheduler) checkScheduledTasks() {
	workspaceIDs, err := ts.workspaceStore.List()
	if err != nil {
		logger.Error("Failed to list workspaces", logger.Fields{"error": err})
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

		// Check each task with an enabled schedule
		// Use pointer iteration to allow in-place modifications of task state
		for i := range ws.Tasks {
			task := &ws.Tasks[i]

			// Skip tasks without schedules or disabled schedules
			if task.Schedule == nil || !task.ScheduleEnabled {
				continue
			}

			// Check if it's time to run (NextRun must be set and in the past/present)
			if task.NextRun == nil || task.NextRun.After(now) {
				continue
			}

			// VALIDATION 1: Check if max runs reached
			// max_runs=0 means unlimited executions
			if task.Schedule.MaxRuns > 0 && task.ExecutionCount >= task.Schedule.MaxRuns {
				logger.Debug("📅 Task schedule reached max runs, disabling", logger.Fields{"task_id": task.ID, "maxruns": task.Schedule.MaxRuns})
				task.ScheduleEnabled = false
				task.NextRun = nil
				if err := ws.UpdateTask(*task); err != nil {
					logger.Error("Failed to update task", logger.Fields{"error": err})
				}
				if err := ts.workspaceStore.Save(ws); err != nil {
					logger.Error("Failed to save workspace", logger.Fields{"error": err})
				}
				continue
			}

			// VALIDATION 2: Check if end date passed
			// end_date is optional; nil means no end date
			if task.Schedule.EndDate != nil && now.After(*task.Schedule.EndDate) {
				logger.Debug("📅 Task schedule passed end date, disabling", logger.Fields{"task_id": task.ID})
				task.ScheduleEnabled = false
				task.NextRun = nil
				if err := ws.UpdateTask(*task); err != nil {
					logger.Error("Failed to update task", logger.Fields{"error": err})
				}
				if err := ts.workspaceStore.Save(ws); err != nil {
					logger.Error("Failed to save workspace", logger.Fields{"error": err})
				}
				continue
			}

			// All checks passed - execute the scheduled task
			ts.executeTaskSchedule(ws, task, now)
		}

		// Also check legacy ScheduledTasks for backward compatibility during migration
		ts.checkLegacyScheduledTasks(ws, now)
	}
}

// checkLegacyScheduledTasks handles old ScheduledTask entities for backward compatibility
func (ts *TaskScheduler) checkLegacyScheduledTasks(ws *Workspace, now time.Time) {
	for i := range ws.ScheduledTasks {
		st := &ws.ScheduledTasks[i]

		if !st.Enabled {
			continue
		}

		if st.NextRun == nil || st.NextRun.After(now) {
			continue
		}

		if st.Schedule.MaxRuns > 0 && st.ExecutionCount >= st.Schedule.MaxRuns {
			st.Enabled = false
			st.NextRun = nil
			if err := ws.UpdateScheduledTask(*st); err != nil {
				logger.Error("Failed to update scheduled task", logger.Fields{"error": err, "task_id": st.ID})
			}
			if err := ts.workspaceStore.Save(ws); err != nil {
				logger.Error("Failed to save workspace", logger.Fields{"error": err})
			}
			continue
		}

		if st.Schedule.EndDate != nil && now.After(*st.Schedule.EndDate) {
			st.Enabled = false
			st.NextRun = nil
			if err := ws.UpdateScheduledTask(*st); err != nil {
				logger.Error("Failed to update scheduled task", logger.Fields{"error": err, "task_id": st.ID})
			}
			if err := ts.workspaceStore.Save(ws); err != nil {
				logger.Error("Failed to save workspace", logger.Fields{"error": err})
			}
			continue
		}

		if st.CanvasNodeID != "" && st.TargetTaskID == "" {
			nextRun := ts.calculateNextRun(st.Schedule, now)
			st.NextRun = nextRun
			if err := ws.UpdateScheduledTask(*st); err != nil {
				logger.Error("Failed to update scheduled task", logger.Fields{"error": err, "task_id": st.ID})
			}
			if err := ts.workspaceStore.Save(ws); err != nil {
				logger.Error("Failed to save workspace", logger.Fields{"error": err})
			}
			continue
		}

		ts.executeScheduledTask(ws, st)
	}
}

// executeTaskSchedule resets a task and queues it for re-execution based on its schedule.
// This is the new approach where schedule is directly on the task.
func (ts *TaskScheduler) executeTaskSchedule(ws *Workspace, task *Task, now time.Time) {
	logger.Debug("📅 Executing scheduled task", logger.Fields{"task_id": task.ID, "name": task.ScheduleName})

	// Validate task is assigned to an agent
	if task.To == "" || task.To == "unassigned" {
		ts.recordTaskScheduleFailure(ws, task, fmt.Errorf("task is not assigned to an agent"))
		return
	}

	// Don't interrupt in-progress tasks
	if task.Status == TaskStatusInProgress {
		ts.recordTaskScheduleFailure(ws, task, fmt.Errorf("task is already in progress"))
		return
	}

	// Reset task state for re-execution
	task.Status = TaskStatusAssigned
	task.Result = ""
	task.Error = ""
	task.Progress = nil
	task.StartedAt = nil
	task.CompletedAt = nil

	// Update schedule tracking
	task.LastRun = &now
	task.ExecutionCount++
	task.FailureCount = 0 // Reset failure count on successful trigger

	// Add to execution history
	execution := TaskExecution{
		TaskID:     task.ID,
		ExecutedAt: now,
		Status:     "success",
	}
	task.ExecutionHistory = append(task.ExecutionHistory, execution)
	if len(task.ExecutionHistory) > 20 {
		task.ExecutionHistory = task.ExecutionHistory[len(task.ExecutionHistory)-20:]
	}

	// Calculate next run
	nextRun := CalculateNextRun(*task.Schedule, now)
	task.NextRun = nextRun

	// Auto-disable one-time schedules
	if nextRun == nil {
		task.ScheduleEnabled = false
		logger.Info("📅 Task schedule completed (one-time execution), disabling", logger.Fields{"task_id": task.ID})
	}

	// Save changes
	if err := ws.UpdateTask(*task); err != nil {
		logger.Error("Failed to update task", logger.Fields{"error": err})
		return
	}

	if err := ts.workspaceStore.Save(ws); err != nil {
		logger.Error("Failed to save workspace", logger.Fields{"error": err})
		return
	}

	// Publish events
	if ts.eventBus != nil {
		event := NewScheduledTaskEvent(EventScheduledTaskTriggered, ws.ID, task.ID, task.ScheduleName, map[string]interface{}{
			"task_id":         task.ID,
			"task_created":    false,
			"execution_count": task.ExecutionCount,
			"next_run":        nextRun,
			"timestamp":       now,
		})
		ts.eventBus.Publish(event)

		workspaceEvent := NewWorkspaceEvent(EventWorkspaceUpdated, ws.ID, "scheduler", map[string]interface{}{
			"task_id":         task.ID,
			"execution_count": task.ExecutionCount,
			"next_run":        nextRun,
		})
		ts.eventBus.Publish(workspaceEvent)
	}
}

// recordTaskScheduleFailure updates task schedule bookkeeping and emits failure events.
func (ts *TaskScheduler) recordTaskScheduleFailure(ws *Workspace, task *Task, err error) {
	logger.Error("Failed to execute scheduled task", logger.Fields{"task_id": task.ID, "err": err})

	task.FailureCount++

	execution := TaskExecution{
		TaskID:     task.ID,
		ExecutedAt: time.Now(),
		Status:     "failed",
		Error:      err.Error(),
	}
	task.ExecutionHistory = append(task.ExecutionHistory, execution)
	if len(task.ExecutionHistory) > 20 {
		task.ExecutionHistory = task.ExecutionHistory[len(task.ExecutionHistory)-20:]
	}

	// Auto-disable after 5 consecutive failures
	if task.FailureCount >= 5 {
		logger.Warn("Task schedule disabled after consecutive failures", logger.Fields{"task_id": task.ID, "failure_count": task.FailureCount})
		task.ScheduleEnabled = false
	}

	if err := ws.UpdateTask(*task); err != nil {
		logger.Error("Failed to update task", logger.Fields{"error": err})
	}
	if err := ts.workspaceStore.Save(ws); err != nil {
		logger.Error("Failed to save workspace", logger.Fields{"error": err})
	}

	if ts.eventBus != nil {
		event := NewScheduledTaskEvent(EventScheduledTaskFailed, ws.ID, task.ID, task.ScheduleName, map[string]interface{}{
			"error":         err.Error(),
			"failure_count": task.FailureCount,
			"timestamp":     time.Now(),
			"disabled":      !task.ScheduleEnabled,
		})
		ts.eventBus.Publish(event)
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

	now := time.Now()

	// Schedulers MUST be linked to an existing task node
	// The prompt-based mode has been removed - all schedulers now rerun existing tasks
	if st.TargetTaskID == "" {
		logger.Warn("📅 Scheduler has no target task linked - skipping execution", logger.Fields{
			"scheduler_id": st.ID,
			"name":         st.Name,
		})
		// Calculate next run but don't execute
		nextRun := ts.calculateNextRun(st.Schedule, now)
		st.NextRun = nextRun
		st.LastRun = &now
		if err := ws.UpdateScheduledTask(*st); err != nil {
			logger.Error("Failed to update scheduled task", logger.Fields{"error": err})
		}
		if err := ts.workspaceStore.Save(ws); err != nil {
			logger.Error("Failed to save workspace", logger.Fields{"error": err})
		}
		return
	}

	// Execute the linked task (rerunTargetTask handles everything: execution, tracking, next run calculation)
	ts.rerunTargetTask(ws, st, now)
}

// rerunTargetTask resets and queues an existing task for execution based on the scheduler configuration.
func (ts *TaskScheduler) rerunTargetTask(ws *Workspace, st *ScheduledTask, now time.Time) {
	targetTask, err := ws.GetTask(st.TargetTaskID)
	if err != nil {
		ts.recordScheduleFailure(ws, st, fmt.Errorf("target task %s not found: %w", st.TargetTaskID, err), st.TargetTaskID)
		return
	}

	if targetTask.Status == TaskStatusInProgress {
		ts.recordScheduleFailure(ws, st, fmt.Errorf("target task %s is already in progress", targetTask.ID), targetTask.ID)
		return
	}

	if targetTask.To == "" || targetTask.To == "unassigned" {
		ts.recordScheduleFailure(ws, st, fmt.Errorf("target task %s is not assigned to an agent", targetTask.ID), targetTask.ID)
		return
	}

	// Reset task state for rerun and queue it for the executor
	targetTask.Status = TaskStatusAssigned
	targetTask.Result = ""
	targetTask.Error = ""
	targetTask.Progress = nil
	targetTask.StartedAt = nil
	targetTask.CompletedAt = nil

	if err := ws.UpdateTask(*targetTask); err != nil {
		ts.recordScheduleFailure(ws, st, fmt.Errorf("failed to queue target task %s: %w", targetTask.ID, err), targetTask.ID)
		return
	}

	// Track successful trigger
	st.LastRun = &now
	st.ExecutionCount++
	st.FailureCount = 0
	st.LastError = ""

	execution := TaskExecution{
		TaskID:     targetTask.ID,
		ExecutedAt: now,
		Status:     "success",
	}
	st.ExecutionHistory = append(st.ExecutionHistory, execution)
	if len(st.ExecutionHistory) > 20 {
		st.ExecutionHistory = st.ExecutionHistory[len(st.ExecutionHistory)-20:]
	}

	nextRun := ts.calculateNextRun(st.Schedule, now)
	st.NextRun = nextRun

	if nextRun == nil {
		st.Enabled = false
		logger.Info("Scheduled task completed (one-time execution), disabling", logger.Fields{"scheduler_id": st.ID})
	}

	if err := ws.UpdateScheduledTask(*st); err != nil {
		logger.Error("Failed to update scheduled task", logger.Fields{"error": err})
		return
	}

	if err := ts.workspaceStore.Save(ws); err != nil {
		logger.Error("Failed to save workspace", logger.Fields{"error": err})
		return
	}

	if ts.eventBus != nil {
		event := NewScheduledTaskEvent(EventScheduledTaskTriggered, ws.ID, st.ID, st.Name, map[string]interface{}{
			"task_id":         targetTask.ID,
			"task_created":    false,
			"execution_count": st.ExecutionCount,
			"next_run":        nextRun,
			"timestamp":       now,
			"scheduled_task":  st,
			"target_task_id":  targetTask.ID,
		})
		ts.eventBus.Publish(event)

		workspaceEvent := NewWorkspaceEvent(EventWorkspaceUpdated, ws.ID, "scheduler", map[string]interface{}{
			"scheduled_task_id": st.ID,
			"task_created":      false,
			"execution_count":   st.ExecutionCount,
			"next_run":          nextRun,
			"target_task_id":    targetTask.ID,
		})
		ts.eventBus.Publish(workspaceEvent)
	}
}

// recordScheduleFailure updates scheduler bookkeeping and emits failure events.
func (ts *TaskScheduler) recordScheduleFailure(ws *Workspace, st *ScheduledTask, err error, taskID string) {
	logger.Error("Failed to execute scheduled task", logger.Fields{"task_id": st.ID, "err": err})

	st.FailureCount++
	st.LastError = err.Error()

	execution := TaskExecution{
		TaskID:     taskID,
		ExecutedAt: time.Now(),
		Status:     "failed",
		Error:      err.Error(),
	}
	st.ExecutionHistory = append(st.ExecutionHistory, execution)
	if len(st.ExecutionHistory) > 20 {
		st.ExecutionHistory = st.ExecutionHistory[len(st.ExecutionHistory)-20:]
	}

	if st.FailureCount >= 5 {
		logger.Warn("Scheduled task disabled after consecutive failures", logger.Fields{"task_id": st.ID, "failure_count": st.FailureCount})
		st.Enabled = false
	}

	if err := ws.UpdateScheduledTask(*st); err != nil {
		logger.Error("Failed to update scheduled task", logger.Fields{"error": err})
	}
	if err := ts.workspaceStore.Save(ws); err != nil {
		logger.Error("Failed to save workspace", logger.Fields{"error": err})
	}

	if ts.eventBus != nil {
		event := NewScheduledTaskEvent(EventScheduledTaskFailed, ws.ID, st.ID, st.Name, map[string]interface{}{
			"error":          st.LastError,
			"failure_count":  st.FailureCount,
			"timestamp":      time.Now(),
			"disabled":       !st.Enabled, // true if disabled after 5 failures
			"scheduled_task": st,
			"target_task_id": taskID,
		})
		ts.eventBus.Publish(event)
	}
}

// calculateNextRun calculates the next execution time based on the schedule configuration.
// It handles multiple schedule types (once, interval, daily, weekly, cron, relative_delay)
// and respects end_date constraints. Returns nil if no future execution should occur.
//
// Schedule type behaviors:
// - ScheduleOnce: Returns ExecuteAt if not yet run (lastRun is zero), nil after execution
// - ScheduleInterval: Adds interval duration to lastRun
// - ScheduleDaily: Schedules for same time next day
// - ScheduleWeekly: Schedules for same time on target weekday next week
// - ScheduleCron: Uses cron expression to calculate next occurrence
// - ScheduleRelativeDelay: Adds delay_duration to lastRun (respects trigger_once flag)
func (ts *TaskScheduler) calculateNextRun(config ScheduleConfig, lastRun time.Time) *time.Time {
	return CalculateNextRun(config, lastRun)
}

// skipPastIntervals advances next by step until it is strictly after now.
// Used by interval/daily/weekly/cron paths so that a long downtime does not
// produce a burst of catch-up firings on the next poll. Caps iterations to
// avoid runaway loops on malformed configs.
func skipPastIntervals(next, now time.Time, step time.Duration) time.Time {
	if step <= 0 {
		return next
	}
	const maxSkips = 100000
	for i := 0; i < maxSkips && !next.After(now); i++ {
		next = next.Add(step)
	}
	return next
}

// nowFunc is overridable in tests so deterministic times can drive
// catch-up behavior. Production callers always observe time.Now.
var nowFunc = time.Now

// CalculateNextRun calculates the next execution time based on the schedule configuration.
func CalculateNextRun(config ScheduleConfig, lastRun time.Time) *time.Time {
	now := nowFunc()
	switch config.Type {
	case ScheduleOnce:
		// One-time execution: return ExecuteAt if task hasn't run yet, nil otherwise
		if config.ExecuteAt == nil {
			return nil
		}
		// If lastRun is zero (never run), return the scheduled time
		if lastRun.IsZero() {
			return config.ExecuteAt
		}
		// Already ran once, no next run
		return nil

	case ScheduleInterval:
		// Validate interval is non-zero
		if config.Interval == 0 {
			logger.Warn("Invalid interval schedule: interval is 0", logger.Fields{})
			return nil
		}
		// Simple arithmetic: lastRun + interval duration; if downtime caused
		// the computed tick to fall in the past, skip ahead to the first
		// future tick rather than catch-up firing.
		next := skipPastIntervals(lastRun.Add(config.Interval), now, config.Interval)

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
			logger.Warn("Invalid time_of_day format", logger.Fields{"error": err, "time_of_day": config.TimeOfDay})
			return nil
		}

		// Schedule for same time tomorrow (day + 1).
		// We use time.Date to handle month/year transitions automatically.
		// On long downtime, advance day-by-day until the tick is in the future.
		next := time.Date(lastRun.Year(), lastRun.Month(), lastRun.Day()+1, hour, minute, 0, 0, lastRun.Location())
		next = skipPastIntervals(next, now, 24*time.Hour)

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
			logger.Warn("Invalid time_of_day format", logger.Fields{"time_of_day": config.TimeOfDay, "error": err})
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
		// On long downtime, advance week-by-week until the tick is in the future.
		next = skipPastIntervals(next, now, 7*24*time.Hour)

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

		// Calculate next execution time from lastRun.
		// On long downtime, schedule.Next(lastRun) can still be in the past;
		// iterate forward until we land on the first tick after now.
		next := schedule.Next(lastRun)
		const maxSkips = 100000
		for i := 0; i < maxSkips && !next.After(now); i++ {
			advanced := schedule.Next(next)
			if !advanced.After(next) {
				// Defensive: cron returned a non-monotonic value; bail out.
				break
			}
			next = advanced
		}

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
