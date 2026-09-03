package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/logger"
)

// schemaVersion is the current database schema version.
// Increment this when adding new migrations.
const schemaVersion = 53

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
	case 34:
		return db.migration034WorkspaceInstalledCapabilities(ctx)
	case 35:
		return db.migration035WorkspaceToolboxes(ctx)
	case 36:
		return db.migration036WorkspaceMission(ctx)
	case 37:
		return db.migration037RunToolboxSnapshot(ctx)
	case 38:
		return db.migration038WorkspaceMapLayouts(ctx)
	case 39:
		return db.migration039WorkspacePlans(ctx)
	case 40:
		return db.migration040WorkspacePlanExecutionSlot(ctx)
	case 41:
		return db.migration041WorkspacePlanReconciliations(ctx)
	case 42:
		return db.migration042WorkspaceMapGroupPresentations(ctx)
	case 43:
		return db.migration043WorkspaceTicketState(ctx)
	case 44:
		return db.migration044WorkspaceFolderSlugs(ctx)
	case 45:
		return db.migration045WorkspaceAssistantProgram(ctx)
	case 46:
		return db.migration046PersonalAssistantFoundation(ctx)
	case 47:
		return db.migration047PersonalAssistantHireRecovery(ctx)
	case 48:
		return db.migration048PersonalAssistantPreviewSupersession(ctx)
	case 49:
		return db.migration049PersonalAssistantApplyRequest(ctx)
	case 50:
		return db.migration050PersonalAssistantFirstBrief(ctx)
	case 51:
		return db.migration051PersonalAssistantRenameJournal(ctx)
	case 52:
		return db.migration052PersonalAssistantHQSetup(ctx)
	case 53:
		return db.migration053PersonalAssistantSpecialist(ctx)
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
			folder_slug TEXT NOT NULL DEFAULT '',
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
			installed_capabilities_json TEXT DEFAULT '[]',
			toolbox_state_json TEXT DEFAULT '{}',
			mission_state_json TEXT,
			assistant_program_json TEXT NOT NULL DEFAULT '{}',
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

// migration034WorkspaceInstalledCapabilities adds the installed_capabilities_json
// column so a workspace's built-in Workspace Capability installs (e.g.
// file-janitor) round-trip through the primary store instead of living only in
// workspace.json. Purely additive: existing rows default to an empty collection,
// which reads back as "no capabilities installed" — never as a phantom install.
//
// The column mirrors workspace.json rather than replacing it. Both stores carry
// the field, and SyncStore.Save restores it from the canonical folder record when
// a stale or partial workspace would otherwise write it away (PRD FR-144).
func (db *DB) migration034WorkspaceInstalledCapabilities(ctx context.Context) error {
	exists, err := db.tableExists(ctx, "workspaces")
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	if _, err := db.ExecContext(ctx, `
		ALTER TABLE workspaces ADD COLUMN installed_capabilities_json TEXT DEFAULT '[]'
	`); err != nil && !isDuplicateColumnError(err) {
		return fmt.Errorf("failed to add workspace installed_capabilities_json column: %w", err)
	}
	return nil
}

// migration035WorkspaceToolboxes adds the toolbox_state_json column so a
// workspace's named Toolboxes, their per-instance assignments, and its
// migration state round-trip through the primary store instead of living only
// in workspace.json.
//
// One column rather than three because the three are always read and written
// together — a Toolbox without its assignment is not a meaningful half-state,
// and splitting them would let a partial write produce one.
//
// Purely additive: existing rows default to an empty object, which reads back
// as "this workspace has no explicit toolboxes yet" and therefore as an
// instance that still resolves through the legacy path — never as a phantom
// assignment that would silently change what an agent can do.
func (db *DB) migration035WorkspaceToolboxes(ctx context.Context) error {
	exists, err := db.tableExists(ctx, "workspaces")
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	if _, err := db.ExecContext(ctx, `
		ALTER TABLE workspaces ADD COLUMN toolbox_state_json TEXT DEFAULT '{}'
	`); err != nil && !isDuplicateColumnError(err) {
		return fmt.Errorf("failed to add workspace toolbox_state_json column: %w", err)
	}
	return nil
}

// migration036WorkspaceMission adds the mission_state_json column so a
// workspace's Goal — its text, cadence, autonomy, notification policy, and run
// counters — round-trips through the primary store.
//
// Without it every one of those fields was silently dropped on read, and the
// ordinary load → mutate → save cycle then wrote the emptied values back over
// the canonical workspace.json. A user could configure a Goal, see it save, and
// find it gone after an unrelated edit.
//
// The default is NULL rather than '{}' on purpose. NULL is the durable signal
// "this row predates the column", which is what lets SyncStore heal a legacy
// workspace from disk exactly once without ever resurrecting a Goal the user
// deliberately cleared — a cleared Goal writes a real (empty-valued) envelope,
// which is not NULL.
func (db *DB) migration036WorkspaceMission(ctx context.Context) error {
	exists, err := db.tableExists(ctx, "workspaces")
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	if _, err := db.ExecContext(ctx, `
		ALTER TABLE workspaces ADD COLUMN mission_state_json TEXT
	`); err != nil && !isDuplicateColumnError(err) {
		return fmt.Errorf("failed to add workspace mission_state_json column: %w", err)
	}
	return nil
}

// migration037RunToolboxSnapshot adds the two run columns that make a finished
// run explainable: the immutable capability snapshot it started with, and the
// Wrap-up measured against it.
//
// Purely additive. Historical runs keep NULL in both, which reads as "this run
// predates snapshots" — deliberately not as "this run had no capabilities",
// since the second would misreport what those runs actually did.
func (db *DB) migration037RunToolboxSnapshot(ctx context.Context) error {
	exists, err := db.tableExists(ctx, "workspace_runs")
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	for _, column := range []string{"toolbox_snapshot_json", "toolbox_wrap_up_json"} {
		if _, err := db.ExecContext(ctx,
			"ALTER TABLE workspace_runs ADD COLUMN "+column+" TEXT",
		); err != nil && !isDuplicateColumnError(err) {
			return fmt.Errorf("failed to add workspace run %s column: %w", column, err)
		}
	}
	return nil
}

// migration038WorkspaceMapLayouts creates the coordinate-based Workspace Map
// storage: one versioned layout record per user, plus one normalized position
// row per (user, workspace) anchor (#292 FR-3, FR-14, FR-15).
//
// Two tables rather than one JSON blob, for three reasons the feature depends
// on. A single dropped building updates one row instead of rewriting the whole
// map, so a stale browser tab cannot erase coordinates for workspaces it never
// touched (FR-101). A corrupt value degrades to one unusable row while every
// valid sibling still reads (FR-22). And the workspace foreign key can do the
// lifecycle work directly: a trashed workspace keeps its SQLite row, so its
// anchor survives for restore (FR-26, FR-27), while permanently deleting a
// workspace cascades away exactly its own position and nothing else (FR-28).
//
// The layout row is deliberately separate from the position rows so a reset can
// clear every anchor while preserving the user's snap preference (FR-110), and
// so a user who has only panned the camera still has a record to write to.
//
// Viewport columns are nullable REALs, not a JSON object. "No camera saved yet"
// is a real state that must open on Fit All rather than on a fabricated
// (0, 0, 1x) camera (FR-45), and a single corrupt axis can then be dropped
// without discarding the zoom beside it.
//
// Nothing here touches workspaces.layout. That column is the per-workspace
// CanvasLayout — tasks, agents, attachments, folders, stations — and stays
// under /api/workspaces/{id}/layout (FR-5, FR-104).
//
// user_id carries no foreign key on purpose: a layout must be writable for the
// current user whether or not a profile row was ever created for them, and V1
// never deletes users, so there is no cascade to gain.
func (db *DB) migration038WorkspaceMapLayouts(ctx context.Context) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS workspace_map_layouts (
			user_id TEXT PRIMARY KEY,
			schema_version INTEGER NOT NULL DEFAULT 1,
			revision INTEGER NOT NULL DEFAULT 0,
			viewport_center_x REAL,
			viewport_center_y REAL,
			viewport_zoom REAL,
			snap_to_grid INTEGER NOT NULL DEFAULT 1,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS workspace_map_positions (
			user_id TEXT NOT NULL,
			workspace_id TEXT NOT NULL,
			x REAL NOT NULL,
			y REAL NOT NULL,
			updated_at DATETIME NOT NULL,
			PRIMARY KEY (user_id, workspace_id),
			FOREIGN KEY (user_id) REFERENCES workspace_map_layouts(user_id) ON DELETE CASCADE,
			FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE
		)`,
		// Indexed for the workspace-side cascade and for the permanent-deletion
		// cleanup path, which look a position up by workspace rather than by user.
		`CREATE INDEX IF NOT EXISTS idx_workspace_map_positions_workspace
			ON workspace_map_positions(workspace_id)`,
	}
	for _, stmt := range statements {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("failed to create workspace map layout schema: %w", err)
		}
	}
	return nil
}

// migration042WorkspaceMapGroupPresentations adds the current user's per-group
// district presentation: the rectangle they sized by hand, whether the district
// is collapsed, and which curated accent and theme it wears (#346 FR-173).
//
// It is a third table rather than columns on workspace_map_positions or a JSON
// blob on workspace_map_layouts, for the same reasons migration 38 split
// positions from the layout header. A district is not a point — it has a width,
// a height, a sizing mode, a collapse state, and two preset identifiers, and
// bolting those onto the anchor row would give every ordinary building six
// columns that can only ever be NULL for it (PRD Technical Considerations,
// FR-177). Keeping it out of a blob is what lets one corrupt district degrade to
// safe defaults while every other district keeps its saved presentation
// (FR-192), and what lets a single resize update one row instead of rewriting
// every group the user has ever customized (FR-178).
//
// Frame columns are nullable REALs because "no custom rectangle" is the ordinary
// state: an automatic district recomputes its frame from its members on every
// render, so there is nothing to store, and storing one would turn a read into a
// write (FR-193). sizing_mode carries the intent so a row with a NULL frame is
// unambiguous rather than being inferred.
//
// Both foreign keys cascade. A permanently deleted group takes its presentation
// with it (FR-183) while a *trashed* group keeps its row, matching how a trashed
// workspace keeps its anchor for restore. Nothing here references parent_id or
// order_index: this table has no vocabulary for hierarchy (FR-5).
func (db *DB) migration042WorkspaceMapGroupPresentations(ctx context.Context) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS workspace_map_group_presentations (
			user_id TEXT NOT NULL,
			group_id TEXT NOT NULL,
			sizing_mode TEXT NOT NULL DEFAULT 'auto',
			frame_x REAL,
			frame_y REAL,
			frame_width REAL,
			frame_height REAL,
			collapsed INTEGER NOT NULL DEFAULT 0,
			accent TEXT NOT NULL DEFAULT 'default',
			theme TEXT NOT NULL DEFAULT 'default',
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			PRIMARY KEY (user_id, group_id),
			FOREIGN KEY (user_id) REFERENCES workspace_map_layouts(user_id) ON DELETE CASCADE,
			FOREIGN KEY (group_id) REFERENCES workspaces(id) ON DELETE CASCADE
		)`,
		// Indexed for the workspace-side cascade and the permanent-deletion
		// cleanup path, which look a district up by group rather than by user.
		`CREATE INDEX IF NOT EXISTS idx_workspace_map_group_presentations_group
			ON workspace_map_group_presentations(group_id)`,
	}
	for _, stmt := range statements {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("failed to create workspace map group presentation schema: %w", err)
		}
	}
	return nil
}

// migration043WorkspaceTicketState persists the two counters that make the
// Task-to-Ticket migration restart-safe. They previously lived only in
// workspace.json, while production reads came from SQLite, so every boot saw a
// zero migration version and rewrote every workspace.
func (db *DB) migration043WorkspaceTicketState(ctx context.Context) error {
	exists, err := db.tableExists(ctx, "workspaces")
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}

	if _, err := db.ExecContext(ctx, `
		ALTER TABLE workspaces ADD COLUMN ticket_migration_version INTEGER NOT NULL DEFAULT 0
	`); err != nil && !isDuplicateColumnError(err) {
		return fmt.Errorf("failed to add workspace ticket_migration_version column: %w", err)
	}
	if _, err := db.ExecContext(ctx, `
		ALTER TABLE workspaces ADD COLUMN ticket_sequence INTEGER NOT NULL DEFAULT 0
	`); err != nil && !isDuplicateColumnError(err) {
		return fmt.Errorf("failed to add workspace ticket_sequence column: %w", err)
	}
	return nil
}

// migration044WorkspaceFolderSlugs persists the browser-facing workspace slug.
// Empty values are intentionally allowed during the disk/SQLite reconciliation
// that follows schema migration. Once populated, active registrations share one
// case-insensitive namespace; trashed and missing rows release their slug so a
// restore must re-enter conflict validation.
func (db *DB) migration044WorkspaceFolderSlugs(ctx context.Context) error {
	exists, err := db.tableExists(ctx, "workspaces")
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}

	if _, err := db.ExecContext(ctx, `
		ALTER TABLE workspaces ADD COLUMN folder_slug TEXT NOT NULL DEFAULT ''
	`); err != nil && !isDuplicateColumnError(err) {
		return fmt.Errorf("failed to add workspace folder_slug column: %w", err)
	}
	var statusColumnCount int
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM pragma_table_info('workspaces') WHERE name = 'status'
	`).Scan(&statusColumnCount); err != nil {
		return fmt.Errorf("failed to inspect workspace status column: %w", err)
	}

	indexSQL := `
		CREATE UNIQUE INDEX IF NOT EXISTS idx_workspaces_folder_slug
		ON workspaces(folder_slug COLLATE NOCASE)
		WHERE TRIM(folder_slug) <> ''
	`
	if statusColumnCount > 0 {
		indexSQL = `
			CREATE UNIQUE INDEX IF NOT EXISTS idx_workspaces_folder_slug
			ON workspaces(folder_slug COLLATE NOCASE)
			WHERE TRIM(folder_slug) <> ''
				AND COALESCE(status, 'active') NOT IN ('trashed', 'missing')
		`
	}
	if _, err := db.ExecContext(ctx, indexSQL); err != nil {
		return fmt.Errorf("failed to create workspace folder_slug index: %w", err)
	}
	return nil
}

// migration039WorkspacePlans creates the Workspace Planning Workflow schema:
// the durable Plan record with its mutable working draft, immutable review
// versions, clarification questions and authored answers, approval records,
// Plan-to-Task and Plan-to-Run provenance, lifecycle activity, and autosave
// recovery snapshots (PRD FR-1 through FR-17).
//
// Three structural decisions are load-bearing rather than stylistic:
//
//   - Clarification answers live in their own table, not inside the draft JSON.
//     Regenerating a draft rewrites draft_json wholesale, so keeping authored
//     answers out of that blob is what makes "a later model summary cannot
//     overwrite the user's answer" true by construction rather than by care
//     (FR-25).
//   - Autosave recovery snapshots live in their own table, separate from
//     immutable review versions, so recovery points can be pruned to ten
//     without ever touching review history and can never be miscounted toward
//     the 50-version limit (FR-30, FR-31).
//   - A partial unique index on (plan_id, version, role, group_id, item_id)
//     makes a duplicate Task tree impossible at the storage layer, not just in
//     the materializer: a retried or concurrent approval consumption cannot
//     write a second link for the same approved item (FR-91, FR-178).
//     Follow-up links are excluded because corrective work deliberately adds a
//     second Task for an item whose original is immutable (FR-78).
//
// Nothing here stores Task execution state, Run traces, or Run artifacts. Those
// records stay authoritative in their own tables and are referenced by ID only
// (FR-11, FR-12).
// migration045WorkspaceAssistantProgram mirrors generic station/link state into
// the primary store. The canonical workspace.json copy remains portable.
func (db *DB) migration045WorkspaceAssistantProgram(ctx context.Context) error {
	exists, err := db.tableExists(ctx, "workspaces")
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	if _, err := db.ExecContext(ctx, `
		ALTER TABLE workspaces ADD COLUMN assistant_program_json TEXT NOT NULL DEFAULT '{}'
	`); err != nil && !isDuplicateColumnError(err) {
		return fmt.Errorf("failed to add workspace assistant_program_json column: %w", err)
	}
	return nil
}

// migration046PersonalAssistantFoundation adds the user-owned relationship
// record and first-assignment operation journal. Daily Brief content/schedules,
// chat transcripts, credentials, Tickets, Follow-Ups, and Memory remain in
// their canonical stores and are referenced here by bounded IDs only.
func (db *DB) migration046PersonalAssistantFoundation(ctx context.Context) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS personal_assistant_state (
			user_id TEXT PRIMARY KEY,
			assistant_id TEXT NOT NULL UNIQUE,
			status TEXT NOT NULL DEFAULT 'not_hired'
				CHECK (status IN ('not_hired', 'hiring', 'active', 'paused', 'repair_needed')),
			display_name TEXT NOT NULL DEFAULT '',
			appearance_json TEXT NOT NULL DEFAULT '{}',
			hq_workspace_id TEXT NOT NULL DEFAULT '',
			hq_entry_agent_instance_id TEXT NOT NULL DEFAULT '',
			global_agent_profile_name TEXT NOT NULL DEFAULT '',
			mandate TEXT NOT NULL DEFAULT '',
			focus_areas_json TEXT NOT NULL DEFAULT '[]',
			first_assignment_status TEXT NOT NULL DEFAULT 'not_started'
				CHECK (first_assignment_status IN ('not_started', 'previewed', 'applying', 'completed', 'failed')),
			last_hire_request_id TEXT NOT NULL DEFAULT '',
			state_version INTEGER NOT NULL DEFAULT 1 CHECK (state_version > 0),
			hired_at DATETIME,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			UNIQUE (user_id, assistant_id)
		)`,
		`CREATE TABLE IF NOT EXISTS personal_assistant_assignment (
			preview_id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL,
			assistant_id TEXT NOT NULL,
			assignment_version INTEGER NOT NULL DEFAULT 1 CHECK (assignment_version > 0),
			normalized_payload_json TEXT NOT NULL,
			normalized_payload_hash TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'previewed'
				CHECK (status IN ('previewed', 'applying', 'completed', 'failed')),
			created_canonical_refs_json TEXT NOT NULL DEFAULT '[]',
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			FOREIGN KEY (user_id, assistant_id)
				REFERENCES personal_assistant_state(user_id, assistant_id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_personal_assistant_assignment_owner
			ON personal_assistant_assignment(user_id, assistant_id, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_personal_assistant_assignment_hash
			ON personal_assistant_assignment(assistant_id, normalized_payload_hash)`,
	}
	for _, stmt := range statements {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("failed to create personal assistant foundation schema: %w", err)
		}
	}
	return nil
}

// migration047PersonalAssistantHireRecovery adds only bounded operation
// metadata needed to prove that a replay carries the same normalized hire
// payload and to resume a known missing provisioning step after restart.
func (db *DB) migration047PersonalAssistantHireRecovery(ctx context.Context) error {
	exists, err := db.tableExists(ctx, "personal_assistant_state")
	if err != nil || !exists {
		return err
	}
	for _, statement := range []struct {
		sql, label string
	}{
		{`ALTER TABLE personal_assistant_state ADD COLUMN hire_payload_hash TEXT NOT NULL DEFAULT ''`, "hire_payload_hash"},
		{`ALTER TABLE personal_assistant_state ADD COLUMN hire_payload_json TEXT NOT NULL DEFAULT ''`, "hire_payload_json"},
		{`ALTER TABLE personal_assistant_state ADD COLUMN repair_step TEXT NOT NULL DEFAULT ''`, "repair_step"},
	} {
		if _, err := db.ExecContext(ctx, statement.sql); err != nil && !isDuplicateColumnError(err) {
			return fmt.Errorf("failed to add personal_assistant_state.%s column: %w", statement.label, err)
		}
	}
	return nil
}

// migration048PersonalAssistantPreviewSupersession extends the assignment
// journal's closed lifecycle with an explicit non-applicable superseded state.
func (db *DB) migration048PersonalAssistantPreviewSupersession(ctx context.Context) error {
	exists, err := db.tableExists(ctx, "personal_assistant_assignment")
	if err != nil || !exists {
		return err
	}
	statements := []string{
		`CREATE TABLE personal_assistant_assignment_v48 (
			preview_id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL,
			assistant_id TEXT NOT NULL,
			assignment_version INTEGER NOT NULL DEFAULT 1 CHECK (assignment_version > 0),
			normalized_payload_json TEXT NOT NULL,
			normalized_payload_hash TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'previewed'
				CHECK (status IN ('previewed', 'applying', 'completed', 'failed', 'superseded')),
			created_canonical_refs_json TEXT NOT NULL DEFAULT '[]',
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			FOREIGN KEY (user_id, assistant_id)
				REFERENCES personal_assistant_state(user_id, assistant_id) ON DELETE CASCADE
		)`,
		`INSERT INTO personal_assistant_assignment_v48 (
			preview_id, user_id, assistant_id, assignment_version,
			normalized_payload_json, normalized_payload_hash, status,
			created_canonical_refs_json, created_at, updated_at
		) SELECT preview_id, user_id, assistant_id, assignment_version,
			normalized_payload_json, normalized_payload_hash, status,
			created_canonical_refs_json, created_at, updated_at
		FROM personal_assistant_assignment`,
		`DROP TABLE personal_assistant_assignment`,
		`ALTER TABLE personal_assistant_assignment_v48 RENAME TO personal_assistant_assignment`,
		`CREATE INDEX idx_personal_assistant_assignment_owner
			ON personal_assistant_assignment(user_id, assistant_id, created_at)`,
		`CREATE INDEX idx_personal_assistant_assignment_hash
			ON personal_assistant_assignment(assistant_id, normalized_payload_hash)`,
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("failed to add personal assistant preview supersession: %w", err)
		}
	}
	return nil
}

// migration049PersonalAssistantApplyRequest binds a client retry identity to
// exactly one preview without storing any free-form failure detail.
func (db *DB) migration049PersonalAssistantApplyRequest(ctx context.Context) error {
	exists, err := db.tableExists(ctx, "personal_assistant_assignment")
	if err != nil || !exists {
		return err
	}
	if _, err := db.ExecContext(ctx, `
		ALTER TABLE personal_assistant_assignment
		ADD COLUMN apply_request_id TEXT NOT NULL DEFAULT ''
	`); err != nil && !isDuplicateColumnError(err) {
		return fmt.Errorf("failed to add personal_assistant_assignment.apply_request_id: %w", err)
	}
	if _, err := db.ExecContext(ctx, `
		CREATE UNIQUE INDEX IF NOT EXISTS idx_personal_assistant_assignment_apply_request
		ON personal_assistant_assignment(user_id, apply_request_id)
		WHERE apply_request_id != ''
	`); err != nil {
		return fmt.Errorf("failed to index personal assistant apply requests: %w", err)
	}
	return nil
}

// migration050PersonalAssistantFirstBrief persists only stable Daily Brief
// lifecycle IDs/status needed to resume after records become visible.
func (db *DB) migration051PersonalAssistantRenameJournal(ctx context.Context) error {
	exists, err := db.tableExists(ctx, "personal_assistant_state")
	if err != nil || !exists {
		return err
	}
	statements := []struct {
		sql   string
		label string
	}{
		{`ALTER TABLE personal_assistant_state ADD COLUMN rename_from_name TEXT NOT NULL DEFAULT ''`, "rename_from_name"},
		{`ALTER TABLE personal_assistant_state ADD COLUMN rename_to_name TEXT NOT NULL DEFAULT ''`, "rename_to_name"},
		{`ALTER TABLE personal_assistant_state ADD COLUMN rename_step TEXT NOT NULL DEFAULT ''`, "rename_step"},
	}
	for _, statement := range statements {
		if _, execErr := db.ExecContext(ctx, statement.sql); execErr != nil && !isDuplicateColumnError(execErr) {
			return fmt.Errorf("failed to add personal_assistant_state.%s column: %w", statement.label, execErr)
		}
	}
	return nil
}

// migration052PersonalAssistantHQSetup makes hiring and Personal HQ creation
// two separate consequences.
//
// It widens the closed personal_assistant_state.status constraint with the two
// post-hire setup stages (awaiting_hq, provisioning_hq) and adds the bounded HQ
// setup operation journal used to make one confirmed Build My HQ request
// idempotent and restart-resumable.
//
// The journal stores only a request ID, a normalized payload hash, and the
// provisional normalized payload. The payload is cleared back to its receipt
// once the canonical workspace and Daily Brief configuration exist, so PAF
// never keeps a permanent duplicate of the Daily Brief schedule.
//
// SQLite cannot alter a CHECK constraint in place, so the parent table is
// rebuilt. Every existing row, timestamp, state version, and bounded operation
// field is copied verbatim; the child assignment journal keeps its foreign key
// because it references the table by name, and foreign_key_check verifies that
// before the rebuild is accepted.
func (db *DB) migration052PersonalAssistantHQSetup(ctx context.Context) error {
	exists, err := db.tableExists(ctx, "personal_assistant_state")
	if err != nil || !exists {
		return err
	}

	// The pool is pinned to a single connection, so these pragmas apply to the
	// same session as the rebuild below.
	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		return fmt.Errorf("failed to suspend foreign keys for personal assistant hq setup: %w", err)
	}
	restore := func() error {
		if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys = ON`); err != nil {
			return fmt.Errorf("failed to restore foreign keys after personal assistant hq setup: %w", err)
		}
		return nil
	}

	columns := `user_id, assistant_id, status, display_name, appearance_json,
		hq_workspace_id, hq_entry_agent_instance_id, global_agent_profile_name,
		mandate, focus_areas_json, first_assignment_status,
		last_hire_request_id, hire_payload_hash, hire_payload_json, repair_step,
		rename_from_name, rename_to_name, rename_step,
		state_version, hired_at, created_at, updated_at`

	statements := []string{
		`CREATE TABLE personal_assistant_state_v52 (
			user_id TEXT PRIMARY KEY,
			assistant_id TEXT NOT NULL UNIQUE,
			status TEXT NOT NULL DEFAULT 'not_hired'
				CHECK (status IN ('not_hired', 'hiring', 'awaiting_hq', 'provisioning_hq',
					'active', 'paused', 'repair_needed')),
			display_name TEXT NOT NULL DEFAULT '',
			appearance_json TEXT NOT NULL DEFAULT '{}',
			hq_workspace_id TEXT NOT NULL DEFAULT '',
			hq_entry_agent_instance_id TEXT NOT NULL DEFAULT '',
			global_agent_profile_name TEXT NOT NULL DEFAULT '',
			mandate TEXT NOT NULL DEFAULT '',
			focus_areas_json TEXT NOT NULL DEFAULT '[]',
			first_assignment_status TEXT NOT NULL DEFAULT 'not_started'
				CHECK (first_assignment_status IN ('not_started', 'previewed', 'applying', 'completed', 'failed')),
			last_hire_request_id TEXT NOT NULL DEFAULT '',
			hire_payload_hash TEXT NOT NULL DEFAULT '',
			hire_payload_json TEXT NOT NULL DEFAULT '',
			repair_step TEXT NOT NULL DEFAULT '',
			rename_from_name TEXT NOT NULL DEFAULT '',
			rename_to_name TEXT NOT NULL DEFAULT '',
			rename_step TEXT NOT NULL DEFAULT '',
			last_hq_request_id TEXT NOT NULL DEFAULT '',
			hq_payload_hash TEXT NOT NULL DEFAULT '',
			hq_payload_json TEXT NOT NULL DEFAULT '',
			state_version INTEGER NOT NULL DEFAULT 1 CHECK (state_version > 0),
			hired_at DATETIME,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			UNIQUE (user_id, assistant_id)
		)`,
		`INSERT INTO personal_assistant_state_v52 (` + columns + `)
			SELECT ` + columns + ` FROM personal_assistant_state`,
		`DROP TABLE personal_assistant_state`,
		`ALTER TABLE personal_assistant_state_v52 RENAME TO personal_assistant_state`,
	}
	// The rebuild runs as one transaction so a failure between DROP and RENAME
	// cannot leave the relationship table missing. The pragma above is
	// deliberately outside it — foreign_keys is a no-op inside a transaction.
	if txErr := db.InTransaction(ctx, func(tx *sql.Tx) error {
		for _, statement := range statements {
			if _, execErr := tx.ExecContext(ctx, statement); execErr != nil {
				return execErr
			}
		}
		return nil
	}); txErr != nil {
		_ = restore()
		return fmt.Errorf("failed to rebuild personal_assistant_state for hq setup: %w", txErr)
	}

	var orphanTable, orphanParent sql.NullString
	var orphanRowID, orphanFKID sql.NullInt64
	row := db.QueryRowContext(ctx, `PRAGMA foreign_key_check`)
	switch scanErr := row.Scan(&orphanTable, &orphanRowID, &orphanParent, &orphanFKID); {
	case scanErr == nil:
		_ = restore()
		return fmt.Errorf("personal assistant hq setup rebuild orphaned rows in %s", orphanTable.String)
	case errors.Is(scanErr, sql.ErrNoRows):
		// No violations: the child journal still resolves to the rebuilt parent.
	default:
		_ = restore()
		return fmt.Errorf("failed to verify personal assistant foreign keys: %w", scanErr)
	}

	return restore()
}

// migration053PersonalAssistantSpecialist records the domain specialist offer's
// outcome, so post-hire surfaces can shape themselves without re-running
// application detection.
//
// Both columns are additive and default to empty: every existing relationship
// reads as "never offered, no specialist" and keeps today's behaviour with no
// backfill. The table remains one row per user; nothing about its identity
// changes.
func (db *DB) migration053PersonalAssistantSpecialist(ctx context.Context) error {
	exists, err := db.tableExists(ctx, "personal_assistant_state")
	if err != nil || !exists {
		return err
	}
	for _, statement := range []struct {
		sql, label string
	}{
		// The accepted domain, "" when none was accepted.
		{`ALTER TABLE personal_assistant_state ADD COLUMN specialist_slug TEXT NOT NULL DEFAULT ''`, "specialist_slug"},
		// Whether the offer has been answered at all: "" (not yet), "accepted",
		// or "declined". Kept separate from the slug so a decline is remembered
		// and never re-asked, which an empty slug alone cannot express.
		{`ALTER TABLE personal_assistant_state ADD COLUMN specialist_offer_state TEXT NOT NULL DEFAULT ''`, "specialist_offer_state"},
	} {
		if _, execErr := db.ExecContext(ctx, statement.sql); execErr != nil && !isDuplicateColumnError(execErr) {
			return fmt.Errorf("failed to add personal_assistant_state.%s column: %w", statement.label, execErr)
		}
	}
	return nil
}

func (db *DB) migration050PersonalAssistantFirstBrief(ctx context.Context) error {
	exists, err := db.tableExists(ctx, "personal_assistant_assignment")
	if err != nil || !exists {
		return err
	}
	for _, column := range []struct {
		name string
		sql  string
	}{
		{"brief_request_id", `ALTER TABLE personal_assistant_assignment ADD COLUMN brief_request_id TEXT NOT NULL DEFAULT ''`},
		{"brief_revision_id", `ALTER TABLE personal_assistant_assignment ADD COLUMN brief_revision_id TEXT NOT NULL DEFAULT ''`},
		{"brief_status", `ALTER TABLE personal_assistant_assignment ADD COLUMN brief_status TEXT NOT NULL DEFAULT ''`},
		{"brief_trigger", `ALTER TABLE personal_assistant_assignment ADD COLUMN brief_trigger TEXT NOT NULL DEFAULT ''`},
	} {
		if _, err := db.ExecContext(ctx, column.sql); err != nil && !isDuplicateColumnError(err) {
			return fmt.Errorf("failed to add personal_assistant_assignment.%s: %w", column.name, err)
		}
	}
	return nil
}

func (db *DB) migration039WorkspacePlans(ctx context.Context) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS workspace_plans (
			id TEXT PRIMARY KEY,
			workspace_id TEXT NOT NULL,
			title TEXT NOT NULL DEFAULT '',
			original_request TEXT NOT NULL DEFAULT '',
			objective TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL,
			draft_json TEXT NOT NULL DEFAULT '{}',
			draft_revision INTEGER NOT NULL DEFAULT 0,
			draft_intent TEXT NOT NULL DEFAULT '',
			current_version INTEGER NOT NULL DEFAULT 0,
			approved_version INTEGER NOT NULL DEFAULT 0,
			superseded_by_plan_id TEXT NOT NULL DEFAULT '',
			supersedes_plan_id TEXT NOT NULL DEFAULT '',
			origin_json TEXT NOT NULL DEFAULT '{}',
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			last_activity_at DATETIME NOT NULL,
			archived_at DATETIME,
			archive_reason TEXT NOT NULL DEFAULT '',
			FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE
		)`,
		// Immutable review snapshots. content_json, content_hash, and
		// policy_snapshot_json are written once and never updated; only the
		// review decision columns are filled in later (FR-31, FR-32, FR-144).
		`CREATE TABLE IF NOT EXISTS workspace_plan_versions (
			plan_id TEXT NOT NULL,
			version INTEGER NOT NULL,
			workspace_id TEXT NOT NULL,
			title TEXT NOT NULL DEFAULT '',
			objective TEXT NOT NULL DEFAULT '',
			content_json TEXT NOT NULL,
			content_hash TEXT NOT NULL,
			policy_snapshot_json TEXT NOT NULL DEFAULT '{}',
			intent TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			created_by_json TEXT NOT NULL DEFAULT '{}',
			decided_at DATETIME,
			decided_by TEXT NOT NULL DEFAULT '',
			decision_reason TEXT NOT NULL DEFAULT '',
			PRIMARY KEY (plan_id, version),
			FOREIGN KEY (plan_id) REFERENCES workspace_plans(id) ON DELETE CASCADE
		)`,
		// Clarification questions and the answers the user authored (FR-24).
		// The answer columns are only ever written by the answer path.
		`CREATE TABLE IF NOT EXISTS workspace_plan_clarifications (
			id TEXT PRIMARY KEY,
			plan_id TEXT NOT NULL,
			workspace_id TEXT NOT NULL,
			prompt TEXT NOT NULL,
			detail TEXT NOT NULL DEFAULT '',
			options_json TEXT NOT NULL DEFAULT '[]',
			required INTEGER NOT NULL DEFAULT 0,
			status TEXT NOT NULL,
			round INTEGER NOT NULL DEFAULT 0,
			ordinal INTEGER NOT NULL DEFAULT 0,
			answer TEXT NOT NULL DEFAULT '',
			answered_by TEXT NOT NULL DEFAULT '',
			answered_at DATETIME,
			skip_reason TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL,
			FOREIGN KEY (plan_id) REFERENCES workspace_plans(id) ON DELETE CASCADE
		)`,
		// Approval records. They survive restart and are consumable exactly
		// once for their declared effect (FR-70, FR-71, FR-72).
		`CREATE TABLE IF NOT EXISTS workspace_plan_approvals (
			id TEXT PRIMARY KEY,
			plan_id TEXT NOT NULL,
			workspace_id TEXT NOT NULL,
			version INTEGER NOT NULL,
			content_hash TEXT NOT NULL,
			effect TEXT NOT NULL,
			user_id TEXT NOT NULL DEFAULT '',
			user_name TEXT NOT NULL DEFAULT '',
			idempotency_key TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			consumed_at DATETIME,
			consumed_result_json TEXT,
			invalidated_at DATETIME,
			invalidated_reason TEXT NOT NULL DEFAULT '',
			FOREIGN KEY (plan_id) REFERENCES workspace_plans(id) ON DELETE CASCADE
		)`,
		// Plan-to-Task provenance. The Task record itself remains the only
		// authority on that Task's state (FR-9, FR-10, FR-11).
		`CREATE TABLE IF NOT EXISTS workspace_plan_task_links (
			plan_id TEXT NOT NULL,
			task_id TEXT NOT NULL,
			workspace_id TEXT NOT NULL,
			version INTEGER NOT NULL,
			approval_id TEXT NOT NULL DEFAULT '',
			group_id TEXT NOT NULL DEFAULT '',
			item_id TEXT NOT NULL DEFAULT '',
			role TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			replaced_by_task_id TEXT NOT NULL DEFAULT '',
			retired_at DATETIME,
			retired_reason TEXT NOT NULL DEFAULT '',
			PRIMARY KEY (plan_id, task_id),
			FOREIGN KEY (plan_id) REFERENCES workspace_plans(id) ON DELETE CASCADE
		)`,
		// Plan-to-Run provenance. Run status, traces, artifacts, and results
		// stay on the Run record (FR-11, FR-100).
		`CREATE TABLE IF NOT EXISTS workspace_plan_run_links (
			plan_id TEXT NOT NULL,
			run_id TEXT NOT NULL,
			workspace_id TEXT NOT NULL,
			version INTEGER NOT NULL,
			group_id TEXT NOT NULL DEFAULT '',
			item_id TEXT NOT NULL DEFAULT '',
			task_id TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL,
			PRIMARY KEY (plan_id, run_id),
			FOREIGN KEY (plan_id) REFERENCES workspace_plans(id) ON DELETE CASCADE
		)`,
		// Append-only lifecycle history: validated status transitions plus the
		// approval audit events that do not change status (FR-15, FR-80).
		`CREATE TABLE IF NOT EXISTS workspace_plan_activity (
			id TEXT PRIMARY KEY,
			plan_id TEXT NOT NULL,
			workspace_id TEXT NOT NULL,
			sequence INTEGER NOT NULL,
			kind TEXT NOT NULL,
			from_status TEXT NOT NULL DEFAULT '',
			to_status TEXT NOT NULL DEFAULT '',
			source TEXT NOT NULL,
			actor TEXT NOT NULL DEFAULT '',
			actor_id TEXT NOT NULL DEFAULT '',
			reason TEXT NOT NULL DEFAULT '',
			version INTEGER NOT NULL DEFAULT 0,
			approval_id TEXT NOT NULL DEFAULT '',
			task_id TEXT NOT NULL DEFAULT '',
			run_id TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL,
			UNIQUE(plan_id, sequence),
			FOREIGN KEY (plan_id) REFERENCES workspace_plans(id) ON DELETE CASCADE
		)`,
		// Autosave recovery points for the working draft. Deliberately not
		// review versions (FR-30).
		`CREATE TABLE IF NOT EXISTS workspace_plan_draft_snapshots (
			id TEXT PRIMARY KEY,
			plan_id TEXT NOT NULL,
			workspace_id TEXT NOT NULL,
			draft_revision INTEGER NOT NULL,
			title TEXT NOT NULL DEFAULT '',
			objective TEXT NOT NULL DEFAULT '',
			content_json TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			FOREIGN KEY (plan_id) REFERENCES workspace_plans(id) ON DELETE CASCADE
		)`,
		// Active/History listing and the retention sweep both read by
		// workspace, archive state, and recency (FR-16, FR-146).
		`CREATE INDEX IF NOT EXISTS idx_workspace_plans_workspace_activity
			ON workspace_plans(workspace_id, archived_at, last_activity_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_workspace_plans_workspace_status
			ON workspace_plans(workspace_id, status)`,
		`CREATE INDEX IF NOT EXISTS idx_workspace_plan_versions_plan
			ON workspace_plan_versions(plan_id, version DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_workspace_plan_clarifications_plan
			ON workspace_plan_clarifications(plan_id, round, ordinal)`,
		`CREATE INDEX IF NOT EXISTS idx_workspace_plan_approvals_plan_version
			ON workspace_plan_approvals(plan_id, version)`,
		// One approval per idempotency key: a retried approval request replays
		// the original record instead of creating a second one (FR-73).
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_workspace_plan_approvals_idempotency
			ON workspace_plan_approvals(plan_id, idempotency_key)`,
		// Reverse lookups: Task and Run detail resolve their originating Plan
		// without scanning (FR-10, FR-148).
		`CREATE INDEX IF NOT EXISTS idx_workspace_plan_task_links_task
			ON workspace_plan_task_links(task_id)`,
		`CREATE INDEX IF NOT EXISTS idx_workspace_plan_run_links_run
			ON workspace_plan_run_links(run_id)`,
		// The duplicate-Task backstop described above (FR-91, FR-178).
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_workspace_plan_task_links_materialized
			ON workspace_plan_task_links(plan_id, version, role, group_id, item_id)
			WHERE role <> 'follow_up'`,
		`CREATE INDEX IF NOT EXISTS idx_workspace_plan_activity_plan_sequence
			ON workspace_plan_activity(plan_id, sequence)`,
		`CREATE INDEX IF NOT EXISTS idx_workspace_plan_draft_snapshots_plan
			ON workspace_plan_draft_snapshots(plan_id, created_at DESC)`,
	}
	for _, stmt := range statements {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("failed to create workspace plan schema: %w", err)
		}
	}
	return nil
}

// migration040WorkspacePlanExecutionSlot creates the workspace execution slot:
// the arbitration that lets exactly one Plan run at a time in a workspace while
// other approved Plans wait visibly (PRD FR-106, FR-107).
//
// The single-executing-Plan rule is a PRIMARY KEY, not a check. One row per
// workspace in workspace_plan_execution_slots means a second Plan physically
// cannot hold the slot, however many processes race for it — the database
// refuses the insert rather than application code remembering to look first.
//
// `generation` is a fencing token. A worker that acquired the slot, stalled,
// and woke up after the lease moved on still holds the old generation, so its
// writes are refused. Without it, a slow worker could dispatch work for a Plan
// that no longer owns the slot — the classic distributed-lease failure, and one
// that a timestamp alone does not prevent.
//
// The queue is a separate table because waiting is not owning: a queued Plan
// has no claim on anything, and modelling it as a weaker kind of ownership
// invites code that treats it as one.
func (db *DB) migration040WorkspacePlanExecutionSlot(ctx context.Context) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS workspace_plan_execution_slots (
			workspace_id TEXT PRIMARY KEY,
			plan_id TEXT NOT NULL,
			generation INTEGER NOT NULL DEFAULT 1,
			owner TEXT NOT NULL DEFAULT '',
			acquired_at DATETIME NOT NULL,
			heartbeat_at DATETIME NOT NULL,
			FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE,
			FOREIGN KEY (plan_id) REFERENCES workspace_plans(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS workspace_plan_execution_queue (
			workspace_id TEXT NOT NULL,
			plan_id TEXT NOT NULL,
			queued_at DATETIME NOT NULL,
			PRIMARY KEY (workspace_id, plan_id),
			FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE,
			FOREIGN KEY (plan_id) REFERENCES workspace_plans(id) ON DELETE CASCADE
		)`,
		// generation is monotonic per workspace and survives release, so a
		// stale worker's token can never be reissued to a later holder.
		`CREATE TABLE IF NOT EXISTS workspace_plan_execution_generations (
			workspace_id TEXT PRIMARY KEY,
			generation INTEGER NOT NULL DEFAULT 0,
			FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE
		)`,
		// Queue order is first-come, and reading it is the "you are Nth in
		// line" the UI shows (FR-107).
		`CREATE INDEX IF NOT EXISTS idx_workspace_plan_execution_queue_order
			ON workspace_plan_execution_queue(workspace_id, queued_at)`,
		`CREATE INDEX IF NOT EXISTS idx_workspace_plan_execution_slots_plan
			ON workspace_plan_execution_slots(plan_id)`,
	}
	for _, stmt := range statements {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("failed to create workspace plan execution slot schema: %w", err)
		}
	}
	return nil
}

// migration041WorkspacePlanReconciliations records the user's confirmation of
// one exact reconciliation preview (PRD FR-77, FR-116).
//
// A corrective revision can cancel Tasks the earlier approval created. That is
// a second decision on top of approving the revised plan, and it is recorded
// separately so the audit trail can show what the user was told they were
// cancelling — `entries` holds the preview verbatim rather than something
// recomputed later, when the state it described has already moved.
//
// `token` is a hash of the exact state the preview was computed from, including
// every linked Task's status. The unique index on (plan_id, token) makes a
// re-confirmation idempotent, and `applied_at` makes it single-use: together
// they mean a double-clicked confirm cannot cancel two rounds of work.
//
// Rows are never deleted. A confirmation that was applied is history; one that
// was superseded is the record of a decision the user made and then moved past.
func (db *DB) migration041WorkspacePlanReconciliations(ctx context.Context) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS workspace_plan_reconciliations (
			id TEXT PRIMARY KEY,
			plan_id TEXT NOT NULL,
			workspace_id TEXT NOT NULL,
			token TEXT NOT NULL,
			from_version INTEGER NOT NULL,
			to_version INTEGER NOT NULL,
			intent TEXT NOT NULL DEFAULT '',
			entries TEXT NOT NULL DEFAULT '[]',
			confirmed_by TEXT NOT NULL DEFAULT '',
			confirmed_at DATETIME NOT NULL,
			applied_at DATETIME,
			FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE,
			FOREIGN KEY (plan_id) REFERENCES workspace_plans(id) ON DELETE CASCADE
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_workspace_plan_reconciliations_token
			ON workspace_plan_reconciliations(plan_id, token)`,
		`CREATE INDEX IF NOT EXISTS idx_workspace_plan_reconciliations_plan
			ON workspace_plan_reconciliations(workspace_id, plan_id, confirmed_at DESC)`,
	}
	for _, stmt := range statements {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("failed to create workspace plan reconciliation schema: %w", err)
		}
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
