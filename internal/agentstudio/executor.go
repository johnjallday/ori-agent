package agentstudio

import (
	"context"
	"fmt"
	"log"
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
	log.Printf("🚀 Task executor started (poll interval: %v, max concurrent: %d)", te.pollInterval, te.maxConcurrent)

	te.wg.Add(1)
	go te.pollLoop()
}

// Stop gracefully stops the task executor
func (te *TaskExecutor) Stop() {
	log.Printf("⏹️  Stopping task executor...")
	close(te.stopChan)

	// Cancel all running tasks
	te.mu.Lock()
	for _, exec := range te.runningTasks {
		exec.Cancel()
	}
	te.mu.Unlock()

	te.wg.Wait()
	log.Printf("✅ Task executor stopped")
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
		log.Printf("⚠️  Failed to list workspaces: %v", err)
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
				log.Printf("⚠️  Max concurrent tasks reached (%d), deferring task %s", te.maxConcurrent, task.ID)
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

	log.Printf("▶️  Executing task %s for agent %s: %s", task.ID, task.To, task.Description)

	// Inject input task results into task context if InputTaskIDs are specified
	if len(task.InputTaskIDs) > 0 {
		log.Printf("🔗 Task %s has %d input task IDs: %v", task.ID, len(task.InputTaskIDs), task.InputTaskIDs)
		enrichedContext := ws.GetInputContext(&task)
		task.Context = enrichedContext

		// Debug: Check what was added to context
		if inputResults, ok := enrichedContext["input_task_results"]; ok {
			resultsMap := inputResults.(map[string]string)
			log.Printf("📥 Injected %d input task results into task %s context", len(resultsMap), task.ID)
			for taskID, result := range resultsMap {
				preview := result
				if len(preview) > 100 {
					preview = preview[:100] + "..."
				}
				log.Printf("   - Task %s result: %s", taskID, preview)
			}
		} else {
			log.Printf("⚠️  Warning: No input results found for task %s despite having InputTaskIDs", task.ID)
		}
	} else {
		log.Printf("ℹ️  Task %s has no input task IDs", task.ID)
	}

	// Update task status to in_progress
	task.Status = TaskStatusInProgress
	now := time.Now()
	task.StartedAt = &now

	if err := ws.UpdateTask(task); err != nil {
		log.Printf("⚠️  Failed to update task status: %v", err)
	}
	if err := te.workspaceStore.Save(ws); err != nil {
		log.Printf("⚠️  Failed to save workspace: %v", err)
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
			log.Printf("❌ Failed to reload workspace %s: %v", ws.ID, wsErr)
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
			log.Printf("❌ Task %s not found in workspace after execution", task.ID)
			return
		}

		// Update task with result
		completedAt := time.Now()
		updatedTask.CompletedAt = &completedAt

		if err != nil {
			log.Printf("❌ Task %s failed: %v", task.ID, err)
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
			log.Printf("✅ Task %s completed successfully", task.ID)
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
			log.Printf("⚠️  Failed to update task: %v", err)
			return
		}
		if err := te.workspaceStore.Save(ws); err != nil {
			log.Printf("⚠️  Failed to save workspace: %v", err)
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
	log.Printf("🚫 Task %s cancelled", taskID)

	return nil
}
