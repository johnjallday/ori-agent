package session

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

// FolderReportInput is one folder agent look's report for the workspace
// Commander's conversation (workspace chat sync). It is host-authored, never
// an HTTP session or message input.
type FolderReportInput struct {
	WorkspaceID string
	AgentName   string
	// ReportKey names the look's end; a report is posted once per key.
	ReportKey string
	Content   string
	// Title names a conversation created for the report, when the agent has
	// none in this workspace yet.
	Title string
}

// FolderReportMessage is where a report was posted, and whether the owner has
// seen it.
type FolderReportMessage struct {
	WorkspaceID string     `json:"workspace_id"`
	SessionID   string     `json:"session_id"`
	MessageID   string     `json:"message_id"`
	ReportKey   string     `json:"report_key"`
	DeliveredAt time.Time  `json:"delivered_at"`
	SeenAt      *time.Time `json:"seen_at,omitempty"`
}

// ErrFolderReportUnavailable is a report whose workspace is gone, or whose
// message was deleted with its conversation.
var ErrFolderReportUnavailable = errors.New("folder report unavailable")

const folderReportKeyMax = 200

// DeliverFolderReport posts the report as an assistant message in the agent's
// most recently used conversation in the workspace, the one the drawer opens
// for that agent, or in a new conversation when there is none. A key already
// delivered returns its message and changed=false, so a report announced
// twice is posted once. The message and its receipt share one transaction.
func (h *hybridStore) DeliverFolderReport(ctx context.Context, in FolderReportInput) (FolderReportMessage, bool, error) {
	in.WorkspaceID = strings.TrimSpace(in.WorkspaceID)
	in.AgentName = strings.TrimSpace(in.AgentName)
	in.ReportKey = strings.TrimSpace(in.ReportKey)
	if in.WorkspaceID == "" || in.AgentName == "" || in.ReportKey == "" ||
		len(in.ReportKey) > folderReportKeyMax || strings.TrimSpace(in.Content) == "" {
		return FolderReportMessage{}, false, ErrInvalidID
	}
	var out FolderReportMessage
	changed := false
	err := h.sqlite.db.InTransaction(ctx, func(tx *sql.Tx) error {
		var status string
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(status,'') FROM workspaces WHERE id=?`, in.WorkspaceID).Scan(&status); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrFolderReportUnavailable
			}
			return err
		}
		if status == "trashed" || status == "missing" {
			return ErrFolderReportUnavailable
		}
		existing, err := scanFolderReport(tx.QueryRowContext(ctx, `SELECT workspace_id,session_id,message_id,report_key,delivered_at,seen_at
			FROM workspace_folder_reports WHERE workspace_id=? AND report_key=?`, in.WorkspaceID, in.ReportKey))
		if err == nil {
			out = existing
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		now := time.Now().UTC()
		var sessionID string
		err = tx.QueryRowContext(ctx, `SELECT id FROM sessions WHERE workspace_id=? AND agent_name=? COLLATE NOCASE
			ORDER BY updated_at DESC, created_at DESC LIMIT 1`, in.WorkspaceID, in.AgentName).Scan(&sessionID)
		if errors.Is(err, sql.ErrNoRows) {
			sessionID = uuid.NewString()
			title := strings.TrimSpace(in.Title)
			if title == "" {
				title = "Getting started"
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO sessions(id,title,agent_name,workspace_id,message_count,created_at,updated_at) VALUES(?,?,?,?,0,?,?)`,
				sessionID, title, in.AgentName, in.WorkspaceID, now, now); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		messageID, err := insertHostMessage(ctx, tx, sessionID, in.Content, now)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO workspace_folder_reports(workspace_id,report_key,session_id,message_id,delivered_at)
			VALUES(?,?,?,?,?)`, in.WorkspaceID, in.ReportKey, sessionID, messageID, now); err != nil {
			return err
		}
		out = FolderReportMessage{WorkspaceID: in.WorkspaceID, SessionID: sessionID, MessageID: messageID, ReportKey: in.ReportKey, DeliveredAt: now}
		changed = true
		return nil
	})
	if err != nil {
		return FolderReportMessage{}, false, err
	}
	if changed {
		h.cache.Remove(out.SessionID)
	}
	return out, changed, nil
}

// LatestFolderReport is the newest report still in its conversation: one whose
// message was deleted with the conversation is not offered again.
func (h *hybridStore) LatestFolderReport(ctx context.Context, workspaceID string) (FolderReportMessage, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return FolderReportMessage{}, ErrInvalidID
	}
	out, err := scanFolderReport(h.sqlite.db.QueryRowContext(ctx, `SELECT r.workspace_id,r.session_id,r.message_id,r.report_key,r.delivered_at,r.seen_at
		FROM workspace_folder_reports r
		JOIN messages m ON m.id=r.message_id AND m.session_id=r.session_id AND m.role='assistant'
		JOIN sessions s ON s.id=r.session_id AND s.workspace_id=r.workspace_id
		WHERE r.workspace_id=? ORDER BY r.delivered_at DESC, r.rowid DESC LIMIT 1`, workspaceID))
	if errors.Is(err, sql.ErrNoRows) {
		return FolderReportMessage{}, ErrFolderReportUnavailable
	}
	return out, err
}

// AcknowledgeFolderReport marks one exact report message seen; a message that
// is not this workspace's report is refused.
func (h *hybridStore) AcknowledgeFolderReport(ctx context.Context, workspaceID, messageID string) error {
	workspaceID, messageID = strings.TrimSpace(workspaceID), strings.TrimSpace(messageID)
	if workspaceID == "" || messageID == "" {
		return ErrInvalidID
	}
	result, err := h.sqlite.db.ExecContext(ctx, `UPDATE workspace_folder_reports SET seen_at=COALESCE(seen_at,?)
		WHERE workspace_id=? AND message_id=?
		AND EXISTS (SELECT 1 FROM messages WHERE id=? AND session_id=workspace_folder_reports.session_id AND role='assistant')`,
		time.Now().UTC(), workspaceID, messageID, messageID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrFolderReportUnavailable
	}
	return nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanFolderReport(row rowScanner) (FolderReportMessage, error) {
	var out FolderReportMessage
	var seen sql.NullTime
	if err := row.Scan(&out.WorkspaceID, &out.SessionID, &out.MessageID, &out.ReportKey, &out.DeliveredAt, &seen); err != nil {
		return FolderReportMessage{}, err
	}
	if seen.Valid {
		at := seen.Time
		out.SeenAt = &at
	}
	return out, nil
}

// insertHostMessage appends one host-authored assistant message to a
// conversation, the way a welcome is delivered: no model, no tokens.
func insertHostMessage(ctx context.Context, tx *sql.Tx, sessionID, content string, at time.Time) (string, error) {
	messageID := uuid.NewString()
	if _, err := tx.ExecContext(ctx, `INSERT INTO messages(id,session_id,role,content,model,tokens_used,created_at) VALUES(?,?,'assistant',?,'',0,?)`,
		messageID, sessionID, content, at); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sessions SET message_count=message_count+1,updated_at=? WHERE id=?`, at, sessionID); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sessions_fts SET content=content || ' ' || ? WHERE session_id=?`, content, sessionID); err != nil {
		return "", err
	}
	return messageID, nil
}
