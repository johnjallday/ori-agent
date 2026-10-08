package session

import (
	"context"
	"database/sql"
	"errors"

	"github.com/johnjallday/ori-agent/internal/assistantcontext"
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
	// GetMessages alone returns an empty slice for a deleted session. Distinguish
	// it from a legitimate new/legacy thread.
	var found string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM sessions WHERE id=?`, id).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSessionNotFound
	}
	if err != nil {
		return nil, err
	}
	return s.GetMessages(ctx, id)
}

func (h *hybridStore) AppendFolderTurn(ctx context.Context, id, workspaceID, agentName, revision string, event foldercontext.Event, user, answer string) ([]Message, error) {
	return h.AppendAttributedTurn(ctx, id, assistantcontext.SaveOwner{WorkspaceID: workspaceID, AgentName: agentName}, &event, revision, user, answer, nil)
}

// AppendFolderTurn atomically stores one observation/detach and its optional
// answered turn. A losing tab writes nothing, including no partial user turn.
func (s *SQLiteStore) AppendFolderTurn(ctx context.Context, id, workspaceID, agentName, revision string, event foldercontext.Event, user, answer string) ([]Message, error) {
	return s.AppendAttributedTurn(ctx, id, assistantcontext.SaveOwner{WorkspaceID: workspaceID, AgentName: agentName}, &event, revision, user, answer, nil)
}
