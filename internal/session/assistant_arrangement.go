package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/assistantcontext"
	"github.com/johnjallday/ori-agent/internal/foldercontext"
	"github.com/johnjallday/ori-agent/internal/workspacearrangement"
)

// AssistantArrangementStore is host-only, not a general message/import writer.
// No approval or execution API exists here. Revision updates will require a
// separate owning-context CAS; an insert cannot replace an existing record.
type AssistantArrangementStore interface {
	AppendArrangementTurn(context.Context, string, assistantcontext.SaveOwner, *foldercontext.Event, string, string, string, *assistantcontext.Attribution, workspacearrangement.Record) ([]Message, error)
	ReadAssistantArrangement(context.Context, string, assistantcontext.SaveOwner, string) (*AssistantArrangement, error)
	ListAssistantArrangementRefs(context.Context, string, assistantcontext.SaveOwner) ([]AssistantArrangementRef, error)
}

// AssistantArrangementCleanup discards only the unchanged, unanswered session
// created by a failed first turn. A committed or concurrently edited session is
// never rollback material, even when the request lost its commit response.
type AssistantArrangementCleanup interface {
	DiscardUnansweredArrangementConversation(context.Context, string, assistantcontext.SaveOwner) error
}

func (h *hybridStore) DiscardUnansweredArrangementConversation(ctx context.Context, id string, owner assistantcontext.SaveOwner) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// Cleanup is best effort; never wait behind another store operation after
	// a cancelled turn, or compete with a request using this new conversation.
	if !h.mu.TryLock() {
		return ErrFolderContextConflict
	}
	defer h.mu.Unlock()
	err := h.sqlite.DiscardUnansweredArrangementConversation(ctx, id, owner)
	h.cache.Remove(id)
	return err
}

func (s *SQLiteStore) DiscardUnansweredArrangementConversation(ctx context.Context, id string, owner assistantcontext.SaveOwner) error {
	if owner.ExpectedConversationRevision == "" {
		return ErrFolderContextConflict
	}
	return s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		revision, err := arrangementContextRevision(ctx, tx, id, owner)
		if err != nil {
			return err
		}
		if revision != owner.ExpectedConversationRevision {
			return ErrFolderContextConflict
		}
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages WHERE session_id=?`, id).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			return ErrFolderContextConflict
		}
		result, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE id=?`, id)
		if err != nil {
			return err
		}
		deleted, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if deleted != 1 {
			return ErrFolderContextConflict
		}
		return nil
	})
}

type AssistantArrangement struct {
	Record                                                     workspacearrangement.Record
	ConversationID, MessageID, ContextRevision, FolderRevision string
	StateVersion                                               int64
	// ContextCurrent compares canonical context pins only. Even when true it
	// grants no live source authority: restart, expiry and provider/skill loss
	// still require the host's held-evidence and current-binding checks.
	ContextCurrent bool
}

func (h *hybridStore) AppendArrangementTurn(ctx context.Context, id string, owner assistantcontext.SaveOwner, event *foldercontext.Event, revision, user, answer string, attribution *assistantcontext.Attribution, record workspacearrangement.Record) ([]Message, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	messages, err := h.sqlite.AppendArrangementTurn(ctx, id, owner, event, revision, user, answer, attribution, record)
	h.cache.Remove(id)
	return messages, err
}

func (s *SQLiteStore) AppendArrangementTurn(ctx context.Context, id string, owner assistantcontext.SaveOwner, event *foldercontext.Event, revision, user, answer string, attribution *assistantcontext.Attribution, record workspacearrangement.Record) ([]Message, error) {
	return s.appendAttributedTurn(ctx, id, owner, event, revision, user, answer, attribution, &record)
}

func validateNewArrangement(owner assistantcontext.SaveOwner, event *foldercontext.Event, user, answer string, record workspacearrangement.Record) error {
	if owner.UserID == "" || owner.WorkspaceID == "" || owner.AgentName == "" || owner.StateVersion < 1 || owner.ExpectedConversationRevision == "" ||
		strings.TrimSpace(user) == "" || strings.TrimSpace(answer) == "" || event == nil || event.Observation == nil ||
		record.SourceKind != workspacearrangement.Attachment || record.SourceID != event.Observation.ID || record.Revision != 1 ||
		(record.State != "draft" && record.State != "needs_more_evidence") {
		return workspacearrangement.ErrInvalid
	}
	return record.Validate()
}

func insertArrangement(ctx context.Context, tx *sql.Tx, id string, owner assistantcontext.SaveOwner, record workspacearrangement.Record, messages []Message, contextRevision string) error {
	encoded, err := json.Marshal(record)
	if err != nil || len(encoded) > workspacearrangement.MaxRecordBytes {
		return workspacearrangement.ErrLimit
	}
	var count, bytes int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(length(CAST(record_json AS BLOB))),0) FROM assistant_arrangements WHERE session_id=?`, id).Scan(&count, &bytes); err != nil {
		return err
	}
	if count >= workspacearrangement.MaxContextRecords || len(encoded) > workspacearrangement.MaxContextRecordBytes-bytes {
		return workspacearrangement.ErrLimit
	}
	var answerID, folderID string
	for _, message := range messages {
		if message.Role == RoleAssistant {
			answerID = message.ID
		}
		if message.FolderContext != nil {
			folderID = message.ID
		}
	}
	if answerID == "" || folderID == "" {
		return workspacearrangement.ErrInvalid
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO assistant_arrangements(id,session_id,message_id,user_id,workspace_id,agent_name,state_version,context_revision,folder_revision,revision,state,record_json) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		record.ID, id, answerID, owner.UserID, owner.WorkspaceID, owner.AgentName, owner.StateVersion, contextRevision, folderID, record.Revision, record.State, string(encoded))
	return err
}

func (h *hybridStore) ReadAssistantArrangement(ctx context.Context, id string, owner assistantcontext.SaveOwner, recordID string) (*AssistantArrangement, error) {
	return h.sqlite.ReadAssistantArrangement(ctx, id, owner, recordID)
}

func (s *SQLiteStore) ReadAssistantArrangement(ctx context.Context, id string, owner assistantcontext.SaveOwner, recordID string) (*AssistantArrangement, error) {
	if owner.UserID == "" || owner.WorkspaceID == "" || owner.AgentName == "" {
		return nil, workspacearrangement.ErrUnavailable
	}
	var result AssistantArrangement
	err := s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		currentRevision, err := arrangementContextRevision(ctx, tx, id, owner)
		if err != nil {
			return err
		}
		var raw string
		var imported bool
		err = tx.QueryRowContext(ctx, `SELECT a.record_json,a.message_id,a.context_revision,a.folder_revision,a.state_version,m.continuity_source_sequence IS NOT NULL
			FROM assistant_arrangements a JOIN messages m ON m.id=a.message_id AND m.session_id=a.session_id
			WHERE a.id=? AND a.session_id=? AND a.user_id=? AND a.workspace_id=? AND a.agent_name=?`, recordID, id, owner.UserID, owner.WorkspaceID, owner.AgentName).
			Scan(&raw, &result.MessageID, &result.ContextRevision, &result.FolderRevision, &result.StateVersion, &imported)
		if errors.Is(err, sql.ErrNoRows) {
			return workspacearrangement.ErrUnavailable
		}
		if err != nil {
			return err
		}
		result.Record, err = workspacearrangement.DecodeRecord([]byte(raw))
		if err != nil || result.Record.ID != recordID {
			return workspacearrangement.ErrInvalid
		}
		result.ConversationID = id
		result.ContextCurrent = !imported && result.ContextRevision == currentRevision && result.StateVersion == owner.StateVersion
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func arrangementContextRevision(ctx context.Context, tx *sql.Tx, id string, owner assistantcontext.SaveOwner) (string, error) {
	if owner.UserID == "" || owner.WorkspaceID == "" || owner.AgentName == "" {
		return "", workspacearrangement.ErrUnavailable
	}
	if err := validateAssistantSaveOwner(ctx, tx, owner); err != nil {
		return "", err
	}
	var workspaceID sql.NullString
	var agent string
	var count int
	var updated time.Time
	var epoch int64
	if err := tx.QueryRowContext(ctx, `SELECT workspace_id,agent_name,message_count,updated_at,assistant_context_epoch FROM sessions WHERE id=?`, id).Scan(&workspaceID, &agent, &count, &updated, &epoch); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrSessionNotFound
		}
		return "", err
	}
	if workspaceID.String != owner.WorkspaceID || !strings.EqualFold(agent, owner.AgentName) {
		return "", ErrFolderContextConflict
	}
	return assistantcontext.ConversationRevision(updated, count, epoch), nil
}

// References hydrate a conversation without sending every draft's potentially
// large membership to the browser. They are not embedded in portable messages.
type AssistantArrangementRef struct {
	ID             string `json:"id"`
	MessageID      string `json:"message_id"`
	Revision       int64  `json:"revision"`
	State          string `json:"state"`
	ContextCurrent bool   `json:"context_current"`
}

func (h *hybridStore) ListAssistantArrangementRefs(ctx context.Context, id string, owner assistantcontext.SaveOwner) ([]AssistantArrangementRef, error) {
	return h.sqlite.ListAssistantArrangementRefs(ctx, id, owner)
}

func (s *SQLiteStore) ListAssistantArrangementRefs(ctx context.Context, id string, owner assistantcontext.SaveOwner) ([]AssistantArrangementRef, error) {
	out := []AssistantArrangementRef{}
	err := s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		current, err := arrangementContextRevision(ctx, tx, id, owner)
		if err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT a.id,a.message_id,a.revision,a.state,a.context_revision,a.state_version,m.continuity_source_sequence IS NOT NULL
			FROM assistant_arrangements a JOIN messages m ON m.id=a.message_id AND m.session_id=a.session_id
			WHERE a.session_id=? AND a.user_id=? AND a.workspace_id=? AND a.agent_name=? ORDER BY m.rowid LIMIT ?`, id, owner.UserID, owner.WorkspaceID, owner.AgentName, workspacearrangement.MaxContextRecords+1)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var ref AssistantArrangementRef
			var revision string
			var version int64
			var imported bool
			if err := rows.Scan(&ref.ID, &ref.MessageID, &ref.Revision, &ref.State, &revision, &version, &imported); err != nil {
				return err
			}
			ref.ContextCurrent = !imported && revision == current && version == owner.StateVersion
			out = append(out, ref)
			if len(out) > workspacearrangement.MaxContextRecords {
				return workspacearrangement.ErrLimit
			}
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
