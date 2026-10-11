package database

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
)

// Both parents had version 78, with different schemas. Exercise real startup
// from each exact relevant schema, not just the migration function directly.
func TestMigration079UpgradesBothVersion78ParentsAndVersion77(t *testing.T) {
	for _, parent := range []string{"dev78", "conversation78", "base77"} {
		t.Run(parent, func(t *testing.T) {
			ctx := t.Context()
			cfg := &Config{Path: filepath.Join(t.TempDir(), "upgrade.db")}
			db, err := Open(ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			exec := func(statement string, args ...any) {
				t.Helper()
				if _, err := db.ExecContext(ctx, statement, args...); err != nil {
					t.Fatal(err)
				}
			}
			if parent != "conversation78" {
				for _, statement := range []string{
					`DROP TRIGGER assistant_context_owner_changed`,
					`DROP TRIGGER assistant_context_source_updated`,
					`DROP TRIGGER assistant_context_source_deleted`,
					`DROP TRIGGER assistant_context_source_imported`,
					`DROP TABLE assistant_conversation_checkpoints`,
					`ALTER TABLE sessions DROP COLUMN assistant_context_epoch`,
				} {
					exec(statement)
				}
			}
			if parent != "dev78" {
				exec(`DROP TRIGGER assistant_folder_context_owner_changed`)
				exec(`ALTER TABLE messages DROP COLUMN folder_context_json`)
				if err := db.migration076AssistantFolderContext(ctx); err != nil {
					t.Fatal(err)
				}
			}
			exec(`DELETE FROM schema_migrations WHERE version >= 78`)
			if parent != "base77" {
				exec(`INSERT INTO schema_migrations(version) VALUES(78)`)
			}
			exec(`INSERT INTO sessions(id,title,agent_name,created_at,updated_at) VALUES('chat','Keep history','Atlas',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`)
			exec(`INSERT INTO messages(id,session_id,role,content,created_at,folder_context_json,turn_context_json)
				VALUES('original','chat','user','No coaching.',CURRENT_TIMESTAMP,NULL,'{"version":1}'),
				('event','chat','system','Keep event',CURRENT_TIMESTAMP,'{"version":1}',NULL)`)
			var originalRowID int64
			var originalCreatedAt string
			if err := db.QueryRowContext(ctx, `SELECT rowid,created_at FROM messages WHERE id='original'`).Scan(&originalRowID, &originalCreatedAt); err != nil {
				t.Fatal(err)
			}
			if parent != "dev78" {
				if _, err := db.ExecContext(ctx, `UPDATE messages SET folder_context_json=? WHERE id='event'`, strings.Repeat("x", foldercontext.MaxEventBytes)); err == nil {
					t.Fatal("old schema must reject the expanded envelope")
				}
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			upgraded, err := Open(ctx, cfg)
			if err != nil {
				t.Fatal("startup from parent failed", err)
			}
			db = upgraded
			for range 2 {
				if err := db.migrate(ctx); err != nil {
					t.Fatal("startup retry", err)
				}
			}
			var version, registered, count, triggers int
			if err := db.QueryRowContext(ctx, `SELECT MAX(version), COUNT(*) FILTER (WHERE version IN (78,79)) FROM schema_migrations`).Scan(&version, &registered); err != nil || version != schemaVersion || registered != 2 {
				t.Fatal("lost migration registration", version, registered, err)
			}
			var rowID int64
			var role, content, createdAt, attribution, event string
			if err := db.QueryRowContext(ctx, `SELECT rowid,role,content,created_at,turn_context_json FROM messages WHERE id='original'`).Scan(&rowID, &role, &content, &createdAt, &attribution); err != nil || rowID != originalRowID || role != "user" || content != "No coaching." || createdAt != originalCreatedAt || attribution != `{"version":1}` {
				t.Fatal("canonical message changed", err)
			}
			if err := db.QueryRowContext(ctx, `SELECT folder_context_json FROM messages WHERE id='event'`).Scan(&event); err != nil || event != `{"version":1}` {
				t.Fatal("folder event changed", event, err)
			}
			if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='trigger' AND name IN ('assistant_folder_context_owner_changed','assistant_context_owner_changed','assistant_context_source_updated','assistant_context_source_deleted','assistant_context_source_imported')`).Scan(&triggers); err != nil || triggers != 5 {
				t.Fatal("lifecycle trigger lost", triggers, err)
			}
			// New capacity works, but its byte/role guards still hold.
			exec(`UPDATE messages SET folder_context_json=? WHERE id='event'`, strings.Repeat("x", foldercontext.MaxEventBytes))
			for _, invalid := range []struct{ id, body string }{
				{"event", strings.Repeat("x", foldercontext.MaxEventBytes+1)},
				{"event", strings.Repeat("界", foldercontext.MaxEventBytes/3+1)},
				{"original", `{"version":1}`},
			} {
				if _, err := db.ExecContext(ctx, `UPDATE messages SET folder_context_json=? WHERE id=?`, invalid.body, invalid.id); err == nil {
					t.Fatal("folder role/byte constraint lost")
				}
			}
			exec(`INSERT INTO assistant_conversation_checkpoints(session_id,owner_user_id,hq_workspace_id,profile_name,state_version,source_epoch,through_rowid,generation,recap_json,updated_at)
				VALUES('chat','local','hq','Atlas',3,0,?,1,'{"version":1,"items":[]}',CURRENT_TIMESTAMP)`, originalRowID)
			exec(`UPDATE sessions SET agent_name='Other' WHERE id='chat'`)
			if err := db.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM assistant_conversation_checkpoints) + (SELECT COUNT(*) FROM messages WHERE folder_context_json IS NOT NULL)`).Scan(&count); err != nil || count != 0 {
				t.Fatal("either parent's ownership invalidation was lost", count, err)
			}
		})
	}
}
