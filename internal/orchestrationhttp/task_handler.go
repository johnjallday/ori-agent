package orchestrationhttp

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/agentcomm"
	"github.com/johnjallday/ori-agent/internal/agentstudio"
)

// TaskHandler manages task and scheduled task operations
type TaskHandler struct {
	workspaceStore agentstudio.Store
	communicator   *agentcomm.Communicator
	taskHandler    agentstudio.TaskHandler
	eventBus       *agentstudio.EventBus
}

// NewTaskHandler creates a new task handler
func NewTaskHandler(workspaceStore agentstudio.Store,
	communicator *agentcomm.Communicator,
	taskHandler agentstudio.TaskHandler,
	eventBus *agentstudio.EventBus) *TaskHandler {
	return &TaskHandler{
		workspaceStore: workspaceStore,
		communicator:   communicator,
		taskHandler:    taskHandler,
		eventBus:       eventBus,
	}
}

// TasksHandler handles task queries
// GET: Get task by ID or list tasks for workspace/agent
// PUT: Update task status
func (th *TaskHandler) TasksHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	switch r.Method {
	case http.MethodGet:
		th.handleGetTasks(w, r)
	case http.MethodPost:
		th.handleCreateTask(w, r)
	case http.MethodPut:
		th.handleUpdateTask(w, r)
	case http.MethodDelete:
		th.handleDeleteTask(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// handleGetTasks retrieves tasks
func (th *TaskHandler) handleGetTasks(w http.ResponseWriter, r *http.Request) {
	taskID := r.URL.Query().Get("id")
	workspaceID := r.URL.Query().Get("studio_id")
	agentName := r.URL.Query().Get("agent")

	if taskID != "" {
		// Get specific task
		task, err := th.communicator.GetTask(taskID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(task)
		return
	}

	if workspaceID != "" {
		// List tasks for workspace
		tasks := th.communicator.ListTasks(workspaceID)
		stats := th.communicator.GetTaskStats(workspaceID)

		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"tasks": tasks,
			"stats": stats,
			"count": len(tasks),
		})
		return
	}

	if agentName != "" {
		// List tasks for agent
		tasks := th.communicator.ListTasksForAgent(agentName)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"tasks": tasks,
			"count": len(tasks),
		})
		return
	}

	http.Error(w, "id, workspace_id, or agent parameter required", http.StatusBadRequest)
}

// handleCreateTask creates a new task in a workspace
func (th *TaskHandler) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WorkspaceID            string   `json:"studio_id"`
		From                   string   `json:"from"`
		To                     string   `json:"to"`
		Description            string   `json:"description"`
		Priority               int      `json:"priority"`
		InputTaskIDs           []string `json:"input_task_ids"`
		ResultCombinationMode  string   `json:"result_combination_mode"`
		CombinationInstruction string   `json:"combination_instruction"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Validate required fields
	if req.WorkspaceID == "" {
		http.Error(w, "workspace_id is required", http.StatusBadRequest)
		return
	}
	if req.From == "" {
		http.Error(w, "from (sender agent) is required", http.StatusBadRequest)
		return
	}
	if req.To == "" {
		http.Error(w, "to (recipient agent) is required", http.StatusBadRequest)
		return
	}
	if req.Description == "" {
		http.Error(w, "description is required", http.StatusBadRequest)
		return
	}

	// Get workspace
	ws, err := th.workspaceStore.Get(req.WorkspaceID)
	if err != nil {
		log.Printf("❌ Error getting workspace %s: %v", req.WorkspaceID, err)
		http.Error(w, "Workspace not found: "+err.Error(), http.StatusNotFound)
		return
	}

	// Create task
	task := agentstudio.Task{
		WorkspaceID:            req.WorkspaceID,
		From:                   req.From,
		To:                     req.To,
		Description:            req.Description,
		Priority:               req.Priority,
		InputTaskIDs:           req.InputTaskIDs,
		ResultCombinationMode:  req.ResultCombinationMode,
		CombinationInstruction: req.CombinationInstruction,
		Status:                 agentstudio.TaskStatusPending,
	}

	// Add task to workspace
	if err := ws.AddTask(task); err != nil {
		log.Printf("❌ Failed to add task to workspace: %v", err)
		http.Error(w, "Failed to add task: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Save workspace
	if err := th.workspaceStore.Save(ws); err != nil {
		log.Printf("❌ Failed to save workspace: %v", err)
		http.Error(w, "Failed to save workspace: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Get the task we just added (it now has an ID)
	// Find the most recently added task with matching properties
	var createdTask *agentstudio.Task
	for i := len(ws.Tasks) - 1; i >= 0; i-- {
		if ws.Tasks[i].Description == req.Description && ws.Tasks[i].From == req.From && ws.Tasks[i].To == req.To {
			createdTask = &ws.Tasks[i]
			break
		}
	}

	if createdTask == nil {
		log.Printf("❌ Could not find created task")
		http.Error(w, "Task created but could not be retrieved", http.StatusInternalServerError)
		return
	}

	if len(req.InputTaskIDs) > 0 {
		log.Printf("✅ Created connected task %s in workspace %s: %s -> %s (receiving input from %d task(s))",
			createdTask.ID, req.WorkspaceID, req.From, req.To, len(req.InputTaskIDs))
	} else {
		log.Printf("✅ Created task %s in workspace %s: %s -> %s", createdTask.ID, req.WorkspaceID, req.From, req.To)
	}

	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"task":    createdTask,
	})
}

func (th *TaskHandler) handleUpdateTask(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TaskID                 string   `json:"task_id"`
		Status                 string   `json:"status"`
		Result                 string   `json:"result"`
		Error                  string   `json:"error"`
		To                     *string  `json:"to"`                      // Optional: reassign task to different agent
		InputTaskIDs           []string `json:"input_task_ids"`          // Optional: update input task connections
		ResultCombinationMode  *string  `json:"result_combination_mode"` // Optional: update combination mode
		CombinationInstruction *string  `json:"combination_instruction"` // Optional: update combination instruction
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Extract task ID from URL path if present (e.g., /api/orchestration/tasks/{id})
	pathParts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/orchestration/tasks/"), "/")
	if len(pathParts) > 0 && pathParts[0] != "" {
		req.TaskID = pathParts[0]
	}

	if req.TaskID == "" {
		http.Error(w, "task_id is required", http.StatusBadRequest)
		return
	}

	// Handle input task connections update
	if req.InputTaskIDs != nil {
		log.Printf("🔗 Updating input connections for task %s", req.TaskID)

		// Get workspace from task
		task, err := th.communicator.GetTask(req.TaskID)
		if err != nil {
			log.Printf("❌ Failed to get task: %v", err)
			http.Error(w, "Task not found: "+err.Error(), http.StatusNotFound)
			return
		}

		ws, err := th.workspaceStore.Get(task.WorkspaceID)
		if err != nil {
			log.Printf("❌ Failed to get workspace: %v", err)
			http.Error(w, "Workspace not found: "+err.Error(), http.StatusNotFound)
			return
		}

		// Update the task's input connections
		taskFound := false
		for i := range ws.Tasks {
			if ws.Tasks[i].ID == req.TaskID {
				ws.Tasks[i].InputTaskIDs = req.InputTaskIDs
				if req.ResultCombinationMode != nil {
					ws.Tasks[i].ResultCombinationMode = *req.ResultCombinationMode
				}
				if req.CombinationInstruction != nil {
					ws.Tasks[i].CombinationInstruction = *req.CombinationInstruction
				}
				taskFound = true
				log.Printf("📝 Updated task %s input connections: %v", req.TaskID, req.InputTaskIDs)
				break
			}
		}

		if !taskFound {
			log.Printf("❌ Task %s not found in workspace %s", req.TaskID, task.WorkspaceID)
			http.Error(w, "Task not found in workspace", http.StatusNotFound)
			return
		}

		// Save workspace
		if err := th.workspaceStore.Save(ws); err != nil {
			log.Printf("❌ Failed to save workspace: %v", err)
			http.Error(w, "Failed to update task: "+err.Error(), http.StatusInternalServerError)
			return
		}

		log.Printf("✅ Updated input connections for task %s", req.TaskID)

		// Publish event
		if th.eventBus != nil {
			th.eventBus.Publish(agentstudio.Event{
				Type:        agentstudio.EventWorkspaceUpdated,
				WorkspaceID: task.WorkspaceID,
				Data: map[string]interface{}{
					"task_id":        req.TaskID,
					"input_task_ids": req.InputTaskIDs,
					"update_type":    "task_connections",
				},
			})
		}

		// Return updated task
		updatedTask, _ := th.communicator.GetTask(req.TaskID)
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(updatedTask)
		return
	}

	// Handle task reassignment (changing "to" field)
	if req.To != nil {
		log.Printf("🔄 Reassigning task %s to %s", req.TaskID, *req.To)

		// Get workspace from task
		task, err := th.communicator.GetTask(req.TaskID)
		if err != nil {
			log.Printf("❌ Failed to get task: %v", err)
			http.Error(w, "Task not found: "+err.Error(), http.StatusNotFound)
			return
		}

		ws, err := th.workspaceStore.Get(task.WorkspaceID)
		if err != nil {
			log.Printf("❌ Failed to get workspace: %v", err)
			http.Error(w, "Workspace not found: "+err.Error(), http.StatusNotFound)
			return
		}

		// Update the task assignment
		taskFound := false
		for i := range ws.Tasks {
			if ws.Tasks[i].ID == req.TaskID {
				ws.Tasks[i].To = *req.To
				taskFound = true
				log.Printf("📝 Updated task in workspace: %s -> %s", req.TaskID, *req.To)
				break
			}
		}

		if !taskFound {
			log.Printf("❌ Task %s not found in workspace %s", req.TaskID, task.WorkspaceID)
			http.Error(w, "Task not found in workspace", http.StatusNotFound)
			return
		}

		// Save workspace
		if err := th.workspaceStore.Save(ws); err != nil {
			log.Printf("❌ Failed to save workspace: %v", err)
			http.Error(w, "Failed to update task: "+err.Error(), http.StatusInternalServerError)
			return
		}

		log.Printf("✅ Reassigned task %s to %s", req.TaskID, *req.To)

		// Publish event
		th.eventBus.Publish(agentstudio.Event{
			Type:        agentstudio.EventTaskAssigned,
			WorkspaceID: task.WorkspaceID,
			Data: map[string]interface{}{
				"task_id": req.TaskID,
				"to":      *req.To,
			},
		})

		// Return updated task
		updatedTask, _ := th.communicator.GetTask(req.TaskID)
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(updatedTask)
		return
	}

	// Handle status update
	if req.Status == "" {
		http.Error(w, "status is required when not reassigning task", http.StatusBadRequest)
		return
	}

	// Update task status
	err := th.communicator.UpdateTaskStatus(
		req.TaskID,
		agentstudio.TaskStatus(req.Status),
		req.Result,
		req.Error,
	)

	if err != nil {
		log.Printf("❌ Failed to update task status: %v", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"task_id": req.TaskID,
		"status":  req.Status,
	})
}

// handleDeleteTask deletes a task
func (th *TaskHandler) handleDeleteTask(w http.ResponseWriter, r *http.Request) {
	taskID := r.URL.Query().Get("id")
	if taskID == "" {
		http.Error(w, "id parameter required", http.StatusBadRequest)
		return
	}

	// Delete task
	err := th.communicator.DeleteTask(taskID)
	if err != nil {
		log.Printf("❌ Failed to delete task: %v", err)
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	log.Printf("✅ Deleted task: %s", taskID)
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Task deleted successfully",
		"task_id": taskID,
	})
}

// TaskResultsHandler retrieves results from one or more tasks
// GET /api/orchestration/task-results?task_ids=id1,id2,id3
func (th *TaskHandler) TaskResultsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// Get task IDs from query parameter
	taskIDsStr := r.URL.Query().Get("task_ids")
	if taskIDsStr == "" {
		http.Error(w, "task_ids parameter required (comma-separated)", http.StatusBadRequest)
		return
	}

	// Split comma-separated task IDs
	taskIDs := strings.Split(taskIDsStr, ",")
	for i := range taskIDs {
		taskIDs[i] = strings.TrimSpace(taskIDs[i])
	}

	// We need to find the workspace that contains these tasks
	// For simplicity, we'll search through all workspaces
	workspaceIDs, err := th.workspaceStore.List()
	if err != nil {
		log.Printf("❌ Error listing workspaces: %v", err)
		http.Error(w, "Failed to retrieve workspaces", http.StatusInternalServerError)
		return
	}

	// Collect results from all workspaces
	allResults := make(map[string]interface{})
	for _, wsID := range workspaceIDs {
		ws, err := th.workspaceStore.Get(wsID)
		if err != nil {
			log.Printf("⚠️ Error getting workspace %s: %v", wsID, err)
			continue
		}

		results := ws.GetTaskResults(taskIDs)
		for taskID, result := range results {
			// Get full task info
			task, err := ws.GetTask(taskID)
			if err == nil {
				allResults[taskID] = map[string]interface{}{
					"task_id":      task.ID,
					"description":  task.Description,
					"status":       task.Status,
					"result":       result,
					"from":         task.From,
					"to":           task.To,
					"completed_at": task.CompletedAt,
				}
			} else {
				allResults[taskID] = map[string]interface{}{
					"task_id": taskID,
					"result":  result,
				}
			}
		}
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"results": allResults,
	})
}

// ExecuteTaskHandler handles manual task execution
func (th *TaskHandler) ExecuteTaskHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		TaskID string `json:"task_id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	if req.TaskID == "" {
		http.Error(w, "task_id is required", http.StatusBadRequest)
		return
	}

	// Find the task across all workspaces
	workspaceIDs, err := th.workspaceStore.List()
	if err != nil {
		log.Printf("❌ Error listing workspaces: %v", err)
		http.Error(w, "Failed to list workspaces: "+err.Error(), http.StatusInternalServerError)
		return
	}

	var foundWorkspace *agentstudio.Workspace
	var foundTask *agentstudio.Task

	for _, wsID := range workspaceIDs {
		ws, err := th.workspaceStore.Get(wsID)
		if err != nil {
			continue
		}

		task, err := ws.GetTask(req.TaskID)
		if err == nil {
			foundWorkspace = ws
			foundTask = task
			break
		}
	}

	if foundTask == nil {
		http.Error(w, fmt.Sprintf("Task %s not found", req.TaskID), http.StatusNotFound)
		return
	}

	// Check if task is in a state that can be executed
	if foundTask.Status == agentstudio.TaskStatusCompleted {
		http.Error(w, "Task already completed", http.StatusBadRequest)
		return
	}

	if foundTask.Status == agentstudio.TaskStatusInProgress {
		http.Error(w, "Task is already in progress", http.StatusBadRequest)
		return
	}

	// Check if task handler is available
	if th.taskHandler == nil {
		log.Printf("❌ Task handler not set")
		http.Error(w, "Task execution not available", http.StatusInternalServerError)
		return
	}

	// Execute the task immediately in a goroutine
	go func() {
		ctx := context.Background()

		// Update task status to in_progress
		foundTask.Status = agentstudio.TaskStatusInProgress
		now := time.Now()
		foundTask.StartedAt = &now

		if err := foundWorkspace.UpdateTask(*foundTask); err != nil {
			log.Printf("❌ Failed to update task status: %v", err)
			return
		}
		if err := th.workspaceStore.Save(foundWorkspace); err != nil {
			log.Printf("❌ Failed to save workspace: %v", err)
			return
		}

		// Publish task started event
		if th.eventBus != nil {
			event := agentstudio.NewTaskEvent(agentstudio.EventTaskStarted, foundWorkspace.ID, foundTask.ID, foundTask.To, map[string]interface{}{
				"description": foundTask.Description,
				"priority":    foundTask.Priority,
				"manual":      true,
			})
			th.eventBus.Publish(event)
		}

		log.Printf("▶️  Manually executing task %s for agent %s: %s", foundTask.ID, foundTask.To, foundTask.Description)

		// Inject input task results into task context if InputTaskIDs are specified
		if len(foundTask.InputTaskIDs) > 0 {
			log.Printf("🔗 Task %s has %d input task IDs: %v", foundTask.ID, len(foundTask.InputTaskIDs), foundTask.InputTaskIDs)
			enrichedContext := foundWorkspace.GetInputContext(foundTask)
			foundTask.Context = enrichedContext

			// Debug: Check what was added to context
			if inputResults, ok := enrichedContext["input_task_results"]; ok {
				resultsMap := inputResults.(map[string]string)
				log.Printf("📥 Injected %d input task results into task %s context", len(resultsMap), foundTask.ID)
				for taskID, result := range resultsMap {
					preview := result
					if len(preview) > 100 {
						preview = preview[:100] + "..."
					}
					log.Printf("   - Task %s result: %s", taskID, preview)
				}
			} else {
				log.Printf("⚠️  Warning: No input results found for task %s despite having InputTaskIDs", foundTask.ID)
			}
		} else {
			log.Printf("ℹ️  Task %s has no input task IDs", foundTask.ID)
		}

		// Execute the task
		result, execErr := th.taskHandler.ExecuteTask(ctx, foundTask.To, *foundTask)

		// Reload workspace (may have changed)
		ws, err := th.workspaceStore.Get(foundWorkspace.ID)
		if err != nil {
			log.Printf("❌ Failed to reload workspace %s: %v", foundWorkspace.ID, err)
			return
		}

		// Find the task in the reloaded workspace
		task, err := ws.GetTask(foundTask.ID)
		if err != nil {
			log.Printf("❌ Task %s not found in workspace after execution", foundTask.ID)
			return
		}

		// Update task with result
		completedAt := time.Now()
		task.CompletedAt = &completedAt

		if execErr != nil {
			log.Printf("❌ Task %s failed: %v", task.ID, execErr)
			task.Status = agentstudio.TaskStatusFailed
			task.Error = execErr.Error()

			// Publish task failed event
			if th.eventBus != nil {
				event := agentstudio.NewTaskEvent(agentstudio.EventTaskFailed, ws.ID, task.ID, task.To, map[string]interface{}{
					"description": task.Description,
					"error":       execErr.Error(),
					"manual":      true,
				})
				th.eventBus.Publish(event)
			}
		} else {
			log.Printf("✅ Task %s completed successfully", task.ID)
			task.Status = agentstudio.TaskStatusCompleted
			task.Result = result

			// Publish task completed event
			if th.eventBus != nil {
				event := agentstudio.NewTaskEvent(agentstudio.EventTaskCompleted, ws.ID, task.ID, task.To, map[string]interface{}{
					"description": task.Description,
					"result":      result,
					"manual":      true,
				})
				th.eventBus.Publish(event)
			}
		}

		// Save updated task
		if err := ws.UpdateTask(*task); err != nil {
			log.Printf("❌ Failed to update task: %v", err)
			return
		}
		if err := th.workspaceStore.Save(ws); err != nil {
			log.Printf("❌ Failed to save workspace: %v", err)
		}

		// Publish workspace updated event
		if th.eventBus != nil {
			event := agentstudio.NewWorkspaceEvent(agentstudio.EventWorkspaceUpdated, ws.ID, "manual-execution", map[string]interface{}{
				"task_id": task.ID,
				"status":  task.Status,
			})
			th.eventBus.Publish(event)
		}
	}()

	log.Printf("✅ Started manual execution of task %s", req.TaskID)

	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Task execution started",
		"task_id": req.TaskID,
	})
}

// ScheduledTasksHandler handles listing and creating scheduled tasks
func (th *TaskHandler) ScheduledTasksHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		th.handleListScheduledTasks(w, r)
	case http.MethodPost:
		th.handleCreateScheduledTask(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleListScheduledTasks lists all scheduled tasks for a workspace
func (th *TaskHandler) handleListScheduledTasks(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.URL.Query().Get("studio_id")
	if workspaceID == "" {
		http.Error(w, "workspace_id is required", http.StatusBadRequest)
		return
	}

	ws, err := th.workspaceStore.Get(workspaceID)
	if err != nil {
		log.Printf("❌ Error getting workspace %s: %v", workspaceID, err)
		http.Error(w, "Workspace not found: "+err.Error(), http.StatusNotFound)
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"scheduled_tasks": ws.ScheduledTasks,
		"count":           len(ws.ScheduledTasks),
	})
}

// handleCreateScheduledTask creates a new scheduled task
func (th *TaskHandler) handleCreateScheduledTask(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WorkspaceID string                     `json:"studio_id"`
		Name        string                     `json:"name"`
		Description string                     `json:"description"`
		From        string                     `json:"from"`
		To          string                     `json:"to"`
		Prompt      string                     `json:"prompt"`
		Priority    int                        `json:"priority"`
		Schedule    agentstudio.ScheduleConfig `json:"schedule"`
		Enabled     bool                       `json:"enabled"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Validate required fields
	if req.WorkspaceID == "" {
		http.Error(w, "workspace_id is required", http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	if req.Prompt == "" {
		http.Error(w, "prompt is required", http.StatusBadRequest)
		return
	}
	if req.From == "" {
		http.Error(w, "from is required", http.StatusBadRequest)
		return
	}
	if req.To == "" {
		http.Error(w, "to is required", http.StatusBadRequest)
		return
	}

	// Get workspace
	ws, err := th.workspaceStore.Get(req.WorkspaceID)
	if err != nil {
		log.Printf("❌ Error getting workspace %s: %v", req.WorkspaceID, err)
		http.Error(w, "Workspace not found: "+err.Error(), http.StatusNotFound)
		return
	}

	// Create scheduled task
	now := time.Now()
	st := agentstudio.ScheduledTask{
		WorkspaceID: req.WorkspaceID,
		Name:        req.Name,
		Description: req.Description,
		From:        req.From,
		To:          req.To,
		Prompt:      req.Prompt,
		Priority:    req.Priority,
		Schedule:    req.Schedule,
		Enabled:     req.Enabled,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	// Calculate initial NextRun if enabled
	if st.Enabled {
		nextRun := calculateInitialNextRun(st.Schedule, now)
		st.NextRun = nextRun
	}

	// Add to workspace
	if err := ws.AddScheduledTask(st); err != nil {
		log.Printf("❌ Failed to add scheduled task: %v", err)
		http.Error(w, "Failed to add scheduled task: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Save workspace
	if err := th.workspaceStore.Save(ws); err != nil {
		log.Printf("❌ Failed to save workspace: %v", err)
		http.Error(w, "Failed to save workspace: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Get the created scheduled task (now has ID)
	var createdTask *agentstudio.ScheduledTask
	for i := len(ws.ScheduledTasks) - 1; i >= 0; i-- {
		if ws.ScheduledTasks[i].Name == req.Name {
			createdTask = &ws.ScheduledTasks[i]
			break
		}
	}

	log.Printf("✅ Created scheduled task %s in workspace %s: %s", createdTask.ID, req.WorkspaceID, req.Name)

	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success":        true,
		"scheduled_task": createdTask,
	})
}

// ScheduledTaskHandler handles get/update/delete for a specific scheduled task
func (th *TaskHandler) ScheduledTaskHandler(w http.ResponseWriter, r *http.Request) {
	// Extract ID from URL path
	// Path format: /api/orchestration/scheduled-tasks/{id} or /api/orchestration/scheduled-tasks/{id}/{action}
	parts := strings.Split(strings.TrimSuffix(r.URL.Path, "/"), "/")

	// Minimum parts: ["", "api", "orchestration", "scheduled-tasks", "{id}"] = 5
	if len(parts) < 5 {
		http.Error(w, "Invalid URL: missing task ID", http.StatusBadRequest)
		return
	}

	id := parts[4] // The ID is always at index 4

	// Handle special actions (e.g., /api/orchestration/scheduled-tasks/{id}/enable)
	if len(parts) >= 6 {
		action := parts[5]

		switch action {
		case "enable":
			th.handleEnableScheduledTask(w, r, id, true)
			return
		case "disable":
			th.handleEnableScheduledTask(w, r, id, false)
			return
		case "trigger":
			th.handleTriggerScheduledTask(w, r, id)
			return
		default:
			http.Error(w, "Unknown action: "+action, http.StatusBadRequest)
			return
		}
	}

	switch r.Method {
	case http.MethodGet:
		th.handleGetScheduledTask(w, r, id)
	case http.MethodPut:
		th.handleUpdateScheduledTask(w, r, id)
	case http.MethodDelete:
		th.handleDeleteScheduledTask(w, r, id)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleGetScheduledTask retrieves a specific scheduled task
func (th *TaskHandler) handleGetScheduledTask(w http.ResponseWriter, r *http.Request, id string) {
	// Find the scheduled task across all workspaces
	workspaceIDs, err := th.workspaceStore.List()
	if err != nil {
		log.Printf("❌ Error listing workspaces: %v", err)
		http.Error(w, "Failed to list workspaces: "+err.Error(), http.StatusInternalServerError)
		return
	}

	for _, wsID := range workspaceIDs {
		ws, err := th.workspaceStore.Get(wsID)
		if err != nil {
			continue
		}

		st, err := ws.GetScheduledTask(id)
		if err == nil {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"scheduled_task": st,
			})
			return
		}
	}

	http.Error(w, fmt.Sprintf("Scheduled task %s not found", id), http.StatusNotFound)
}

// handleUpdateScheduledTask updates a scheduled task
func (th *TaskHandler) handleUpdateScheduledTask(w http.ResponseWriter, r *http.Request, id string) {
	var req struct {
		Name        *string                     `json:"name,omitempty"`
		Description *string                     `json:"description,omitempty"`
		Prompt      *string                     `json:"prompt,omitempty"`
		Priority    *int                        `json:"priority,omitempty"`
		Schedule    *agentstudio.ScheduleConfig `json:"schedule,omitempty"`
		Enabled     *bool                       `json:"enabled,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Find the scheduled task
	workspaceIDs, err := th.workspaceStore.List()
	if err != nil {
		log.Printf("❌ Error listing workspaces: %v", err)
		http.Error(w, "Failed to list workspaces: "+err.Error(), http.StatusInternalServerError)
		return
	}

	for _, wsID := range workspaceIDs {
		ws, err := th.workspaceStore.Get(wsID)
		if err != nil {
			continue
		}

		st, err := ws.GetScheduledTask(id)
		if err != nil {
			continue
		}

		// Update fields if provided
		if req.Name != nil {
			st.Name = *req.Name
		}
		if req.Description != nil {
			st.Description = *req.Description
		}
		if req.Prompt != nil {
			st.Prompt = *req.Prompt
		}
		if req.Priority != nil {
			st.Priority = *req.Priority
		}
		if req.Schedule != nil {
			st.Schedule = *req.Schedule
			// Recalculate NextRun if schedule changed
			if st.Enabled {
				now := time.Now()
				nextRun := calculateInitialNextRun(st.Schedule, now)
				st.NextRun = nextRun
			}
		}
		if req.Enabled != nil {
			wasEnabled := st.Enabled
			st.Enabled = *req.Enabled

			// Calculate NextRun when enabling
			if st.Enabled && !wasEnabled {
				now := time.Now()
				nextRun := calculateInitialNextRun(st.Schedule, now)
				st.NextRun = nextRun
			} else if !st.Enabled && wasEnabled {
				st.NextRun = nil
			}
		}

		st.UpdatedAt = time.Now()

		if err := ws.UpdateScheduledTask(*st); err != nil {
			log.Printf("❌ Failed to update scheduled task: %v", err)
			http.Error(w, "Failed to update scheduled task: "+err.Error(), http.StatusInternalServerError)
			return
		}

		if err := th.workspaceStore.Save(ws); err != nil {
			log.Printf("❌ Failed to save workspace: %v", err)
			http.Error(w, "Failed to save workspace: "+err.Error(), http.StatusInternalServerError)
			return
		}

		log.Printf("✅ Updated scheduled task %s", id)

		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success":        true,
			"scheduled_task": st,
		})
		return
	}

	http.Error(w, fmt.Sprintf("Scheduled task %s not found", id), http.StatusNotFound)
}

// handleDeleteScheduledTask deletes a scheduled task
func (th *TaskHandler) handleDeleteScheduledTask(w http.ResponseWriter, r *http.Request, id string) {
	workspaceIDs, err := th.workspaceStore.List()
	if err != nil {
		log.Printf("❌ Error listing workspaces: %v", err)
		http.Error(w, "Failed to list workspaces: "+err.Error(), http.StatusInternalServerError)
		return
	}

	for _, wsID := range workspaceIDs {
		ws, err := th.workspaceStore.Get(wsID)
		if err != nil {
			continue
		}

		if err := ws.DeleteScheduledTask(id); err == nil {
			if err := th.workspaceStore.Save(ws); err != nil {
				log.Printf("❌ Failed to save workspace: %v", err)
				http.Error(w, "Failed to save workspace: "+err.Error(), http.StatusInternalServerError)
				return
			}

			log.Printf("✅ Deleted scheduled task %s", id)

			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
			})
			return
		}
	}

	http.Error(w, fmt.Sprintf("Scheduled task %s not found", id), http.StatusNotFound)
}

// handleEnableScheduledTask enables or disables a scheduled task
func (th *TaskHandler) handleEnableScheduledTask(w http.ResponseWriter, r *http.Request, id string, enable bool) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	workspaceIDs, err := th.workspaceStore.List()
	if err != nil {
		log.Printf("❌ Error listing workspaces: %v", err)
		http.Error(w, "Failed to list workspaces: "+err.Error(), http.StatusInternalServerError)
		return
	}

	for _, wsID := range workspaceIDs {
		ws, err := th.workspaceStore.Get(wsID)
		if err != nil {
			continue
		}

		st, err := ws.GetScheduledTask(id)
		if err != nil {
			continue
		}

		st.Enabled = enable
		st.UpdatedAt = time.Now()

		// Calculate NextRun when enabling
		if enable {
			now := time.Now()
			nextRun := calculateInitialNextRun(st.Schedule, now)
			st.NextRun = nextRun
		} else {
			st.NextRun = nil
		}

		if err := ws.UpdateScheduledTask(*st); err != nil {
			log.Printf("❌ Failed to update scheduled task: %v", err)
			http.Error(w, "Failed to update scheduled task: "+err.Error(), http.StatusInternalServerError)
			return
		}

		if err := th.workspaceStore.Save(ws); err != nil {
			log.Printf("❌ Failed to save workspace: %v", err)
			http.Error(w, "Failed to save workspace: "+err.Error(), http.StatusInternalServerError)
			return
		}

		action := "disabled"
		if enable {
			action = "enabled"
		}
		// Capitalize first letter manually (strings.Title is deprecated)
		capitalizedAction := action
		if len(action) > 0 {
			capitalizedAction = string(action[0]-32) + action[1:]
		}
		log.Printf("✅ %s scheduled task %s", capitalizedAction, id)

		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success":        true,
			"enabled":        enable,
			"scheduled_task": st,
		})
		return
	}

	http.Error(w, fmt.Sprintf("Scheduled task %s not found", id), http.StatusNotFound)
}

// handleTriggerScheduledTask manually triggers a scheduled task
func (th *TaskHandler) handleTriggerScheduledTask(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	workspaceIDs, err := th.workspaceStore.List()
	if err != nil {
		log.Printf("❌ Error listing workspaces: %v", err)
		http.Error(w, "Failed to list workspaces: "+err.Error(), http.StatusInternalServerError)
		return
	}

	for _, wsID := range workspaceIDs {
		ws, err := th.workspaceStore.Get(wsID)
		if err != nil {
			continue
		}

		st, err := ws.GetScheduledTask(id)
		if err != nil {
			continue
		}

		// Create a task from the scheduled task
		task := agentstudio.Task{
			WorkspaceID: ws.ID,
			From:        st.From,
			To:          st.To,
			Description: st.Prompt,
			Priority:    st.Priority,
			Context:     st.Context,
			Status:      agentstudio.TaskStatusPending,
		}

		if err := ws.AddTask(task); err != nil {
			log.Printf("❌ Failed to create task from scheduled task: %v", err)
			http.Error(w, "Failed to create task: "+err.Error(), http.StatusBadRequest)
			return
		}

		if err := th.workspaceStore.Save(ws); err != nil {
			log.Printf("❌ Failed to save workspace: %v", err)
			http.Error(w, "Failed to save workspace: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// Get the created task ID
		var taskID string
		if len(ws.Tasks) > 0 {
			taskID = ws.Tasks[len(ws.Tasks)-1].ID
		}

		log.Printf("✅ Manually triggered scheduled task %s, created task %s", id, taskID)

		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"task_id": taskID,
		})
		return
	}

	http.Error(w, fmt.Sprintf("Scheduled task %s not found", id), http.StatusNotFound)
}

// calculateInitialNextRun calculates the initial next run time for a schedule
func calculateInitialNextRun(config agentstudio.ScheduleConfig, now time.Time) *time.Time {
	switch config.Type {
	case agentstudio.ScheduleOnce:
		if config.ExecuteAt != nil {
			return config.ExecuteAt
		}
		return nil

	case agentstudio.ScheduleInterval:
		if config.Interval == 0 {
			return nil
		}
		next := now.Add(config.Interval)
		return &next

	case agentstudio.ScheduleDaily:
		if config.TimeOfDay == "" {
			return nil
		}

		var hour, minute int
		if _, err := fmt.Sscanf(config.TimeOfDay, "%d:%d", &hour, &minute); err != nil {
			return nil
		}

		// Calculate next occurrence
		next := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, now.Location())
		if next.Before(now) || next.Equal(now) {
			// If time has passed today, schedule for tomorrow
			next = next.AddDate(0, 0, 1)
		}

		return &next

	case agentstudio.ScheduleWeekly:
		if config.TimeOfDay == "" {
			return nil
		}

		var hour, minute int
		if _, err := fmt.Sscanf(config.TimeOfDay, "%d:%d", &hour, &minute); err != nil {
			return nil
		}

		targetWeekday := time.Weekday(config.DayOfWeek)
		currentWeekday := now.Weekday()

		daysUntil := int(targetWeekday - currentWeekday)
		if daysUntil < 0 {
			daysUntil += 7
		} else if daysUntil == 0 {
			// Same day - check if time has passed
			testTime := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, now.Location())
			if testTime.Before(now) || testTime.Equal(now) {
				daysUntil = 7 // Next week
			}
		}

		next := time.Date(
			now.Year(),
			now.Month(),
			now.Day()+daysUntil,
			hour,
			minute,
			0,
			0,
			now.Location(),
		)

		return &next

	default:
		return nil
	}
}
