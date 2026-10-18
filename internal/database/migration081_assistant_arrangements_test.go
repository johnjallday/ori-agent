package database

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigration081_UpgradesLegacyDatabaseWithoutManufacturingDrafts(t *testing.T) {
	config := &Config{Path: filepath.Join(t.TempDir(), "legacy.db")}
	db, err := Open(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`DROP TABLE assistant_arrangement_effects`,
		`DROP TABLE assistant_arrangements`,
		`DELETE FROM schema_migrations WHERE version>=81`,
		`INSERT INTO sessions(id,title,agent_name,created_at,updated_at) VALUES('legacy','Existing conversation','Atlas',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`,
		`INSERT INTO messages(id,session_id,role,content,created_at) VALUES('legacy-message','legacy','user','Keep my history',CURRENT_TIMESTAMP)`,
	} {
		if _, err := db.ExecContext(t.Context(), statement); err != nil {
			_ = db.Close()
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var content string
	var count, version int
	if err := db.QueryRowContext(t.Context(), `SELECT content FROM messages WHERE id='legacy-message'`).Scan(&content); err != nil || content != "Keep my history" {
		t.Fatal("upgrade changed legacy history", err)
	}
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM assistant_arrangements`).Scan(&count); err != nil || count != 0 {
		t.Fatal("upgrade manufactured drafts", err)
	}
	if err := db.QueryRowContext(t.Context(), `SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil || version != schemaVersion {
		t.Fatal("upgrade version", version, err)
	}
}

func TestMigration081ArrangementsAreBoundedOwnedAndRetryable(t *testing.T) {
	db, err := Open(t.Context(), &Config{Path: filepath.Join(t.TempDir(), "arrangements.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(t.Context(), `INSERT INTO sessions(id,title,agent_name,created_at,updated_at) VALUES('chat','Existing','Atlas',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		if _, err := db.ExecContext(t.Context(), `INSERT INTO messages(id,session_id,role,content,created_at) VALUES(?,'chat','assistant','Existing',CURRENT_TIMESTAMP)`, fmt.Sprintf("message-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	insert := func(id, session, message, raw string) error {
		_, err := db.ExecContext(t.Context(), `INSERT INTO assistant_arrangements(id,session_id,message_id,user_id,workspace_id,agent_name,state_version,context_revision,folder_revision,revision,state,record_json) VALUES(?,?,?,'local','hq','Atlas',1,'context','message-0',1,'draft',?)`, id, session, message, raw)
		return err
	}
	if err := insert("saved", "chat", "message-1", `{"version":1,"id":"saved","revision":1,"state":"draft"}`); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := db.migration081AssistantArrangements(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	for name, raw := range map[string]string{
		"empty": `{}`, "invalid": `not JSON`, "mismatch": `{"version":1,"id":"wrong","revision":1,"state":"draft"}`,
		"oversized": `{"version":1,"id":"large","revision":1,"state":"draft","text":"` + strings.Repeat("x", 1<<20) + `"}`,
	} {
		if err := insert(name, "chat", "message-2", raw); err == nil {
			t.Fatalf("accepted %s", name)
		}
	}
	if err := insert("foreign", "missing", "message-3", `{"version":1,"id":"foreign","revision":1,"state":"draft"}`); err == nil {
		t.Fatal("foreign conversation accepted")
	}
	var count int
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM assistant_arrangements`).Scan(&count); err != nil || count != 1 {
		t.Fatal("retry lost saved draft", err)
	}
	if _, err := db.ExecContext(t.Context(), `DELETE FROM sessions WHERE id='chat'`); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM assistant_arrangements`).Scan(&count); err != nil || count != 0 {
		t.Fatal("deletion retained draft", err)
	}
}
