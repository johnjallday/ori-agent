package database

import (
	"context"
	"testing"
)

func TestMigration067KeepsImportedSourceReferencesSeparateFromAuthority(t *testing.T) {
	db, err := Open(context.Background(), &Config{InMemory: true, WALMode: false})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var applied int
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM schema_migrations WHERE version=67`).Scan(&applied); err != nil || applied != 1 {
		t.Fatal(applied, err)
	}
	columns, err := db.QueryContext(t.Context(), `PRAGMA table_info(followup_continuity_source_refs)`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = columns.Close() }()
	found := map[string]bool{}
	for columns.Next() {
		var id, notNull, primary int
		var name, kind string
		var defaultValue any
		if err := columns.Scan(&id, &name, &kind, &notNull, &defaultValue, &primary); err != nil {
			t.Fatal(err)
		}
		found[name] = true
	}
	if err := columns.Err(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"followup_id", "workspace_id", "operation_id", "source_account_id", "source_dedup_key", "related_workspace_id", "related_task_id", "saved_at"} {
		if !found[name] {
			t.Fatal("lost historical reference column", name)
		}
	}
}
