package sessionhttp

import (
	"net/http"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/session"
)

// TaskHandler handles task-related HTTP requests.
type TaskHandler struct {
	taskStore *session.SQLiteTaskStore
}

// NewTaskHandler creates a new task handler using the database from the hybrid store.
func NewTaskHandler(store session.HybridStore) *TaskHandler {
	return &TaskHandler{
		taskStore: session.NewSQLiteTaskStore(store.DB()),
	}
}

// HandleSessionTasks routes requests to /api/sessions/{id}/tasks.
func (h *TaskHandler) HandleSessionTasks(w http.ResponseWriter, r *http.Request) {
	// Extract session ID and task ID from path
	// Path format: /api/sessions/{sessionID}/tasks or /api/sessions/{sessionID}/tasks/{taskID}
	path := r.URL.Path
	path = strings.TrimPrefix(path, "/api/sessions/")

	parts := strings.Split(path, "/")
	if len(parts) < 2 || parts[1] != "tasks" {
		_ = orihttp.RespondBadRequest(w, "invalid path")
		return
	}

	sessionID := parts[0]
	if sessionID == "" {
		_ = orihttp.RespondBadRequest(w, "session_id is required")
		return
	}

	// Check if there's a task ID
	var taskID string
	if len(parts) >= 3 && parts[2] != "" {
		taskID = parts[2]

		// Check for /complete endpoint
		if len(parts) >= 4 && parts[3] == "complete" {
			h.completeTask(w, r, taskID)
			return
		}

		// Handle specific task
		h.handleTask(w, r, taskID)
		return
	}

	// Handle collection-level requests
	switch r.Method {
	case http.MethodGet:
		h.listTasks(w, r, sessionID)
	case http.MethodPost:
		h.createTask(w, r, sessionID, "")
	default:
		_ = orihttp.RespondMethodNotAllowed(w)
	}
}

// HandleWorkspaceTasks routes requests to /api/workspaces/{id}/tasks.
func (h *TaskHandler) HandleWorkspaceTasks(w http.ResponseWriter, r *http.Request) {
	// Extract workspace ID from path
	path := r.URL.Path
	path = strings.TrimPrefix(path, "/api/workspaces/")

	parts := strings.Split(path, "/")
	if len(parts) < 2 || parts[1] != "tasks" {
		_ = orihttp.RespondBadRequest(w, "invalid path")
		return
	}

	workspaceID := parts[0]
	if workspaceID == "" {
		_ = orihttp.RespondBadRequest(w, "workspace_id is required")
		return
	}

	// Handle collection-level requests
	switch r.Method {
	case http.MethodGet:
		h.listWorkspaceTasks(w, r, workspaceID)
	case http.MethodPost:
		h.createTask(w, r, "", workspaceID)
	default:
		_ = orihttp.RespondMethodNotAllowed(w)
	}
}

// handleTask handles requests for a specific task.
func (h *TaskHandler) handleTask(w http.ResponseWriter, r *http.Request, taskID string) {
	switch r.Method {
	case http.MethodGet:
		h.getTask(w, r, taskID)
	case http.MethodPut, http.MethodPatch:
		h.updateTask(w, r, taskID)
	case http.MethodDelete:
		h.deleteTask(w, r, taskID)
	default:
		_ = orihttp.RespondMethodNotAllowed(w)
	}
}

// listTasks handles GET /api/sessions/{id}/tasks.
func (h *TaskHandler) listTasks(w http.ResponseWriter, r *http.Request, sessionID string) {
	ctx := r.Context()

	tasks, err := h.taskStore.ListTasksBySession(ctx, sessionID)
	if err != nil {
		logger.Error("Failed to list tasks", logger.Fields{"session_id": sessionID, "error": err})
		_ = orihttp.RespondInternalError(w, "failed to list tasks")
		return
	}

	// Get task counts
	counts, err := h.taskStore.GetTaskCounts(ctx, sessionID)
	if err != nil {
		logger.Warn("Failed to get task counts", logger.Fields{"session_id": sessionID, "error": err})
		counts = &session.TaskCounts{}
	}

	_ = orihttp.RespondJSON(w, http.StatusOK, map[string]interface{}{
		"tasks":  tasks,
		"counts": counts,
	})
}

// listWorkspaceTasks handles GET /api/workspaces/{id}/tasks.
func (h *TaskHandler) listWorkspaceTasks(w http.ResponseWriter, r *http.Request, workspaceID string) {
	ctx := r.Context()

	tasks, err := h.taskStore.ListTasksByWorkspace(ctx, workspaceID)
	if err != nil {
		logger.Error("Failed to list workspace tasks", logger.Fields{"workspace_id": workspaceID, "error": err})
		_ = orihttp.RespondInternalError(w, "failed to list workspace tasks")
		return
	}

	// Get task counts
	counts, err := h.taskStore.GetWorkspaceTaskCounts(ctx, workspaceID)
	if err != nil {
		logger.Warn("Failed to get workspace task counts", logger.Fields{"workspace_id": workspaceID, "error": err})
		counts = &session.TaskCounts{}
	}

	_ = orihttp.RespondJSON(w, http.StatusOK, map[string]interface{}{
		"tasks":  tasks,
		"counts": counts,
	})
}

// createTask handles POST /api/sessions/{id}/tasks or POST /api/workspaces/{id}/tasks.
// If sessionID is provided, looks up the session's workspace.
// If workspaceID is provided directly, uses that.
func (h *TaskHandler) createTask(w http.ResponseWriter, r *http.Request, sessionID, workspaceID string) {
	var req struct {
		Description string `json:"description"`
		Details     string `json:"details,omitempty"`
		Priority    int    `json:"priority,omitempty"`
	}

	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	if req.Description == "" {
		_ = orihttp.RespondBadRequest(w, "description is required")
		return
	}

	ctx := r.Context()

	// If creating via session endpoint, look up the session's workspace
	if workspaceID == "" && sessionID != "" {
		wsID, err := h.taskStore.GetSessionWorkspace(ctx, sessionID)
		if err != nil {
			logger.Error("Failed to get session workspace", logger.Fields{"session_id": sessionID, "error": err})
			_ = orihttp.RespondBadRequest(w, "session must belong to a workspace to create tasks")
			return
		}
		if wsID == "" {
			_ = orihttp.RespondBadRequest(w, "session must belong to a workspace to create tasks")
			return
		}
		workspaceID = wsID
	}

	if workspaceID == "" {
		_ = orihttp.RespondBadRequest(w, "workspace_id is required")
		return
	}

	task := &session.SessionTask{
		WorkspaceID: workspaceID,
		Description: req.Description,
		Details:     req.Details,
		Priority:    req.Priority,
		Status:      session.TaskStatusPending,
	}

	if err := h.taskStore.CreateTask(ctx, task); err != nil {
		logger.Error("Failed to create task", logger.Fields{"error": err})
		_ = orihttp.RespondInternalError(w, "failed to create task")
		return
	}

	logger.Info("Task created", logger.Fields{"task_id": task.ID, "workspace_id": workspaceID})
	_ = orihttp.RespondJSON(w, http.StatusCreated, task)
}

// getTask handles GET /api/sessions/{id}/tasks/{taskId}.
func (h *TaskHandler) getTask(w http.ResponseWriter, r *http.Request, taskID string) {
	ctx := r.Context()

	task, err := h.taskStore.GetTask(ctx, taskID)
	if err != nil {
		logger.Error("Failed to get task", logger.Fields{"task_id": taskID, "error": err})
		if strings.Contains(err.Error(), "not found") {
			_ = orihttp.RespondNotFound(w, "task not found")
		} else {
			_ = orihttp.RespondInternalError(w, "failed to get task")
		}
		return
	}

	_ = orihttp.RespondJSON(w, http.StatusOK, task)
}

// updateTask handles PUT/PATCH /api/sessions/{id}/tasks/{taskId}.
func (h *TaskHandler) updateTask(w http.ResponseWriter, r *http.Request, taskID string) {
	var req struct {
		Description string             `json:"description,omitempty"`
		Details     *string            `json:"details,omitempty"`
		Status      session.TaskStatus `json:"status,omitempty"`
		Priority    int                `json:"priority,omitempty"`
	}

	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	ctx := r.Context()

	// Get existing task
	task, err := h.taskStore.GetTask(ctx, taskID)
	if err != nil {
		logger.Error("Failed to get task for update", logger.Fields{"task_id": taskID, "error": err})
		if strings.Contains(err.Error(), "not found") {
			_ = orihttp.RespondNotFound(w, "task not found")
		} else {
			_ = orihttp.RespondInternalError(w, "failed to get task")
		}
		return
	}

	// Update fields if provided
	if req.Description != "" {
		task.Description = req.Description
	}
	if req.Details != nil {
		task.Details = *req.Details
	}
	if req.Status != "" {
		task.Status = req.Status
	}
	if req.Priority > 0 {
		task.Priority = req.Priority
	}

	if err := h.taskStore.UpdateTask(ctx, task); err != nil {
		logger.Error("Failed to update task", logger.Fields{"task_id": taskID, "error": err})
		_ = orihttp.RespondInternalError(w, "failed to update task")
		return
	}

	logger.Info("Task updated", logger.Fields{"task_id": taskID})
	_ = orihttp.RespondJSON(w, http.StatusOK, task)
}

// deleteTask handles DELETE /api/sessions/{id}/tasks/{taskId}.
func (h *TaskHandler) deleteTask(w http.ResponseWriter, r *http.Request, taskID string) {
	ctx := r.Context()

	if err := h.taskStore.DeleteTask(ctx, taskID); err != nil {
		logger.Error("Failed to delete task", logger.Fields{"task_id": taskID, "error": err})
		if strings.Contains(err.Error(), "not found") {
			_ = orihttp.RespondNotFound(w, "task not found")
		} else {
			_ = orihttp.RespondInternalError(w, "failed to delete task")
		}
		return
	}

	logger.Info("Task deleted", logger.Fields{"task_id": taskID})
	orihttp.RespondNoContent(w)
}

// completeTask handles POST /api/sessions/{id}/tasks/{taskId}/complete.
func (h *TaskHandler) completeTask(w http.ResponseWriter, r *http.Request, taskID string) {
	if r.Method != http.MethodPost {
		_ = orihttp.RespondMethodNotAllowed(w)
		return
	}

	ctx := r.Context()

	if err := h.taskStore.CompleteTask(ctx, taskID); err != nil {
		logger.Error("Failed to complete task", logger.Fields{"task_id": taskID, "error": err})
		if strings.Contains(err.Error(), "not found") {
			_ = orihttp.RespondNotFound(w, "task not found")
		} else {
			_ = orihttp.RespondInternalError(w, "failed to complete task")
		}
		return
	}

	// Return the updated task
	task, err := h.taskStore.GetTask(ctx, taskID)
	if err != nil {
		logger.Warn("Task completed but failed to retrieve", logger.Fields{"task_id": taskID, "error": err})
		_ = orihttp.RespondJSON(w, http.StatusOK, map[string]string{"status": "completed"})
		return
	}

	logger.Info("Task completed", logger.Fields{"task_id": taskID})
	_ = orihttp.RespondJSON(w, http.StatusOK, task)
}

// GetTaskCounts handles GET /api/sessions/{id}/tasks/counts.
func (h *TaskHandler) GetTaskCounts(w http.ResponseWriter, r *http.Request, sessionID string) {
	ctx := r.Context()

	counts, err := h.taskStore.GetTaskCounts(ctx, sessionID)
	if err != nil {
		logger.Error("Failed to get task counts", logger.Fields{"session_id": sessionID, "error": err})
		_ = orihttp.RespondInternalError(w, "failed to get task counts")
		return
	}

	_ = orihttp.RespondJSON(w, http.StatusOK, counts)
}
