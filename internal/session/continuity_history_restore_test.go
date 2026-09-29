package session

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

// Synthetic receipt setup only: it does not install files, complete other
// components or make the imported workspace active.
func seedSessionRestoreReceipt(t *testing.T, db *database.DB, owner string) workspacecontinuity.RestoreScope {
	t.Helper()
	ctx := t.Context()
	local := workspacecontinuity.NewLocalStore(db)
	op, err := local.BeginImport(ctx, workspacecontinuity.Operation{ID: uuid.NewString(), UserID: "local", Action: workspacecontinuity.WorkspaceOnly,
		TreeDigest: workspacecontinuity.Digest([]byte("synthetic-session-tree")), DestinationDigest: workspacecontinuity.Digest([]byte("synthetic-destination"))},
		[]workspacecontinuity.ImportMember{{WorkspaceID: owner, Generation: uuid.NewString(), Digest: workspacecontinuity.Digest([]byte("synthetic-generation")), Disposition: workspacecontinuity.Ordinary}})
	if err != nil {
		t.Fatal(err)
	}
	scope := workspacecontinuity.RestoreScope{OperationID: op.ID, WorkspaceID: owner, UserID: "local"}
	if err := db.InTransaction(ctx, func(tx *sql.Tx) error {
		if _, err := workspacecontinuity.ClaimRecord(ctx, tx, scope, "workspace", "workspaces", owner, workspacecontinuity.Digest([]byte("synthetic-verified-workspace"))); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO workspaces(id,name,owner_user_id,created_at,updated_at) VALUES(?,?, 'local',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, owner, "Copied synthetic work")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return scope
}

func TestRestoreContinuitySessionPreservesHistorySearchAndExactRetry(t *testing.T) {
	source := continuitySessionDB(t, "source")
	original := continuityHistorySeed(t, source, 4)
	sessions, messages := collectSessionRecords(t, source, "owned")
	dest := continuitySessionDB(t, "destination")
	scope := seedSessionRestoreReceipt(t, dest, "owned")
	store := NewSQLiteStore(dest)
	if err := dest.InTransaction(t.Context(), func(tx *sql.Tx) error {
		_, err := store.RestoreContinuityMessage(t.Context(), tx, scope, messages[0])
		return err
	}); !errors.Is(err, workspacecontinuity.ErrConflict) {
		t.Fatal(err)
	}
	if err := dest.InTransaction(t.Context(), func(tx *sql.Tx) error {
		inserted, err := store.RestoreContinuitySession(t.Context(), tx, scope, sessions[0])
		if err != nil || !inserted {
			t.Fatalf("session restore: %v %t", err, inserted)
		}
		for i := len(messages) - 1; i >= 0; i-- { // equal-time display must follow source sequence, not restore arrival
			inserted, err := store.RestoreContinuityMessage(t.Context(), tx, scope, messages[i])
			if err != nil || !inserted {
				return fmt.Errorf("message restore: %v %t", err, inserted)
			}
		}
		return store.CheckContinuitySessionCount(t.Context(), tx, scope, sessions[0])
	}); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetSession(t.Context(), "saved")
	if err != nil || got.FolderID != "owned" || got.AgentName != "same-name" || got.MessageCount != 4 || len(got.Tags) != 1 || len(got.Messages) != 4 {
		t.Fatal("restored session not readable", got, err)
	}
	for i, m := range got.Messages {
		if m.ID != original[i].ID || m.Content != original[i].Content || !m.CreatedAt.Equal(original[i].CreatedAt) {
			t.Fatal("old message reordered or restamped", i, m)
		}
	}
	owner := "owned"
	matches, total, err := store.Search(t.Context(), "private", &SessionFilter{FolderID: &owner}, nil)
	if err != nil || total != 1 || len(matches) != 1 || matches[0].ID != "saved" {
		t.Fatal("restored history missing from normal search", total, matches, err)
	}
	matches, total, err = store.Search(t.Context(), "private", nil, nil)
	if err != nil || total != 1 || len(matches) != 1 || matches[0].ID != "saved" {
		t.Fatal("restored history missing from unfiltered search", total, matches, err)
	}
	if _, err := dest.ExecContext(t.Context(), `UPDATE sessions SET title='Local title' WHERE id='saved'`); err != nil {
		t.Fatal(err)
	}
	if _, err := dest.ExecContext(t.Context(), `UPDATE messages SET content='Local edited message' WHERE id=?`, original[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := dest.InTransaction(t.Context(), func(tx *sql.Tx) error {
		if inserted, err := store.RestoreContinuitySession(t.Context(), tx, scope, sessions[0]); err != nil || inserted {
			t.Fatalf("session retry replaced local edit: %v %t", err, inserted)
		}
		for _, record := range messages {
			if inserted, err := store.RestoreContinuityMessage(t.Context(), tx, scope, record); err != nil || inserted {
				return fmt.Errorf("message retry replaced local edit: %v %t", err, inserted)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	got, err = store.GetSession(t.Context(), "saved")
	if err != nil || got.Title != "Local title" || got.Messages[0].Content != "Local edited message" {
		t.Fatal("exact retry overwrote local work", got, err)
	}
	if _, err := dest.ExecContext(t.Context(), `DELETE FROM messages WHERE id=?`, original[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := dest.InTransaction(t.Context(), func(tx *sql.Tx) error {
		inserted, err := store.RestoreContinuityMessage(t.Context(), tx, scope, messages[0])
		if inserted {
			t.Fatal("retry resurrected deleted message")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	// Simulate a previously completed exact receipt only: real completion must
	// wait for every other domain and the production coordinator.
	if _, err := dest.ExecContext(t.Context(), `UPDATE continuity_operations SET status='complete' WHERE id=?`, scope.OperationID); err != nil {
		t.Fatal(err)
	}
	if err := dest.InTransaction(t.Context(), func(tx *sql.Tx) error { return store.CheckContinuitySessionCount(t.Context(), tx, scope, sessions[0]) }); err != nil {
		t.Fatal("completed retry rechecked deleted local history", err)
	}
}

func TestRestoreContinuitySessionRefusesLocalCollisionAndIncompleteCount(t *testing.T) {
	source := continuitySessionDB(t, "source")
	continuityHistorySeed(t, source, 2)
	sessions, messages := collectSessionRecords(t, source, "owned")
	dest := continuitySessionDB(t, "destination")
	scope := seedSessionRestoreReceipt(t, dest, "owned")
	store := NewSQLiteStore(dest)
	if _, err := dest.ExecContext(t.Context(), `INSERT INTO sessions(id,title,agent_name,workspace_id,created_at,updated_at) VALUES('saved','Incumbent','other','owned',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	if err := dest.InTransaction(t.Context(), func(tx *sql.Tx) error {
		_, err := store.RestoreContinuitySession(t.Context(), tx, scope, sessions[0])
		return err
	}); !errors.Is(err, workspacecontinuity.ErrConflict) {
		t.Fatal("foreign incumbent adopted", err)
	}
	if _, err := dest.ExecContext(t.Context(), `DELETE FROM sessions WHERE id='saved'`); err != nil {
		t.Fatal(err)
	}
	if err := dest.InTransaction(t.Context(), func(tx *sql.Tx) error {
		if _, err := store.RestoreContinuitySession(t.Context(), tx, scope, sessions[0]); err != nil {
			return err
		}
		if _, err := store.RestoreContinuityMessage(t.Context(), tx, scope, messages[0]); err != nil {
			return err
		}
		return store.CheckContinuitySessionCount(t.Context(), tx, scope, sessions[0])
	}); !errors.Is(err, workspacecontinuity.ErrIncomplete) {
		t.Fatal("incomplete message set committed", err)
	}
	if _, err := store.GetSession(context.Background(), "saved"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatal("failed transaction left a partial session", err)
	}
}
