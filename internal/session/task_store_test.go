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

// createTestSession creates a session for testing (needed for foreign key constraints)
func createTestSession(ctx context.Context, sessionStore *SQLiteStore, sessionID string) {
	session := &Session{
		ID:        sessionID,
		Title:     "Test Session",
		AgentName: "assistant",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	_ = sessionStore.CreateSession(ctx, session)
}

func TestTaskStore_CreateAndGetTask(t *testing.T) {
	db, sessionStore, cleanup := setupTaskTestDB(t)
	defer cleanup()

	store := NewSQLiteTaskStore(db)
	ctx := context.Background()

	// Create parent session first
	createTestSession(ctx, sessionStore, "session-1")

	task := &SessionTask{
		ID:          "task-1",
		SessionID:   "session-1",
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

func TestTaskStore_UpdateTask(t *testing.T) {
	db, sessionStore, cleanup := setupTaskTestDB(t)
	defer cleanup()

	store := NewSQLiteTaskStore(db)
	ctx := context.Background()

	createTestSession(ctx, sessionStore, "session-1")

	task := &SessionTask{
		ID:          "update-task",
		SessionID:   "session-1",
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
	db, sessionStore, cleanup := setupTaskTestDB(t)
	defer cleanup()

	store := NewSQLiteTaskStore(db)
	ctx := context.Background()

	createTestSession(ctx, sessionStore, "session-1")

	task := &SessionTask{
		ID:          "complete-task",
		SessionID:   "session-1",
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
	db, sessionStore, cleanup := setupTaskTestDB(t)
	defer cleanup()

	store := NewSQLiteTaskStore(db)
	ctx := context.Background()

	createTestSession(ctx, sessionStore, "session-1")

	task := &SessionTask{
		ID:          "delete-task",
		SessionID:   "session-1",
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

func TestTaskStore_ListTasksBySession(t *testing.T) {
	db, sessionStore, cleanup := setupTaskTestDB(t)
	defer cleanup()

	store := NewSQLiteTaskStore(db)
	ctx := context.Background()

	createTestSession(ctx, sessionStore, "session-1")
	createTestSession(ctx, sessionStore, "session-2")

	// Create tasks for session-1
	tasks := []*SessionTask{
		{ID: "t1", SessionID: "session-1", Description: "Task 1", Status: TaskStatusPending},
		{ID: "t2", SessionID: "session-1", Description: "Task 2", Status: TaskStatusCompleted},
		{ID: "t3", SessionID: "session-2", Description: "Task 3", Status: TaskStatusPending}, // Different session
	}

	for _, task := range tasks {
		_ = store.CreateTask(ctx, task)
	}

	// List for session-1
	got, err := store.ListTasksBySession(ctx, "session-1")
	if err != nil {
		t.Fatalf("Failed to list tasks: %v", err)
	}

	if len(got) != 2 {
		t.Errorf("Expected 2 tasks for session-1, got %d", len(got))
	}
}

func TestTaskStore_GetTaskCounts(t *testing.T) {
	db, sessionStore, cleanup := setupTaskTestDB(t)
	defer cleanup()

	store := NewSQLiteTaskStore(db)
	ctx := context.Background()

	createTestSession(ctx, sessionStore, "session-1")

	// Create tasks
	tasks := []*SessionTask{
		{ID: "c1", SessionID: "session-1", Description: "Task 1", Status: TaskStatusPending},
		{ID: "c2", SessionID: "session-1", Description: "Task 2", Status: TaskStatusInProgress},
		{ID: "c3", SessionID: "session-1", Description: "Task 3", Status: TaskStatusCompleted},
	}

	for _, task := range tasks {
		_ = store.CreateTask(ctx, task)
	}

	// Get counts
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
	db, sessionStore, cleanup := setupTaskTestDB(t)
	defer cleanup()

	store := NewSQLiteTaskStore(db)
	ctx := context.Background()

	createTestSession(ctx, sessionStore, "session-1")

	task := &SessionTask{
		SessionID:   "session-1",
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
	db, sessionStore, cleanup := setupTaskTestDB(t)
	defer cleanup()

	store := NewSQLiteTaskStore(db)
	ctx := context.Background()

	createTestSession(ctx, sessionStore, "session-1")

	executeAt := time.Now().Add(24 * time.Hour)
	reminder := &ScheduledTaskReminder{
		ID:           "reminder-1",
		SessionID:    "session-1",
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
	db, sessionStore, cleanup := setupTaskTestDB(t)
	defer cleanup()

	store := NewSQLiteTaskStore(db)
	ctx := context.Background()

	createTestSession(ctx, sessionStore, "session-1")

	reminder := &ScheduledTaskReminder{
		ID:           "delete-reminder",
		SessionID:    "session-1",
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
	db, sessionStore, cleanup := setupTaskTestDB(t)
	defer cleanup()

	store := NewSQLiteTaskStore(db)
	ctx := context.Background()

	createTestSession(ctx, sessionStore, "session-1")

	futureTime := time.Now().Add(24 * time.Hour)
	reminder := &ScheduledTaskReminder{
		SessionID:    "session-1",
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
	db, sessionStore, cleanup := setupTaskTestDB(t)
	defer cleanup()

	store := NewSQLiteTaskStore(db)
	ctx := context.Background()

	createTestSession(ctx, sessionStore, "session-1")

	reminder := &ScheduledTaskReminder{
		SessionID:    "session-1",
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
