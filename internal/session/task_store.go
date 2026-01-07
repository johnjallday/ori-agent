package session

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/database"
)

// TaskStore defines the interface for session task persistence operations.
type TaskStore interface {
	// CreateTask creates a new task.
	CreateTask(ctx context.Context, task *SessionTask) error

	// GetTask retrieves a task by ID.
	GetTask(ctx context.Context, taskID string) (*SessionTask, error)

	// ListTasksBySession returns all tasks for a session (including workspace-level tasks).
	ListTasksBySession(ctx context.Context, sessionID string) ([]SessionTask, error)

	// ListTasksByWorkspace returns all workspace-level tasks.
	ListTasksByWorkspace(ctx context.Context, workspaceID string) ([]SessionTask, error)

	// UpdateTask updates an existing task.
	UpdateTask(ctx context.Context, task *SessionTask) error

	// DeleteTask removes a task.
	DeleteTask(ctx context.Context, taskID string) error

	// CompleteTask marks a task as completed.
	CompleteTask(ctx context.Context, taskID string) error

	// GetTaskCounts returns task statistics for a session.
	GetTaskCounts(ctx context.Context, sessionID string) (*TaskCounts, error)

	// GetWorkspaceTaskCounts returns task statistics for a workspace.
	GetWorkspaceTaskCounts(ctx context.Context, workspaceID string) (*TaskCounts, error)
}

// SQLiteTaskStore implements TaskStore using SQLite.
type SQLiteTaskStore struct {
	db *database.DB
}

// NewSQLiteTaskStore creates a new SQLite-backed task store.
func NewSQLiteTaskStore(db *database.DB) *SQLiteTaskStore {
	return &SQLiteTaskStore{db: db}
}

// CreateTask creates a new task in the database.
func (s *SQLiteTaskStore) CreateTask(ctx context.Context, task *SessionTask) error {
	if task.ID == "" {
		task.ID = uuid.New().String()
	}

	now := time.Now()
	if task.CreatedAt.IsZero() {
		task.CreatedAt = now
	}
	task.UpdatedAt = now

	if task.Status == "" {
		task.Status = TaskStatusPending
	}

	if task.Priority == 0 {
		task.Priority = 3 // Default medium priority
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO session_tasks (id, session_id, workspace_id, description, details, status, priority, created_at, updated_at, completed_at)
		VALUES (?, NULLIF(?, ''), NULLIF(?, ''), ?, ?, ?, ?, ?, ?, ?)
	`, task.ID, task.SessionID, task.WorkspaceID, task.Description, task.Details, task.Status,
		task.Priority, task.CreatedAt, task.UpdatedAt, task.CompletedAt)

	if err != nil {
		return fmt.Errorf("failed to create task: %w", err)
	}

	return nil
}

// GetTask retrieves a task by ID.
func (s *SQLiteTaskStore) GetTask(ctx context.Context, taskID string) (*SessionTask, error) {
	task := &SessionTask{}

	var sessionID, workspaceID sql.NullString
	var completedAt sql.NullTime

	err := s.db.QueryRowContext(ctx, `
		SELECT id, session_id, workspace_id, description, details, status, priority, created_at, updated_at, completed_at
		FROM session_tasks WHERE id = ?
	`, taskID).Scan(&task.ID, &sessionID, &workspaceID, &task.Description, &task.Details, &task.Status,
		&task.Priority, &task.CreatedAt, &task.UpdatedAt, &completedAt)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("task not found: %s", taskID)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get task: %w", err)
	}

	task.SessionID = sessionID.String
	task.WorkspaceID = workspaceID.String
	if completedAt.Valid {
		task.CompletedAt = &completedAt.Time
	}

	return task, nil
}

// ListTasksBySession returns all tasks for a session, including workspace-level tasks.
func (s *SQLiteTaskStore) ListTasksBySession(ctx context.Context, sessionID string) ([]SessionTask, error) {
	// First, get the workspace (folder) ID for this session
	var workspaceID sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT folder_id FROM sessions WHERE id = ?
	`, sessionID).Scan(&workspaceID)
	if err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("failed to get session folder: %w", err)
	}

	// Query tasks for this session OR workspace-level tasks in the same workspace
	query := `
		SELECT id, session_id, workspace_id, description, details, status, priority, created_at, updated_at, completed_at
		FROM session_tasks
		WHERE session_id = ? OR (session_id IS NULL AND workspace_id = ?)
		ORDER BY
			CASE status
				WHEN 'pending' THEN 1
				WHEN 'in_progress' THEN 2
				WHEN 'completed' THEN 3
				WHEN 'cancelled' THEN 4
			END,
			created_at DESC
	`

	rows, err := s.db.QueryContext(ctx, query, sessionID, workspaceID.String)
	if err != nil {
		return nil, fmt.Errorf("failed to list tasks: %w", err)
	}
	defer rows.Close()

	return s.scanTasks(rows)
}

// ListTasksByWorkspace returns all workspace-level tasks (not attached to a specific session).
func (s *SQLiteTaskStore) ListTasksByWorkspace(ctx context.Context, workspaceID string) ([]SessionTask, error) {
	query := `
		SELECT id, session_id, workspace_id, description, details, status, priority, created_at, updated_at, completed_at
		FROM session_tasks
		WHERE workspace_id = ? AND session_id IS NULL
		ORDER BY
			CASE status
				WHEN 'pending' THEN 1
				WHEN 'in_progress' THEN 2
				WHEN 'completed' THEN 3
				WHEN 'cancelled' THEN 4
			END,
			created_at DESC
	`

	rows, err := s.db.QueryContext(ctx, query, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("failed to list workspace tasks: %w", err)
	}
	defer rows.Close()

	return s.scanTasks(rows)
}

// scanTasks scans task rows into SessionTask structs.
func (s *SQLiteTaskStore) scanTasks(rows *sql.Rows) ([]SessionTask, error) {
	var tasks []SessionTask

	for rows.Next() {
		var task SessionTask
		var sessionID, workspaceID sql.NullString
		var completedAt sql.NullTime

		if err := rows.Scan(&task.ID, &sessionID, &workspaceID, &task.Description, &task.Details, &task.Status,
			&task.Priority, &task.CreatedAt, &task.UpdatedAt, &completedAt); err != nil {
			return nil, fmt.Errorf("failed to scan task: %w", err)
		}

		task.SessionID = sessionID.String
		task.WorkspaceID = workspaceID.String
		if completedAt.Valid {
			task.CompletedAt = &completedAt.Time
		}

		tasks = append(tasks, task)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating tasks: %w", err)
	}

	return tasks, nil
}

// UpdateTask updates an existing task.
func (s *SQLiteTaskStore) UpdateTask(ctx context.Context, task *SessionTask) error {
	task.UpdatedAt = time.Now()

	result, err := s.db.ExecContext(ctx, `
		UPDATE session_tasks
		SET description = ?, details = ?, status = ?, priority = ?, updated_at = ?, completed_at = ?
		WHERE id = ?
	`, task.Description, task.Details, task.Status, task.Priority, task.UpdatedAt, task.CompletedAt, task.ID)

	if err != nil {
		return fmt.Errorf("failed to update task: %w", err)
	}

	if err := database.CheckRowsAffectedWithError(result, "task", fmt.Errorf("task not found: %s", task.ID)); err != nil {
		return err
	}

	return nil
}

// DeleteTask removes a task.
func (s *SQLiteTaskStore) DeleteTask(ctx context.Context, taskID string) error {
	result, err := s.db.ExecContext(ctx, "DELETE FROM session_tasks WHERE id = ?", taskID)
	if err != nil {
		return fmt.Errorf("failed to delete task: %w", err)
	}

	if err := database.CheckRowsAffectedWithError(result, "task", fmt.Errorf("task not found: %s", taskID)); err != nil {
		return err
	}

	return nil
}

// CompleteTask marks a task as completed.
func (s *SQLiteTaskStore) CompleteTask(ctx context.Context, taskID string) error {
	now := time.Now()

	result, err := s.db.ExecContext(ctx, `
		UPDATE session_tasks
		SET status = ?, updated_at = ?, completed_at = ?
		WHERE id = ?
	`, TaskStatusCompleted, now, now, taskID)

	if err != nil {
		return fmt.Errorf("failed to complete task: %w", err)
	}

	if err := database.CheckRowsAffectedWithError(result, "task", fmt.Errorf("task not found: %s", taskID)); err != nil {
		return err
	}

	return nil
}

// GetTaskCounts returns task statistics for a session.
func (s *SQLiteTaskStore) GetTaskCounts(ctx context.Context, sessionID string) (*TaskCounts, error) {
	// First, get the workspace (folder) ID for this session
	var workspaceID sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT folder_id FROM sessions WHERE id = ?
	`, sessionID).Scan(&workspaceID)
	if err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("failed to get session folder: %w", err)
	}

	counts := &TaskCounts{}

	err = s.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*) as total,
			COALESCE(SUM(CASE WHEN status IN ('pending', 'in_progress') THEN 1 ELSE 0 END), 0) as pending,
			COALESCE(SUM(CASE WHEN status = 'completed' THEN 1 ELSE 0 END), 0) as completed
		FROM session_tasks
		WHERE session_id = ? OR (session_id IS NULL AND workspace_id = ?)
	`, sessionID, workspaceID.String).Scan(&counts.Total, &counts.Pending, &counts.Completed)

	if err != nil {
		return nil, fmt.Errorf("failed to get task counts: %w", err)
	}

	return counts, nil
}

// GetWorkspaceTaskCounts returns task statistics for a workspace.
func (s *SQLiteTaskStore) GetWorkspaceTaskCounts(ctx context.Context, workspaceID string) (*TaskCounts, error) {
	counts := &TaskCounts{}

	err := s.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*) as total,
			COALESCE(SUM(CASE WHEN status IN ('pending', 'in_progress') THEN 1 ELSE 0 END), 0) as pending,
			COALESCE(SUM(CASE WHEN status = 'completed' THEN 1 ELSE 0 END), 0) as completed
		FROM session_tasks
		WHERE workspace_id = ? AND session_id IS NULL
	`, workspaceID).Scan(&counts.Total, &counts.Pending, &counts.Completed)

	if err != nil {
		return nil, fmt.Errorf("failed to get workspace task counts: %w", err)
	}

	return counts, nil
}

// ===== Scheduled Reminder Store Methods =====

// ReminderStore defines the interface for scheduled reminder persistence.
type ReminderStore interface {
	// CreateReminder creates a new scheduled reminder.
	CreateReminder(ctx context.Context, reminder *ScheduledTaskReminder) error

	// GetReminder retrieves a reminder by ID.
	GetReminder(ctx context.Context, reminderID string) (*ScheduledTaskReminder, error)

	// ListRemindersBySession returns all reminders for a session.
	ListRemindersBySession(ctx context.Context, sessionID string) ([]ScheduledTaskReminder, error)

	// UpdateReminder updates an existing reminder.
	UpdateReminder(ctx context.Context, reminder *ScheduledTaskReminder) error

	// DeleteReminder removes a reminder.
	DeleteReminder(ctx context.Context, reminderID string) error

	// ListDueReminders returns all reminders that need to fire now.
	ListDueReminders(ctx context.Context) ([]ScheduledTaskReminder, error)
}

// CreateReminder creates a new scheduled reminder.
func (s *SQLiteTaskStore) CreateReminder(ctx context.Context, reminder *ScheduledTaskReminder) error {
	if reminder.ID == "" {
		reminder.ID = uuid.New().String()
	}

	now := time.Now()
	if reminder.CreatedAt.IsZero() {
		reminder.CreatedAt = now
	}
	reminder.UpdatedAt = now

	// Calculate next run time based on schedule type
	reminder.NextRun = s.calculateNextRun(reminder)

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO scheduled_task_reminders
		(id, session_id, workspace_id, name, description, schedule_type, execute_at, time_of_day, day_of_week, next_run, last_run, enabled, created_at, updated_at)
		VALUES (?, NULLIF(?, ''), NULLIF(?, ''), ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, reminder.ID, reminder.SessionID, reminder.WorkspaceID, reminder.Name, reminder.Description,
		reminder.ScheduleType, reminder.ExecuteAt, reminder.TimeOfDay, reminder.DayOfWeek,
		reminder.NextRun, reminder.LastRun, reminder.Enabled, reminder.CreatedAt, reminder.UpdatedAt)

	if err != nil {
		return fmt.Errorf("failed to create reminder: %w", err)
	}

	return nil
}

// GetReminder retrieves a reminder by ID.
func (s *SQLiteTaskStore) GetReminder(ctx context.Context, reminderID string) (*ScheduledTaskReminder, error) {
	reminder := &ScheduledTaskReminder{}

	var sessionID, workspaceID sql.NullString
	var executeAt, nextRun, lastRun sql.NullTime

	err := s.db.QueryRowContext(ctx, `
		SELECT id, session_id, workspace_id, name, description, schedule_type, execute_at, time_of_day, day_of_week, next_run, last_run, enabled, created_at, updated_at
		FROM scheduled_task_reminders WHERE id = ?
	`, reminderID).Scan(&reminder.ID, &sessionID, &workspaceID, &reminder.Name, &reminder.Description,
		&reminder.ScheduleType, &executeAt, &reminder.TimeOfDay, &reminder.DayOfWeek,
		&nextRun, &lastRun, &reminder.Enabled, &reminder.CreatedAt, &reminder.UpdatedAt)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("reminder not found: %s", reminderID)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get reminder: %w", err)
	}

	reminder.SessionID = sessionID.String
	reminder.WorkspaceID = workspaceID.String
	if executeAt.Valid {
		reminder.ExecuteAt = &executeAt.Time
	}
	if nextRun.Valid {
		reminder.NextRun = &nextRun.Time
	}
	if lastRun.Valid {
		reminder.LastRun = &lastRun.Time
	}

	return reminder, nil
}

// ListRemindersBySession returns all reminders for a session.
func (s *SQLiteTaskStore) ListRemindersBySession(ctx context.Context, sessionID string) ([]ScheduledTaskReminder, error) {
	// Get workspace ID for this session
	var workspaceID sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT folder_id FROM sessions WHERE id = ?`, sessionID).Scan(&workspaceID)
	if err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("failed to get session folder: %w", err)
	}

	query := `
		SELECT id, session_id, workspace_id, name, description, schedule_type, execute_at, time_of_day, day_of_week, next_run, last_run, enabled, created_at, updated_at
		FROM scheduled_task_reminders
		WHERE session_id = ? OR (session_id IS NULL AND workspace_id = ?)
		ORDER BY next_run ASC NULLS LAST, created_at DESC
	`

	rows, err := s.db.QueryContext(ctx, query, sessionID, workspaceID.String)
	if err != nil {
		return nil, fmt.Errorf("failed to list reminders: %w", err)
	}
	defer rows.Close()

	return s.scanReminders(rows)
}

// UpdateReminder updates an existing reminder.
func (s *SQLiteTaskStore) UpdateReminder(ctx context.Context, reminder *ScheduledTaskReminder) error {
	reminder.UpdatedAt = time.Now()

	// Recalculate next run time
	reminder.NextRun = s.calculateNextRun(reminder)

	result, err := s.db.ExecContext(ctx, `
		UPDATE scheduled_task_reminders
		SET name = ?, description = ?, schedule_type = ?, execute_at = ?, time_of_day = ?, day_of_week = ?, next_run = ?, enabled = ?, updated_at = ?
		WHERE id = ?
	`, reminder.Name, reminder.Description, reminder.ScheduleType, reminder.ExecuteAt,
		reminder.TimeOfDay, reminder.DayOfWeek, reminder.NextRun, reminder.Enabled, reminder.UpdatedAt, reminder.ID)

	if err != nil {
		return fmt.Errorf("failed to update reminder: %w", err)
	}

	if err := database.CheckRowsAffectedWithError(result, "reminder", fmt.Errorf("reminder not found: %s", reminder.ID)); err != nil {
		return err
	}

	return nil
}

// DeleteReminder removes a reminder.
func (s *SQLiteTaskStore) DeleteReminder(ctx context.Context, reminderID string) error {
	result, err := s.db.ExecContext(ctx, "DELETE FROM scheduled_task_reminders WHERE id = ?", reminderID)
	if err != nil {
		return fmt.Errorf("failed to delete reminder: %w", err)
	}

	if err := database.CheckRowsAffectedWithError(result, "reminder", fmt.Errorf("reminder not found: %s", reminderID)); err != nil {
		return err
	}

	return nil
}

// ListDueReminders returns all enabled reminders whose next_run is in the past.
func (s *SQLiteTaskStore) ListDueReminders(ctx context.Context) ([]ScheduledTaskReminder, error) {
	query := `
		SELECT id, session_id, workspace_id, name, description, schedule_type, execute_at, time_of_day, day_of_week, next_run, last_run, enabled, created_at, updated_at
		FROM scheduled_task_reminders
		WHERE enabled = 1 AND next_run IS NOT NULL AND next_run <= ?
		ORDER BY next_run ASC
	`

	rows, err := s.db.QueryContext(ctx, query, time.Now())
	if err != nil {
		return nil, fmt.Errorf("failed to list due reminders: %w", err)
	}
	defer rows.Close()

	return s.scanReminders(rows)
}

// scanReminders scans reminder rows into ScheduledTaskReminder structs.
func (s *SQLiteTaskStore) scanReminders(rows *sql.Rows) ([]ScheduledTaskReminder, error) {
	var reminders []ScheduledTaskReminder

	for rows.Next() {
		var reminder ScheduledTaskReminder
		var sessionID, workspaceID sql.NullString
		var executeAt, nextRun, lastRun sql.NullTime

		if err := rows.Scan(&reminder.ID, &sessionID, &workspaceID, &reminder.Name, &reminder.Description,
			&reminder.ScheduleType, &executeAt, &reminder.TimeOfDay, &reminder.DayOfWeek,
			&nextRun, &lastRun, &reminder.Enabled, &reminder.CreatedAt, &reminder.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan reminder: %w", err)
		}

		reminder.SessionID = sessionID.String
		reminder.WorkspaceID = workspaceID.String
		if executeAt.Valid {
			reminder.ExecuteAt = &executeAt.Time
		}
		if nextRun.Valid {
			reminder.NextRun = &nextRun.Time
		}
		if lastRun.Valid {
			reminder.LastRun = &lastRun.Time
		}

		reminders = append(reminders, reminder)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating reminders: %w", err)
	}

	return reminders, nil
}

// calculateNextRun determines the next run time based on schedule type.
func (s *SQLiteTaskStore) calculateNextRun(reminder *ScheduledTaskReminder) *time.Time {
	now := time.Now()

	switch reminder.ScheduleType {
	case ReminderOnce:
		if reminder.ExecuteAt != nil && reminder.ExecuteAt.After(now) {
			return reminder.ExecuteAt
		}
		return nil

	case ReminderDaily:
		if reminder.TimeOfDay == "" {
			return nil
		}
		// Parse time of day (e.g., "09:00")
		parts := strings.Split(reminder.TimeOfDay, ":")
		if len(parts) != 2 {
			return nil
		}
		hour, err1 := parseInt(parts[0])
		minute, err2 := parseInt(parts[1])
		if err1 != nil || err2 != nil {
			return nil
		}

		next := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, now.Location())
		if !next.After(now) {
			next = next.AddDate(0, 0, 1)
		}
		return &next

	case ReminderWeekly:
		if reminder.TimeOfDay == "" {
			return nil
		}
		parts := strings.Split(reminder.TimeOfDay, ":")
		if len(parts) != 2 {
			return nil
		}
		hour, err1 := parseInt(parts[0])
		minute, err2 := parseInt(parts[1])
		if err1 != nil || err2 != nil {
			return nil
		}

		// Calculate days until target day of week
		targetDay := time.Weekday(reminder.DayOfWeek % 7)
		currentDay := now.Weekday()
		daysUntil := int(targetDay - currentDay)
		if daysUntil < 0 {
			daysUntil += 7
		}

		next := time.Date(now.Year(), now.Month(), now.Day()+daysUntil, hour, minute, 0, 0, now.Location())
		if !next.After(now) {
			next = next.AddDate(0, 0, 7)
		}
		return &next
	}

	return nil
}

// parseInt is a helper to parse an integer string.
func parseInt(s string) (int, error) {
	s = strings.TrimSpace(s)
	var n int
	_, err := fmt.Sscanf(s, "%d", &n)
	return n, err
}
