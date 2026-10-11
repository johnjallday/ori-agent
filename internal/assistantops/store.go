package assistantops

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
)

// Store persists operations. Every change is a compare-and-swap on revision,
// so two requests for one operation cannot both win.
type Store interface {
	Insert(ctx context.Context, op *Operation) error
	Get(ctx context.Context, id string) (*Operation, error)
	// Update writes op when its stored revision is still expected, bumps the
	// revision and returns the stored record. A lost race is ErrConflict.
	Update(ctx context.Context, op *Operation, expected int64) (*Operation, error)
	// OpenForUser lists a user's operations that can still continue, oldest
	// first. The list is bounded by MaxActivePerUser plus a small margin.
	OpenForUser(ctx context.Context, userID string) ([]*Operation, error)
	// Sweep expires operations past their time and removes ones that ended
	// more than PurgeAfter ago, at most limit rows each.
	Sweep(ctx context.Context, now time.Time, limit int) (SweepResult, error)
}

// SweepResult reports one bounded cleanup pass.
type SweepResult struct {
	Expired int64
	Purged  int64
}

// SQLiteStore keeps operations in the shared application database.
type SQLiteStore struct {
	db *database.DB
}

// NewSQLiteStore constructs the operation store.
func NewSQLiteStore(db *database.DB) *SQLiteStore { return &SQLiteStore{db: db} }

var _ Store = (*SQLiteStore)(nil)

const operationColumns = `id, contract_version, kind, status, revision, user_id, assistant_id, state_version,
	hq_workspace_id, profile_name, conversation_id, turn_message_id, tab_id, watch, workspace_id,
	context_json, commands_json, draft_json, task_id, run_id, claimed_at_ms, receipts_json,
	created_at_ms, updated_at_ms, expires_at_ms`

// encoded is an operation's bounded JSON columns.
type encoded struct {
	context, commands, receipts string
	draft                       sql.NullString
	taskID, runID               string
	claimedAt                   int64
}

func encodeOperation(op *Operation) (encoded, error) {
	var out encoded
	if err := op.Validate(); err != nil {
		return out, err
	}
	column := func(value any, limit int, name string) (string, error) {
		data, err := json.Marshal(value)
		if err != nil {
			return "", fmt.Errorf("%w: %s could not be encoded", ErrInvalidCommand, name)
		}
		if len(data) > limit {
			return "", fmt.Errorf("%w: %s exceeds %d bytes", ErrBounds, name, limit)
		}
		return string(data), nil
	}
	var err error
	if out.context, err = column(op.Context, MaxContextBytes, "context"); err != nil {
		return out, err
	}
	commands := op.Commands
	if commands == nil {
		commands = []Command{}
	}
	if out.commands, err = column(commands, MaxCommandsBytes, "commands"); err != nil {
		return out, err
	}
	receipts := op.Receipts
	if receipts == nil {
		receipts = []Receipt{}
	}
	if out.receipts, err = column(receipts, MaxReceiptsBytes, "receipts"); err != nil {
		return out, err
	}
	if op.Draft != nil {
		draft, err := column(op.Draft, MaxDraftBytes, "draft")
		if err != nil {
			return out, err
		}
		out.draft = sql.NullString{String: draft, Valid: true}
	}
	if op.Task != nil {
		out.taskID, out.runID = strings.TrimSpace(op.Task.TaskID), strings.TrimSpace(op.Task.RunID)
		if op.Task.ClaimedAt != nil {
			out.claimedAt = op.Task.ClaimedAt.UnixMilli()
		}
	}
	return out, nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

// Insert stores a new operation at revision 1.
func (s *SQLiteStore) Insert(ctx context.Context, op *Operation) error {
	if s == nil || s.db == nil {
		return ErrUnavailable
	}
	if op == nil || op.Revision != 1 {
		return fmt.Errorf("%w: a new operation starts at revision 1", ErrInvalidCommand)
	}
	columns, err := encodeOperation(op)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO assistant_operations (`+operationColumns+`)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		op.ID, op.Version, string(op.Kind), string(op.Status), op.Revision, op.Owner.UserID, op.Owner.AssistantID,
		op.Owner.StateVersion, op.Owner.HQWorkspaceID, op.Owner.ProfileName, op.Owner.ConversationID,
		op.Owner.TurnMessageID, op.TabID, boolInt(op.Watch), op.Context.WorkspaceID,
		columns.context, columns.commands, columns.draft, columns.taskID, columns.runID, columns.claimedAt,
		columns.receipts, op.CreatedAt.UnixMilli(), op.UpdatedAt.UnixMilli(), op.ExpiresAt.UnixMilli())
	if err != nil {
		if constraintError(err) {
			// A missing conversation, a reused ID or a task already bound to
			// another operation: none may be stored, and none is retried.
			return fmt.Errorf("%w: operation could not be bound", ErrConflict)
		}
		return fmt.Errorf("%w: insert failed", ErrUnavailable)
	}
	return nil
}

// Get reads one operation by ID. Ownership is the service's check, not this
// read's: the store never decides who may see a row.
func (s *SQLiteStore) Get(ctx context.Context, id string) (*Operation, error) {
	if s == nil || s.db == nil {
		return nil, ErrUnavailable
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, ErrNotFound
	}
	row := s.db.QueryRowContext(ctx, `SELECT `+operationColumns+` FROM assistant_operations WHERE id = ?`, id)
	op, err := scanOperation(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return op, err
}

// Update compare-and-swaps the mutable fields. Owner and identity columns are
// never rewritten: an operation cannot change who or what it belongs to.
func (s *SQLiteStore) Update(ctx context.Context, op *Operation, expected int64) (*Operation, error) {
	if s == nil || s.db == nil {
		return nil, ErrUnavailable
	}
	if op == nil || expected <= 0 {
		return nil, fmt.Errorf("%w: an expected revision is required", ErrConflict)
	}
	columns, err := encodeOperation(op)
	if err != nil {
		return nil, err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE assistant_operations SET
			status = ?, revision = revision + 1, turn_message_id = ?, tab_id = ?, watch = ?,
			context_json = ?, commands_json = ?, draft_json = ?, task_id = ?, run_id = ?, claimed_at_ms = ?,
			receipts_json = ?, updated_at_ms = ?, expires_at_ms = ?
		WHERE id = ? AND revision = ?`,
		string(op.Status), op.Owner.TurnMessageID, op.TabID, boolInt(op.Watch),
		columns.context, columns.commands, columns.draft, columns.taskID, columns.runID, columns.claimedAt,
		columns.receipts, op.UpdatedAt.UnixMilli(), op.ExpiresAt.UnixMilli(), op.ID, expected)
	if err != nil {
		if constraintError(err) {
			return nil, fmt.Errorf("%w: operation could not be bound", ErrConflict)
		}
		return nil, fmt.Errorf("%w: update failed", ErrUnavailable)
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		if _, getErr := s.Get(ctx, op.ID); errors.Is(getErr, ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, ErrConflict
	}
	return s.Get(ctx, op.ID)
}

// OpenForUser lists operations that have not ended, oldest first.
func (s *SQLiteStore) OpenForUser(ctx context.Context, userID string) ([]*Operation, error) {
	if s == nil || s.db == nil {
		return nil, ErrUnavailable
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+operationColumns+` FROM assistant_operations
		WHERE user_id = ? AND status NOT IN ('cancelled','expired','invalidated')
		ORDER BY updated_at_ms ASC, id ASC LIMIT ?`, strings.TrimSpace(userID), MaxActivePerUser+CleanupBatch)
	if err != nil {
		return nil, fmt.Errorf("%w: listing failed", ErrUnavailable)
	}
	defer func() { _ = rows.Close() }()
	var out []*Operation
	for rows.Next() {
		op, err := scanOperation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, op)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%w: listing failed", ErrUnavailable)
	}
	return out, nil
}

// Sweep is the only bulk write. It ends continuation for operations past their
// time and removes long-ended rows; it never touches a task.
func (s *SQLiteStore) Sweep(ctx context.Context, now time.Time, limit int) (SweepResult, error) {
	var out SweepResult
	if s == nil || s.db == nil {
		return out, ErrUnavailable
	}
	if limit <= 0 || limit > CleanupBatch {
		limit = CleanupBatch
	}
	nowMS := now.UnixMilli()
	err := s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		expired, err := tx.ExecContext(ctx, `UPDATE assistant_operations
			SET status = 'expired', revision = revision + 1, updated_at_ms = ?
			WHERE id IN (SELECT id FROM assistant_operations
				WHERE expires_at_ms <= ? AND status NOT IN ('cancelled','expired','invalidated')
				ORDER BY expires_at_ms ASC LIMIT ?)`, nowMS, nowMS, limit)
		if err != nil {
			return err
		}
		out.Expired, _ = expired.RowsAffected()
		cutoff := now.Add(-PurgeAfter).UnixMilli()
		purged, err := tx.ExecContext(ctx, `DELETE FROM assistant_operations
			WHERE id IN (SELECT id FROM assistant_operations
				WHERE status IN ('cancelled','expired','invalidated') AND updated_at_ms <= ?
				ORDER BY updated_at_ms ASC LIMIT ?)`, cutoff, limit)
		if err != nil {
			return err
		}
		out.Purged, _ = purged.RowsAffected()
		return nil
	})
	if err != nil {
		return SweepResult{}, fmt.Errorf("%w: cleanup failed", ErrUnavailable)
	}
	return out, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanOperation(row rowScanner) (*Operation, error) {
	var (
		op                                     Operation
		kind, status                           string
		watch                                  int
		workspaceID                            string
		contextJSON, commandsJSON, receiptJSON string
		draftJSON                              sql.NullString
		taskID, runID                          string
		claimedAt, created, updated, expires   int64
	)
	err := row.Scan(&op.ID, &op.Version, &kind, &status, &op.Revision, &op.Owner.UserID, &op.Owner.AssistantID,
		&op.Owner.StateVersion, &op.Owner.HQWorkspaceID, &op.Owner.ProfileName, &op.Owner.ConversationID,
		&op.Owner.TurnMessageID, &op.TabID, &watch, &workspaceID,
		&contextJSON, &commandsJSON, &draftJSON, &taskID, &runID, &claimedAt, &receiptJSON,
		&created, &updated, &expires)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: read failed", ErrUnavailable)
	}
	op.Kind, op.Status, op.Watch = Kind(kind), Status(status), watch == 1
	// A row this build cannot decode is unavailable, never an empty operation.
	if json.Unmarshal([]byte(contextJSON), &op.Context) != nil ||
		json.Unmarshal([]byte(commandsJSON), &op.Commands) != nil ||
		json.Unmarshal([]byte(receiptJSON), &op.Receipts) != nil {
		return nil, fmt.Errorf("%w: stored operation is unreadable", ErrUnavailable)
	}
	if op.Context.WorkspaceID != workspaceID {
		return nil, fmt.Errorf("%w: stored operation is inconsistent", ErrUnavailable)
	}
	if draftJSON.Valid {
		var draft Draft
		if json.Unmarshal([]byte(draftJSON.String), &draft) != nil {
			return nil, fmt.Errorf("%w: stored draft is unreadable", ErrUnavailable)
		}
		op.Draft = &draft
	}
	if taskID != "" {
		op.Task = &TaskBinding{TaskID: taskID, RunID: runID}
		if claimedAt > 0 {
			at := time.UnixMilli(claimedAt).UTC()
			op.Task.ClaimedAt = &at
		}
	}
	op.CreatedAt, op.UpdatedAt, op.ExpiresAt = time.UnixMilli(created).UTC(), time.UnixMilli(updated).UTC(), time.UnixMilli(expires).UTC()
	if len(op.Commands) == 0 {
		op.Commands = nil
	}
	if len(op.Receipts) == 0 {
		op.Receipts = nil
	}
	return &op, nil
}

func constraintError(err error) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	return strings.Contains(message, "UNIQUE constraint") || strings.Contains(message, "FOREIGN KEY constraint") ||
		strings.Contains(message, "CHECK constraint")
}
