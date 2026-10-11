package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/internal/assistantcontext"
)

// AssistantConversationContextStore bypasses full-session/cache convenience
// reads. Every body read and derived write is guarded in the same transaction.
// No method accepts a model-selected conversation or workspace.
type AssistantConversationContextStore interface {
	ReadAssistantConversationContext(context.Context, string, assistantcontext.SaveOwner) (*AssistantConversationContext, error)
	SaveAssistantConversationRecap(context.Context, string, assistantcontext.SaveOwner, assistantcontext.ContextPin, int64, *assistantcontext.ConversationRecap) error
}

type AssistantConversationContext struct {
	Session        Session
	Recent         []Message
	Pin            assistantcontext.ContextPin
	Recap          *assistantcontext.ConversationRecap
	Batch          []assistantcontext.RecapSource
	NextThrough    int64
	Older          []assistantcontext.RecapSource
	HasOlder       bool
	StaleDiscarded bool
}

func (h *hybridStore) ReadAssistantConversationContext(ctx context.Context, id string, owner assistantcontext.SaveOwner) (*AssistantConversationContext, error) {
	return h.sqlite.ReadAssistantConversationContext(ctx, id, owner)
}
func (h *hybridStore) SaveAssistantConversationRecap(ctx context.Context, id string, owner assistantcontext.SaveOwner, pin assistantcontext.ContextPin, through int64, recap *assistantcontext.ConversationRecap) error {
	return h.sqlite.SaveAssistantConversationRecap(ctx, id, owner, pin, through, recap)
}

func readContextOwner(ctx context.Context, tx *sql.Tx, id string, owner assistantcontext.SaveOwner) (Session, error) {
	var sess Session
	var workspace sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT id,substr(title,1,60),workspace_id,agent_name,message_count,updated_at,assistant_context_epoch FROM sessions WHERE id=?`, id).
		Scan(&sess.ID, &sess.Title, &workspace, &sess.AgentName, &sess.MessageCount, &sess.UpdatedAt, &sess.ContextEpoch)
	if errors.Is(err, sql.ErrNoRows) {
		return sess, ErrSessionNotFound
	}
	if err != nil {
		return sess, err
	}
	sess.FolderID = workspace.String
	if owner.UserID == "" || owner.WorkspaceID == "" || owner.AgentName == "" || sess.FolderID != owner.WorkspaceID || !strings.EqualFold(sess.AgentName, owner.AgentName) {
		return sess, ErrFolderContextConflict
	}
	if owner.ExpectedConversationRevision != "" && owner.ExpectedConversationRevision != assistantcontext.ConversationRevision(sess.UpdatedAt, sess.MessageCount, sess.ContextEpoch) {
		return sess, ErrFolderContextConflict
	}
	return sess, validateAssistantSaveOwner(ctx, tx, owner)
}

func readContextCheckpoint(ctx context.Context, tx *sql.Tx, id string, owner assistantcontext.SaveOwner, epoch int64) (*assistantcontext.ConversationRecap, int64, int64, bool, error) {
	var user, hq, profile, data string
	var version, storedEpoch, through, generation int64
	err := tx.QueryRowContext(ctx, `SELECT owner_user_id,hq_workspace_id,profile_name,state_version,source_epoch,through_rowid,generation,recap_json FROM assistant_conversation_checkpoints WHERE session_id=?`, id).
		Scan(&user, &hq, &profile, &version, &storedEpoch, &through, &generation, &data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, 0, 0, false, nil
	}
	if err != nil {
		return nil, 0, 0, false, err
	}
	recap, decodeErr := assistantcontext.DecodeRecap(data)
	if decodeErr != nil || user != owner.UserID || hq != owner.WorkspaceID || profile != owner.AgentName || version != owner.StateVersion || storedEpoch != epoch {
		_, err = tx.ExecContext(ctx, `DELETE FROM assistant_conversation_checkpoints WHERE session_id=?`, id)
		return nil, 0, 0, true, err
	}
	return recap, through, generation, false, nil
}

func readContextSources(ctx context.Context, tx *sql.Tx, id string, after, before int64, limit int, newest bool) ([]assistantcontext.RecapSource, int64, error) {
	order := "ASC"
	if newest {
		order = "DESC"
	}
	// #nosec G202 -- order is ASC/DESC host constants; bounds and identity are parameters.
	rows, err := tx.QueryContext(ctx, `SELECT rowid,id,role,substr(content,1,1000),continuity_source_sequence IS NOT NULL FROM messages WHERE session_id=? AND rowid>? AND rowid<? AND role IN ('user','assistant') ORDER BY rowid `+order+` LIMIT ?`, id, after, before, limit)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	var sources []assistantcontext.RecapSource
	var through int64
	budget := assistantcontext.ContextBatchRunes
	if newest {
		budget = assistantcontext.ContextOlderMessages * assistantcontext.ContextBatchMessageRunes
	}
	for rows.Next() {
		var source assistantcontext.RecapSource
		var row int64
		if err := rows.Scan(&row, &source.ID, &source.Role, &source.Content, &source.Imported); err != nil {
			return nil, 0, err
		}
		size := utf8.RuneCountInString(source.Content)
		if size > budget {
			break
		}
		budget -= size
		sources = append(sources, source)
		through = row
	}
	return sources, through, rows.Err()
}

func (s *SQLiteStore) ReadAssistantConversationContext(ctx context.Context, id string, owner assistantcontext.SaveOwner) (*AssistantConversationContext, error) {
	result := &AssistantConversationContext{}
	err := s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		var err error
		result.Session, err = readContextOwner(ctx, tx, id, owner)
		if err != nil {
			return err
		}
		result.Pin.Revision = assistantcontext.ConversationRevision(result.Session.UpdatedAt, result.Session.MessageCount, result.Session.ContextEpoch)
		result.Pin.Epoch = result.Session.ContextEpoch
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(rowid),0) FROM messages WHERE session_id=?`, id).Scan(&result.Pin.HighWater); err != nil {
			return err
		}
		result.Recap, result.Pin.Through, result.Pin.Generation, result.StaleDiscarded, err = readContextCheckpoint(ctx, tx, id, owner, result.Pin.Epoch)
		if err != nil {
			return err
		}
		var low int64
		result.Recent, low, _, err = readContextWindow(ctx, tx, id, result.Pin.HighWater, assistantcontext.ContextRecentMessages, assistantcontext.ContextMessageRunes+1, assistantcontext.ContextRecentRunes, 64000)
		if err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM messages WHERE session_id=? AND rowid<? AND role IN ('user','assistant'))`, id, low).Scan(&result.HasOlder); err != nil {
			return err
		}
		if !result.HasOlder {
			return nil
		}
		result.Batch, result.NextThrough, err = readContextSources(ctx, tx, id, result.Pin.Through, low, assistantcontext.ContextBatchMessages, false)
		if err != nil {
			return err
		}
		result.Older, _, err = readContextSources(ctx, tx, id, 0, low, assistantcontext.ContextOlderMessages, true)
		return err
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (s *SQLiteStore) SaveAssistantConversationRecap(ctx context.Context, id string, owner assistantcontext.SaveOwner, pin assistantcontext.ContextPin, through int64, recap *assistantcontext.ConversationRecap) error {
	if pin.Revision == "" || through <= pin.Through || through > pin.HighWater {
		return assistantcontext.ErrContinuity
	}
	return s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		sess, err := readContextOwner(ctx, tx, id, owner)
		if err != nil {
			return err
		}
		if pin.Epoch != sess.ContextEpoch || pin.Revision != assistantcontext.ConversationRevision(sess.UpdatedAt, sess.MessageCount, sess.ContextEpoch) {
			return ErrFolderContextConflict
		}
		var high int64
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(rowid),0) FROM messages WHERE session_id=?`, id).Scan(&high); err != nil {
			return err
		}
		prior, oldThrough, generation, stale, err := readContextCheckpoint(ctx, tx, id, owner, sess.ContextEpoch)
		if err != nil {
			return err
		}
		if stale || high != pin.HighWater || oldThrough != pin.Through || generation != pin.Generation {
			return ErrFolderContextConflict
		}
		sources, actualThrough, err := readContextSources(ctx, tx, id, oldThrough, through+1, assistantcontext.ContextBatchMessages, false)
		if err != nil {
			return err
		}
		if actualThrough != through {
			return assistantcontext.ErrContinuity
		}
		if err := assistantcontext.ValidateRecap(recap, sources, prior); err != nil {
			return err
		}
		data, err := json.Marshal(recap)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO assistant_conversation_checkpoints(session_id,owner_user_id,hq_workspace_id,profile_name,state_version,source_epoch,through_rowid,generation,recap_json,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(session_id) DO UPDATE SET owner_user_id=excluded.owner_user_id,hq_workspace_id=excluded.hq_workspace_id,profile_name=excluded.profile_name,state_version=excluded.state_version,source_epoch=excluded.source_epoch,through_rowid=excluded.through_rowid,generation=excluded.generation,recap_json=excluded.recap_json,updated_at=excluded.updated_at`, id, owner.UserID, owner.WorkspaceID, owner.AgentName, owner.StateVersion, pin.Epoch, through, generation+1, string(data), time.Now().UTC())
		return err
	})
}
