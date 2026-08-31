package database

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenInMemory(t *testing.T) {
	ctx := context.Background()

	cfg := &Config{
		InMemory: true,
		WALMode:  false, // WAL doesn't apply to in-memory databases
	}

	db, err := Open(ctx, cfg)
	if err != nil {
		t.Fatalf("Failed to open in-memory database: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Verify the database is accessible
	var result int
	err = db.QueryRowContext(ctx, "SELECT 1").Scan(&result)
	if err != nil {
		t.Fatalf("Failed to query database: %v", err)
	}

	if result != 1 {
		t.Errorf("Expected 1, got %d", result)
	}
}

func TestOpenFile(t *testing.T) {
	ctx := context.Background()

	// Create a temporary directory for the test
	tmpDir, err := os.MkdirTemp("", "ori-db-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	dbPath := filepath.Join(tmpDir, "test.db")

	cfg := &Config{
		Path:    dbPath,
		WALMode: true,
	}

	db, err := Open(ctx, cfg)
	if err != nil {
		t.Fatalf("Failed to open file database: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Verify the database file was created
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Errorf("Database file was not created at %s", dbPath)
	}

	// Verify the path is reported correctly
	if db.Path() != dbPath {
		t.Errorf("Expected path %s, got %s", dbPath, db.Path())
	}
}

func TestMigrations(t *testing.T) {
	ctx := context.Background()

	cfg := &Config{
		InMemory: true,
		WALMode:  false,
	}

	db, err := Open(ctx, cfg)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Verify schema version
	version, err := db.GetSchemaVersion(ctx)
	if err != nil {
		t.Fatalf("Failed to get schema version: %v", err)
	}

	if version != schemaVersion {
		t.Errorf("Expected schema version %d, got %d", schemaVersion, version)
	}

	// Verify tables exist
	tables := []string{
		"sessions",
		"messages",
		"workspaces",
		"session_tags",
		"sessions_fts",
		"schema_migrations",
		"vaults",
		"home_assistant_intake_traces",
		"users",
		"personal_assistant_state",
		"personal_assistant_assignment",
	}
	for _, table := range tables {
		var name string
		err := db.QueryRowContext(ctx, "SELECT name FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&name)
		if err != nil {
			t.Errorf("Table %s does not exist: %v", table, err)
		}
	}

	// Verify view exists
	var viewName string
	err = db.QueryRowContext(ctx, "SELECT name FROM sqlite_master WHERE type='view' AND name='tag_counts'").Scan(&viewName)
	if err != nil {
		t.Errorf("View tag_counts does not exist: %v", err)
	}

	workspaceColumns := map[string]bool{
		"folder_slug":             false,
		"mcp_bindings_json":       false,
		"agent_mcp_access_json":   false,
		"skill_bindings_json":     false,
		"agent_skill_access_json": false,
		"folders_json":            false,
		"tags":                    false,
		"owner_user_id":           false,
	}
	vaultColumns := map[string]bool{
		"name":        false,
		"description": false,
		"file_path":   false,
		"created_at":  false,
		"updated_at":  false,
	}

	rows, err := db.QueryContext(ctx, "PRAGMA table_info(workspaces)")
	if err != nil {
		t.Fatalf("Failed to inspect workspaces columns: %v", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var cid int
		var name string
		var colType string
		var notNull int
		var defaultValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &colType, &notNull, &defaultValue, &pk); err != nil {
			t.Fatalf("Failed to scan workspaces column info: %v", err)
		}
		if _, ok := workspaceColumns[name]; ok {
			workspaceColumns[name] = true
		}
	}

	for name, found := range workspaceColumns {
		if !found {
			t.Errorf("workspace column %s does not exist", name)
		}
	}

	vaultRows, err := db.QueryContext(ctx, "PRAGMA table_info(vaults)")
	if err != nil {
		t.Fatalf("Failed to inspect vaults columns: %v", err)
	}
	defer func() { _ = vaultRows.Close() }()

	for vaultRows.Next() {
		var cid int
		var name string
		var colType string
		var notNull int
		var defaultValue sql.NullString
		var pk int
		if err := vaultRows.Scan(&cid, &name, &colType, &notNull, &defaultValue, &pk); err != nil {
			t.Fatalf("Failed to scan vaults column info: %v", err)
		}
		if _, ok := vaultColumns[name]; ok {
			vaultColumns[name] = true
		}
	}

	for name, found := range vaultColumns {
		if !found {
			t.Errorf("vault column %s does not exist", name)
		}
	}

	var vaultCount int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM vaults").Scan(&vaultCount); err != nil {
		t.Fatalf("Failed to count vaults: %v", err)
	}
	if vaultCount != 0 {
		t.Errorf("expected no vaults in a fresh database, got %d", vaultCount)
	}
}

func TestForeignKeys(t *testing.T) {
	ctx := context.Background()

	cfg := &Config{
		InMemory: true,
		WALMode:  false,
	}

	db, err := Open(ctx, cfg)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Verify foreign keys are enabled
	var fkEnabled int
	err = db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fkEnabled)
	if err != nil {
		t.Fatalf("Failed to check foreign_keys pragma: %v", err)
	}

	if fkEnabled != 1 {
		t.Errorf("Foreign keys should be enabled, got %d", fkEnabled)
	}
}

func TestMigration013RemovesLegacyVaultCatalogRowsWithoutFilePath(t *testing.T) {
	ctx := context.Background()

	tmpDir, err := os.MkdirTemp("", "ori-db-migration-*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	dbPath := filepath.Join(tmpDir, "test.db")
	legacyDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("Failed to open legacy database: %v", err)
	}

	if _, err := legacyDB.ExecContext(ctx, `
		CREATE TABLE schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`); err != nil {
		t.Fatalf("Failed to create schema_migrations: %v", err)
	}
	if _, err := legacyDB.ExecContext(ctx, `INSERT INTO schema_migrations (version) VALUES (12)`); err != nil {
		t.Fatalf("Failed to seed schema version 12: %v", err)
	}
	if _, err := legacyDB.ExecContext(ctx, `
		CREATE TABLE vaults (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			description TEXT DEFAULT '',
			file_path TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)
	`); err != nil {
		t.Fatalf("Failed to create legacy vaults table: %v", err)
	}
	if _, err := legacyDB.ExecContext(ctx, `
		INSERT INTO vaults (id, name, description, file_path, created_at, updated_at)
		VALUES
			('legacy-row', 'Legacy Row', 'blank file path', '', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP),
			('file-backed-row', 'File Backed Row', 'valid file path', 'vaults/file-backed.db', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`); err != nil {
		t.Fatalf("Failed to seed legacy vault rows: %v", err)
	}
	if err := legacyDB.Close(); err != nil {
		t.Fatalf("Failed to close legacy database: %v", err)
	}

	db, err := Open(ctx, &Config{
		Path:    dbPath,
		WALMode: false,
	})
	if err != nil {
		t.Fatalf("Failed to reopen migrated database: %v", err)
	}
	defer func() { _ = db.Close() }()

	var legacyCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM vaults WHERE TRIM(COALESCE(file_path, '')) = ''`).Scan(&legacyCount); err != nil {
		t.Fatalf("Failed to count legacy vault rows: %v", err)
	}
	if legacyCount != 0 {
		t.Fatalf("expected blank-file-path vault rows to be removed, got %d", legacyCount)
	}

	var fileBackedCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM vaults WHERE id = 'file-backed-row'`).Scan(&fileBackedCount); err != nil {
		t.Fatalf("Failed to count preserved file-backed vault rows: %v", err)
	}
	if fileBackedCount != 1 {
		t.Fatalf("expected valid file-backed vault row to survive migration, got %d", fileBackedCount)
	}
}

func TestMigration028CreatesUsersAndWorkspaceOwners(t *testing.T) {
	ctx := context.Background()

	tmpDir, err := os.MkdirTemp("", "ori-db-migration-028-*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	dbPath := filepath.Join(tmpDir, "test.db")
	legacyDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("Failed to open legacy database: %v", err)
	}
	if _, err := legacyDB.ExecContext(ctx, `
		CREATE TABLE schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`); err != nil {
		t.Fatalf("Failed to create schema_migrations: %v", err)
	}
	if _, err := legacyDB.ExecContext(ctx, `INSERT INTO schema_migrations (version) VALUES (27)`); err != nil {
		t.Fatalf("Failed to seed schema version 27: %v", err)
	}
	if _, err := legacyDB.ExecContext(ctx, `
		CREATE TABLE workspaces (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)
	`); err != nil {
		t.Fatalf("Failed to create legacy workspaces table: %v", err)
	}
	if _, err := legacyDB.ExecContext(ctx, `
		INSERT INTO workspaces (id, name, created_at, updated_at)
		VALUES ('ws-1', 'Workspace', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`); err != nil {
		t.Fatalf("Failed to seed legacy workspace: %v", err)
	}
	if err := legacyDB.Close(); err != nil {
		t.Fatalf("Failed to close legacy database: %v", err)
	}

	db, err := Open(ctx, &Config{Path: dbPath, WALMode: false})
	if err != nil {
		t.Fatalf("Failed to reopen migrated database: %v", err)
	}
	defer func() { _ = db.Close() }()

	var localUsers int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE id = 'local'`).Scan(&localUsers); err != nil {
		t.Fatalf("Failed to query users table: %v", err)
	}
	if localUsers != 1 {
		t.Fatalf("expected local user row, got %d", localUsers)
	}
	var ownerUserID string
	if err := db.QueryRowContext(ctx, `SELECT owner_user_id FROM workspaces WHERE id = 'ws-1'`).Scan(&ownerUserID); err != nil {
		t.Fatalf("Failed to query owner_user_id: %v", err)
	}
	if ownerUserID != "local" {
		t.Fatalf("expected workspace owner local, got %q", ownerUserID)
	}
	if err := db.runMigration(ctx, 28); err != nil {
		t.Fatalf("migration 28 should be idempotent: %v", err)
	}
}

func TestMigration030AddsPersonalHQColumns(t *testing.T) {
	ctx := context.Background()

	db, err := Open(ctx, &Config{InMemory: true, WALMode: false})
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer func() { _ = db.Close() }()

	var workspaceID string
	var onboardingState string
	var updatedAt sql.NullString
	if err := db.QueryRowContext(ctx, `
		SELECT personal_workspace_id, hq_onboarding_state, hq_onboarding_updated_at
		FROM users WHERE id = 'local'
	`).Scan(&workspaceID, &onboardingState, &updatedAt); err != nil {
		t.Fatalf("Failed to query personal HQ columns: %v", err)
	}
	if workspaceID != "" {
		t.Errorf("expected empty personal_workspace_id default, got %q", workspaceID)
	}
	if onboardingState != "unseen" {
		t.Errorf("expected hq_onboarding_state default 'unseen', got %q", onboardingState)
	}
	if updatedAt.Valid {
		t.Errorf("expected NULL hq_onboarding_updated_at default, got %q", updatedAt.String)
	}

	if err := db.runMigration(ctx, 30); err != nil {
		t.Fatalf("migration 30 should be idempotent: %v", err)
	}
}

func TestMigration031CreatesDailyBriefSchema(t *testing.T) {
	ctx := context.Background()

	db, err := Open(ctx, &Config{InMemory: true, WALMode: false})
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer func() { _ = db.Close() }()

	for _, table := range []string{"daily_brief_config", "daily_brief_revision", "daily_brief_generation_claim", "daily_brief_notification"} {
		exists, err := db.tableExists(ctx, table)
		if err != nil {
			t.Fatalf("tableExists(%s): %v", table, err)
		}
		if !exists {
			t.Fatalf("expected table %s to exist", table)
		}
	}

	if err := db.runMigration(ctx, 31); err != nil {
		t.Fatalf("migration 31 should be idempotent: %v", err)
	}
}

// TestMigration031EnforcesAtMostOneCurrentRevisionPerWorkspace proves the
// partial unique index (not just application logic) rejects a second
// current revision for the same workspace.
func TestMigration031EnforcesAtMostOneCurrentRevisionPerWorkspace(t *testing.T) {
	ctx := context.Background()

	db, err := Open(ctx, &Config{InMemory: true, WALMode: false})
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer func() { _ = db.Close() }()

	insert := func(id string, isCurrent int) error {
		_, err := db.ExecContext(ctx, `
			INSERT INTO daily_brief_revision
				(id, workspace_id, user_id, local_date, revision_number, is_current, trigger_type, status, created_at)
			VALUES (?, 'ws-1', 'local', '2026-07-14', 1, ?, 'manual', 'succeeded', CURRENT_TIMESTAMP)
		`, id, isCurrent)
		return err
	}
	if err := insert("rev-1", 1); err != nil {
		t.Fatalf("first current revision insert: %v", err)
	}
	if err := insert("rev-2", 1); err == nil {
		t.Fatal("expected a second current revision for the same workspace to be rejected")
	}
}

// TestMigration031DedupesNonManualClaimsButNotManual proves the partial
// unique index allows unlimited manual claims for the same date while
// rejecting a second first_open/scheduled claim.
func TestMigration031DedupesNonManualClaimsButNotManual(t *testing.T) {
	ctx := context.Background()

	db, err := Open(ctx, &Config{InMemory: true, WALMode: false})
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer func() { _ = db.Close() }()

	insertClaim := func(id, triggerType, status string) error {
		_, err := db.ExecContext(ctx, `
			INSERT INTO daily_brief_generation_claim
				(id, workspace_id, local_date, trigger_type, status, claimed_at)
			VALUES (?, 'ws-1', '2026-07-14', ?, ?, CURRENT_TIMESTAMP)
		`, id, triggerType, status)
		return err
	}

	if err := insertClaim("claim-1", "first_open", "succeeded"); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if err := insertClaim("claim-2", "scheduled", "pending"); err == nil {
		t.Fatal("expected a second non-manual claim for the same date to be rejected")
	}
	if err := insertClaim("claim-3", "manual", "succeeded"); err != nil {
		t.Fatalf("manual claims must never be deduped: %v", err)
	}
	if err := insertClaim("claim-4", "manual", "succeeded"); err != nil {
		t.Fatalf("a second manual claim for the same date must also be allowed: %v", err)
	}
}

func TestMigration030UpgradesFromPriorSchema(t *testing.T) {
	ctx := context.Background()

	tmpDir, err := os.MkdirTemp("", "ori-db-migration-030-*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	dbPath := filepath.Join(tmpDir, "test.db")
	legacyDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("Failed to open legacy database: %v", err)
	}
	if _, err := legacyDB.ExecContext(ctx, `
		CREATE TABLE schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`); err != nil {
		t.Fatalf("Failed to create schema_migrations: %v", err)
	}
	if _, err := legacyDB.ExecContext(ctx, `INSERT INTO schema_migrations (version) VALUES (29)`); err != nil {
		t.Fatalf("Failed to seed schema version 29: %v", err)
	}
	if _, err := legacyDB.ExecContext(ctx, `
		CREATE TABLE users (
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
		t.Fatalf("Failed to create legacy users table: %v", err)
	}
	if _, err := legacyDB.ExecContext(ctx, `
		INSERT INTO users (id, created_at, updated_at)
		VALUES ('local', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`); err != nil {
		t.Fatalf("Failed to seed legacy local user: %v", err)
	}
	if err := legacyDB.Close(); err != nil {
		t.Fatalf("Failed to close legacy database: %v", err)
	}

	db, err := Open(ctx, &Config{Path: dbPath, WALMode: false})
	if err != nil {
		t.Fatalf("Failed to reopen migrated database: %v", err)
	}
	defer func() { _ = db.Close() }()

	var workspaceID, onboardingState string
	if err := db.QueryRowContext(ctx, `
		SELECT personal_workspace_id, hq_onboarding_state FROM users WHERE id = 'local'
	`).Scan(&workspaceID, &onboardingState); err != nil {
		t.Fatalf("Failed to query personal HQ columns after upgrade: %v", err)
	}
	if workspaceID != "" {
		t.Errorf("expected empty personal_workspace_id after upgrade, got %q", workspaceID)
	}
	if onboardingState != "unseen" {
		t.Errorf("expected hq_onboarding_state 'unseen' after upgrade, got %q", onboardingState)
	}
}

// TestMigration030DoesNotTouchExistingWorkspaces covers PRD task 8.2: the
// migration that adds Personal HQ support to the users table must not
// rename, rewrite, or infer HQ status for a workspace that already existed
// (e.g. one created from the pre-rename "Personal Ops" template) — the
// designation lives only on the users row, and no migration in this
// feature touches the workspaces table at all. Proves the seeded
// workspace row is byte-identical after migrating to the latest schema,
// and that no workspace was auto-designated as the user's HQ.
func TestMigration030DoesNotTouchExistingWorkspaces(t *testing.T) {
	ctx := context.Background()

	tmpDir, err := os.MkdirTemp("", "ori-db-migration-030-workspaces-*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	dbPath := filepath.Join(tmpDir, "test.db")
	legacyDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("Failed to open legacy database: %v", err)
	}
	if _, err := legacyDB.ExecContext(ctx, `
		CREATE TABLE schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`); err != nil {
		t.Fatalf("Failed to create schema_migrations: %v", err)
	}
	if _, err := legacyDB.ExecContext(ctx, `INSERT INTO schema_migrations (version) VALUES (29)`); err != nil {
		t.Fatalf("Failed to seed schema version 29: %v", err)
	}
	if _, err := legacyDB.ExecContext(ctx, `
		CREATE TABLE users (
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
		t.Fatalf("Failed to create legacy users table: %v", err)
	}
	if _, err := legacyDB.ExecContext(ctx, `
		INSERT INTO users (id, created_at, updated_at)
		VALUES ('local', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`); err != nil {
		t.Fatalf("Failed to seed legacy local user: %v", err)
	}
	if _, err := legacyDB.ExecContext(ctx, `
		CREATE TABLE workspaces (
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
		t.Fatalf("Failed to create legacy workspaces table: %v", err)
	}
	const seededSharedData = `{"entry_agent_name":"Personal Chief of Staff","template_id":"personal-ops"}`
	if _, err := legacyDB.ExecContext(ctx, `
		INSERT INTO workspaces (id, name, owner_user_id, shared_data, status, created_at, updated_at)
		VALUES ('ws-personal-ops', 'Personal Ops', 'local', ?, 'active', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, seededSharedData); err != nil {
		t.Fatalf("Failed to seed legacy Personal Ops workspace: %v", err)
	}
	if err := legacyDB.Close(); err != nil {
		t.Fatalf("Failed to close legacy database: %v", err)
	}

	db, err := Open(ctx, &Config{Path: dbPath, WALMode: false})
	if err != nil {
		t.Fatalf("Failed to reopen migrated database: %v", err)
	}
	defer func() { _ = db.Close() }()

	var name, ownerUserID, sharedData, status string
	if err := db.QueryRowContext(ctx, `
		SELECT name, owner_user_id, shared_data, status FROM workspaces WHERE id = 'ws-personal-ops'
	`).Scan(&name, &ownerUserID, &sharedData, &status); err != nil {
		t.Fatalf("Failed to query the seeded workspace after upgrade: %v", err)
	}
	if name != "Personal Ops" {
		t.Errorf("expected the existing workspace's name to be untouched, got %q", name)
	}
	if sharedData != seededSharedData {
		t.Errorf("expected shared_data to be byte-identical after migration, got %q", sharedData)
	}
	if status != "active" {
		t.Errorf("expected status to be untouched, got %q", status)
	}
	if ownerUserID != "local" {
		t.Errorf("expected owner_user_id to be untouched, got %q", ownerUserID)
	}

	var personalWorkspaceID string
	if err := db.QueryRowContext(ctx, `
		SELECT personal_workspace_id FROM users WHERE id = 'local'
	`).Scan(&personalWorkspaceID); err != nil {
		t.Fatalf("Failed to query personal_workspace_id after upgrade: %v", err)
	}
	if personalWorkspaceID != "" {
		t.Errorf("expected migration to designate zero existing workspaces as HQ, got personal_workspace_id=%q", personalWorkspaceID)
	}
}

func TestMigration046CreatesPersonalAssistantFoundationSchema(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, &Config{InMemory: true, WALMode: false})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer func() { _ = db.Close() }()

	for _, table := range []string{"personal_assistant_state", "personal_assistant_assignment"} {
		exists, existsErr := db.tableExists(ctx, table)
		if existsErr != nil {
			t.Fatalf("tableExists(%s): %v", table, existsErr)
		}
		if !exists {
			t.Fatalf("expected table %s", table)
		}
	}

	stateColumns := map[string]bool{
		"user_id": false, "assistant_id": false, "status": false,
		"display_name": false, "appearance_json": false,
		"hq_workspace_id": false, "hq_entry_agent_instance_id": false,
		"global_agent_profile_name": false, "mandate": false,
		"focus_areas_json": false, "first_assignment_status": false,
		"last_hire_request_id": false, "hire_payload_hash": false,
		"hire_payload_json": false, "repair_step": false,
		"rename_from_name": false, "rename_to_name": false, "rename_step": false, "state_version": false,
		"hired_at": false, "created_at": false, "updated_at": false,
	}
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(personal_assistant_state)`)
	if err != nil {
		t.Fatalf("inspect personal_assistant_state: %v", err)
	}
	for rows.Next() {
		var cid, notNull, pk int
		var name, colType string
		var defaultValue sql.NullString
		if scanErr := rows.Scan(&cid, &name, &colType, &notNull, &defaultValue, &pk); scanErr != nil {
			_ = rows.Close()
			t.Fatalf("scan personal_assistant_state: %v", scanErr)
		}
		if _, ok := stateColumns[name]; ok {
			stateColumns[name] = true
		}
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("close schema rows: %v", err)
	}
	for name, found := range stateColumns {
		if !found {
			t.Errorf("personal_assistant_state column %s does not exist", name)
		}
	}

	now := "2026-01-01T00:00:00Z"
	insertState := func(userID, assistantID string) error {
		_, insertErr := db.ExecContext(ctx, `
			INSERT INTO personal_assistant_state
				(user_id, assistant_id, created_at, updated_at)
			VALUES (?, ?, ?, ?)
		`, userID, assistantID, now, now)
		return insertErr
	}
	if err := insertState("user-a", "assistant-a"); err != nil {
		t.Fatalf("insert first relationship: %v", err)
	}
	if err := insertState("user-a", "assistant-b"); err == nil {
		t.Fatal("expected one-assistant-per-user constraint")
	}
	if err := insertState("user-b", "assistant-a"); err == nil {
		t.Fatal("expected globally unique stable assistant ID")
	}
	if err := db.runMigration(ctx, 46); err != nil {
		t.Fatalf("migration 46 should be idempotent: %v", err)
	}
}

func TestMigration046UpgradesPriorSchemaWithoutChangingExistingRows(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "prior.db")
	legacyDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open prior database: %v", err)
	}
	statements := []string{
		`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at DATETIME DEFAULT CURRENT_TIMESTAMP)`,
		`INSERT INTO schema_migrations (version) VALUES (45)`,
		`CREATE TABLE preserved_rows (id TEXT PRIMARY KEY, body TEXT NOT NULL)`,
		`INSERT INTO preserved_rows (id, body) VALUES ('existing', '{"byte":"stable"}')`,
	}
	for _, stmt := range statements {
		if _, err := legacyDB.ExecContext(ctx, stmt); err != nil {
			_ = legacyDB.Close()
			t.Fatalf("prepare prior database: %v", err)
		}
	}
	if err := legacyDB.Close(); err != nil {
		t.Fatalf("close prior database: %v", err)
	}

	db, err := Open(ctx, &Config{Path: dbPath, WALMode: false})
	if err != nil {
		t.Fatalf("migrate prior database: %v", err)
	}
	defer func() { _ = db.Close() }()

	var body string
	if err := db.QueryRowContext(ctx, `SELECT body FROM preserved_rows WHERE id = 'existing'`).Scan(&body); err != nil {
		t.Fatalf("read preserved row: %v", err)
	}
	if body != `{"byte":"stable"}` {
		t.Fatalf("existing row changed: %q", body)
	}
	for _, table := range []string{"personal_assistant_state", "personal_assistant_assignment"} {
		exists, existsErr := db.tableExists(ctx, table)
		if existsErr != nil || !exists {
			t.Fatalf("migrated table %s: exists=%v err=%v", table, exists, existsErr)
		}
	}
	version, err := db.GetSchemaVersion(ctx)
	if err != nil || version != 51 {
		t.Fatalf("schema version = %d, %v; want 51", version, err)
	}
}

func TestInTransaction(t *testing.T) {
	ctx := context.Background()

	cfg := &Config{
		InMemory: true,
		WALMode:  false,
	}

	db, err := Open(ctx, cfg)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Test successful transaction
	err = db.InTransaction(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO workspaces (id, name, created_at, updated_at)
			VALUES ('workspace-1', 'Test Workspace', datetime('now'), datetime('now'))
		`)
		return err
	})
	if err != nil {
		t.Fatalf("Transaction failed: %v", err)
	}

	// Verify the insert was committed
	var name string
	err = db.QueryRowContext(ctx, "SELECT name FROM workspaces WHERE id = 'workspace-1'").Scan(&name)
	if err != nil {
		t.Fatalf("Failed to query inserted row: %v", err)
	}
	if name != "Test Workspace" {
		t.Errorf("Expected 'Test Workspace', got %s", name)
	}

	// Test rolled back transaction
	err = db.InTransaction(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO workspaces (id, name, created_at, updated_at)
			VALUES ('workspace-2', 'Another Workspace', datetime('now'), datetime('now'))
		`)
		if err != nil {
			return err
		}
		// Return an error to trigger rollback
		return context.Canceled
	})
	if err == nil {
		t.Error("Expected transaction to fail")
	}

	// Verify the insert was rolled back
	var count int
	err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM workspaces WHERE id = 'workspace-2'").Scan(&count)
	if err != nil {
		t.Fatalf("Failed to count rows: %v", err)
	}
	if count != 0 {
		t.Errorf("Expected 0 rows for workspace-2, got %d", count)
	}
}

func TestIndexesExist(t *testing.T) {
	ctx := context.Background()

	cfg := &Config{
		InMemory: true,
		WALMode:  false,
	}

	db, err := Open(ctx, cfg)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer func() { _ = db.Close() }()

	expectedIndexes := []string{
		"idx_sessions_agent_name",
		"idx_sessions_workspace_id",
		"idx_sessions_updated_at",
		"idx_sessions_created_at",
		"idx_messages_session_id",
		"idx_messages_created_at",
		"idx_session_tags_tag",
		"idx_workspaces_parent_id",
	}

	for _, idx := range expectedIndexes {
		var name string
		err := db.QueryRowContext(ctx,
			"SELECT name FROM sqlite_master WHERE type='index' AND name=?", idx).Scan(&name)
		if err != nil {
			t.Errorf("Index %s does not exist: %v", idx, err)
		}
	}
}

func TestCascadeDelete(t *testing.T) {
	ctx := context.Background()

	cfg := &Config{
		InMemory: true,
		WALMode:  false,
	}

	db, err := Open(ctx, cfg)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Insert a session
	_, err = db.ExecContext(ctx, `
		INSERT INTO sessions (id, title, agent_name, created_at, updated_at)
		VALUES ('session-1', 'Test Session', 'assistant', datetime('now'), datetime('now'))
	`)
	if err != nil {
		t.Fatalf("Failed to insert session: %v", err)
	}

	// Insert messages for the session
	_, err = db.ExecContext(ctx, `
		INSERT INTO messages (id, session_id, role, content, created_at)
		VALUES
			('msg-1', 'session-1', 'user', 'Hello', datetime('now')),
			('msg-2', 'session-1', 'assistant', 'Hi there!', datetime('now'))
	`)
	if err != nil {
		t.Fatalf("Failed to insert messages: %v", err)
	}

	// Insert tags for the session
	_, err = db.ExecContext(ctx, `
		INSERT INTO session_tags (session_id, tag)
		VALUES
			('session-1', 'test'),
			('session-1', 'demo')
	`)
	if err != nil {
		t.Fatalf("Failed to insert tags: %v", err)
	}

	// Delete the session
	_, err = db.ExecContext(ctx, "DELETE FROM sessions WHERE id = 'session-1'")
	if err != nil {
		t.Fatalf("Failed to delete session: %v", err)
	}

	// Verify messages were deleted (CASCADE)
	var msgCount int
	err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM messages WHERE session_id = 'session-1'").Scan(&msgCount)
	if err != nil {
		t.Fatalf("Failed to count messages: %v", err)
	}
	if msgCount != 0 {
		t.Errorf("Expected 0 messages after cascade delete, got %d", msgCount)
	}

	// Verify tags were deleted (CASCADE)
	var tagCount int
	err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM session_tags WHERE session_id = 'session-1'").Scan(&tagCount)
	if err != nil {
		t.Fatalf("Failed to count tags: %v", err)
	}
	if tagCount != 0 {
		t.Errorf("Expected 0 tags after cascade delete, got %d", tagCount)
	}
}

// TestFreshSchemaHasInstalledCapabilitiesColumn proves a database created from
// scratch carries installed_capabilities_json (PRD FR-4). The baseline CREATE
// TABLE declares it and migration 34 re-adds it defensively; the duplicate-column
// guard makes that a no-op, so a fresh open must still succeed and expose the
// column with its empty-collection default.
func TestFreshSchemaHasInstalledCapabilitiesColumn(t *testing.T) {
	ctx := context.Background()

	db, err := Open(ctx, &Config{Path: ":memory:", WALMode: false})
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer func() { _ = db.Close() }()

	if _, err := db.ExecContext(ctx, `
		INSERT INTO workspaces (id, name, created_at, updated_at)
		VALUES ('ws-fresh', 'Fresh', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`); err != nil {
		t.Fatalf("Failed to insert workspace: %v", err)
	}

	var installed string
	if err := db.QueryRowContext(ctx, `
		SELECT installed_capabilities_json FROM workspaces WHERE id = 'ws-fresh'
	`).Scan(&installed); err != nil {
		t.Fatalf("Failed to read installed_capabilities_json: %v", err)
	}
	if installed != "[]" {
		t.Errorf("expected empty-collection default '[]', got %q", installed)
	}
}

// TestMigration034UpgradesFromPriorSchema covers the upgrade path: a database
// stamped at version 33 has no installed_capabilities_json column. Migration 34
// must add it without disturbing the existing row, and every pre-existing
// workspace must read back as "no capabilities installed" — never as a phantom
// install (PRD FR-125, FR-136: legacy state alone must not imply an install).
func TestMigration034UpgradesFromPriorSchema(t *testing.T) {
	ctx := context.Background()

	tmpDir, err := os.MkdirTemp("", "ori-db-migration-034-*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	dbPath := filepath.Join(tmpDir, "test.db")
	legacyDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("Failed to open legacy database: %v", err)
	}
	if _, err := legacyDB.ExecContext(ctx, `
		CREATE TABLE schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`); err != nil {
		t.Fatalf("Failed to create schema_migrations: %v", err)
	}
	if _, err := legacyDB.ExecContext(ctx, `INSERT INTO schema_migrations (version) VALUES (33)`); err != nil {
		t.Fatalf("Failed to seed schema version 33: %v", err)
	}
	// A pre-034 workspaces table: no installed_capabilities_json column.
	if _, err := legacyDB.ExecContext(ctx, `
		CREATE TABLE workspaces (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			description TEXT DEFAULT '',
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			tasks_json TEXT DEFAULT '[]',
			status TEXT DEFAULT 'active'
		)
	`); err != nil {
		t.Fatalf("Failed to create legacy workspaces table: %v", err)
	}
	if _, err := legacyDB.ExecContext(ctx, `
		INSERT INTO workspaces (id, name, description, created_at, updated_at, tasks_json, status)
		VALUES ('ws-legacy', 'Downloads Janitor', 'pre-existing', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, '[{"id":"t1"}]', 'active')
	`); err != nil {
		t.Fatalf("Failed to seed legacy workspace: %v", err)
	}
	if err := legacyDB.Close(); err != nil {
		t.Fatalf("Failed to close legacy database: %v", err)
	}

	db, err := Open(ctx, &Config{Path: dbPath, WALMode: false})
	if err != nil {
		t.Fatalf("Failed to reopen migrated database: %v", err)
	}
	defer func() { _ = db.Close() }()

	var name, description, tasks string
	var installed sql.NullString
	if err := db.QueryRowContext(ctx, `
		SELECT name, description, tasks_json, installed_capabilities_json
		FROM workspaces WHERE id = 'ws-legacy'
	`).Scan(&name, &description, &tasks, &installed); err != nil {
		t.Fatalf("Failed to query workspace after upgrade: %v", err)
	}

	// Existing data is untouched: the migration is purely additive.
	if name != "Downloads Janitor" || description != "pre-existing" {
		t.Errorf("migration altered existing workspace metadata: name=%q description=%q", name, description)
	}
	if tasks != `[{"id":"t1"}]` {
		t.Errorf("migration altered existing task state: %q", tasks)
	}

	// The upgraded row reports no installs. ALTER TABLE ... DEFAULT backfills
	// existing rows with the default in SQLite, but NULL would be equally
	// acceptable — both must read as "nothing installed", which is what the
	// session store's `.Valid && != ""` guard relies on.
	if installed.Valid && installed.String != "[]" && installed.String != "" {
		t.Errorf("legacy workspace gained a capability install: %q", installed.String)
	}
}

// TestMigration038CreatesWorkspaceMapSchema checks that a fresh database gets
// the coordinate-map tables, and — the part that actually matters — that the
// map schema is a separate domain from the workspace record it points at
// (#292 FR-5, FR-104).
func TestMigration038CreatesWorkspaceMapSchema(t *testing.T) {
	ctx := context.Background()

	db, err := Open(ctx, &Config{Path: ":memory:", WALMode: false})
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer func() { _ = db.Close() }()

	for _, table := range []string{"workspace_map_layouts", "workspace_map_positions"} {
		exists, err := db.tableExists(ctx, table)
		if err != nil {
			t.Fatalf("tableExists(%s): %v", table, err)
		}
		if !exists {
			t.Fatalf("fresh database is missing %s", table)
		}
	}

	// The per-workspace CanvasLayout column is untouched by this feature.
	var layoutColumns int
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(1) FROM pragma_table_info('workspaces') WHERE name = 'layout'
	`).Scan(&layoutColumns); err != nil {
		t.Fatalf("inspect workspaces columns: %v", err)
	}
	if layoutColumns != 1 {
		t.Errorf("workspaces.layout column count = %d, want 1 (the map must not disturb it)", layoutColumns)
	}
}

// TestMigration038PositionLifecycleFollowsTheWorkspace proves the two lifecycle
// rules the foreign key exists for: a trashed workspace keeps its anchor so a
// restore puts the building back where it was (FR-26, FR-27), and a permanently
// deleted one takes exactly its own anchor with it (FR-28).
func TestMigration038PositionLifecycleFollowsTheWorkspace(t *testing.T) {
	ctx := context.Background()

	db, err := Open(ctx, &Config{Path: ":memory:", WALMode: false})
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer func() { _ = db.Close() }()

	for _, id := range []string{"ws-keep", "ws-drop"} {
		if _, err := db.ExecContext(ctx, `
			INSERT INTO workspaces (id, name, created_at, updated_at)
			VALUES (?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		`, id, id); err != nil {
			t.Fatalf("seed workspace %s: %v", id, err)
		}
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO workspace_map_layouts (user_id, schema_version, revision, snap_to_grid, created_at, updated_at)
		VALUES ('local', 1, 1, 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`); err != nil {
		t.Fatalf("seed layout: %v", err)
	}
	for _, id := range []string{"ws-keep", "ws-drop"} {
		if _, err := db.ExecContext(ctx, `
			INSERT INTO workspace_map_positions (user_id, workspace_id, x, y, updated_at)
			VALUES ('local', ?, 10, 20, CURRENT_TIMESTAMP)
		`, id); err != nil {
			t.Fatalf("seed position %s: %v", id, err)
		}
	}

	// Soft delete leaves the row in place, so the anchor survives.
	if _, err := db.ExecContext(ctx, `UPDATE workspaces SET status = 'trashed' WHERE id = 'ws-drop'`); err != nil {
		t.Fatalf("trash workspace: %v", err)
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(1) FROM workspace_map_positions`).Scan(&count); err != nil {
		t.Fatalf("count positions: %v", err)
	}
	if count != 2 {
		t.Fatalf("positions after trash = %d, want 2 (a trashed workspace keeps its anchor)", count)
	}

	// Permanent deletion cascades away exactly one anchor.
	if _, err := db.ExecContext(ctx, `DELETE FROM workspaces WHERE id = 'ws-drop'`); err != nil {
		t.Fatalf("delete workspace: %v", err)
	}
	var remaining string
	if err := db.QueryRowContext(ctx, `SELECT workspace_id FROM workspace_map_positions`).Scan(&remaining); err != nil {
		t.Fatalf("read remaining position: %v", err)
	}
	if remaining != "ws-keep" {
		t.Errorf("remaining anchor = %q, want ws-keep", remaining)
	}
}

// TestMigration038UpgradesFromPriorSchema proves an existing database gains the
// map tables without its workspace data being touched.
func TestMigration038UpgradesFromPriorSchema(t *testing.T) {
	ctx := context.Background()

	tmpDir, err := os.MkdirTemp("", "ori-db-migration-038-*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	dbPath := filepath.Join(tmpDir, "test.db")
	legacyDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("Failed to open legacy database: %v", err)
	}
	if _, err := legacyDB.ExecContext(ctx, `
		CREATE TABLE schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`); err != nil {
		t.Fatalf("Failed to create schema_migrations: %v", err)
	}
	if _, err := legacyDB.ExecContext(ctx, `INSERT INTO schema_migrations (version) VALUES (37)`); err != nil {
		t.Fatalf("Failed to seed schema version 37: %v", err)
	}
	if _, err := legacyDB.ExecContext(ctx, `
		CREATE TABLE workspaces (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			parent_id TEXT,
			layout TEXT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			status TEXT DEFAULT 'active'
		)
	`); err != nil {
		t.Fatalf("Failed to create legacy workspaces table: %v", err)
	}
	if _, err := legacyDB.ExecContext(ctx, `
		INSERT INTO workspaces (id, name, parent_id, layout, created_at, updated_at, status)
		VALUES ('ws-legacy', 'Alpha', 'grp-1', '{"pan":{"x":4}}', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, 'active')
	`); err != nil {
		t.Fatalf("Failed to seed legacy workspace: %v", err)
	}
	if err := legacyDB.Close(); err != nil {
		t.Fatalf("Failed to close legacy database: %v", err)
	}

	db, err := Open(ctx, &Config{Path: dbPath, WALMode: false})
	if err != nil {
		t.Fatalf("Failed to reopen migrated database: %v", err)
	}
	defer func() { _ = db.Close() }()

	exists, err := db.tableExists(ctx, "workspace_map_positions")
	if err != nil {
		t.Fatalf("tableExists: %v", err)
	}
	if !exists {
		t.Fatal("upgraded database is missing workspace_map_positions")
	}

	var name, parent, layout string
	if err := db.QueryRowContext(ctx, `
		SELECT name, parent_id, layout FROM workspaces WHERE id = 'ws-legacy'
	`).Scan(&name, &parent, &layout); err != nil {
		t.Fatalf("query workspace after upgrade: %v", err)
	}
	if name != "Alpha" || parent != "grp-1" {
		t.Errorf("migration altered workspace metadata: name=%q parent=%q", name, parent)
	}
	if layout != `{"pan":{"x":4}}` {
		t.Errorf("migration altered the per-workspace CanvasLayout: %q", layout)
	}
}

// TestMigration042CreatesGroupPresentationSchema proves a fresh database gets
// the district presentation table with its documented defaults, and that the
// table carries no hierarchy vocabulary at all (#346 FR-5, FR-173).
func TestMigration042CreatesGroupPresentationSchema(t *testing.T) {
	ctx := context.Background()

	db, err := Open(ctx, &Config{Path: ":memory:", WALMode: false})
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer func() { _ = db.Close() }()

	exists, err := db.tableExists(ctx, "workspace_map_group_presentations")
	if err != nil {
		t.Fatalf("tableExists: %v", err)
	}
	if !exists {
		t.Fatal("fresh database is missing workspace_map_group_presentations")
	}

	// A row inserted with only its keys must read back as the documented safe
	// default: automatic sizing, no frame, expanded, default appearance
	// (FR-31, FR-101, FR-127).
	seedGroupPresentationFixtures(ctx, t, db, "grp-a")
	if _, err := db.ExecContext(ctx, `
		INSERT INTO workspace_map_group_presentations (user_id, group_id, created_at, updated_at)
		VALUES ('local', 'grp-a', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`); err != nil {
		t.Fatalf("insert bare presentation row: %v", err)
	}
	var (
		sizingMode  string
		collapsed   int
		accent      string
		theme       string
		frameWidth  sql.NullFloat64
		frameHeight sql.NullFloat64
	)
	if err := db.QueryRowContext(ctx, `
		SELECT sizing_mode, collapsed, accent, theme, frame_width, frame_height
		FROM workspace_map_group_presentations WHERE user_id = 'local' AND group_id = 'grp-a'
	`).Scan(&sizingMode, &collapsed, &accent, &theme, &frameWidth, &frameHeight); err != nil {
		t.Fatalf("read presentation defaults: %v", err)
	}
	if sizingMode != "auto" || collapsed != 0 || accent != "default" || theme != "default" {
		t.Errorf("defaults = %q/%d/%q/%q, want auto/0/default/default", sizingMode, collapsed, accent, theme)
	}
	if frameWidth.Valid || frameHeight.Valid {
		t.Error("an automatic district must store no frame — a read would otherwise have to write one")
	}

	// No presentation column may name a hierarchy field (FR-5, FR-62).
	rows, err := db.QueryContext(ctx, `SELECT name FROM pragma_table_info('workspace_map_group_presentations')`)
	if err != nil {
		t.Fatalf("inspect presentation columns: %v", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var column string
		if err := rows.Scan(&column); err != nil {
			t.Fatalf("scan column: %v", err)
		}
		switch column {
		case "parent_id", "order_index", "status", "designation":
			t.Errorf("presentation table exposes hierarchy field %q", column)
		}
	}
}

// TestMigration042PresentationLifecycleFollowsTheGroup proves the same two
// lifecycle rules migration 38 established for anchors: a trashed group keeps
// its district presentation for restore, and a permanently deleted one takes
// exactly its own row with it (#346 FR-183).
func TestMigration042PresentationLifecycleFollowsTheGroup(t *testing.T) {
	ctx := context.Background()

	db, err := Open(ctx, &Config{Path: ":memory:", WALMode: false})
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer func() { _ = db.Close() }()

	seedGroupPresentationFixtures(ctx, t, db, "grp-keep", "grp-drop")
	for _, id := range []string{"grp-keep", "grp-drop"} {
		if _, err := db.ExecContext(ctx, `
			INSERT INTO workspace_map_group_presentations
				(user_id, group_id, sizing_mode, frame_x, frame_y, frame_width, frame_height, collapsed, accent, theme, created_at, updated_at)
			VALUES ('local', ?, 'custom', 10, 20, 400, 300, 1, 'moss', 'blueprint', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		`, id); err != nil {
			t.Fatalf("seed presentation %s: %v", id, err)
		}
	}

	if _, err := db.ExecContext(ctx, `UPDATE workspaces SET status = 'trashed' WHERE id = 'grp-drop'`); err != nil {
		t.Fatalf("trash group: %v", err)
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(1) FROM workspace_map_group_presentations`).Scan(&count); err != nil {
		t.Fatalf("count presentations: %v", err)
	}
	if count != 2 {
		t.Fatalf("presentations after trash = %d, want 2 (a trashed group keeps its district)", count)
	}

	if _, err := db.ExecContext(ctx, `DELETE FROM workspaces WHERE id = 'grp-drop'`); err != nil {
		t.Fatalf("delete group: %v", err)
	}
	var remaining string
	if err := db.QueryRowContext(ctx, `SELECT group_id FROM workspace_map_group_presentations`).Scan(&remaining); err != nil {
		t.Fatalf("read remaining presentation: %v", err)
	}
	if remaining != "grp-keep" {
		t.Errorf("remaining district = %q, want grp-keep", remaining)
	}

	// And the layout-side cascade: dropping the user's layout drops their
	// districts with it, exactly as it drops their anchors.
	if _, err := db.ExecContext(ctx, `DELETE FROM workspace_map_layouts WHERE user_id = 'local'`); err != nil {
		t.Fatalf("delete layout: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(1) FROM workspace_map_group_presentations`).Scan(&count); err != nil {
		t.Fatalf("count presentations after layout delete: %v", err)
	}
	if count != 0 {
		t.Errorf("presentations after layout delete = %d, want 0", count)
	}
}

// TestMigration042PresentationRequiresAnExistingGroup proves the foreign key is
// live: a district can only exist for a real workspace record, so a corrupt or
// hostile group_id cannot accumulate orphan presentation rows.
func TestMigration042PresentationRequiresAnExistingGroup(t *testing.T) {
	ctx := context.Background()

	db, err := Open(ctx, &Config{Path: ":memory:", WALMode: false})
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer func() { _ = db.Close() }()

	seedGroupPresentationFixtures(ctx, t, db)
	if _, err := db.ExecContext(ctx, `
		INSERT INTO workspace_map_group_presentations (user_id, group_id, created_at, updated_at)
		VALUES ('local', 'ghost-group', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`); err == nil {
		t.Fatal("inserting a district for a workspace that does not exist should fail the foreign key")
	}
}

// seedGroupPresentationFixtures creates the layout row every district hangs off
// plus the given group workspaces.
func seedGroupPresentationFixtures(ctx context.Context, t *testing.T, db *DB, groupIDs ...string) {
	t.Helper()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO workspace_map_layouts (user_id, schema_version, revision, snap_to_grid, created_at, updated_at)
		VALUES ('local', 1, 1, 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`); err != nil {
		t.Fatalf("seed layout: %v", err)
	}
	for _, id := range groupIDs {
		if _, err := db.ExecContext(ctx, `
			INSERT INTO workspaces (id, name, created_at, updated_at)
			VALUES (?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		`, id, id); err != nil {
			t.Fatalf("seed group %s: %v", id, err)
		}
	}
}

// TestMigration039CreatesWorkspacePlanSchema proves a fresh database gets every
// Plan table the Workspace Planning Workflow persists (PRD FR-1, FR-16).
func TestMigration039CreatesWorkspacePlanSchema(t *testing.T) {
	ctx := context.Background()

	db, err := Open(ctx, &Config{Path: ":memory:", WALMode: false})
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer func() { _ = db.Close() }()

	for _, table := range []string{
		"workspace_plans",
		"workspace_plan_versions",
		"workspace_plan_clarifications",
		"workspace_plan_approvals",
		"workspace_plan_task_links",
		"workspace_plan_run_links",
		"workspace_plan_activity",
		"workspace_plan_draft_snapshots",
	} {
		exists, err := db.tableExists(ctx, table)
		if err != nil {
			t.Fatalf("tableExists(%s): %v", table, err)
		}
		if !exists {
			t.Errorf("fresh database is missing %s", table)
		}
	}

	for _, index := range []string{
		"idx_workspace_plans_workspace_activity",
		"idx_workspace_plan_versions_plan",
		"idx_workspace_plan_approvals_idempotency",
		"idx_workspace_plan_task_links_task",
		"idx_workspace_plan_task_links_materialized",
		"idx_workspace_plan_run_links_run",
		"idx_workspace_plan_activity_plan_sequence",
	} {
		var name string
		if err := db.QueryRowContext(ctx,
			"SELECT name FROM sqlite_master WHERE type='index' AND name=?", index).Scan(&name); err != nil {
			t.Errorf("index %s does not exist: %v", index, err)
		}
	}
}

// TestMigration039PlanRecordsFollowTheWorkspace proves Plans use the existing
// workspace-deletion policy rather than inventing their own: a permanently
// deleted workspace takes exactly its own Plans and their dependent rows with
// it, and leaves every other workspace's Plans alone (FR-17).
func TestMigration039PlanRecordsFollowTheWorkspace(t *testing.T) {
	ctx := context.Background()

	db, err := Open(ctx, &Config{Path: ":memory:", WALMode: false})
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer func() { _ = db.Close() }()

	for _, id := range []string{"ws-keep", "ws-drop"} {
		if _, err := db.ExecContext(ctx, `
			INSERT INTO workspaces (id, name, created_at, updated_at)
			VALUES (?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		`, id, id); err != nil {
			t.Fatalf("seed workspace %s: %v", id, err)
		}
		if _, err := db.ExecContext(ctx, `
			INSERT INTO workspace_plans (id, workspace_id, status, created_at, updated_at, last_activity_at)
			VALUES (?, ?, 'draft', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		`, "plan-"+id, id); err != nil {
			t.Fatalf("seed plan for %s: %v", id, err)
		}
		if _, err := db.ExecContext(ctx, `
			INSERT INTO workspace_plan_versions
				(plan_id, version, workspace_id, content_json, content_hash, status, created_at)
			VALUES (?, 1, ?, '{}', 'hash', 'in_review', CURRENT_TIMESTAMP)
		`, "plan-"+id, id); err != nil {
			t.Fatalf("seed version for %s: %v", id, err)
		}
	}

	if _, err := db.ExecContext(ctx, `DELETE FROM workspaces WHERE id = 'ws-drop'`); err != nil {
		t.Fatalf("delete workspace: %v", err)
	}

	var remainingPlan string
	if err := db.QueryRowContext(ctx, `SELECT id FROM workspace_plans`).Scan(&remainingPlan); err != nil {
		t.Fatalf("read remaining plan: %v", err)
	}
	if remainingPlan != "plan-ws-keep" {
		t.Errorf("remaining plan = %q, want plan-ws-keep", remainingPlan)
	}

	var remainingVersions int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM workspace_plan_versions`).Scan(&remainingVersions); err != nil {
		t.Fatalf("count remaining versions: %v", err)
	}
	if remainingVersions != 1 {
		t.Errorf("remaining plan versions = %d, want 1 (the deleted workspace's version must cascade)", remainingVersions)
	}
}

// TestMigration039RejectsDuplicateMaterializedTaskLinks proves the storage-level
// backstop against a duplicate Task tree: the same approved Plan item cannot be
// linked to two Tasks, however many times an approval is retried or raced
// (FR-91, FR-178, SM-2). Corrective follow-up links are deliberately exempt,
// because they add a second Task for an item whose original is immutable
// (FR-78).
func TestMigration039RejectsDuplicateMaterializedTaskLinks(t *testing.T) {
	ctx := context.Background()

	db, err := Open(ctx, &Config{Path: ":memory:", WALMode: false})
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer func() { _ = db.Close() }()

	if _, err := db.ExecContext(ctx, `
		INSERT INTO workspaces (id, name, created_at, updated_at)
		VALUES ('ws-1', 'Alpha', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO workspace_plans (id, workspace_id, status, created_at, updated_at, last_activity_at)
		VALUES ('plan-1', 'ws-1', 'approved', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`); err != nil {
		t.Fatalf("seed plan: %v", err)
	}

	insertLink := func(taskID, role string) error {
		_, err := db.ExecContext(ctx, `
			INSERT INTO workspace_plan_task_links
				(plan_id, task_id, workspace_id, version, approval_id, group_id, item_id, role, created_at)
			VALUES ('plan-1', ?, 'ws-1', 1, 'apr-1', 'grp-1', 'itm-1', ?, CURRENT_TIMESTAMP)
		`, taskID, role)
		return err
	}

	if err := insertLink("task-1", "item"); err != nil {
		t.Fatalf("first materialized link: %v", err)
	}
	if err := insertLink("task-2", "item"); err == nil {
		t.Error("a second Task for the same approved Plan item was accepted; the unique index must reject it")
	}
	// The same item may gain a corrective follow-up Task, because the original
	// Task stays immutable rather than being rewritten.
	if err := insertLink("task-3", "follow_up"); err != nil {
		t.Errorf("follow-up link rejected: %v", err)
	}
}

// TestMigration039RejectsDuplicateApprovalIdempotencyKeys proves a retried
// approval request cannot create a second approval record for the same Plan
// (FR-73, FR-178).
func TestMigration039RejectsDuplicateApprovalIdempotencyKeys(t *testing.T) {
	ctx := context.Background()

	db, err := Open(ctx, &Config{Path: ":memory:", WALMode: false})
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer func() { _ = db.Close() }()

	if _, err := db.ExecContext(ctx, `
		INSERT INTO workspaces (id, name, created_at, updated_at)
		VALUES ('ws-1', 'Alpha', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO workspace_plans (id, workspace_id, status, created_at, updated_at, last_activity_at)
		VALUES ('plan-1', 'ws-1', 'in_review', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`); err != nil {
		t.Fatalf("seed plan: %v", err)
	}

	insertApproval := func(id string) error {
		_, err := db.ExecContext(ctx, `
			INSERT INTO workspace_plan_approvals
				(id, plan_id, workspace_id, version, content_hash, effect, idempotency_key, created_at)
			VALUES (?, 'plan-1', 'ws-1', 1, 'hash-1', 'create_tasks', 'key-1', CURRENT_TIMESTAMP)
		`, id)
		return err
	}

	if err := insertApproval("apr-1"); err != nil {
		t.Fatalf("first approval: %v", err)
	}
	if err := insertApproval("apr-2"); err == nil {
		t.Error("a duplicate approval idempotency key was accepted; the unique index must reject it")
	}
}

// TestMigration039UpgradesFromPriorSchema proves an existing database gains the
// Plan tables without its workspace data being touched.
func TestMigration039UpgradesFromPriorSchema(t *testing.T) {
	ctx := context.Background()

	tmpDir, err := os.MkdirTemp("", "ori-db-migration-039-*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	dbPath := filepath.Join(tmpDir, "test.db")
	legacyDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("Failed to open legacy database: %v", err)
	}
	if _, err := legacyDB.ExecContext(ctx, `
		CREATE TABLE schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`); err != nil {
		t.Fatalf("Failed to create schema_migrations: %v", err)
	}
	if _, err := legacyDB.ExecContext(ctx, `INSERT INTO schema_migrations (version) VALUES (38)`); err != nil {
		t.Fatalf("Failed to seed schema version 38: %v", err)
	}
	if _, err := legacyDB.ExecContext(ctx, `
		CREATE TABLE workspaces (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			parent_id TEXT,
			layout TEXT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			status TEXT DEFAULT 'active'
		)
	`); err != nil {
		t.Fatalf("Failed to create legacy workspaces table: %v", err)
	}
	if _, err := legacyDB.ExecContext(ctx, `
		INSERT INTO workspaces (id, name, parent_id, layout, created_at, updated_at, status)
		VALUES ('ws-legacy', 'Alpha', 'grp-1', '{"pan":{"x":4}}', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, 'active')
	`); err != nil {
		t.Fatalf("Failed to seed legacy workspace: %v", err)
	}
	if err := legacyDB.Close(); err != nil {
		t.Fatalf("Failed to close legacy database: %v", err)
	}

	db, err := Open(ctx, &Config{Path: dbPath, WALMode: false})
	if err != nil {
		t.Fatalf("Failed to reopen migrated database: %v", err)
	}
	defer func() { _ = db.Close() }()

	exists, err := db.tableExists(ctx, "workspace_plans")
	if err != nil {
		t.Fatalf("tableExists: %v", err)
	}
	if !exists {
		t.Fatal("upgraded database is missing workspace_plans")
	}

	var name, parent, layout string
	if err := db.QueryRowContext(ctx, `
		SELECT name, parent_id, layout FROM workspaces WHERE id = 'ws-legacy'
	`).Scan(&name, &parent, &layout); err != nil {
		t.Fatalf("query workspace after upgrade: %v", err)
	}
	if name != "Alpha" || parent != "grp-1" || layout != `{"pan":{"x":4}}` {
		t.Errorf("migration altered workspace data: name=%q parent=%q layout=%q", name, parent, layout)
	}
}

// TestMigration039IsIdempotentOnAlreadyMigratedSchema proves re-running the
// migration body against a database that already has the Plan schema succeeds
// rather than failing startup.
func TestMigration039IsIdempotentOnAlreadyMigratedSchema(t *testing.T) {
	ctx := context.Background()

	db, err := Open(ctx, &Config{Path: ":memory:", WALMode: false})
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer func() { _ = db.Close() }()

	if err := db.migration039WorkspacePlans(ctx); err != nil {
		t.Fatalf("re-running migration 39 failed: %v", err)
	}
}

// TestMigration040CreatesTheExecutionSlotSchema proves a fresh database gets
// the slot, its queue, and the generation counter (PRD FR-106).
func TestMigration040CreatesTheExecutionSlotSchema(t *testing.T) {
	ctx := context.Background()

	db, err := Open(ctx, &Config{Path: ":memory:", WALMode: false})
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer func() { _ = db.Close() }()

	for _, table := range []string{
		"workspace_plan_execution_slots",
		"workspace_plan_execution_queue",
		"workspace_plan_execution_generations",
	} {
		exists, err := db.tableExists(ctx, table)
		if err != nil {
			t.Fatalf("tableExists(%s): %v", table, err)
		}
		if !exists {
			t.Errorf("fresh database is missing %s", table)
		}
	}
}

// TestMigration040AdmitsOneExecutingPlanPerWorkspace proves the arbitration is
// structural: the single-executing-plan rule is a PRIMARY KEY, so a second
// plan physically cannot hold the slot however many processes race (FR-106).
func TestMigration040AdmitsOneExecutingPlanPerWorkspace(t *testing.T) {
	ctx := context.Background()

	db, err := Open(ctx, &Config{Path: ":memory:", WALMode: false})
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer func() { _ = db.Close() }()

	if _, err := db.ExecContext(ctx, `
		INSERT INTO workspaces (id, name, created_at, updated_at)
		VALUES ('ws-1', 'Alpha', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	for _, id := range []string{"plan-a", "plan-b"} {
		if _, err := db.ExecContext(ctx, `
			INSERT INTO workspace_plans (id, workspace_id, status, created_at, updated_at, last_activity_at)
			VALUES (?, 'ws-1', 'approved', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		`, id); err != nil {
			t.Fatalf("seed plan %s: %v", id, err)
		}
	}

	acquire := func(planID string) error {
		_, err := db.ExecContext(ctx, `
			INSERT INTO workspace_plan_execution_slots
				(workspace_id, plan_id, generation, owner, acquired_at, heartbeat_at)
			VALUES ('ws-1', ?, 1, 'test', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		`, planID)
		return err
	}

	if err := acquire("plan-a"); err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	if err := acquire("plan-b"); err == nil {
		t.Error("a second plan took the workspace's execution slot")
	}

	// A different workspace is unaffected: one plan per WORKSPACE, not one
	// globally.
	if _, err := db.ExecContext(ctx, `
		INSERT INTO workspaces (id, name, created_at, updated_at)
		VALUES ('ws-2', 'Beta', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`); err != nil {
		t.Fatalf("seed second workspace: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO workspace_plans (id, workspace_id, status, created_at, updated_at, last_activity_at)
		VALUES ('plan-c', 'ws-2', 'approved', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`); err != nil {
		t.Fatalf("seed third plan: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO workspace_plan_execution_slots
			(workspace_id, plan_id, generation, owner, acquired_at, heartbeat_at)
		VALUES ('ws-2', 'plan-c', 1, 'test', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`); err != nil {
		t.Errorf("a second workspace was blocked by the first: %v", err)
	}
}

// The slot and queue follow the plan and the workspace: deleting either takes
// its arbitration state with it rather than leaving a slot held by nothing.
func TestMigration040SlotFollowsThePlan(t *testing.T) {
	ctx := context.Background()

	db, err := Open(ctx, &Config{Path: ":memory:", WALMode: false})
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer func() { _ = db.Close() }()

	if _, err := db.ExecContext(ctx, `
		INSERT INTO workspaces (id, name, created_at, updated_at)
		VALUES ('ws-1', 'Alpha', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO workspace_plans (id, workspace_id, status, created_at, updated_at, last_activity_at)
		VALUES ('plan-a', 'ws-1', 'approved', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`); err != nil {
		t.Fatalf("seed plan: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO workspace_plan_execution_slots
			(workspace_id, plan_id, generation, owner, acquired_at, heartbeat_at)
		VALUES ('ws-1', 'plan-a', 1, 'test', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`); err != nil {
		t.Fatalf("acquire: %v", err)
	}

	if _, err := db.ExecContext(ctx, `DELETE FROM workspace_plans WHERE id = 'plan-a'`); err != nil {
		t.Fatalf("delete plan: %v", err)
	}
	var held int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM workspace_plan_execution_slots`).Scan(&held); err != nil {
		t.Fatalf("count slots: %v", err)
	}
	if held != 0 {
		t.Error("deleting a plan left its execution slot held")
	}
}

// TestMigration043UpgradesWorkspaceTicketState proves an existing database gets
// durable Ticket migration counters without altering its workspace rows.
func TestMigration043UpgradesWorkspaceTicketState(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "migration-043.db")

	legacyDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open legacy database: %v", err)
	}
	if _, err := legacyDB.ExecContext(ctx, `
		CREATE TABLE schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		INSERT INTO schema_migrations (version) VALUES (42);
		CREATE TABLE workspaces (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			status TEXT DEFAULT 'active',
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		);
		INSERT INTO workspaces (id, name, status, created_at, updated_at)
		VALUES ('ws-legacy', 'Legacy', 'active', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP);
	`); err != nil {
		_ = legacyDB.Close()
		t.Fatalf("seed legacy database: %v", err)
	}
	if err := legacyDB.Close(); err != nil {
		t.Fatalf("close legacy database: %v", err)
	}

	db, err := Open(ctx, &Config{Path: dbPath, WALMode: false})
	if err != nil {
		t.Fatalf("open migrated database: %v", err)
	}
	defer func() { _ = db.Close() }()

	var migrationVersion int
	var ticketSequence int64
	if err := db.QueryRowContext(ctx, `
		SELECT ticket_migration_version, ticket_sequence
		FROM workspaces WHERE id = 'ws-legacy'
	`).Scan(&migrationVersion, &ticketSequence); err != nil {
		t.Fatalf("read migrated ticket state: %v", err)
	}
	if migrationVersion != 0 || ticketSequence != 0 {
		t.Fatalf("legacy defaults = (%d, %d), want (0, 0)", migrationVersion, ticketSequence)
	}

	if err := db.migration043WorkspaceTicketState(ctx); err != nil {
		t.Fatalf("re-running migration 43 failed: %v", err)
	}
}

// TestMigration044UpgradesWorkspaceFolderSlugs proves a version-43 database
// gains durable slugs and the active-registration uniqueness boundary without
// changing existing workspace identities.
func TestMigration044UpgradesWorkspaceFolderSlugs(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "migration-044.db")

	legacyDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open legacy database: %v", err)
	}
	if _, err := legacyDB.ExecContext(ctx, `
		CREATE TABLE schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		INSERT INTO schema_migrations (version) VALUES (43);
		CREATE TABLE workspaces (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			status TEXT DEFAULT 'active',
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		);
		INSERT INTO workspaces (id, name, status, created_at, updated_at)
		VALUES ('ws-legacy', 'Legacy', 'active', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP);
	`); err != nil {
		_ = legacyDB.Close()
		t.Fatalf("seed legacy database: %v", err)
	}
	if err := legacyDB.Close(); err != nil {
		t.Fatalf("close legacy database: %v", err)
	}

	db, err := Open(ctx, &Config{Path: dbPath, WALMode: false})
	if err != nil {
		t.Fatalf("open migrated database: %v", err)
	}
	defer func() { _ = db.Close() }()

	var slug string
	if err := db.QueryRowContext(ctx,
		`SELECT folder_slug FROM workspaces WHERE id = 'ws-legacy'`).Scan(&slug); err != nil {
		t.Fatalf("read migrated folder slug: %v", err)
	}
	if slug != "" {
		t.Fatalf("legacy folder_slug = %q, want empty pending reconciliation", slug)
	}

	if _, err := db.ExecContext(ctx, `
		INSERT INTO workspaces (id, name, folder_slug, status, created_at, updated_at)
		VALUES ('ws-a', 'Reports', 'reports', 'active', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`); err != nil {
		t.Fatalf("insert first slug: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO workspaces (id, name, folder_slug, status, created_at, updated_at)
		VALUES ('ws-b', 'Reports Again', 'REPORTS', 'active', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`); err == nil {
		t.Fatal("case-insensitive duplicate active slug unexpectedly succeeded")
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO workspaces (id, name, folder_slug, status, created_at, updated_at)
		VALUES ('ws-trashed', 'Old Reports', 'reports', 'trashed', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`); err != nil {
		t.Fatalf("trashed workspace should not reserve slug: %v", err)
	}

	if err := db.migration044WorkspaceFolderSlugs(ctx); err != nil {
		t.Fatalf("re-running migration 44 failed: %v", err)
	}
}

// TestMigration034IsIdempotentOnAlreadyMigratedSchema proves the duplicate-column
// guard: re-running migration 34 against a workspaces table that already has the
// column must succeed rather than fail the whole startup (PRD FR-145 isolation).
func TestMigration034IsIdempotentOnAlreadyMigratedSchema(t *testing.T) {
	ctx := context.Background()

	db, err := Open(ctx, &Config{Path: ":memory:", WALMode: false})
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer func() { _ = db.Close() }()

	// The fresh schema already declares the column; running the migration body
	// again must be a no-op.
	if err := db.migration034WorkspaceInstalledCapabilities(ctx); err != nil {
		t.Fatalf("re-running migration 34 failed: %v", err)
	}
}
