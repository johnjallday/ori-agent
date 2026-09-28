package session

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

func TestRestoreContinuityToolHistoryIsInertScopedAndRetrySafe(t *testing.T) {
	source := continuitySessionDB(t, "tools-source")
	continuityHistorySeed(t, source, 1)
	sessions, messages := collectSessionRecords(t, source, "owned")
	dest := continuitySessionDB(t, "tools-dest")
	scope := seedSessionRestoreReceipt(t, dest, "owned")
	at := time.Date(2024, 3, 4, 5, 6, 7, 0, time.UTC)
	value := ContinuityToolCall{Version: 1, WorkspaceID: "owned", Call: ToolCall{ID: "tool-1", SessionID: "saved", MessageID: "message-0000", ToolName: "historic", Arguments: `{"authored":"private"}`, Result: "Old result", CreatedAt: at}}
	record, err := SnapshotContinuityToolCall(value, "owned")
	if err != nil {
		t.Fatal(err)
	}
	tools := NewSQLiteToolCallStore(dest)
	if err := dest.InTransaction(t.Context(), func(tx *sql.Tx) error {
		if _, err := NewSQLiteStore(dest).RestoreContinuitySession(t.Context(), tx, scope, sessions[0]); err != nil {
			return err
		}
		if _, err := tools.RestoreContinuityToolCall(t.Context(), tx, scope, record); !errors.Is(err, workspacecontinuity.ErrConflict) {
			t.Fatal("unrestored message authorized history", err)
		}
		return workspacecontinuity.ErrConflict
	}); !errors.Is(err, workspacecontinuity.ErrConflict) {
		t.Fatal(err)
	}
	if err := dest.InTransaction(t.Context(), func(tx *sql.Tx) error {
		if _, err := NewSQLiteStore(dest).RestoreContinuitySession(t.Context(), tx, scope, sessions[0]); err != nil {
			return err
		}
		if _, err := NewSQLiteStore(dest).RestoreContinuityMessage(t.Context(), tx, scope, messages[0]); err != nil {
			return err
		}
		inserted, err := tools.RestoreContinuityToolCall(t.Context(), tx, scope, record)
		if err != nil || !inserted {
			t.Fatal("inert tool history not inserted", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := dest.ExecContext(t.Context(), `UPDATE tool_calls SET result='Local revised result' WHERE id='tool-1'`); err != nil {
		t.Fatal(err)
	}
	if err := dest.InTransaction(t.Context(), func(tx *sql.Tx) error {
		inserted, err := tools.RestoreContinuityToolCall(t.Context(), tx, scope, record)
		if inserted {
			t.Fatal("retry replaced local tool edit")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	got, err := tools.GetToolCalls(t.Context(), "saved")
	if err != nil || len(got) != 1 || got[0].Result != "Local revised result" {
		t.Fatal("local tool history changed", got, err)
	}
	if _, err := dest.ExecContext(t.Context(), `DELETE FROM tool_calls WHERE id='tool-1'`); err != nil {
		t.Fatal(err)
	}
	if err := dest.InTransaction(t.Context(), func(tx *sql.Tx) error {
		inserted, err := tools.RestoreContinuityToolCall(t.Context(), tx, scope, record)
		if inserted {
			t.Fatal("retry resurrected deleted history")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestCollectContinuityToolHistoryRequiresExactOwnedMessage(t *testing.T) {
	db := continuitySessionDB(t, "tools")
	continuityHistorySeed(t, db, 2)
	ctx := t.Context()
	if _, err := db.ExecContext(ctx, `INSERT INTO workspaces(id,name,owner_user_id,created_at,updated_at) VALUES('neighbor','Neighbor','local',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO sessions(id,title,agent_name,workspace_id,created_at,updated_at) VALUES('other','Neighbor','same-name','neighbor',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO messages(id,session_id,role,content,created_at) VALUES('foreign-message','other','user','foreign text',CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2024, 3, 4, 5, 6, 7, 0, time.UTC)
	tools := NewSQLiteToolCallStore(db)
	if err := tools.AddToolCall(ctx, &ToolCall{ID: "invalid-ref", SessionID: "saved", MessageID: "foreign-message", ToolName: "historic", CreatedAt: at}); err != nil {
		t.Fatal(err)
	}
	spool, err := workspacecontinuity.NewSpool(t.TempDir(), "owned")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = spool.Close() }()
	if err := db.InTransaction(ctx, func(tx *sql.Tx) error { return CollectContinuityToolHistory(ctx, tx, "owned", spool) }); !errors.Is(err, workspacecontinuity.ErrIncomplete) {
		t.Fatal("cross-session tool reference exported", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE tool_calls SET message_id='message-0000' WHERE id='invalid-ref'`); err != nil {
		t.Fatal(err)
	}
	if err := tools.AddToolCall(ctx, &ToolCall{ID: "other-tool", SessionID: "other", MessageID: "foreign-message", ToolName: "same-name", CreatedAt: at}); err != nil {
		t.Fatal(err)
	}
	fresh, err := workspacecontinuity.NewSpool(t.TempDir(), "owned")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fresh.Close() }()
	if err := db.InTransaction(ctx, func(tx *sql.Tx) error { return CollectContinuityToolHistory(ctx, tx, "owned", fresh) }); err != nil {
		t.Fatal(err)
	}
	components, _, err := fresh.Seal(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, component := range components {
		if component.Domain == "tool_history" && (component.Counts["tool_calls"] != 1 || len(component.Chunks) != 1) {
			t.Fatal("same-name neighbor's tool leaked", component)
		}
	}
}
