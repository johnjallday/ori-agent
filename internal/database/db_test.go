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
