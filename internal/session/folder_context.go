package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/foldercontext"
)

var ErrFolderContextConflict = errors.New("conversation folder context changed; reload before sending")

// FolderContextStore is deliberately separate from the general message writer.
// Reads bypass cached session projections. All state is in canonical Messages.
type FolderContextStore interface {
	GetFolderSession(context.Context, string) (*Session, error)
	GetFolderMessages(context.Context, string) ([]Message, error)
	AppendFolderTurn(ctx context.Context, id, workspaceID, agentName, revision string, event foldercontext.Event, user, answer string) ([]Message, error)
}

func (h *hybridStore) GetFolderSession(ctx context.Context, id string) (*Session, error) {
	return h.sqlite.GetSession(ctx, id)
}

func (s *SQLiteStore) GetFolderSession(ctx context.Context, id string) (*Session, error) {
	return s.GetSession(ctx, id)
}

func (h *hybridStore) GetFolderMessages(ctx context.Context, id string) ([]Message, error) {
	return h.sqlite.GetFolderMessages(ctx, id)
}

func (s *SQLiteStore) GetFolderMessages(ctx context.Context, id string) ([]Message, error) {
	// GetMessages alone returns an empty slice for a deleted session. This
	// existence check distinguishes it from a legitimate new/legacy thread.
	var found string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM sessions WHERE id = ?`, id).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSessionNotFound
	}
	if err != nil {
		return nil, err
	}
	return s.GetMessages(ctx, id)
}

func (h *hybridStore) AppendFolderTurn(ctx context.Context, id, workspaceID, agentName, revision string, event foldercontext.Event, user, answer string) ([]Message, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	messages, err := h.sqlite.AppendFolderTurn(ctx, id, workspaceID, agentName, revision, event, user, answer)
	// Invalidate even on failure: another writer/deletion may have won the CAS.
	h.cache.Remove(id)
	return messages, err
}

// AppendFolderTurn atomically stores one observation/detach and its optional
// answered turn. A losing tab writes nothing, including no partial user turn.
func (s *SQLiteStore) AppendFolderTurn(ctx context.Context, id, workspaceID, agentName, revision string, event foldercontext.Event, user, answer string) ([]Message, error) {
	if err := event.Validate(); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(event)
	if err != nil || len(encoded) > foldercontext.MaxBytes+256 {
		return nil, foldercontext.ErrInvalid
	}
	now := time.Now().UTC()
	messages := []Message{{ID: uuid.NewString(), SessionID: id, Role: RoleSystem, CreatedAt: now, FolderContext: &event}}
	for _, text := range []struct {
		role    MessageRole
		content string
	}{{RoleUser, user}, {RoleAssistant, answer}} {
		if text.content != "" {
			messages = append(messages, Message{ID: uuid.NewString(), SessionID: id, Role: text.role, Content: text.content, CreatedAt: now})
		}
	}
	err = s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		var actualWorkspace sql.NullString
		var actualAgent string
		if err := tx.QueryRowContext(ctx, `SELECT workspace_id, agent_name FROM sessions WHERE id = ?`, id).Scan(&actualWorkspace, &actualAgent); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrSessionNotFound
			}
			return err
		}
		if actualWorkspace.String != workspaceID || !strings.EqualFold(actualAgent, agentName) {
			return ErrFolderContextConflict
		}
		var current string
		err := tx.QueryRowContext(ctx, `SELECT id FROM messages WHERE session_id = ? AND folder_context_json IS NOT NULL AND continuity_source_sequence IS NULL ORDER BY rowid DESC LIMIT 1`, id).Scan(&current)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if current != revision {
			return ErrFolderContextConflict
		}
		for i, message := range messages {
			var folderJSON any
			if i == 0 {
				folderJSON = string(encoded)
			}
			_, err := tx.ExecContext(ctx, `INSERT INTO messages (id, session_id, role, content, model, tokens_used, created_at, folder_context_json) VALUES (?, ?, ?, ?, '', 0, ?, ?)`,
				message.ID, id, message.Role, message.Content, message.CreatedAt, folderJSON)
			if err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE sessions SET message_count = message_count + ?, updated_at = ? WHERE id = ?`, len(messages), now, id); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE sessions_fts SET content = content || ' ' || ? || ' ' || ? WHERE session_id = ?`, user, answer, id)
		return err
	})
	if err != nil {
		return nil, err
	}
	return messages, nil
}
