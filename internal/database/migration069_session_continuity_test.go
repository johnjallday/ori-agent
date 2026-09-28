package database

import (
	"path/filepath"
	"testing"
)

func TestMigration069SessionHistoryDirtiesExactOwners(t *testing.T) {
	ctx := t.Context()
	db, err := Open(ctx, &Config{Path: filepath.Join(t.TempDir(), "sessions.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	for _, id := range []string{"source", "other"} {
		if _, err := db.ExecContext(ctx, `INSERT INTO workspaces(id,name,created_at,updated_at) VALUES(?,?,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, id, id); err != nil {
			t.Fatal(err)
		}
	}
	reset := func() {
		t.Helper()
		if _, err := db.ExecContext(ctx, `DELETE FROM continuity_dirty`); err != nil {
			t.Fatal(err)
		}
	}
	check := func(owner string) {
		t.Helper()
		var n int
		if err := db.QueryRowContext(ctx, `SELECT COALESCE((SELECT sequence FROM continuity_dirty WHERE workspace_id=?),0)`, owner).Scan(&n); err != nil || n == 0 {
			t.Fatalf("history write did not dirty %s: %d %v", owner, n, err)
		}
	}
	reset()
	if _, err := db.ExecContext(ctx, `INSERT INTO sessions(id,title,agent_name,workspace_id,created_at,updated_at) VALUES('chat','Synthetic','shared','source',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	check("source")
	reset()
	if _, err := db.ExecContext(ctx, `INSERT INTO messages(id,session_id,role,content,created_at) VALUES('m','chat','user','private',CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	check("source")
	reset()
	if _, err := db.ExecContext(ctx, `UPDATE messages SET content='edited' WHERE id='m'`); err != nil {
		t.Fatal(err)
	}
	check("source")
	reset()
	if _, err := db.ExecContext(ctx, `INSERT INTO session_tags(session_id,tag) VALUES('chat','retained')`); err != nil {
		t.Fatal(err)
	}
	check("source")
	reset()
	if _, err := db.ExecContext(ctx, `INSERT INTO tool_calls(id,message_id,session_id,tool_name) VALUES('tool','m','chat','historic')`); err != nil {
		t.Fatal(err)
	}
	check("source")
	reset()
	if _, err := db.ExecContext(ctx, `UPDATE sessions SET workspace_id='other' WHERE id='chat'`); err != nil {
		t.Fatal(err)
	}
	check("source")
	check("other")
	reset()
	if _, err := db.ExecContext(ctx, `DELETE FROM sessions WHERE id='chat'`); err != nil {
		t.Fatal(err)
	}
	check("other")
	var seq int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('messages') WHERE name='continuity_source_sequence'`).Scan(&seq); err != nil || seq != 1 {
		t.Fatal("source-order column absent", seq, err)
	}
}
