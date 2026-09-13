package database

import (
	"path/filepath"
	"testing"
)

func TestMigration059CreatesGroupRequirementReceiptSchema(t *testing.T) {
	db, err := Open(t.Context(), &Config{InMemory: true, WALMode: false})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	for _, table := range []string{"group_requirement_reviews", "group_requirement_operations"} {
		exists, existsErr := db.tableExists(t.Context(), table)
		if existsErr != nil || !exists {
			t.Fatalf("table %s exists=%v err=%v", table, exists, existsErr)
		}
	}
}

func TestMigration059PreservesTablesCreatedByEarlierService(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v58.db")
	staging, err := Open(t.Context(), &Config{Path: path, WALMode: false})
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`INSERT INTO group_requirement_reviews(token, payload_json, expires_at)
			VALUES ('review-1', '{}', '2026-09-13T00:00:00Z')`,
		`INSERT INTO group_requirement_operations(
			owner_user_id, operation_kind, idempotency_key, input_digest,
			review_digest, child_workspace_id, payload_json, updated_at
		) VALUES ('owner-1', 'create_workspace', 'operation-1', 'input-1',
			'review-1', 'child-1', '{}', '2026-09-13T00:00:00Z')`,
		`DELETE FROM schema_migrations WHERE version > 58`,
	} {
		if _, err := staging.ExecContext(t.Context(), statement); err != nil {
			_ = staging.Close()
			t.Fatal(err)
		}
	}
	if err := staging.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded, err := Open(t.Context(), &Config{Path: path, WALMode: false})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = upgraded.Close() }()

	for _, table := range []string{"group_requirement_reviews", "group_requirement_operations"} {
		var count int
		if err := upgraded.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM `+table).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if count != 1 {
			t.Fatalf("%s rows after upgrade = %d, want 1", table, count)
		}
	}
	version, err := upgraded.GetSchemaVersion(t.Context())
	if err != nil || version != schemaVersion {
		t.Fatalf("schema version = %d, err=%v; want %d", version, err, schemaVersion)
	}
}
