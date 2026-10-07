package database

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigration077PreservesLegacyAndBoundsAttribution(t *testing.T) {
	ctx := t.Context()
	db, err := Open(ctx, &Config{Path: filepath.Join(t.TempDir(), "attribution.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(ctx, `INSERT INTO sessions(id,title,agent_name,created_at,updated_at) VALUES('chat','Legacy','Atlas',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO messages(id,session_id,role,content,created_at) VALUES('legacy','chat','user','Keep me',CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `ALTER TABLE messages DROP COLUMN turn_context_json`); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := db.migration077AssistantTurnAttribution(ctx); err != nil {
			t.Fatal("migration/retry", err)
		}
	}
	var text string
	var value sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT content,turn_context_json FROM messages WHERE id='legacy'`).Scan(&text, &value); err != nil || text != "Keep me" || value.Valid {
		t.Fatal("changed legacy", text, value, err)
	}
	for _, input := range []struct{ role, body string }{{"system", `{"version":1}`}, {"user", `{invalid`}, {"assistant", `{"text":"` + strings.Repeat("x", 32000) + `"}`}} {
		if _, err := db.ExecContext(ctx, `INSERT INTO messages(id,session_id,role,content,created_at,turn_context_json) VALUES('bad','chat',?,'',CURRENT_TIMESTAMP,?)`, input.role, input.body); err == nil {
			t.Fatal("invalid metadata accepted")
		}
	}
}
