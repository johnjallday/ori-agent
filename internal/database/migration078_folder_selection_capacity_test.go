package database

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
)

func TestMigration078PreservesEventsBoundsAndOwnerRetirement(t *testing.T) {
	ctx := t.Context()
	db, err := Open(ctx, &Config{Path: filepath.Join(t.TempDir(), "folder-selection.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	// Restore the actual pre-upgrade column and constraint.
	for _, statement := range []string{
		`DROP TRIGGER assistant_folder_context_owner_changed`,
		`ALTER TABLE messages DROP COLUMN folder_context_json`,
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.migration076AssistantFolderContext(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO sessions(id,title,agent_name,created_at,updated_at) VALUES('chat','Legacy','Atlas',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO messages(id,session_id,role,content,created_at,folder_context_json)
		VALUES('legacy','chat','user','Keep me',CURRENT_TIMESTAMP,NULL),('event','chat','system','',CURRENT_TIMESTAMP,'{"version":1}')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO messages(id,session_id,role,content,created_at,folder_context_json)
		VALUES('large','chat','system','',CURRENT_TIMESTAMP,?)`, strings.Repeat("x", foldercontext.MaxEventBytes)); err == nil {
		t.Fatal("fixture does not have the old constraint")
	}
	for range 2 {
		if err := db.migration078FolderSelectionCapacity(ctx); err != nil {
			t.Fatal("upgrade/retry failed", err)
		}
	}
	var content, event string
	var empty sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT content,folder_context_json FROM messages WHERE id='legacy'`).Scan(&content, &empty); err != nil || content != "Keep me" || empty.Valid {
		t.Fatal("ordinary message changed", content, empty, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT folder_context_json FROM messages WHERE id='event'`).Scan(&event); err != nil || event != `{"version":1}` {
		t.Fatal("saved event changed", event, err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO messages(id,session_id,role,content,created_at,folder_context_json)
		VALUES('large','chat','system','',CURRENT_TIMESTAMP,?)`, strings.Repeat("x", foldercontext.MaxEventBytes)); err != nil {
		t.Fatal("new bounded envelope rejected", err)
	}
	for _, invalid := range []struct{ role, body string }{
		{"user", `{"version":1}`},
		{"system", strings.Repeat("x", foldercontext.MaxEventBytes+1)},
		{"system", strings.Repeat("界", foldercontext.MaxEventBytes/3+1)},
	} {
		if _, err := db.ExecContext(ctx, `INSERT INTO messages(id,session_id,role,content,created_at,folder_context_json)
			VALUES('invalid','chat',?,'',CURRENT_TIMESTAMP,?)`, invalid.role, invalid.body); err == nil {
			t.Fatal("role/byte guard was lost")
		}
	}
	if _, err := db.ExecContext(ctx, `UPDATE sessions SET agent_name='Other' WHERE id='chat'`); err != nil {
		t.Fatal(err)
	}
	var remaining int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages WHERE folder_context_json IS NOT NULL`).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatal("owner retirement trigger was lost", remaining, err)
	}
}
