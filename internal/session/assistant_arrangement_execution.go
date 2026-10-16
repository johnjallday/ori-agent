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
	"github.com/johnjallday/ori-agent/internal/workspacearrangement"
)

// ErrArrangementEffectConflict means an arranged group's review, run or record
// changed since the caller read it. Nothing was written.
var ErrArrangementEffectConflict = errors.New("arranged group review changed; reopen the conversation")

// States of one arranged group's existing folder setup review, as correlated
// from that review's own canonical offer. They are never model claims.
const (
	ArrangementEffectAwaitingReview = "awaiting_user_review"
	ArrangementEffectApplying       = "applying"
	ArrangementEffectPartial        = "partially_applied"
	ArrangementEffectCompleted      = "completed"
	ArrangementEffectClosed         = "closed"
)

// AssistantArrangementEffect correlates one create_group intention with the
// reviewed folder setup carrying it out. It holds canonical IDs and counts
// only: no path, folder name or membership copy.
type AssistantArrangementEffect struct {
	Operation      int
	RecordRevision int64
	OfferID        string
	MessageID      string
	State          string
	WorkspaceID    string
	Route          string
	Workspaces     int
	ReceiptDigest  string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// AssistantArrangementReview is one explicit review of an arranged group. The
// folder event names the record's own observation and the review's offer; it
// is appended under the conversation's folder-revision CAS in the same
// transaction that binds the offer to the record, its revision and operation.
type AssistantArrangementReview struct {
	RecordID       string
	RecordRevision int64
	Operation      int
	FolderRevision string
	Event          foldercontext.Event
}

// AssistantArrangementExecutionStore is host-only. Nothing here approves or
// runs an effect: the existing folder setup review owns confirmation and its
// receipt. These calls bind, read and reconcile that correlation.
type AssistantArrangementExecutionStore interface {
	BindAssistantArrangementReview(context.Context, string, assistantcontext.SaveOwner, AssistantArrangementReview) ([]Message, error)
	ListAssistantArrangementEffects(context.Context, string, assistantcontext.SaveOwner, string) ([]AssistantArrangementEffect, error)
	ReconcileAssistantArrangementEffect(context.Context, string, assistantcontext.SaveOwner, string, AssistantArrangementEffect, AssistantArrangementEffect) error
	DeclineAssistantArrangement(context.Context, string, assistantcontext.SaveOwner, string, int64) (workspacearrangement.Record, error)
}

func (h *hybridStore) BindAssistantArrangementReview(ctx context.Context, id string, owner assistantcontext.SaveOwner, review AssistantArrangementReview) ([]Message, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	messages, err := h.sqlite.BindAssistantArrangementReview(ctx, id, owner, review)
	h.cache.Remove(id)
	return messages, err
}

func (h *hybridStore) ListAssistantArrangementEffects(ctx context.Context, id string, owner assistantcontext.SaveOwner, recordID string) ([]AssistantArrangementEffect, error) {
	return h.sqlite.ListAssistantArrangementEffects(ctx, id, owner, recordID)
}

func (h *hybridStore) ReconcileAssistantArrangementEffect(ctx context.Context, id string, owner assistantcontext.SaveOwner, recordID string, expected, next AssistantArrangementEffect) error {
	return h.sqlite.ReconcileAssistantArrangementEffect(ctx, id, owner, recordID, expected, next)
}

func (h *hybridStore) DeclineAssistantArrangement(ctx context.Context, id string, owner assistantcontext.SaveOwner, recordID string, expected int64) (workspacearrangement.Record, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	record, err := h.sqlite.DeclineAssistantArrangement(ctx, id, owner, recordID, expected)
	h.cache.Remove(id)
	return record, err
}

// ownedArrangement reads one record inside a transaction after the owner and
// session checks. It refuses imported/foreign rows and a changed state version.
func ownedArrangement(ctx context.Context, tx *sql.Tx, id string, owner assistantcontext.SaveOwner, recordID string) (workspacearrangement.Record, error) {
	var raw string
	var version int64
	var imported bool
	err := tx.QueryRowContext(ctx, `SELECT a.record_json,a.state_version,m.continuity_source_sequence IS NOT NULL
		FROM assistant_arrangements a JOIN messages m ON m.id=a.message_id AND m.session_id=a.session_id
		WHERE a.id=? AND a.session_id=? AND a.user_id=? AND a.workspace_id=? AND a.agent_name=?`, recordID, id, owner.UserID, owner.WorkspaceID, owner.AgentName).
		Scan(&raw, &version, &imported)
	if errors.Is(err, sql.ErrNoRows) {
		return workspacearrangement.Record{}, workspacearrangement.ErrUnavailable
	}
	if err != nil {
		return workspacearrangement.Record{}, err
	}
	if imported || version != owner.StateVersion {
		return workspacearrangement.Record{}, workspacearrangement.ErrStale
	}
	record, err := workspacearrangement.DecodeRecord([]byte(raw))
	if err != nil || record.ID != recordID || record.SourceKind != workspacearrangement.Attachment {
		return workspacearrangement.Record{}, workspacearrangement.ErrInvalid
	}
	return record, nil
}

func validArrangementOwner(owner assistantcontext.SaveOwner) bool {
	return owner.UserID != "" && owner.WorkspaceID != "" && owner.AgentName != "" && owner.StateVersion >= 1
}

func (s *SQLiteStore) BindAssistantArrangementReview(ctx context.Context, id string, owner assistantcontext.SaveOwner, review AssistantArrangementReview) ([]Message, error) {
	event := review.Event
	if !validArrangementOwner(owner) || review.RecordRevision < 1 || review.Operation < 0 || review.Operation >= workspacearrangement.MaxOperations ||
		review.FolderRevision == "" || event.Observation == nil || event.OfferID == "" || len(event.OfferID) > 200 || event.Validate() != nil {
		return nil, workspacearrangement.ErrInvalid
	}
	encoded, err := json.Marshal(event)
	if err != nil || len(encoded) > foldercontext.MaxEventBytes {
		return nil, foldercontext.ErrInvalid
	}
	now := time.Now().UTC()
	message := Message{ID: uuid.NewString(), SessionID: id, Role: RoleSystem, CreatedAt: now, FolderContext: &event}
	err = s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		before, err := arrangementContextRevision(ctx, tx, id, owner)
		if err != nil {
			return err
		}
		var count int
		var epoch int64
		if err := tx.QueryRowContext(ctx, `SELECT message_count,assistant_context_epoch FROM sessions WHERE id=?`, id).Scan(&count, &epoch); err != nil {
			return err
		}
		var current string
		err = tx.QueryRowContext(ctx, `SELECT id FROM messages WHERE session_id=? AND folder_context_json IS NOT NULL AND continuity_source_sequence IS NULL ORDER BY rowid DESC LIMIT 1`, id).Scan(&current)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if current != review.FolderRevision {
			return ErrFolderContextConflict
		}
		record, err := ownedArrangement(ctx, tx, id, owner, review.RecordID)
		if err != nil {
			return err
		}
		if record.Revision != review.RecordRevision || (record.State != "draft" && record.State != "needs_more_evidence") ||
			record.SourceID != event.Observation.ID || review.Operation >= len(record.Draft.Operations) ||
			record.Draft.Operations[review.Operation].Kind != workspacearrangement.CreateGroup {
			return workspacearrangement.ErrStale
		}
		var priorOffer, priorState string
		err = tx.QueryRowContext(ctx, `SELECT offer_id,state FROM assistant_arrangement_effects WHERE arrangement_id=? AND operation=?`, review.RecordID, review.Operation).Scan(&priorOffer, &priorState)
		switch {
		case errors.Is(err, sql.ErrNoRows):
		case err != nil:
			return err
		case priorOffer == event.OfferID && priorState == ArrangementEffectAwaitingReview:
			// The same open review, opened again on a fresh canonical event.
		case priorState == ArrangementEffectClosed:
			// A closed review created nothing; a new review may replace it.
		default:
			return ErrArrangementEffectConflict
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO messages(id,session_id,role,content,model,tokens_used,created_at,folder_context_json,turn_context_json) VALUES(?,?,?,'','',0,?,?,NULL)`,
			message.ID, id, message.Role, message.CreatedAt, string(encoded)); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE sessions SET message_count=message_count+1,updated_at=? WHERE id=?`, now, id); err != nil {
			return err
		}
		// This review's own canonical event is accounted for: a record whose
		// pin was current stays current. A pin already behind an unrelated
		// change stays behind; this write never makes it current again.
		after := assistantcontext.ConversationRevision(now, count+1, epoch)
		if _, err := tx.ExecContext(ctx, `UPDATE assistant_arrangements SET context_revision=? WHERE id=? AND session_id=? AND context_revision=?`,
			after, review.RecordID, id, before); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO assistant_arrangement_effects(arrangement_id,session_id,operation,record_revision,offer_id,message_id,state,created_at,updated_at)
			VALUES(?,?,?,?,?,?,?,?,?)
			ON CONFLICT(arrangement_id,operation) DO UPDATE SET record_revision=excluded.record_revision,offer_id=excluded.offer_id,message_id=excluded.message_id,
				state=excluded.state,workspace_id='',route='',workspaces=0,receipt_digest='',updated_at=excluded.updated_at`,
			review.RecordID, id, review.Operation, review.RecordRevision, event.OfferID, message.ID, ArrangementEffectAwaitingReview, now, now)
		return err
	})
	if err != nil {
		return nil, err
	}
	return []Message{message}, nil
}

func (s *SQLiteStore) ListAssistantArrangementEffects(ctx context.Context, id string, owner assistantcontext.SaveOwner, recordID string) ([]AssistantArrangementEffect, error) {
	if owner.UserID == "" || owner.WorkspaceID == "" || owner.AgentName == "" {
		return nil, workspacearrangement.ErrUnavailable
	}
	out := []AssistantArrangementEffect{}
	err := s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		if _, err := arrangementContextRevision(ctx, tx, id, owner); err != nil {
			return err
		}
		var found int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM assistant_arrangements WHERE id=? AND session_id=? AND user_id=? AND workspace_id=? AND agent_name=?`,
			recordID, id, owner.UserID, owner.WorkspaceID, owner.AgentName).Scan(&found); err != nil {
			return err
		}
		if found != 1 {
			return workspacearrangement.ErrUnavailable
		}
		rows, err := tx.QueryContext(ctx, `SELECT operation,record_revision,offer_id,message_id,state,workspace_id,route,workspaces,receipt_digest,created_at,updated_at
			FROM assistant_arrangement_effects WHERE arrangement_id=? AND session_id=? ORDER BY operation LIMIT ?`, recordID, id, workspacearrangement.MaxOperations+1)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var effect AssistantArrangementEffect
			if err := rows.Scan(&effect.Operation, &effect.RecordRevision, &effect.OfferID, &effect.MessageID, &effect.State, &effect.WorkspaceID, &effect.Route,
				&effect.Workspaces, &effect.ReceiptDigest, &effect.CreatedAt, &effect.UpdatedAt); err != nil {
				return err
			}
			out = append(out, effect)
			if len(out) > workspacearrangement.MaxOperations {
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

// arrangementEffectMoves lists the transitions a reconciliation may record. A
// completed group or a closed review is final for that offer; a new review is
// a new binding, never a resurrected one.
var arrangementEffectMoves = map[string][]string{
	ArrangementEffectAwaitingReview: {ArrangementEffectApplying, ArrangementEffectPartial, ArrangementEffectCompleted, ArrangementEffectClosed},
	ArrangementEffectApplying:       {ArrangementEffectPartial, ArrangementEffectCompleted},
	ArrangementEffectPartial:        {ArrangementEffectApplying, ArrangementEffectCompleted},
}

// ReconcileAssistantArrangementEffect records what the bound offer's own
// canonical state shows, compare-and-swap on the exact offer and prior state.
// A completion must carry the read-back group and receipt digest.
func (s *SQLiteStore) ReconcileAssistantArrangementEffect(ctx context.Context, id string, owner assistantcontext.SaveOwner, recordID string, expected, next AssistantArrangementEffect) error {
	allowed := false
	for _, state := range arrangementEffectMoves[expected.State] {
		allowed = allowed || state == next.State
	}
	completed := next.State == ArrangementEffectCompleted
	if !allowed || expected.OfferID == "" || next.OfferID != expected.OfferID || next.Operation != expected.Operation ||
		completed != (next.WorkspaceID != "" && len(next.ReceiptDigest) == 64 && next.Workspaces > 0) ||
		next.Workspaces < 0 || next.Workspaces > 24 || len(next.WorkspaceID) > 200 || len(next.Route) > 512 ||
		(next.Route != "" && !strings.HasPrefix(next.Route, "/workspaces/")) {
		return workspacearrangement.ErrInvalid
	}
	if !completed {
		next.WorkspaceID, next.Route, next.Workspaces, next.ReceiptDigest = "", "", 0, ""
	}
	return s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		if _, err := arrangementContextRevision(ctx, tx, id, owner); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE assistant_arrangement_effects SET state=?,workspace_id=?,route=?,workspaces=?,receipt_digest=?,updated_at=?
			WHERE arrangement_id=? AND session_id=? AND operation=? AND offer_id=? AND state=?
			AND EXISTS(SELECT 1 FROM assistant_arrangements a WHERE a.id=arrangement_id AND a.user_id=? AND a.workspace_id=? AND a.agent_name=?)`,
			next.State, next.WorkspaceID, next.Route, next.Workspaces, next.ReceiptDigest, time.Now().UTC(),
			recordID, id, expected.Operation, expected.OfferID, expected.State, owner.UserID, owner.WorkspaceID, owner.AgentName)
		if err != nil {
			return err
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if changed != 1 {
			return ErrArrangementEffectConflict
		}
		return nil
	})
}

// DeclineAssistantArrangement closes a current unapplied PA proposal. It keeps
// the typed intent as cancelled history and fences every older review: the
// record's revision advances and an open bound review is marked closed in the
// same transaction. Work already confirmed, running or completed is never
// undone, so a proposal with such an effect cannot be declined.
func (s *SQLiteStore) DeclineAssistantArrangement(ctx context.Context, id string, owner assistantcontext.SaveOwner, recordID string, expected int64) (workspacearrangement.Record, error) {
	if !validArrangementOwner(owner) || expected < 1 {
		return workspacearrangement.Record{}, workspacearrangement.ErrInvalid
	}
	var declined workspacearrangement.Record
	err := s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		if _, err := arrangementContextRevision(ctx, tx, id, owner); err != nil {
			return err
		}
		record, err := ownedArrangement(ctx, tx, id, owner, recordID)
		if err != nil {
			return err
		}
		if record.Revision != expected || (record.State != "draft" && record.State != "needs_more_evidence") {
			return workspacearrangement.ErrStale
		}
		var active int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM assistant_arrangement_effects WHERE arrangement_id=? AND state IN (?,?,?)`,
			recordID, ArrangementEffectApplying, ArrangementEffectPartial, ArrangementEffectCompleted).Scan(&active); err != nil {
			return err
		}
		if active != 0 {
			return ErrArrangementEffectConflict
		}
		now := time.Now().UTC()
		record.State, record.Revision = "cancelled", record.Revision+1
		if now.After(record.UpdatedAt) {
			record.UpdatedAt = now
		}
		if err := record.Validate(); err != nil {
			return err
		}
		encoded, err := json.Marshal(record)
		if err != nil || len(encoded) > workspacearrangement.MaxRecordBytes {
			return workspacearrangement.ErrLimit
		}
		result, err := tx.ExecContext(ctx, `UPDATE assistant_arrangements SET revision=?,state=?,record_json=? WHERE id=? AND session_id=? AND revision=?`,
			record.Revision, record.State, string(encoded), recordID, id, expected)
		if err != nil {
			return err
		}
		if changed, err := result.RowsAffected(); err != nil || changed != 1 {
			return ErrArrangementEffectConflict
		}
		if _, err := tx.ExecContext(ctx, `UPDATE assistant_arrangement_effects SET state=?,updated_at=? WHERE arrangement_id=? AND state=?`,
			ArrangementEffectClosed, now, recordID, ArrangementEffectAwaitingReview); err != nil {
			return err
		}
		declined = record
		return nil
	})
	if err != nil {
		return workspacearrangement.Record{}, err
	}
	return declined, nil
}
