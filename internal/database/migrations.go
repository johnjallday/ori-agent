package database

import (
	"context"
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/logger"
)

// schemaVersion is the current database schema version.
// Increment this when adding new migrations.
const schemaVersion = 33

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
	case 16:
		return db.migration016NoteHeadingsIndex(ctx)
	case 17:
		return db.migration017NoteLinks(ctx)
	case 18:
		return db.migration018WorkspaceRuns(ctx)
	case 19:
		return db.migration019WorkspaceRunContext(ctx)
	case 20:
		return db.migration020HomeAssistantIntakeTraces(ctx)
	case 21:
		return db.migration021WorkspaceRunTaskOutput(ctx)
	case 22:
		return db.migration022WorkspaceFolders(ctx)
	case 23:
		return db.migration023WorkspaceTrash(ctx)
	case 24:
		return db.migration024WorkspaceRunReferenceURL(ctx)
	case 25:
		return db.migration025WorkspaceOpportunities(ctx)
	case 26:
		return db.migration026WorkspaceTags(ctx)
	case 27:
		return db.migration027NoteTags(ctx)
	case 28:
		return db.migration028Users(ctx)
	case 29:
		return db.migration029WorkspaceNativeMCPOptIn(ctx)
	case 30:
		return db.migration030PersonalHQ(ctx)
	case 31:
		return db.migration031DailyBrief(ctx)
	case 32:
		return db.migration032FollowUps(ctx)
	case 33:
		return db.migration033CalendarMeetingPreps(ctx)
	default:
		return fmt.Errorf("unknown migration version: %d", version)
	}
}

// migration001Baseline creates the current database schema from scratch.
func (db *DB) migration001Baseline(ctx context.Context) error {
	// Core tables
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS users (
			id TEXT PRIMARY KEY,
			display_name TEXT NOT NULL DEFAULT '',
			email TEXT NOT NULL DEFAULT '',
			timezone TEXT NOT NULL DEFAULT '',
			locale TEXT NOT NULL DEFAULT '',
			role_category TEXT NOT NULL DEFAULT '',
			specializations TEXT NOT NULL DEFAULT '[]',
			preferences TEXT NOT NULL DEFAULT '{}',
			about TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)
	`); err != nil {
		return fmt.Errorf("failed to create users table: %w", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO users (id, created_at, updated_at)
		VALUES ('local', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO NOTHING
	`); err != nil {
		return fmt.Errorf("failed to seed local user row: %w", err)
	}

	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS workspaces (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			kind TEXT DEFAULT 'workspace',
			description TEXT DEFAULT '',
			tags TEXT DEFAULT '[]',
			owner_user_id TEXT NOT NULL DEFAULT 'local',
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
			folders_json TEXT DEFAULT '[]',
			scheduled_tasks_json TEXT DEFAULT '[]',
			store_nodes_json TEXT DEFAULT '[]',
			workflows_json TEXT DEFAULT '{}',
			directory_references_json TEXT DEFAULT '[]',
			mcp_bindings_json TEXT DEFAULT '[]',
			agent_mcp_access_json TEXT DEFAULT '[]',
			skill_bindings_json TEXT DEFAULT '[]',
			agent_skill_access_json TEXT DEFAULT '[]',
			order_index INTEGER DEFAULT 0,
			FOREIGN KEY (owner_user_id) REFERENCES users(id) ON DELETE RESTRICT,
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
		"CREATE INDEX IF NOT EXISTS idx_workspaces_owner_user_id ON workspaces(owner_user_id)",

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

// migration017NoteLinks creates the note_links table that powers wikilinks
// (`[[Other Note]]` references) and the Backlinks panel. Each row is one
// outbound link from a note. target_note_id is nullable for broken links —
// references to titles that don't currently match any note.
func (db *DB) migration017NoteLinks(ctx context.Context) error {
	exists, err := db.tableExists(ctx, "workspace_notes")
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}

	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS note_links (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			source_note_id TEXT NOT NULL,
			target_note_id TEXT,
			target_text TEXT NOT NULL,
			display_text TEXT,
			position INTEGER NOT NULL,
			FOREIGN KEY (source_note_id) REFERENCES workspace_notes(id) ON DELETE CASCADE,
			FOREIGN KEY (target_note_id) REFERENCES workspace_notes(id) ON DELETE SET NULL
		)
	`); err != nil {
		return fmt.Errorf("failed to create note_links table: %w", err)
	}

	indexes := []string{
		"CREATE INDEX IF NOT EXISTS idx_note_links_source ON note_links(source_note_id)",
		"CREATE INDEX IF NOT EXISTS idx_note_links_target ON note_links(target_note_id)",
		"CREATE INDEX IF NOT EXISTS idx_note_links_target_text ON note_links(target_text)",
	}
	for _, stmt := range indexes {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("failed to create note_links index: %w", err)
		}
	}
	return nil
}

// migration018WorkspaceRuns creates durable storage for workspace-scoped harness runs.
func (db *DB) migration018WorkspaceRuns(ctx context.Context) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS workspace_runs (
			id TEXT PRIMARY KEY,
			workspace_id TEXT NOT NULL,
			parent_run_id TEXT,
			profile_id TEXT NOT NULL,
			profile_version TEXT NOT NULL,
			profile_snapshot_json TEXT NOT NULL,
			executor_json TEXT NOT NULL,
			scope_json TEXT NOT NULL,
			policy_json TEXT NOT NULL,
			environment_json TEXT NOT NULL,
			prompt TEXT NOT NULL,
			status TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			started_at DATETIME,
			finished_at DATETIME,
			validation_request_json TEXT,
			validation_result_json TEXT,
			cost_json TEXT,
			report_json TEXT,
			error TEXT,
			updated_at DATETIME NOT NULL,
			FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS workspace_run_trace (
			id TEXT PRIMARY KEY,
			workspace_id TEXT NOT NULL,
			run_id TEXT NOT NULL,
			sequence INTEGER NOT NULL,
			kind TEXT NOT NULL,
			source TEXT,
			message TEXT,
			status TEXT,
			tool_name TEXT,
			artifact_id TEXT,
			data_json TEXT,
			created_at DATETIME NOT NULL,
			UNIQUE(run_id, sequence),
			FOREIGN KEY (run_id) REFERENCES workspace_runs(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS workspace_run_artifacts (
			id TEXT PRIMARY KEY,
			workspace_id TEXT NOT NULL,
			run_id TEXT NOT NULL,
			kind TEXT NOT NULL,
			path TEXT,
			inline BLOB,
			metadata_json TEXT,
			created_at DATETIME NOT NULL,
			FOREIGN KEY (run_id) REFERENCES workspace_runs(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_workspace_runs_workspace_created ON workspace_runs(workspace_id, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_workspace_runs_status ON workspace_runs(status)`,
		`CREATE INDEX IF NOT EXISTS idx_workspace_run_trace_run_sequence ON workspace_run_trace(run_id, sequence)`,
		`CREATE INDEX IF NOT EXISTS idx_workspace_run_artifacts_run ON workspace_run_artifacts(run_id)`,
	}
	for _, stmt := range statements {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("failed to create workspace run schema: %w", err)
		}
	}
	return nil
}

// migration019WorkspaceRunContext adds persisted task context planning and output.
func (db *DB) migration019WorkspaceRunContext(ctx context.Context) error {
	statements := []string{
		`ALTER TABLE workspace_runs ADD COLUMN context_plan_json TEXT NOT NULL DEFAULT '{}'`,
		`ALTER TABLE workspace_runs ADD COLUMN prepared_context_json TEXT`,
	}
	for _, stmt := range statements {
		// Tolerate duplicate columns so a partially-applied migration can
		// re-run, matching every other ALTER TABLE migration in this file.
		if _, err := db.ExecContext(ctx, stmt); err != nil && !isDuplicateColumnError(err) {
			return fmt.Errorf("failed to extend workspace run context schema: %w", err)
		}
	}
	return nil
}

// migration020HomeAssistantIntakeTraces persists home context-routing outcomes
// so routing quality can be evaluated beyond process logs.
func (db *DB) migration020HomeAssistantIntakeTraces(ctx context.Context) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS home_assistant_intake_traces (
			id TEXT PRIMARY KEY,
			prompt TEXT NOT NULL,
			intent TEXT,
			intent_variant TEXT,
			routing_policy TEXT,
			context_mode TEXT,
			handoff_policy TEXT,
			route_mode TEXT,
			target_surface TEXT,
			matched_agent TEXT,
			workspace_state TEXT,
			selected_workspace_id TEXT,
			selected_workspace_name TEXT,
			final_workspace_id TEXT,
			confidence REAL NOT NULL DEFAULT 0,
			reasons_json TEXT NOT NULL DEFAULT '[]',
			candidates_json TEXT NOT NULL DEFAULT '[]',
			user_override INTEGER NOT NULL DEFAULT 0,
			final_handoff_target TEXT NOT NULL,
			route_context_json TEXT,
			created_at DATETIME NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_home_assistant_intake_traces_created_at
			ON home_assistant_intake_traces(created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_home_assistant_intake_traces_workspace_state
			ON home_assistant_intake_traces(workspace_state)`,
		`CREATE INDEX IF NOT EXISTS idx_home_assistant_intake_traces_final_workspace
			ON home_assistant_intake_traces(final_workspace_id)`,
	}
	for _, stmt := range statements {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("failed to create home assistant intake trace schema: %w", err)
		}
	}
	return nil
}

// migration021WorkspaceRunTaskOutput stores task-level output validation
// summaries on durable workspace runs without storing raw task output.
func (db *DB) migration021WorkspaceRunTaskOutput(ctx context.Context) error {
	if _, err := db.ExecContext(ctx, `ALTER TABLE workspace_runs ADD COLUMN task_output_json TEXT`); err != nil {
		return fmt.Errorf("failed to extend workspace run task output schema: %w", err)
	}
	return nil
}

// migration022WorkspaceFolders persists managed workspace file folders.
func (db *DB) migration022WorkspaceFolders(ctx context.Context) error {
	exists, err := db.tableExists(ctx, "workspaces")
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	if _, err := db.ExecContext(ctx, `
		ALTER TABLE workspaces ADD COLUMN folders_json TEXT DEFAULT '[]'
	`); err != nil && !isDuplicateColumnError(err) {
		return fmt.Errorf("failed to add workspace folders column: %w", err)
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE workspaces
		SET folders_json = '[]'
		WHERE folders_json IS NULL OR TRIM(folders_json) = ''
	`); err != nil {
		return fmt.Errorf("failed to backfill workspace folders: %w", err)
	}
	return nil
}

// migration023WorkspaceTrash adds a nullable deleted_at column. The #50 Trash
// feature that used it was reverted (#54), but the additive, idempotent column
// migration is retained so schemaVersion stays monotonic — databases that already
// applied v23 under #50 upgrade forward cleanly instead of being rejected as
// "newer than supported". The column is otherwise unused.
func (db *DB) migration023WorkspaceTrash(ctx context.Context) error {
	exists, err := db.tableExists(ctx, "workspaces")
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	if _, err := db.ExecContext(ctx, `
		ALTER TABLE workspaces ADD COLUMN deleted_at DATETIME
	`); err != nil && !isDuplicateColumnError(err) {
		return fmt.Errorf("failed to add workspace deleted_at column: %w", err)
	}
	return nil
}

// migration024WorkspaceRunReferenceURL stores the effective reference URL used
// by a durable workspace run.
func (db *DB) migration024WorkspaceRunReferenceURL(ctx context.Context) error {
	exists, err := db.tableExists(ctx, "workspace_runs")
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	if _, err := db.ExecContext(ctx, `
		ALTER TABLE workspace_runs ADD COLUMN reference_url TEXT NOT NULL DEFAULT ''
	`); err != nil && !isDuplicateColumnError(err) {
		return fmt.Errorf("failed to add workspace run reference URL column: %w", err)
	}
	return nil
}

// migration025WorkspaceOpportunities adds the opportunities_json column so
// mission findings (Action Center opportunities) persist in the primary store.
// Before this, the session adapter never serialized Opportunities, so findings
// were dropped on every read and lost on restart.
func (db *DB) migration025WorkspaceOpportunities(ctx context.Context) error {
	exists, err := db.tableExists(ctx, "workspaces")
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	if _, err := db.ExecContext(ctx, `
		ALTER TABLE workspaces ADD COLUMN opportunities_json TEXT DEFAULT '[]'
	`); err != nil && !isDuplicateColumnError(err) {
		return fmt.Errorf("failed to add workspace opportunities_json column: %w", err)
	}
	return nil
}

// migration026WorkspaceTags stores workspace organization tags in SQLite for
// workspaces that are not backed by a portable workspace.json file.
func (db *DB) migration026WorkspaceTags(ctx context.Context) error {
	exists, err := db.tableExists(ctx, "workspaces")
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	if _, err := db.ExecContext(ctx, `
		ALTER TABLE workspaces ADD COLUMN tags TEXT DEFAULT '[]'
	`); err != nil && !isDuplicateColumnError(err) {
		return fmt.Errorf("failed to add workspace tags column: %w", err)
	}
	return nil
}

// migration029WorkspaceNativeMCPOptIn stores the per-workspace opt-in that lets
// CLI-provider agents run MCP/built-in tools natively (mirrors workspace.json's
// allow_native_mcp_cli into SQLite so the primary store round-trips it).
func (db *DB) migration029WorkspaceNativeMCPOptIn(ctx context.Context) error {
	exists, err := db.tableExists(ctx, "workspaces")
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	if _, err := db.ExecContext(ctx, `
		ALTER TABLE workspaces ADD COLUMN allow_native_mcp_cli INTEGER NOT NULL DEFAULT 0
	`); err != nil && !isDuplicateColumnError(err) {
		return fmt.Errorf("failed to add workspace allow_native_mcp_cli column: %w", err)
	}
	return nil
}

// migration031DailyBrief creates the Daily Brief domain tables: HQ-owned
// configuration, generation claims (in-flight/idempotency tracking),
// revisions (the generated documents, with at most one current per
// workspace), and notification records (at most one Action Center
// notification per revision). Keyed by workspace_id (the designated HQ),
// not just user_id, so replacing or clearing an HQ never carries
// configuration or history onto a different workspace.
// migration032FollowUps creates the dedicated structured follow-up domain
// (contract §2): personal commitments/dependencies with their own lifecycle and
// source-based deduplication — deliberately NOT reusing Action Center
// opportunities (which are title-deduped mission findings).
func (db *DB) migration032FollowUps(ctx context.Context) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS personal_hq_followup (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL,
			workspace_id TEXT NOT NULL,
			category TEXT NOT NULL,
			direction TEXT NOT NULL DEFAULT 'none',
			title TEXT NOT NULL,
			detail TEXT NOT NULL DEFAULT '',
			counterparty TEXT NOT NULL DEFAULT '',
			source_type TEXT NOT NULL DEFAULT 'manual',
			source_id TEXT NOT NULL DEFAULT '',
			source_account_id TEXT NOT NULL DEFAULT '',
			dedup_key TEXT NOT NULL DEFAULT '',
			provenance TEXT NOT NULL DEFAULT 'manual',
			confidence TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'active',
			due_at DATETIME,
			snoozed_until DATETIME,
			last_nudged_at DATETIME,
			related_workspace_id TEXT NOT NULL DEFAULT '',
			related_task_id TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			completed_at DATETIME,
			dismissed_at DATETIME
		)`,
		// Source-based dedup: a sourced follow-up is unique per user+dedup_key,
		// enforced at the DB boundary so reprocessing the same thread can never
		// create a duplicate. Manual items (empty dedup_key) are exempt.
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_personal_hq_followup_dedup
			ON personal_hq_followup(user_id, dedup_key)
			WHERE dedup_key != ''`,
		`CREATE INDEX IF NOT EXISTS idx_personal_hq_followup_user_status ON personal_hq_followup(user_id, status)`,
		`CREATE INDEX IF NOT EXISTS idx_personal_hq_followup_due ON personal_hq_followup(user_id, status, due_at)`,
		`CREATE INDEX IF NOT EXISTS idx_personal_hq_followup_updated ON personal_hq_followup(user_id, status, updated_at)`,
	}
	for _, stmt := range statements {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("failed to create follow-up schema: %w", err)
		}
	}
	return nil
}

// migration033CalendarMeetingPreps creates the durable link between a
// Calendar Ops event and the Calendar Ops note prepared for it: workspace +
// binding + calendar + event identifies the meeting uniquely (the same raw
// event id can otherwise collide across different calendars/bindings); the
// row stores only the linked note id, the last normalized event fingerprint,
// and run status -- never the event body itself, which stays a live read
// through the gateway rather than a cache.
func (db *DB) migration033CalendarMeetingPreps(ctx context.Context) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS calendar_meeting_prep (
			id TEXT PRIMARY KEY,
			workspace_id TEXT NOT NULL,
			binding_id TEXT NOT NULL,
			calendar_id TEXT NOT NULL,
			event_id TEXT NOT NULL,
			note_id TEXT NOT NULL DEFAULT '',
			event_fingerprint TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'pending',
			task_id TEXT NOT NULL DEFAULT '',
			error TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		// One link per (workspace, binding, calendar, event) -- the natural key
		// this feature upserts against on every "Prepare me" run.
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_calendar_meeting_prep_key
			ON calendar_meeting_prep(workspace_id, binding_id, calendar_id, event_id)`,
	}
	for _, stmt := range statements {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("failed to create calendar meeting prep schema: %w", err)
		}
	}
	return nil
}

func (db *DB) migration031DailyBrief(ctx context.Context) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS daily_brief_config (
			workspace_id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL,
			timezone TEXT NOT NULL,
			schedule_days TEXT NOT NULL DEFAULT '[]',
			schedule_time TEXT NOT NULL DEFAULT '08:00',
			schedule_enabled INTEGER NOT NULL DEFAULT 1,
			scope TEXT NOT NULL DEFAULT 'all',
			selected_workspace_ids TEXT NOT NULL DEFAULT '[]',
			include_future_workspaces INTEGER NOT NULL DEFAULT 1,
			notify_on_ready INTEGER NOT NULL DEFAULT 0,
			config_revision INTEGER NOT NULL DEFAULT 1,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS daily_brief_revision (
			id TEXT PRIMARY KEY,
			workspace_id TEXT NOT NULL,
			user_id TEXT NOT NULL,
			local_date TEXT NOT NULL,
			revision_number INTEGER NOT NULL,
			is_current INTEGER NOT NULL DEFAULT 0,
			trigger_type TEXT NOT NULL,
			status TEXT NOT NULL,
			config_revision INTEGER NOT NULL DEFAULT 0,
			content_json TEXT NOT NULL DEFAULT '',
			source_window_start DATETIME,
			source_window_end DATETIME,
			failure_reason TEXT NOT NULL DEFAULT '',
			generated_at DATETIME,
			created_at DATETIME NOT NULL,
			UNIQUE(workspace_id, local_date, revision_number)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_daily_brief_revision_workspace_date ON daily_brief_revision(workspace_id, local_date)`,
		// At most one current revision per workspace, enforced at the
		// database boundary (not just in application code).
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_daily_brief_revision_current ON daily_brief_revision(workspace_id) WHERE is_current = 1`,
		`CREATE TABLE IF NOT EXISTS daily_brief_generation_claim (
			id TEXT PRIMARY KEY,
			workspace_id TEXT NOT NULL,
			local_date TEXT NOT NULL,
			trigger_type TEXT NOT NULL,
			status TEXT NOT NULL,
			revision_id TEXT NOT NULL DEFAULT '',
			error TEXT NOT NULL DEFAULT '',
			claimed_at DATETIME NOT NULL,
			finished_at DATETIME
		)`,
		// Deduplicates first-open/scheduled triggers (never manual, which may
		// always create a new same-day revision) against each other for the
		// same workspace/local-date, so at most one non-manual claim is ever
		// in flight or has already succeeded for that date. A failed claim
		// does not block a later retry.
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_daily_brief_claim_dedupe
			ON daily_brief_generation_claim(workspace_id, local_date)
			WHERE trigger_type != 'manual' AND status != 'failed'`,
		`CREATE INDEX IF NOT EXISTS idx_daily_brief_claim_workspace_date ON daily_brief_generation_claim(workspace_id, local_date)`,
		`CREATE TABLE IF NOT EXISTS daily_brief_notification (
			revision_id TEXT PRIMARY KEY,
			workspace_id TEXT NOT NULL,
			notified_at DATETIME NOT NULL
		)`,
	}
	for _, stmt := range statements {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("failed to create daily brief schema: %w", err)
		}
	}
	return nil
}

// migration030PersonalHQ adds the per-user Personal HQ designation and
// onboarding-status columns to the users table. The designation
// (personal_workspace_id) and the onboarding status are separate columns on
// purpose: clearing or losing the designated workspace must never reset the
// user's onboarding history (unseen/in_progress/completed/skipped).
func (db *DB) migration030PersonalHQ(ctx context.Context) error {
	exists, err := db.tableExists(ctx, "users")
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	if _, err := db.ExecContext(ctx, `
		ALTER TABLE users ADD COLUMN personal_workspace_id TEXT NOT NULL DEFAULT ''
	`); err != nil && !isDuplicateColumnError(err) {
		return fmt.Errorf("failed to add users.personal_workspace_id column: %w", err)
	}
	if _, err := db.ExecContext(ctx, `
		ALTER TABLE users ADD COLUMN hq_onboarding_state TEXT NOT NULL DEFAULT 'unseen'
	`); err != nil && !isDuplicateColumnError(err) {
		return fmt.Errorf("failed to add users.hq_onboarding_state column: %w", err)
	}
	if _, err := db.ExecContext(ctx, `
		ALTER TABLE users ADD COLUMN hq_onboarding_updated_at DATETIME
	`); err != nil && !isDuplicateColumnError(err) {
		return fmt.Errorf("failed to add users.hq_onboarding_updated_at column: %w", err)
	}
	return nil
}

// migration027NoteTags stores workspace note tags, mirroring the session_tags
// layout so per-tag usage counting stays a cheap GROUP BY.
func (db *DB) migration027NoteTags(ctx context.Context) error {
	exists, err := db.tableExists(ctx, "workspace_notes")
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS note_tags (
			note_id TEXT NOT NULL,
			tag TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (note_id, tag),
			FOREIGN KEY (note_id) REFERENCES workspace_notes(id) ON DELETE CASCADE
		)
	`); err != nil {
		return fmt.Errorf("failed to create note_tags table: %w", err)
	}
	if _, err := db.ExecContext(ctx, `
		CREATE INDEX IF NOT EXISTS idx_note_tags_tag ON note_tags(tag)
	`); err != nil {
		return fmt.Errorf("failed to create note_tags index: %w", err)
	}
	return nil
}

// migration028Users creates the user profile table and records workspace ownership.
func (db *DB) migration028Users(ctx context.Context) error {
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS users (
			id TEXT PRIMARY KEY,
			display_name TEXT NOT NULL DEFAULT '',
			email TEXT NOT NULL DEFAULT '',
			timezone TEXT NOT NULL DEFAULT '',
			locale TEXT NOT NULL DEFAULT '',
			role_category TEXT NOT NULL DEFAULT '',
			specializations TEXT NOT NULL DEFAULT '[]',
			preferences TEXT NOT NULL DEFAULT '{}',
			about TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)
	`); err != nil {
		return fmt.Errorf("failed to create users table: %w", err)
	}

	if _, err := db.ExecContext(ctx, `
		INSERT INTO users (id, created_at, updated_at)
		VALUES ('local', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO NOTHING
	`); err != nil {
		return fmt.Errorf("failed to seed local user row: %w", err)
	}

	exists, err := db.tableExists(ctx, "workspaces")
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	if _, err := db.ExecContext(ctx, `
		ALTER TABLE workspaces ADD COLUMN owner_user_id TEXT NOT NULL DEFAULT 'local'
	`); err != nil && !isDuplicateColumnError(err) {
		return fmt.Errorf("failed to add workspace owner column: %w", err)
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE workspaces
		SET owner_user_id = 'local'
		WHERE TRIM(COALESCE(owner_user_id, '')) = ''
	`); err != nil {
		return fmt.Errorf("failed to backfill workspace owner user ids: %w", err)
	}
	if _, err := db.ExecContext(ctx, `
		CREATE INDEX IF NOT EXISTS idx_workspaces_owner_user_id ON workspaces(owner_user_id)
	`); err != nil {
		return fmt.Errorf("failed to create workspace owner index: %w", err)
	}
	return nil
}

// migration016NoteHeadingsIndex creates the note_headings table, its FTS5 mirror,
// and the delete trigger that keeps the FTS in sync. Backfill of existing notes
// happens at runtime in session.NewHybridStore (database cannot import session).
func (db *DB) migration016NoteHeadingsIndex(ctx context.Context) error {
	exists, err := db.tableExists(ctx, "workspace_notes")
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}

	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS note_headings (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			note_id TEXT NOT NULL,
			level INTEGER NOT NULL,
			text TEXT NOT NULL,
			position INTEGER NOT NULL,
			FOREIGN KEY (note_id) REFERENCES workspace_notes(id) ON DELETE CASCADE
		)
	`); err != nil {
		return fmt.Errorf("failed to create note_headings table: %w", err)
	}

	if _, err := db.ExecContext(ctx, `
		CREATE INDEX IF NOT EXISTS idx_note_headings_note_id ON note_headings(note_id)
	`); err != nil {
		return fmt.Errorf("failed to create note_headings index: %w", err)
	}

	if _, err := db.ExecContext(ctx, `
		CREATE VIRTUAL TABLE IF NOT EXISTS note_headings_fts USING fts5(
			text,
			note_id UNINDEXED,
			tokenize='porter unicode61'
		)
	`); err != nil {
		return fmt.Errorf("failed to create note_headings FTS table: %w", err)
	}

	// Triggers: insert/delete pairs on note_headings keep note_headings_fts in sync.
	// Updates are not needed because callers always delete-then-insert per note.
	headingTriggers := []string{
		`CREATE TRIGGER IF NOT EXISTS note_headings_ai AFTER INSERT ON note_headings BEGIN
			INSERT INTO note_headings_fts(rowid, text, note_id) VALUES (new.id, new.text, new.note_id);
		END`,
		`CREATE TRIGGER IF NOT EXISTS note_headings_ad AFTER DELETE ON note_headings BEGIN
			DELETE FROM note_headings_fts WHERE rowid = old.id;
		END`,
	}
	for _, trigger := range headingTriggers {
		if _, err := db.ExecContext(ctx, trigger); err != nil {
			return fmt.Errorf("failed to create note_headings trigger: %w", err)
		}
	}

	return nil
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
