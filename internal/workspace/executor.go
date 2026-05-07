package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/platform"
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
	logger.Debug("Task executor started", logger.Fields{"max_concurrent": te.maxConcurrent, "poll_interval": te.pollInterval})

	// Clean up orphaned tasks before starting
	te.cleanupOrphanedTasks()

	te.wg.Add(1)
	go te.pollLoop()
}

// cleanupOrphanedTasks resets tasks that were left in "in_progress" state from a previous server run
func (te *TaskExecutor) cleanupOrphanedTasks() {
	workspaceIDs, err := te.workspaceStore.List()
	if err != nil {
		logger.Error("Failed to list workspaces for orphaned task cleanup", logger.Fields{"error": err})
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
				logger.Debug("Reset orphaned tasks in workspace", logger.Fields{"count": resetCount, "workspace_id": wsID})
			}
		}
	}

	if totalReset > 0 {
		logger.Info("Cleaned up orphaned task(s) across all workspaces", logger.Fields{"task_id": totalReset})
	}
}

// Stop gracefully stops the task executor
func (te *TaskExecutor) Stop() {
	logger.Debug("Stopping task executor", logger.Fields{})
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
		logger.Error("Failed to list workspaces", logger.Fields{"error": err})
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

			// Check if already running and claim the task atomically
			// Use write lock to prevent race condition between check and insert
			te.mu.Lock()
			_, isRunning := te.runningTasks[task.ID]
			if isRunning {
				te.mu.Unlock()
				continue
			}

			// Check if we have capacity
			if len(te.runningTasks) >= te.maxConcurrent {
				te.mu.Unlock()
				logger.Warn("Max concurrent tasks reached, deferring task", logger.Fields{"max_concurrent": te.maxConcurrent, "task_id": task.ID})
				continue
			}

			// Mark task as claimed immediately to prevent double execution
			// Create a placeholder execution entry that will be replaced by executeTask
			te.runningTasks[task.ID] = &taskExecution{
				Task:      *task,
				StartedAt: time.Now(),
			}
			te.mu.Unlock()

			// Execute the task (it will update the runningTasks entry with full context)
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
	// NOTE: Don't defer cancel() here because we launch a goroutine below
	// The goroutine will defer cancel() when it completes (see line ~290)

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
			resultsMap, ok := inputResults.(map[string]string)
			if !ok {
				logger.Warn("Unexpected input_task_results type", logger.Fields{"task_id": task.ID})
			} else {
				logger.Debug("Injected input task results into task context", logger.Fields{"result": len(resultsMap), "id": task.ID})
				for taskID, result := range resultsMap {
					preview := result
					if len(preview) > 100 {
						preview = preview[:100] + "..."
					}
					logger.Debug("- Task result", logger.Fields{"task_id": taskID, "preview": preview})
				}
			}
		} else {
			logger.Warn("Warning: No input results found for task despite having InputTaskIDs", logger.Fields{"task_id": task.ID})
		}
	} else {
		logger.Debug("ℹ️ Task has no input task IDs", logger.Fields{"task_id": task.ID})
	}

	// Update task status to in_progress
	now := time.Now()
	task.Status = TaskStatusInProgress
	task.StartedAt = &now

	if err := MutateTaskAndSave(te.workspaceStore, ws, task.ID, func(t *Task) error {
		t.Status = TaskStatusInProgress
		t.StartedAt = &now
		return nil
	}); err != nil {
		logger.Error("Failed to mark task in_progress", logger.Fields{"task_id": task.ID, "error": err})
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

		// Apply post-execution result/status atomically against the authoritative
		// workspace state via Store.Update. The closure captures a snapshot for
		// post-mutation event publishing and best-effort store I/O, which run
		// after the per-workspace lock is released.
		var (
			snapshot    Task
			blockedErr  *TaskBlockedError
			completedAt = time.Now()
			workspaceID = ws.ID
		)
		if mutErr := te.workspaceStore.Update(workspaceID, func(fresh *Workspace) error {
			return fresh.MutateTask(task.ID, func(t *Task) error {
				t.CompletedAt = &completedAt
				startedAt := completedAt
				if t.StartedAt != nil && !t.StartedAt.IsZero() {
					startedAt = *t.StartedAt
				}

				if err != nil {
					logger.Error("Task failed", logger.Fields{"task_id": task.ID, "err": err})
					executionStatus := "failed"
					executionSummary := err.Error()
					if be, ok := AsTaskBlockedError(err); ok {
						blockedErr = be
						executionStatus = "blocked"
						executionSummary = be.Error()
						if strings.TrimSpace(be.RawResponse) != "" {
							executionSummary = be.RawResponse
						}
						t.CompletedAt = nil
						t.Status = TaskStatusWaitingForChoice
						t.Error = ""
						t.Result = ""
						ApplyTaskResultMetadata(t, "")
						applyExecutorTaskBlockedContext(t, be)
					} else {
						t.Status = TaskStatusFailed
						t.Error = err.Error()
					}
					RecordTaskExecution(t, executionStatus, executionSummary, startedAt, completedAt.Sub(startedAt))
				} else {
					logger.Info("Task completed successfully", logger.Fields{"task_id": task.ID})
					t.Status = TaskStatusCompleted
					t.Result = result
					ApplyTaskResultMetadata(t, result)
					RecordTaskExecution(t, "success", result, startedAt, completedAt.Sub(startedAt))
				}

				if te.eventBus != nil {
					RecordTaskExecutionTraceFromEventBus(t, te.eventBus, workspaceID, task.ID, startedAt, completedAt)
				}

				snapshot = *t
				return nil
			})
		}); mutErr != nil {
			logger.Error("Failed to update task", logger.Fields{"task_id": task.ID, "error": mutErr})
			return
		}

		// Post-mutation side effects (lock released).
		if err != nil {
			if te.eventBus != nil {
				if blockedErr != nil {
					te.eventBus.Publish(NewTaskEvent(EventTaskBlocked, workspaceID, task.ID, task.To, map[string]interface{}{
						"description": task.Description,
						"human_loop":  snapshot.Context["human_loop"],
						"status":      snapshot.Status,
						"error":       blockedErr.Error(),
					}))
				} else {
					te.eventBus.Publish(NewTaskEvent(EventTaskFailed, workspaceID, task.ID, task.To, map[string]interface{}{
						"description": task.Description,
						"error":       err.Error(),
					}))
				}
			}
		} else {
			// Refresh ws so autoStoreResult sees the post-mutation workspace.
			// autoStoreResult is best-effort and does its own Save outside the
			// per-workspace lock, so a concurrent Update can interleave; that's
			// accepted today (store-node bookkeeping is not load-bearing).
			if fresh, getErr := te.workspaceStore.Get(workspaceID); getErr == nil {
				te.autoStoreResult(fresh, &task, result)
			}

			if te.eventBus != nil {
				te.eventBus.Publish(NewTaskEvent(EventTaskCompleted, workspaceID, task.ID, task.To, map[string]interface{}{
					"description": task.Description,
					"result":      result,
				}))
			}
		}

		// Publish workspace updated event
		if te.eventBus != nil {
			te.eventBus.Publish(NewWorkspaceEvent(EventWorkspaceUpdated, workspaceID, "task-executor", map[string]interface{}{
				"task_id": task.ID,
				"status":  snapshot.Status,
			}))
		}
	}()
}

func applyExecutorTaskBlockedContext(task *Task, blockedErr *TaskBlockedError) {
	if task == nil {
		return
	}
	if task.Context == nil {
		task.Context = map[string]interface{}{}
	}

	blockID := fmt.Sprintf("blk_%d", time.Now().UnixNano())
	if existing, ok := task.Context["human_loop"].(map[string]interface{}); ok {
		if prior, ok := existing["block_id"].(string); ok && strings.TrimSpace(prior) != "" {
			blockID = strings.TrimSpace(prior)
		}
	}

	humanLoop := map[string]interface{}{
		"state":       "waiting_for_choice",
		"block_id":    blockID,
		"reason_code": "blocked",
		"updated_at":  time.Now().UTC().Format(time.RFC3339),
	}
	if blockedErr != nil {
		if reasonCode := strings.TrimSpace(blockedErr.ReasonCode); reasonCode != "" {
			humanLoop["reason_code"] = reasonCode
		}
		if reason := strings.TrimSpace(blockedErr.Reason); reason != "" {
			humanLoop["reason"] = reason
		}
		if question := strings.TrimSpace(blockedErr.Question); question != "" {
			humanLoop["question"] = question
		}
		if len(blockedErr.SuggestedActions) > 0 {
			humanLoop["suggested_actions"] = blockedErr.SuggestedActions
		}
		if raw := strings.TrimSpace(blockedErr.RawResponse); raw != "" {
			humanLoop["agent_response"] = raw
		}
		if workflowStep := PrepareTaskBlockedWorkflowStep(blockedErr.WorkflowStep, blockedErr.ReasonCode); workflowStep != nil && (len(workflowStep.Choices) > 0 || len(workflowStep.Fields) > 0) {
			humanLoop["workflow_step"] = workflowStep
		}
	}
	task.Context["human_loop"] = humanLoop
}

// AutoStoreResult automatically stores task result based on:
// 1. Task-level ResultStorage configuration (if enabled)
// 2. Agent's connected store node (if auto-store enabled)
// This is a package-level function so it can be called from both executor and HTTP handlers
func AutoStoreResult(ws *Workspace, task *Task, result string, workspaceStore Store) {
	// Check for task-level result storage configuration first
	if task.ResultStorage != nil && task.ResultStorage.Enabled {
		autoStoreTaskResult(ws, task, result, workspaceStore)
		return
	}

	// Fall back to agent-based store node lookup
	// Find agent's canvas node ID (use AssignedNodeID for multi-instance agents)
	agentNodeID := task.AssignedNodeID
	if agentNodeID == "" || agentNodeID == "unassigned" {
		return
	}

	// Find store node assigned to this agent
	var assignedStore *StoreNode
	for i := range ws.StoreNodes {
		if ws.StoreNodes[i].AgentNodeID == agentNodeID {
			assignedStore = &ws.StoreNodes[i]
			break
		}
	}

	// No store assigned - skip automatic storage
	if assignedStore == nil {
		return
	}

	// Check if auto-store is enabled for this store node
	if !assignedStore.AutoStore {
		return
	}

	// Generate filename: task-{short-id}-{timestamp}.{format}
	taskIDShort := task.ID
	if len(taskIDShort) > 8 {
		taskIDShort = taskIDShort[:8]
	}
	timestamp := time.Now().Format("20060102-150405")

	// Determine file extension based on store format
	ext := "txt"
	switch assignedStore.Format {
	case "json":
		ext = "json"
	case "markdown":
		ext = "md"
	case "text":
		ext = "txt"
	case "binary":
		ext = "bin"
	}

	filename := fmt.Sprintf("task-%s-%s.%s", taskIDShort, timestamp, ext)

	// Prepare data for storage
	dataToStore := result
	if assignedStore.Format == "json" {
		// Wrap plain text result in JSON structure
		jsonData := map[string]interface{}{
			"task_id":     task.ID,
			"agent":       agentNodeID,
			"result":      result,
			"timestamp":   timestamp,
			"description": task.Description,
		}
		jsonBytes, err := json.Marshal(jsonData)
		if err != nil {
			logger.Error("Failed to marshal result to JSON", logger.Fields{
				"task_id": task.ID,
				"err":     err,
			})
			return
		}
		dataToStore = string(jsonBytes)
	}

	// Write result to store
	if err := WriteToStore(assignedStore, filename, dataToStore); err != nil {
		logger.Error("Failed to auto-store task result", logger.Fields{
			"task_id":       task.ID,
			"store_node_id": assignedStore.ID,
			"filename":      filename,
			"err":           err,
		})
		// Don't fail the task - storage is best-effort
		return
	}

	logger.Info("✅ Task result auto-stored", logger.Fields{
		"task_id":       task.ID,
		"store_node_id": assignedStore.ID,
		"filename":      filename,
		"write_count":   assignedStore.WriteCount,
	})

	// Save workspace to persist store node stats (WriteToStore updated them)
	if err := workspaceStore.Save(ws); err != nil {
		logger.Error("Failed to save workspace after auto-store", logger.Fields{"workspace_id": ws.ID, "err": err})
	}
}

// autoStoreTaskResult handles task-level result storage configuration
func autoStoreTaskResult(ws *Workspace, task *Task, result string, workspaceStore Store) {
	storage := task.ResultStorage
	if storage == nil || !storage.Enabled {
		return
	}

	// Generate filename
	timestamp := time.Now().Format("20060102-150405")

	// Determine format and extension
	format := storage.Format
	if format == "" {
		format = "text"
	}
	ext := "txt"
	switch format {
	case "json":
		ext = "json"
	case "markdown":
		ext = "md"
	}

	// Generate task name slug for filename
	taskName := task.Description
	if len(taskName) > 30 {
		taskName = taskName[:30]
	}
	// Sanitize: replace non-alphanumeric with underscore
	sanitized := ""
	for _, r := range taskName {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			sanitized += string(r)
		} else if r == ' ' {
			sanitized += "_"
		}
	}
	if sanitized == "" {
		sanitized = "task"
	}

	filename := fmt.Sprintf("%s_%s.%s", sanitized, timestamp, ext)

	// Prepare data for storage
	dataToStore := result
	if format == "json" {
		jsonData := map[string]interface{}{
			"task_id":     task.ID,
			"result":      result,
			"timestamp":   timestamp,
			"description": task.Description,
		}
		jsonBytes, err := json.Marshal(jsonData)
		if err != nil {
			logger.Error("Failed to marshal result to JSON", logger.Fields{"task_id": task.ID, "err": err})
			return
		}
		dataToStore = string(jsonBytes)
	}

	// If store node is specified, use it
	if storage.StoreNodeID != "" {
		var storeNode *StoreNode
		for i := range ws.StoreNodes {
			if ws.StoreNodes[i].ID == storage.StoreNodeID || ws.StoreNodes[i].CanvasNodeID == storage.StoreNodeID {
				storeNode = &ws.StoreNodes[i]
				break
			}
		}

		if storeNode == nil {
			logger.Error("Store node not found for task result storage", logger.Fields{
				"task_id":       task.ID,
				"store_node_id": storage.StoreNodeID,
			})
			return
		}

		if err := WriteToStore(storeNode, filename, dataToStore); err != nil {
			logger.Error("Failed to auto-store task result to store node", logger.Fields{
				"task_id":       task.ID,
				"store_node_id": storeNode.ID,
				"filename":      filename,
				"err":           err,
			})
			return
		}

		logger.Info("Task result auto-stored to store node", logger.Fields{
			"task_id":       task.ID,
			"store_node_id": storeNode.ID,
			"filename":      filename,
		})

		if err := workspaceStore.Save(ws); err != nil {
			logger.Error("Failed to save workspace after auto-store", logger.Fields{"workspace_id": ws.ID, "err": err})
		}
		return
	}

	// Otherwise use file path (or default output directory)
	filePath := storage.FilePath
	if filePath == "" {
		// Default to workspace output directory: ~/Documents/Ori/outputs/<workspace-name>/
		baseOutputDir, err := platform.GetDefaultOutputDir()
		if err != nil {
			// Fallback to relative path if home dir lookup fails
			baseOutputDir = "outputs"
			logger.Warn("Failed to get default output dir, using fallback", logger.Fields{"error": err})
		}
		filePath = filepath.Join(baseOutputDir, ws.Name, filename)
	} else {
		// If user specified a directory-like path, append filename
		if strings.HasSuffix(filePath, "/") || !strings.Contains(filepath.Base(filePath), ".") {
			filePath = filepath.Join(filePath, filename)
		}
	}

	// Create directories
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		logger.Error("Failed to create directories for task result", logger.Fields{
			"task_id": task.ID,
			"dir":     dir,
			"err":     err,
		})
		return
	}

	// Write file
	if err := os.WriteFile(filePath, []byte(dataToStore), 0644); err != nil {
		logger.Error("Failed to auto-store task result to file", logger.Fields{
			"task_id":   task.ID,
			"file_path": filePath,
			"err":       err,
		})
		return
	}

	logger.Info("Task result auto-stored to file", logger.Fields{
		"task_id":   task.ID,
		"file_path": filePath,
	})
}

// autoStoreResult is a convenience wrapper that calls AutoStoreResult with the executor's workspace store
func (te *TaskExecutor) autoStoreResult(ws *Workspace, task *Task, result string) {
	AutoStoreResult(ws, task, result, te.workspaceStore)
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
