package orchestration

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/johnjallday/ori-agent/internal/agentcomm"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// executeResearchPipeline executes a full research, analysis, synthesis, validation pipeline
func (o *Orchestrator) executeResearchPipeline(ctx context.Context, ws *workspace.Workspace, task CollaborativeTask, agents []string) (*CollaborativeResult, error) {
	log.Printf("🔬 Executing full research pipeline")

	subResults := make(map[string]interface{})
	var researcherAgent, analyzerAgent, synthesizerAgent, validatorAgent string

	// Identify agents by role
	for _, agentName := range agents {
		agent, _ := o.agentStore.GetAgent(agentName)
		if agent == nil {
			continue
		}

		switch agent.Role {
		case types.RoleResearcher:
			researcherAgent = agentName
		case types.RoleAnalyzer:
			analyzerAgent = agentName
		case types.RoleSynthesizer:
			synthesizerAgent = agentName
		case types.RoleValidator:
			validatorAgent = agentName
		}
	}

	// Ensure we have required agents
	if researcherAgent == "" || analyzerAgent == "" || synthesizerAgent == "" {
		return nil, fmt.Errorf("missing required agents for research pipeline")
	}

	// Phase 1: Research
	log.Printf("📚 Phase 1: Research")
	researchTask, err := o.communicator.DelegateTask(agentcomm.DelegationRequest{
		WorkspaceID: ws.ID,
		From:        ws.ParentAgent,
		To:          researcherAgent,
		Description: fmt.Sprintf("Research: %s", task.Goal),
		Priority:    5,
		Context:     task.Context,
		Timeout:     task.MaxDuration / 4,
	})

	if err != nil {
		return nil, fmt.Errorf("failed to delegate research task: %w", err)
	}

	subResults["research_task"] = researchTask.ID
	subResults["researcher"] = researcherAgent
	log.Printf("📋 Research delegated to %s (task: %s)", researcherAgent, researchTask.ID)

	// Phase 2: Analysis
	log.Printf("🔍 Phase 2: Analysis")
	analysisTask, err := o.communicator.DelegateTask(agentcomm.DelegationRequest{
		WorkspaceID: ws.ID,
		From:        ws.ParentAgent,
		To:          analyzerAgent,
		Description: fmt.Sprintf("Analyze findings from research on: %s", task.Goal),
		Priority:    4,
		Context: map[string]interface{}{
			"research_task_id": researchTask.ID,
			"original_goal":    task.Goal,
		},
		Timeout: task.MaxDuration / 4,
	})

	if err != nil {
		log.Printf("⚠️  Failed to delegate analysis: %v", err)
	} else {
		subResults["analysis_task"] = analysisTask.ID
		subResults["analyzer"] = analyzerAgent
		log.Printf("📋 Analysis delegated to %s (task: %s)", analyzerAgent, analysisTask.ID)
	}

	// Phase 3: Synthesis
	log.Printf("✍️  Phase 3: Synthesis")
	synthesisTask, err := o.communicator.DelegateTask(agentcomm.DelegationRequest{
		WorkspaceID: ws.ID,
		From:        ws.ParentAgent,
		To:          synthesizerAgent,
		Description: fmt.Sprintf("Create comprehensive report combining research and analysis on: %s", task.Goal),
		Priority:    4,
		Context: map[string]interface{}{
			"research_task_id":  researchTask.ID,
			"analysis_task_id":  analysisTask.ID,
			"original_goal":     task.Goal,
		},
		Timeout: task.MaxDuration / 4,
	})

	if err != nil {
		log.Printf("⚠️  Failed to delegate synthesis: %v", err)
	} else {
		subResults["synthesis_task"] = synthesisTask.ID
		subResults["synthesizer"] = synthesizerAgent
		log.Printf("📋 Synthesis delegated to %s (task: %s)", synthesizerAgent, synthesisTask.ID)
	}

	// Phase 4: Validation (optional)
	if validatorAgent != "" {
		log.Printf("✅ Phase 4: Validation")
		validationTask, err := o.communicator.DelegateTask(agentcomm.DelegationRequest{
			WorkspaceID: ws.ID,
			From:        ws.ParentAgent,
			To:          validatorAgent,
			Description: fmt.Sprintf("Validate findings and check for inconsistencies in report on: %s", task.Goal),
			Priority:    3,
			Context: map[string]interface{}{
				"synthesis_task_id": synthesisTask.ID,
				"original_goal":     task.Goal,
			},
			Timeout: task.MaxDuration / 4,
		})

		if err != nil {
			log.Printf("⚠️  Failed to delegate validation: %v", err)
		} else {
			subResults["validation_task"] = validationTask.ID
			subResults["validator"] = validatorAgent
			log.Printf("📋 Validation delegated to %s (task: %s)", validatorAgent, validationTask.ID)
		}
	}

	// Build result summary
	summary := fmt.Sprintf(`Research Pipeline Initiated:
- Research: %s (task: %s)
- Analysis: %s (task: %s)
- Synthesis: %s (task: %s)`,
		researcherAgent, researchTask.ID,
		analyzerAgent, analysisTask.ID,
		synthesizerAgent, synthesisTask.ID,
	)

	if validatorAgent != "" {
		summary += fmt.Sprintf("\n- Validation: %s (task: %s)", validatorAgent, subResults["validation_task"])
	}

	return &CollaborativeResult{
		FinalOutput: summary,
		SubResults:  subResults,
	}, nil
}

// WorkflowStatus represents the status of a workflow execution
type WorkflowStatus struct {
	WorkspaceID string                 `json:"workspace_id"`
	Phase       string                 `json:"phase"`
	Progress    float64                `json:"progress"` // 0.0 to 1.0
	Tasks       map[string]TaskSummary `json:"tasks"`
	StartTime   time.Time              `json:"start_time"`
	UpdatedAt   time.Time              `json:"updated_at"`
}

// TaskSummary provides a summary of a task's status
type TaskSummary struct {
	Agent       string    `json:"agent"`
	Status      string    `json:"status"`
	Description string    `json:"description"`
	StartedAt   time.Time `json:"started_at"`
}

// GetWorkflowStatus retrieves the status of an ongoing workflow
func (o *Orchestrator) GetWorkflowStatus(workspaceID string) (*WorkflowStatus, error) {
	ws, err := o.workspaceStore.Get(workspaceID)
	if err != nil {
		return nil, fmt.Errorf("workspace not found: %w", err)
	}

	// Get all tasks for this workspace
	tasks := o.communicator.ListTasks(workspaceID)
	taskSummaries := make(map[string]TaskSummary)

	completedCount := 0
	totalCount := len(tasks)

	for _, task := range tasks {
		taskSummaries[task.ID] = TaskSummary{
			Agent:       task.To,
			Status:      string(task.Status),
			Description: task.Description,
			StartedAt:   task.CreatedAt,
		}

		if task.Status == workspace.TaskStatusCompleted ||
		   task.Status == workspace.TaskStatusFailed ||
		   task.Status == workspace.TaskStatusCancelled ||
		   task.Status == workspace.TaskStatusTimeout {
			completedCount++
		}
	}

	// Calculate progress
	progress := 0.0
	if totalCount > 0 {
		progress = float64(completedCount) / float64(totalCount)
	}

	// Determine current phase
	phase := "initializing"
	if progress > 0 && progress < 0.5 {
		phase = "executing"
	} else if progress >= 0.5 && progress < 1.0 {
		phase = "finalizing"
	} else if progress == 1.0 {
		phase = "completed"
	}

	return &WorkflowStatus{
		WorkspaceID: workspaceID,
		Phase:       phase,
		Progress:    progress,
		Tasks:       taskSummaries,
		StartTime:   ws.CreatedAt,
		UpdatedAt:   ws.UpdatedAt,
	}, nil
}

// AggregateResults collects and combines results from multiple tasks
func (o *Orchestrator) AggregateResults(workspaceID string, taskIDs []string) (string, error) {
	results := make([]string, 0)

	for _, taskID := range taskIDs {
		task, err := o.communicator.GetTask(taskID)
		if err != nil {
			log.Printf("⚠️  Failed to get task %s: %v", taskID, err)
			continue
		}

		if task.Status == workspace.TaskStatusCompleted && task.Result != "" {
			results = append(results, fmt.Sprintf("[%s]: %s", task.To, task.Result))
		}
	}

	if len(results) == 0 {
		return "", fmt.Errorf("no completed tasks with results found")
	}

	// Combine results
	aggregated := "Aggregated Results:\n\n"
	for i, result := range results {
		aggregated += fmt.Sprintf("%d. %s\n\n", i+1, result)
	}

	return aggregated, nil
}
