package agentstudio

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/openai/openai-go/v2"
)

// Orchestrator manages autonomous task delegation and agent coordination
type Orchestrator struct {
	studioStore  Store
	llmProvider  LLMProvider // For intelligent task breakdown
	eventBus     *EventBus   // For real-time updates
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
	go o.executeTasksSequentially(ctx, studioID, tasks)

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
func (o *Orchestrator) executeTasksSequentially(ctx context.Context, studioID string, tasks []Task) {
	log.Printf("[Orchestrator] Starting sequential task execution for studio %s", studioID)

	for _, task := range tasks {
		select {
		case <-ctx.Done():
			log.Printf("[Orchestrator] Context cancelled, stopping task execution")
			return
		default:
			// Execute the task
			if err := o.executeTask(ctx, studioID, task); err != nil {
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

// executeTask executes a single task by delegating to an agent
func (o *Orchestrator) executeTask(ctx context.Context, studioID string, task Task) error {
	log.Printf("[Orchestrator] Executing task %s: %s (assigned to: %s)", task.ID, task.Description, task.To)

	studio, err := o.studioStore.Get(studioID)
	if err != nil {
		return fmt.Errorf("failed to get studio: %w", err)
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

	// For now, simulate task execution
	// TODO: Integrate with actual agent execution system
	time.Sleep(2 * time.Second)

	// Mark task as completed
	completed := time.Now()
	task.Status = TaskStatusCompleted
	task.CompletedAt = &completed
	task.Result = fmt.Sprintf("Task completed by %s", task.To)
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
