package workspace

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/johnjallday/ori-agent/internal/logger"
)

// StepExecutor manages the execution of workflow steps
type StepExecutor struct {
	workspaceStore Store
	taskHandler    TaskHandler
	pollInterval   time.Duration

	mu           sync.RWMutex
	runningSteps map[string]*stepExecution
	stopChan     chan struct{}
	wg           sync.WaitGroup
}

// stepExecution tracks a running step
type stepExecution struct {
	WorkflowID string
	Step       WorkflowStep
	StartedAt  time.Time
	Context    context.Context
	Cancel     context.CancelFunc
}

// StepExecutorConfig contains configuration for the step executor
type StepExecutorConfig struct {
	PollInterval time.Duration // How often to check for ready steps
}

// NewStepExecutor creates a new step executor
func NewStepExecutor(store Store, handler TaskHandler, config StepExecutorConfig) *StepExecutor {
	if config.PollInterval == 0 {
		config.PollInterval = 5 * time.Second
	}

	return &StepExecutor{
		workspaceStore: store,
		taskHandler:    handler,
		pollInterval:   config.PollInterval,
		runningSteps:   make(map[string]*stepExecution),
		stopChan:       make(chan struct{}),
	}
}

// Start begins the step executor polling loop
func (se *StepExecutor) Start() {
	logger.Debug("Step executor started", logger.Fields{"poll_interval": se.pollInterval})

	se.wg.Add(1)
	go se.pollLoop()
}

// Stop gracefully stops the step executor
func (se *StepExecutor) Stop() {
	logger.Debug("Stopping step executor", logger.Fields{})
	close(se.stopChan)

	// Cancel all running steps
	se.mu.Lock()
	for _, exec := range se.runningSteps {
		exec.Cancel()
	}
	se.mu.Unlock()

	se.wg.Wait()
	logger.Info("Step executor stopped", logger.Fields{})
}

// pollLoop continuously polls for ready steps
func (se *StepExecutor) pollLoop() {
	defer se.wg.Done()

	ticker := time.NewTicker(se.pollInterval)
	defer ticker.Stop()

	// Run immediately on start
	se.checkAndExecuteSteps()

	for {
		select {
		case <-se.stopChan:
			return
		case <-ticker.C:
			se.checkAndExecuteSteps()
		}
	}
}

// checkAndExecuteSteps checks for ready steps and executes them
func (se *StepExecutor) checkAndExecuteSteps() {
	// Get all workspaces
	workspaceIDs, err := se.workspaceStore.List()
	if err != nil {
		logger.Error("Failed to list workspaces", logger.Fields{"error": err})
		return
	}

	for _, wsID := range workspaceIDs {
		ws, err := se.workspaceStore.Get(wsID)
		if err != nil {
			continue
		}

		// Only process active workspaces
		if ws.Status != StatusActive {
			continue
		}

		// Process each workflow
		for workflowID := range ws.Workflows {
			se.processWorkflow(ws, workflowID)
		}
	}
}

// processWorkflow processes a single workflow.
//
// Each state-changing block runs under store.Update so a different instance
// (or another goroutine in this one) can't load the same stale snapshot and
// re-flip the step. The local snapshot read at the top is only used to decide
// whether work is needed at all and to dispatch executeStep — actual mutations
// go through the canonical Get → mutate → Save serialized by the store lock.
func (se *StepExecutor) processWorkflow(ws *Workspace, workflowID string) {
	workflow, err := ws.GetWorkflow(workflowID)
	if err != nil {
		return
	}

	// Skip completed/failed workflows
	if workflow.Status == WorkflowStatusCompleted ||
		workflow.Status == WorkflowStatusFailed ||
		workflow.Status == WorkflowStatusCancelled {
		return
	}

	// Update workflow status to in_progress if pending. The double-check
	// inside the closure absorbs the (rare) case where another worker just
	// won the race and already flipped the workflow.
	if workflow.Status == WorkflowStatusPending {
		if err := se.workspaceStore.Update(ws.ID, func(fresh *Workspace) error {
			wf, ok := fresh.Workflows[workflowID]
			if !ok {
				return fmt.Errorf("workflow %s not found", workflowID)
			}
			if wf.Status != WorkflowStatusPending {
				return nil
			}
			wf.Status = WorkflowStatusInProgress
			now := time.Now()
			wf.StartedAt = &now
			fresh.Workflows[workflowID] = wf
			fresh.UpdatedAt = time.Now()
			return nil
		}); err != nil {
			logger.Warn("Failed to flip workflow to in_progress", logger.Fields{"workflow_id": workflowID, "err": err})
		}
	}

	// Update step statuses based on dependencies (atomic).
	se.updateStepStatuses(ws.ID, workflowID)

	// Re-read the workflow once after the dependency-resolution batch so the
	// dispatch loop sees the latest Ready set without holding the store lock.
	freshWS, err := se.workspaceStore.Get(ws.ID)
	if err != nil {
		return
	}
	freshWorkflow, err := freshWS.GetWorkflow(workflowID)
	if err != nil {
		return
	}

	// Find and execute ready steps.
	for i := range freshWorkflow.Steps {
		step := freshWorkflow.Steps[i]

		if step.Status != StepStatusReady {
			continue
		}

		// Local-process duplicate guard. Cross-instance races are caught by
		// the SetStatus check inside executeStep's claim Update.
		se.mu.RLock()
		_, isRunning := se.runningSteps[step.ID]
		se.mu.RUnlock()
		if isRunning {
			continue
		}

		se.executeStep(freshWS, freshWorkflow, &freshWorkflow.Steps[i])
	}

	// Check if workflow is complete.
	se.checkWorkflowCompletion(ws.ID, workflowID)
}

// updateStepStatuses transitions Pending/Waiting steps based on their
// dependency state, atomic under store.Update so two workers don't race on
// the same set of pending dependencies. Each individual transition goes
// through SetStatus; an illegal transition (e.g. another worker already
// promoted the step to Ready) is logged and skipped instead of being silently
// overwritten.
func (se *StepExecutor) updateStepStatuses(workspaceID, workflowID string) {
	if err := se.workspaceStore.Update(workspaceID, func(fresh *Workspace) error {
		wf, ok := fresh.Workflows[workflowID]
		if !ok {
			return fmt.Errorf("workflow %s not found", workflowID)
		}

		changed := false
		for i := range wf.Steps {
			step := &wf.Steps[i]

			if step.Status != StepStatusPending && step.Status != StepStatusWaiting {
				continue
			}

			dependenciesMet, shouldSkip := se.checkDependencies(&wf, step)

			var target StepStatus
			switch {
			case shouldSkip:
				target = StepStatusSkipped
				logger.Debug("Step skipped due to upstream failure", logger.Fields{"name": step.Name, "id": step.ID})
			case dependenciesMet:
				shouldExecute, err := se.evaluateCondition(&wf, step)
				if err != nil {
					logger.Error("Failed to evaluate condition for step", logger.Fields{"id": step.ID, "err": err})
					continue
				}
				if shouldExecute {
					target = StepStatusReady
					logger.Info("Step is ready to execute", logger.Fields{"id": step.ID, "name": step.Name})
				} else {
					target = StepStatusSkipped
					logger.Debug("Step skipped due to condition", logger.Fields{"name": step.Name, "id": step.ID})
				}
			default:
				if step.Status == StepStatusWaiting {
					continue
				}
				target = StepStatusWaiting
			}

			if step.Status == target {
				continue
			}
			if err := step.SetStatus(target); err != nil {
				logger.Warn("Refused step status transition", logger.Fields{"id": step.ID, "err": err})
				continue
			}
			changed = true
		}

		if !changed {
			return nil
		}
		fresh.Workflows[workflowID] = wf
		fresh.UpdatedAt = time.Now()
		return nil
	}); err != nil {
		logger.Warn("Failed to update step statuses", logger.Fields{"workflow_id": workflowID, "err": err})
	}
}

// checkDependencies checks if all dependencies for a step are met
func (se *StepExecutor) checkDependencies(workflow *Workflow, step *WorkflowStep) (met bool, shouldSkip bool) {
	if len(step.DependsOn) == 0 {
		return true, false
	}

	// Create map of steps by ID for quick lookup
	stepMap := make(map[string]*WorkflowStep)
	for i := range workflow.Steps {
		stepMap[workflow.Steps[i].ID] = &workflow.Steps[i]
	}

	for _, depID := range step.DependsOn {
		depStep, exists := stepMap[depID]
		if !exists {
			logger.Warn("Dependency step not found", logger.Fields{"depID": depID})
			return false, false
		}

		// If any dependency failed, skip this step
		if depStep.Status == StepStatusFailed {
			return false, true
		}

		// If any dependency is not completed, dependencies not met
		if depStep.Status != StepStatusCompleted && depStep.Status != StepStatusSkipped {
			return false, false
		}
	}

	return true, false
}

// evaluateCondition evaluates a step's condition
func (se *StepExecutor) evaluateCondition(workflow *Workflow, step *WorkflowStep) (bool, error) {
	if step.Condition == nil {
		return true, nil // No condition, execute
	}

	cond := step.Condition
	result := false

	switch cond.Type {
	case ConditionTypePreviousResult:
		// Check previous step result
		prevStep := se.findStep(workflow, cond.StepID)
		if prevStep == nil {
			return false, fmt.Errorf("step %s not found", cond.StepID)
		}

		result = se.evaluateOperator(prevStep.Result, cond.Operator, cond.Value)

	case ConditionTypeStepStatus:
		// Check step status
		prevStep := se.findStep(workflow, cond.StepID)
		if prevStep == nil {
			return false, fmt.Errorf("step %s not found", cond.StepID)
		}

		result = se.evaluateOperator(string(prevStep.Status), cond.Operator, cond.Value)

	case ConditionTypeContextValue:
		// Check context value
		if val, exists := step.Context[cond.StepID]; exists {
			result = se.evaluateOperator(val, cond.Operator, cond.Value)
		}
	}

	// Determine action based on result
	if result && cond.OnTrue == "execute" {
		return true, nil
	} else if !result && cond.OnFalse == "execute" {
		return true, nil
	}

	return false, nil
}

// evaluateOperator evaluates a condition operator
func (se *StepExecutor) evaluateOperator(actual any, operator string, expected any) bool {
	switch operator {
	case "eq":
		return fmt.Sprintf("%v", actual) == fmt.Sprintf("%v", expected)
	case "ne":
		return fmt.Sprintf("%v", actual) != fmt.Sprintf("%v", expected)
	case "contains":
		actualStr := fmt.Sprintf("%v", actual)
		expectedStr := fmt.Sprintf("%v", expected)
		return strings.Contains(actualStr, expectedStr)
	case "exists":
		return actual != nil
	default:
		return false
	}
}

// findStep finds a step by ID in a workflow
func (se *StepExecutor) findStep(workflow *Workflow, stepID string) *WorkflowStep {
	for i := range workflow.Steps {
		if workflow.Steps[i].ID == stepID {
			return &workflow.Steps[i]
		}
	}
	return nil
}

// executeStep executes a single workflow step.
//
// The Ready → InProgress transition is performed under store.Update via
// SetStatus, which means at most one worker (across instances) can claim a
// given step: the SetStatus call returns IllegalStepTransitionError for any
// worker that loses the race, and that worker bails before spawning a
// goroutine. The terminal flip (Completed/Failed) is likewise atomic and is
// followed by a workflow-completion check on the same fresh snapshot.
func (se *StepExecutor) executeStep(ws *Workspace, workflow *Workflow, step *WorkflowStep) {
	timeout := step.Timeout
	if timeout == 0 {
		timeout = 10 * time.Minute
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)

	stepID := step.ID
	workflowID := workflow.ID
	workspaceID := ws.ID
	stepType := step.Type
	stepName := step.Name

	// Atomically claim the step. If SetStatus rejects the transition, another
	// worker already moved this step out of Ready — abandon without spawning
	// a goroutine.
	claimErr := se.workspaceStore.Update(workspaceID, func(fresh *Workspace) error {
		return fresh.MutateWorkflowStep(workflowID, stepID, func(s *WorkflowStep) error {
			if err := s.SetStatus(StepStatusInProgress); err != nil {
				return err
			}
			now := time.Now()
			s.StartedAt = &now
			return nil
		})
	})
	if claimErr != nil {
		var illegal *IllegalStepTransitionError
		if errors.As(claimErr, &illegal) {
			logger.Debug("Step claim refused, another worker likely advanced it", logger.Fields{"id": stepID, "err": claimErr})
		} else {
			logger.Warn("Failed to claim step", logger.Fields{"id": stepID, "err": claimErr})
		}
		cancel()
		return
	}

	se.mu.Lock()
	se.runningSteps[stepID] = &stepExecution{
		WorkflowID: workflowID,
		Step:       *step,
		StartedAt:  time.Now(),
		Context:    ctx,
		Cancel:     cancel,
	}
	se.mu.Unlock()

	logger.Debug("Executing step in workflow", logger.Fields{"step_id": stepID, "workflow_name": workflow.Name})

	se.wg.Add(1)
	go func() {
		defer se.wg.Done()
		defer cancel()
		defer func() {
			se.mu.Lock()
			delete(se.runningSteps, stepID)
			se.mu.Unlock()
		}()

		var result string
		var execErr error

		switch stepType {
		case StepTypeTask:
			result, execErr = se.executeTaskStep(ctx, ws, step)
		case StepTypeAggregate:
			// Aggregate over a fresh workflow snapshot so it sees any sibling
			// steps that finished while we were waiting for our claim.
			fresh, getErr := se.workspaceStore.Get(workspaceID)
			if getErr == nil {
				if wf, wfErr := fresh.GetWorkflow(workflowID); wfErr == nil {
					result, execErr = se.executeAggregateStep(wf, step)
				} else {
					execErr = wfErr
				}
			} else {
				execErr = getErr
			}
		default:
			execErr = fmt.Errorf("unsupported step type: %s", stepType)
		}

		if updErr := se.workspaceStore.Update(workspaceID, func(fresh *Workspace) error {
			return fresh.MutateWorkflowStep(workflowID, stepID, func(s *WorkflowStep) error {
				completedAt := time.Now()
				s.CompletedAt = &completedAt
				if execErr != nil {
					if err := s.SetStatus(StepStatusFailed); err != nil {
						return err
					}
					s.Error = execErr.Error()
					return nil
				}
				if err := s.SetStatus(StepStatusCompleted); err != nil {
					return err
				}
				s.Result = result
				return nil
			})
		}); updErr != nil {
			logger.Error("Failed to record step completion", logger.Fields{"id": stepID, "name": stepName, "err": updErr})
			return
		}

		if execErr != nil {
			logger.Error("Step failed", logger.Fields{"id": stepID, "name": stepName, "err": execErr})
		} else {
			logger.Info("Step completed successfully", logger.Fields{"id": stepID, "name": stepName})
		}

		// Roll up the workflow if this step's completion finished it.
		se.checkWorkflowCompletion(workspaceID, workflowID)
	}()
}

// executeTaskStep executes a task step by delegating to an agent
func (se *StepExecutor) executeTaskStep(ctx context.Context, ws *Workspace, step *WorkflowStep) (string, error) {
	if step.AssignedTo == "" {
		return "", fmt.Errorf("no agent assigned to step")
	}

	// Create task for this step
	// Use first agent as coordinator
	coordinatorAgent := "system"
	if agentNames := ws.AgentNames(); len(agentNames) > 0 {
		coordinatorAgent = agentNames[0]
	}

	task := Task{
		ID:          fmt.Sprintf("step-%s", step.ID),
		WorkspaceID: ws.ID,
		From:        coordinatorAgent,
		To:          step.AssignedTo,
		Description: step.Description,
		Priority:    5,
		Context:     step.Context,
		Timeout:     step.Timeout,
		Status:      TaskStatusPending,
		CreatedAt:   time.Now(),
	}

	// Execute task via handler
	taskRun, err := ExecuteTaskWithRunMetadata(ctx, se.taskHandler, step.AssignedTo, task)
	if err != nil {
		return "", err
	}

	// Store task ID in step
	step.TaskID = task.ID

	return taskRun.Result, nil
}

// executeAggregateStep aggregates results from previous steps
func (se *StepExecutor) executeAggregateStep(workflow *Workflow, step *WorkflowStep) (string, error) {
	if len(step.DependsOn) == 0 {
		return "", fmt.Errorf("aggregate step has no dependencies")
	}

	var results strings.Builder
	results.WriteString("Aggregated Results:\n\n")

	for _, depID := range step.DependsOn {
		depStep := se.findStep(workflow, depID)
		if depStep == nil {
			continue
		}

		if depStep.Status == StepStatusCompleted && depStep.Result != "" {
			results.WriteString(fmt.Sprintf("## %s\n\n%s\n\n", depStep.Name, depStep.Result))
		}
	}

	return results.String(), nil
}

// checkWorkflowCompletion checks if a workflow is complete and rolls it up
// atomically. If another caller has already marked the workflow terminal,
// the closure no-ops.
func (se *StepExecutor) checkWorkflowCompletion(workspaceID, workflowID string) {
	if err := se.workspaceStore.Update(workspaceID, func(fresh *Workspace) error {
		wf, ok := fresh.Workflows[workflowID]
		if !ok {
			return fmt.Errorf("workflow %s not found", workflowID)
		}
		if wf.Status == WorkflowStatusCompleted ||
			wf.Status == WorkflowStatusFailed ||
			wf.Status == WorkflowStatusCancelled {
			return nil
		}

		allComplete := true
		anyFailed := false
		for _, step := range wf.Steps {
			if step.Status == StepStatusFailed {
				anyFailed = true
			}
			if step.Status != StepStatusCompleted &&
				step.Status != StepStatusSkipped &&
				step.Status != StepStatusFailed {
				allComplete = false
			}
		}
		if !allComplete {
			return nil
		}

		completedAt := time.Now()
		wf.CompletedAt = &completedAt
		if anyFailed {
			wf.Status = WorkflowStatusFailed
			logger.Error("Workflow completed with failures", logger.Fields{"id": wf.ID, "name": wf.Name})
		} else {
			wf.Status = WorkflowStatusCompleted
			logger.Info("Workflow completed successfully", logger.Fields{"id": wf.ID, "name": wf.Name})
		}
		fresh.Workflows[workflowID] = wf
		fresh.UpdatedAt = time.Now()
		return nil
	}); err != nil {
		logger.Warn("Failed to finalize workflow completion", logger.Fields{"workflow_id": workflowID, "err": err})
	}
}
