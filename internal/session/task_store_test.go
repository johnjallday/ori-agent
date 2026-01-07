package session

import (
	"context"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
)

func setupTaskTestDB(t *testing.T) (*database.DB, *SQLiteStore, func()) {
	ctx := context.Background()
	db, err := database.Open(ctx, &database.Config{InMemory: true})
	if err != nil {
		t.Fatalf("Failed to open test database: %v", err)
	}
	sessionStore := NewSQLiteStore(db)
	return db, sessionStore, func() { _ = db.Close() }
}

// createTestWorkspace creates a workspace for testing
func createTestWorkspace(ctx context.Context, db *database.DB, workspaceID, name string) {
	_, _ = db.ExecContext(ctx, `
		INSERT INTO workspaces (id, name, created_at, updated_at)
		VALUES (?, ?, datetime('now'), datetime('now'))
	`, workspaceID, name)
}

// createTestSessionInWorkspace creates a session in a workspace for testing
func createTestSessionInWorkspace(ctx context.Context, sessionStore *SQLiteStore, sessionID, workspaceID string) {
	session := &Session{
		ID:        sessionID,
		Title:     "Test Session",
		AgentName: "assistant",
		FolderID:  workspaceID,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	_ = sessionStore.CreateSession(ctx, session)
}

func TestTaskStore_CreateAndGetTask(t *testing.T) {
	db, _, cleanup := setupTaskTestDB(t)
	defer cleanup()

	store := NewSQLiteTaskStore(db)
	ctx := context.Background()

	// Create workspace (folder) first
	createTestWorkspace(ctx, db, "workspace-1", "Test Workspace")

	task := &SessionTask{
		ID:          "task-1",
		WorkspaceID: "workspace-1",
		Description: "Test task",
		Status:      TaskStatusPending,
		Priority:    3,
	}

	// Create
	err := store.CreateTask(ctx, task)
	if err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}

	// Get
	got, err := store.GetTask(ctx, "task-1")
	if err != nil {
		t.Fatalf("Failed to get task: %v", err)
	}

	if got.ID != task.ID {
		t.Errorf("Expected ID %s, got %s", task.ID, got.ID)
	}
	if got.Description != task.Description {
		t.Errorf("Expected description %s, got %s", task.Description, got.Description)
	}
	if got.Status != task.Status {
		t.Errorf("Expected status %s, got %s", task.Status, got.Status)
	}
	if got.WorkspaceID != task.WorkspaceID {
		t.Errorf("Expected workspace_id %s, got %s", task.WorkspaceID, got.WorkspaceID)
	}
}

func TestTaskStore_TaskNotFound(t *testing.T) {
	db, _, cleanup := setupTaskTestDB(t)
	defer cleanup()

	store := NewSQLiteTaskStore(db)
	ctx := context.Background()

	_, err := store.GetTask(ctx, "nonexistent")
	if err == nil {
		t.Error("Expected error for nonexistent task, got nil")
	}
}

func TestTaskStore_CreateTaskRequiresWorkspace(t *testing.T) {
	db, _, cleanup := setupTaskTestDB(t)
	defer cleanup()

	store := NewSQLiteTaskStore(db)
	ctx := context.Background()

	task := &SessionTask{
		ID:          "task-no-workspace",
		Description: "Task without workspace",
	}

	err := store.CreateTask(ctx, task)
	if err == nil {
		t.Error("Expected error when creating task without workspace_id")
	}
}

func TestTaskStore_UpdateTask(t *testing.T) {
	db, _, cleanup := setupTaskTestDB(t)
	defer cleanup()

	store := NewSQLiteTaskStore(db)
	ctx := context.Background()

	createTestWorkspace(ctx, db, "workspace-1", "Test Workspace")

	task := &SessionTask{
		ID:          "update-task",
		WorkspaceID: "workspace-1",
		Description: "Original description",
		Status:      TaskStatusPending,
	}

	_ = store.CreateTask(ctx, task)

	// Update
	task.Description = "Updated description"
	task.Status = TaskStatusInProgress

	err := store.UpdateTask(ctx, task)
	if err != nil {
		t.Fatalf("Failed to update task: %v", err)
	}

	// Verify
	got, _ := store.GetTask(ctx, task.ID)
	if got.Description != "Updated description" {
		t.Errorf("Expected updated description, got %s", got.Description)
	}
	if got.Status != TaskStatusInProgress {
		t.Errorf("Expected in_progress status, got %s", got.Status)
	}
}

func TestTaskStore_CompleteTask(t *testing.T) {
	db, _, cleanup := setupTaskTestDB(t)
	defer cleanup()

	store := NewSQLiteTaskStore(db)
	ctx := context.Background()

	createTestWorkspace(ctx, db, "workspace-1", "Test Workspace")

	task := &SessionTask{
		ID:          "complete-task",
		WorkspaceID: "workspace-1",
		Description: "Task to complete",
		Status:      TaskStatusPending,
	}

	_ = store.CreateTask(ctx, task)

	// Complete
	err := store.CompleteTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("Failed to complete task: %v", err)
	}

	// Verify
	got, _ := store.GetTask(ctx, task.ID)
	if got.Status != TaskStatusCompleted {
		t.Errorf("Expected completed status, got %s", got.Status)
	}
	if got.CompletedAt == nil {
		t.Error("Expected CompletedAt to be set")
	}
}

func TestTaskStore_DeleteTask(t *testing.T) {
	db, _, cleanup := setupTaskTestDB(t)
	defer cleanup()

	store := NewSQLiteTaskStore(db)
	ctx := context.Background()

	createTestWorkspace(ctx, db, "workspace-1", "Test Workspace")

	task := &SessionTask{
		ID:          "delete-task",
		WorkspaceID: "workspace-1",
		Description: "Task to delete",
	}

	_ = store.CreateTask(ctx, task)

	// Delete
	err := store.DeleteTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("Failed to delete task: %v", err)
	}

	// Verify deleted
	_, err = store.GetTask(ctx, task.ID)
	if err == nil {
		t.Error("Expected error getting deleted task, got nil")
	}
}

func TestTaskStore_ListTasksByWorkspace(t *testing.T) {
	db, _, cleanup := setupTaskTestDB(t)
	defer cleanup()

	store := NewSQLiteTaskStore(db)
	ctx := context.Background()

	createTestWorkspace(ctx, db, "workspace-1", "Workspace 1")
	createTestWorkspace(ctx, db, "workspace-2", "Workspace 2")

	// Create tasks for different workspaces
	tasks := []*SessionTask{
		{ID: "t1", WorkspaceID: "workspace-1", Description: "Task 1", Status: TaskStatusPending},
		{ID: "t2", WorkspaceID: "workspace-1", Description: "Task 2", Status: TaskStatusCompleted},
		{ID: "t3", WorkspaceID: "workspace-2", Description: "Task 3", Status: TaskStatusPending}, // Different workspace
	}

	for _, task := range tasks {
		_ = store.CreateTask(ctx, task)
	}

	// List for workspace-1
	got, err := store.ListTasksByWorkspace(ctx, "workspace-1")
	if err != nil {
		t.Fatalf("Failed to list tasks: %v", err)
	}

	if len(got) != 2 {
		t.Errorf("Expected 2 tasks for workspace-1, got %d", len(got))
	}
}

func TestTaskStore_ListTasksBySession(t *testing.T) {
	db, sessionStore, cleanup := setupTaskTestDB(t)
	defer cleanup()

	store := NewSQLiteTaskStore(db)
	ctx := context.Background()

	// Create workspace and session in that workspace
	createTestWorkspace(ctx, db, "workspace-1", "Workspace 1")
	createTestSessionInWorkspace(ctx, sessionStore, "session-1", "workspace-1")

	// Create tasks for the workspace
	tasks := []*SessionTask{
		{ID: "t1", WorkspaceID: "workspace-1", Description: "Task 1", Status: TaskStatusPending},
		{ID: "t2", WorkspaceID: "workspace-1", Description: "Task 2", Status: TaskStatusCompleted},
	}

	for _, task := range tasks {
		_ = store.CreateTask(ctx, task)
	}

	// List via session (should return workspace tasks)
	got, err := store.ListTasksBySession(ctx, "session-1")
	if err != nil {
		t.Fatalf("Failed to list tasks by session: %v", err)
	}

	if len(got) != 2 {
		t.Errorf("Expected 2 tasks for session's workspace, got %d", len(got))
	}
}

func TestTaskStore_ListTasksBySession_NoWorkspace(t *testing.T) {
	db, sessionStore, cleanup := setupTaskTestDB(t)
	defer cleanup()

	store := NewSQLiteTaskStore(db)
	ctx := context.Background()

	// Create session without a folder
	session := &Session{
		ID:        "session-no-folder",
		Title:     "No Folder Session",
		AgentName: "assistant",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	_ = sessionStore.CreateSession(ctx, session)

	// List tasks (should return empty since session has no workspace)
	got, err := store.ListTasksBySession(ctx, "session-no-folder")
	if err != nil {
		t.Fatalf("Failed to list tasks by session: %v", err)
	}

	if len(got) != 0 {
		t.Errorf("Expected 0 tasks for session without workspace, got %d", len(got))
	}
}

func TestTaskStore_GetTaskCounts(t *testing.T) {
	db, sessionStore, cleanup := setupTaskTestDB(t)
	defer cleanup()

	store := NewSQLiteTaskStore(db)
	ctx := context.Background()

	createTestWorkspace(ctx, db, "workspace-1", "Workspace 1")
	createTestSessionInWorkspace(ctx, sessionStore, "session-1", "workspace-1")

	// Create tasks
	tasks := []*SessionTask{
		{ID: "c1", WorkspaceID: "workspace-1", Description: "Task 1", Status: TaskStatusPending},
		{ID: "c2", WorkspaceID: "workspace-1", Description: "Task 2", Status: TaskStatusInProgress},
		{ID: "c3", WorkspaceID: "workspace-1", Description: "Task 3", Status: TaskStatusCompleted},
	}

	for _, task := range tasks {
		_ = store.CreateTask(ctx, task)
	}

	// Get counts via session
	counts, err := store.GetTaskCounts(ctx, "session-1")
	if err != nil {
		t.Fatalf("Failed to get task counts: %v", err)
	}

	if counts.Total != 3 {
		t.Errorf("Expected total 3, got %d", counts.Total)
	}
	if counts.Pending != 2 { // pending + in_progress
		t.Errorf("Expected pending 2, got %d", counts.Pending)
	}
	if counts.Completed != 1 {
		t.Errorf("Expected completed 1, got %d", counts.Completed)
	}
}

func TestTaskStore_CreateTaskAutoFields(t *testing.T) {
	db, _, cleanup := setupTaskTestDB(t)
	defer cleanup()

	store := NewSQLiteTaskStore(db)
	ctx := context.Background()

	createTestWorkspace(ctx, db, "workspace-1", "Test Workspace")

	task := &SessionTask{
		WorkspaceID: "workspace-1",
		Description: "Task without ID",
	}

	err := store.CreateTask(ctx, task)
	if err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}

	// Verify auto-generated fields
	if task.ID == "" {
		t.Error("Expected auto-generated ID")
	}
	if task.Status != TaskStatusPending {
		t.Errorf("Expected default status pending, got %s", task.Status)
	}
	if task.Priority != 3 {
		t.Errorf("Expected default priority 3, got %d", task.Priority)
	}
	if task.CreatedAt.IsZero() {
		t.Error("Expected CreatedAt to be set")
	}
}

func TestReminderStore_CreateAndGetReminder(t *testing.T) {
	db, _, cleanup := setupTaskTestDB(t)
	defer cleanup()

	store := NewSQLiteTaskStore(db)
	ctx := context.Background()

	createTestWorkspace(ctx, db, "workspace-1", "Test Workspace")

	executeAt := time.Now().Add(24 * time.Hour)
	reminder := &ScheduledTaskReminder{
		ID:           "reminder-1",
		WorkspaceID:  "workspace-1",
		Name:         "Test Reminder",
		Description:  "Test description",
		ScheduleType: ReminderOnce,
		ExecuteAt:    &executeAt,
		Enabled:      true,
	}

	// Create
	err := store.CreateReminder(ctx, reminder)
	if err != nil {
		t.Fatalf("Failed to create reminder: %v", err)
	}

	// Get
	got, err := store.GetReminder(ctx, "reminder-1")
	if err != nil {
		t.Fatalf("Failed to get reminder: %v", err)
	}

	if got.ID != reminder.ID {
		t.Errorf("Expected ID %s, got %s", reminder.ID, got.ID)
	}
	if got.Name != reminder.Name {
		t.Errorf("Expected name %s, got %s", reminder.Name, got.Name)
	}
	if got.ScheduleType != ReminderOnce {
		t.Errorf("Expected schedule type once, got %s", got.ScheduleType)
	}
}

func TestReminderStore_DeleteReminder(t *testing.T) {
	db, _, cleanup := setupTaskTestDB(t)
	defer cleanup()

	store := NewSQLiteTaskStore(db)
	ctx := context.Background()

	createTestWorkspace(ctx, db, "workspace-1", "Test Workspace")

	reminder := &ScheduledTaskReminder{
		ID:           "delete-reminder",
		WorkspaceID:  "workspace-1",
		Name:         "To Delete",
		ScheduleType: ReminderOnce,
		Enabled:      true,
	}

	_ = store.CreateReminder(ctx, reminder)

	// Delete
	err := store.DeleteReminder(ctx, reminder.ID)
	if err != nil {
		t.Fatalf("Failed to delete reminder: %v", err)
	}

	// Verify deleted
	_, err = store.GetReminder(ctx, reminder.ID)
	if err == nil {
		t.Error("Expected error getting deleted reminder, got nil")
	}
}

func TestReminderStore_CalculateNextRun_Once(t *testing.T) {
	db, _, cleanup := setupTaskTestDB(t)
	defer cleanup()

	store := NewSQLiteTaskStore(db)
	ctx := context.Background()

	createTestWorkspace(ctx, db, "workspace-1", "Test Workspace")

	futureTime := time.Now().Add(24 * time.Hour)
	reminder := &ScheduledTaskReminder{
		WorkspaceID:  "workspace-1",
		Name:         "Once Reminder",
		ScheduleType: ReminderOnce,
		ExecuteAt:    &futureTime,
		Enabled:      true,
	}

	_ = store.CreateReminder(ctx, reminder)

	if reminder.NextRun == nil {
		t.Error("Expected NextRun to be set for once reminder")
	}
	// Check that NextRun is close to ExecuteAt (within a second)
	if reminder.NextRun.Sub(futureTime).Abs() > time.Second {
		t.Errorf("Expected NextRun to be close to ExecuteAt")
	}
}

func TestReminderStore_CalculateNextRun_Daily(t *testing.T) {
	db, _, cleanup := setupTaskTestDB(t)
	defer cleanup()

	store := NewSQLiteTaskStore(db)
	ctx := context.Background()

	createTestWorkspace(ctx, db, "workspace-1", "Test Workspace")

	reminder := &ScheduledTaskReminder{
		WorkspaceID:  "workspace-1",
		Name:         "Daily Reminder",
		ScheduleType: ReminderDaily,
		TimeOfDay:    "09:00",
		Enabled:      true,
	}

	_ = store.CreateReminder(ctx, reminder)

	if reminder.NextRun == nil {
		t.Error("Expected NextRun to be set for daily reminder")
	}
	if reminder.NextRun.Hour() != 9 || reminder.NextRun.Minute() != 0 {
		t.Errorf("Expected NextRun at 09:00, got %02d:%02d", reminder.NextRun.Hour(), reminder.NextRun.Minute())
	}
}

func TestTaskStore_GetSessionWorkspace(t *testing.T) {
	db, sessionStore, cleanup := setupTaskTestDB(t)
	defer cleanup()

	store := NewSQLiteTaskStore(db)
	ctx := context.Background()

	createTestWorkspace(ctx, db, "workspace-1", "Test Workspace")
	createTestSessionInWorkspace(ctx, sessionStore, "session-1", "workspace-1")

	// Get workspace for session
	wsID, err := store.GetSessionWorkspace(ctx, "session-1")
	if err != nil {
		t.Fatalf("Failed to get session workspace: %v", err)
	}

	if wsID != "workspace-1" {
		t.Errorf("Expected workspace-1, got %s", wsID)
	}
}
