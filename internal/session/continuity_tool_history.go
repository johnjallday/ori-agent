package session

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

// ContinuityToolCall preserves a historical result. It does not contain
// permission to call a tool, queued work, a runtime grant or a native session.
type ContinuityToolCall struct {
	Version     int      `json:"version"`
	WorkspaceID string   `json:"workspace_id"`
	Call        ToolCall `json:"call"`
}

func SnapshotContinuityToolCall(value ContinuityToolCall, owner string) (workspacecontinuity.Record, error) {
	call := value.Call
	if value.Version != 1 || value.WorkspaceID != owner || !workspacecontinuity.ValidID(owner) || !workspacecontinuity.ValidID(call.ID) ||
		!workspacecontinuity.ValidID(call.SessionID) || call.MessageID != "" && !workspacecontinuity.ValidID(call.MessageID) ||
		call.ToolName == "" || len(call.ToolName) > 4096 || !utf8.ValidString(call.ToolName) || !utf8.ValidString(call.Arguments) ||
		!utf8.ValidString(call.Result) || !utf8.ValidString(call.Error) || call.DurationMs < 0 || call.CreatedAt.IsZero() {
		return workspacecontinuity.Record{}, workspacecontinuity.ErrInvalid
	}
	return workspacecontinuity.EncodeRecord(call.ID, value)
}

func DecodeContinuityToolCall(record workspacecontinuity.Record, owner string) (ContinuityToolCall, error) {
	var value ContinuityToolCall
	if err := workspacecontinuity.DecodeRecord(record, &value); err != nil {
		return value, err
	}
	if value.Version != 1 {
		return value, workspacecontinuity.ErrVersion
	}
	encoded, err := SnapshotContinuityToolCall(value, owner)
	if err != nil {
		return value, err
	}
	if record.ID != encoded.ID || !bytes.Equal(record.Data, encoded.Data) {
		return value, workspacecontinuity.ErrInvalid
	}
	return value, nil
}

// CollectContinuityToolHistory uses the same caller-owned SQL view as session
// history. Only explicitly workspace-owned session IDs participate; every
// referenced message must belong to that same session before export. A forged
// history row cannot acquire authority by matching an agent name.
func CollectContinuityToolHistory(ctx context.Context, query workspacecontinuity.Queryer, owner string, spool *workspacecontinuity.Spool) error {
	if query == nil || spool == nil || !workspacecontinuity.ValidID(owner) {
		return workspacecontinuity.ErrInvalid
	}
	if err := requireContinuitySourceOwner(ctx, query, owner); err != nil {
		return err
	}
	const pageSize = 256
	after, count := "", 0
	for {
		rows, err := query.QueryContext(ctx, `SELECT tc.id,tc.message_id,tc.session_id,tc.tool_name,tc.arguments,tc.result,tc.error,tc.duration_ms,tc.created_at
			FROM tool_calls tc JOIN sessions s ON s.id=tc.session_id WHERE s.workspace_id=? AND tc.id>? ORDER BY tc.id LIMIT ?`, owner, after, pageSize)
		if err != nil {
			return err
		}
		page := make([]ToolCall, 0, pageSize)
		for rows.Next() {
			var tc ToolCall
			var args, result, errorMessage sql.NullString
			var duration sql.NullInt64
			if err := rows.Scan(&tc.ID, &tc.MessageID, &tc.SessionID, &tc.ToolName, &args, &result, &errorMessage, &duration, &tc.CreatedAt); err != nil {
				_ = rows.Close()
				return err
			}
			tc.Arguments, tc.Result, tc.Error = args.String, result.String, errorMessage.String
			if duration.Valid {
				if duration.Int64 < 0 || duration.Int64 > int64(int(^uint(0)>>1)) {
					_ = rows.Close()
					return workspacecontinuity.ErrLimit
				}
				tc.DurationMs = int(duration.Int64)
			}
			page = append(page, tc)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		for _, call := range page {
			if err := ctx.Err(); err != nil {
				return err
			}
			if count >= workspacecontinuity.MaxReferences*workspacecontinuity.MaxRecords {
				return workspacecontinuity.ErrLimit
			}
			if call.MessageID != "" {
				var owned int
				if err := query.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages WHERE id=? AND session_id=?`, call.MessageID, call.SessionID).Scan(&owned); err != nil {
					return err
				}
				if owned != 1 {
					return workspacecontinuity.ErrIncomplete
				}
			}
			record, err := SnapshotContinuityToolCall(ContinuityToolCall{Version: 1, WorkspaceID: owner, Call: call}, owner)
			if err != nil {
				return err
			}
			if err := spool.AddRecord(ctx, "tool_history", "tool_calls", record); err != nil {
				return err
			}
			after = call.ID
			count++
		}
		if len(page) < pageSize {
			break
		}
	}
	if count == 0 {
		return spool.SetAvailability(ctx, "tool_history", workspacecontinuity.Empty, "")
	}
	return nil
}

// RestoreContinuityToolCall inserts only an inert historical row referencing
// the same operation's exact restored session/message. No callback executes
// the saved tool or acquires local grants; an exact retry preserves local edits.
func (s *SQLiteToolCallStore) RestoreContinuityToolCall(ctx context.Context, tx *sql.Tx, scope workspacecontinuity.RestoreScope, record workspacecontinuity.Record) (bool, error) {
	if s == nil || tx == nil || scope.UserID != "local" {
		return false, workspacecontinuity.ErrInvalid
	}
	value, err := DecodeContinuityToolCall(record, scope.WorkspaceID)
	if err != nil {
		return false, err
	}
	claimed, err := workspacecontinuity.ClaimRecord(ctx, tx, scope, "tool_history", "tool_calls", record.ID, workspacecontinuity.Digest(record.Data))
	if err != nil || !claimed {
		return false, err
	}
	if err := workspacecontinuity.RequireOwnedWorkspace(ctx, tx, scope); err != nil {
		return false, err
	}
	call := value.Call
	var owned int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions s JOIN continuity_records r ON r.domain='sessions' AND r.family='sessions' AND r.record_id=s.id
		WHERE s.id=? AND s.workspace_id=? AND r.operation_id=? AND r.workspace_id=?`, call.SessionID, scope.WorkspaceID, scope.OperationID, scope.WorkspaceID).Scan(&owned); err != nil {
		return false, err
	}
	if owned != 1 {
		return false, workspacecontinuity.ErrConflict
	}
	if call.MessageID != "" {
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages m JOIN continuity_records r ON r.domain='sessions' AND r.family='messages' AND r.record_id=m.id
			WHERE m.id=? AND m.session_id=? AND r.operation_id=? AND r.workspace_id=?`, call.MessageID, call.SessionID, scope.OperationID, scope.WorkspaceID).Scan(&owned); err != nil {
			return false, err
		}
		if owned != 1 {
			return false, workspacecontinuity.ErrConflict
		}
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM tool_calls WHERE id=?`, call.ID).Scan(&owned); err != nil {
		return false, err
	}
	if owned != 0 {
		return false, workspacecontinuity.ErrConflict
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO tool_calls(id,message_id,session_id,tool_name,arguments,result,error,duration_ms,created_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		call.ID, call.MessageID, call.SessionID, call.ToolName, nullToolText(call.Arguments), nullToolText(call.Result), nullToolText(call.Error), call.DurationMs, call.CreatedAt); err != nil {
		return false, workspacecontinuity.ErrConflict
	}
	return true, nil
}

func nullToolText(text string) any {
	if text == "" {
		return nil
	}
	return text
}
