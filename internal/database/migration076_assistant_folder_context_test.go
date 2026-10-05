package database

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigration076PreservesLegacyMessagesAndBoundsTypedEvents(t *testing.T) {
	ctx := t.Context()
	db, err := Open(ctx, &Config{Path: filepath.Join(t.TempDir(), "folders.db")})
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
	if _, err := db.ExecContext(ctx, `DROP TRIGGER assistant_folder_context_owner_changed`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `ALTER TABLE messages DROP COLUMN folder_context_json`); err != nil {
		t.Fatal(err)
	}
	if err := db.migration076AssistantFolderContext(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.migration076AssistantFolderContext(ctx); err != nil {
		t.Fatal("migration retry:", err)
	}
	var text string
	var event sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT content,folder_context_json FROM messages WHERE id='legacy'`).Scan(&text, &event); err != nil || text != "Keep me" || event.Valid {
		t.Fatalf("legacy changed: %q %+v %v", text, event, err)
	}
	for _, change := range []struct{ role, body string }{
		{"user", `{"version":1}`}, {"system", strings.Repeat("x", 8449)},
	} {
		if _, err := db.ExecContext(ctx, `INSERT INTO messages(id,session_id,role,content,created_at,folder_context_json) VALUES('bad','chat',?,'',CURRENT_TIMESTAMP,?)`, change.role, change.body); err == nil {
			t.Fatal("accepted invalid typed event")
		}
	}
}
