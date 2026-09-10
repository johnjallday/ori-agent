package database

import (
	"context"
	"path/filepath"
	"testing"
)

func newEconomyTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(context.Background(), &Config{InMemory: true, WALMode: false})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestMigration057CreatesEconomySchema(t *testing.T) {
	ctx := context.Background()
	db := newEconomyTestDB(t)

	for _, table := range []string{"economy_ledger", "economy_harvest_pending"} {
		exists, err := db.tableExists(ctx, table)
		if err != nil || !exists {
			t.Fatalf("table %s exists=%v err=%v", table, exists, err)
		}
		var count int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(1) FROM `+table).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("%s has %d rows on a fresh database, want 0 — both balances start empty", table, count)
		}
	}
}

// The whole feature's replay-safety rests on this constraint: two writers paying
// for the same thing collapse to one row rather than one balance (FR3).
func TestMigration057KeysLedgerEntriesByResourceKindAndReference(t *testing.T) {
	ctx := context.Background()
	db := newEconomyTestDB(t)

	insert := `INSERT INTO economy_ledger (resource, delta, reason, ref_kind, ref_id, workspace_id, created_at)
		VALUES (?, ?, ?, ?, ?, '', '2026-09-09T00:00:00Z')`

	if _, err := db.ExecContext(ctx, insert, "craft", 1, "chat", "message", "event-1"); err != nil {
		t.Fatalf("insert first entry: %v", err)
	}
	if _, err := db.ExecContext(ctx, insert, "craft", 1, "chat", "message", "event-1"); err == nil {
		t.Fatal("a repeated (resource, ref_kind, ref_id) should violate the unique constraint")
	}
	// The same reference under a different resource is a different payment.
	if _, err := db.ExecContext(ctx, insert, "harvest", 1, "harvest", "message", "event-1"); err != nil {
		t.Fatalf("insert same reference under another resource: %v", err)
	}
}

// One finished run is one pending Harvest: a duplicate delivery of the same run
// must not pile up twice (FR10).
func TestMigration057KeysPendingHarvestByTaskAndRun(t *testing.T) {
	ctx := context.Background()
	db := newEconomyTestDB(t)

	insert := `INSERT INTO economy_harvest_pending (task_id, workspace_id, run_key, produced_at, harvested_at)
		VALUES (?, 'ws1', ?, '2026-09-09T00:00:00Z', NULL)`

	if _, err := db.ExecContext(ctx, insert, "task-1", "run-1"); err != nil {
		t.Fatalf("insert first pending run: %v", err)
	}
	if _, err := db.ExecContext(ctx, insert, "task-1", "run-1"); err == nil {
		t.Fatal("a repeated (task_id, run_key) should violate the primary key")
	}
	if _, err := db.ExecContext(ctx, insert, "task-1", "run-2"); err != nil {
		t.Fatalf("insert second run of the same task: %v", err)
	}
}

// Purely additive: an install that has never seen the economy opens cleanly,
// keeps every existing row, and gains only the two new tables.
func TestMigration057UpgradesV56WithoutChangingExistingRows(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "v56.db")
	staging, err := Open(ctx, &Config{Path: dbPath, WALMode: false})
	if err != nil {
		t.Fatalf("open staging database: %v", err)
	}
	if _, err := staging.ExecContext(ctx, `
		UPDATE users SET display_name = 'Keep Me' WHERE id = 'local'
	`); err != nil {
		t.Fatalf("seed existing row: %v", err)
	}
	// Rewind to v56 by removing exactly what migration 57 adds. The version
	// delete is `> 56` rather than `= 57` so a future migration does not turn
	// this into a test that silently re-applies nothing: the runner resolves the
	// current version with MAX(version).
	for _, statement := range []string{
		`DROP TABLE economy_harvest_pending`,
		`DROP TABLE economy_ledger`,
		`DELETE FROM schema_migrations WHERE version > 56`,
	} {
		if _, err := staging.ExecContext(ctx, statement); err != nil {
			t.Fatalf("rewind to v56: %v", err)
		}
	}
	if err := staging.Close(); err != nil {
		t.Fatalf("close staging database: %v", err)
	}

	db, err := Open(ctx, &Config{Path: dbPath, WALMode: false})
	if err != nil {
		t.Fatalf("upgrade v56 database: %v", err)
	}
	defer func() { _ = db.Close() }()

	var displayName string
	if err := db.QueryRowContext(ctx, `SELECT display_name FROM users WHERE id = 'local'`).Scan(&displayName); err != nil {
		t.Fatalf("read existing user after upgrade: %v", err)
	}
	if displayName != "Keep Me" {
		t.Fatalf("existing row changed across migration: %q", displayName)
	}

	for _, table := range []string{"economy_ledger", "economy_harvest_pending"} {
		exists, existsErr := db.tableExists(ctx, table)
		if existsErr != nil || !exists {
			t.Fatalf("upgraded table %s exists=%v err=%v", table, exists, existsErr)
		}
	}

	version, err := db.GetSchemaVersion(ctx)
	if err != nil {
		t.Fatalf("read schema version: %v", err)
	}
	if version != schemaVersion {
		t.Fatalf("schema version = %d, want %d", version, schemaVersion)
	}
}

// Re-applying the migration over its own output changes nothing: every statement
// is CREATE ... IF NOT EXISTS, so a partially-applied upgrade can be retried.
func TestMigration057IsIdempotent(t *testing.T) {
	ctx := context.Background()
	db := newEconomyTestDB(t)

	if _, err := db.ExecContext(ctx, `
		INSERT INTO economy_ledger (resource, delta, reason, ref_kind, ref_id, workspace_id, created_at)
		VALUES ('craft', 7, 'chat', 'message', 'event-1', '', '2026-09-09T00:00:00Z')
	`); err != nil {
		t.Fatalf("seed ledger entry: %v", err)
	}

	if err := db.migration057Economy(ctx); err != nil {
		t.Fatalf("re-apply migration 057: %v", err)
	}

	var total int64
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(SUM(delta), 0) FROM economy_ledger`).Scan(&total); err != nil {
		t.Fatalf("sum ledger: %v", err)
	}
	if total != 7 {
		t.Fatalf("ledger sum after re-applying = %d, want 7", total)
	}
}

// Both tables must be app records, or reset inspection appends
// unclassified_database_domain and a settings reset stops verifying.
func TestMigration057TablesAreClassifiedAsAppRecords(t *testing.T) {
	classified := make(map[string]bool)
	for _, name := range ResetRecordTables() {
		classified[name] = true
	}
	for _, table := range []string{"economy_ledger", "economy_harvest_pending"} {
		if !classified[table] {
			t.Fatalf("%s is missing from ResetRecordTables", table)
		}
	}
}
