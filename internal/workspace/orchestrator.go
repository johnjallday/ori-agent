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

	// delegationLoop, when set, drives adaptive delegation on task failure
	// (opt-in). Nil means the orchestrator records failures as before.
	delegationLoop *DelegationLoop
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

// SetDelegationLoop wires the adaptive delegation loop. When set, ExecuteTask
// asks the coordinator to adapt on a triggering failure before recording it.
func (o *Orchestrator) SetDelegationLoop(loop *DelegationLoop) {
	o.delegationLoop = loop
}

// ExecuteMission starts autonomous execution of a mission
func (o *Orchestrator) ExecuteMission(ctx context.Context, workspaceID string, mission string) error {
	workspace, err := o.workspaceStore.Get(workspaceID)
	if err != nil {
		return fmt.Errorf("failed to get workspace: %w", err)
	}

	logger.Debug("[Orchestrator] Starting mission for workspace", logger.Fields{"workspace_id": workspaceID, "mission": mission})

	// Step 1: Analyze the mission and break it down into tasks. analyzeMission
	// resolves the LLM's dependency hints (1-based indices into the task
	// array) into Task.InputTaskIDs against the freshly minted task UUIDs.
	tasks, err := o.analyzeMission(ctx, mission, workspace.Agents)
	if err != nil {
		return fmt.Errorf("failed to analyze mission: %w", err)
	}

	logger.Info("[Orchestrator] Created tasks from mission", logger.Fields{"task_id": len(tasks)})

	// Step 1.5: Topologically sort so AddTask sees dependencies before
	// dependents (graph validation in AddTask rejects forward references)
	// AND so ExecuteTasksSequentially below honors the dependency order
	// even when the LLM returned tasks in an arbitrary sequence. Cycles
	// (which shouldn't normally come out of the LLM) are reported here
	// rather than discovered later as a deadlock during execution.
	sortedTasks, sortErr := TopoSortTasks(tasks)
	if sortErr != nil {
		return fmt.Errorf("orchestrator: %w", sortErr)
	}
	tasks = sortedTasks

	// Resolve the coordinator once so every mission task records static-plan
	// provenance attributed to it (the mission orchestrator is a coordinator-
	// driven planning path).
	coordinator, _ := workspace.ResolveCoordinator()

	// Step 2: Add tasks to the workspace
	for i := range tasks {
		tasks[i].WorkspaceID = workspaceID
		assignMissionTask(workspace, &tasks[i], coordinator)
		if err := workspace.AddTask(tasks[i]); err != nil {
			logger.Error("[Orchestrator] Warning: failed to add task", logger.Fields{"task_id": tasks[i].ID, "err": err})
		}

		// Publish task creation event
		o.publishEvent("task_created", workspaceID, map[string]any{
			"task_id":     tasks[i].ID,
			"description": tasks[i].Description,
			"assigned_to": tasks[i].To,
			"priority":    tasks[i].Priority,
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

// analyzeMission uses LLM to break down a mission into tasks. The LLM is
// asked to express dependencies as 1-based indices into the task array
// (since it doesn't know task UUIDs at generation time), which we then
// resolve into the corresponding generated task IDs and stash on
// Task.InputTaskIDs so downstream execution can build runtime inputs.
func (o *Orchestrator) analyzeMission(ctx context.Context, mission string, availableAgents []string) ([]Task, error) {
	// Create a system prompt for task breakdown
	systemPrompt := fmt.Sprintf(`You are an intelligent task orchestrator. Your job is to break down a high-level mission into specific tasks and delegate them to available agents.

Available agents and their capabilities:
%s

Analyze the mission and create a list of tasks. For each task:
1. Provide a clear, actionable description
2. Assign it to the most appropriate agent based on their capabilities
3. Set a priority (1-10, higher = more urgent)
4. Identify dependencies as 1-based indices into THIS task array (e.g. [1, 3] means depends on tasks 1 and 3). Use [] if no dependencies. Do not refer to tasks by description or by name; only indices.

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

	// Parse JSON response. Dependencies are read as RawMessage so we can
	// accept either ints (1-based) or numeric strings; LLMs occasionally
	// stringify the indices despite the schema.
	var taskSpecs []struct {
		Description  string            `json:"description"`
		AssignedTo   string            `json:"assigned_to"`
		Priority     int               `json:"priority"`
		Dependencies []json.RawMessage `json:"dependencies"`
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
				Context:     map[string]any{"original_mission": mission},
				Status:      TaskStatusPending,
				CreatedAt:   time.Now(),
			},
		}, nil
	}

	// Generate IDs first so dependency indices can resolve to real UUIDs.
	tasks := make([]Task, len(taskSpecs))
	for i, spec := range taskSpecs {
		tasks[i] = Task{
			ID:          uuid.New().String(),
			From:        "orchestrator",
			To:          spec.AssignedTo,
			Description: spec.Description,
			Priority:    spec.Priority,
			Context: map[string]any{
				"original_mission": mission,
				"task_index":       i,
			},
			Status:    TaskStatusPending,
			CreatedAt: time.Now(),
		}
	}

	// Resolve dependencies. Each dependency entry is interpreted as a
	// 1-based index into taskSpecs; out-of-range or unparseable entries are
	// dropped with a warning rather than failing the whole plan.
	for i, spec := range taskSpecs {
		if len(spec.Dependencies) == 0 {
			continue
		}
		var resolved []string
		for _, raw := range spec.Dependencies {
			depIdx, ok := parseDependencyIndex(raw, len(tasks))
			if !ok {
				logger.Warn("[Orchestrator] dropping unparseable dependency", logger.Fields{
					"task_index": i + 1,
					"raw":        string(raw),
				})
				continue
			}
			if depIdx == i {
				logger.Warn("[Orchestrator] dropping self-dependency", logger.Fields{
					"task_index": i + 1,
				})
				continue
			}
			resolved = append(resolved, tasks[depIdx].ID)
		}
		if len(resolved) > 0 {
			tasks[i].InputTaskIDs = resolved
		}
	}

	return tasks, nil
}

// parseDependencyIndex returns the 0-based index a dependency entry refers
// to, given the LLM emits 1-based indices into the same array. Accepts
// either a JSON number or a JSON string that happens to wrap an integer
// (some LLMs do this). Returns (idx, true) on success and (0, false) on
// any unparseable / out-of-range value.
func parseDependencyIndex(raw json.RawMessage, taskCount int) (int, bool) {
	// Try integer.
	var asInt int
	if err := json.Unmarshal(raw, &asInt); err == nil {
		idx := asInt - 1
		if idx >= 0 && idx < taskCount {
			return idx, true
		}
		return 0, false
	}
	// Try string-of-int.
	var asStr string
	if err := json.Unmarshal(raw, &asStr); err == nil {
		var parsed int
		if _, scanErr := fmt.Sscanf(asStr, "%d", &parsed); scanErr == nil {
			idx := parsed - 1
			if idx >= 0 && idx < taskCount {
				return idx, true
			}
		}
	}
	return 0, false
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
				o.publishEvent("task_failed", workspaceID, map[string]any{
					"task_id": task.ID,
					"error":   err.Error(),
				})
			}
		}
	}

	logger.Info("[Orchestrator] All tasks completed for workspace", logger.Fields{"task_id": workspaceID})
	o.publishEvent("mission_completed", workspaceID, map[string]any{
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

	// Build runtime inputs for this execution. Persisted task.Context stays
	// untouched; see Task.RuntimeInputs for the runtime-only data path.
	if len(task.InputTaskIDs) > 0 {
		logger.Debug("🔗 Task has input task IDs", logger.Fields{"task_id": task.ID, "inputtaskids)": len(task.InputTaskIDs), "inputtaskids": task.InputTaskIDs})
		task.RuntimeInputs = workspace.BuildRuntimeInputs(&task)

		if task.RuntimeInputs != nil && len(task.RuntimeInputs.TaskResults) > 0 {
			logger.Debug("Built runtime inputs for task", logger.Fields{"result": len(task.RuntimeInputs.TaskResults), "id": task.ID})
			for taskID, result := range task.RuntimeInputs.TaskResults {
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
	if err := task.SetStatus(TaskStatusInProgress); err != nil {
		logger.Error("[Orchestrator] Warning: failed to mark local task in_progress", logger.Fields{"task_id": task.ID, "error": err})
		return err
	}
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
			if err := t.SetStatus(TaskStatusInProgress); err != nil {
				return err
			}
			t.StartedAt = &now
			return nil
		}); err != nil {
			return err
		}
		return fresh.AddMessage(message)
	}); updateErr != nil {
		logger.Error("[Orchestrator] Warning: failed to start task", logger.Fields{"task_id": task.ID, "error": updateErr})
	}

	o.publishEvent("task_started", workspaceID, map[string]any{
		"task_id":     task.ID,
		"assigned_to": task.To,
	})

	o.publishEvent("message_sent", workspaceID, map[string]any{
		"from":    message.From,
		"to":      message.To,
		"content": message.Content,
	})

	// Execute task using the wired task handler. Tool loading (MCP + workspace),
	// the multi-turn tool-call loop, and provider/model selection all live in
	// LLMTaskHandler — the orchestrator does not duplicate that work.
	// A missing handler is treated as a task failure (rather than an early
	// return) so the workspace state and lifecycle events stay consistent.
	var (
		result  string
		taskRun TaskRunResult
	)
	if o.taskHandler == nil {
		err = fmt.Errorf("orchestrator: task handler not configured (call SetTaskHandler before ExecuteTask)")
	} else {
		taskRun, err = ExecuteTaskWithRunMetadata(ctx, o.taskHandler, task.To, task)
		result = taskRun.Result
		if taskRun.RunID != "" {
			task.CurrentRunID = taskRun.RunID
		}
	}

	// Adaptive delegation (opt-in): when a loop is wired and the outcome warrants
	// it, let the coordinator adapt before this is recorded as a failure. With no
	// loop configured, behavior is unchanged.
	if o.delegationLoop != nil {
		if trigger := ClassifyDelegationTrigger(task, result, err); trigger.Trigger {
			loopRes, loopErr := o.delegationLoop.Run(ctx, workspaceID, task, trigger)
			switch {
			case loopErr == nil && loopRes.Resolved:
				logger.Info("[Orchestrator] Delegation loop resolved task", logger.Fields{
					"task_id": task.ID, "iterations": loopRes.Iterations, "subtasks": loopRes.SubtaskCount,
				})
				result = loopRes.Result
				err = nil
			case loopErr != nil:
				// A loop block becomes a pause-to-ask when interactive, or a
				// failure when unattended (missions/scheduled never hang).
				if blocked, ok := AsTaskBlockedError(loopErr); ok && shouldPauseForDelegationBlock(task) {
					logger.Info("[Orchestrator] Delegation loop paused task for input", logger.Fields{"task_id": task.ID})
					o.pauseTaskForDelegation(workspace, task, blocked)
					return nil
				}
				logger.Warn("[Orchestrator] Delegation loop did not resolve task", logger.Fields{
					"task_id": task.ID, "error": loopErr.Error(),
				})
				err = loopErr
			}
		}
	}

	completed := time.Now()
	if err != nil {
		// Task failed
		logger.Error("[Orchestrator] Task failed", logger.Fields{"task_id": task.ID, "err": err})
		if statusErr := task.SetStatus(TaskStatusFailed); statusErr != nil {
			logger.Error("[Orchestrator] Warning: failed to mark local task failed", logger.Fields{"task_id": task.ID, "error": statusErr})
		}
		task.CompletedAt = &completed
		task.Error = err.Error()

		if updateErr := MutateTaskAndSave(o.workspaceStore, workspace, task.ID, func(t *Task) error {
			if taskRun.RunID != "" {
				t.CurrentRunID = taskRun.RunID
			}
			if statusErr := t.SetStatus(TaskStatusFailed); statusErr != nil {
				return statusErr
			}
			t.CompletedAt = &completed
			t.Error = err.Error()
			o.recordExecutionTrace(t, workspaceID, completed)
			return nil
		}); updateErr != nil {
			logger.Error("[Orchestrator] Warning: failed to record task failure", logger.Fields{"task_id": task.ID, "error": updateErr})
		}

		o.publishEvent("task_failed", workspaceID, map[string]any{
			"task_id": task.ID,
			"error":   err.Error(),
		})

		return err
	}

	// Mark task as completed
	if statusErr := task.SetStatus(TaskStatusCompleted); statusErr != nil {
		logger.Error("[Orchestrator] Warning: failed to mark local task completed", logger.Fields{"task_id": task.ID, "error": statusErr})
	}
	task.CompletedAt = &completed
	task.Result = result
	ApplyTaskResultMetadata(&task, result)
	if err := MutateTaskAndSave(o.workspaceStore, workspace, task.ID, func(t *Task) error {
		if taskRun.RunID != "" {
			t.CurrentRunID = taskRun.RunID
		}
		if statusErr := t.SetStatus(TaskStatusCompleted); statusErr != nil {
			return statusErr
		}
		t.CompletedAt = &completed
		t.Result = result
		ApplyTaskResultMetadata(t, result)
		o.recordExecutionTrace(t, workspaceID, completed)
		return nil
	}); err != nil {
		logger.Error("[Orchestrator] Warning: failed to record task completion", logger.Fields{"task_id": task.ID, "error": err})
	}

	o.publishEvent("task_completed", workspaceID, map[string]any{
		"task_id": task.ID,
		"result":  task.Result,
	})

	return nil
}

// assignMissionTask stamps static-plan provenance on a mission task, attributing
// it to the workspace coordinator. If the planned assignee is not a workspace
// member (e.g. an LLM-invented name), it falls back to the coordinator when one
// is known so the task stays runnable; otherwise it records provenance without
// changing the assignee. Routing through ApplyTaskAssignment keeps Task.To and
// the provenance fields in lockstep.
func assignMissionTask(ws *Workspace, task *Task, coordinator string) {
	if ws == nil || task == nil {
		return
	}
	assignedBy := coordinator
	if assignedBy == "" {
		assignedBy = "orchestrator"
	}
	a := TaskAssignment{
		AgentName:  task.To,
		Mode:       TaskAssignmentModeStaticPlan,
		AssignedBy: assignedBy,
		Reason:     "mission plan",
	}
	if err := ws.ApplyTaskAssignment(task, a); err == nil {
		return
	}
	if coordinator != "" {
		a.AgentName = coordinator
		a.Reason = "mission plan (reassigned to coordinator; planned assignee not in workspace)"
		if err := ws.ApplyTaskAssignment(task, a); err == nil {
			return
		}
	}
	// Last resort: record provenance without changing the (non-member) assignee.
	task.AssignmentMode = TaskAssignmentModeStaticPlan
	task.AssignedBy = assignedBy
	task.AssignmentReason = "mission plan"
}

// shouldPauseForDelegationBlock reports whether a delegation block should
// pause-to-ask (interactive) instead of failing. Unattended runs (missions and
// scheduled tasks) must never hang waiting for input, so they fail instead.
func shouldPauseForDelegationBlock(task Task) bool {
	if IsMissionTask(task.Context) {
		return false
	}
	if task.ScheduleEnabled {
		return false
	}
	return true
}

// recordExecutionTrace persists task events (tool calls, delegation.*, blocked)
// into the task's execution trace from the event bus history, so the task-detail
// UI can render them after the fact (matches the task executor's behavior).
func (o *Orchestrator) recordExecutionTrace(t *Task, workspaceID string, completed time.Time) {
	if o.eventBus == nil || t == nil {
		return
	}
	startedAt := completed
	if t.StartedAt != nil && !t.StartedAt.IsZero() {
		startedAt = *t.StartedAt
	}
	RecordTaskExecutionTraceFromEventBus(t, o.eventBus, workspaceID, t.ID, startedAt, completed)
}

// pauseTaskForDelegation suspends a task pending user input, reusing the same
// blocked primitives as the task executor: waiting_for_choice + a task.blocked
// event carrying the coordinator's question.
func (o *Orchestrator) pauseTaskForDelegation(ws *Workspace, task Task, blocked *TaskBlockedError) {
	completed := time.Now()
	if mutErr := MutateTaskAndSave(o.workspaceStore, ws, task.ID, func(t *Task) error {
		if err := t.SetStatus(TaskStatusWaitingForChoice); err != nil {
			return err
		}
		t.Error = ""
		t.Result = ""
		t.CompletedAt = nil
		applyExecutorTaskBlockedContext(t, blocked)
		o.recordExecutionTrace(t, ws.ID, completed)
		return nil
	}); mutErr != nil {
		logger.Error("[Orchestrator] failed to pause task for delegation", logger.Fields{
			"task_id": task.ID, "error": mutErr,
		})
		return
	}
	o.publishEvent(string(EventTaskBlocked), ws.ID, map[string]any{
		"task_id":     task.ID,
		"description": task.Description,
		"error":       blocked.Error(),
	})
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
func (o *Orchestrator) publishEvent(eventType string, workspaceID string, data map[string]any) {
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
