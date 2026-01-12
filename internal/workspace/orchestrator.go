package workspace

import (
	"context"
	"encoding/json"
	"fmt"

	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/pluginapi"
	"github.com/openai/openai-go/v3"
)

// Orchestrator manages autonomous task delegation and agent coordination
type Orchestrator struct {
	studioStore Store
	agentStore  store.Store // For loading agents and their tools
	llmProvider LLMProvider // For intelligent task breakdown
	eventBus    *EventBus   // For real-time updates
}

// LLMProvider interface for calling AI models
type LLMProvider interface {
	ChatCompletion(ctx context.Context, messages []openai.ChatCompletionMessageParamUnion, tools []openai.ChatCompletionToolUnionParam) (*openai.ChatCompletion, error)
	// ChatWithTools provides tool-calling support using llm.Tool type
	ChatWithTools(ctx context.Context, systemPrompt, userPrompt string, tools []llm.Tool) (*llm.ChatResponse, error)
	// ChatWithMessages continues a conversation with full message history
	ChatWithMessages(ctx context.Context, messages []llm.Message, tools []llm.Tool) (*llm.ChatResponse, error)
}

// NewOrchestrator creates a new orchestrator
func NewOrchestrator(studioStore Store, agentStore store.Store, llmProvider LLMProvider, eventBus *EventBus) *Orchestrator {
	return &Orchestrator{
		studioStore: studioStore,
		agentStore:  agentStore,
		llmProvider: llmProvider,
		eventBus:    eventBus,
	}
}

// ExecuteMission starts autonomous execution of a mission
func (o *Orchestrator) ExecuteMission(ctx context.Context, studioID string, mission string) error {
	studio, err := o.studioStore.Get(studioID)
	if err != nil {
		return fmt.Errorf("failed to get studio: %w", err)
	}

	logger.Debug("[Orchestrator] Starting mission for studio", logger.Fields{"workspace_id": studioID, "mission": mission})

	// Step 1: Analyze the mission and break it down into tasks
	tasks, err := o.analyzeMission(ctx, mission, studio.Agents)
	if err != nil {
		return fmt.Errorf("failed to analyze mission: %w", err)
	}

	logger.Info("[Orchestrator] Created tasks from mission", logger.Fields{"task_id": len(tasks)})

	// Step 2: Add tasks to the studio
	for _, task := range tasks {
		task.WorkspaceID = studioID
		if err := studio.AddTask(task); err != nil {
			logger.Error("[Orchestrator] Warning: failed to add task", logger.Fields{"task_id": task.ID, "err": err})
		}

		// Publish task creation event
		o.publishEvent("task_created", studioID, map[string]interface{}{
			"task_id":     task.ID,
			"description": task.Description,
			"assigned_to": task.To,
			"priority":    task.Priority,
		})
	}

	// Save updated studio
	if err := o.studioStore.Save(studio); err != nil {
		logger.Error("[Orchestrator] Warning: failed to save studio", logger.Fields{"error": err})
	}

	// Step 3: Start task execution in background
	go o.ExecuteTasksSequentially(ctx, studioID, tasks)

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
func (o *Orchestrator) ExecuteTasksSequentially(ctx context.Context, studioID string, tasks []Task) {
	logger.Debug("[Orchestrator] Starting sequential task execution for studio", logger.Fields{"workspace_id": studioID})

	for _, task := range tasks {
		select {
		case <-ctx.Done():
			logger.Debug("[Orchestrator] Context cancelled, stopping task execution", logger.Fields{})
			return
		default:
			// Execute the task
			if err := o.ExecuteTask(ctx, studioID, task); err != nil {
				logger.Error("[Orchestrator] Task failed", logger.Fields{"task_id": task.ID, "err": err})
				o.publishEvent("task_failed", studioID, map[string]interface{}{
					"task_id": task.ID,
					"error":   err.Error(),
				})
			}
		}
	}

	logger.Info("[Orchestrator] All tasks completed for studio", logger.Fields{"task_id": studioID})
	o.publishEvent("mission_completed", studioID, map[string]interface{}{
		"total_tasks": len(tasks),
	})
}

// ExecuteTask executes a single task by delegating to an agent
func (o *Orchestrator) ExecuteTask(ctx context.Context, studioID string, task Task) error {
	logger.Debug("[Orchestrator] Executing task : (assigned to: )", logger.Fields{"task_id": task.ID, "description": task.Description, "to": task.To})

	studio, err := o.studioStore.Get(studioID)
	if err != nil {
		return fmt.Errorf("failed to get studio: %w", err)
	}

	// Inject input task results into task context if InputTaskIDs are specified
	if len(task.InputTaskIDs) > 0 {
		logger.Debug("🔗 Task has input task IDs", logger.Fields{"task_id": task.ID, "inputtaskids)": len(task.InputTaskIDs), "inputtaskids": task.InputTaskIDs})
		enrichedContext := studio.GetInputContext(&task)
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

	// Update task status to in_progress
	now := time.Now()
	task.Status = TaskStatusInProgress
	task.StartedAt = &now
	if err := studio.UpdateTask(task); err != nil {
		logger.Error("[Orchestrator] Warning: failed to update task", logger.Fields{"task_id": err})
	}

	o.publishEvent("task_started", studioID, map[string]interface{}{
		"task_id":     task.ID,
		"assigned_to": task.To,
	})

	// Send message to the assigned agent
	message := AgentMessage{
		ID:        uuid.New().String(),
		From:      "orchestrator",
		To:        task.To,
		Type:      MessageTaskRequest,
		Content:   task.Description,
		Metadata:  task.Context,
		Timestamp: time.Now(),
	}

	if err := studio.AddMessage(message); err != nil {
		return fmt.Errorf("failed to send message: %w", err)
	}

	// Save studio with updated task and message
	if err := o.studioStore.Save(studio); err != nil {
		logger.Error("[Orchestrator] Warning: failed to save studio", logger.Fields{"error": err})
	}

	o.publishEvent("message_sent", studioID, map[string]interface{}{
		"from":    message.From,
		"to":      message.To,
		"content": message.Content,
	})

	// Execute task using LLM provider
	result, err := o.executeTaskWithLLM(ctx, task)

	completed := time.Now()
	if err != nil {
		// Task failed
		logger.Error("[Orchestrator] Task failed", logger.Fields{"task_id": task.ID, "err": err})
		task.Status = TaskStatusFailed
		task.CompletedAt = &completed
		task.Error = err.Error()

		if updateErr := studio.UpdateTask(task); updateErr != nil {
			logger.Error("[Orchestrator] Warning: failed to update task", logger.Fields{"task_id": updateErr})
		}

		if saveErr := o.studioStore.Save(studio); saveErr != nil {
			logger.Error("[Orchestrator] Warning: failed to save studio", logger.Fields{"workspace_id": saveErr})
		}

		o.publishEvent("task_failed", studioID, map[string]interface{}{
			"task_id": task.ID,
			"error":   err.Error(),
		})

		return err
	}

	// Mark task as completed
	task.Status = TaskStatusCompleted
	task.CompletedAt = &completed
	task.Result = result
	if err := studio.UpdateTask(task); err != nil {
		logger.Error("[Orchestrator] Warning: failed to update task", logger.Fields{"task_id": err})
	}

	// Save final studio state
	if err := o.studioStore.Save(studio); err != nil {
		logger.Error("[Orchestrator] Warning: failed to save studio", logger.Fields{"error": err})
	}

	o.publishEvent("task_completed", studioID, map[string]interface{}{
		"task_id": task.ID,
		"result":  task.Result,
	})

	return nil
}

// executeTaskWithLLM executes a task using the LLM provider with tool support
func (o *Orchestrator) executeTaskWithLLM(ctx context.Context, task Task) (string, error) {
	if o.llmProvider == nil {
		return "", fmt.Errorf("LLM provider not configured")
	}

	logger.Debug("[Orchestrator] Executing task with LLM", logger.Fields{"task_id": task.ID, "description": task.Description, "assigned_to": task.To})

	// Load the agent to get its tools
	var tools []llm.Tool
	var ag *agent.Agent
	if o.agentStore != nil && task.To != "" {
		var ok bool
		ag, ok = o.agentStore.GetAgent(task.To)
		if ok && ag != nil {
			// Build tools from agent's plugins
			for _, pl := range ag.Plugins {
				var def pluginapi.Tool
				if pl.Tool != nil {
					def = pl.Tool.Definition()
				} else {
					def = pl.Definition
				}
				tools = append(tools, llm.Tool{
					Name:        def.Name,
					Description: def.Description,
					Parameters:  def.Parameters,
				})
			}
			logger.Debug("[Orchestrator] Loaded agent tools", logger.Fields{"agent": task.To, "tool_count": len(tools)})
		} else {
			logger.Warn("[Orchestrator] Agent not found, executing without tools", logger.Fields{"agent": task.To})
		}
	}

	// Create system prompt with agent role and tool guidance
	systemPrompt := fmt.Sprintf(
		"You are %s, an AI agent in a multi-agent workspace. You have been assigned a task. "+
			"Please complete the task to the best of your ability and provide a clear result.",
		task.To,
	)

	// Add tool guidance if tools are available
	if len(tools) > 0 {
		var toolNames []string
		for _, t := range tools {
			toolNames = append(toolNames, t.Name)
		}
		systemPrompt += fmt.Sprintf(" You have access to the following tools: %s. Use them when appropriate to complete your task.",
			fmt.Sprintf("%v", toolNames))
	}

	// Build user message with task description and formatted context
	taskPrompt := fmt.Sprintf("# Task Assignment\n\n%s\n\n", task.Description)

	// Format input task results if available
	if inputResults, ok := task.Context["input_task_results"]; ok {
		if resultsMap, ok := inputResults.(map[string]string); ok && len(resultsMap) > 0 {
			taskPrompt += "## Input from Previous Tasks\n\n"
			for taskID, result := range resultsMap {
				taskPrompt += fmt.Sprintf("**Task %s Result:**\n```\n%s\n```\n\n", taskID, result)
			}
		}
	}

	// Include other context fields
	hasOtherContext := false
	for key := range task.Context {
		if key != "input_task_results" {
			hasOtherContext = true
			break
		}
	}

	if hasOtherContext {
		taskPrompt += "## Additional Context\n\n"
		for key, value := range task.Context {
			if key != "input_task_results" {
				taskPrompt += fmt.Sprintf("- **%s**: %v\n", key, value)
			}
		}
		taskPrompt += "\n"
	}

	taskPrompt += "Please complete this task. If you have tools available, use them to accomplish the task rather than providing general information."

	// Call LLM with timeout and tools
	llmCtx, cancel := context.WithTimeout(ctx, 60*time.Second) // Longer timeout for tool execution
	defer cancel()

	resp, err := o.llmProvider.ChatWithTools(llmCtx, systemPrompt, taskPrompt, tools)
	if err != nil {
		if friendlyMsg := classifyContextError(err); friendlyMsg != "" {
			return "", fmt.Errorf("%s", friendlyMsg)
		}
		return "", fmt.Errorf("LLM call failed: %w", err)
	}

	// Check if LLM wants to call tools
	if len(resp.ToolCalls) > 0 && ag != nil {
		logger.Debug("[Orchestrator] LLM requested tool calls", logger.Fields{"count": len(resp.ToolCalls)})
		return o.executeToolCallsAndContinue(llmCtx, ag, systemPrompt, taskPrompt, tools, resp)
	}

	result := resp.Content
	logger.Info("[Orchestrator] Task completed with result", logger.Fields{"result_length": len(result)})

	return result, nil
}

// executeToolCallsAndContinue executes tool calls and continues the conversation
func (o *Orchestrator) executeToolCallsAndContinue(ctx context.Context, ag *agent.Agent, systemPrompt, taskPrompt string, tools []llm.Tool, resp *llm.ChatResponse) (string, error) {
	maxIterations := 5 // Prevent infinite loops
	messages := []llm.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: taskPrompt},
		{Role: "assistant", Content: resp.Content, ToolCalls: resp.ToolCalls},
	}

	for iteration := 0; iteration < maxIterations && len(resp.ToolCalls) > 0; iteration++ {
		logger.Debug("[Orchestrator] Executing tool calls", logger.Fields{"iteration": iteration, "tool_count": len(resp.ToolCalls)})

		// Execute each tool call
		for _, toolCall := range resp.ToolCalls {
			toolResult := o.executeToolCall(ctx, ag, toolCall)
			messages = append(messages, llm.Message{
				Role:       "tool",
				Content:    toolResult,
				ToolCallID: toolCall.ID,
			})
			logger.Debug("[Orchestrator] Tool call executed", logger.Fields{"tool": toolCall.Name, "result_length": len(toolResult)})
		}

		// Continue conversation with tool results using full message history
		var err error
		resp, err = o.llmProvider.ChatWithMessages(ctx, messages, tools)
		if err != nil {
			return "", fmt.Errorf("LLM continuation failed: %w", err)
		}

		// Add assistant response to messages
		messages = append(messages, llm.Message{
			Role:      "assistant",
			Content:   resp.Content,
			ToolCalls: resp.ToolCalls,
		})
	}

	return resp.Content, nil
}

// executeToolCall executes a single tool call and returns the result
func (o *Orchestrator) executeToolCall(ctx context.Context, ag *agent.Agent, toolCall llm.ToolCall) string {
	// Find the tool in the agent's plugins
	for _, pl := range ag.Plugins {
		var toolName string
		if pl.Tool != nil {
			toolName = pl.Tool.Definition().Name
		} else {
			toolName = pl.Definition.Name
		}

		if toolName == toolCall.Name {
			if pl.Tool == nil {
				return fmt.Sprintf("Error: Tool '%s' is not loaded", toolCall.Name)
			}

			// Execute the tool
			toolCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()

			result, err := pl.Tool.Call(toolCtx, toolCall.Arguments)
			if err != nil {
				logger.Error("[Orchestrator] Tool execution failed", logger.Fields{"tool": toolCall.Name, "error": err})
				return fmt.Sprintf("Error executing tool '%s': %v", toolCall.Name, err)
			}

			return result
		}
	}

	return fmt.Sprintf("Error: Tool '%s' not found", toolCall.Name)
}

// formatAgentCapabilities formats agent list with capabilities.
// Note: Currently shows generic capabilities. To show real capabilities:
// - Load agent config from store using o.agentStore.GetAgent(agent)
// - Extract capabilities from agent.Capabilities slice
// - Include agent role (agent.Role) in the output
func (o *Orchestrator) formatAgentCapabilities(agents []string) string {
	result := ""
	for _, agent := range agents {
		result += fmt.Sprintf("- %s: General purpose agent\n", agent)
	}
	return result
}

// publishEvent publishes an event to the event bus
func (o *Orchestrator) publishEvent(eventType string, studioID string, data map[string]interface{}) {
	if o.eventBus == nil {
		return
	}

	event := Event{
		ID:          uuid.New().String(),
		Type:        EventType(eventType),
		WorkspaceID: studioID,
		Timestamp:   time.Now(),
		Source:      "orchestrator",
		Data:        data,
		Metadata:    make(map[string]string),
	}

	o.eventBus.Publish(event)
}
