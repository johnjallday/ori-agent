package sessionhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/session"
)

// createTestTaskHandler creates a task handler with an in-memory store for testing.
func createTestTaskHandler(t *testing.T) (*TaskHandler, session.HybridStore, func()) {
	t.Helper()

	ctx := context.Background()
	db, err := database.Open(ctx, &database.Config{InMemory: true})
	if err != nil {
		t.Fatalf("Failed to open test database: %v", err)
	}

	store := session.NewHybridStoreWithDB(db, 50)
	handler := NewTaskHandler(store)

	return handler, store, func() {
		_ = store.Close()
	}
}

// createTestWorkspaceAndSession creates a workspace and session for task testing.
// Tasks require a workspace, and sessions must belong to a workspace to create tasks.
func createTestWorkspaceAndSession(t *testing.T, store session.HybridStore, sessionID, workspaceID string) {
	t.Helper()
	ctx := context.Background()

	// Create workspace first
	workspace := &session.Workspace{
		ID:   workspaceID,
		Name: "Test Workspace",
	}
	if err := store.CreateWorkspace(ctx, workspace); err != nil {
		t.Fatalf("Failed to create test workspace: %v", err)
	}

	// Create session in that workspace
	sess := &session.Session{
		ID:        sessionID,
		Title:     "Test Session",
		AgentName: "test-agent",
		FolderID:  workspaceID,
	}
	if err := store.CreateSession(ctx, sess); err != nil {
		t.Fatalf("Failed to create test session: %v", err)
	}
}

// TestTaskHandler_CreateTask tests task creation via POST /api/sessions/{id}/tasks.
func TestTaskHandler_CreateTask(t *testing.T) {
	handler, store, cleanup := createTestTaskHandler(t)
	defer cleanup()

	createTestWorkspaceAndSession(t, store, "session-1", "workspace-1")

	body := `{"description": "Test task"}`
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/session-1/tasks", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.HandleSessionTasks(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("Expected status 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if resp["description"] != "Test task" {
		t.Errorf("Expected description 'Test task', got '%v'", resp["description"])
	}
	if resp["id"] == nil || resp["id"] == "" {
		t.Error("Expected task ID to be set")
	}
	if resp["status"] != "pending" {
		t.Errorf("Expected status 'pending', got '%v'", resp["status"])
	}
}

// TestTaskHandler_CreateTaskMissingDescription tests that description is required.
func TestTaskHandler_CreateTaskMissingDescription(t *testing.T) {
	handler, store, cleanup := createTestTaskHandler(t)
	defer cleanup()

	createTestWorkspaceAndSession(t, store, "session-1", "workspace-1")

	body := `{}`
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/session-1/tasks", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.HandleSessionTasks(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", w.Code)
	}
}

// TestTaskHandler_ListTasks tests listing tasks via GET /api/sessions/{id}/tasks.
func TestTaskHandler_ListTasks(t *testing.T) {
	handler, store, cleanup := createTestTaskHandler(t)
	defer cleanup()

	createTestWorkspaceAndSession(t, store, "session-1", "workspace-1")

	// Create multiple tasks
	for i := 0; i < 3; i++ {
		body := `{"description": "Task"}`
		req := httptest.NewRequest(http.MethodPost, "/api/sessions/session-1/tasks", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.HandleSessionTasks(w, req)
	}

	// List tasks
	listReq := httptest.NewRequest(http.MethodGet, "/api/sessions/session-1/tasks", nil)
	listW := httptest.NewRecorder()
	handler.HandleSessionTasks(listW, listReq)

	if listW.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", listW.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(listW.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	tasks := resp["tasks"].([]interface{})
	if len(tasks) != 3 {
		t.Errorf("Expected 3 tasks, got %d", len(tasks))
	}

	counts := resp["counts"].(map[string]interface{})
	if int(counts["total"].(float64)) != 3 {
		t.Errorf("Expected total 3, got %v", counts["total"])
	}
}

// TestTaskHandler_GetTask tests getting a specific task.
func TestTaskHandler_GetTask(t *testing.T) {
	handler, store, cleanup := createTestTaskHandler(t)
	defer cleanup()

	createTestWorkspaceAndSession(t, store, "session-1", "workspace-1")

	// Create a task
	body := `{"description": "Get me"}`
	createReq := httptest.NewRequest(http.MethodPost, "/api/sessions/session-1/tasks", bytes.NewBufferString(body))
	createReq.Header.Set("Content-Type", "application/json")
	createW := httptest.NewRecorder()
	handler.HandleSessionTasks(createW, createReq)

	var createResp map[string]interface{}
	_ = json.Unmarshal(createW.Body.Bytes(), &createResp)
	taskID := createResp["id"].(string)

	// Get the task
	getReq := httptest.NewRequest(http.MethodGet, "/api/sessions/session-1/tasks/"+taskID, nil)
	getW := httptest.NewRecorder()
	handler.HandleSessionTasks(getW, getReq)

	if getW.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d: %s", getW.Code, getW.Body.String())
	}

	var getResp map[string]interface{}
	_ = json.Unmarshal(getW.Body.Bytes(), &getResp)
	if getResp["description"] != "Get me" {
		t.Errorf("Expected description 'Get me', got '%v'", getResp["description"])
	}
}

// TestTaskHandler_GetTaskNotFound tests 404 for non-existent task.
func TestTaskHandler_GetTaskNotFound(t *testing.T) {
	handler, store, cleanup := createTestTaskHandler(t)
	defer cleanup()

	createTestWorkspaceAndSession(t, store, "session-1", "workspace-1")

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/session-1/tasks/non-existent", nil)
	w := httptest.NewRecorder()
	handler.HandleSessionTasks(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Expected status 404, got %d", w.Code)
	}
}

// TestTaskHandler_UpdateTask tests updating a task via PUT.
func TestTaskHandler_UpdateTask(t *testing.T) {
	handler, store, cleanup := createTestTaskHandler(t)
	defer cleanup()

	createTestWorkspaceAndSession(t, store, "session-1", "workspace-1")

	// Create a task
	body := `{"description": "Original"}`
	createReq := httptest.NewRequest(http.MethodPost, "/api/sessions/session-1/tasks", bytes.NewBufferString(body))
	createReq.Header.Set("Content-Type", "application/json")
	createW := httptest.NewRecorder()
	handler.HandleSessionTasks(createW, createReq)

	var createResp map[string]interface{}
	_ = json.Unmarshal(createW.Body.Bytes(), &createResp)
	taskID := createResp["id"].(string)

	// Update the task
	updateBody := `{"description": "Updated", "status": "in_progress"}`
	updateReq := httptest.NewRequest(http.MethodPut, "/api/sessions/session-1/tasks/"+taskID, bytes.NewBufferString(updateBody))
	updateReq.Header.Set("Content-Type", "application/json")
	updateW := httptest.NewRecorder()
	handler.HandleSessionTasks(updateW, updateReq)

	if updateW.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d: %s", updateW.Code, updateW.Body.String())
	}

	var updateResp map[string]interface{}
	_ = json.Unmarshal(updateW.Body.Bytes(), &updateResp)
	if updateResp["description"] != "Updated" {
		t.Errorf("Expected description 'Updated', got '%v'", updateResp["description"])
	}
	if updateResp["status"] != "in_progress" {
		t.Errorf("Expected status 'in_progress', got '%v'", updateResp["status"])
	}
}

// TestTaskHandler_DeleteTask tests task deletion.
func TestTaskHandler_DeleteTask(t *testing.T) {
	handler, store, cleanup := createTestTaskHandler(t)
	defer cleanup()

	createTestWorkspaceAndSession(t, store, "session-1", "workspace-1")

	// Create a task
	body := `{"description": "To delete"}`
	createReq := httptest.NewRequest(http.MethodPost, "/api/sessions/session-1/tasks", bytes.NewBufferString(body))
	createReq.Header.Set("Content-Type", "application/json")
	createW := httptest.NewRecorder()
	handler.HandleSessionTasks(createW, createReq)

	var createResp map[string]interface{}
	_ = json.Unmarshal(createW.Body.Bytes(), &createResp)
	taskID := createResp["id"].(string)

	// Delete the task
	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/sessions/session-1/tasks/"+taskID, nil)
	deleteW := httptest.NewRecorder()
	handler.HandleSessionTasks(deleteW, deleteReq)

	if deleteW.Code != http.StatusNoContent {
		t.Errorf("Expected status 204, got %d", deleteW.Code)
	}

	// Verify deletion
	getReq := httptest.NewRequest(http.MethodGet, "/api/sessions/session-1/tasks/"+taskID, nil)
	getW := httptest.NewRecorder()
	handler.HandleSessionTasks(getW, getReq)

	if getW.Code != http.StatusNotFound {
		t.Errorf("Expected status 404 after deletion, got %d", getW.Code)
	}
}

// TestTaskHandler_CompleteTask tests completing a task via POST /api/sessions/{id}/tasks/{taskId}/complete.
func TestTaskHandler_CompleteTask(t *testing.T) {
	handler, store, cleanup := createTestTaskHandler(t)
	defer cleanup()

	createTestWorkspaceAndSession(t, store, "session-1", "workspace-1")

	// Create a task
	body := `{"description": "Complete me"}`
	createReq := httptest.NewRequest(http.MethodPost, "/api/sessions/session-1/tasks", bytes.NewBufferString(body))
	createReq.Header.Set("Content-Type", "application/json")
	createW := httptest.NewRecorder()
	handler.HandleSessionTasks(createW, createReq)

	var createResp map[string]interface{}
	_ = json.Unmarshal(createW.Body.Bytes(), &createResp)
	taskID := createResp["id"].(string)

	// Complete the task
	completeReq := httptest.NewRequest(http.MethodPost, "/api/sessions/session-1/tasks/"+taskID+"/complete", nil)
	completeW := httptest.NewRecorder()
	handler.HandleSessionTasks(completeW, completeReq)

	if completeW.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d: %s", completeW.Code, completeW.Body.String())
	}

	var completeResp map[string]interface{}
	_ = json.Unmarshal(completeW.Body.Bytes(), &completeResp)
	if completeResp["status"] != "completed" {
		t.Errorf("Expected status 'completed', got '%v'", completeResp["status"])
	}
	if completeResp["completed_at"] == nil {
		t.Error("Expected completed_at to be set")
	}
}

// TestTaskHandler_WorkspaceTasks tests workspace-level tasks.
func TestTaskHandler_WorkspaceTasks(t *testing.T) {
	handler, store, cleanup := createTestTaskHandler(t)
	defer cleanup()

	// Create a workspace first
	ctx := context.Background()
	workspace := &session.Workspace{
		ID:   "workspace-1",
		Name: "Test Workspace",
	}
	if err := store.CreateWorkspace(ctx, workspace); err != nil {
		t.Fatalf("Failed to create workspace: %v", err)
	}

	// Create a workspace task
	body := `{"description": "Workspace task"}`
	createReq := httptest.NewRequest(http.MethodPost, "/api/workspaces/workspace-1/tasks", bytes.NewBufferString(body))
	createReq.Header.Set("Content-Type", "application/json")
	createW := httptest.NewRecorder()
	handler.HandleWorkspaceTasks(createW, createReq)

	if createW.Code != http.StatusCreated {
		t.Errorf("Expected status 201, got %d: %s", createW.Code, createW.Body.String())
	}

	var createResp map[string]interface{}
	_ = json.Unmarshal(createW.Body.Bytes(), &createResp)
	if createResp["workspace_id"] != "workspace-1" {
		t.Errorf("Expected workspace_id 'workspace-1', got '%v'", createResp["workspace_id"])
	}

	// List workspace tasks
	listReq := httptest.NewRequest(http.MethodGet, "/api/workspaces/workspace-1/tasks", nil)
	listW := httptest.NewRecorder()
	handler.HandleWorkspaceTasks(listW, listReq)

	if listW.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", listW.Code)
	}

	var listResp map[string]interface{}
	_ = json.Unmarshal(listW.Body.Bytes(), &listResp)
	tasks := listResp["tasks"].([]interface{})
	if len(tasks) != 1 {
		t.Errorf("Expected 1 task, got %d", len(tasks))
	}
}

// TestTaskHandler_InvalidPath tests that invalid paths return bad request.
func TestTaskHandler_InvalidPath(t *testing.T) {
	handler, _, cleanup := createTestTaskHandler(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/session-1/invalid", nil)
	w := httptest.NewRecorder()
	handler.HandleSessionTasks(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", w.Code)
	}
}

// TestTaskHandler_MethodNotAllowed tests method not allowed responses.
func TestTaskHandler_MethodNotAllowed(t *testing.T) {
	handler, store, cleanup := createTestTaskHandler(t)
	defer cleanup()

	createTestWorkspaceAndSession(t, store, "session-1", "workspace-1")

	// PATCH on collection should be method not allowed
	req := httptest.NewRequest(http.MethodPatch, "/api/sessions/session-1/tasks", nil)
	w := httptest.NewRecorder()
	handler.HandleSessionTasks(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Expected status 405, got %d", w.Code)
	}
}

// TestTaskHandler_TaskWithPriority tests task creation with priority.
func TestTaskHandler_TaskWithPriority(t *testing.T) {
	handler, store, cleanup := createTestTaskHandler(t)
	defer cleanup()

	createTestWorkspaceAndSession(t, store, "session-1", "workspace-1")

	body := `{"description": "High priority", "priority": 1}`
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/session-1/tasks", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.HandleSessionTasks(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("Expected status 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)

	priority := int(resp["priority"].(float64))
	if priority != 1 {
		t.Errorf("Expected priority 1, got %d", priority)
	}
}

// TestTaskHandler_TaskCounts tests that task counts are returned correctly.
func TestTaskHandler_TaskCounts(t *testing.T) {
	handler, store, cleanup := createTestTaskHandler(t)
	defer cleanup()

	createTestWorkspaceAndSession(t, store, "session-1", "workspace-1")

	// Create tasks with different statuses
	tasks := []struct {
		description string
		complete    bool
	}{
		{"Pending 1", false},
		{"Pending 2", false},
		{"Complete", true},
	}

	for _, task := range tasks {
		body := `{"description": "` + task.description + `"}`
		createReq := httptest.NewRequest(http.MethodPost, "/api/sessions/session-1/tasks", bytes.NewBufferString(body))
		createReq.Header.Set("Content-Type", "application/json")
		createW := httptest.NewRecorder()
		handler.HandleSessionTasks(createW, createReq)

		if task.complete {
			var resp map[string]interface{}
			_ = json.Unmarshal(createW.Body.Bytes(), &resp)
			taskID := resp["id"].(string)

			completeReq := httptest.NewRequest(http.MethodPost, "/api/sessions/session-1/tasks/"+taskID+"/complete", nil)
			completeW := httptest.NewRecorder()
			handler.HandleSessionTasks(completeW, completeReq)
		}
	}

	// List tasks and check counts
	listReq := httptest.NewRequest(http.MethodGet, "/api/sessions/session-1/tasks", nil)
	listW := httptest.NewRecorder()
	handler.HandleSessionTasks(listW, listReq)

	var resp map[string]interface{}
	_ = json.Unmarshal(listW.Body.Bytes(), &resp)

	counts := resp["counts"].(map[string]interface{})
	if int(counts["total"].(float64)) != 3 {
		t.Errorf("Expected total 3, got %v", counts["total"])
	}
	if int(counts["pending"].(float64)) != 2 {
		t.Errorf("Expected pending 2, got %v", counts["pending"])
	}
	if int(counts["completed"].(float64)) != 1 {
		t.Errorf("Expected completed 1, got %v", counts["completed"])
	}
}
