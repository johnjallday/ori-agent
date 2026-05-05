package workspace

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
)

// CreateTaskRequest represents the request to create a task
type CreateTaskRequest struct {
	Description            string            `json:"description"`
	From                   string            `json:"from"`
	To                     string            `json:"to"`
	Priority               int               `json:"priority"`
	ParentTaskID           string            `json:"parent_task_id"`
	SubtaskIndex           int               `json:"subtask_index"`
	OrchestrationMode      string            `json:"orchestration_mode"`
	ResultCombinationMode  string            `json:"result_combination_mode"`
	CombinationInstruction string            `json:"combination_instruction"`
	OutputSchema           *TaskOutputSchema `json:"output_schema"`
	TemplateRef            *TaskTemplateRef  `json:"template_ref"`
}

// CreateTask handles POST /api/workspaces/:id/tasks
func (h *HTTPHandler) CreateTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}

	// Extract workspace ID from URL path
	path := strings.TrimPrefix(r.URL.Path, "/api/workspaces/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		orihttp.BadRequest(w, "Invalid URL format")
		return
	}
	workspaceID := parts[0]

	// Parse request body

	var req CreateTaskRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	// Validate request
	// Note: From and To agents are optional - tasks can be created without connections
	if req.Description == "" {
		orihttp.BadRequest(w, "Task description is required")
		return
	}

	// Get workspace
	workspace, err := h.store.Get(workspaceID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace not found: %v", err))
		return
	}

	logger.Debug("CreateTask - Workspace and agents", logger.Fields{"workspace_id": workspaceID, "agents": workspace.Agents})
	logger.Debug("CreateTask - Request routing", logger.Fields{"from": req.From, "to": req.To})

	// Create task
	task := Task{
		ID:                     uuid.New().String(),
		WorkspaceID:            workspaceID,
		From:                   req.From,
		To:                     req.To,
		Description:            req.Description,
		Priority:               req.Priority,
		Context:                make(map[string]interface{}),
		ParentTaskID:           req.ParentTaskID,
		SubtaskIndex:           req.SubtaskIndex,
		OrchestrationMode:      NormalizeTaskOrchestrationMode(req.OrchestrationMode),
		ResultCombinationMode:  NormalizeTaskResultCombinationMode(req.ResultCombinationMode),
		CombinationInstruction: strings.TrimSpace(req.CombinationInstruction),
		OutputSchema:           NormalizeTaskOutputSchema(req.OutputSchema),
		TemplateRef:            req.TemplateRef,
		Status:                 TaskStatusPending,
		CreatedAt:              time.Now(),
	}

	// Add task to workspace
	if err := workspace.AddTask(task); err != nil {
		logger.Error("[DEBUG] CreateTask - AddTask failed", logger.Fields{"task_id": err})
		orihttp.InternalError(w, fmt.Sprintf("Failed to add task: %v", err))
		return
	}

	// Save updated workspace
	if err := h.store.Save(workspace); err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to save workspace: %v", err))
		return
	}

	logger.Info("Created task in workspace", logger.Fields{"description": req.Description, "task_id": task.ID, "workspaceID": workspaceID})

	// Return success
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if encErr := json.NewEncoder(w).Encode(map[string]interface{}{
		"message":   "Task created successfully",
		"task_id":   task.ID,
		"task":      task,
		"workspace": workspaceID,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// UpdateTask handles PATCH /api/workspaces/:id/tasks/:task_id
func (h *HTTPHandler) UpdateTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		orihttp.MethodNotAllowed(w)
		return
	}

	// Extract workspace ID and task ID from URL path
	// URL format: /api/workspaces/{workspace_id}/tasks/{task_id}
	path := strings.TrimPrefix(r.URL.Path, "/api/workspaces/")
	parts := strings.Split(path, "/")
	if len(parts) < 3 {
		orihttp.BadRequest(w, "Invalid URL format")
		return
	}
	workspaceID := parts[0]
	taskID := parts[2]

	// Parse request body

	var req struct {
		Description            *string           `json:"description,omitempty"`
		Details                *string           `json:"details,omitempty"`
		To                     *string           `json:"to,omitempty"`
		From                   *string           `json:"from,omitempty"`
		InputTaskIDs           *[]string         `json:"input_task_ids,omitempty"`
		AssignedNodeID         *string           `json:"assigned_node_id,omitempty"`
		ParentTaskID           *string           `json:"parent_task_id,omitempty"`
		SubtaskIndex           *int              `json:"subtask_index,omitempty"`
		OrchestrationMode      *string           `json:"orchestration_mode,omitempty"`
		ResultCombinationMode  *string           `json:"result_combination_mode,omitempty"`
		CombinationInstruction *string           `json:"combination_instruction,omitempty"`
		OutputSchema           *TaskOutputSchema `json:"output_schema,omitempty"`
		TemplateRef            *TaskTemplateRef  `json:"template_ref,omitempty"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	// Get workspace
	workspace, err := h.store.Get(workspaceID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace not found: %v", err))
		return
	}

	// Find and update task
	found := false
	for i, task := range workspace.Tasks {
		if task.ID == taskID {
			// Update only provided fields (allowing explicit empty values)
			if req.Description != nil {
				workspace.Tasks[i].Description = *req.Description
			}
			if req.Details != nil {
				workspace.Tasks[i].Details = *req.Details
			}
			if req.To != nil {
				workspace.Tasks[i].To = *req.To
			}
			if req.From != nil {
				workspace.Tasks[i].From = *req.From
			}
			if req.AssignedNodeID != nil {
				workspace.Tasks[i].AssignedNodeID = *req.AssignedNodeID
			}
			if req.InputTaskIDs != nil {
				workspace.Tasks[i].InputTaskIDs = *req.InputTaskIDs
			}
			if req.ParentTaskID != nil {
				workspace.Tasks[i].ParentTaskID = strings.TrimSpace(*req.ParentTaskID)
				if workspace.Tasks[i].ParentTaskID == "" {
					workspace.Tasks[i].SubtaskIndex = 0
				}
			}
			if req.SubtaskIndex != nil {
				workspace.Tasks[i].SubtaskIndex = *req.SubtaskIndex
			}
			if req.OrchestrationMode != nil {
				workspace.Tasks[i].OrchestrationMode = NormalizeTaskOrchestrationMode(*req.OrchestrationMode)
			}
			if req.ResultCombinationMode != nil {
				workspace.Tasks[i].ResultCombinationMode = NormalizeTaskResultCombinationMode(*req.ResultCombinationMode)
			}
			if req.CombinationInstruction != nil {
				workspace.Tasks[i].CombinationInstruction = strings.TrimSpace(*req.CombinationInstruction)
			}
			if req.OutputSchema != nil {
				workspace.Tasks[i].OutputSchema = NormalizeTaskOutputSchema(req.OutputSchema)
			}
			if req.TemplateRef != nil {
				workspace.Tasks[i].TemplateRef = req.TemplateRef
			}
			found = true
			break
		}
	}

	if !found {
		orihttp.NotFound(w, "Task not found")
		return
	}

	// Save updated workspace
	if err := h.store.Save(workspace); err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to save workspace: %v", err))
		return
	}

	logger.Debug("Updated task in workspace", logger.Fields{"task_id": taskID, "workspaceID": workspaceID})

	// Return success
	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]interface{}{
		"message":   "Task updated successfully",
		"task_id":   taskID,
		"workspace": workspaceID,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// DeleteTask handles DELETE /api/workspaces/:id/tasks/:task_id
func (h *HTTPHandler) DeleteTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		orihttp.MethodNotAllowed(w)
		return
	}

	// Extract workspace ID and task ID from URL path
	// URL format: /api/workspaces/{workspace_id}/tasks/{task_id}
	path := strings.TrimPrefix(r.URL.Path, "/api/workspaces/")
	parts := strings.Split(path, "/")
	if len(parts) < 3 {
		orihttp.BadRequest(w, "Invalid URL format")
		return
	}
	workspaceID := parts[0]
	taskID := parts[2]

	// Get workspace
	workspace, err := h.store.Get(workspaceID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace not found: %v", err))
		return
	}

	if err := workspace.DeleteTask(taskID); err != nil {
		orihttp.NotFound(w, "Task not found")
		return
	}

	if err := h.store.Save(workspace); err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to update workspace: %v", err))
		return
	}

	logger.Debug("Deleted task from workspace", logger.Fields{"workspace_id": taskID, "workspaceID": workspaceID})

	// Return success
	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]interface{}{
		"message":   "Task deleted successfully",
		"task_id":   taskID,
		"workspace": workspaceID,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// ExecuteTaskManually handles POST /api/workspaces/:id/tasks/:task_id/execute
func (h *HTTPHandler) ExecuteTaskManually(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}

	// Extract workspace ID and task ID from URL path
	// URL format: /api/workspaces/{workspace_id}/tasks/{task_id}/execute
	path := strings.TrimPrefix(r.URL.Path, "/api/workspaces/")
	parts := strings.Split(path, "/")
	if len(parts) < 4 {
		orihttp.BadRequest(w, "Invalid URL format")
		return
	}
	workspaceID := parts[0]
	taskID := parts[2]

	// Get workspace
	workspace, err := h.store.Get(workspaceID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace not found: %v", err))
		return
	}

	// Find the task
	var targetTask *Task
	for i := range workspace.Tasks {
		if workspace.Tasks[i].ID == taskID {
			targetTask = &workspace.Tasks[i]
			break
		}
	}

	if targetTask == nil {
		orihttp.NotFound(w, "Task not found")
		return
	}

	logger.Debug("Manually executing task in workspace", logger.Fields{"workspaceID": workspaceID, "workspace_id": taskID})

	// Execute task asynchronously
	go func() {
		ctx := r.Context()
		if err := h.orchestrator.ExecuteTask(ctx, workspaceID, *targetTask); err != nil {
			logger.Error("Failed to execute task", logger.Fields{"task_id": taskID, "err": err})
		}
	}()

	// Return success
	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]interface{}{
		"message":   "Task execution started",
		"task_id":   taskID,
		"workspace": workspaceID,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}
