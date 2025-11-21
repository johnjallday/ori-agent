package agentstudio

import (
	"fmt"
	"log"
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
	log.Printf("📅 Task scheduler started (poll interval: %v)", ts.pollInterval)

	ts.wg.Add(1)
	go ts.pollLoop()
}

// Stop gracefully stops the scheduler
func (ts *TaskScheduler) Stop() {
	log.Printf("⏹️  Stopping task scheduler...")
	close(ts.stopChan)
	ts.wg.Wait()
	log.Printf("✅ Task scheduler stopped")
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

// checkScheduledTasks checks all workspaces for scheduled tasks that need to run
func (ts *TaskScheduler) checkScheduledTasks() {
	workspaceIDs, err := ts.workspaceStore.List()
	if err != nil {
		log.Printf("⚠️  Failed to list workspaces: %v", err)
		return
	}

	now := time.Now()

	for _, wsID := range workspaceIDs {
		ws, err := ts.workspaceStore.Get(wsID)
		if err != nil {
			continue
		}

		// Only process active workspaces
		if ws.Status != StatusActive {
			continue
		}

		// Check each enabled scheduled task
		for i := range ws.ScheduledTasks {
			st := &ws.ScheduledTasks[i]

			// Skip disabled tasks
			if !st.Enabled {
				continue
			}

			// Check if it's time to run
			if st.NextRun == nil || st.NextRun.After(now) {
				continue
			}

			// Check if max runs reached
			if st.Schedule.MaxRuns > 0 && st.ExecutionCount >= st.Schedule.MaxRuns {
				log.Printf("📅 Scheduled task %s reached max runs (%d), disabling", st.ID, st.Schedule.MaxRuns)
				st.Enabled = false
				st.NextRun = nil
				if err := ws.UpdateScheduledTask(*st); err != nil {
					log.Printf("❌ Failed to update scheduled task: %v", err)
				}
				if err := ts.workspaceStore.Save(ws); err != nil {
					log.Printf("❌ Failed to save workspace: %v", err)
				}
				continue
			}

			// Check if end date passed
			if st.Schedule.EndDate != nil && now.After(*st.Schedule.EndDate) {
				log.Printf("📅 Scheduled task %s passed end date, disabling", st.ID)
				st.Enabled = false
				st.NextRun = nil
				if err := ws.UpdateScheduledTask(*st); err != nil {
					log.Printf("❌ Failed to update scheduled task: %v", err)
				}
				if err := ts.workspaceStore.Save(ws); err != nil {
					log.Printf("❌ Failed to save workspace: %v", err)
				}
				continue
			}

			// Execute the scheduled task
			ts.executeScheduledTask(ws, st)
		}
	}
}

// executeScheduledTask creates a Task from a ScheduledTask and updates the schedule
func (ts *TaskScheduler) executeScheduledTask(ws *Workspace, st *ScheduledTask) {
	log.Printf("📅 Executing scheduled task %s: %s", st.ID, st.Name)

	// Create a regular Task from the ScheduledTask
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
		log.Printf("❌ Failed to create task from scheduled task %s: %v", st.ID, err)
		st.FailureCount++
		st.LastError = err.Error()

		// Record failed execution in history
		execution := TaskExecution{
			TaskID:     "", // No task was created
			ExecutedAt: time.Now(),
			Status:     "failed",
			Error:      err.Error(),
		}
		st.ExecutionHistory = append(st.ExecutionHistory, execution)

		// Keep only last 20 executions
		if len(st.ExecutionHistory) > 20 {
			st.ExecutionHistory = st.ExecutionHistory[len(st.ExecutionHistory)-20:]
		}

		// Optionally disable after consecutive failures
		if st.FailureCount >= 5 {
			log.Printf("⚠️  Scheduled task %s disabled after %d consecutive failures", st.ID, st.FailureCount)
			st.Enabled = false
		}

		if err := ws.UpdateScheduledTask(*st); err != nil {
			log.Printf("❌ Failed to update scheduled task: %v", err)
		}
		if err := ts.workspaceStore.Save(ws); err != nil {
			log.Printf("❌ Failed to save workspace: %v", err)
		}
		return
	}

	// Get the created task ID (it's the last task in the list)
	var createdTaskID string
	if len(ws.Tasks) > 0 {
		createdTaskID = ws.Tasks[len(ws.Tasks)-1].ID
	}

	// Update execution tracking
	now := time.Now()
	st.LastRun = &now
	st.ExecutionCount++
	st.FailureCount = 0 // Reset failure count on successful task creation

	// Record successful execution in history
	execution := TaskExecution{
		TaskID:     createdTaskID,
		ExecutedAt: now,
		Status:     "success",
	}
	st.ExecutionHistory = append(st.ExecutionHistory, execution)

	// Keep only last 20 executions
	if len(st.ExecutionHistory) > 20 {
		st.ExecutionHistory = st.ExecutionHistory[len(st.ExecutionHistory)-20:]
	}

	// Calculate next run time
	nextRun := ts.calculateNextRun(st.Schedule, now)
	st.NextRun = nextRun

	// If this was a "once" schedule or no next run, disable the task
	if nextRun == nil {
		st.Enabled = false
		log.Printf("📅 Scheduled task %s completed (one-time execution), disabling", st.ID)
	}

	// Update the scheduled task
	if err := ws.UpdateScheduledTask(*st); err != nil {
		log.Printf("❌ Failed to update scheduled task: %v", err)
		return
	}

	// Save workspace
	if err := ts.workspaceStore.Save(ws); err != nil {
		log.Printf("❌ Failed to save workspace: %v", err)
		return
	}

	log.Printf("✅ Scheduled task %s executed successfully (next run: %v)", st.ID, nextRun)

	// Publish event
	if ts.eventBus != nil {
		event := NewWorkspaceEvent(EventWorkspaceUpdated, ws.ID, "scheduler", map[string]interface{}{
			"scheduled_task_id": st.ID,
			"task_created":      true,
			"execution_count":   st.ExecutionCount,
			"next_run":          nextRun,
		})
		ts.eventBus.Publish(event)
	}
}

// calculateNextRun calculates the next execution time based on the schedule configuration
func (ts *TaskScheduler) calculateNextRun(config ScheduleConfig, lastRun time.Time) *time.Time {
	switch config.Type {
	case ScheduleOnce:
		// One-time execution, no next run
		return nil

	case ScheduleInterval:
		if config.Interval == 0 {
			log.Printf("⚠️  Invalid interval schedule: interval is 0")
			return nil
		}
		next := lastRun.Add(config.Interval)

		// Check if next run exceeds end date
		if config.EndDate != nil && next.After(*config.EndDate) {
			return nil
		}

		return &next

	case ScheduleDaily:
		if config.TimeOfDay == "" {
			log.Printf("⚠️  Invalid daily schedule: time_of_day is empty")
			return nil
		}

		// Parse time of day (format: "HH:MM")
		var hour, minute int
		if _, err := fmt.Sscanf(config.TimeOfDay, "%d:%d", &hour, &minute); err != nil {
			log.Printf("⚠️  Invalid time_of_day format %s: %v", config.TimeOfDay, err)
			return nil
		}

		// Start from the day after lastRun
		next := time.Date(lastRun.Year(), lastRun.Month(), lastRun.Day()+1, hour, minute, 0, 0, lastRun.Location())

		// Check if next run exceeds end date
		if config.EndDate != nil && next.After(*config.EndDate) {
			return nil
		}

		return &next

	case ScheduleWeekly:
		if config.TimeOfDay == "" {
			log.Printf("⚠️  Invalid weekly schedule: time_of_day is empty")
			return nil
		}

		// Parse time of day
		var hour, minute int
		if _, err := fmt.Sscanf(config.TimeOfDay, "%d:%d", &hour, &minute); err != nil {
			log.Printf("⚠️  Invalid time_of_day format %s: %v", config.TimeOfDay, err)
			return nil
		}

		// Find next occurrence of the target day of week
		targetWeekday := time.Weekday(config.DayOfWeek)
		currentWeekday := lastRun.Weekday()

		// Calculate days until next occurrence
		daysUntil := int(targetWeekday - currentWeekday)
		if daysUntil <= 0 {
			daysUntil += 7 // Next week
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
		// TODO: Implement cron expression parsing (Phase 4)
		log.Printf("⚠️  Cron schedules not yet implemented")
		return nil

	default:
		log.Printf("⚠️  Unknown schedule type: %s", config.Type)
		return nil
	}
}
