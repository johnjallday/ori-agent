package database

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestMigration080RetryKeepsOperationsAndEnforcesBoundsAndOwnership(t *testing.T) {
	db, err := Open(t.Context(), &Config{Path: filepath.Join(t.TempDir(), "operations.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(t.Context(), `INSERT INTO sessions(id,title,agent_name,created_at,updated_at) VALUES('chat','Existing','Atlas',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	insert := func(id, conversation, status, commands, task string) error {
		_, err := db.ExecContext(t.Context(), `INSERT INTO assistant_operations(
			id, contract_version, kind, status, revision, user_id, assistant_id, state_version, hq_workspace_id, profile_name,
			conversation_id, workspace_id, context_json, commands_json, receipts_json, task_id, created_at_ms, updated_at_ms, expires_at_ms
		) VALUES (?, 1, 'navigation', ?, 1, 'local', 'asst', 1, 'hq', 'Atlas', ?, 'ws', '{}', ?, '[]', ?, 1000, 1000, 2000)`,
			id, status, conversation, commands, task)
		return err
	}
	if err := insert("op-1", "chat", "requested", `[]`, "task-1"); err != nil {
		t.Fatal(err)
	}
	// Re-running the migration is harmless and keeps what is stored.
	for range 2 {
		if err := db.migration080AssistantOperations(t.Context()); err != nil {
			t.Fatal("retry", err)
		}
	}
	var count int
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM assistant_operations WHERE id='op-1'`).Scan(&count); err != nil || count != 1 {
		t.Fatal("retry lost a row", count, err)
	}
	refused := map[string]error{
		"unknown status":         insert("op-2", "chat", "running", `[]`, ""),
		"not JSON":               insert("op-3", "chat", "requested", `not JSON`, ""),
		"oversized commands":     insert("op-4", "chat", "requested", `["`+strings.Repeat("x", 16000)+`"]`, ""),
		"missing conversation":   insert("op-5", "nowhere", "requested", `[]`, ""),
		"task bound twice":       insert("op-6", "chat", "requested", `[]`, "task-1"),
		"duplicate operation id": insert("op-1", "chat", "requested", `[]`, ""),
	}
	for name, err := range refused {
		if err == nil {
			t.Errorf("%s: the schema must refuse it", name)
		}
	}
	// Two operations without a task do not collide on the empty task id.
	if err := insert("op-7", "chat", "requested", `[]`, ""); err != nil {
		t.Fatal(err)
	}
	if err := insert("op-8", "chat", "requested", `[]`, ""); err != nil {
		t.Fatal(err)
	}
	// An operation belongs to its conversation and goes with it.
	if _, err := db.ExecContext(t.Context(), `DELETE FROM sessions WHERE id='chat'`); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM assistant_operations`).Scan(&count); err != nil || count != 0 {
		t.Fatal("conversation deletion must remove its operations", count, err)
	}
}
