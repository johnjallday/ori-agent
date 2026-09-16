package database

import (
	"testing"
)

// TestMigration060AgentAnchorsFollowTheirWorkspace proves agent anchors share
// the lifecycle rules of building anchors: a trashed workspace keeps its agents'
// positions for restore, a permanently deleted one takes exactly its own with it,
// and dropping the user's layout drops every agent anchor.
func TestMigration060AgentAnchorsFollowTheirWorkspace(t *testing.T) {
	ctx := t.Context()
	db, err := Open(ctx, &Config{InMemory: true, WALMode: false})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	count := func() int {
		t.Helper()
		var n int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(1) FROM workspace_map_agent_positions`).Scan(&n); err != nil {
			t.Fatalf("count agent anchors: %v", err)
		}
		return n
	}

	for _, id := range []string{"grp-keep", "grp-drop"} {
		exec(`INSERT INTO workspaces (id, name, created_at, updated_at)
			VALUES (?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`, id, id)
	}
	exec(`INSERT INTO workspace_map_layouts (user_id, schema_version, revision, snap_to_grid, created_at, updated_at)
		VALUES ('local', 1, 1, 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	for _, id := range []string{"grp-keep", "grp-drop"} {
		exec(`INSERT INTO workspace_map_agent_positions (user_id, workspace_id, agent_key, x, y, updated_at)
			VALUES ('local', ?, 'manager', 10, 20, CURRENT_TIMESTAMP)`, id)
	}

	// An agent anchor can only exist for a real workspace.
	if _, err := db.ExecContext(ctx, `INSERT INTO workspace_map_agent_positions (user_id, workspace_id, agent_key, x, y, updated_at)
		VALUES ('local', 'grp-ghost', 'manager', 0, 0, CURRENT_TIMESTAMP)`); err == nil {
		t.Fatal("agent anchor for a missing workspace was accepted")
	}

	exec(`UPDATE workspaces SET status = 'trashed' WHERE id = 'grp-drop'`)
	if got := count(); got != 2 {
		t.Fatalf("agent anchors after trash = %d, want 2", got)
	}

	exec(`DELETE FROM workspaces WHERE id = 'grp-drop'`)
	var remaining string
	if err := db.QueryRowContext(ctx, `SELECT workspace_id FROM workspace_map_agent_positions`).Scan(&remaining); err != nil {
		t.Fatalf("read remaining agent anchor: %v", err)
	}
	if remaining != "grp-keep" {
		t.Fatalf("remaining agent anchor = %q, want grp-keep", remaining)
	}

	exec(`DELETE FROM workspace_map_layouts WHERE user_id = 'local'`)
	if got := count(); got != 0 {
		t.Fatalf("agent anchors after layout delete = %d, want 0", got)
	}
}
