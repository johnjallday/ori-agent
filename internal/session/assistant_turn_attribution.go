package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/assistantcontext"
	"github.com/johnjallday/ori-agent/internal/foldercontext"
)

// AssistantTurnStore extends canonical Messages with one atomic, attributed
// turn. An optional folder event keeps the existing revision CAS in the same
// transaction. Ordinary writers cannot submit this metadata.
type AssistantTurnStore interface {
	AppendAttributedTurn(context.Context, string, assistantcontext.SaveOwner, *foldercontext.Event, string, string, string, *assistantcontext.Attribution) ([]Message, error)
}

func (h *hybridStore) AppendAttributedTurn(ctx context.Context, id string, owner assistantcontext.SaveOwner, event *foldercontext.Event, revision, user, answer string, attribution *assistantcontext.Attribution) ([]Message, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	messages, err := h.sqlite.AppendAttributedTurn(ctx, id, owner, event, revision, user, answer, attribution)
	h.cache.Remove(id)
	return messages, err
}

func (s *SQLiteStore) AppendAttributedTurn(ctx context.Context, id string, owner assistantcontext.SaveOwner, event *foldercontext.Event, revision, user, answer string, attribution *assistantcontext.Attribution) ([]Message, error) {
	encodedAttribution, err := assistantcontext.EncodeAttribution(attribution)
	if err != nil {
		return nil, err
	}
	var folderJSON any
	if event != nil {
		if err := event.Validate(); err != nil {
			return nil, err
		}
		encoded, err := json.Marshal(event)
		if err != nil || len(encoded) > foldercontext.MaxBytes+256 {
			return nil, foldercontext.ErrInvalid
		}
		folderJSON = string(encoded)
	}
	now := time.Now().UTC()
	messages := []Message{}
	if event != nil {
		messages = append(messages, Message{ID: uuid.NewString(), SessionID: id, Role: RoleSystem, CreatedAt: now, FolderContext: event})
	}
	for _, text := range []struct {
		role    MessageRole
		content string
	}{{RoleUser, user}, {RoleAssistant, answer}} {
		if text.content != "" {
			messages = append(messages, Message{ID: uuid.NewString(), SessionID: id, Role: text.role, Content: text.content, CreatedAt: now, WorkspaceContext: attribution})
		}
	}
	err = s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		var actualWorkspace sql.NullString
		var actualAgent string
		var actualCount int
		var actualUpdated time.Time
		if err := tx.QueryRowContext(ctx, `SELECT workspace_id,agent_name,message_count,updated_at FROM sessions WHERE id=?`, id).Scan(&actualWorkspace, &actualAgent, &actualCount, &actualUpdated); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrSessionNotFound
			}
			return err
		}
		if owner.ExpectedConversationRevision != "" && owner.ExpectedConversationRevision != assistantcontext.ConversationRevision(actualUpdated, actualCount) {
			return ErrFolderContextConflict
		}
		if actualWorkspace.String != owner.WorkspaceID || !strings.EqualFold(actualAgent, owner.AgentName) {
			return ErrFolderContextConflict
		}
		if err := validateAssistantSaveOwner(ctx, tx, owner); err != nil {
			return err
		}
		if event != nil {
			var current string
			err := tx.QueryRowContext(ctx, `SELECT id FROM messages WHERE session_id=? AND folder_context_json IS NOT NULL AND continuity_source_sequence IS NULL ORDER BY rowid DESC LIMIT 1`, id).Scan(&current)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if current != revision {
				return ErrFolderContextConflict
			}
		}
		for _, message := range messages {
			var folder, turn any
			if message.Role == RoleSystem {
				folder = folderJSON
			} else if encodedAttribution != "" {
				turn = encodedAttribution
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO messages(id,session_id,role,content,model,tokens_used,created_at,folder_context_json,turn_context_json) VALUES(?,?,?,?,'',0,?,?,?)`, message.ID, id, message.Role, message.Content, message.CreatedAt, folder, turn); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE sessions SET message_count=message_count+?,updated_at=? WHERE id=?`, len(messages), now, id); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE sessions_fts SET content=content||' '||?||' '||? WHERE session_id=?`, user, answer, id)
		return err
	})
	if err != nil {
		return nil, err
	}
	return messages, nil
}

func validateAssistantSaveOwner(ctx context.Context, tx *sql.Tx, owner assistantcontext.SaveOwner) error {
	if owner.UserID == "" {
		return nil
	} // Existing non-attributed folder writer.
	var status, hq, profile string
	var version int64
	if err := tx.QueryRowContext(ctx, `SELECT status,hq_workspace_id,global_agent_profile_name,state_version FROM personal_assistant_state WHERE user_id=?`, owner.UserID).Scan(&status, &hq, &profile, &version); err != nil {
		return ErrFolderContextConflict
	}
	if (status != "active" && status != "paused") || version != owner.StateVersion || hq != owner.WorkspaceID || profile != owner.AgentName {
		return ErrFolderContextConflict
	}
	for _, id := range append([]string{owner.WorkspaceID}, owner.ContextWorkspaceIDs...) {
		var user, status string
		if err := tx.QueryRowContext(ctx, `SELECT owner_user_id,status FROM workspaces WHERE id=?`, id).Scan(&user, &status); err != nil {
			return ErrFolderContextConflict
		}
		if (user != owner.UserID && (user != "" || owner.UserID != "local")) || status == "trashed" || status == "missing" {
			return ErrFolderContextConflict
		}
	}
	return nil
}
