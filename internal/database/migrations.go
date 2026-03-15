package database

import (
	"context"
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/logger"
)

// schemaVersion is the current database schema version.
// Increment this when adding new migrations.
const schemaVersion = 2

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

	if currentVersion > schemaVersion {
		return fmt.Errorf("database schema version %d is newer than supported %d; reset the database or migrate manually", currentVersion, schemaVersion)
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
		return db.migration001Baseline(ctx)
	case 2:
		return db.migration002WorkspaceMCPState(ctx)
	default:
		return fmt.Errorf("unknown migration version: %d", version)
	}
}

// migration001Baseline creates the current database schema from scratch.
func (db *DB) migration001Baseline(ctx context.Context) error {
	// Core tables
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS workspaces (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			description TEXT DEFAULT '',
			parent_id TEXT,
			color TEXT,
			session_count INTEGER DEFAULT 0,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			agents TEXT DEFAULT '[]',
			agent_instances TEXT DEFAULT '[]',
			shared_data TEXT DEFAULT '{}',
			status TEXT DEFAULT 'active',
			layout TEXT,
			messages_json TEXT DEFAULT '[]',
			tasks_json TEXT DEFAULT '[]',
			attachments_json TEXT DEFAULT '[]',
			scheduled_tasks_json TEXT DEFAULT '[]',
			store_nodes_json TEXT DEFAULT '[]',
			workflows_json TEXT DEFAULT '{}',
			directory_references_json TEXT DEFAULT '[]',
			mcp_bindings_json TEXT DEFAULT '[]',
			agent_mcp_access_json TEXT DEFAULT '[]',
			order_index INTEGER DEFAULT 0,
			FOREIGN KEY (parent_id) REFERENCES workspaces(id) ON DELETE SET NULL
		)
	`); err != nil {
		return fmt.Errorf("failed to create workspaces table: %w", err)
	}

	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS sessions (
			id TEXT PRIMARY KEY,
			title TEXT NOT NULL,
			agent_name TEXT NOT NULL,
			workspace_id TEXT,
			message_count INTEGER DEFAULT 0,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE SET NULL
		)
	`); err != nil {
		return fmt.Errorf("failed to create sessions table: %w", err)
	}

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
			FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
		)
	`); err != nil {
		return fmt.Errorf("failed to create tool_calls table: %w", err)
	}

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

	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS session_tasks (
			id TEXT PRIMARY KEY,
			workspace_id TEXT,
			description TEXT NOT NULL,
			details TEXT DEFAULT '',
			status TEXT NOT NULL DEFAULT 'pending',
			priority INTEGER DEFAULT 3,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			completed_at DATETIME,
			FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE
		)
	`); err != nil {
		return fmt.Errorf("failed to create session_tasks table: %w", err)
	}

	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS scheduled_task_reminders (
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
			FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE
		)
	`); err != nil {
		return fmt.Errorf("failed to create scheduled_task_reminders table: %w", err)
	}

	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS smart_input_overrides (
			id TEXT PRIMARY KEY,
			workspace_id TEXT,
			input TEXT NOT NULL,
			predicted_decision TEXT NOT NULL,
			selected_decision TEXT NOT NULL,
			method TEXT NOT NULL,
			confidence REAL NOT NULL,
			created_at DATETIME NOT NULL
		)
	`); err != nil {
		return fmt.Errorf("failed to create smart_input_overrides table: %w", err)
	}

	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS workspace_notes (
			id TEXT PRIMARY KEY,
			workspace_id TEXT NOT NULL,
			name TEXT NOT NULL,
			content TEXT DEFAULT '',
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE
		)
	`); err != nil {
		return fmt.Errorf("failed to create workspace_notes table: %w", err)
	}

	// Indexes
	indexes := []string{
		// Sessions indexes
		"CREATE INDEX IF NOT EXISTS idx_sessions_agent_name ON sessions(agent_name)",
		"CREATE INDEX IF NOT EXISTS idx_sessions_workspace_id ON sessions(workspace_id)",
		"CREATE INDEX IF NOT EXISTS idx_sessions_updated_at ON sessions(updated_at DESC)",
		"CREATE INDEX IF NOT EXISTS idx_sessions_created_at ON sessions(created_at DESC)",

		// Messages indexes
		"CREATE INDEX IF NOT EXISTS idx_messages_session_id ON messages(session_id)",
		"CREATE INDEX IF NOT EXISTS idx_messages_created_at ON messages(created_at)",

		// Tags indexes
		"CREATE INDEX IF NOT EXISTS idx_session_tags_tag ON session_tags(tag)",

		// Workspaces indexes
		"CREATE INDEX IF NOT EXISTS idx_workspaces_parent_id ON workspaces(parent_id)",
		"CREATE INDEX IF NOT EXISTS idx_workspaces_status ON workspaces(status)",
		"CREATE INDEX IF NOT EXISTS idx_workspaces_parent_order ON workspaces(parent_id, order_index)",

		// Tool calls indexes
		"CREATE INDEX IF NOT EXISTS idx_tool_calls_session_id ON tool_calls(session_id)",
		"CREATE INDEX IF NOT EXISTS idx_tool_calls_tool_name ON tool_calls(tool_name)",
		"CREATE INDEX IF NOT EXISTS idx_tool_calls_message_id ON tool_calls(message_id)",

		// Review indexes
		"CREATE INDEX IF NOT EXISTS idx_review_issues_session_id ON review_issues(session_id)",
		"CREATE INDEX IF NOT EXISTS idx_review_issues_issue_type ON review_issues(issue_type)",
		"CREATE INDEX IF NOT EXISTS idx_review_issues_agent_name ON review_issues(agent_name)",
		"CREATE INDEX IF NOT EXISTS idx_review_runs_status ON review_runs(status)",

		// Task indexes
		"CREATE INDEX IF NOT EXISTS idx_session_tasks_workspace_id ON session_tasks(workspace_id)",
		"CREATE INDEX IF NOT EXISTS idx_scheduled_reminders_workspace_id ON scheduled_task_reminders(workspace_id)",

		// Smart input overrides indexes
		"CREATE INDEX IF NOT EXISTS idx_smart_input_overrides_workspace_id ON smart_input_overrides(workspace_id)",
		"CREATE INDEX IF NOT EXISTS idx_smart_input_overrides_created_at ON smart_input_overrides(created_at DESC)",
		"CREATE INDEX IF NOT EXISTS idx_smart_input_overrides_predicted ON smart_input_overrides(predicted_decision)",

		// Workspace notes indexes
		"CREATE INDEX IF NOT EXISTS idx_workspace_notes_workspace_id ON workspace_notes(workspace_id)",
		"CREATE INDEX IF NOT EXISTS idx_workspace_notes_updated_at ON workspace_notes(updated_at DESC)",
		"CREATE INDEX IF NOT EXISTS idx_workspace_notes_name ON workspace_notes(name)",
	}

	for _, idx := range indexes {
		if _, err := db.ExecContext(ctx, idx); err != nil {
			return fmt.Errorf("failed to create index: %w", err)
		}
	}

	// FTS tables
	if _, err := db.ExecContext(ctx, `
		CREATE VIRTUAL TABLE IF NOT EXISTS sessions_fts USING fts5(
			session_id,
			title,
			content,
			tokenize='porter unicode61'
		)
	`); err != nil {
		return fmt.Errorf("failed to create sessions FTS table: %w", err)
	}

	if _, err := db.ExecContext(ctx, `
		CREATE VIRTUAL TABLE IF NOT EXISTS workspace_notes_fts USING fts5(
			note_id,
			name,
			content,
			tokenize='porter unicode61'
		)
	`); err != nil {
		return fmt.Errorf("failed to create workspace notes FTS table: %w", err)
	}

	// FTS triggers
	sessionTriggers := []string{
		`CREATE TRIGGER IF NOT EXISTS sessions_ai AFTER INSERT ON sessions BEGIN
			INSERT INTO sessions_fts(session_id, title, content) VALUES (new.id, new.title, '');
		END`,
		`CREATE TRIGGER IF NOT EXISTS sessions_au AFTER UPDATE OF title ON sessions BEGIN
			UPDATE sessions_fts SET title = new.title WHERE session_id = old.id;
		END`,
		`CREATE TRIGGER IF NOT EXISTS sessions_ad AFTER DELETE ON sessions BEGIN
			DELETE FROM sessions_fts WHERE session_id = old.id;
		END`,
	}

	for _, trigger := range sessionTriggers {
		if _, err := db.ExecContext(ctx, trigger); err != nil {
			return fmt.Errorf("failed to create sessions trigger: %w", err)
		}
	}

	noteTriggers := []string{
		`CREATE TRIGGER IF NOT EXISTS workspace_notes_ai AFTER INSERT ON workspace_notes BEGIN
			INSERT INTO workspace_notes_fts(note_id, name, content) VALUES (new.id, new.name, new.content);
		END`,
		`CREATE TRIGGER IF NOT EXISTS workspace_notes_au AFTER UPDATE ON workspace_notes BEGIN
			UPDATE workspace_notes_fts SET name = new.name, content = new.content WHERE note_id = old.id;
		END`,
		`CREATE TRIGGER IF NOT EXISTS workspace_notes_ad AFTER DELETE ON workspace_notes BEGIN
			DELETE FROM workspace_notes_fts WHERE note_id = old.id;
		END`,
	}

	for _, trigger := range noteTriggers {
		if _, err := db.ExecContext(ctx, trigger); err != nil {
			return fmt.Errorf("failed to create workspace notes trigger: %w", err)
		}
	}

	// Views
	if _, err := db.ExecContext(ctx, `
		CREATE VIEW IF NOT EXISTS tag_counts AS
		SELECT tag as name, COUNT(*) as usage_count
		FROM session_tags
		GROUP BY tag
		ORDER BY usage_count DESC
	`); err != nil {
		return fmt.Errorf("failed to create tag_counts view: %w", err)
	}

	return nil
}

func (db *DB) migration002WorkspaceMCPState(ctx context.Context) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `
		ALTER TABLE workspaces ADD COLUMN mcp_bindings_json TEXT DEFAULT '[]'
	`); err != nil && !isDuplicateColumnError(err) {
		return fmt.Errorf("failed to add mcp_bindings_json column: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		ALTER TABLE workspaces ADD COLUMN agent_mcp_access_json TEXT DEFAULT '[]'
	`); err != nil && !isDuplicateColumnError(err) {
		return fmt.Errorf("failed to add agent_mcp_access_json column: %w", err)
	}

	return tx.Commit()
}

func isDuplicateColumnError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "duplicate column name")
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
