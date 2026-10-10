package session

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/johnjallday/ori-agent/internal/assistantcontext"
)

// AssistantConversationOwnerStore validates an opaque conversation reference
// against canonical ownership without loading its private message bodies. Route
// needs identity, not a transcript or historical action authority.
type AssistantConversationOwnerStore interface {
	ReadAssistantConversationOwner(context.Context, string, assistantcontext.SaveOwner) (*Session, error)
}

func (h *hybridStore) ReadAssistantConversationOwner(ctx context.Context, id string, owner assistantcontext.SaveOwner) (*Session, error) {
	return h.sqlite.ReadAssistantConversationOwner(ctx, id, owner)
}

func (s *SQLiteStore) ReadAssistantConversationOwner(ctx context.Context, id string, owner assistantcontext.SaveOwner) (*Session, error) {
	var result Session
	err := s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		var workspaceID sql.NullString
		if err := tx.QueryRowContext(ctx, `SELECT id,workspace_id,agent_name FROM sessions WHERE id=?`, id).Scan(&result.ID, &workspaceID, &result.AgentName); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrSessionNotFound
			}
			return err
		}
		result.FolderID = workspaceID.String
		if owner.UserID == "" || owner.WorkspaceID == "" || owner.AgentName == "" ||
			result.FolderID != owner.WorkspaceID || !strings.EqualFold(result.AgentName, owner.AgentName) {
			return ErrFolderContextConflict
		}
		return validateAssistantSaveOwner(ctx, tx, owner)
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}
