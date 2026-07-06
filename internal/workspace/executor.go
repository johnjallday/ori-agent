package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/platform"
)

// TaskExecutor handles automatic execution of workspace tasks
type TaskExecutor struct {
	workspaceStore         Store
	taskHandler            TaskHandler
	pollInterval           time.Duration
	maxConcurrent          int
	localTimeoutMultiplier int
	eventBus               *EventBus // Optional event bus for publishing events

	providerResolver TaskProviderResolver // optional; overrides taskHandler assertion

	mu           sync.RWMutex
	runningTasks map[string]*taskExecution
	runningByKey map[string]int // per concurrency-key running counts (WS6.23)
	stopChan     chan struct{}
	wg           sync.WaitGroup
}

// SetProviderResolver wires provider-profile resolution for scheduling (WS6).
// This is needed when the execution handler is a wrapper (e.g. a run bridge) that
// does not itself implement TaskProviderResolver; without it the executor falls
// back to type-asserting the task handler.
func (te *TaskExecutor) SetProviderResolver(resolver TaskProviderResolver) {
	te.providerResolver = resolver
}

// resolveProviderProfile resolves a task's provider profile, preferring an
// explicitly wired resolver over a type-asserted handler.
func (te *TaskExecutor) resolveProviderProfile(task Task) TaskProviderProfile {
	resolver := te.providerResolver
	if resolver == nil {
		resolver, _ = te.taskHandler.(TaskProviderResolver)
	}
	if resolver == nil {
		return TaskProviderProfile{}
	}
	return resolver.ResolveTaskProviderProfile(task)
}

// TaskProviderProfile describes a task's resolved provider for scheduling
// decisions, so the executor can respect local hardware without coupling to the
// LLM layer (WS6).
type TaskProviderProfile struct {
	// ConcurrencyKey scopes a per-instance concurrency limit ("" = global pool
	// only, i.e. cloud providers).
	ConcurrencyKey string
	// Limit is the max concurrent tasks for ConcurrencyKey (<= 0 = unlimited).
	Limit int
	// IsLocal reports whether the task runs on a local provider.
	IsLocal bool
	// OrderKey groups identical provider+model tasks consecutively within a poll
	// cycle to reduce model-swap churn (empty for cloud).
	OrderKey string
}

// TaskProviderResolver lets the executor learn a task's provider profile. It is
// an optional capability of the task handler; when absent the executor falls
// back to the global concurrency pool and default timeout (cloud behavior).
type TaskProviderResolver interface {
	ResolveTaskProviderProfile(task Task) TaskProviderProfile
}

var errSkipOrphanCleanup = errors.New("skip orphan cleanup")

// TaskHandler defines the interface for executing tasks
type TaskHandler interface {
	// ExecuteTask executes a task for a specific agent
	// Returns the result string and any error
	ExecuteTask(ctx context.Context, agentName string, task Task) (string, error)
}

// taskExecution tracks a running task
type taskExecution struct {
	Task           Task
	StartedAt      time.Time
	Context        context.Context
	Cancel         context.CancelFunc
	ConcurrencyKey string // per-instance key held for the duration (WS6.23)
}

// defaultTaskTimeout is the fallback per-task timeout when a task sets none.
const defaultTaskTimeout = 5 * time.Minute

// defaultLocalTimeoutMultiplier scales the default timeout for local providers to
// absorb cold model loads (WS6.25).
const defaultLocalTimeoutMultiplier = 2

// effectiveTaskTimeout returns the timeout for a task: an explicit task timeout is
// honored as-is; otherwise the default is scaled by the local multiplier for
// local-provider tasks (WS6.25).
func effectiveTaskTimeout(taskTimeout time.Duration, isLocal bool, multiplier int) time.Duration {
	if taskTimeout != 0 {
		return taskTimeout
	}
	timeout := defaultTaskTimeout
	if isLocal && multiplier > 1 {
		timeout *= time.Duration(multiplier)
	}
	return timeout
}

// ExecutorConfig contains configuration for the task executor
type ExecutorConfig struct {
	PollInterval           time.Duration // How often to check for new tasks
	MaxConcurrent          int           // Max number of concurrent task executions
	LocalTimeoutMultiplier int           // Multiplies the default timeout for local providers (0 = default)
}

// NewTaskExecutor creates a new task executor
func NewTaskExecutor(store Store, handler TaskHandler, config ExecutorConfig) *TaskExecutor {
	if config.PollInterval == 0 {
		config.PollInterval = 10 * time.Second
	}
	if config.MaxConcurrent == 0 {
		config.MaxConcurrent = 5
	}
	if config.LocalTimeoutMultiplier <= 0 {
		config.LocalTimeoutMultiplier = defaultLocalTimeoutMultiplier
	}

	return &TaskExecutor{
		workspaceStore:         store,
		taskHandler:            handler,
		pollInterval:           config.PollInterval,
		maxConcurrent:          config.MaxConcurrent,
		localTimeoutMultiplier: config.LocalTimeoutMultiplier,
		runningTasks:           make(map[string]*taskExecution),
		runningByKey:           make(map[string]int),
		stopChan:               make(chan struct{}),
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

// cleanupOrphanedTasks resets tasks that were left in "in_progress" state
// from a previous server run.
//
// Per-workspace mutation runs inside Store.Update so the read-modify-save
// is serialized against other instances and against in-flight handlers
// touching the same workspace. The previous implementation did Get →
// mutate → Save without per-workspace locking, which on multi-instance
// deployments could (a) clobber a concurrent mutation between the Get and
// the Save, and (b) let two boot-time cleanups race each other.
func (te *TaskExecutor) cleanupOrphanedTasks() {
	workspaces, err := te.workspaceStore.ListActive()
	if err != nil {
		logger.Error("Failed to list workspaces for orphaned task cleanup", logger.Fields{"error": err})
		return
	}

	totalReset := 0
	for _, ws := range workspaces {
		if ws == nil {
			continue
		}

		current, err := te.workspaceStore.Get(ws.ID)
		if err != nil || !hasInProgressTasks(current) {
			continue
		}

		resetCount := 0
		if err := te.workspaceStore.Update(ws.ID, func(fresh *Workspace) error {
			if fresh.Status != StatusActive {
				return errSkipOrphanCleanup
			}
			for i := range fresh.Tasks {
				task := &fresh.Tasks[i]
				if task.Status != TaskStatusInProgress {
					continue
				}
				if err := task.SetStatus(TaskStatusPending); err != nil {
					logger.Error("Orphan cleanup transition rejected", logger.Fields{"task_id": task.ID, "error": err})
					continue
				}
				task.StartedAt = nil
				resetCount++
			}
			if resetCount == 0 {
				return errSkipOrphanCleanup
			}
			return nil
		}); err != nil {
			if errors.Is(err, errSkipOrphanCleanup) {
				continue
			}
			logger.Error("Failed to clean orphaned tasks in workspace", logger.Fields{"workspace_id": ws.ID, "err": err})
			continue
		}
		if resetCount > 0 {
			totalReset += resetCount
			logger.Debug("Reset orphaned tasks in workspace", logger.Fields{"count": resetCount, "workspace_id": ws.ID})
		}
	}

	if totalReset > 0 {
		logger.Info("Cleaned up orphaned task(s) across all workspaces", logger.Fields{"task_id": totalReset})
	}
}

func hasInProgressTasks(ws *Workspace) bool {
	if ws == nil || ws.Status != StatusActive {
		return false
	}
	for i := range ws.Tasks {
		if ws.Tasks[i].Status == TaskStatusInProgress {
			return true
		}
	}
	return false
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

// taskCandidate is a claimable task plus its workspace and resolved provider
// profile, collected before ordering/claiming within a poll cycle.
type taskCandidate struct {
	ws      *Workspace
	task    Task
	profile TaskProviderProfile
}

// checkAndExecuteTasks checks for assigned tasks and executes those that fit the
// global and per-provider concurrency limits.
func (te *TaskExecutor) checkAndExecuteTasks() {
	workspaceIDs, err := te.workspaceStore.List()
	if err != nil {
		logger.Error("Failed to list workspaces", logger.Fields{"error": err})
		return
	}

	// Collect all claimable candidates across active workspaces first, so they can
	// be ordered before claiming.
	var candidates []taskCandidate
	for _, wsID := range workspaceIDs {
		ws, err := te.workspaceStore.Get(wsID)
		if err != nil || ws.Status != StatusActive {
			continue
		}
		for i := range ws.Tasks {
			// Only auto-execute "assigned" tasks; "pending" requires the UI RUN button.
			if ws.Tasks[i].Status != TaskStatusAssigned {
				continue
			}
			task := ws.Tasks[i]
			candidates = append(candidates, taskCandidate{ws: ws, task: task, profile: te.resolveProviderProfile(task)})
		}
	}

	// Group identical provider+model tasks consecutively to cut model-swap churn
	// on a single local server (WS6.24). A stable sort preserves the original
	// (FIFO) order within a group and among cloud tasks (OrderKey ""), so no task
	// is starved by reordering (WS6.23).
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].profile.OrderKey < candidates[j].profile.OrderKey
	})

	for _, c := range candidates {
		te.mu.Lock()
		if _, running := te.runningTasks[c.task.ID]; running {
			te.mu.Unlock()
			continue
		}
		// Global capacity.
		if len(te.runningTasks) >= te.maxConcurrent {
			te.mu.Unlock()
			logger.Warn("Max concurrent tasks reached, deferring task", logger.Fields{"max_concurrent": te.maxConcurrent, "task_id": c.task.ID})
			continue
		}
		// Per-provider-instance capacity: a task whose provider is at its limit is
		// deferred (not failed) and consumes no global slot (WS6.23).
		key := c.profile.ConcurrencyKey
		if key != "" && c.profile.Limit > 0 && te.runningByKey[key] >= c.profile.Limit {
			te.mu.Unlock()
			logger.Debug("Provider at concurrency limit, deferring task", logger.Fields{"provider_key": key, "limit": c.profile.Limit, "task_id": c.task.ID})
			continue
		}

		// Claim: placeholder entry (replaced by executeTask with full context) and
		// hold a per-key slot for the duration.
		te.runningTasks[c.task.ID] = &taskExecution{
			Task:           c.task,
			StartedAt:      time.Now(),
			ConcurrencyKey: key,
		}
		if key != "" {
			te.runningByKey[key]++
		}
		te.mu.Unlock()

		te.executeTask(c.ws, c.task, c.profile)
	}
}

// executeTask executes a single task asynchronously
func (te *TaskExecutor) executeTask(ws *Workspace, task Task, profile TaskProviderProfile) {
	// Create context with timeout. Local providers get a scaled default to absorb
	// cold model loads when the task set no explicit timeout (WS6.25).
	timeout := effectiveTaskTimeout(task.Timeout, profile.IsLocal, te.localTimeoutMultiplier)

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	// NOTE: Don't defer cancel() here because we launch a goroutine below
	// The goroutine will defer cancel() when it completes (see line ~290)

	// Track running task
	te.mu.Lock()
	te.runningTasks[task.ID] = &taskExecution{
		Task:           task,
		StartedAt:      time.Now(),
		Context:        ctx,
		Cancel:         cancel,
		ConcurrencyKey: profile.ConcurrencyKey,
	}
	te.mu.Unlock()

	logger.Debug("▶️ Executing task for agent", logger.Fields{"description": task.Description, "agent": task.ID, "to": task.To})

	// Build runtime inputs (results of upstream tasks named in InputTaskIDs)
	// and attach them to the task for the duration of this execution. Note:
	// task.Context is intentionally untouched — runtime data lives in
	// RuntimeInputs so re-runs cannot accumulate stale injection.
	if len(task.InputTaskIDs) > 0 {
		logger.Debug("🔗 Task has input task IDs", logger.Fields{"task_id": task.ID, "input_task_count": len(task.InputTaskIDs), "input_task_ids": task.InputTaskIDs})
		task.RuntimeInputs = ws.BuildRuntimeInputs(&task)

		if task.RuntimeInputs != nil && len(task.RuntimeInputs.TaskResults) > 0 {
			logger.Debug("Built runtime inputs for task", logger.Fields{"task_id": task.ID, "input_result_count": len(task.RuntimeInputs.TaskResults)})
			for taskID, result := range task.RuntimeInputs.TaskResults {
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

	// Update task status to in_progress. The local `task` value is a snapshot
	// used for downstream event metadata; the persisted slice element is
	// updated by the closure below.
	now := time.Now()
	if err := task.SetStatus(TaskStatusInProgress); err != nil {
		logger.Error("Failed to mark local task in_progress", logger.Fields{"task_id": task.ID, "error": err})
		return
	}
	task.StartedAt = &now

	if err := MutateTaskAndSave(te.workspaceStore, ws, task.ID, func(t *Task) error {
		if err := t.SetStatus(TaskStatusInProgress); err != nil {
			return err
		}
		t.StartedAt = &now
		return nil
	}); err != nil {
		logger.Error("Failed to mark task in_progress", logger.Fields{"task_id": task.ID, "error": err})
	}

	// Publish task started event
	if te.eventBus != nil {
		event := NewTaskEvent(EventTaskStarted, ws.ID, task.ID, task.To, map[string]any{
			"description": task.Description,
			"priority":    task.Priority,
		})
		te.eventBus.Publish(event)
	}

	// Execute asynchronously
	te.wg.Go(func() {
		defer cancel()
		defer func() {
			te.mu.Lock()
			if exec, ok := te.runningTasks[task.ID]; ok && exec.ConcurrencyKey != "" {
				te.runningByKey[exec.ConcurrencyKey]--
				if te.runningByKey[exec.ConcurrencyKey] <= 0 {
					delete(te.runningByKey, exec.ConcurrencyKey)
				}
			}
			delete(te.runningTasks, task.ID)
			te.mu.Unlock()
		}()

		// Execute the task with a run-start output spec snapshot so later
		// approvals do not change prompt/validation semantics in-flight.
		task.OutputSpec = SnapshotTaskOutputSpec(task.OutputSpec)
		if task.OutputSpec != nil {
			task.OutputSchema = task.OutputSpec.Schema
			task.OutputContract = task.OutputSpec.Contract
		}
		taskRun, err := ExecuteTaskWithRunMetadata(ctx, te.taskHandler, task.To, task)
		result := taskRun.Result

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
				if taskRun.RunID != "" {
					t.CurrentRunID = taskRun.RunID
				}
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
						if err := t.SetStatus(TaskStatusWaitingForChoice); err != nil {
							return err
						}
						t.Error = ""
						t.Result = ""
						ApplyTaskResultMetadata(t, "")
						applyExecutorTaskBlockedContext(t, be)
					} else {
						if err := t.SetStatus(TaskStatusFailed); err != nil {
							return err
						}
						t.Error = err.Error()
					}
					RecordTaskExecution(t, executionStatus, executionSummary, startedAt, completedAt.Sub(startedAt))
				} else {
					logger.Info("Task completed successfully", logger.Fields{"task_id": task.ID})
					if err := t.SetStatus(TaskStatusCompleted); err != nil {
						return err
					}
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
					te.eventBus.Publish(NewTaskEvent(EventTaskBlocked, workspaceID, task.ID, task.To, map[string]any{
						"description": task.Description,
						"human_loop":  snapshot.Context["human_loop"],
						"status":      snapshot.Status,
						"error":       blockedErr.Error(),
					}))
				} else {
					te.eventBus.Publish(NewTaskEvent(EventTaskFailed, workspaceID, task.ID, task.To, map[string]any{
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
				taskForStorage := task
				if persisted, taskErr := fresh.GetTask(task.ID); taskErr == nil && persisted != nil {
					taskForStorage = *persisted
					taskForStorage.OutputSpec = SnapshotTaskOutputSpec(task.OutputSpec)
					if taskForStorage.OutputSpec != nil {
						taskForStorage.OutputSchema = taskForStorage.OutputSpec.Schema
						taskForStorage.OutputContract = taskForStorage.OutputSpec.Contract
					}
				}
				te.autoStoreResult(ctx, fresh, &taskForStorage, result)
			}

			if te.eventBus != nil {
				te.eventBus.Publish(NewTaskEvent(EventTaskCompleted, workspaceID, task.ID, task.To, map[string]any{
					"description": task.Description,
					"result":      result,
				}))
			}
		}

		// Publish workspace updated event
		if te.eventBus != nil {
			te.eventBus.Publish(NewWorkspaceEvent(EventWorkspaceUpdated, workspaceID, "task-executor", map[string]any{
				"task_id": task.ID,
				"status":  snapshot.Status,
			}))
		}
	})
}

func applyExecutorTaskBlockedContext(task *Task, blockedErr *TaskBlockedError) {
	if task == nil {
		return
	}
	if task.Context == nil {
		task.Context = map[string]any{}
	}

	blockID := fmt.Sprintf("blk_%d", time.Now().UnixNano())
	if existing, ok := task.Context["human_loop"].(map[string]any); ok {
		if prior, ok := existing["block_id"].(string); ok && strings.TrimSpace(prior) != "" {
			blockID = strings.TrimSpace(prior)
		}
	}

	humanLoop := map[string]any{
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
	AutoStoreResultWithAssistant(context.Background(), ws, task, result, workspaceStore, nil)
}

// AutoStoreResultWithAssistant automatically stores task result based on:
// 1. Task-level ResultStorage configuration (if enabled)
// 2. Agent's connected store node (if auto-store enabled)
//
// When an active output spec exists, assistant can perform one bounded
// normalize/repair pass before CSV projection.
func AutoStoreResultWithAssistant(ctx context.Context, ws *Workspace, task *Task, result string, workspaceStore Store, assistant TaskOutputSpecAssistant) {
	// Check for task-level result storage configuration first
	if task.ResultStorage != nil && task.ResultStorage.Enabled {
		autoStoreTaskResult(ctx, ws, task, result, workspaceStore, assistant)
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

	validation, contractCSV := validateTaskOutputForStorage(ctx, task, result, assistant)
	if validation.ValidationStatus == TaskValidationNeedsReview {
		recordTaskStorageValidation(ws, task, workspaceStore, validation)
		logger.Warn("Task result held for review; output contract validation failed", logger.Fields{
			"task_id":          task.ID,
			"store_node_id":    assignedStore.ID,
			"contract_version": validation.ContractVersion,
			"error_count":      len(validation.Errors),
		})
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
	case "csv":
		ext = "csv"
	case "binary":
		ext = "bin"
	}

	filename := fmt.Sprintf("task-%s-%s.%s", taskIDShort, timestamp, ext)

	// Prepare data for storage
	dataToStore := result
	switch assignedStore.Format {
	case "json":
		// Wrap plain text result in JSON structure
		jsonData := map[string]any{
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
	case "csv":
		if validation.ValidationStatus == TaskValidationPassed && contractCSV != "" {
			dataToStore = contractCSV
		} else {
			dataToStore = TaskResultToCSV(task, result, timestamp, agentNodeID)
		}
	}

	// Write result to store
	if err := WriteToStoreForWorkspace(assignedStore, workspaceStore, ws.ID, filename, dataToStore); err != nil {
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
	if validation.ValidationStatus == TaskValidationPassed || validation.ValidationStatus == TaskValidationNotApplicable {
		validation.StorageStatus = TaskStorageSaved
		recordTaskStorageValidation(ws, task, workspaceStore, validation)
	}

	// Save workspace to persist store node stats (WriteToStore updated them)
	if err := workspaceStore.Save(ws); err != nil {
		logger.Error("Failed to save workspace after auto-store", logger.Fields{"workspace_id": ws.ID, "err": err})
	}
}

func validateTaskOutputForStorage(ctx context.Context, task *Task, result string, assistant TaskOutputSpecAssistant) (*TaskValidationResult, string) {
	if ctx == nil {
		ctx = context.Background()
	}
	if task != nil && task.OutputSpec != nil {
		return ValidateTaskOutputSpecResultWithAssistant(ctx, task, result, assistant)
	}
	return ValidateTaskOutputContractResult(task, result)
}

// autoStoreTaskResult handles task-level result storage configuration
func autoStoreTaskResult(ctx context.Context, ws *Workspace, task *Task, result string, workspaceStore Store, assistant TaskOutputSpecAssistant) {
	storage := task.ResultStorage
	if storage == nil || !storage.Enabled {
		return
	}

	// Generate filename
	timestamp := time.Now().Format("20060102-150405")

	validation, contractCSV := validateTaskOutputForStorage(ctx, task, result, assistant)
	if validation.ValidationStatus == TaskValidationNeedsReview {
		recordTaskStorageValidation(ws, task, workspaceStore, validation)
		logger.Warn("Task result held for review; output contract validation failed", logger.Fields{
			"task_id":          task.ID,
			"contract_version": validation.ContractVersion,
			"error_count":      len(validation.Errors),
		})
		return
	}

	// Determine format and extension
	format := storage.Format
	if format == "" {
		format = "text"
	}
	writeMode := strings.ToLower(strings.TrimSpace(storage.WriteMode))
	appendMode := writeMode == "append"
	if appendMode {
		// Append datasets are JSONL (canonical); a spreadsheet CSV is produced
		// on demand via export rather than appended live.
		format = "jsonl"
	}
	ext := "txt"
	switch format {
	case "json":
		ext = "json"
	case "markdown":
		ext = "md"
	case "csv":
		ext = "csv"
	case "jsonl":
		ext = "jsonl"
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
	if appendMode {
		// Honors a user-set storage.FileName; otherwise derives from the task
		// description. Append datasets are JSONL.
		filename = AppendJSONLFileName(task, storage)
	}

	// Prepare data for storage
	dataToStore := result
	if appendMode {
		jsonlData, buildErr := BuildAppendJSONL(task, result, validation)
		if buildErr != nil {
			logger.Error("Failed to build JSONL for append", logger.Fields{"task_id": task.ID, "err": buildErr})
			return
		}
		dataToStore = jsonlData
	} else if format == "json" {
		jsonData := map[string]any{
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
	} else if format == "csv" {
		if validation.ValidationStatus == TaskValidationPassed && contractCSV != "" {
			dataToStore = contractCSV
		} else {
			dataToStore = TaskResultToCSV(task, result, timestamp, "")
		}
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

		storeFilePath := filename
		if storage.FilePath != "" {
			storeFilePath = storage.FilePath
		}
		if appendMode {
			storeNodeCopy := *storeNode
			storeNodeCopy.WriteMode = "append"
			storeNodeCopy.Format = "jsonl"
			// JSONL needs no header reconciliation — each line is a
			// self-describing record — so the records are appended directly
			// (no CSV header strip, no reproject-on-mismatch).
			if err := WriteToStoreForWorkspace(&storeNodeCopy, workspaceStore, ws.ID, storeFilePath, dataToStore); err != nil {
				logger.Error("Failed to append task result to store node", logger.Fields{
					"task_id":       task.ID,
					"store_node_id": storeNode.ID,
					"filename":      storeFilePath,
					"err":           err,
				})
				return
			}
			storeNode.LastWriteTime = storeNodeCopy.LastWriteTime
			storeNode.WriteCount = storeNodeCopy.WriteCount
			storeNode.LastFilePath = storeNodeCopy.LastFilePath
			storeNode.LastError = storeNodeCopy.LastError
			storeNode.UpdatedAt = storeNodeCopy.UpdatedAt
			if validation.ValidationStatus == TaskValidationPassed || validation.ValidationStatus == TaskValidationNotApplicable {
				validation.StorageStatus = TaskStorageAppended
				recordTaskStorageValidation(ws, task, workspaceStore, validation)
			}
		} else if err := WriteToStoreForWorkspace(storeNode, workspaceStore, ws.ID, storeFilePath, dataToStore); err != nil {
			logger.Error("Failed to auto-store task result to store node", logger.Fields{
				"task_id":       task.ID,
				"store_node_id": storeNode.ID,
				"filename":      storeFilePath,
				"err":           err,
			})
			return
		} else if validation.ValidationStatus == TaskValidationPassed || validation.ValidationStatus == TaskValidationNotApplicable {
			validation.StorageStatus = TaskStorageSaved
			recordTaskStorageValidation(ws, task, workspaceStore, validation)
		}

		logger.Info("Task result auto-stored to store node", logger.Fields{
			"task_id":       task.ID,
			"store_node_id": storeNode.ID,
			"filename":      storeFilePath,
		})

		if err := workspaceStore.Save(ws); err != nil {
			logger.Error("Failed to save workspace after auto-store", logger.Fields{"workspace_id": ws.ID, "err": err})
		}
		return
	}

	// Otherwise use file path (or default output directory)
	filePath := storage.FilePath
	if ResultStorageUsesWorkspaceFolder(storage) {
		baseDir, _, err := ResolveWorkspaceFolderBaseDir(workspaceStore, ws.ID, storage.Folder)
		if err != nil {
			logger.Error("Failed to resolve workspace folder for task result storage", logger.Fields{
				"task_id": task.ID,
				"folder":  storage.Folder,
				"err":     err,
			})
			return
		}
		relativeFilePath := strings.TrimSpace(filePath)
		if relativeFilePath == "" {
			relativeFilePath = filename
		} else if strings.HasSuffix(relativeFilePath, "/") || !strings.Contains(filepath.Base(relativeFilePath), ".") {
			relativeFilePath = filepath.Join(relativeFilePath, filename)
		}
		finalPath, err := BuildFinalPath(baseDir, relativeFilePath)
		if err != nil {
			logger.Error("Failed to resolve task result path inside workspace folder", logger.Fields{
				"task_id": task.ID,
				"path":    relativeFilePath,
				"err":     err,
			})
			return
		}
		filePath = finalPath
	} else if filePath == "" {
		// Default to the workspace's own folder: <workspace>/outputs/
		baseOutputDir := ""
		if workspaceStore != nil {
			baseOutputDir = workspaceStore.GetOutputsPath(ws.ID)
		}
		if baseOutputDir == "" {
			// Fallback to the global output directory if the workspace folder
			// can't be resolved (e.g. in-memory stores during tests).
			fallback, err := platform.GetDefaultOutputDir()
			if err != nil {
				fallback = "outputs"
				logger.Warn("Failed to get default output dir, using fallback", logger.Fields{"error": err})
			}
			baseOutputDir = filepath.Join(fallback, ws.Name)
		}
		filePath = filepath.Join(baseOutputDir, filename)
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

	if appendMode {
		if err := AppendJSONLToFile(filePath, dataToStore); err != nil {
			logger.Error("Failed to append task result to JSONL file", logger.Fields{
				"task_id":   task.ID,
				"file_path": filePath,
				"err":       err,
			})
			return
		}
		logger.Info("Task result appended to JSONL file", logger.Fields{
			"task_id":   task.ID,
			"file_path": filePath,
		})
		if validation.ValidationStatus == TaskValidationPassed || validation.ValidationStatus == TaskValidationNotApplicable {
			validation.StorageStatus = TaskStorageAppended
			recordTaskStorageValidation(ws, task, workspaceStore, validation)
		}
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
	if validation.ValidationStatus == TaskValidationPassed || validation.ValidationStatus == TaskValidationNotApplicable {
		validation.StorageStatus = TaskStorageSaved
		recordTaskStorageValidation(ws, task, workspaceStore, validation)
	}
}

func recordTaskStorageValidation(ws *Workspace, task *Task, workspaceStore Store, validation *TaskValidationResult) {
	if task == nil || validation == nil {
		return
	}
	ApplyTaskValidationResultToLatestExecution(task, validation)
	workspaceID := ""
	if ws != nil {
		workspaceID = ws.ID
	}
	mirrorLatestTaskValidationResult(workspaceID, task, validation)
	logger.Info("Task output contract validation outcome", logger.Fields{
		"action":            "validation_outcome",
		"workspace_id":      workspaceID,
		"task_id":           task.ID,
		"run_id":            latestTaskExecutionRunID(task),
		"validation_status": validation.ValidationStatus,
		"contract_version":  validation.ContractVersion,
		"validation_errors": len(validation.Errors),
		"raw_output_stored": false,
	})
	logger.Info("Task output contract storage outcome", logger.Fields{
		"action":            "storage_gating_outcome",
		"workspace_id":      workspaceID,
		"task_id":           task.ID,
		"run_id":            latestTaskExecutionRunID(task),
		"validation_status": validation.ValidationStatus,
		"storage_status":    validation.StorageStatus,
		"contract_version":  validation.ContractVersion,
		"validation_errors": len(validation.Errors),
		"raw_output_stored": false,
		"manual_approval":   validation.ManualApproval != nil,
	})
	if ws != nil {
		_ = ws.MutateTask(task.ID, func(t *Task) error {
			ApplyTaskValidationResultToLatestExecution(t, validation)
			return nil
		})
	}
	if ws == nil || workspaceStore == nil || strings.TrimSpace(ws.ID) == "" {
		return
	}
	if err := workspaceStore.Update(ws.ID, func(fresh *Workspace) error {
		return fresh.MutateTask(task.ID, func(t *Task) error {
			ApplyTaskValidationResultToLatestExecution(t, validation)
			return nil
		})
	}); err != nil {
		logger.Warn("Failed to persist task validation result", logger.Fields{
			"task_id":      task.ID,
			"workspace_id": ws.ID,
			"error":        err,
		})
	}
}

func latestTaskExecutionRunID(task *Task) string {
	if task == nil || len(task.ExecutionHistory) == 0 {
		return ""
	}
	return task.ExecutionHistory[len(task.ExecutionHistory)-1].RunID
}

// autoStoreResult is a convenience wrapper that calls AutoStoreResult with the executor's workspace store.
func (te *TaskExecutor) autoStoreResult(ctx context.Context, ws *Workspace, task *Task, result string) {
	var assistant TaskOutputSpecAssistant
	if candidate, ok := te.taskHandler.(TaskOutputSpecAssistant); ok {
		assistant = candidate
	}
	AutoStoreResultWithAssistant(ctx, ws, task, result, te.workspaceStore, assistant)
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

	if exec.Cancel != nil {
		exec.Cancel()
	}
	logger.Debug("🚫 Task cancelled", logger.Fields{"task_id": taskID})

	// Propagate cancellation to in-flight delegated subtasks so none are
	// orphaned when their parent is stopped (single-level: subtasks are leaves).
	for childID, child := range te.runningTasks {
		if childID == taskID || child == nil || child.Cancel == nil {
			continue
		}
		if child.Task.ParentTaskID == taskID {
			child.Cancel()
			logger.Debug("🚫 Cancelled in-flight subtask of parent", logger.Fields{
				"task_id": childID, "parent_task_id": taskID,
			})
		}
	}

	return nil
}
