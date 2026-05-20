package workspace

import (
	"fmt"
	"time"
)

// WorkflowStep represents a single step in a multi-step workflow
type WorkflowStep struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Type        StepType       `json:"type"`
	Status      StepStatus     `json:"status"`
	Order       int            `json:"order"`       // Execution order
	DependsOn   []string       `json:"depends_on"`  // Step IDs this step depends on
	AssignedTo  string         `json:"assigned_to"` // Agent name
	TaskID      string         `json:"task_id"`     // Created task ID
	Result      string         `json:"result"`
	Error       string         `json:"error,omitempty"`
	Condition   *StepCondition `json:"condition,omitempty"` // Conditional execution
	Context     map[string]any `json:"context"`
	Timeout     time.Duration  `json:"timeout"`
	CreatedAt   time.Time      `json:"created_at"`
	StartedAt   *time.Time     `json:"started_at,omitempty"`
	CompletedAt *time.Time     `json:"completed_at,omitempty"`
}

// StepType represents the type of workflow step
type StepType string

const (
	StepTypeTask       StepType = "task"       // Delegate to agent
	StepTypeAggregate  StepType = "aggregate"  // Aggregate results from previous steps
	StepTypeCondition  StepType = "condition"  // Conditional branching
	StepTypeParallel   StepType = "parallel"   // Parallel execution group
	StepTypeSequential StepType = "sequential" // Sequential execution group
)

// StepStatus represents the status of a workflow step
type StepStatus string

const (
	StepStatusPending    StepStatus = "pending"
	StepStatusWaiting    StepStatus = "waiting" // Waiting for dependencies
	StepStatusReady      StepStatus = "ready"   // Dependencies met, ready to execute
	StepStatusInProgress StepStatus = "in_progress"
	StepStatusCompleted  StepStatus = "completed"
	StepStatusFailed     StepStatus = "failed"
	StepStatusSkipped    StepStatus = "skipped" // Skipped due to condition
	StepStatusCancelled  StepStatus = "cancelled"
)

// StepCondition defines a condition for step execution
type StepCondition struct {
	Type     ConditionType `json:"type"`
	StepID   string        `json:"step_id"`  // Step to evaluate
	Operator string        `json:"operator"` // eq, ne, contains, exists
	Value    any           `json:"value"`
	OnTrue   string        `json:"on_true"`  // Action if true: "execute", "skip"
	OnFalse  string        `json:"on_false"` // Action if false: "execute", "skip"
}

// ConditionType defines types of conditions
type ConditionType string

const (
	ConditionTypePreviousResult ConditionType = "previous_result"
	ConditionTypeStepStatus     ConditionType = "step_status"
	ConditionTypeContextValue   ConditionType = "context_value"
)

// Workflow represents a multi-step workflow definition
type Workflow struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	WorkspaceID string         `json:"workspace_id"`
	Steps       []WorkflowStep `json:"steps"`
	Status      WorkflowStatus `json:"status"`
	Context     map[string]any `json:"context"`
	CreatedAt   time.Time      `json:"created_at"`
	StartedAt   *time.Time     `json:"started_at,omitempty"`
	CompletedAt *time.Time     `json:"completed_at,omitempty"`
}

// WorkflowStatus represents the overall workflow status
type WorkflowStatus string

const (
	WorkflowStatusPending    WorkflowStatus = "pending"
	WorkflowStatusInProgress WorkflowStatus = "in_progress"
	WorkflowStatusCompleted  WorkflowStatus = "completed"
	WorkflowStatusFailed     WorkflowStatus = "failed"
	WorkflowStatusCancelled  WorkflowStatus = "cancelled"
)

// AddWorkflow adds a workflow to the workspace
func (w *Workspace) AddWorkflow(workflow Workflow) error {
	if w.Workflows == nil {
		w.Workflows = make(map[string]Workflow)
	}

	workflow.WorkspaceID = w.ID
	w.Workflows[workflow.ID] = workflow
	w.UpdatedAt = time.Now()

	return nil
}

// GetWorkflow retrieves a workflow by ID
func (w *Workspace) GetWorkflow(workflowID string) (*Workflow, error) {
	if w.Workflows == nil {
		return nil, fmt.Errorf("no workflows in workspace")
	}

	workflow, exists := w.Workflows[workflowID]
	if !exists {
		return nil, fmt.Errorf("workflow %s not found", workflowID)
	}

	return &workflow, nil
}

// UpdateWorkflow updates a workflow
func (w *Workspace) UpdateWorkflow(workflow Workflow) error {
	if w.Workflows == nil {
		return fmt.Errorf("no workflows in workspace")
	}

	if _, exists := w.Workflows[workflow.ID]; !exists {
		return fmt.Errorf("workflow %s not found", workflow.ID)
	}

	w.Workflows[workflow.ID] = workflow
	w.UpdatedAt = time.Now()

	return nil
}

// MutateWorkflowStep applies fn to the step identified by stepID inside the
// workflow identified by workflowID, while holding w.mu — the analogue of
// MutateTask for steps. fn receives a pointer to the live slice element and
// may mutate it freely; returning a non-nil error aborts the mutation (the
// in-memory state is left untouched). On success, UpdatedAt is bumped.
//
// fn must not call back into Workspace methods that take w.mu (deadlock) and
// must not retain the *WorkflowStep beyond its own scope.
//
// Cross-instance race safety: pair this with Store.Update via
// MutateWorkflowStepAndSave so the read-modify-write is serialized against
// other instances/handlers operating on the same workspace.
func (w *Workspace) MutateWorkflowStep(workflowID, stepID string, fn func(*WorkflowStep) error) error {
	if fn == nil {
		return fmt.Errorf("MutateWorkflowStep: fn is nil")
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	if w.Workflows == nil {
		return fmt.Errorf("workflow %s not found", workflowID)
	}
	workflow, exists := w.Workflows[workflowID]
	if !exists {
		return fmt.Errorf("workflow %s not found", workflowID)
	}

	stepIdx := -1
	for i := range workflow.Steps {
		if workflow.Steps[i].ID == stepID {
			stepIdx = i
			break
		}
	}
	if stepIdx == -1 {
		return fmt.Errorf("step %s not found in workflow %s", stepID, workflowID)
	}

	if err := fn(&workflow.Steps[stepIdx]); err != nil {
		return err
	}

	// Write the workflow back so map-stored scalar fields stay consistent
	// with the slice mutation we just performed (slice elements share their
	// backing array, but assigning the workflow value defends against future
	// fn implementations that assign whole-step replacements).
	w.Workflows[workflowID] = workflow
	w.UpdatedAt = time.Now()
	return nil
}

// MutateWorkflowStepAndSave applies fn to the step identified by workflowID/
// stepID and persists the workspace via store. Cross-instance safe via
// store.Update.
//
// The caller's ws argument is advisory — its fields are not the ones fn
// mutates and it must not be read after this call returns; re-Get the
// workspace if you need the post-mutation state.
func MutateWorkflowStepAndSave(store Store, ws *Workspace, workflowID, stepID string, fn func(*WorkflowStep) error) error {
	return store.Update(ws.ID, func(fresh *Workspace) error {
		return fresh.MutateWorkflowStep(workflowID, stepID, fn)
	})
}

// ListWorkflows returns all workflows in the workspace
func (w *Workspace) ListWorkflows() []Workflow {
	if w.Workflows == nil {
		return []Workflow{}
	}

	workflows := make([]Workflow, 0, len(w.Workflows))
	for _, wf := range w.Workflows {
		workflows = append(workflows, wf)
	}

	return workflows
}

// GetStepsByStatus returns all steps with a given status from all workflows
func (w *Workspace) GetStepsByStatus(status StepStatus) []WorkflowStep {
	steps := make([]WorkflowStep, 0)

	for _, workflow := range w.Workflows {
		for _, step := range workflow.Steps {
			if step.Status == status {
				steps = append(steps, step)
			}
		}
	}

	return steps
}

// GetReadySteps returns all steps that are ready to execute (dependencies met)
func (w *Workspace) GetReadySteps() []WorkflowStep {
	steps := make([]WorkflowStep, 0)

	for _, workflow := range w.Workflows {
		// Skip completed/failed workflows
		if workflow.Status == WorkflowStatusCompleted ||
			workflow.Status == WorkflowStatusFailed ||
			workflow.Status == WorkflowStatusCancelled {
			continue
		}

		for _, step := range workflow.Steps {
			if step.Status == StepStatusReady {
				steps = append(steps, step)
			}
		}
	}

	return steps
}
