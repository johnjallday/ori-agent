package agentstudio

import (
	"context"
	"fmt"

	"github.com/johnjallday/ori-agent/internal/logger"
	"sync"
	"time"
)

// TaskExecutor handles automatic execution of workspace tasks
type TaskExecutor struct {
	workspaceStore Store
	taskHandler    TaskHandler
	pollInterval   time.Duration
	maxConcurrent  int
	eventBus       *EventBus // Optional event bus for publishing events

	mu           sync.RWMutex
	runningTasks map[string]*taskExecution
	stopChan     chan struct{}
	wg           sync.WaitGroup
}

// TaskHandler defines the interface for executing tasks
type TaskHandler interface {
	// ExecuteTask executes a task for a specific agent
	// Returns the result string and any error
	ExecuteTask(ctx context.Context, agentName string, task Task) (string, error)
}

// taskExecution tracks a running task
type taskExecution struct {
	Task      Task
	StartedAt time.Time
	Context   context.Context
	Cancel    context.CancelFunc
}

// ExecutorConfig contains configuration for the task executor
type ExecutorConfig struct {
	PollInterval  time.Duration // How often to check for new tasks
	MaxConcurrent int           // Max number of concurrent task executions
}

// NewTaskExecutor creates a new task executor
func NewTaskExecutor(store Store, handler TaskHandler, config ExecutorConfig) *TaskExecutor {
	if config.PollInterval == 0 {
		config.PollInterval = 10 * time.Second
	}
	if config.MaxConcurrent == 0 {
		config.MaxConcurrent = 5
	}

	return &TaskExecutor{
		workspaceStore: store,
		taskHandler:    handler,
		pollInterval:   config.PollInterval,
		maxConcurrent:  config.MaxConcurrent,
		runningTasks:   make(map[string]*taskExecution),
		stopChan:       make(chan struct{}),
	}
}

// SetEventBus sets the event bus for publishing task events
func (te *TaskExecutor) SetEventBus(eventBus *EventBus) {
	te.eventBus = eventBus
}

// Start begins the task executor polling loop
func (te *TaskExecutor) Start() {
	logger.Debug("🚀 Task executor started (poll interval: , max concurrent: )", logger.Fields{"maxconcurrent": te.maxConcurrent, "task_id": te.pollInterval})

	// Clean up orphaned tasks before starting
	te.cleanupOrphanedTasks()

	te.wg.Add(1)
	go te.pollLoop()
}

// cleanupOrphanedTasks resets tasks that were left in "in_progress" state from a previous server run
func (te *TaskExecutor) cleanupOrphanedTasks() {
	workspaceIDs, err := te.workspaceStore.List()
	if err != nil {
		logger.Error("Failed to list workspaces for orphaned task cleanup", logger.Fields{"workspace_id": err})
		return
	}

	totalReset := 0
	for _, wsID := range workspaceIDs {
		ws, err := te.workspaceStore.Get(wsID)
		if err != nil {
			continue
		}

		resetCount := 0
		for i := range ws.Tasks {
			task := &ws.Tasks[i]
			if task.Status == TaskStatusInProgress {
				task.Status = TaskStatusPending
				task.StartedAt = nil
				resetCount++
			}
		}

		if resetCount > 0 {
			if err := te.workspaceStore.Save(ws); err != nil {
				logger.Error("Failed to save workspace after orphaned task cleanup", logger.Fields{"workspace_id": wsID, "err": err})
			} else {
				totalReset += resetCount
				logger.Debug("🔄 Reset orphaned task(s) in workspace", logger.Fields{"task_id": resetCount, "wsID": wsID})
			}
		}
	}

	if totalReset > 0 {
		logger.Info("Cleaned up orphaned task(s) across all workspaces", logger.Fields{"task_id": totalReset})
	}
}

// Stop gracefully stops the task executor
func (te *TaskExecutor) Stop() {
	logger.Debug("⏹️ Stopping task executor...", logger.Fields{})
	close(te.stopChan)

	// Cancel all running tasks
	te.mu.Lock()
	for _, exec := range te.runningTasks {
		exec.Cancel()
	}
	te.mu.Unlock()

	te.wg.Wait()
	logger.Info("Task executor stopped", logger.Fields{})
}

// pollLoop continuously polls for new tasks
func (te *TaskExecutor) pollLoop() {
	defer te.wg.Done()

	ticker := time.NewTicker(te.pollInterval)
	defer ticker.Stop()

	// Run immediately on start
	te.checkAndExecuteTasks()

	for {
		select {
		case <-te.stopChan:
			return
		case <-ticker.C:
			te.checkAndExecuteTasks()
		}
	}
}

// checkAndExecuteTasks checks for pending tasks and executes them
func (te *TaskExecutor) checkAndExecuteTasks() {
	// Get all workspaces
	workspaceIDs, err := te.workspaceStore.List()
	if err != nil {
		logger.Error("Failed to list workspaces", logger.Fields{"workspace_id": err})
		return
	}

	for _, wsID := range workspaceIDs {
		ws, err := te.workspaceStore.Get(wsID)
		if err != nil {
			continue
		}

		// Only process active workspaces
		if ws.Status != StatusActive {
			continue
		}

		// Find tasks ready for execution
		for i := range ws.Tasks {
			task := &ws.Tasks[i]

			// Only auto-execute tasks with "assigned" status
			// Pending tasks require manual execution via the UI (click RUN button)
			if task.Status != TaskStatusAssigned {
				continue
			}

			// Skip if already running
			te.mu.RLock()
			_, isRunning := te.runningTasks[task.ID]
			te.mu.RUnlock()
			if isRunning {
				continue
			}

			// Check if we have capacity
			te.mu.RLock()
			canRun := len(te.runningTasks) < te.maxConcurrent
			te.mu.RUnlock()

			if !canRun {
				logger.Warn("Max concurrent tasks reached (), deferring task", logger.Fields{"error": te.maxConcurrent, "id": task.ID})
				continue
			}

			// Execute the task
			te.executeTask(ws, *task)
		}
	}
}

// executeTask executes a single task asynchronously
func (te *TaskExecutor) executeTask(ws *Workspace, task Task) {
	// Create context with timeout
	timeout := task.Timeout
	if timeout == 0 {
		timeout = 5 * time.Minute // Default timeout
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)

	// Track running task
	te.mu.Lock()
	te.runningTasks[task.ID] = &taskExecution{
		Task:      task,
		StartedAt: time.Now(),
		Context:   ctx,
		Cancel:    cancel,
	}
	te.mu.Unlock()

	logger.Debug("▶️ Executing task for agent", logger.Fields{"description": task.Description, "agent": task.ID, "to": task.To})

	// Inject input task results into task context if InputTaskIDs are specified
	if len(task.InputTaskIDs) > 0 {
		logger.Debug("🔗 Task has input task IDs", logger.Fields{"task_id": task.ID, "inputtaskids)": len(task.InputTaskIDs), "inputtaskids": task.InputTaskIDs})
		enrichedContext := ws.GetInputContext(&task)
		task.Context = enrichedContext

		// Debug: Check what was added to context
		if inputResults, ok := enrichedContext["input_task_results"]; ok {
			resultsMap := inputResults.(map[string]string)
			logger.Debug("Injected input task results into task context", logger.Fields{"result": len(resultsMap), "id": task.ID})
			for taskID, result := range resultsMap {
				preview := result
				if len(preview) > 100 {
					preview = preview[:100] + "..."
				}
				logger.Debug("- Task result", logger.Fields{"task_id": taskID, "preview": preview})
			}
		} else {
			logger.Warn("Warning: No input results found for task despite having InputTaskIDs", logger.Fields{"task_id": task.ID})
		}
	} else {
		logger.Debug("ℹ️ Task has no input task IDs", logger.Fields{"task_id": task.ID})
	}

	// Update task status to in_progress
	task.Status = TaskStatusInProgress
	now := time.Now()
	task.StartedAt = &now

	if err := ws.UpdateTask(task); err != nil {
		logger.Error("Failed to update task status", logger.Fields{"status": err})
	}
	if err := te.workspaceStore.Save(ws); err != nil {
		logger.Error("Failed to save workspace", logger.Fields{"workspace_id": err})
	}

	// Publish task started event
	if te.eventBus != nil {
		event := NewTaskEvent(EventTaskStarted, ws.ID, task.ID, task.To, map[string]interface{}{
			"description": task.Description,
			"priority":    task.Priority,
		})
		te.eventBus.Publish(event)
	}

	// Execute asynchronously
	te.wg.Add(1)
	go func() {
		defer te.wg.Done()
		defer cancel()
		defer func() {
			te.mu.Lock()
			delete(te.runningTasks, task.ID)
			te.mu.Unlock()
		}()

		// Execute the task
		result, err := te.taskHandler.ExecuteTask(ctx, task.To, task)

		// Reload workspace (may have changed)
		ws, wsErr := te.workspaceStore.Get(ws.ID)
		if wsErr != nil {
			logger.Error("Failed to reload workspace", logger.Fields{"workspace_id": ws.ID, "wsErr": wsErr})
			return
		}

		// Find the task in the reloaded workspace
		var updatedTask *Task
		for i := range ws.Tasks {
			if ws.Tasks[i].ID == task.ID {
				updatedTask = &ws.Tasks[i]
				break
			}
		}

		if updatedTask == nil {
			logger.Error("Task not found in workspace after execution", logger.Fields{"task_id": task.ID})
			return
		}

		// Update task with result
		completedAt := time.Now()
		updatedTask.CompletedAt = &completedAt

		if err != nil {
			logger.Error("Task failed", logger.Fields{"task_id": task.ID, "err": err})
			updatedTask.Status = TaskStatusFailed
			updatedTask.Error = err.Error()

			// Publish task failed event
			if te.eventBus != nil {
				event := NewTaskEvent(EventTaskFailed, ws.ID, task.ID, task.To, map[string]interface{}{
					"description": task.Description,
					"error":       err.Error(),
				})
				te.eventBus.Publish(event)
			}
		} else {
			logger.Info("Task completed successfully", logger.Fields{"task_id": task.ID})
			updatedTask.Status = TaskStatusCompleted
			updatedTask.Result = result

			// Publish task completed event
			if te.eventBus != nil {
				event := NewTaskEvent(EventTaskCompleted, ws.ID, task.ID, task.To, map[string]interface{}{
					"description": task.Description,
					"result":      result,
				})
				te.eventBus.Publish(event)
			}
		}

		// Save updated task
		if err := ws.UpdateTask(*updatedTask); err != nil {
			logger.Error("Failed to update task", logger.Fields{"task_id": err})
			return
		}
		if err := te.workspaceStore.Save(ws); err != nil {
			logger.Error("Failed to save workspace", logger.Fields{"workspace_id": err})
		}

		// Publish workspace updated event
		if te.eventBus != nil {
			event := NewWorkspaceEvent(EventWorkspaceUpdated, ws.ID, "task-executor", map[string]interface{}{
				"task_id": task.ID,
				"status":  updatedTask.Status,
			})
			te.eventBus.Publish(event)
		}
	}()
}

// GetRunningTaskCount returns the number of currently running tasks
func (te *TaskExecutor) GetRunningTaskCount() int {
	te.mu.RLock()
	defer te.mu.RUnlock()
	return len(te.runningTasks)
}

// GetRunningTasks returns a list of currently running task IDs
func (te *TaskExecutor) GetRunningTasks() []string {
	te.mu.RLock()
	defer te.mu.RUnlock()

	tasks := make([]string, 0, len(te.runningTasks))
	for id := range te.runningTasks {
		tasks = append(tasks, id)
	}
	return tasks
}

// CancelTask cancels a running task
func (te *TaskExecutor) CancelTask(taskID string) error {
	te.mu.Lock()
	defer te.mu.Unlock()

	exec, exists := te.runningTasks[taskID]
	if !exists {
		return fmt.Errorf("task %s is not currently running", taskID)
	}

	exec.Cancel()
	logger.Debug("🚫 Task cancelled", logger.Fields{"task_id": taskID})

	return nil
}
