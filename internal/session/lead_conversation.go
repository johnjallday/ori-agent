package session

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

// LeadConversationInput is host-authored, never an HTTP session/message input.
// Version fences the exact workspace snapshot used to compose the welcome.
type LeadConversationInput struct {
	WorkspaceID, OwnerUserID, AgentInstanceID, AgentName string
	WorkspaceVersion                                     int64
	Introduction, Assessment                             string
}

type LeadConversationResult struct {
	SessionID          string `json:"session_id,omitempty"`
	Changed            bool   `json:"changed"`
	AwaitingAssessment bool   `json:"awaiting_assessment"`
}

// EnsureLeadConversation atomically delivers an introduction and at most one
// assessment into an ordinary session. User conversations suppress seeding; an
// enrolled folder lead's host-report-only thread can receive its later welcome.
// Deleted/moved sessions and replacement leads never inherit delivery. The
// receipt survives session deletion, so reloads cannot recreate a dismissed chat.
func (h *hybridStore) EnsureLeadConversation(ctx context.Context, in LeadConversationInput) (LeadConversationResult, error) {
	var out LeadConversationResult
	if in.WorkspaceID == "" || in.OwnerUserID == "" || in.AgentInstanceID == "" || in.AgentName == "" || strings.TrimSpace(in.Introduction) == "" {
		return out, ErrInvalidID
	}
	err := h.sqlite.db.InTransaction(ctx, func(tx *sql.Tx) error {
		var owner, status string
		var version int64
		if err := tx.QueryRowContext(ctx, `SELECT owner_user_id, status, version FROM workspaces WHERE id=?`, in.WorkspaceID).Scan(&owner, &status, &version); err != nil {
			return err
		}
		if owner != in.OwnerUserID || status == "trashed" || status == "missing" || version != in.WorkspaceVersion {
			return ErrWorkspaceVersionConflict
		}
		// Creation enrollment binds the original lead even before the first
		// message exists. Neither recovery nor the legacy POST may transfer
		// that pending welcome to a replacement instance with the same name.
		var enrolledOwner, enrolledInstance, enrolledName string
		var enrollmentSettled bool
		enrollmentErr := tx.QueryRowContext(ctx, `SELECT owner_user_id,agent_instance_id,agent_name,settled FROM workspace_welcome_enrollments WHERE workspace_id=?`, in.WorkspaceID).Scan(&enrolledOwner, &enrolledInstance, &enrolledName, &enrollmentSettled)
		if enrollmentErr != nil && !errors.Is(enrollmentErr, sql.ErrNoRows) {
			return enrollmentErr
		}
		if enrollmentErr == nil && (enrollmentSettled || enrolledOwner != in.OwnerUserID || enrolledInstance != in.AgentInstanceID || enrolledName != in.AgentName) {
			return nil
		}
		var instance, name, sessionID, deliveredOwner string
		var delivered bool
		err := tx.QueryRowContext(ctx, `SELECT owner_user_id, agent_instance_id, agent_name, session_id, assessment_delivered FROM workspace_lead_conversations WHERE workspace_id=?`, in.WorkspaceID).Scan(&deliveredOwner, &instance, &name, &sessionID, &delivered)
		if errors.Is(err, sql.ErrNoRows) {
			var existing int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions WHERE workspace_id=? AND agent_name=? COLLATE NOCASE`, in.WorkspaceID, in.AgentName).Scan(&existing); err != nil {
				return err
			}
			if existing == 0 {
				sessionID = uuid.NewString()
			} else if existing == 1 && enrollmentErr == nil {
				// A fast folder look can publish before the welcome worker. Only
				// reuse its verified host-only thread, never a user's conversation.
				// The enrollment above still fences the original owner and lead.
				err := tx.QueryRowContext(ctx, `SELECT s.id FROM sessions s
					WHERE s.workspace_id=? AND s.agent_name=? COLLATE NOCASE
					AND EXISTS (SELECT 1 FROM workspace_folder_reports r JOIN messages m ON m.id=r.message_id AND m.session_id=r.session_id
						WHERE r.workspace_id=s.workspace_id AND r.session_id=s.id)
					AND NOT EXISTS (SELECT 1 FROM messages m WHERE m.session_id=s.id AND
						(m.role!='assistant' OR NOT EXISTS (SELECT 1 FROM workspace_folder_reports r
						 WHERE r.workspace_id=s.workspace_id AND r.session_id=s.id AND r.message_id=m.id)))`,
					in.WorkspaceID, in.AgentName).Scan(&sessionID)
				if err != nil && !errors.Is(err, sql.ErrNoRows) {
					return err
				}
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO workspace_lead_conversations(workspace_id,owner_user_id,agent_instance_id,agent_name,session_id) VALUES(?,?,?,?,?)`, in.WorkspaceID, in.OwnerUserID, in.AgentInstanceID, in.AgentName, sessionID); err != nil {
				return err
			}
			if sessionID == "" {
				return nil
			}
			now := time.Now().UTC()
			if existing == 0 {
				if _, err := tx.ExecContext(ctx, `INSERT INTO sessions(id,title,agent_name,workspace_id,message_count,created_at,updated_at) VALUES(?,?,?,?,0,?,?)`, sessionID, "Getting started", in.AgentName, in.WorkspaceID, now, now); err != nil {
					return err
				}
			}
			content := in.Introduction
			if in.Assessment != "" {
				content += "\n\n" + in.Assessment
				delivered = true
			}
			if err := insertLeadMessage(ctx, tx, in.WorkspaceID, sessionID, content, 1, now); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE workspace_lead_conversations SET assessment_delivered=? WHERE workspace_id=?`, delivered, in.WorkspaceID); err != nil {
				return err
			}
			out = LeadConversationResult{SessionID: sessionID, Changed: true, AwaitingAssessment: !delivered}
			return nil
		}
		if err != nil {
			return err
		}
		if deliveredOwner != in.OwnerUserID || instance != in.AgentInstanceID || name != in.AgentName || sessionID == "" {
			return nil
		}
		var actualWorkspace, actualAgent string
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(workspace_id,''), agent_name FROM sessions WHERE id=?`, sessionID).Scan(&actualWorkspace, &actualAgent); errors.Is(err, sql.ErrNoRows) {
			return nil
		} else if err != nil {
			return err
		}
		if actualWorkspace != in.WorkspaceID || actualAgent != in.AgentName {
			return nil
		}
		out.SessionID = sessionID
		out.AwaitingAssessment = !delivered
		if delivered {
			return nil
		}
		// A user-led conversation owns the thread. Do not interrupt or append
		// a delayed unsolicited assessment after they have started talking.
		var users int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages WHERE session_id=? AND role='user'`, sessionID).Scan(&users); err != nil {
			return err
		}
		if users == 0 && in.Assessment == "" {
			return nil
		}
		if users == 0 {
			if err := insertLeadMessage(ctx, tx, in.WorkspaceID, sessionID, in.Assessment, 2, time.Now().UTC()); err != nil {
				return err
			}
			out.Changed = true
		}
		_, err = tx.ExecContext(ctx, `UPDATE workspace_lead_conversations SET assessment_delivered=1 WHERE workspace_id=?`, in.WorkspaceID)
		out.AwaitingAssessment = false
		return err
	})
	if err != nil {
		return LeadConversationResult{}, err
	}
	if out.Changed {
		h.cache.Remove(out.SessionID)
	}
	return out, nil
}

func insertLeadMessage(ctx context.Context, tx *sql.Tx, workspaceID, sessionID, content string, revision int, at time.Time) error {
	messageID := uuid.NewString()
	if _, err := tx.ExecContext(ctx, `INSERT INTO messages(id,session_id,role,content,model,tokens_used,created_at) VALUES(?,?,'assistant',?,'',0,?)`, messageID, sessionID, content, at); err != nil {
		return err
	}
	if err := recordWelcomeMessage(ctx, tx, workspaceID, sessionID, messageID, revision, at); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sessions SET message_count=message_count+1,updated_at=? WHERE id=?`, at, sessionID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE sessions_fts SET content=content || ' ' || ? WHERE session_id=?`, content, sessionID)
	return err
}
