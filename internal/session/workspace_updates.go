package session

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// WelcomeScope is resolved by the host from the current workspace/lead, never
// decoded from a client body. Version fences that resolution inside each write.
type WelcomeScope struct {
	WorkspaceID, OwnerUserID, AgentInstanceID, AgentName string
	WorkspaceVersion                                     int64
}

var ErrWelcomeUnavailable = errors.New("workspace welcome is no longer available")

func (s WelcomeScope) valid() bool {
	return strings.TrimSpace(s.WorkspaceID) != "" && strings.TrimSpace(s.OwnerUserID) != "" &&
		strings.TrimSpace(s.AgentInstanceID) != "" && strings.TrimSpace(s.AgentName) != ""
}

func checkWelcomeWorkspace(ctx context.Context, tx *sql.Tx, scope WelcomeScope) error {
	if !scope.valid() {
		return ErrInvalidID
	}
	var owner, status string
	var version int64
	if err := tx.QueryRowContext(ctx, `SELECT owner_user_id,status,version FROM workspaces WHERE id=?`, scope.WorkspaceID).Scan(&owner, &status, &version); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrWelcomeUnavailable
		}
		return err
	}
	if owner != scope.OwnerUserID || status == "trashed" || status == "missing" || version != scope.WorkspaceVersion {
		return ErrWorkspaceVersionConflict
	}
	return nil
}

// WelcomeEnrollment binds recovery to the exact lead present at successful
// creation. Its integer ID orders bounded traversal; it is not a seen cursor.
type WelcomeEnrollment struct {
	ID int64
	WelcomeScope
}

// EnrollWelcome is called only at successful creation, never on import or GET.
// Repeating the same enrollment is harmless; a namesake/replacement cannot adopt it.
func (h *hybridStore) EnrollWelcome(ctx context.Context, scope WelcomeScope) error {
	return h.sqlite.db.InTransaction(ctx, func(tx *sql.Tx) error {
		if err := checkWelcomeWorkspace(ctx, tx, scope); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO workspace_welcome_enrollments(workspace_id,owner_user_id,agent_instance_id,agent_name)
			VALUES(?,?,?,?) ON CONFLICT(workspace_id) DO NOTHING`, scope.WorkspaceID, scope.OwnerUserID, scope.AgentInstanceID, scope.AgentName); err != nil {
			return err
		}
		var owner, instance, name string
		if err := tx.QueryRowContext(ctx, `SELECT owner_user_id,agent_instance_id,agent_name FROM workspace_welcome_enrollments WHERE workspace_id=?`, scope.WorkspaceID).Scan(&owner, &instance, &name); err != nil {
			return err
		}
		if owner != scope.OwnerUserID || instance != scope.AgentInstanceID || name != scope.AgentName {
			return ErrWelcomeUnavailable
		}
		return nil
	})
}

// PendingWelcomes visits only explicitly enrolled identities, not all workspaces.
// The worker wraps its keyset after exhaustion so an unready first page cannot
// starve later enrollments. Canonical readiness/version is re-read by the worker.
func (h *hybridStore) PendingWelcomes(ctx context.Context, after int64, limit int) ([]WelcomeEnrollment, error) {
	if after < 0 || limit < 1 || limit > 100 {
		return nil, ErrInvalidID
	}
	rows, err := h.sqlite.db.QueryContext(ctx, `SELECT id,workspace_id,owner_user_id,agent_instance_id,agent_name
		FROM workspace_welcome_enrollments WHERE settled=0 AND id>? ORDER BY id LIMIT ?`, after, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []WelcomeEnrollment{}
	for rows.Next() {
		var item WelcomeEnrollment
		if err := rows.Scan(&item.ID, &item.WorkspaceID, &item.OwnerUserID, &item.AgentInstanceID, &item.AgentName); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// SettleWelcome retires only the original enrollment, including invalidated or
// suppressed ones. It never deletes the receipt or rearms it on another identity.
func (h *hybridStore) SettleWelcome(ctx context.Context, item WelcomeEnrollment) error {
	if item.ID <= 0 || !item.WelcomeScope.valid() {
		return ErrInvalidID
	}
	_, err := h.sqlite.db.ExecContext(ctx, `UPDATE workspace_welcome_enrollments SET settled=1
		WHERE id=? AND workspace_id=? AND owner_user_id=? AND agent_instance_id=? AND agent_name=?`,
		item.ID, item.WorkspaceID, item.OwnerUserID, item.AgentInstanceID, item.AgentName)
	return err
}

// WelcomeMessage is a counts/identity-only projection. Content stays in chat.
type WelcomeMessage struct {
	MessageID   string     `json:"message_id"`
	Revision    int        `json:"revision"`
	DeliveredAt time.Time  `json:"delivered_at"`
	SeenAt      *time.Time `json:"seen_at,omitempty"`
}

type WelcomeDelivery struct {
	SessionID string           `json:"session_id,omitempty"`
	Messages  []WelcomeMessage `json:"messages"`
	Unread    int              `json:"unread_count"`
}

// currentWelcomeSession rejects suppressed receipts and deleted/moved threads.
// A legacy receipt can return a real session with zero tracked (unread) messages.
func currentWelcomeSession(ctx context.Context, tx *sql.Tx, scope WelcomeScope) (string, error) {
	var id string
	err := tx.QueryRowContext(ctx, `SELECT r.session_id FROM workspace_lead_conversations r
		JOIN sessions s ON s.id=r.session_id AND s.workspace_id=r.workspace_id AND s.agent_name=r.agent_name
		WHERE r.workspace_id=? AND r.owner_user_id=? AND r.agent_instance_id=? AND r.agent_name=?`,
		scope.WorkspaceID, scope.OwnerUserID, scope.AgentInstanceID, scope.AgentName).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrWelcomeUnavailable
	}
	return id, err
}

// ReadWelcome is a projection only: it never enrolls, delivers or marks seen.
func (h *hybridStore) ReadWelcome(ctx context.Context, scope WelcomeScope) (WelcomeDelivery, error) {
	out := WelcomeDelivery{Messages: []WelcomeMessage{}}
	err := h.sqlite.db.InTransaction(ctx, func(tx *sql.Tx) error {
		if err := checkWelcomeWorkspace(ctx, tx, scope); err != nil {
			return err
		}
		// No receipt (or an exact suppressed receipt) is a known absence,
		// unlike a delivered thread whose destination became unavailable.
		var savedSession, owner, instance, name string
		err := tx.QueryRowContext(ctx, `SELECT session_id,owner_user_id,agent_instance_id,agent_name FROM workspace_lead_conversations WHERE workspace_id=?`, scope.WorkspaceID).Scan(&savedSession, &owner, &instance, &name)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if owner != scope.OwnerUserID || instance != scope.AgentInstanceID || name != scope.AgentName {
			return ErrWelcomeUnavailable
		}
		if savedSession == "" {
			return nil
		}
		id, err := currentWelcomeSession(ctx, tx, scope)
		if err != nil {
			return err
		}
		out.SessionID = id
		rows, err := tx.QueryContext(ctx, `SELECT u.message_id,u.revision,u.delivered_at,u.seen_at
			FROM workspace_welcome_messages u JOIN messages m ON m.id=u.message_id AND m.session_id=u.session_id AND m.role='assistant'
			WHERE u.workspace_id=? AND u.session_id=? ORDER BY u.revision`, scope.WorkspaceID, id)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var message WelcomeMessage
			if err := rows.Scan(&message.MessageID, &message.Revision, &message.DeliveredAt, &message.SeenAt); err != nil {
				return err
			}
			if message.SeenAt == nil {
				out.Unread++
			}
			out.Messages = append(out.Messages, message)
		}
		return rows.Err()
	})
	if err != nil {
		return WelcomeDelivery{}, err
	}
	return out, nil
}

// WelcomeAcknowledgement names one exact delivered message, never a high-water
// cursor. A delayed acknowledgement of revision 1 cannot clear revision 2.
type WelcomeAcknowledgement struct {
	SessionID string `json:"session_id"`
	MessageID string `json:"message_id"`
	Revision  int    `json:"revision"`
}

func (h *hybridStore) AcknowledgeWelcome(ctx context.Context, scope WelcomeScope, ack WelcomeAcknowledgement) error {
	if ack.SessionID == "" || ack.MessageID == "" || (ack.Revision != 1 && ack.Revision != 2) {
		return ErrInvalidID
	}
	return h.sqlite.db.InTransaction(ctx, func(tx *sql.Tx) error {
		if err := checkWelcomeWorkspace(ctx, tx, scope); err != nil {
			return err
		}
		id, err := currentWelcomeSession(ctx, tx, scope)
		if err != nil {
			return err
		}
		if id != ack.SessionID {
			return ErrWelcomeUnavailable
		}
		result, err := tx.ExecContext(ctx, `UPDATE workspace_welcome_messages SET seen_at=COALESCE(seen_at,?)
			WHERE workspace_id=? AND session_id=? AND message_id=? AND revision=?
			AND EXISTS (SELECT 1 FROM messages WHERE id=? AND session_id=? AND role='assistant')`,
			time.Now().UTC(), scope.WorkspaceID, ack.SessionID, ack.MessageID, ack.Revision, ack.MessageID, ack.SessionID)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if count != 1 {
			return ErrWelcomeUnavailable
		}
		return nil
	})
}

// recordWelcomeMessage must share the message/receipt transaction. A failure
// rolls back the chat too; a process exit after commit needs no publication replay.
func recordWelcomeMessage(ctx context.Context, tx *sql.Tx, workspaceID, sessionID, messageID string, revision int, at time.Time) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO workspace_welcome_messages(workspace_id,revision,session_id,message_id,delivered_at)
		VALUES(?,?,?,?,?)`, workspaceID, revision, sessionID, messageID, at)
	return err
}
