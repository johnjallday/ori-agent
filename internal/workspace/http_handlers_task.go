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
	Description  string `json:"description"`
	From         string `json:"from"`
	To           string `json:"to"`
	Priority     int    `json:"priority"`
	ParentTaskID string `json:"parent_task_id"`
	SubtaskIndex int    `json:"subtask_index"`
}

// CreateTask handles POST /api/studios/:id/tasks
func (h *HTTPHandler) CreateTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}

	// Extract studio ID from URL path
	path := strings.TrimPrefix(r.URL.Path, "/api/studios/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		orihttp.BadRequest(w, "Invalid URL format")
		return
	}
	studioID := parts[0]

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

	// Get studio
	studio, err := h.store.Get(studioID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Studio not found: %v", err))
		return
	}

	logger.Debug("[DEBUG] CreateTask - Studio: , Agents", logger.Fields{"agent": studioID, "agents": studio.Agents})
	logger.Debug("[DEBUG] CreateTask - Request: From=, To=", logger.Fields{"task_id": req.From, "to": req.To})

	// Create task
	task := Task{
		ID:           uuid.New().String(),
		WorkspaceID:  studioID,
		From:         req.From,
		To:           req.To,
		Description:  req.Description,
		Priority:     req.Priority,
		Context:      make(map[string]interface{}),
		ParentTaskID: req.ParentTaskID,
		SubtaskIndex: req.SubtaskIndex,
		Status:       TaskStatusPending,
		CreatedAt:    time.Now(),
	}

	// Add task to studio
	if err := studio.AddTask(task); err != nil {
		logger.Error("[DEBUG] CreateTask - AddTask failed", logger.Fields{"task_id": err})
		orihttp.InternalError(w, fmt.Sprintf("Failed to add task: %v", err))
		return
	}

	// Save updated studio
	if err := h.store.Save(studio); err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to save studio: %v", err))
		return
	}

	logger.Info("Created task in studio", logger.Fields{"description": req.Description, "task_id": task.ID, "studioID": studioID})

	// Return success
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if encErr := json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Task created successfully",
		"task_id": task.ID,
		"task":    task,
		"studio":  studioID,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// UpdateTask handles PATCH /api/studios/:id/tasks/:task_id
func (h *HTTPHandler) UpdateTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		orihttp.MethodNotAllowed(w)
		return
	}

	// Extract studio ID and task ID from URL path
	// URL format: /api/studios/{studio_id}/tasks/{task_id}
	path := strings.TrimPrefix(r.URL.Path, "/api/studios/")
	parts := strings.Split(path, "/")
	if len(parts) < 3 {
		orihttp.BadRequest(w, "Invalid URL format")
		return
	}
	studioID := parts[0]
	taskID := parts[2]

	// Parse request body

	var req struct {
		Description    *string   `json:"description,omitempty"`
		To             *string   `json:"to,omitempty"`
		From           *string   `json:"from,omitempty"`
		InputTaskIDs   *[]string `json:"input_task_ids,omitempty"`
		AssignedNodeID *string   `json:"assigned_node_id,omitempty"`
		ParentTaskID   *string   `json:"parent_task_id,omitempty"`
		SubtaskIndex   *int      `json:"subtask_index,omitempty"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	// Get studio
	studio, err := h.store.Get(studioID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Studio not found: %v", err))
		return
	}

	// Find and update task
	found := false
	for i, task := range studio.Tasks {
		if task.ID == taskID {
			// Update only provided fields (allowing explicit empty values)
			if req.Description != nil {
				studio.Tasks[i].Description = *req.Description
			}
			if req.To != nil {
				studio.Tasks[i].To = *req.To
			}
			if req.From != nil {
				studio.Tasks[i].From = *req.From
			}
			if req.AssignedNodeID != nil {
				studio.Tasks[i].AssignedNodeID = *req.AssignedNodeID
			}
			if req.InputTaskIDs != nil {
				studio.Tasks[i].InputTaskIDs = *req.InputTaskIDs
			}
			if req.ParentTaskID != nil {
				studio.Tasks[i].ParentTaskID = strings.TrimSpace(*req.ParentTaskID)
				if studio.Tasks[i].ParentTaskID == "" {
					studio.Tasks[i].SubtaskIndex = 0
				}
			}
			if req.SubtaskIndex != nil {
				studio.Tasks[i].SubtaskIndex = *req.SubtaskIndex
			}
			found = true
			break
		}
	}

	if !found {
		orihttp.NotFound(w, "Task not found")
		return
	}

	// Save updated studio
	if err := h.store.Save(studio); err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to save studio: %v", err))
		return
	}

	logger.Debug("Updated task in studio", logger.Fields{"task_id": taskID, "studioID": studioID})

	// Return success
	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Task updated successfully",
		"task_id": taskID,
		"studio":  studioID,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// DeleteTask handles DELETE /api/studios/:id/tasks/:task_id
func (h *HTTPHandler) DeleteTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		orihttp.MethodNotAllowed(w)
		return
	}

	// Extract studio ID and task ID from URL path
	// URL format: /api/studios/{studio_id}/tasks/{task_id}
	path := strings.TrimPrefix(r.URL.Path, "/api/studios/")
	parts := strings.Split(path, "/")
	if len(parts) < 3 {
		orihttp.BadRequest(w, "Invalid URL format")
		return
	}
	studioID := parts[0]
	taskID := parts[2]

	// Get studio
	studio, err := h.store.Get(studioID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Studio not found: %v", err))
		return
	}

	if err := studio.DeleteTask(taskID); err != nil {
		orihttp.NotFound(w, "Task not found")
		return
	}

	if err := h.store.Save(studio); err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to update studio: %v", err))
		return
	}

	logger.Debug("Deleted task from studio", logger.Fields{"workspace_id": taskID, "studioID": studioID})

	// Return success
	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Task deleted successfully",
		"task_id": taskID,
		"studio":  studioID,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// ExecuteTaskManually handles POST /api/studios/:id/tasks/:task_id/execute
func (h *HTTPHandler) ExecuteTaskManually(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}

	// Extract studio ID and task ID from URL path
	// URL format: /api/studios/{studio_id}/tasks/{task_id}/execute
	path := strings.TrimPrefix(r.URL.Path, "/api/studios/")
	parts := strings.Split(path, "/")
	if len(parts) < 4 {
		orihttp.BadRequest(w, "Invalid URL format")
		return
	}
	studioID := parts[0]
	taskID := parts[2]

	// Get studio
	studio, err := h.store.Get(studioID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Studio not found: %v", err))
		return
	}

	// Find the task
	var targetTask *Task
	for i := range studio.Tasks {
		if studio.Tasks[i].ID == taskID {
			targetTask = &studio.Tasks[i]
			break
		}
	}

	if targetTask == nil {
		orihttp.NotFound(w, "Task not found")
		return
	}

	logger.Debug("Manually executing task in studio", logger.Fields{"studioID": studioID, "workspace_id": taskID})

	// Execute task asynchronously
	go func() {
		ctx := r.Context()
		if err := h.orchestrator.ExecuteTask(ctx, studioID, *targetTask); err != nil {
			logger.Error("Failed to execute task", logger.Fields{"task_id": taskID, "err": err})
		}
	}()

	// Return success
	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Task execution started",
		"task_id": taskID,
		"studio":  studioID,
	}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}
