package cliagent

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/llm"
)

// MicroStepExecutor coordinates the micro-step execution loop for CLI agent tasks.
type MicroStepExecutor struct {
	registry    *CLIAgentRegistry
	planner     *StepPlanner
	eventLogger *EventLogger
	diff        *DiffDetector
	costTracker *llm.CostTracker

	mu            sync.Mutex
	running       map[string]context.CancelFunc // taskID -> cancel
	maxConcurrent int
}

// NewMicroStepExecutor creates a new executor with the given dependencies.
func NewMicroStepExecutor(
	registry *CLIAgentRegistry,
	planner *StepPlanner,
	eventLogger *EventLogger,
	diff *DiffDetector,
	costTracker *llm.CostTracker,
) *MicroStepExecutor {
	return &MicroStepExecutor{
		registry:      registry,
		planner:       planner,
		eventLogger:   eventLogger,
		diff:          diff,
		costTracker:   costTracker,
		running:       make(map[string]context.CancelFunc),
		maxConcurrent: DefaultMaxConcurrent,
	}
}

// SetMaxConcurrent sets the maximum number of concurrent tasks.
func (e *MicroStepExecutor) SetMaxConcurrent(n int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.maxConcurrent = n
}

// RunningCount returns the number of currently running tasks.
func (e *MicroStepExecutor) RunningCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.running)
}

// Execute runs a CLI agent task through the micro-step loop.
// This is a blocking call — run in a goroutine for async execution.
func (e *MicroStepExecutor) Execute(ctx context.Context, config TaskConfig) (*TaskResult, error) {
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	// Check concurrency limit
	e.mu.Lock()
	if e.maxConcurrent > 0 && len(e.running) >= e.maxConcurrent {
		e.mu.Unlock()
		return nil, fmt.Errorf("max concurrent tasks reached (%d)", e.maxConcurrent)
	}

	taskID := uuid.New().String()
	taskCtx, cancel := context.WithCancel(ctx)
	e.running[taskID] = cancel
	e.mu.Unlock()

	defer func() {
		e.mu.Lock()
		delete(e.running, taskID)
		e.mu.Unlock()
		cancel()
	}()

	return e.executeTask(taskCtx, taskID, config)
}

// Stop cancels a running task.
func (e *MicroStepExecutor) Stop(taskID string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()

	cancel, ok := e.running[taskID]
	if !ok {
		return false
	}
	cancel()
	return true
}

func (e *MicroStepExecutor) executeTask(ctx context.Context, taskID string, config TaskConfig) (*TaskResult, error) {
	startTime := time.Now()

	adapter, err := e.registry.Get(config.CLIBackend)
	if err != nil {
		return nil, fmt.Errorf("get adapter: %w", err)
	}
	if !adapter.IsAvailable() {
		return nil, fmt.Errorf("cli backend %q is not available", config.CLIBackend)
	}

	// Decompose task into steps
	plans, err := e.planner.DecomposeTask(ctx, config.Prompt)
	if err != nil {
		return nil, fmt.Errorf("decompose task: %w", err)
	}

	maxSteps := config.EffectiveMaxSteps()
	if len(plans) > maxSteps {
		plans = plans[:maxSteps]
	}

	budget := NewBudgetEnforcer()
	var steps []StepResult
	var summaries []string
	var allFileChanges []FileChange
	var finalStatus TaskStatus

	for i, plan := range plans {
		// Check context cancellation (stop was called)
		if err := ctx.Err(); err != nil {
			finalStatus = TaskStopped
			break
		}

		// Check budget
		budgetStatus := budget.Check(config.TokenBudget, config.CostBudgetUSD)
		if !budgetStatus.WithinBudget {
			finalStatus = TaskBudgetExceeded
			break
		}

		// Take a diff snapshot
		snapshot, _ := e.diff.Snapshot(config.WorkingDir)

		// Build step prompt with context from previous steps
		prompt := e.planner.GenerateStepPrompt(plan, summaries)

		// Calculate remaining budget for this step
		stepBudget := StepBudget{
			Timeout: DefaultStepTimeout,
		}
		if config.CostBudgetUSD > 0 {
			stepBudget.MaxCostUSD = budget.RemainingCostUSD(config.CostBudgetUSD)
		}

		// Execute the step
		req := StepRequest{
			TaskID:     taskID,
			StepNumber: i + 1,
			Prompt:     prompt,
			WorkingDir: config.WorkingDir,
			Model:      config.Model,
			Budget:     stepBudget,
		}

		stepStart := time.Now()
		result, err := adapter.ExecuteStep(ctx, req)
		if err != nil {
			result = &StepResult{
				StepNumber: i + 1,
				Status:     StepFailed,
				Error:      err.Error(),
			}
		}
		result.Duration = time.Since(stepStart)

		// Detect file changes
		if snapshot != nil {
			changes, _ := e.diff.Compare(snapshot, config.WorkingDir)
			result.FilesChanged = changes
			allFileChanges = append(allFileChanges, changes...)
		}

		// Record usage
		budget.Record(result.Usage)

		// Log events
		for j := range result.Events {
			result.Events[j].StepNumber = i + 1
		}
		e.eventLogger.LogEvents(taskID, result.Events)

		steps = append(steps, *result)

		// Handle step failure — report and stop
		if result.Status == StepFailed {
			finalStatus = TaskFailed
			break
		}

		// Summarize for next step context
		summary, _ := e.planner.SummarizeStepResult(ctx, *result)
		summaries = append(summaries, summary)

		// Evaluate completion
		done, _, _ := e.planner.EvaluateCompletion(ctx, config.Prompt, summaries)
		if done {
			finalStatus = TaskCompleted
			break
		}
	}

	// If we exhausted all steps without completing
	if finalStatus == "" {
		finalStatus = TaskMaxStepsReached
	}

	// Build final result
	totalUsage := budget.TotalUsage()
	var summaryText string
	if len(steps) > 0 {
		summaryText = steps[len(steps)-1].Output
	}

	taskResult := &TaskResult{
		TaskID:        taskID,
		Status:        finalStatus,
		Summary:       summaryText,
		Steps:         steps,
		FilesChanged:  dedupeFileChanges(allFileChanges),
		TotalUsage:    totalUsage,
		StepsExecuted: len(steps),
		Duration:      time.Since(startTime),
	}

	if finalStatus == TaskFailed && len(steps) > 0 {
		taskResult.Error = steps[len(steps)-1].Error
	}

	// Report to cost tracker
	budget.ReportToCostTracker(e.costTracker, config.CLIBackend, config.Model, "cli-agent-"+config.CLIBackend)

	// Persist event log
	_ = e.eventLogger.Persist(taskID)

	return taskResult, nil
}

// dedupeFileChanges removes duplicate file changes, keeping the last one per path.
func dedupeFileChanges(changes []FileChange) []FileChange {
	seen := make(map[string]int) // path -> index in result
	var result []FileChange

	for _, c := range changes {
		if idx, ok := seen[c.Path]; ok {
			result[idx] = c // Replace with latest
		} else {
			seen[c.Path] = len(result)
			result = append(result, c)
		}
	}
	return result
}
