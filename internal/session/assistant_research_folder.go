package session

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/johnjallday/ori-agent/internal/assistantcontext"
)

// AssistantResearchFolder is canonical reference/focus metadata, not contents,
// a path, a transcript, or live filesystem permission.
type AssistantResearchFolder = assistantcontext.ResearchFolderRef
type AssistantResearchFolderStore interface {
	ReadAssistantResearchFolder(context.Context, string, assistantcontext.SaveOwner) (AssistantResearchFolder, error)
}

func (h *hybridStore) ReadAssistantResearchFolder(ctx context.Context, id string, owner assistantcontext.SaveOwner) (AssistantResearchFolder, error) {
	return h.sqlite.ReadAssistantResearchFolder(ctx, id, owner)
}
func (s *SQLiteStore) ReadAssistantResearchFolder(ctx context.Context, id string, owner assistantcontext.SaveOwner) (AssistantResearchFolder, error) {
	var result AssistantResearchFolder
	err := s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		var ws sql.NullString
		var agent string
		if err := tx.QueryRowContext(ctx, `SELECT workspace_id,agent_name FROM sessions WHERE id=?`, id).Scan(&ws, &agent); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrSessionNotFound
			}
			return err
		}
		if owner.UserID == "" || owner.WorkspaceID != ws.String || !strings.EqualFold(owner.AgentName, agent) {
			return ErrFolderContextConflict
		}
		if err := validateAssistantSaveOwner(ctx, tx, owner); err != nil {
			return err
		}
		// Shared bounded typed-event owner; malformed latest rows fail closed.
		message, err := readContextFolderEvent(ctx, tx, id, "", "")
		if err != nil {
			return err
		}
		if message == nil {
			return nil
		}
		result.Revision = message.ID
		if event := message.FolderContext; event.Observation != nil {
			result.SelectionID = event.Observation.ID
			result.FocusIDs = append([]string(nil), event.FocusIDs...)
		}
		return nil
	})
	return result, err
}
