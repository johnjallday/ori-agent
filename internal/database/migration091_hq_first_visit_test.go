package database

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestHQFirstVisitMigrationBackfillsOnlyExistingHQs(t *testing.T) {
	ctx := t.Context()
	cfg := &Config{Path: filepath.Join(t.TempDir(), "upgrade.db")}
	raw, err := sql.Open("sqlite", cfg.Path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	legacy := &DB{DB: raw, path: cfg.Path}
	if _, err := raw.ExecContext(ctx, `CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	for version := 1; version <= 90; version++ {
		if err := legacy.runMigration(ctx, version); err != nil {
			t.Fatalf("migration %d: %v", version, err)
		}
		if _, err := raw.ExecContext(ctx, `INSERT INTO schema_migrations(version) VALUES(?)`, version); err != nil {
			t.Fatal(err)
		}
	}
	for _, state := range []string{"completed", "unseen", "in_progress", "skipped"} {
		if _, err := raw.ExecContext(ctx, `INSERT INTO users(id, hq_onboarding_state, created_at, updated_at) VALUES(?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`, state, state); err != nil {
			t.Fatal(err)
		}
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for id, want := range map[string]string{"completed": "done", "unseen": "", "in_progress": "", "skipped": ""} {
		var got string
		if err := db.QueryRowContext(ctx, `SELECT hq_first_visit_state FROM users WHERE id = ?`, id).Scan(&got); err != nil || got != want {
			t.Fatalf("%s = %q, want %q: %v", id, got, want, err)
		}
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO users(id, hq_onboarding_state, hq_first_visit_state, created_at, updated_at) VALUES('new', 'completed', 'pending', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	if err := db.migration091HQFirstVisit(ctx); err != nil {
		t.Fatal(err)
	}
	var got string
	if err := db.QueryRowContext(ctx, `SELECT hq_first_visit_state FROM users WHERE id = 'new'`).Scan(&got); err != nil || got != "pending" {
		t.Fatalf("replay changed a newly offered welcome: %q, %v", got, err)
	}
}
