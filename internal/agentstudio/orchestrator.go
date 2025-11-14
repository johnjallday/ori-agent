package agentstudio

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/openai/openai-go/v3"
)

// Orchestrator manages autonomous task delegation and agent coordination
type Orchestrator struct {
	studioStore Store
	llmProvider LLMProvider // For intelligent task breakdown
	eventBus    *EventBus   // For real-time updates
}

// LLMProvider interface for calling AI models
type LLMProvider interface {
	ChatCompletion(ctx context.Context, messages []openai.ChatCompletionMessageParamUnion, tools []openai.ChatCompletionToolUnionParam) (*openai.ChatCompletion, error)
}

// NewOrchestrator creates a new orchestrator
func NewOrchestrator(store Store, llmProvider LLMProvider, eventBus *EventBus) *Orchestrator {
	return &Orchestrator{
		studioStore: store,
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

	log.Printf("[Orchestrator] Starting mission for studio %s: %s", studioID, mission)

	// Step 1: Analyze the mission and break it down into tasks
	tasks, err := o.analyzeMission(ctx, mission, studio.Agents)
	if err != nil {
		return fmt.Errorf("failed to analyze mission: %w", err)
	}

	log.Printf("[Orchestrator] Created %d tasks from mission", len(tasks))

	// Step 2: Add tasks to the studio
	for _, task := range tasks {
		task.WorkspaceID = studioID
		if err := studio.AddTask(task); err != nil {
			log.Printf("[Orchestrator] Warning: failed to add task %s: %v", task.ID, err)
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
		log.Printf("[Orchestrator] Warning: failed to save studio: %v", err)
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
		log.Printf("[Orchestrator] Warning: Failed to parse LLM response as JSON: %v. Content: %s", err, content)
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
	log.Printf("[Orchestrator] Starting sequential task execution for studio %s", studioID)

	for _, task := range tasks {
		select {
		case <-ctx.Done():
			log.Printf("[Orchestrator] Context cancelled, stopping task execution")
			return
		default:
			// Execute the task
			if err := o.ExecuteTask(ctx, studioID, task); err != nil {
				log.Printf("[Orchestrator] Task %s failed: %v", task.ID, err)
				o.publishEvent("task_failed", studioID, map[string]interface{}{
					"task_id": task.ID,
					"error":   err.Error(),
				})
			}
		}
	}

	log.Printf("[Orchestrator] All tasks completed for studio %s", studioID)
	o.publishEvent("mission_completed", studioID, map[string]interface{}{
		"total_tasks": len(tasks),
	})
}

// ExecuteTask executes a single task by delegating to an agent
func (o *Orchestrator) ExecuteTask(ctx context.Context, studioID string, task Task) error {
	log.Printf("[Orchestrator] Executing task %s: %s (assigned to: %s)", task.ID, task.Description, task.To)

	studio, err := o.studioStore.Get(studioID)
	if err != nil {
		return fmt.Errorf("failed to get studio: %w", err)
	}

	// Inject input task results into task context if InputTaskIDs are specified
	if len(task.InputTaskIDs) > 0 {
		log.Printf("🔗 Task %s has %d input task IDs: %v", task.ID, len(task.InputTaskIDs), task.InputTaskIDs)
		enrichedContext := studio.GetInputContext(&task)
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
	now := time.Now()
	task.Status = TaskStatusInProgress
	task.StartedAt = &now
	if err := studio.UpdateTask(task); err != nil {
		log.Printf("[Orchestrator] Warning: failed to update task: %v", err)
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
		log.Printf("[Orchestrator] Warning: failed to save studio: %v", err)
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
		log.Printf("[Orchestrator] Task %s failed: %v", task.ID, err)
		task.Status = TaskStatusFailed
		task.CompletedAt = &completed
		task.Error = err.Error()

		if updateErr := studio.UpdateTask(task); updateErr != nil {
			log.Printf("[Orchestrator] Warning: failed to update task: %v", updateErr)
		}

		if saveErr := o.studioStore.Save(studio); saveErr != nil {
			log.Printf("[Orchestrator] Warning: failed to save studio: %v", saveErr)
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
		log.Printf("[Orchestrator] Warning: failed to update task: %v", err)
	}

	// Save final studio state
	if err := o.studioStore.Save(studio); err != nil {
		log.Printf("[Orchestrator] Warning: failed to save studio: %v", err)
	}

	o.publishEvent("task_completed", studioID, map[string]interface{}{
		"task_id": task.ID,
		"result":  task.Result,
	})

	return nil
}

// executeTaskWithLLM executes a task using the LLM provider
func (o *Orchestrator) executeTaskWithLLM(ctx context.Context, task Task) (string, error) {
	if o.llmProvider == nil {
		return "", fmt.Errorf("LLM provider not configured")
	}

	log.Printf("[Orchestrator] Executing task %s with LLM: %s", task.ID, task.Description)

	// Create system message with agent role
	systemMsg := openai.SystemMessage(fmt.Sprintf(
		"You are %s, an AI agent in a multi-agent workspace. You have been assigned a task. "+
			"Please complete the task to the best of your ability and provide a clear result.",
		task.To,
	))

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

	taskPrompt += "Please complete this task to the best of your ability. Provide a clear, concise response with your findings or results."

	userMsg := openai.UserMessage(taskPrompt)

	messages := []openai.ChatCompletionMessageParamUnion{
		systemMsg,
		userMsg,
	}

	// Call LLM with timeout
	llmCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	resp, err := o.llmProvider.ChatCompletion(llmCtx, messages, nil)
	if err != nil {
		return "", fmt.Errorf("LLM call failed: %w", err)
	}

	// Extract response content
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("no response from LLM")
	}

	result := resp.Choices[0].Message.Content
	log.Printf("[Orchestrator] Task %s completed with result: %s", task.ID, result)

	return result, nil
}

// formatAgentCapabilities formats agent list with capabilities
func (o *Orchestrator) formatAgentCapabilities(agents []string) string {
	// TODO: Get actual agent capabilities from agent registry
	// For now, return basic format
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
