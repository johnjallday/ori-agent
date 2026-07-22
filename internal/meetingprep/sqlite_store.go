package meetingprep

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/johnjallday/ori-agent/internal/database"
)

// ErrNotFound is returned when no link exists for the requested key/id.
var ErrNotFound = errors.New("meetingprep: not found")

// SQLiteStore persists meeting-prep links in the shared SQLite database.
type SQLiteStore struct {
	db *database.DB
}

// NewSQLiteStore constructs a meeting-prep store over db.
func NewSQLiteStore(db *database.DB) *SQLiteStore { return &SQLiteStore{db: db} }

const linkColumns = `id, workspace_id, binding_id, calendar_id, event_id, note_id,
	event_fingerprint, status, task_id, error, created_at, updated_at`

// GetByKey returns the link for a meeting, or ErrNotFound.
func (s *SQLiteStore) GetByKey(ctx context.Context, key Key) (*Link, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+linkColumns+` FROM calendar_meeting_prep
		WHERE workspace_id = ? AND binding_id = ? AND calendar_id = ? AND event_id = ?`,
		key.WorkspaceID, key.BindingID, key.CalendarID, key.EventID)
	link, err := scanLink(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return link, err
}

// GetByID returns a link by its own id, or ErrNotFound.
func (s *SQLiteStore) GetByID(ctx context.Context, id string) (*Link, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+linkColumns+` FROM calendar_meeting_prep WHERE id = ?`, id)
	link, err := scanLink(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return link, err
}

// StartRun begins a new prep run for key. It always attempts the INSERT
// first (a first-ever run for this meeting) and lets the database's own
// UNIQUE(workspace_id, binding_id, calendar_id, event_id) index -- not a
// separate read-then-write check racing against other requests -- decide
// whether one already exists; SQLiteStore's connection pool is capped to one
// connection (internal/database/db.go), so this single statement is the only
// point that needs to be atomic. On conflict the existing row is read back:
// if it is already StatusPending, it is returned unchanged (dedupes a
// concurrent second "Prepare me" click into the same in-flight run -- task
// 6.6/6.7); otherwise it is reset to Pending in place (a rerun), preserving
// its id and prior NoteID so the rerun still upserts the same note.
func (s *SQLiteStore) StartRun(ctx context.Context, key Key, taskID string) (link *Link, alreadyRunning bool, err error) {
	now := time.Now().UTC()
	link = &Link{
		ID:        uuid.NewString(),
		Key:       key,
		Status:    StatusPending,
		TaskID:    taskID,
		CreatedAt: now,
		UpdatedAt: now,
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO calendar_meeting_prep (`+linkColumns+`)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		link.ID, link.Key.WorkspaceID, link.Key.BindingID, link.Key.CalendarID, link.Key.EventID,
		link.NoteID, link.EventFingerprint, string(link.Status), link.TaskID, link.Error,
		link.CreatedAt, link.UpdatedAt)
	if err == nil {
		return link, false, nil
	}
	if !isUniqueConstraintError(err) {
		return nil, false, fmt.Errorf("meetingprep start run: %w", err)
	}

	existing, getErr := s.GetByKey(ctx, key)
	if getErr != nil {
		return nil, false, fmt.Errorf("meetingprep start run: load existing after conflict: %w", getErr)
	}
	if existing.Status == StatusPending {
		return existing, true, nil
	}
	existing.Status = StatusPending
	existing.TaskID = taskID
	existing.Error = ""
	existing.UpdatedAt = now
	if _, err := s.db.ExecContext(ctx, `UPDATE calendar_meeting_prep SET
		status = ?, task_id = ?, error = '', updated_at = ? WHERE id = ?`,
		string(existing.Status), existing.TaskID, existing.UpdatedAt, existing.ID); err != nil {
		return nil, false, fmt.Errorf("meetingprep restart run: %w", err)
	}
	return existing, false, nil
}

func isUniqueConstraintError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint")
}

// MarkReady records a successful prep run: the linked note id and the
// fingerprint of the event it was grounded in.
func (s *SQLiteStore) MarkReady(ctx context.Context, id, noteID, fingerprint string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE calendar_meeting_prep SET
		status = ?, note_id = ?, event_fingerprint = ?, error = '', updated_at = ? WHERE id = ?`,
		string(StatusReady), noteID, fingerprint, time.Now().UTC(), id)
	if err != nil {
		return fmt.Errorf("meetingprep mark ready: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// MarkFailed records a failed prep run with a human-readable reason.
func (s *SQLiteStore) MarkFailed(ctx context.Context, id, reason string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE calendar_meeting_prep SET
		status = ?, error = ?, updated_at = ? WHERE id = ?`,
		string(StatusFailed), reason, time.Now().UTC(), id)
	if err != nil {
		return fmt.Errorf("meetingprep mark failed: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanLink(row scanner) (*Link, error) {
	var link Link
	var status string
	if err := row.Scan(
		&link.ID, &link.Key.WorkspaceID, &link.Key.BindingID, &link.Key.CalendarID, &link.Key.EventID,
		&link.NoteID, &link.EventFingerprint, &status, &link.TaskID, &link.Error,
		&link.CreatedAt, &link.UpdatedAt,
	); err != nil {
		return nil, err
	}
	link.Status = Status(status)
	return &link, nil
}
