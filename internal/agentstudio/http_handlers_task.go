package agentstudio

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
	Description string `json:"description"`
	From        string `json:"from"`
	To          string `json:"to"`
	Priority    int    `json:"priority"`
}

// CreateTask handles POST /api/studios/:id/tasks
func (h *HTTPHandler) CreateTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.
				// Extract studio ID from URL path
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/studios/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		if err := orihttp.RespondBadRequest(w, "Invalid URL format"); err != nil {
			logger.Error("Failed to write response",
				// Parse request body
				logger.Fields{"error": err})
		}
		return
	}
	studioID := parts[0]

	var req CreateTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if err := orihttp.RespondBadRequest(w, fmt.Sprintf("Invalid request body: %v", err)); err != nil {
			logger.
				// Validate request
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	if req.Description == "" {
		if err := orihttp.RespondBadRequest(w, "Task description is required"); err != nil {
			logger.
				// Note: From and To agents are optional - tasks can be created without connections
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	// Get studio
	studio, err := h.store.Get(studioID)
	if err != nil {
		if err := orihttp.RespondNotFound(w, fmt.Sprintf("Studio not found: %v", err)); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	logger.Debug("[DEBUG] CreateTask - Studio: , Agents", logger.Fields{"agent": studioID, "agents": studio.Agents})
	logger.Debug("[DEBUG] CreateTask - Request: From=, To=", logger.Fields{"task_id": req.From, "to": req.To})

	// Create task
	task := Task{
		ID:          uuid.New().String(),
		WorkspaceID: studioID,
		From:        req.From,
		To:          req.To,
		Description: req.Description,
		Priority:    req.Priority,
		Context:     make(map[string]interface{}),
		Status:      TaskStatusPending,
		CreatedAt:   time.Now(),
	}

	// Add task to studio
	if err := studio.AddTask(task); err != nil {
		logger.Error("[DEBUG] CreateTask - AddTask failed", logger.Fields{"task_id": err})
		if err := orihttp.RespondInternalError(w, fmt.Sprintf("Failed to add task: %v", err)); err != nil {
			logger.
				// Save updated studio
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	if err := h.store.Save(studio); err != nil {
		if err := orihttp.RespondInternalError(w, fmt.Sprintf("Failed to save studio: %v", err)); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	logger.Info("Created task in studio", logger.Fields{"description": req.Description, "task_id": task.ID, "studioID": studioID})

	// Return success
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Task created successfully",
		"task_id": task.ID,
		"task":    task,
		"studio":  studioID,
	}); err != nil {
		logger.Error("Failed to encode response", logger.Fields{"response": err})
	}
}

// UpdateTask handles PATCH /api/studios/:id/tasks/:task_id
func (h *HTTPHandler) UpdateTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.
				// Extract studio ID and task ID from URL path
				// URL format: /api/studios/{studio_id}/tasks/{task_id}
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/studios/")
	parts := strings.Split(path, "/")
	if len(parts) < 3 {
		if err := orihttp.RespondBadRequest(w, "Invalid URL format"); err != nil {
			logger.Error("Failed to write response", logger.Fields{
				// Parse request body
				"error": err})
		}
		return
	}
	studioID := parts[0]
	taskID := parts[2]

	var req struct {
		Description    *string   `json:"description,omitempty"`
		To             *string   `json:"to,omitempty"`
		From           *string   `json:"from,omitempty"`
		InputTaskIDs   *[]string `json:"input_task_ids,omitempty"`
		AssignedNodeID *string   `json:"assigned_node_id,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if err := orihttp.RespondBadRequest(w, fmt.Sprintf("Invalid request body: %v", err)); err != nil {
			logger.
				// Get studio
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	studio, err := h.store.Get(studioID)
	if err != nil {
		if err := orihttp.RespondNotFound(w, fmt.Sprintf("Studio not found: %v", err)); err != nil {
			logger.
				// Find and update task
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

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
			found = true
			break
		}
	}

	if !found {
		if err := orihttp.RespondNotFound(w, "Task not found"); err != nil {
			logger.
				// Save updated studio
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	if err := h.store.Save(studio); err != nil {
		if err := orihttp.RespondInternalError(w, fmt.Sprintf("Failed to save studio: %v", err)); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	logger.Debug("Updated task in studio", logger.Fields{"task_id": taskID, "studioID": studioID})

	// Return success
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Task updated successfully",
		"task_id": taskID,
		"studio":  studioID,
	}); err != nil {
		logger.Error("Failed to encode response", logger.Fields{"response": err})
	}
}

// DeleteTask handles DELETE /api/studios/:id/tasks/:task_id
func (h *HTTPHandler) DeleteTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.
				// Extract studio ID and task ID from URL path
				// URL format: /api/studios/{studio_id}/tasks/{task_id}
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/studios/")
	parts := strings.Split(path, "/")
	if len(parts) < 3 {
		if err := orihttp.RespondBadRequest(w, "Invalid URL format"); err != nil {
			logger.Error("Failed to write response", logger.Fields{
				// Get studio
				"error": err})
		}
		return
	}
	studioID := parts[0]
	taskID := parts[2]

	studio, err := h.store.Get(studioID)
	if err != nil {
		if err := orihttp.RespondNotFound(w, fmt.Sprintf("Studio not found: %v", err)); err != nil {
			logger.
				// Find and remove task
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	found := false
	newTasks := make([]Task, 0)
	for _, task := range studio.Tasks {
		if task.ID != taskID {
			newTasks = append(newTasks, task)
		} else {
			found = true
		}
	}

	if !found {
		if err := orihttp.RespondNotFound(w, "Task not found"); err != nil {
			logger.Error("Failed to write response",
				// Save updated studio
				logger.Fields{"error": err})
		}
		return
	}

	studio.Tasks = newTasks

	if err := h.store.Save(studio); err != nil {
		if err := orihttp.RespondInternalError(w, fmt.Sprintf("Failed to update studio: %v", err)); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	logger.Debug("Deleted task from studio", logger.Fields{"workspace_id": taskID, "studioID": studioID})

	// Return success
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Task deleted successfully",
		"task_id": taskID,
		"studio":  studioID,
	}); err != nil {
		logger.Error("Failed to encode response", logger.Fields{"response": err})
	}
}

// ExecuteTaskManually handles POST /api/studios/:id/tasks/:task_id/execute
func (h *HTTPHandler) ExecuteTaskManually(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.
				// Extract studio ID and task ID from URL path
				// URL format: /api/studios/{studio_id}/tasks/{task_id}/execute
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/studios/")
	parts := strings.Split(path, "/")
	if len(parts) < 4 {
		if err := orihttp.RespondBadRequest(w, "Invalid URL format"); err != nil {
			logger.Error("Failed to write response", logger.Fields{
				// Get studio
				"error": err})
		}
		return
	}
	studioID := parts[0]
	taskID := parts[2]

	studio, err := h.store.Get(studioID)
	if err != nil {
		if err := orihttp.RespondNotFound(w, fmt.Sprintf("Studio not found: %v", err)); err != nil {
			logger.
				// Find the task
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	var targetTask *Task
	for i := range studio.Tasks {
		if studio.Tasks[i].ID == taskID {
			targetTask = &studio.Tasks[i]
			break
		}
	}

	if targetTask == nil {
		if err := orihttp.RespondNotFound(w, "Task not found"); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
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
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Task execution started",
		"task_id": taskID,
		"studio":  studioID,
	}); err != nil {
		logger.Error("Failed to encode response", logger.Fields{"response": err})
	}
}
