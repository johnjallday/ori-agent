package database

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigration054CreatesBoundedSetupJourneySchema(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, &Config{InMemory: true, WALMode: false})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer func() { _ = db.Close() }()

	version, err := db.GetSchemaVersion(ctx)
	if err != nil || version != schemaVersion {
		t.Fatalf("schema version = %d, %v; want %d", version, err, schemaVersion)
	}
	for _, table := range []string{
		"setup_journey_run",
		"setup_journey_operation_receipt",
		"setup_journey_declaration_migration_receipt",
		"setup_journey_review_receipt",
	} {
		exists, existsErr := db.tableExists(ctx, table)
		if existsErr != nil || !exists {
			t.Fatalf("table %s exists=%v err=%v", table, exists, existsErr)
		}
	}
	for _, index := range []string{
		"idx_setup_journey_run_root_identity",
		"idx_setup_journey_run_children",
		"idx_setup_journey_run_unbound_child",
		"idx_setup_journey_run_child_project",
		"idx_setup_journey_operation_busy",
		"idx_setup_journey_operation_created",
		"idx_setup_journey_declaration_migration_run",
		"idx_setup_journey_review_run",
	} {
		var found string
		if err := db.QueryRowContext(ctx,
			`SELECT name FROM sqlite_master WHERE type = 'index' AND name = ?`, index).Scan(&found); err != nil {
			t.Fatalf("index %s missing: %v", index, err)
		}
	}

	// The setup persistence boundary must not gain generic blobs for sensitive
	// owner data. Its JSON columns are limited to structural step state and the
	// strict canonical result receipt.
	for _, table := range []string{
		"setup_journey_run", "setup_journey_operation_receipt",
		"setup_journey_declaration_migration_receipt", "setup_journey_review_receipt",
	} {
		rows, queryErr := db.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
		if queryErr != nil {
			t.Fatalf("inspect %s: %v", table, queryErr)
		}
		for rows.Next() {
			var cid, notNull, primaryKey int
			var name, columnType string
			var defaultValue sql.NullString
			if scanErr := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); scanErr != nil {
				_ = rows.Close()
				t.Fatalf("scan %s columns: %v", table, scanErr)
			}
			lower := strings.ToLower(name)
			for _, forbidden := range []string{"path", "manifest", "prompt", "credential", "role_binding", "catalog_content", "error"} {
				if strings.Contains(lower, forbidden) {
					_ = rows.Close()
					t.Fatalf("%s contains forbidden setup journey column %q", table, name)
				}
			}
		}
		if closeErr := rows.Close(); closeErr != nil {
			t.Fatalf("close %s schema rows: %v", table, closeErr)
		}
	}
}

func TestMigration054EnforcesRootChildAndOperationIdentity(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, &Config{InMemory: true, WALMode: false})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer func() { _ = db.Close() }()

	insertRun := func(id, kind string, rootID any, owner, relationship, specialist, project string) error {
		_, insertErr := db.ExecContext(ctx, `
			INSERT INTO setup_journey_run (
				id, run_kind, root_run_id, owner_user_id, relationship_id,
				specialist_slug, journey_id, declaration_schema_version,
				declaration_version, lifecycle_state, current_step_id,
				step_states_json, project_workspace_id, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, 'journey', 1, 1, 'not_started', 'integration',
				'[{"step_id":"integration","status":"pending"}]', ?,
				CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		`, id, kind, rootID, owner, relationship, specialist, project)
		return insertErr
	}
	if err := insertRun("root-1", "root", nil, "local", "assistant-1", "music", ""); err != nil {
		t.Fatalf("insert root: %v", err)
	}
	if err := insertRun("root-2", "root", nil, "local", "assistant-1", "music", ""); err == nil {
		t.Fatal("duplicate accepted-relationship root was allowed")
	}
	if err := insertRun("child-1", "child", "root-1", "", "", "", ""); err != nil {
		t.Fatalf("insert child: %v", err)
	}
	if err := insertRun("child-2", "child", "root-1", "", "", "", ""); err == nil {
		t.Fatal("duplicate unbound child was allowed")
	}
	if err := insertRun("grandchild", "child", "child-1", "", "", "", "workspace-2"); err == nil {
		t.Fatal("child run was accepted as another child's root")
	}

	insertOperation := func(key string) error {
		_, insertErr := db.ExecContext(ctx, `
			INSERT INTO setup_journey_operation_receipt (
				run_kind, run_id, idempotency_key, step_id, action_id,
				input_digest, status, run_revision_before, run_revision_after, created_at
			) VALUES ('root', 'root-1', ?, 'integration', 'install',
				?, 'claimed', 1, 2, CURRENT_TIMESTAMP)
		`, key, strings.Repeat("a", 64))
		return insertErr
	}
	if err := insertOperation("request-1"); err != nil {
		t.Fatalf("insert operation: %v", err)
	}
	if err := insertOperation("request-2"); err == nil {
		t.Fatal("second busy operation was allowed for the same run")
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE setup_journey_operation_receipt
		SET status = 'succeeded', result_code = 'applied', completed_at = CURRENT_TIMESTAMP
		WHERE run_id = 'root-1'
	`); err != nil {
		t.Fatalf("finalize first operation: %v", err)
	}
	if err := insertOperation("request-2"); err != nil {
		t.Fatalf("terminal receipt incorrectly blocked the next operation: %v", err)
	}
	if err := insertOperation("request-2"); err == nil {
		t.Fatal("duplicate operation idempotency key was allowed")
	}
}

func TestMigration054UpgradesV53WithoutChangingExistingRows(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "v53.db")
	staging, err := Open(ctx, &Config{Path: dbPath, WALMode: false})
	if err != nil {
		t.Fatalf("open staging database: %v", err)
	}
	if _, err := staging.ExecContext(ctx, `
		UPDATE users SET display_name = 'Keep Me' WHERE id = 'local'
	`); err != nil {
		t.Fatalf("seed existing row: %v", err)
	}
	for _, statement := range []string{
		`DROP TABLE sample_library_operation_receipt`,
		`DROP TABLE sample_library_review_receipt`,
		`DROP TABLE sample_library_child_copy`,
		`DROP TABLE sample_library_collection_member`,
		`DROP TABLE sample_library_collection`,
		`DROP TABLE sample_library_annotation`,
		`DROP TABLE sample_library_content_fact`,
		`DROP TABLE sample_library_entry`,
		`DROP TABLE sample_library_root`,
		`DROP TABLE sample_library_state`,
		`DELETE FROM schema_migrations WHERE version = 55`,
		`DROP TABLE setup_journey_review_receipt`,
		`DROP TABLE setup_journey_declaration_migration_receipt`,
		`DROP TABLE setup_journey_operation_receipt`,
		`DROP TABLE setup_journey_run`,
		`DELETE FROM schema_migrations WHERE version = 54`,
	} {
		if _, err := staging.ExecContext(ctx, statement); err != nil {
			t.Fatalf("rewind to v53: %v", err)
		}
	}
	if err := staging.Close(); err != nil {
		t.Fatalf("close staging database: %v", err)
	}

	db, err := Open(ctx, &Config{Path: dbPath, WALMode: false})
	if err != nil {
		t.Fatalf("upgrade v53 database: %v", err)
	}
	defer func() { _ = db.Close() }()
	var displayName string
	if err := db.QueryRowContext(ctx, `SELECT display_name FROM users WHERE id = 'local'`).Scan(&displayName); err != nil {
		t.Fatalf("read existing user after upgrade: %v", err)
	}
	if displayName != "Keep Me" {
		t.Fatalf("existing row changed across migration: %q", displayName)
	}
	for _, table := range []string{
		"setup_journey_run", "setup_journey_operation_receipt",
		"setup_journey_declaration_migration_receipt", "setup_journey_review_receipt",
	} {
		exists, existsErr := db.tableExists(ctx, table)
		if existsErr != nil || !exists {
			t.Fatalf("upgraded table %s exists=%v err=%v", table, exists, existsErr)
		}
	}
}
