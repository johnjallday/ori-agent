package session

import (
	"context"
	"database/sql"

	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

// RestoreContinuitySession inserts a source-owned session and inert tag history
// in the caller's receipt transaction. No agent is resolved by name and the
// message count starts at zero until separately claimed messages are inserted.
func (s *SQLiteStore) RestoreContinuitySession(ctx context.Context, tx *sql.Tx, scope workspacecontinuity.RestoreScope, record workspacecontinuity.Record) (bool, error) {
	if s == nil || tx == nil || scope.UserID != "local" {
		return false, workspacecontinuity.ErrInvalid
	}
	value, err := DecodeContinuitySession(record, scope.WorkspaceID)
	if err != nil {
		return false, err
	}
	claimed, err := workspacecontinuity.ClaimRecord(ctx, tx, scope, "sessions", "sessions", record.ID, workspacecontinuity.Digest(record.Data))
	if err != nil || !claimed {
		return false, err
	}
	if err := workspacecontinuity.RequireOwnedWorkspace(ctx, tx, scope); err != nil {
		return false, err
	}
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions WHERE id=?`, value.ID).Scan(&exists); err != nil {
		return false, err
	}
	if exists != 0 {
		return false, workspacecontinuity.ErrConflict
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO sessions(id,title,agent_name,workspace_id,message_count,created_at,updated_at) VALUES(?,?,?,?,0,?,?)`,
		value.ID, value.Title, value.AgentName, scope.WorkspaceID, value.CreatedAt, value.UpdatedAt); err != nil {
		return false, workspacecontinuity.ErrConflict
	}
	for _, tag := range value.Tags {
		if _, err := tx.ExecContext(ctx, `INSERT INTO session_tags(session_id,tag,created_at) VALUES(?,?,?)`, value.ID, tag.Name, tag.CreatedAt); err != nil {
			return false, workspacecontinuity.ErrConflict
		}
	}
	return true, nil
}

// RestoreContinuityMessage admits only an exact previously receipt-owned
// session in this workspace. It inserts historical text/metadata and updates
// the normal FTS projection, but does not resume any model/tool work. A retry
// returns before reading mutable rows, preserving later edits/deletions. The
// caller must roll back on ANY error and verify final source message counts.
func (s *SQLiteStore) RestoreContinuityMessage(ctx context.Context, tx *sql.Tx, scope workspacecontinuity.RestoreScope, record workspacecontinuity.Record) (bool, error) {
	if s == nil || tx == nil || scope.UserID != "local" {
		return false, workspacecontinuity.ErrInvalid
	}
	value, err := DecodeContinuityMessage(record, scope.WorkspaceID)
	if err != nil {
		return false, err
	}
	claimed, err := workspacecontinuity.ClaimRecord(ctx, tx, scope, "sessions", "messages", record.ID, workspacecontinuity.Digest(record.Data))
	if err != nil || !claimed {
		return false, err
	}
	if err := workspacecontinuity.RequireOwnedWorkspace(ctx, tx, scope); err != nil {
		return false, err
	}
	m := value.Message
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions s JOIN continuity_records r ON r.domain='sessions' AND r.family='sessions' AND r.record_id=s.id
		WHERE s.id=? AND s.workspace_id=? AND r.operation_id=? AND r.workspace_id=?`, m.SessionID, scope.WorkspaceID, scope.OperationID, scope.WorkspaceID).Scan(&count); err != nil {
		return false, err
	}
	if count != 1 {
		return false, workspacecontinuity.ErrConflict
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages WHERE id=?`, m.ID).Scan(&count); err != nil {
		return false, err
	}
	if count != 0 {
		return false, workspacecontinuity.ErrConflict
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO messages(id,session_id,role,content,model,tokens_used,created_at,continuity_source_sequence) VALUES(?,?,?,?,?,?,?,?)`,
		m.ID, m.SessionID, m.Role, m.Content, nullContinuityModel(m.Model), m.TokensUsed, m.CreatedAt, value.SourceSequence); err != nil {
		return false, workspacecontinuity.ErrConflict
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sessions SET message_count=message_count+1 WHERE id=? AND workspace_id=?`, m.SessionID, scope.WorkspaceID); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sessions_fts SET content=content||' '||? WHERE session_id=?`, m.Content, m.SessionID); err != nil {
		return false, err
	}
	return true, nil
}

func nullContinuityModel(model string) any {
	if model == "" {
		return nil
	}
	return model
}

// CheckContinuitySessionCount is the caller's final, same-transaction
// completeness check, never permission to expose a partially restored chat.
func (s *SQLiteStore) CheckContinuitySessionCount(ctx context.Context, tx *sql.Tx, scope workspacecontinuity.RestoreScope, record workspacecontinuity.Record) error {
	if s == nil || tx == nil || scope.UserID != "local" {
		return workspacecontinuity.ErrInvalid
	}
	value, err := DecodeContinuitySession(record, scope.WorkspaceID)
	if err != nil {
		return err
	}
	var status, digest string
	if err := tx.QueryRowContext(ctx, `SELECT o.status,r.source_digest FROM continuity_records r JOIN continuity_operations o ON o.id=r.operation_id
		WHERE r.domain='sessions' AND r.family='sessions' AND r.record_id=? AND r.workspace_id=? AND o.id=? AND o.owner_user_id=?`,
		value.ID, scope.WorkspaceID, scope.OperationID, scope.UserID).Scan(&status, &digest); err != nil || digest != workspacecontinuity.Digest(record.Data) {
		return workspacecontinuity.ErrConflict
	}
	if status == "complete" {
		return nil
	} // exact receipt owns the prior result, not later mutable rows
	var current, actual int
	if err := tx.QueryRowContext(ctx, `SELECT s.message_count,(SELECT COUNT(*) FROM messages WHERE session_id=s.id)
		FROM sessions s JOIN continuity_records r ON r.domain='sessions' AND r.family='sessions' AND r.record_id=s.id
		WHERE s.id=? AND s.workspace_id=? AND r.operation_id=? AND r.workspace_id=?`, value.ID, scope.WorkspaceID, scope.OperationID, scope.WorkspaceID).Scan(&current, &actual); err != nil {
		return workspacecontinuity.ErrIncomplete
	}
	if current != value.MessageCount || actual != value.MessageCount {
		return workspacecontinuity.ErrIncomplete
	}
	return nil
}
