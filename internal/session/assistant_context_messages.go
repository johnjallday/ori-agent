package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/johnjallday/ori-agent/internal/assistantcontext"
	"github.com/johnjallday/ori-agent/internal/foldercontext"
)

// Exact message and folder-event reads preserve reviewed draft/setup owners
// without materializing the transcript. Inputs are host-owned current scope,
// or exact IDs already named by an existing user review, never model authority.
type AssistantContextMessageStore interface {
	ReadAssistantContextMessage(context.Context, string, assistantcontext.SaveOwner, string) (*Session, *Message, error)
	ReadAssistantContextFolderEvent(context.Context, string, assistantcontext.SaveOwner, string, string) (*Session, *Message, error)
}

func (h *hybridStore) ReadAssistantContextMessage(ctx context.Context, id string, owner assistantcontext.SaveOwner, messageID string) (*Session, *Message, error) {
	return h.sqlite.ReadAssistantContextMessage(ctx, id, owner, messageID)
}
func (h *hybridStore) ReadAssistantContextFolderEvent(ctx context.Context, id string, owner assistantcontext.SaveOwner, offerID, observationID string) (*Session, *Message, error) {
	return h.sqlite.ReadAssistantContextFolderEvent(ctx, id, owner, offerID, observationID)
}

func readContextFolderEvent(ctx context.Context, tx *sql.Tx, id, offerID, observationID string) (*Message, error) {
	if len(offerID) > 128 || len(observationID) > 128 {
		return nil, foldercontext.ErrInvalid
	}
	var msg Message
	var raw string
	err := tx.QueryRowContext(ctx, `SELECT id,created_at,CASE WHEN length(CAST(folder_context_json AS BLOB))<=? THEN folder_context_json ELSE '' END FROM messages WHERE session_id=? AND role='system' AND folder_context_json IS NOT NULL AND continuity_source_sequence IS NULL AND (?='' OR json_extract(folder_context_json,'$.offer_id')=?) AND (?='' OR json_extract(folder_context_json,'$.observation.id')=?) ORDER BY rowid DESC LIMIT 1`, foldercontext.MaxEventBytes, id, offerID, offerID, observationID, observationID).Scan(&msg.ID, &msg.CreatedAt, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var event foldercontext.Event
	if raw == "" || json.Unmarshal([]byte(raw), &event) != nil || event.Validate() != nil {
		return nil, ErrFolderContextConflict
	}
	msg.Role, msg.SessionID, msg.FolderContext = RoleSystem, id, &event
	return &msg, nil
}

func (s *SQLiteStore) ReadAssistantContextFolderEvent(ctx context.Context, id string, owner assistantcontext.SaveOwner, offerID, observationID string) (*Session, *Message, error) {
	var sess Session
	var msg *Message
	err := s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		var err error
		sess, err = readContextOwner(ctx, tx, id, owner)
		if err != nil {
			return err
		}
		msg, err = readContextFolderEvent(ctx, tx, id, offerID, observationID)
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	return &sess, msg, nil
}

func (s *SQLiteStore) ReadAssistantContextMessage(ctx context.Context, id string, owner assistantcontext.SaveOwner, messageID string) (*Session, *Message, error) {
	if messageID == "" || len(messageID) > 128 {
		return nil, nil, assistantcontext.ErrContinuity
	}
	var sess Session
	var msg Message
	err := s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		var err error
		sess, err = readContextOwner(ctx, tx, id, owner)
		if err != nil {
			return err
		}
		// One exact canonical reply, capped above the existing Ticket body
		// limit. Oversized legacy replies refuse rather than silently cutting.
		var body sql.NullString
		var turn sql.NullString
		err = tx.QueryRowContext(ctx, `SELECT id,role,CASE WHEN length(content)<=100001 THEN content ELSE NULL END,created_at,continuity_source_sequence IS NOT NULL,turn_context_json FROM messages WHERE session_id=? AND id=? AND role IN ('user','assistant')`, id, messageID).Scan(&msg.ID, &msg.Role, &body, &msg.CreatedAt, &msg.Imported, &turn)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if !body.Valid {
			return assistantcontext.ErrContinuity
		}
		msg.SessionID, msg.Content, msg.WorkspaceContext = id, body.String, assistantcontext.DecodeAttribution(turn.String)
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	if msg.ID == "" {
		return &sess, nil, nil
	}
	return &sess, &msg, nil
}
