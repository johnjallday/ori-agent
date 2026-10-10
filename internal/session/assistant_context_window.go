package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"slices"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/internal/assistantcontext"
	"github.com/johnjallday/ori-agent/internal/foldercontext"
)

// One shared bounded canonical reader serves the smaller provider window and
// the larger display window. Its quotas are compiled host policy, not inputs.
func readContextWindow(ctx context.Context, tx *sql.Tx, id string, high int64, count, perMessage, runeLimit, metadataLimit int) ([]Message, int64, bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT rowid,id,role,substr(content,1,?),length(content)>?,created_at,continuity_source_sequence IS NOT NULL,folder_context_json,turn_context_json FROM messages WHERE session_id=? AND (role IN ('user','assistant') OR folder_context_json IS NOT NULL) ORDER BY rowid DESC LIMIT ?`, perMessage, perMessage, id, count*3)
	if err != nil {
		return nil, 0, false, err
	}
	defer func() { _ = rows.Close() }()
	var messages []Message
	low := high + 1
	exact, recentRunes, clipped := 0, 0, false
	for rows.Next() {
		var message Message
		var folder, turn sql.NullString
		var row int64
		if err := rows.Scan(&row, &message.ID, &message.Role, &message.Content, &message.ContentTruncated, &message.CreatedAt, &message.Imported, &folder, &turn); err != nil {
			return nil, 0, false, err
		}
		if message.Role != RoleSystem {
			exact++
			size := utf8.RuneCountInString(message.Content)
			if exact > count || (exact > 1 && recentRunes+size > runeLimit) {
				break
			}
			recentRunes += size
			clipped = clipped || message.ContentTruncated
		}
		low = row
		if len(turn.String) <= metadataLimit {
			message.WorkspaceContext = assistantcontext.DecodeAttribution(turn.String)
			metadataLimit -= len(turn.String)
		}
		if folder.Valid && !message.Imported && len(folder.String) <= metadataLimit {
			var event foldercontext.Event
			if len(folder.String) > foldercontext.MaxBytes+256 || json.Unmarshal([]byte(folder.String), &event) != nil || event.Validate() != nil {
				return nil, 0, false, foldercontext.ErrInvalid
			}
			message.FolderContext = &event
			metadataLimit -= len(folder.String)
		}
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, false, err
	}
	_ = rows.Close()
	slices.Reverse(messages)
	latest, err := readContextFolderEvent(ctx, tx, id, "", "")
	if err != nil {
		return nil, 0, false, err
	}
	if latest != nil {
		found := false
		for i, message := range messages {
			if message.ID == latest.ID {
				messages[i] = *latest
				found = true
				break
			}
		}
		if !found {
			messages = append([]Message{*latest}, messages...)
		}
	}
	return messages, low, clipped, nil
}

type AssistantConversationDisplayStore interface {
	ReadAssistantConversationDisplay(context.Context, string, assistantcontext.SaveOwner) (*Session, bool, error)
}

func (h *hybridStore) ReadAssistantConversationDisplay(ctx context.Context, id string, owner assistantcontext.SaveOwner) (*Session, bool, error) {
	return h.sqlite.ReadAssistantConversationDisplay(ctx, id, owner)
}
func (s *SQLiteStore) ReadAssistantConversationDisplay(ctx context.Context, id string, owner assistantcontext.SaveOwner) (*Session, bool, error) {
	var sess Session
	var omitted bool
	err := s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		var err error
		sess, err = readContextOwner(ctx, tx, id, owner)
		if err != nil {
			return err
		}
		var high, low int64
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(rowid),0) FROM messages WHERE session_id=?`, id).Scan(&high); err != nil {
			return err
		}
		var clipped bool
		sess.Messages, low, clipped, err = readContextWindow(ctx, tx, id, high, 200, 100001, 240000, 8000000)
		if err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM messages WHERE session_id=? AND rowid<? AND role IN ('user','assistant'))`, id, low).Scan(&omitted); err != nil {
			return err
		}
		omitted = omitted || clipped
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return &sess, omitted, nil
}
