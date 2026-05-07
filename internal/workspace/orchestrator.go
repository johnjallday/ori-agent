package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/openai/openai-go/v3"
)

// Orchestrator manages autonomous task delegation and agent coordination
type Orchestrator struct {
	workspaceStore Store
	agentStore     store.Store  // For loading agents and their tools
	llmProvider    LLMProvider  // For intelligent task breakdown
	eventBus       *EventBus    // For real-time updates
	taskHandler    taskExecutor // For single-task LLM execution (delegated)
}

// LLMProvider interface for calling AI models
type LLMProvider interface {
	ChatCompletion(ctx context.Context, messages []openai.ChatCompletionMessageParamUnion, tools []openai.ChatCompletionToolUnionParam) (*openai.ChatCompletion, error)
	// ChatWithTools provides tool-calling support using llm.Tool type
	ChatWithTools(ctx context.Context, systemPrompt, userPrompt string, tools []llm.Tool) (*llm.ChatResponse, error)
	// ChatWithMessages continues a conversation with full message history
	ChatWithMessages(ctx context.Context, messages []llm.Message, tools []llm.Tool) (*llm.ChatResponse, error)
}

// taskExecutor describes the single-task LLM execution surface the orchestrator
// delegates to. LLMTaskHandler.ExecuteTask satisfies this interface; it owns
// MCP/workspace tool loading and the multi-turn tool-call loop, so the
// orchestrator does not duplicate that work.
type taskExecutor interface {
	ExecuteTask(ctx context.Context, agentName string, task Task) (string, error)
}

// NewOrchestrator creates a new orchestrator
func NewOrchestrator(workspaceStore Store, agentStore store.Store, llmProvider LLMProvider, eventBus *EventBus) *Orchestrator {
	return &Orchestrator{
		workspaceStore: workspaceStore,
		agentStore:     agentStore,
		llmProvider:    llmProvider,
		eventBus:       eventBus,
	}
}

// SetTaskHandler wires the task executor used for single-task LLM execution.
// Must be called before ExecuteTask, otherwise ExecuteTask returns an error.
func (o *Orchestrator) SetTaskHandler(h taskExecutor) {
	o.taskHandler = h
}

// ExecuteMission starts autonomous execution of a mission
func (o *Orchestrator) ExecuteMission(ctx context.Context, workspaceID string, mission string) error {
	workspace, err := o.workspaceStore.Get(workspaceID)
	if err != nil {
		return fmt.Errorf("failed to get workspace: %w", err)
	}

	logger.Debug("[Orchestrator] Starting mission for workspace", logger.Fields{"workspace_id": workspaceID, "mission": mission})

	// Step 1: Analyze the mission and break it down into tasks
	tasks, err := o.analyzeMission(ctx, mission, workspace.Agents)
	if err != nil {
		return fmt.Errorf("failed to analyze mission: %w", err)
	}

	logger.Info("[Orchestrator] Created tasks from mission", logger.Fields{"task_id": len(tasks)})

	// Step 2: Add tasks to the workspace
	for _, task := range tasks {
		task.WorkspaceID = workspaceID
		if err := workspace.AddTask(task); err != nil {
			logger.Error("[Orchestrator] Warning: failed to add task", logger.Fields{"task_id": task.ID, "err": err})
		}

		// Publish task creation event
		o.publishEvent("task_created", workspaceID, map[string]interface{}{
			"task_id":     task.ID,
			"description": task.Description,
			"assigned_to": task.To,
			"priority":    task.Priority,
		})
	}

	// Save updated workspace
	if err := o.workspaceStore.Save(workspace); err != nil {
		logger.Error("[Orchestrator] Warning: failed to save workspace", logger.Fields{"error": err})
	}

	// Step 3: Start task execution in background
	go o.ExecuteTasksSequentially(ctx, workspaceID, tasks)

	return nil
}

// analyzeMission uses LLM to break down a mission into tasks
func (o *Orchestrator) analyzeMission(ctx context.Context, mission string, availableAgents []string) ([]Task, error) {
	// Create a system prompt for task breakdown
	systemPrompt := fmt.Sprintf(`You are an intelligent task orchestrator. Your job is to break down a high-level mission into specific tasks and delegate them to available agents.

Available agents and their capabilities:
%s

Analyze the mission and create a list of tasks. For each task:
1. Provide a clear, actionable description
2. Assign it to the most appropriate agent based on their capabilities
3. Set a priority (1-10, higher = more urgent)
4. Identify dependencies (which tasks must complete first)

Return your response as a JSON array of tasks in this format:
[
  {
    "description": "Task description",
    "assigned_to": "agent_name",
    "priority": 5,
    "dependencies": []
  }
]`, o.formatAgentCapabilities(availableAgents))

	userPrompt := fmt.Sprintf("Mission: %s\n\nBreak this down into specific tasks for the available agents.", mission)

	messages := []openai.ChatCompletionMessageParamUnion{
		openai.SystemMessage(systemPrompt),
		openai.UserMessage(userPrompt),
	}

	// Call LLM to analyze mission
	completion, err := o.llmProvider.ChatCompletion(ctx, messages, nil)
	if err != nil {
		if friendlyMsg := classifyContextError(err); friendlyMsg != "" {
			return nil, fmt.Errorf("%s", friendlyMsg)
		}
		return nil, fmt.Errorf("LLM call failed: %w", err)
	}

	if len(completion.Choices) == 0 {
		return nil, fmt.Errorf("no completion choices returned")
	}

	content := completion.Choices[0].Message.Content

	// Parse JSON response
	var taskSpecs []struct {
		Description  string   `json:"description"`
		AssignedTo   string   `json:"assigned_to"`
		Priority     int      `json:"priority"`
		Dependencies []string `json:"dependencies"`
	}

	if err := json.Unmarshal([]byte(content), &taskSpecs); err != nil {
		logger.Error("[Orchestrator] Warning: Failed to parse LLM response as JSON: . Content", logger.Fields{"response": err, "content": content})
		// Fallback: create a single task
		return []Task{
			{
				ID:          uuid.New().String(),
				From:        "orchestrator",
				To:          availableAgents[0], // Assign to first agent
				Description: mission,
				Priority:    5,
				Context:     map[string]interface{}{"original_mission": mission},
				Status:      TaskStatusPending,
				CreatedAt:   time.Now(),
			},
		}, nil
	}

	// Convert to Task structs
	tasks := make([]Task, len(taskSpecs))
	for i, spec := range taskSpecs {
		tasks[i] = Task{
			ID:          uuid.New().String(),
			From:        "orchestrator",
			To:          spec.AssignedTo,
			Description: spec.Description,
			Priority:    spec.Priority,
			Context: map[string]interface{}{
				"original_mission": mission,
				"dependencies":     spec.Dependencies,
				"task_index":       i,
			},
			Status:    TaskStatusPending,
			CreatedAt: time.Now(),
		}
	}

	return tasks, nil
}

// executeTasksSequentially executes tasks in priority order
func (o *Orchestrator) ExecuteTasksSequentially(ctx context.Context, workspaceID string, tasks []Task) {
	logger.Debug("[Orchestrator] Starting sequential task execution for workspace", logger.Fields{"workspace_id": workspaceID})

	for _, task := range tasks {
		select {
		case <-ctx.Done():
			logger.Debug("[Orchestrator] Context cancelled, stopping task execution", logger.Fields{})
			return
		default:
			// Execute the task
			if err := o.ExecuteTask(ctx, workspaceID, task); err != nil {
				logger.Error("[Orchestrator] Task failed", logger.Fields{"task_id": task.ID, "err": err})
				o.publishEvent("task_failed", workspaceID, map[string]interface{}{
					"task_id": task.ID,
					"error":   err.Error(),
				})
			}
		}
	}

	logger.Info("[Orchestrator] All tasks completed for workspace", logger.Fields{"task_id": workspaceID})
	o.publishEvent("mission_completed", workspaceID, map[string]interface{}{
		"total_tasks": len(tasks),
	})
}

// ExecuteTask executes a single task by delegating to an agent.
// LLM execution (tool loading, model/provider selection, tool-call loop) is
// delegated to the LLMTaskHandler wired via SetTaskHandler; the orchestrator
// owns workspace state, message events, and task lifecycle bookkeeping.
func (o *Orchestrator) ExecuteTask(ctx context.Context, workspaceID string, task Task) error {
	logger.Debug("[Orchestrator] Executing task : (assigned to: )", logger.Fields{"task_id": task.ID, "description": task.Description, "to": task.To})

	workspace, err := o.workspaceStore.Get(workspaceID)
	if err != nil {
		return fmt.Errorf("failed to get workspace: %w", err)
	}

	// Inject input task results into task context if InputTaskIDs are specified
	if len(task.InputTaskIDs) > 0 {
		logger.Debug("🔗 Task has input task IDs", logger.Fields{"task_id": task.ID, "inputtaskids)": len(task.InputTaskIDs), "inputtaskids": task.InputTaskIDs})
		enrichedContext := workspace.GetInputContext(&task)
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
				logger.Debug("- Task result", logger.Fields{"preview": preview, "task_id": taskID})
			}
		} else {
			logger.Warn("Warning: No input results found for task despite having InputTaskIDs", logger.Fields{"task_id": task.ID})
		}
	} else {
		logger.Debug("ℹ️ Task has no input task IDs", logger.Fields{"task_id": task.ID})
	}

	// Update task status to in_progress and post the task-request message in
	// one atomic Update so a concurrent goroutine cannot drop either change.
	now := time.Now()
	task.Status = TaskStatusInProgress
	task.StartedAt = &now

	message := AgentMessage{
		ID:        uuid.New().String(),
		From:      "orchestrator",
		To:        task.To,
		Type:      MessageTaskRequest,
		Content:   task.Description,
		Metadata:  task.Context,
		Timestamp: time.Now(),
	}

	if updateErr := o.workspaceStore.Update(workspaceID, func(fresh *Workspace) error {
		if err := fresh.MutateTask(task.ID, func(t *Task) error {
			t.Status = TaskStatusInProgress
			t.StartedAt = &now
			return nil
		}); err != nil {
			return err
		}
		return fresh.AddMessage(message)
	}); updateErr != nil {
		logger.Error("[Orchestrator] Warning: failed to start task", logger.Fields{"task_id": task.ID, "error": updateErr})
	}

	o.publishEvent("task_started", workspaceID, map[string]interface{}{
		"task_id":     task.ID,
		"assigned_to": task.To,
	})

	o.publishEvent("message_sent", workspaceID, map[string]interface{}{
		"from":    message.From,
		"to":      message.To,
		"content": message.Content,
	})

	// Execute task using the wired task handler. Tool loading (MCP + workspace),
	// the multi-turn tool-call loop, and provider/model selection all live in
	// LLMTaskHandler — the orchestrator does not duplicate that work.
	// A missing handler is treated as a task failure (rather than an early
	// return) so the workspace state and lifecycle events stay consistent.
	var result string
	if o.taskHandler == nil {
		err = fmt.Errorf("orchestrator: task handler not configured (call SetTaskHandler before ExecuteTask)")
	} else {
		result, err = o.taskHandler.ExecuteTask(ctx, task.To, task)
	}

	completed := time.Now()
	if err != nil {
		// Task failed
		logger.Error("[Orchestrator] Task failed", logger.Fields{"task_id": task.ID, "err": err})
		task.Status = TaskStatusFailed
		task.CompletedAt = &completed
		task.Error = err.Error()

		if updateErr := MutateTaskAndSave(o.workspaceStore, workspace, task.ID, func(t *Task) error {
			t.Status = TaskStatusFailed
			t.CompletedAt = &completed
			t.Error = err.Error()
			return nil
		}); updateErr != nil {
			logger.Error("[Orchestrator] Warning: failed to record task failure", logger.Fields{"task_id": task.ID, "error": updateErr})
		}

		o.publishEvent("task_failed", workspaceID, map[string]interface{}{
			"task_id": task.ID,
			"error":   err.Error(),
		})

		return err
	}

	// Mark task as completed
	task.Status = TaskStatusCompleted
	task.CompletedAt = &completed
	task.Result = result
	ApplyTaskResultMetadata(&task, result)
	if err := MutateTaskAndSave(o.workspaceStore, workspace, task.ID, func(t *Task) error {
		t.Status = TaskStatusCompleted
		t.CompletedAt = &completed
		t.Result = result
		ApplyTaskResultMetadata(t, result)
		return nil
	}); err != nil {
		logger.Error("[Orchestrator] Warning: failed to record task completion", logger.Fields{"task_id": task.ID, "error": err})
	}

	o.publishEvent("task_completed", workspaceID, map[string]interface{}{
		"task_id": task.ID,
		"result":  task.Result,
	})

	return nil
}

// formatAgentCapabilities formats agent list with capabilities.
// Note: Currently shows generic capabilities. To show real capabilities:
// - Load agent config from store using o.agentStore.GetAgent(agent)
// - Extract capabilities from agent.Capabilities slice
// - Include agent role (agent.Role) in the output
func (o *Orchestrator) formatAgentCapabilities(agents []string) string {
	var sb strings.Builder
	for _, agentName := range agents {
		ag, ok := o.agentStore.GetAgent(agentName)
		if !ok || ag == nil {
			sb.WriteString(fmt.Sprintf("- %s: General purpose agent\n", agentName))
			continue
		}
		caps := strings.Join(ag.Capabilities, ", ")
		if caps == "" {
			caps = "none"
		}
		sb.WriteString(fmt.Sprintf("- %s (role: %s; capabilities: %s)\n", agentName, ag.Role, caps))
	}
	return sb.String()
}

// publishEvent publishes an event to the event bus
func (o *Orchestrator) publishEvent(eventType string, workspaceID string, data map[string]interface{}) {
	if o.eventBus == nil {
		return
	}

	event := Event{
		ID:          uuid.New().String(),
		Type:        EventType(eventType),
		WorkspaceID: workspaceID,
		Timestamp:   time.Now(),
		Source:      "orchestrator",
		Data:        data,
		Metadata:    make(map[string]string),
	}

	o.eventBus.Publish(event)
}
