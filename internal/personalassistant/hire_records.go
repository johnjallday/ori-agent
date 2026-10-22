package personalassistant

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/internal/database"
)

// A hire from the Agents page (agents-hire-with-a-brief) is recorded once it
// has created its agent and workspace. The record is how the assistant finds
// the hire's first look to report on, and how a retried request with the same
// id returns the first hire instead of hiring twice. Every field is short text
// the hire wrote itself; none is a path.

// HireState is where a hire's first look stands, as the report reads it.
type HireState string

const (
	// HireStateLooking is a hire whose first look has not finished.
	HireStateLooking HireState = "looking"
	// HireStateReported is a finished look whose report waits for the user.
	HireStateReported HireState = "reported"
	// HireStateStarted is a report whose suggestion the user started.
	HireStateStarted HireState = "started"
	// HireStateDismissed is a report the user put aside with Not now.
	HireStateDismissed HireState = "dismissed"
	// HireStateFailed is a look that did not start or did not finish.
	HireStateFailed HireState = "failed"
)

// HireBlueprintBlank is the blueprint of a hire whose role has no project
// blueprint of its own: a workspace with the hired agent as its only member.
const HireBlueprintBlank = "blank"

// Bounds on a hire record's text.
const (
	hireIDMax        = 128
	hireAgentNameMax = 100
	hireRoleMax      = 40
	hireBlueprintMax = 80
	hireFailureMax   = 400
)

var (
	// ErrHireNotFound is a hire id or request id with no record.
	ErrHireNotFound = errors.New("personal assistant: no such hire")
	// ErrHireRequestUsed is a request id another hire already used.
	ErrHireRequestUsed = errors.New("personal assistant: that hire request id is already used")
)

// HireRecord is one hire as the assistant keeps it.
type HireRecord struct {
	ID          string    `json:"id"`
	UserID      string    `json:"user_id"`
	RequestID   string    `json:"request_id"`
	AgentName   string    `json:"agent_name"`
	WorkspaceID string    `json:"workspace_id"`
	RunID       string    `json:"run_id,omitempty"`
	TaskID      string    `json:"task_id,omitempty"`
	Brief       string    `json:"brief"`
	Role        string    `json:"role"`
	Blueprint   string    `json:"blueprint"`
	State       HireState `json:"state"`
	Failure     string    `json:"failure,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	// FinishedAt is when the look finished or failed; zero while it runs.
	FinishedAt time.Time `json:"finished_at,omitzero"`
}

// HireRecordStore keeps hires from the Agents page.
type HireRecordStore interface {
	Create(ctx context.Context, record HireRecord) (HireRecord, error)
	Get(ctx context.Context, id string) (HireRecord, error)
	ByRequestID(ctx context.Context, requestID string) (HireRecord, error)
	ByWorkspace(ctx context.Context, workspaceID string) ([]HireRecord, error)
	List(ctx context.Context, userID string) ([]HireRecord, error)
	Update(ctx context.Context, id string, mutate func(*HireRecord) error) (HireRecord, error)
}

// SQLiteHireRecords stores hires in the shared application database.
type SQLiteHireRecords struct {
	db  *database.DB
	now func() time.Time
}

var _ HireRecordStore = (*SQLiteHireRecords)(nil)

// NewSQLiteHireRecords constructs the hire record store.
func NewSQLiteHireRecords(db *database.DB) *SQLiteHireRecords {
	return &SQLiteHireRecords{db: db, now: time.Now}
}

func (s *SQLiteHireRecords) ready() error {
	if s == nil || s.db == nil {
		return errors.New("personal assistant: hire records are not configured")
	}
	return nil
}

// validate keeps a record bounded, path-free and in a known state.
func (r *HireRecord) validate() error {
	ids := []struct {
		label, value string
		max          int
	}{
		{"hire id", r.ID, hireIDMax},
		{"user id", r.UserID, hireIDMax},
		{"request id", r.RequestID, hireIDMax},
		{"agent name", r.AgentName, hireAgentNameMax},
		{"workspace id", r.WorkspaceID, hireIDMax},
		{"role", r.Role, hireRoleMax},
		{"blueprint", r.Blueprint, hireBlueprintMax},
	}
	for _, field := range ids {
		if strings.TrimSpace(field.value) == "" || len(field.value) > field.max ||
			strings.ContainsAny(field.value, "\x00\r\n\t") || looksLikeFilesystemPath(field.value) {
			return fmt.Errorf("%w: hire %s", ErrValidation, field.label)
		}
	}
	for _, field := range []struct{ label, value string }{{"run id", r.RunID}, {"task id", r.TaskID}} {
		if len(field.value) > hireIDMax || strings.ContainsAny(field.value, "\x00\r\n\t") || looksLikeFilesystemPath(field.value) {
			return fmt.Errorf("%w: hire %s", ErrValidation, field.label)
		}
	}
	if _, err := validateText("brief", r.Brief, WorkspaceGoalMaxRunes, true); err != nil {
		return fmt.Errorf("%w: %s", ErrValidation, err.Error())
	}
	if utf8.RuneCountInString(r.Failure) > hireFailureMax || strings.ContainsRune(r.Failure, 0) {
		return fmt.Errorf("%w: hire failure", ErrValidation)
	}
	switch r.State {
	case HireStateLooking, HireStateReported, HireStateStarted, HireStateDismissed, HireStateFailed:
	default:
		return fmt.Errorf("%w: hire state", ErrValidation)
	}
	return nil
}

// Create stores a new hire. A request id already used is ErrHireRequestUsed.
func (s *SQLiteHireRecords) Create(ctx context.Context, record HireRecord) (HireRecord, error) {
	if err := s.ready(); err != nil {
		return HireRecord{}, err
	}
	now := s.now().UTC()
	record.CreatedAt, record.UpdatedAt = now, now
	if err := record.validate(); err != nil {
		return HireRecord{}, err
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO agent_hires (id, user_id, request_id, agent_name, workspace_id, run_id, task_id,
			brief, role, blueprint, state, failure, created_at, updated_at, finished_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.ID, record.UserID, record.RequestID, record.AgentName, record.WorkspaceID, record.RunID, record.TaskID,
		record.Brief, record.Role, record.Blueprint, string(record.State), record.Failure,
		record.CreatedAt, record.UpdatedAt, nullableTime(record.FinishedAt))
	if err != nil {
		if isConstraintError(err) {
			return HireRecord{}, ErrHireRequestUsed
		}
		return HireRecord{}, fmt.Errorf("personal assistant: create hire: %w", err)
	}
	return record, nil
}

const hireColumns = `id, user_id, request_id, agent_name, workspace_id, run_id, task_id,
	brief, role, blueprint, state, failure, created_at, updated_at, finished_at`

type hireRowScanner interface {
	Scan(dest ...any) error
}

func scanHire(row hireRowScanner) (HireRecord, error) {
	var record HireRecord
	var state string
	var finished sql.NullTime
	err := row.Scan(&record.ID, &record.UserID, &record.RequestID, &record.AgentName, &record.WorkspaceID,
		&record.RunID, &record.TaskID, &record.Brief, &record.Role, &record.Blueprint, &state, &record.Failure,
		&record.CreatedAt, &record.UpdatedAt, &finished)
	if errors.Is(err, sql.ErrNoRows) {
		return HireRecord{}, ErrHireNotFound
	}
	if err != nil {
		return HireRecord{}, fmt.Errorf("personal assistant: read hire: %w", err)
	}
	record.State = HireState(state)
	if finished.Valid {
		record.FinishedAt = finished.Time
	}
	return record, nil
}

func (s *SQLiteHireRecords) one(ctx context.Context, where string, arg string) (HireRecord, error) {
	if err := s.ready(); err != nil {
		return HireRecord{}, err
	}
	return scanHire(s.db.QueryRowContext(ctx, `SELECT `+hireColumns+` FROM agent_hires WHERE `+where, arg))
}

func (s *SQLiteHireRecords) many(ctx context.Context, where string, arg string) ([]HireRecord, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+hireColumns+` FROM agent_hires WHERE `+where+` ORDER BY created_at, id`, arg)
	if err != nil {
		return nil, fmt.Errorf("personal assistant: list hires: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var records []HireRecord
	for rows.Next() {
		record, err := scanHire(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("personal assistant: list hires: %w", err)
	}
	return records, nil
}

// Get reads one hire by its id.
func (s *SQLiteHireRecords) Get(ctx context.Context, id string) (HireRecord, error) {
	return s.one(ctx, `id = ?`, id)
}

// ByRequestID reads the hire a request id made, whoever made it; the caller
// refuses one that belongs to another user.
func (s *SQLiteHireRecords) ByRequestID(ctx context.Context, requestID string) (HireRecord, error) {
	return s.one(ctx, `request_id = ?`, requestID)
}

// ByWorkspace lists the hires that made a workspace, oldest first.
func (s *SQLiteHireRecords) ByWorkspace(ctx context.Context, workspaceID string) ([]HireRecord, error) {
	return s.many(ctx, `workspace_id = ?`, workspaceID)
}

// List lists a user's hires, oldest first.
func (s *SQLiteHireRecords) List(ctx context.Context, userID string) ([]HireRecord, error) {
	return s.many(ctx, `user_id = ?`, userID)
}

// Update changes one hire in a transaction. mutate may change the run, task,
// state, failure and finished time; the hire's identity and what it created
// stay as they were.
func (s *SQLiteHireRecords) Update(ctx context.Context, id string, mutate func(*HireRecord) error) (HireRecord, error) {
	if err := s.ready(); err != nil {
		return HireRecord{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return HireRecord{}, fmt.Errorf("personal assistant: update hire: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	current, err := scanHire(tx.QueryRowContext(ctx, `SELECT `+hireColumns+` FROM agent_hires WHERE id = ?`, id))
	if err != nil {
		return HireRecord{}, err
	}
	next := current
	if err := mutate(&next); err != nil {
		return HireRecord{}, err
	}
	next.ID, next.UserID, next.RequestID, next.CreatedAt = current.ID, current.UserID, current.RequestID, current.CreatedAt
	next.AgentName, next.WorkspaceID, next.Brief, next.Role, next.Blueprint =
		current.AgentName, current.WorkspaceID, current.Brief, current.Role, current.Blueprint
	next.UpdatedAt = s.now().UTC()
	if err := next.validate(); err != nil {
		return HireRecord{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE agent_hires SET run_id = ?, task_id = ?, state = ?, failure = ?, updated_at = ?, finished_at = ?
		WHERE id = ?`,
		next.RunID, next.TaskID, string(next.State), next.Failure, next.UpdatedAt, nullableTime(next.FinishedAt), next.ID); err != nil {
		return HireRecord{}, fmt.Errorf("personal assistant: update hire: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return HireRecord{}, fmt.Errorf("personal assistant: update hire: %w", err)
	}
	return next, nil
}

func nullableTime(at time.Time) any {
	if at.IsZero() {
		return nil
	}
	return at.UTC()
}
