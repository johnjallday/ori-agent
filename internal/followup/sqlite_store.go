package followup

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
)

// ErrNotFound is returned when a follow-up does not exist for the requesting user.
var ErrNotFound = errors.New("followup: not found")

// SQLiteStore persists follow-ups in the shared SQLite database.
type SQLiteStore struct {
	db *database.DB
}

// NewSQLiteStore constructs a follow-up store over db.
func NewSQLiteStore(db *database.DB) *SQLiteStore { return &SQLiteStore{db: db} }

const followUpColumns = `id, user_id, workspace_id, category, direction, title, detail, counterparty,
	source_type, source_id, source_account_id, dedup_key, provenance, confidence, status,
	due_at, snoozed_until, last_nudged_at, related_workspace_id, related_task_id,
	created_at, updated_at, completed_at, dismissed_at`

// Create inserts a new follow-up.
func (s *SQLiteStore) Create(ctx context.Context, f *FollowUp) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO personal_hq_followup (`+followUpColumns+`)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		f.ID, f.UserID, f.WorkspaceID, string(f.Category), string(f.Direction), f.Title, f.Detail, f.Counterparty,
		f.Source.Type, f.Source.ID, f.Source.AccountID, f.DedupKey, string(f.Provenance), string(f.Confidence), string(f.Status),
		nullTime(f.DueAt), nullTime(f.SnoozedUntil), nullTime(f.LastNudgedAt),
		taskWorkspace(f.RelatedTask), taskID(f.RelatedTask),
		f.CreatedAt, f.UpdatedAt, nullTime(f.CompletedAt), nullTime(f.DismissedAt))
	if err != nil {
		return fmt.Errorf("followup create: %w", err)
	}
	return nil
}

// Get returns a follow-up by id, scoped to userID.
func (s *SQLiteStore) Get(ctx context.Context, userID, id string) (*FollowUp, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+followUpColumns+` FROM personal_hq_followup WHERE id = ? AND user_id = ?`, id, userID)
	f, err := scanFollowUp(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return f, err
}

// GetByDedupKey returns the existing sourced follow-up for a user+dedup key, or
// ErrNotFound. Used by source-based upsert to avoid duplicates.
func (s *SQLiteStore) GetByDedupKey(ctx context.Context, userID, dedupKey string) (*FollowUp, error) {
	if strings.TrimSpace(dedupKey) == "" {
		return nil, ErrNotFound
	}
	row := s.db.QueryRowContext(ctx, `SELECT `+followUpColumns+` FROM personal_hq_followup WHERE user_id = ? AND dedup_key = ?`, userID, dedupKey)
	f, err := scanFollowUp(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return f, err
}

// Filter narrows a list query.
type Filter struct {
	UserID string
	// WorkspaceID, when set, scopes the list to follow-ups owned by that
	// workspace (the Email Ops spin-off: follow-ups are keyed to the workspace
	// whose agents captured them). Blank = any workspace, so legacy rows with an
	// empty or HQ workspace_id are simply never matched by a workspace filter.
	WorkspaceID string
	Statuses    []Status // empty = any
	OpenOnly    bool     // convenience: active/snoozed/candidate/reopened
}

// List returns follow-ups matching the filter, most-recently-updated first.
func (s *SQLiteStore) List(ctx context.Context, f Filter) ([]*FollowUp, error) {
	var where []string
	var args []any
	// A blank UserID lists across all users (used by global maintenance sweeps
	// like snooze-wake); a set UserID scopes to that user.
	if strings.TrimSpace(f.UserID) != "" {
		where = append(where, "user_id = ?")
		args = append(args, f.UserID)
	}
	if strings.TrimSpace(f.WorkspaceID) != "" {
		where = append(where, "workspace_id = ?")
		args = append(args, f.WorkspaceID)
	}

	statuses := f.Statuses
	if f.OpenOnly {
		statuses = []Status{StatusActive, StatusSnoozed, StatusCandidate, StatusReopened}
	}
	if len(statuses) > 0 {
		ph := make([]string, len(statuses))
		for i, st := range statuses {
			ph[i] = "?"
			args = append(args, string(st))
		}
		where = append(where, "status IN ("+strings.Join(ph, ",")+")")
	}

	query := `SELECT ` + followUpColumns + ` FROM personal_hq_followup`
	if len(where) > 0 {
		query += ` WHERE ` + strings.Join(where, " AND ")
	}
	query += ` ORDER BY updated_at DESC`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("followup list: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []*FollowUp
	for rows.Next() {
		fu, err := scanFollowUp(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, fu)
	}
	return out, rows.Err()
}

// Update writes all mutable fields of an existing follow-up.
func (s *SQLiteStore) Update(ctx context.Context, f *FollowUp) error {
	res, err := s.db.ExecContext(ctx, `UPDATE personal_hq_followup SET
		category=?, direction=?, title=?, detail=?, counterparty=?, provenance=?, confidence=?, status=?,
		due_at=?, snoozed_until=?, last_nudged_at=?, related_workspace_id=?, related_task_id=?,
		updated_at=?, completed_at=?, dismissed_at=?
		WHERE id=? AND user_id=?`,
		string(f.Category), string(f.Direction), f.Title, f.Detail, f.Counterparty, string(f.Provenance), string(f.Confidence), string(f.Status),
		nullTime(f.DueAt), nullTime(f.SnoozedUntil), nullTime(f.LastNudgedAt), taskWorkspace(f.RelatedTask), taskID(f.RelatedTask),
		f.UpdatedAt, nullTime(f.CompletedAt), nullTime(f.DismissedAt),
		f.ID, f.UserID)
	if err != nil {
		return fmt.Errorf("followup update: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete removes a follow-up (used by retention).
func (s *SQLiteStore) Delete(ctx context.Context, userID, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM personal_hq_followup WHERE id=? AND user_id=?`, id, userID)
	return err
}

type scanner interface {
	Scan(dest ...any) error
}

func scanFollowUp(row scanner) (*FollowUp, error) {
	var f FollowUp
	var category, direction, provenance, confidence, status string
	var due, snoozed, nudged, completed, dismissed sql.NullTime
	var relWS, relTask string
	if err := row.Scan(
		&f.ID, &f.UserID, &f.WorkspaceID, &category, &direction, &f.Title, &f.Detail, &f.Counterparty,
		&f.Source.Type, &f.Source.ID, &f.Source.AccountID, &f.DedupKey, &provenance, &confidence, &status,
		&due, &snoozed, &nudged, &relWS, &relTask,
		&f.CreatedAt, &f.UpdatedAt, &completed, &dismissed,
	); err != nil {
		return nil, err
	}
	f.Category = Category(category)
	f.Direction = Direction(direction)
	f.Provenance = Provenance(provenance)
	f.Confidence = Confidence(confidence)
	f.Status = Status(status)
	f.DueAt = scanTime(due)
	f.SnoozedUntil = scanTime(snoozed)
	f.LastNudgedAt = scanTime(nudged)
	f.CompletedAt = scanTime(completed)
	f.DismissedAt = scanTime(dismissed)
	if relWS != "" || relTask != "" {
		f.RelatedTask = &TaskRef{WorkspaceID: relWS, TaskID: relTask}
	}
	return &f, nil
}

func nullTime(t *time.Time) any {
	if t == nil || t.IsZero() {
		return nil
	}
	return *t
}

func scanTime(n sql.NullTime) *time.Time {
	if !n.Valid {
		return nil
	}
	t := n.Time
	return &t
}

func taskWorkspace(r *TaskRef) string {
	if r == nil {
		return ""
	}
	return r.WorkspaceID
}

func taskID(r *TaskRef) string {
	if r == nil {
		return ""
	}
	return r.TaskID
}
