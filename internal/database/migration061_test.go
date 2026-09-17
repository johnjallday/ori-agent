package database

import (
	"testing"
)

// TestMigration061ResultParcelsAreUniquePerRunAndSurviveAWorkspaceIndexRebuild
// proves the two properties parcels depend on: a replayed run cannot create a
// second parcel, and clearing the workspaces table — which the workspace index
// does on every rebuild — leaves parcels alone.
func TestMigration061ResultParcelsAreUniquePerRunAndSurviveAWorkspaceIndexRebuild(t *testing.T) {
	ctx := t.Context()
	db, err := Open(ctx, &Config{InMemory: true, WALMode: false})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	var version int
	if err := db.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil || version != schemaVersion {
		t.Fatalf("schema version = %d, %v; want %d", version, err, schemaVersion)
	}

	insert := func(id, runKey string) error {
		_, err := db.ExecContext(ctx, `INSERT INTO result_parcels
			(id, workspace_id, kind, ref_id, run_key, outcome, produced_at)
			VALUES (?, 'ws-1', 'task', 'task-1', ?, 'succeeded', '2026-09-16T10:00:00Z')`, id, runKey)
		return err
	}
	if err := insert("p1", "run-1"); err != nil {
		t.Fatalf("first parcel: %v", err)
	}
	if err := insert("p2", "run-1"); err == nil {
		t.Fatal("a second parcel for the same run was accepted")
	}
	if err := insert("p3", "run-2"); err != nil {
		t.Fatalf("a later run of the same task: %v", err)
	}

	if _, err := db.ExecContext(ctx, `INSERT INTO workspaces (id, name, created_at, updated_at)
		VALUES ('ws-1', 'ws-1', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM workspaces`); err != nil {
		t.Fatalf("clear workspaces: %v", err)
	}

	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(1) FROM result_parcels WHERE opened_at IS NULL`).Scan(&n); err != nil {
		t.Fatalf("count parcels: %v", err)
	}
	if n != 2 {
		t.Fatalf("unopened parcels = %d, want 2 (both runs, untouched by the workspaces table)", n)
	}
}
