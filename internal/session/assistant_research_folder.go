package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/johnjallday/ori-agent/internal/assistantcontext"
	"github.com/johnjallday/ori-agent/internal/foldercontext"
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
		var raw string
		// Inspect at most one bounded typed event. A malformed/oversized latest row
		// is a refusal, never permission to fall back to an older event.
		err := tx.QueryRowContext(ctx, `SELECT id,CASE WHEN length(CAST(folder_context_json AS BLOB))<=? THEN folder_context_json ELSE '' END FROM messages WHERE session_id=? AND role='system' AND folder_context_json IS NOT NULL AND continuity_source_sequence IS NULL ORDER BY rowid DESC LIMIT 1`, foldercontext.MaxBytes+256, id).Scan(&result.Revision, &raw)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		var event foldercontext.Event
		if raw == "" || json.Unmarshal([]byte(raw), &event) != nil || event.Validate() != nil {
			return ErrFolderContextConflict
		}
		if event.Observation != nil {
			result.SelectionID = event.Observation.ID
			result.FocusIDs = append([]string(nil), event.FocusIDs...)
		}
		return nil
	})
	return result, err
}
