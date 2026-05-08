package database

import (
	"context"
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/logger"
)

// schemaVersion is the current database schema version.
// Increment this when adding new migrations.
const schemaVersion = 15

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
	case 3:
		return db.migration003VaultTables(ctx)
	case 4:
		return db.migration004NamedVaults(ctx)
	case 5:
		return db.migration005RemoveEmptyDefaultVault(ctx)
	case 6:
		return db.migration006VaultPasswordKeys(ctx)
	case 7:
		return db.migration007WorkspaceKinds(ctx)
	case 8:
		return db.migration008WorkspaceSkillState(ctx)
	case 9:
		return db.migration009VaultRecordAttachments(ctx)
	case 10:
		return db.migration010VaultFolders(ctx)
	case 11:
		return db.migration011VaultCatalogFilePath(ctx)
	case 12:
		return db.migration012VaultCatalogOnly(ctx)
	case 13:
		return db.migration013RemoveLegacyVaultCatalogRows(ctx)
	case 14:
		return db.migration014WorkspaceNoteVaultReferences(ctx)
	case 15:
		return db.migration015WorkspaceVersion(ctx)
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
			kind TEXT DEFAULT 'workspace',
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
			skill_bindings_json TEXT DEFAULT '[]',
			agent_skill_access_json TEXT DEFAULT '[]',
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
				vault_reference_json TEXT DEFAULT '',
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

func (db *DB) migration003VaultTables(ctx context.Context) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS vault_records (
			id TEXT PRIMARY KEY,
			type TEXT NOT NULL,
			workspace_id TEXT DEFAULT '',
			source TEXT DEFAULT '',
			retention_policy TEXT DEFAULT '',
			metadata_nonce TEXT NOT NULL,
			metadata_ciphertext TEXT NOT NULL,
			payload_nonce TEXT NOT NULL,
			payload_ciphertext TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)
	`); err != nil {
		return fmt.Errorf("failed to create vault_records table: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS vault_grants (
			id TEXT PRIMARY KEY,
			workspace_id TEXT NOT NULL,
			actor_type TEXT NOT NULL,
			actor_id TEXT NOT NULL,
			capability TEXT NOT NULL,
			record_type TEXT NOT NULL DEFAULT '*',
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)
	`); err != nil {
		return fmt.Errorf("failed to create vault_grants table: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS vault_audit_events (
			id TEXT PRIMARY KEY,
			workspace_id TEXT DEFAULT '',
			actor_type TEXT DEFAULT '',
			actor_id TEXT DEFAULT '',
			action TEXT NOT NULL,
			record_id TEXT DEFAULT '',
			record_type TEXT DEFAULT '',
			outcome TEXT NOT NULL,
			details TEXT DEFAULT '',
			created_at DATETIME NOT NULL
		)
	`); err != nil {
		return fmt.Errorf("failed to create vault_audit_events table: %w", err)
	}

	indexes := []string{
		"CREATE INDEX IF NOT EXISTS idx_vault_records_workspace_id ON vault_records(workspace_id)",
		"CREATE INDEX IF NOT EXISTS idx_vault_records_type ON vault_records(type)",
		"CREATE INDEX IF NOT EXISTS idx_vault_records_updated_at ON vault_records(updated_at DESC)",
		"CREATE UNIQUE INDEX IF NOT EXISTS idx_vault_grants_scope ON vault_grants(workspace_id, actor_type, actor_id, capability, record_type)",
		"CREATE INDEX IF NOT EXISTS idx_vault_grants_workspace_id ON vault_grants(workspace_id)",
		"CREATE INDEX IF NOT EXISTS idx_vault_audit_workspace_created_at ON vault_audit_events(workspace_id, created_at DESC)",
		"CREATE INDEX IF NOT EXISTS idx_vault_audit_actor_created_at ON vault_audit_events(actor_type, actor_id, created_at DESC)",
	}

	for _, stmt := range indexes {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("failed to create vault index: %w", err)
		}
	}

	return tx.Commit()
}

func (db *DB) migration004NamedVaults(ctx context.Context) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS vaults (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			description TEXT DEFAULT '',
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)
	`); err != nil {
		return fmt.Errorf("failed to create vaults table: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		ALTER TABLE vault_records ADD COLUMN vault_id TEXT NOT NULL DEFAULT 'default'
	`); err != nil && !isDuplicateColumnError(err) {
		return fmt.Errorf("failed to add vault_id to vault_records: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		ALTER TABLE vault_grants ADD COLUMN vault_id TEXT NOT NULL DEFAULT 'default'
	`); err != nil && !isDuplicateColumnError(err) {
		return fmt.Errorf("failed to add vault_id to vault_grants: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		ALTER TABLE vault_audit_events ADD COLUMN vault_id TEXT NOT NULL DEFAULT 'default'
	`); err != nil && !isDuplicateColumnError(err) {
		return fmt.Errorf("failed to add vault_id to vault_audit_events: %w", err)
	}

	backfillStatements := []string{
		`UPDATE vault_records SET vault_id = 'default' WHERE TRIM(COALESCE(vault_id, '')) = ''`,
		`UPDATE vault_grants SET vault_id = 'default' WHERE TRIM(COALESCE(vault_id, '')) = ''`,
		`UPDATE vault_audit_events SET vault_id = 'default' WHERE TRIM(COALESCE(vault_id, '')) = ''`,
	}
	for _, stmt := range backfillStatements {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("failed to backfill named vault data: %w", err)
		}
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO vaults (id, name, description, created_at, updated_at)
		SELECT 'default', 'Private Vault', 'Default encrypted vault', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
		WHERE EXISTS (SELECT 1 FROM vault_records WHERE vault_id = 'default' LIMIT 1)
		   OR EXISTS (SELECT 1 FROM vault_grants WHERE vault_id = 'default' LIMIT 1)
		   OR EXISTS (SELECT 1 FROM vault_audit_events WHERE vault_id = 'default' LIMIT 1)
	`); err != nil {
		return fmt.Errorf("failed to seed legacy default vault: %w", err)
	}

	indexStatements := []string{
		"DROP INDEX IF EXISTS idx_vault_grants_scope",
		"CREATE UNIQUE INDEX IF NOT EXISTS idx_vaults_name ON vaults(name COLLATE NOCASE)",
		"CREATE INDEX IF NOT EXISTS idx_vaults_updated_at ON vaults(updated_at DESC)",
		"CREATE INDEX IF NOT EXISTS idx_vault_records_vault_id ON vault_records(vault_id)",
		"CREATE INDEX IF NOT EXISTS idx_vault_records_vault_updated_at ON vault_records(vault_id, updated_at DESC)",
		"CREATE UNIQUE INDEX IF NOT EXISTS idx_vault_grants_scope ON vault_grants(vault_id, workspace_id, actor_type, actor_id, capability, record_type)",
		"CREATE INDEX IF NOT EXISTS idx_vault_grants_vault_id ON vault_grants(vault_id)",
		"CREATE INDEX IF NOT EXISTS idx_vault_audit_vault_created_at ON vault_audit_events(vault_id, created_at DESC)",
	}
	for _, stmt := range indexStatements {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("failed to update named vault index: %w", err)
		}
	}

	return tx.Commit()
}

func (db *DB) migration005RemoveEmptyDefaultVault(ctx context.Context) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `
		DELETE FROM vaults
		WHERE id = 'default'
		  AND NOT EXISTS (SELECT 1 FROM vault_records WHERE vault_id = 'default')
		  AND NOT EXISTS (SELECT 1 FROM vault_grants WHERE vault_id = 'default')
		  AND NOT EXISTS (SELECT 1 FROM vault_audit_events WHERE vault_id = 'default')
	`); err != nil {
		return fmt.Errorf("failed to remove empty default vault: %w", err)
	}

	return tx.Commit()
}

func (db *DB) migration006VaultPasswordKeys(ctx context.Context) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	columnStatements := []string{
		`ALTER TABLE vaults ADD COLUMN key_salt TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE vaults ADD COLUMN key_nonce TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE vaults ADD COLUMN key_ciphertext TEXT NOT NULL DEFAULT ''`,
	}
	for _, stmt := range columnStatements {
		if _, err := tx.ExecContext(ctx, stmt); err != nil && !isDuplicateColumnError(err) {
			return fmt.Errorf("failed to add vault password key column: %w", err)
		}
	}

	return tx.Commit()
}

func (db *DB) migration007WorkspaceKinds(ctx context.Context) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `
		ALTER TABLE workspaces ADD COLUMN kind TEXT DEFAULT 'workspace'
	`); err != nil && !isDuplicateColumnError(err) {
		return fmt.Errorf("failed to add workspace kind column: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE workspaces
		SET kind = 'workspace'
		WHERE kind IS NULL OR TRIM(kind) = ''
	`); err != nil {
		return fmt.Errorf("failed to backfill workspace kinds: %w", err)
	}

	return tx.Commit()
}

func (db *DB) migration008WorkspaceSkillState(ctx context.Context) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `
		ALTER TABLE workspaces ADD COLUMN skill_bindings_json TEXT DEFAULT '[]'
	`); err != nil && !isDuplicateColumnError(err) {
		return fmt.Errorf("failed to add skill_bindings_json column: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		ALTER TABLE workspaces ADD COLUMN agent_skill_access_json TEXT DEFAULT '[]'
	`); err != nil && !isDuplicateColumnError(err) {
		return fmt.Errorf("failed to add agent_skill_access_json column: %w", err)
	}

	return tx.Commit()
}

func (db *DB) migration009VaultRecordAttachments(ctx context.Context) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS vault_record_attachments (
			id TEXT PRIMARY KEY,
			record_id TEXT NOT NULL,
			vault_id TEXT NOT NULL,
			metadata_nonce TEXT NOT NULL,
			metadata_ciphertext TEXT NOT NULL,
			data_nonce TEXT NOT NULL,
			data_ciphertext TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			FOREIGN KEY (record_id) REFERENCES vault_records(id) ON DELETE CASCADE,
			FOREIGN KEY (vault_id) REFERENCES vaults(id) ON DELETE CASCADE
		)
	`); err != nil {
		return fmt.Errorf("failed to create vault_record_attachments table: %w", err)
	}

	indexStatements := []string{
		"CREATE INDEX IF NOT EXISTS idx_vault_record_attachments_record_id ON vault_record_attachments(record_id)",
		"CREATE INDEX IF NOT EXISTS idx_vault_record_attachments_vault_id ON vault_record_attachments(vault_id)",
		"CREATE INDEX IF NOT EXISTS idx_vault_record_attachments_record_created_at ON vault_record_attachments(record_id, created_at ASC)",
	}
	for _, stmt := range indexStatements {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("failed to create vault attachment index: %w", err)
		}
	}

	return tx.Commit()
}

func (db *DB) migration010VaultFolders(ctx context.Context) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS vault_folders (
			id TEXT PRIMARY KEY,
			vault_id TEXT NOT NULL,
			path_hash TEXT NOT NULL,
			path_nonce TEXT NOT NULL,
			path_ciphertext TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			UNIQUE(vault_id, path_hash),
			FOREIGN KEY (vault_id) REFERENCES vaults(id) ON DELETE CASCADE
		)
	`); err != nil {
		return fmt.Errorf("failed to create vault_folders table: %w", err)
	}

	indexStatements := []string{
		"CREATE INDEX IF NOT EXISTS idx_vault_folders_vault_id ON vault_folders(vault_id)",
		"CREATE INDEX IF NOT EXISTS idx_vault_folders_vault_created_at ON vault_folders(vault_id, created_at ASC)",
	}
	for _, stmt := range indexStatements {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("failed to create vault folder index: %w", err)
		}
	}

	return tx.Commit()
}

func (db *DB) migration011VaultCatalogFilePath(ctx context.Context) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `
		ALTER TABLE vaults ADD COLUMN file_path TEXT NOT NULL DEFAULT ''
	`); err != nil && !isDuplicateColumnError(err) {
		return fmt.Errorf("failed to add file_path to vaults: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		CREATE UNIQUE INDEX IF NOT EXISTS idx_vaults_file_path
		ON vaults(file_path)
		WHERE TRIM(COALESCE(file_path, '')) <> ''
	`); err != nil {
		return fmt.Errorf("failed to create vault file path index: %w", err)
	}

	return tx.Commit()
}

func (db *DB) migration012VaultCatalogOnly(ctx context.Context) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	dropStatements := []string{
		`DROP TABLE IF EXISTS vault_record_attachments`,
		`DROP TABLE IF EXISTS vault_folders`,
		`DROP TABLE IF EXISTS vault_grants`,
		`DROP TABLE IF EXISTS vault_audit_events`,
		`DROP TABLE IF EXISTS vault_records`,
	}
	for _, stmt := range dropStatements {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("failed to drop legacy vault content table: %w", err)
		}
	}

	if _, err := tx.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS vaults_catalog_new (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			description TEXT DEFAULT '',
			file_path TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)
	`); err != nil {
		return fmt.Errorf("failed to create vaults catalog table: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO vaults_catalog_new (id, name, description, file_path, created_at, updated_at)
		SELECT id, name, description, COALESCE(file_path, ''), created_at, updated_at
		FROM vaults
	`); err != nil {
		return fmt.Errorf("failed to copy vault catalog rows: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `DROP TABLE IF EXISTS vaults`); err != nil {
		return fmt.Errorf("failed to drop legacy vaults table: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `ALTER TABLE vaults_catalog_new RENAME TO vaults`); err != nil {
		return fmt.Errorf("failed to rename vault catalog table: %w", err)
	}

	indexStatements := []string{
		"CREATE UNIQUE INDEX IF NOT EXISTS idx_vaults_name ON vaults(name COLLATE NOCASE)",
		"CREATE INDEX IF NOT EXISTS idx_vaults_updated_at ON vaults(updated_at DESC)",
		"CREATE UNIQUE INDEX IF NOT EXISTS idx_vaults_file_path ON vaults(file_path) WHERE TRIM(COALESCE(file_path, '')) <> ''",
	}
	for _, stmt := range indexStatements {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("failed to rebuild vault catalog index: %w", err)
		}
	}

	return tx.Commit()
}

func (db *DB) migration013RemoveLegacyVaultCatalogRows(ctx context.Context) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `
		DELETE FROM vaults
		WHERE TRIM(COALESCE(file_path, '')) = ''
	`); err != nil {
		return fmt.Errorf("failed to remove legacy vault catalog rows: %w", err)
	}

	return tx.Commit()
}

func (db *DB) migration015WorkspaceVersion(ctx context.Context) error {
	exists, err := db.tableExists(ctx, "workspaces")
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	if _, err := db.ExecContext(ctx, `
		ALTER TABLE workspaces ADD COLUMN version INTEGER NOT NULL DEFAULT 0
	`); err != nil && !isDuplicateColumnError(err) {
		return fmt.Errorf("failed to add workspace version column: %w", err)
	}
	return nil
}

func (db *DB) migration014WorkspaceNoteVaultReferences(ctx context.Context) error {
	exists, err := db.tableExists(ctx, "workspace_notes")
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	if _, err := db.ExecContext(ctx, `
		ALTER TABLE workspace_notes ADD COLUMN vault_reference_json TEXT DEFAULT ''
	`); err != nil && !isDuplicateColumnError(err) {
		return fmt.Errorf("failed to add workspace note vault reference column: %w", err)
	}
	return nil
}

func (db *DB) tableExists(ctx context.Context, tableName string) (bool, error) {
	var count int
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(1)
		FROM sqlite_master
		WHERE type = 'table' AND name = ?
	`, tableName).Scan(&count); err != nil {
		return false, fmt.Errorf("failed to check table %s: %w", tableName, err)
	}
	return count > 0, nil
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
