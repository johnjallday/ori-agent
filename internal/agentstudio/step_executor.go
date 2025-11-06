package agentstudio

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
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
	log.Printf("🎬 Step executor started (poll interval: %v)", se.pollInterval)

	se.wg.Add(1)
	go se.pollLoop()
}

// Stop gracefully stops the step executor
func (se *StepExecutor) Stop() {
	log.Printf("⏹️  Stopping step executor...")
	close(se.stopChan)

	// Cancel all running steps
	se.mu.Lock()
	for _, exec := range se.runningSteps {
		exec.Cancel()
	}
	se.mu.Unlock()

	se.wg.Wait()
	log.Printf("✅ Step executor stopped")
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
		log.Printf("⚠️  Failed to list workspaces: %v", err)
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

// processWorkflow processes a single workflow
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

	// Update workflow status to in_progress if pending
	if workflow.Status == WorkflowStatusPending {
		workflow.Status = WorkflowStatusInProgress
		now := time.Now()
		workflow.StartedAt = &now
		ws.UpdateWorkflow(*workflow)
		se.workspaceStore.Save(ws)
	}

	// Update step statuses based on dependencies
	se.updateStepStatuses(ws, workflow)

	// Find and execute ready steps
	for i := range workflow.Steps {
		step := &workflow.Steps[i]

		// Skip if not ready
		if step.Status != StepStatusReady {
			continue
		}

		// Check if already running
		se.mu.RLock()
		_, isRunning := se.runningSteps[step.ID]
		se.mu.RUnlock()
		if isRunning {
			continue
		}

		// Execute the step
		se.executeStep(ws, workflow, step)
	}

	// Check if workflow is complete
	se.checkWorkflowCompletion(ws, workflow)
}

// updateStepStatuses updates step statuses based on dependencies
func (se *StepExecutor) updateStepStatuses(ws *Workspace, workflow *Workflow) {
	changed := false

	for i := range workflow.Steps {
		step := &workflow.Steps[i]

		// Skip already processed steps
		if step.Status != StepStatusPending && step.Status != StepStatusWaiting {
			continue
		}

		// Check if dependencies are met
		dependenciesMet, shouldSkip := se.checkDependencies(workflow, step)

		if shouldSkip {
			step.Status = StepStatusSkipped
			changed = true
			log.Printf("⏭️  Step %s (%s) skipped due to condition", step.ID, step.Name)
		} else if dependenciesMet {
			// Check condition if present
			shouldExecute, err := se.evaluateCondition(workflow, step)
			if err != nil {
				log.Printf("⚠️  Failed to evaluate condition for step %s: %v", step.ID, err)
				continue
			}

			if shouldExecute {
				step.Status = StepStatusReady
				changed = true
				log.Printf("✅ Step %s (%s) is ready to execute", step.ID, step.Name)
			} else {
				step.Status = StepStatusSkipped
				changed = true
				log.Printf("⏭️  Step %s (%s) skipped due to condition", step.ID, step.Name)
			}
		} else {
			if step.Status != StepStatusWaiting {
				step.Status = StepStatusWaiting
				changed = true
			}
		}
	}

	if changed {
		ws.UpdateWorkflow(*workflow)
		se.workspaceStore.Save(ws)
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
			log.Printf("⚠️  Dependency step %s not found", depID)
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
func (se *StepExecutor) evaluateOperator(actual interface{}, operator string, expected interface{}) bool {
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

// executeStep executes a single workflow step
func (se *StepExecutor) executeStep(ws *Workspace, workflow *Workflow, step *WorkflowStep) {
	// Create context with timeout
	timeout := step.Timeout
	if timeout == 0 {
		timeout = 10 * time.Minute // Default timeout
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)

	// Track running step
	se.mu.Lock()
	se.runningSteps[step.ID] = &stepExecution{
		WorkflowID: workflow.ID,
		Step:       *step,
		StartedAt:  time.Now(),
		Context:    ctx,
		Cancel:     cancel,
	}
	se.mu.Unlock()

	log.Printf("▶️  Executing step %s (%s) in workflow %s", step.ID, step.Name, workflow.Name)

	// Update step status to in_progress
	step.Status = StepStatusInProgress
	now := time.Now()
	step.StartedAt = &now

	// Update in workflow
	for i := range workflow.Steps {
		if workflow.Steps[i].ID == step.ID {
			workflow.Steps[i] = *step
			break
		}
	}

	ws.UpdateWorkflow(*workflow)
	se.workspaceStore.Save(ws)

	// Execute asynchronously
	se.wg.Add(1)
	go func() {
		defer se.wg.Done()
		defer cancel()
		defer func() {
			se.mu.Lock()
			delete(se.runningSteps, step.ID)
			se.mu.Unlock()
		}()

		var result string
		var execErr error

		// Execute based on step type
		switch step.Type {
		case StepTypeTask:
			result, execErr = se.executeTaskStep(ctx, ws, step)
		case StepTypeAggregate:
			result, execErr = se.executeAggregateStep(ctx, ws, workflow, step)
		default:
			execErr = fmt.Errorf("unsupported step type: %s", step.Type)
		}

		// Reload workspace (may have changed)
		ws, wsErr := se.workspaceStore.Get(ws.ID)
		if wsErr != nil {
			log.Printf("❌ Failed to reload workspace %s: %v", ws.ID, wsErr)
			return
		}

		// Reload workflow
		workflow, wfErr := ws.GetWorkflow(workflow.ID)
		if wfErr != nil {
			log.Printf("❌ Failed to reload workflow %s: %v", workflow.ID, wfErr)
			return
		}

		// Find the step in reloaded workflow
		var updatedStep *WorkflowStep
		for i := range workflow.Steps {
			if workflow.Steps[i].ID == step.ID {
				updatedStep = &workflow.Steps[i]
				break
			}
		}

		if updatedStep == nil {
			log.Printf("❌ Step %s not found in workflow after execution", step.ID)
			return
		}

		// Update step with result
		completedAt := time.Now()
		updatedStep.CompletedAt = &completedAt

		if execErr != nil {
			log.Printf("❌ Step %s (%s) failed: %v", step.ID, step.Name, execErr)
			updatedStep.Status = StepStatusFailed
			updatedStep.Error = execErr.Error()
		} else {
			log.Printf("✅ Step %s (%s) completed successfully", step.ID, step.Name)
			updatedStep.Status = StepStatusCompleted
			updatedStep.Result = result
		}

		// Update workflow with step
		for i := range workflow.Steps {
			if workflow.Steps[i].ID == step.ID {
				workflow.Steps[i] = *updatedStep
				break
			}
		}

		ws.UpdateWorkflow(*workflow)
		se.workspaceStore.Save(ws)
	}()
}

// executeTaskStep executes a task step by delegating to an agent
func (se *StepExecutor) executeTaskStep(ctx context.Context, ws *Workspace, step *WorkflowStep) (string, error) {
	if step.AssignedTo == "" {
		return "", fmt.Errorf("no agent assigned to step")
	}

	// Create task for this step
	task := Task{
		ID:          fmt.Sprintf("step-%s", step.ID),
		WorkspaceID: ws.ID,
		From:        ws.ParentAgent,
		To:          step.AssignedTo,
		Description: step.Description,
		Priority:    5,
		Context:     step.Context,
		Timeout:     step.Timeout,
		Status:      TaskStatusPending,
		CreatedAt:   time.Now(),
	}

	// Execute task via handler
	result, err := se.taskHandler.ExecuteTask(ctx, step.AssignedTo, task)
	if err != nil {
		return "", err
	}

	// Store task ID in step
	step.TaskID = task.ID

	return result, nil
}

// executeAggregateStep aggregates results from previous steps
func (se *StepExecutor) executeAggregateStep(ctx context.Context, ws *Workspace, workflow *Workflow, step *WorkflowStep) (string, error) {
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

// checkWorkflowCompletion checks if a workflow is complete
func (se *StepExecutor) checkWorkflowCompletion(ws *Workspace, workflow *Workflow) {
	allComplete := true
	anyFailed := false

	for _, step := range workflow.Steps {
		if step.Status == StepStatusFailed {
			anyFailed = true
		}

		if step.Status != StepStatusCompleted &&
			step.Status != StepStatusSkipped &&
			step.Status != StepStatusFailed {
			allComplete = false
		}
	}

	if allComplete {
		completedAt := time.Now()
		workflow.CompletedAt = &completedAt

		if anyFailed {
			workflow.Status = WorkflowStatusFailed
			log.Printf("❌ Workflow %s (%s) completed with failures", workflow.ID, workflow.Name)
		} else {
			workflow.Status = WorkflowStatusCompleted
			log.Printf("✅ Workflow %s (%s) completed successfully", workflow.ID, workflow.Name)
		}

		ws.UpdateWorkflow(*workflow)
		se.workspaceStore.Save(ws)
	}
}
