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
		"vault_records",
		"vault_grants",
		"vault_audit_events",
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
		"mcp_bindings_json":     false,
		"agent_mcp_access_json": false,
	}
	vaultColumns := map[string]bool{
		"key_salt":       false,
		"key_nonce":      false,
		"key_ciphertext": false,
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
