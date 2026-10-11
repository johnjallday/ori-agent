package database

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestMigration079RetryPreservesCanonicalHistoryAndInvalidatesDerivedData(t *testing.T) {
	db, err := Open(t.Context(), &Config{Path: filepath.Join(t.TempDir(), "context.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	for range 2 {
		if err := db.migration079AssistantConversationContext(t.Context()); err != nil {
			t.Fatal("retry", err)
		}
	}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO sessions(id,title,agent_name,created_at,updated_at) VALUES('chat','Existing','Atlas',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP); INSERT INTO messages(id,session_id,role,content,created_at) VALUES('early','chat','user','No coaching.',CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	insert := func(data string) error {
		_, err := db.ExecContext(t.Context(), `INSERT INTO assistant_conversation_checkpoints(session_id,owner_user_id,hq_workspace_id,profile_name,state_version,source_epoch,through_rowid,generation,recap_json,updated_at) VALUES('chat','local','hq','Atlas',3,0,1,1,?,CURRENT_TIMESTAMP)`, data)
		return err
	}
	if err := insert(`{"version":1,"items":[]}`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `UPDATE messages SET content='No coaching; membership matters.' WHERE id='early'`); err != nil {
		t.Fatal(err)
	}
	var count, epoch int
	var text string
	if err := db.QueryRowContext(t.Context(), `SELECT (SELECT COUNT(*) FROM assistant_conversation_checkpoints),assistant_context_epoch,(SELECT content FROM messages WHERE id='early') FROM sessions WHERE id='chat'`).Scan(&count, &epoch, &text); err != nil || count != 0 || epoch != 1 || text != "No coaching; membership matters." {
		t.Fatal("mutation", count, epoch, text, err)
	}
	for _, data := range []string{`not JSON`, `{"text":"` + strings.Repeat("x", 16000) + `"}`} {
		if insert(data) == nil {
			t.Fatal("unsafe SQL envelope")
		}
	}
	if err := insert(`{"version":1,"items":[]}`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `UPDATE sessions SET agent_name='Aria' WHERE id='chat'`); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM assistant_conversation_checkpoints`).Scan(&count); err != nil || count != 0 {
		t.Fatal("owner invalidation", count, err)
	}
}
