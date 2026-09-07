package database

import (
	"context"
	"path/filepath"
	"testing"
)

func newAgentMapTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(context.Background(), &Config{InMemory: true, WALMode: false})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestMigration056CreatesAgentMapSchema(t *testing.T) {
	ctx := context.Background()
	db := newAgentMapTestDB(t)

	for _, table := range []string{"agent_map_layouts", "agent_map_positions"} {
		exists, err := db.tableExists(ctx, table)
		if err != nil || !exists {
			t.Fatalf("table %s exists=%v err=%v", table, exists, err)
		}
	}

	// A fresh database has no layout rows at all, so every agent falls back to
	// automatic placement (agents-page-ux FR-55).
	for _, table := range []string{"agent_map_layouts", "agent_map_positions"} {
		var count int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(1) FROM `+table).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("%s has %d rows on a fresh database, want 0", table, count)
		}
	}
}

// The layout row is separate from the position rows precisely so a reset can
// clear anchors while keeping the camera and the snap preference. That is only
// true if the cascade runs the one direction: deleting a layout takes its
// positions, and nothing else does.
func TestMigration056CascadesPositionsFromTheLayoutRow(t *testing.T) {
	ctx := context.Background()
	db := newAgentMapTestDB(t)

	if _, err := db.ExecContext(ctx, `
		INSERT INTO agent_map_layouts (user_id, schema_version, revision, snap_to_grid, created_at, updated_at)
		VALUES ('local', 1, 0, 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`); err != nil {
		t.Fatalf("insert layout: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO agent_map_positions (user_id, agent_name, x, y, updated_at)
		VALUES ('local', 'Atlas', 1, 2, CURRENT_TIMESTAMP)
	`); err != nil {
		t.Fatalf("insert position: %v", err)
	}

	if _, err := db.ExecContext(ctx, `DELETE FROM agent_map_layouts WHERE user_id = 'local'`); err != nil {
		t.Fatalf("delete layout: %v", err)
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(1) FROM agent_map_positions`).Scan(&count); err != nil {
		t.Fatalf("count positions: %v", err)
	}
	if count != 0 {
		t.Fatalf("positions after deleting the layout = %d, want 0", count)
	}
}

// Positions are keyed by (user, agent name), so two users can anchor the same
// agent independently and one user cannot hold two anchors for one agent.
func TestMigration056KeysPositionsByUserAndAgent(t *testing.T) {
	ctx := context.Background()
	db := newAgentMapTestDB(t)

	for _, user := range []string{"alice", "bob"} {
		if _, err := db.ExecContext(ctx, `
			INSERT INTO agent_map_layouts (user_id, schema_version, revision, snap_to_grid, created_at, updated_at)
			VALUES (?, 1, 0, 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		`, user); err != nil {
			t.Fatalf("insert layout for %s: %v", user, err)
		}
		if _, err := db.ExecContext(ctx, `
			INSERT INTO agent_map_positions (user_id, agent_name, x, y, updated_at)
			VALUES (?, 'Atlas', 1, 2, CURRENT_TIMESTAMP)
		`, user); err != nil {
			t.Fatalf("insert position for %s: %v", user, err)
		}
	}

	if _, err := db.ExecContext(ctx, `
		INSERT INTO agent_map_positions (user_id, agent_name, x, y, updated_at)
		VALUES ('alice', 'Atlas', 9, 9, CURRENT_TIMESTAMP)
	`); err == nil {
		t.Fatal("a duplicate (user, agent) anchor should violate the primary key")
	}
}

// Purely additive: a database that has never seen the Agent Map opens cleanly,
// keeps every existing row, and gains only the two new tables (FR-55).
func TestMigration056UpgradesV55WithoutChangingExistingRows(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "v55.db")
	staging, err := Open(ctx, &Config{Path: dbPath, WALMode: false})
	if err != nil {
		t.Fatalf("open staging database: %v", err)
	}
	if _, err := staging.ExecContext(ctx, `
		UPDATE users SET display_name = 'Keep Me' WHERE id = 'local'
	`); err != nil {
		t.Fatalf("seed existing row: %v", err)
	}
	// Rewind to v55 by removing exactly what migration 56 adds. The version
	// delete is `> 55` rather than `= 56` so a future migration does not turn
	// this into a test that silently re-applies nothing: the runner resolves the
	// current version with MAX(version).
	for _, statement := range []string{
		`DROP TABLE agent_map_positions`,
		`DROP TABLE agent_map_layouts`,
		`DELETE FROM schema_migrations WHERE version > 55`,
	} {
		if _, err := staging.ExecContext(ctx, statement); err != nil {
			t.Fatalf("rewind to v55: %v", err)
		}
	}
	if err := staging.Close(); err != nil {
		t.Fatalf("close staging database: %v", err)
	}

	db, err := Open(ctx, &Config{Path: dbPath, WALMode: false})
	if err != nil {
		t.Fatalf("upgrade v55 database: %v", err)
	}
	defer func() { _ = db.Close() }()

	var displayName string
	if err := db.QueryRowContext(ctx, `SELECT display_name FROM users WHERE id = 'local'`).Scan(&displayName); err != nil {
		t.Fatalf("read existing user after upgrade: %v", err)
	}
	if displayName != "Keep Me" {
		t.Fatalf("existing row changed across migration: %q", displayName)
	}

	for _, table := range []string{"agent_map_layouts", "agent_map_positions"} {
		exists, existsErr := db.tableExists(ctx, table)
		if existsErr != nil || !exists {
			t.Fatalf("upgraded table %s exists=%v err=%v", table, exists, existsErr)
		}
		var count int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(1) FROM `+table).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("%s has %d rows after the upgrade, want 0 — every agent falls back to automatic placement", table, count)
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
