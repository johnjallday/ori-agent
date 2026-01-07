package database

import (
	"context"
	"fmt"

	"github.com/johnjallday/ori-agent/internal/logger"
)

// schemaVersion is the current database schema version.
// Increment this when adding new migrations.
const schemaVersion = 6

// migrate runs all pending migrations to bring the database up to the current schema.
func (db *DB) migrate(ctx context.Context) error {
	// Create the migrations tracking table if it doesn't exist
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`); err != nil {
		return fmt.Errorf("failed to create migrations table: %w", err)
	}

	// Get current version
	var currentVersion int
	err := db.QueryRowContext(ctx, "SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&currentVersion)
	if err != nil {
		return fmt.Errorf("failed to get current schema version: %w", err)
	}

	if currentVersion >= schemaVersion {
		logger.Debug("Database schema up to date", logger.Fields{"version": currentVersion})
		return nil
	}

	logger.Info("Running database migrations", logger.Fields{
		"from_version": currentVersion,
		"to_version":   schemaVersion,
	})

	// Run migrations in order
	for version := currentVersion + 1; version <= schemaVersion; version++ {
		if err := db.runMigration(ctx, version); err != nil {
			return fmt.Errorf("migration %d failed: %w", version, err)
		}

		// Record the migration
		if _, err := db.ExecContext(ctx, "INSERT INTO schema_migrations (version) VALUES (?)", version); err != nil {
			return fmt.Errorf("failed to record migration %d: %w", version, err)
		}

		logger.Info("Applied migration", logger.Fields{"version": version})
	}

	return nil
}

// runMigration executes a specific migration version.
func (db *DB) runMigration(ctx context.Context, version int) error {
	switch version {
	case 1:
		return db.migration001InitialSchema(ctx)
	case 2:
		return db.migration002ReviewSchema(ctx)
	case 3:
		return db.migration003FixToolCallsForeignKey(ctx)
	case 4:
		return db.migration004SessionTasks(ctx)
	case 5:
		return db.migration005TaskDetails(ctx)
	case 6:
		return db.migration006TasksWorkspaceOnly(ctx)
	default:
		return fmt.Errorf("unknown migration version: %d", version)
	}
}

// migration001InitialSchema creates the initial database schema.
func (db *DB) migration001InitialSchema(ctx context.Context) error {
	// Create folders table
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS folders (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			description TEXT DEFAULT '',
			parent_id TEXT,
			color TEXT,
			session_count INTEGER DEFAULT 0,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			FOREIGN KEY (parent_id) REFERENCES folders(id) ON DELETE SET NULL
		)
	`); err != nil {
		return fmt.Errorf("failed to create folders table: %w", err)
	}

	// Create sessions table
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS sessions (
			id TEXT PRIMARY KEY,
			title TEXT NOT NULL,
			agent_name TEXT NOT NULL,
			folder_id TEXT,
			message_count INTEGER DEFAULT 0,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			FOREIGN KEY (folder_id) REFERENCES folders(id) ON DELETE SET NULL
		)
	`); err != nil {
		return fmt.Errorf("failed to create sessions table: %w", err)
	}

	// Create messages table
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS messages (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			role TEXT NOT NULL,
			content TEXT NOT NULL,
			model TEXT,
			tokens_used INTEGER DEFAULT 0,
			created_at DATETIME NOT NULL,
			FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
		)
	`); err != nil {
		return fmt.Errorf("failed to create messages table: %w", err)
	}

	// Create session_tags table (many-to-many relationship)
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS session_tags (
			session_id TEXT NOT NULL,
			tag TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (session_id, tag),
			FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
		)
	`); err != nil {
		return fmt.Errorf("failed to create session_tags table: %w", err)
	}

	// Create indexes for performance
	indexes := []string{
		// Sessions indexes
		"CREATE INDEX IF NOT EXISTS idx_sessions_agent_name ON sessions(agent_name)",
		"CREATE INDEX IF NOT EXISTS idx_sessions_folder_id ON sessions(folder_id)",
		"CREATE INDEX IF NOT EXISTS idx_sessions_updated_at ON sessions(updated_at DESC)",
		"CREATE INDEX IF NOT EXISTS idx_sessions_created_at ON sessions(created_at DESC)",

		// Messages indexes
		"CREATE INDEX IF NOT EXISTS idx_messages_session_id ON messages(session_id)",
		"CREATE INDEX IF NOT EXISTS idx_messages_created_at ON messages(created_at)",

		// Tags indexes
		"CREATE INDEX IF NOT EXISTS idx_session_tags_tag ON session_tags(tag)",

		// Folders indexes
		"CREATE INDEX IF NOT EXISTS idx_folders_parent_id ON folders(parent_id)",
	}

	for _, idx := range indexes {
		if _, err := db.ExecContext(ctx, idx); err != nil {
			return fmt.Errorf("failed to create index: %w", err)
		}
	}

	// Create FTS5 virtual table for full-text search
	// This is a standalone FTS table (not content-synced) that we populate manually
	// It stores session titles and message content for searchability
	if _, err := db.ExecContext(ctx, `
		CREATE VIRTUAL TABLE IF NOT EXISTS sessions_fts USING fts5(
			session_id,
			title,
			content,
			tokenize='porter unicode61'
		)
	`); err != nil {
		return fmt.Errorf("failed to create FTS table: %w", err)
	}

	// Create triggers to keep FTS index in sync
	// Note: Message content updates are handled by the application layer
	// since we need to aggregate all messages for a session
	triggers := []string{
		// Insert trigger for sessions - add entry with empty content
		`CREATE TRIGGER IF NOT EXISTS sessions_ai AFTER INSERT ON sessions BEGIN
			INSERT INTO sessions_fts(session_id, title, content) VALUES (new.id, new.title, '');
		END`,

		// Update trigger for sessions - update title
		`CREATE TRIGGER IF NOT EXISTS sessions_au AFTER UPDATE OF title ON sessions BEGIN
			UPDATE sessions_fts SET title = new.title WHERE session_id = old.id;
		END`,

		// Delete trigger for sessions - remove entry
		`CREATE TRIGGER IF NOT EXISTS sessions_ad AFTER DELETE ON sessions BEGIN
			DELETE FROM sessions_fts WHERE session_id = old.id;
		END`,
	}

	for _, trigger := range triggers {
		if _, err := db.ExecContext(ctx, trigger); err != nil {
			return fmt.Errorf("failed to create trigger: %w", err)
		}
	}

	// Create view for tag counts (useful for autocomplete)
	if _, err := db.ExecContext(ctx, `
		CREATE VIEW IF NOT EXISTS tag_counts AS
		SELECT tag as name, COUNT(*) as usage_count
		FROM session_tags
		GROUP BY tag
		ORDER BY usage_count DESC
	`); err != nil {
		return fmt.Errorf("failed to create tag_counts view: %w", err)
	}

	// Create folder_notes table
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS folder_notes (
			id TEXT PRIMARY KEY,
			folder_id TEXT NOT NULL,
			name TEXT NOT NULL,
			content TEXT DEFAULT '',
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			FOREIGN KEY (folder_id) REFERENCES folders(id) ON DELETE CASCADE
		)
	`); err != nil {
		return fmt.Errorf("failed to create folder_notes table: %w", err)
	}

	// Create indexes for folder_notes
	noteIndexes := []string{
		"CREATE INDEX IF NOT EXISTS idx_folder_notes_folder_id ON folder_notes(folder_id)",
		"CREATE INDEX IF NOT EXISTS idx_folder_notes_updated_at ON folder_notes(updated_at DESC)",
		"CREATE INDEX IF NOT EXISTS idx_folder_notes_name ON folder_notes(name)",
	}

	for _, idx := range noteIndexes {
		if _, err := db.ExecContext(ctx, idx); err != nil {
			return fmt.Errorf("failed to create index: %w", err)
		}
	}

	// Create FTS5 virtual table for folder notes full-text search
	if _, err := db.ExecContext(ctx, `
		CREATE VIRTUAL TABLE IF NOT EXISTS folder_notes_fts USING fts5(
			note_id,
			name,
			content,
			tokenize='porter unicode61'
		)
	`); err != nil {
		return fmt.Errorf("failed to create folder notes FTS table: %w", err)
	}

	// Create triggers to keep folder notes FTS index in sync
	noteTriggers := []string{
		`CREATE TRIGGER IF NOT EXISTS folder_notes_ai AFTER INSERT ON folder_notes BEGIN
			INSERT INTO folder_notes_fts(note_id, name, content) VALUES (new.id, new.name, new.content);
		END`,
		`CREATE TRIGGER IF NOT EXISTS folder_notes_au AFTER UPDATE ON folder_notes BEGIN
			UPDATE folder_notes_fts SET name = new.name, content = new.content WHERE note_id = old.id;
		END`,
		`CREATE TRIGGER IF NOT EXISTS folder_notes_ad AFTER DELETE ON folder_notes BEGIN
			DELETE FROM folder_notes_fts WHERE note_id = old.id;
		END`,
	}

	for _, trigger := range noteTriggers {
		if _, err := db.ExecContext(ctx, trigger); err != nil {
			return fmt.Errorf("failed to create trigger: %w", err)
		}
	}

	return nil
}

// GetSchemaVersion returns the current database schema version.
func (db *DB) GetSchemaVersion(ctx context.Context) (int, error) {
	var version int
	err := db.QueryRowContext(ctx, "SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&version)
	if err != nil {
		return 0, err
	}
	return version, nil
}

// migration002ReviewSchema adds tables for conversation review and tool call tracking.
func (db *DB) migration002ReviewSchema(ctx context.Context) error {
	// Create tool_calls table for storing tool call metadata
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS tool_calls (
			id TEXT PRIMARY KEY,
			message_id TEXT NOT NULL,
			session_id TEXT NOT NULL,
			tool_name TEXT NOT NULL,
			arguments TEXT,
			result TEXT,
			error TEXT,
			duration_ms INTEGER,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (message_id) REFERENCES messages(id) ON DELETE CASCADE,
			FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
		)
	`); err != nil {
		return fmt.Errorf("failed to create tool_calls table: %w", err)
	}

	// Create review_issues table for detected problems
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS review_issues (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			agent_name TEXT,
			issue_type TEXT NOT NULL,
			tool_name TEXT,
			occurrence_count INTEGER,
			first_message_id TEXT,
			last_message_id TEXT,
			context_summary TEXT,
			content_hash TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
		)
	`); err != nil {
		return fmt.Errorf("failed to create review_issues table: %w", err)
	}

	// Create review_runs table for tracking review job execution
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS review_runs (
			id TEXT PRIMARY KEY,
			started_at DATETIME,
			completed_at DATETIME,
			sessions_reviewed INTEGER DEFAULT 0,
			issues_found INTEGER DEFAULT 0,
			status TEXT NOT NULL,
			error_message TEXT
		)
	`); err != nil {
		return fmt.Errorf("failed to create review_runs table: %w", err)
	}

	// Create session_review_status table for incremental review tracking
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS session_review_status (
			session_id TEXT PRIMARY KEY,
			last_reviewed_at DATETIME,
			last_message_id TEXT,
			FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
		)
	`); err != nil {
		return fmt.Errorf("failed to create session_review_status table: %w", err)
	}

	// Create indexes for performance
	indexes := []string{
		"CREATE INDEX IF NOT EXISTS idx_tool_calls_session_id ON tool_calls(session_id)",
		"CREATE INDEX IF NOT EXISTS idx_tool_calls_tool_name ON tool_calls(tool_name)",
		"CREATE INDEX IF NOT EXISTS idx_tool_calls_message_id ON tool_calls(message_id)",
		"CREATE INDEX IF NOT EXISTS idx_review_issues_session_id ON review_issues(session_id)",
		"CREATE INDEX IF NOT EXISTS idx_review_issues_issue_type ON review_issues(issue_type)",
		"CREATE INDEX IF NOT EXISTS idx_review_issues_agent_name ON review_issues(agent_name)",
		"CREATE INDEX IF NOT EXISTS idx_review_runs_status ON review_runs(status)",
	}

	for _, idx := range indexes {
		if _, err := db.ExecContext(ctx, idx); err != nil {
			return fmt.Errorf("failed to create index: %w", err)
		}
	}

	return nil
}

// migration003FixToolCallsForeignKey removes the invalid foreign key constraint on message_id.
// The message_id column stores the OpenAI tool call ID (e.g., "call_abc123"), not a reference
// to the messages table. SQLite doesn't support ALTER TABLE DROP CONSTRAINT, so we recreate the table.
func (db *DB) migration003FixToolCallsForeignKey(ctx context.Context) error {
	// Create new table without the message_id foreign key constraint
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS tool_calls_new (
			id TEXT PRIMARY KEY,
			message_id TEXT NOT NULL,
			session_id TEXT NOT NULL,
			tool_name TEXT NOT NULL,
			arguments TEXT,
			result TEXT,
			error TEXT,
			duration_ms INTEGER,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
		)
	`); err != nil {
		return fmt.Errorf("failed to create new tool_calls table: %w", err)
	}

	// Copy existing data (if any)
	if _, err := db.ExecContext(ctx, `
		INSERT INTO tool_calls_new (id, message_id, session_id, tool_name, arguments, result, error, duration_ms, created_at)
		SELECT id, message_id, session_id, tool_name, arguments, result, error, duration_ms, created_at
		FROM tool_calls
	`); err != nil {
		// Ignore error if no data exists
		logger.Debug("No existing tool_calls data to migrate", nil)
	}

	// Drop old table
	if _, err := db.ExecContext(ctx, `DROP TABLE IF EXISTS tool_calls`); err != nil {
		return fmt.Errorf("failed to drop old tool_calls table: %w", err)
	}

	// Rename new table
	if _, err := db.ExecContext(ctx, `ALTER TABLE tool_calls_new RENAME TO tool_calls`); err != nil {
		return fmt.Errorf("failed to rename tool_calls table: %w", err)
	}

	// Recreate indexes
	indexes := []string{
		"CREATE INDEX IF NOT EXISTS idx_tool_calls_session_id ON tool_calls(session_id)",
		"CREATE INDEX IF NOT EXISTS idx_tool_calls_tool_name ON tool_calls(tool_name)",
		"CREATE INDEX IF NOT EXISTS idx_tool_calls_message_id ON tool_calls(message_id)",
	}

	for _, idx := range indexes {
		if _, err := db.ExecContext(ctx, idx); err != nil {
			return fmt.Errorf("failed to create index: %w", err)
		}
	}

	return nil
}

// migration004SessionTasks adds tables for session tasks and scheduled reminders.
func (db *DB) migration004SessionTasks(ctx context.Context) error {
	// Create session_tasks table
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS session_tasks (
			id TEXT PRIMARY KEY,
			session_id TEXT,
			workspace_id TEXT,
			description TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'pending',
			priority INTEGER DEFAULT 3,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			completed_at DATETIME,
			FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE,
			FOREIGN KEY (workspace_id) REFERENCES folders(id) ON DELETE CASCADE
		)
	`); err != nil {
		return fmt.Errorf("failed to create session_tasks table: %w", err)
	}

	// Create scheduled_task_reminders table
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS scheduled_task_reminders (
			id TEXT PRIMARY KEY,
			session_id TEXT,
			workspace_id TEXT,
			name TEXT NOT NULL,
			description TEXT DEFAULT '',
			schedule_type TEXT NOT NULL,
			execute_at DATETIME,
			time_of_day TEXT,
			day_of_week INTEGER,
			next_run DATETIME,
			last_run DATETIME,
			enabled INTEGER DEFAULT 1,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE,
			FOREIGN KEY (workspace_id) REFERENCES folders(id) ON DELETE CASCADE
		)
	`); err != nil {
		return fmt.Errorf("failed to create scheduled_task_reminders table: %w", err)
	}

	// Create indexes for performance
	taskIndexes := []string{
		"CREATE INDEX IF NOT EXISTS idx_session_tasks_session_id ON session_tasks(session_id)",
		"CREATE INDEX IF NOT EXISTS idx_session_tasks_workspace_id ON session_tasks(workspace_id)",
		"CREATE INDEX IF NOT EXISTS idx_session_tasks_status ON session_tasks(status)",
		"CREATE INDEX IF NOT EXISTS idx_session_tasks_created_at ON session_tasks(created_at DESC)",
		"CREATE INDEX IF NOT EXISTS idx_scheduled_reminders_session_id ON scheduled_task_reminders(session_id)",
		"CREATE INDEX IF NOT EXISTS idx_scheduled_reminders_workspace_id ON scheduled_task_reminders(workspace_id)",
		"CREATE INDEX IF NOT EXISTS idx_scheduled_reminders_next_run ON scheduled_task_reminders(next_run)",
		"CREATE INDEX IF NOT EXISTS idx_scheduled_reminders_enabled ON scheduled_task_reminders(enabled)",
	}

	for _, idx := range taskIndexes {
		if _, err := db.ExecContext(ctx, idx); err != nil {
			return fmt.Errorf("failed to create index: %w", err)
		}
	}

	return nil
}

// migration005TaskDetails adds a details column to session_tasks for additional task information.
func (db *DB) migration005TaskDetails(ctx context.Context) error {
	// Add details column to session_tasks
	if _, err := db.ExecContext(ctx, `
		ALTER TABLE session_tasks ADD COLUMN details TEXT DEFAULT ''
	`); err != nil {
		return fmt.Errorf("failed to add details column to session_tasks: %w", err)
	}

	return nil
}

// migration006TasksWorkspaceOnly removes session_id from tasks, making them workspace-scoped only.
// Tasks now belong to workspaces (folders) and can be viewed/executed from any session in that workspace.
// SQLite doesn't support DROP COLUMN on columns with foreign keys, so we recreate the tables.
func (db *DB) migration006TasksWorkspaceOnly(ctx context.Context) error {
	// Recreate session_tasks without session_id
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE session_tasks_new (
			id TEXT PRIMARY KEY,
			workspace_id TEXT,
			description TEXT NOT NULL,
			details TEXT DEFAULT '',
			status TEXT NOT NULL DEFAULT 'pending',
			priority INTEGER DEFAULT 3,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			completed_at DATETIME,
			FOREIGN KEY (workspace_id) REFERENCES folders(id) ON DELETE CASCADE
		)
	`); err != nil {
		return fmt.Errorf("failed to create new session_tasks table: %w", err)
	}

	// Copy data (workspace_id from session's folder_id if session_id was set)
	if _, err := db.ExecContext(ctx, `
		INSERT INTO session_tasks_new (id, workspace_id, description, details, status, priority, created_at, updated_at, completed_at)
		SELECT
			t.id,
			COALESCE(t.workspace_id, s.folder_id) as workspace_id,
			t.description,
			t.details,
			t.status,
			t.priority,
			t.created_at,
			t.updated_at,
			t.completed_at
		FROM session_tasks t
		LEFT JOIN sessions s ON t.session_id = s.id
	`); err != nil {
		return fmt.Errorf("failed to copy session_tasks data: %w", err)
	}

	// Drop old table and rename new one
	if _, err := db.ExecContext(ctx, `DROP TABLE session_tasks`); err != nil {
		return fmt.Errorf("failed to drop old session_tasks table: %w", err)
	}
	if _, err := db.ExecContext(ctx, `ALTER TABLE session_tasks_new RENAME TO session_tasks`); err != nil {
		return fmt.Errorf("failed to rename session_tasks table: %w", err)
	}

	// Recreate scheduled_task_reminders without session_id
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE scheduled_task_reminders_new (
			id TEXT PRIMARY KEY,
			workspace_id TEXT,
			name TEXT NOT NULL,
			description TEXT DEFAULT '',
			schedule_type TEXT NOT NULL,
			execute_at DATETIME,
			time_of_day TEXT,
			day_of_week INTEGER,
			next_run DATETIME,
			last_run DATETIME,
			enabled INTEGER DEFAULT 1,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			FOREIGN KEY (workspace_id) REFERENCES folders(id) ON DELETE CASCADE
		)
	`); err != nil {
		return fmt.Errorf("failed to create new scheduled_task_reminders table: %w", err)
	}

	// Copy data
	if _, err := db.ExecContext(ctx, `
		INSERT INTO scheduled_task_reminders_new (id, workspace_id, name, description, schedule_type, execute_at, time_of_day, day_of_week, next_run, last_run, enabled, created_at, updated_at)
		SELECT
			r.id,
			COALESCE(r.workspace_id, s.folder_id) as workspace_id,
			r.name,
			r.description,
			r.schedule_type,
			r.execute_at,
			r.time_of_day,
			r.day_of_week,
			r.next_run,
			r.last_run,
			r.enabled,
			r.created_at,
			r.updated_at
		FROM scheduled_task_reminders r
		LEFT JOIN sessions s ON r.session_id = s.id
	`); err != nil {
		return fmt.Errorf("failed to copy scheduled_task_reminders data: %w", err)
	}

	// Drop old table and rename new one
	if _, err := db.ExecContext(ctx, `DROP TABLE scheduled_task_reminders`); err != nil {
		return fmt.Errorf("failed to drop old scheduled_task_reminders table: %w", err)
	}
	if _, err := db.ExecContext(ctx, `ALTER TABLE scheduled_task_reminders_new RENAME TO scheduled_task_reminders`); err != nil {
		return fmt.Errorf("failed to rename scheduled_task_reminders table: %w", err)
	}

	// Create indexes
	if _, err := db.ExecContext(ctx, `
		CREATE INDEX IF NOT EXISTS idx_session_tasks_workspace_id ON session_tasks(workspace_id)
	`); err != nil {
		return fmt.Errorf("failed to create workspace_id index on session_tasks: %w", err)
	}

	if _, err := db.ExecContext(ctx, `
		CREATE INDEX IF NOT EXISTS idx_scheduled_reminders_workspace_id ON scheduled_task_reminders(workspace_id)
	`); err != nil {
		return fmt.Errorf("failed to create workspace_id index on scheduled_task_reminders: %w", err)
	}

	return nil
}
