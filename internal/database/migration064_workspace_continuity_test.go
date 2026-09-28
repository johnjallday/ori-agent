package database

import (
	"fmt"
	"path/filepath"
	"testing"
)

func TestMigration064PreservesExistingWorkAndDoesNotGrantAdmission(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "prior.db")
	db, err := Open(ctx, &Config{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	// Reconstruct the previous full schema, unlike the older focused fixtures
	// that intentionally contain only one domain. Names are compiled constants.
	for _, table := range []string{"workspaces", "personal_assistant_state", "daily_brief_config", "users"} {
		for _, event := range []string{"INSERT", "UPDATE", "DELETE"} {
			if _, err := db.ExecContext(ctx, fmt.Sprintf(`DROP TRIGGER continuity_%s_%s`, table, event)); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, statement := range []string{
		// Migration 70's notes/topology triggers write continuity_dirty too.
		`DROP TRIGGER continuity_workspace_parent_insert`,
		`DROP TRIGGER continuity_workspace_parent_update`,
		`DROP TRIGGER continuity_workspace_parent_delete`,
		`DROP TABLE continuity_installs`,
		`DROP TABLE continuity_records`,
		`DROP TABLE continuity_components`,
		`DROP TABLE continuity_attachments`,
		`DROP TABLE continuity_operations`,
		`DROP TABLE continuity_dirty`,
		`DROP TABLE workspace_local_config`,
		`DROP TABLE continuity_file_mutations`,
		`DELETE FROM schema_migrations WHERE version>=64`,
		`INSERT INTO workspaces(id,name,created_at,updated_at) VALUES ('existing','Original project','2024-01-02 03:04:05','2024-02-03 04:05:06')`,
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(ctx, &Config{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.migration064WorkspaceContinuity(ctx); err != nil {
		t.Fatal("idempotent migration:", err)
	}
	var name, created, updated string
	if err := db.QueryRowContext(ctx, `SELECT name,CAST(created_at AS TEXT),CAST(updated_at AS TEXT) FROM workspaces WHERE id='existing'`).Scan(&name, &created, &updated); err != nil {
		t.Fatal(err)
	}
	if name != "Original project" || created != "2024-01-02 03:04:05" || updated != "2024-02-03 04:05:06" {
		t.Fatal("migration rewrote canonical work")
	}
	var attachments, dirty, triggers int
	for _, query := range []struct {
		sql    string
		result *int
	}{
		{`SELECT COUNT(*) FROM continuity_attachments`, &attachments},
		{`SELECT COUNT(*) FROM continuity_dirty`, &dirty},
		{`SELECT COUNT(*) FROM sqlite_master WHERE type='trigger' AND name LIKE 'continuity_%' AND name NOT LIKE 'continuity_followup_%' AND name NOT LIKE 'continuity_brief_revision_%' AND name NOT LIKE 'continuity_sessions_%' AND name NOT LIKE 'continuity_messages_%' AND name NOT LIKE 'continuity_session_tags_%' AND name NOT LIKE 'continuity_tool_calls_%' AND name NOT LIKE 'continuity_workspace_notes_%' AND name NOT LIKE 'continuity_note_tags_%' AND name NOT LIKE 'continuity_workspace_parent_%' AND name NOT LIKE 'continuity_retained_%'`, &triggers},
	} {
		if err := db.QueryRowContext(ctx, query.sql).Scan(query.result); err != nil {
			t.Fatal(err)
		}
	}
	if attachments != 0 || dirty != 0 || triggers != 12 {
		t.Fatalf("unexpected migration authority/work: attachments=%d dirty=%d triggers=%d", attachments, dirty, triggers)
	}
	if _, err := db.ExecContext(ctx, `UPDATE workspaces SET name='Next canonical edit' WHERE id='existing'`); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM continuity_dirty WHERE workspace_id='existing'`).Scan(&dirty); err != nil || dirty != 1 {
		t.Fatalf("canonical edit did not enqueue dirty owner: %d, %v", dirty, err)
	}
}
