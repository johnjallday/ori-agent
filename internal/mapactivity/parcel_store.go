package mapactivity

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

// ErrParcelNotFound means no parcel has that id.
var ErrParcelNotFound = errors.New("parcel not found")

// ErrParcelStoreUnavailable means no parcel storage is wired. The tracker then
// creates no parcels, which is the same as the feature being off for results.
var ErrParcelStoreUnavailable = errors.New("parcel storage is not configured")

// XPReport is what a run's XP award actually paid, captured when it finished
// (FR38). Progress is the fraction of the current level already earned, 0 to 1.
type XPReport struct {
	Awarded        int64   `json:"awarded"`
	LevelBefore    int     `json:"level_before"`
	LevelAfter     int     `json:"level_after"`
	ProgressBefore float64 `json:"progress_before"`
	ProgressAfter  float64 `json:"progress_after"`
	StageBefore    string  `json:"stage_before"`
	StageAfter     string  `json:"stage_after"`
}

// Parcel is one finished run's result waiting to be opened (FR34).
type Parcel struct {
	ID            string
	WorkspaceID   string
	Kind          Kind
	RefID         string
	RunKey        string
	AgentName     string
	Title         string
	Summary       string
	Outcome       Outcome
	FailureReason string
	StartedAt     *time.Time
	ProducedAt    time.Time
	OpenedAt      *time.Time
	XP            XPReport
}

// Row is the parcel as a pile row: no summary, no failure reason, no
// rewards (FR37, FR62).
func (p Parcel) Row() ParcelSummary {
	return ParcelSummary{
		ID:          p.ID,
		WorkspaceID: p.WorkspaceID,
		Kind:        p.Kind,
		Title:       p.Title,
		AgentName:   p.AgentName,
		Outcome:     p.Outcome,
		ProducedAt:  p.ProducedAt,
	}
}

// ParcelStore keeps parcels.
type ParcelStore interface {
	// Create writes a parcel once per (kind, ref id, run key). The bool is
	// false when that run already had one; the existing parcel is returned.
	Create(ctx context.Context, parcel Parcel) (Parcel, bool, error)
	ListUnopened(ctx context.Context) ([]Parcel, error)
	// Open marks a parcel opened the first time and returns it every time.
	Open(ctx context.Context, id string, now time.Time) (Parcel, error)
	// OpenByRef opens every unopened parcel for one thing — a task, a brief, a
	// janitor scan — and returns the ones it opened.
	OpenByRef(ctx context.Context, kind Kind, workspaceID, refID string, now time.Time) ([]Parcel, error)
	DeleteForWorkspace(ctx context.Context, workspaceID string) (int64, error)
	DeleteForTask(ctx context.Context, workspaceID, taskID string) (int64, error)
	// SweepUnopenedOlderThan marks parcels produced before cutoff opened.
	SweepUnopenedOlderThan(ctx context.Context, cutoff, now time.Time) (int64, error)
}

// SQLiteParcelStore is the ParcelStore over the shared application database.
type SQLiteParcelStore struct {
	db *database.DB
}

// NewSQLiteParcelStore builds the store. A nil database is allowed and answers
// ErrParcelStoreUnavailable.
func NewSQLiteParcelStore(db *database.DB) *SQLiteParcelStore {
	return &SQLiteParcelStore{db: db}
}

const parcelColumns = `id, workspace_id, kind, ref_id, run_key, agent_name, title, summary,
	outcome, failure_reason, started_at, produced_at, opened_at,
	xp_awarded, level_before, level_after, progress_before, progress_after, stage_before, stage_after`

func (s *SQLiteParcelStore) available() error {
	if s == nil || s.db == nil {
		return ErrParcelStoreUnavailable
	}
	return nil
}

// storedTimeLayout keeps every fractional digit, so stored times sort and
// compare correctly as text in SQL. RFC3339Nano trims trailing zeros, which
// would put "10:00:00Z" after "10:00:00.5Z".
const storedTimeLayout = "2006-01-02T15:04:05.000000000Z07:00"

func formatTime(t time.Time) string {
	return t.UTC().Format(storedTimeLayout)
}

func nullableTime(t *time.Time) any {
	if t == nil || t.IsZero() {
		return nil
	}
	return formatTime(*t)
}

// Create writes the parcel unless its run already has one.
func (s *SQLiteParcelStore) Create(ctx context.Context, parcel Parcel) (Parcel, bool, error) {
	if err := s.available(); err != nil {
		return Parcel{}, false, err
	}
	parcel.WorkspaceID = strings.TrimSpace(parcel.WorkspaceID)
	parcel.RefID = strings.TrimSpace(parcel.RefID)
	if parcel.WorkspaceID == "" || parcel.RefID == "" || parcel.Kind == "" || parcel.Outcome == "" {
		return Parcel{}, false, fmt.Errorf("parcel needs a workspace, a kind, a reference and an outcome")
	}
	if parcel.ID == "" {
		parcel.ID = uuid.NewString()
	}
	if parcel.ProducedAt.IsZero() {
		parcel.ProducedAt = time.Now()
	}
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO result_parcels (`+parcelColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(kind, ref_id, run_key) DO NOTHING
	`,
		parcel.ID, parcel.WorkspaceID, string(parcel.Kind), parcel.RefID, parcel.RunKey,
		parcel.AgentName, parcel.Title, parcel.Summary, string(parcel.Outcome), parcel.FailureReason,
		nullableTime(parcel.StartedAt), formatTime(parcel.ProducedAt),
		parcel.XP.Awarded, parcel.XP.LevelBefore, parcel.XP.LevelAfter,
		parcel.XP.ProgressBefore, parcel.XP.ProgressAfter, parcel.XP.StageBefore, parcel.XP.StageAfter,
	)
	if err != nil {
		return Parcel{}, false, fmt.Errorf("create parcel: %w", err)
	}
	affected, _ := result.RowsAffected()
	stored, err := s.scanOne(ctx, `SELECT `+parcelColumns+` FROM result_parcels
		WHERE kind = ? AND ref_id = ? AND run_key = ?`, string(parcel.Kind), parcel.RefID, parcel.RunKey)
	if err != nil {
		return Parcel{}, false, err
	}
	return stored, affected > 0, nil
}

// ListUnopened returns every unopened parcel, oldest first.
func (s *SQLiteParcelStore) ListUnopened(ctx context.Context) ([]Parcel, error) {
	if err := s.available(); err != nil {
		return nil, err
	}
	return s.scanMany(ctx, `SELECT `+parcelColumns+` FROM result_parcels
		WHERE opened_at IS NULL ORDER BY produced_at, id`)
}

// Open marks the parcel opened once and returns it.
func (s *SQLiteParcelStore) Open(ctx context.Context, id string, now time.Time) (Parcel, error) {
	if err := s.available(); err != nil {
		return Parcel{}, err
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return Parcel{}, ErrParcelNotFound
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE result_parcels SET opened_at = ?
		WHERE id = ? AND opened_at IS NULL`, formatTime(now), id); err != nil {
		return Parcel{}, fmt.Errorf("open parcel: %w", err)
	}
	return s.scanOne(ctx, `SELECT `+parcelColumns+` FROM result_parcels WHERE id = ?`, id)
}

// OpenByRef opens every unopened parcel for one reference and returns them.
func (s *SQLiteParcelStore) OpenByRef(ctx context.Context, kind Kind, workspaceID, refID string, now time.Time) ([]Parcel, error) {
	if err := s.available(); err != nil {
		return nil, err
	}
	workspaceID = strings.TrimSpace(workspaceID)
	refID = strings.TrimSpace(refID)
	if kind == "" || workspaceID == "" || refID == "" {
		return nil, nil
	}
	parcels, err := s.scanMany(ctx, `SELECT `+parcelColumns+` FROM result_parcels
		WHERE kind = ? AND workspace_id = ? AND ref_id = ? AND opened_at IS NULL
		ORDER BY produced_at, id`, string(kind), workspaceID, refID)
	if err != nil || len(parcels) == 0 {
		return nil, err
	}
	stamp := formatTime(now)
	if _, err := s.db.ExecContext(ctx, `UPDATE result_parcels SET opened_at = ?
		WHERE kind = ? AND workspace_id = ? AND ref_id = ? AND opened_at IS NULL`,
		stamp, string(kind), workspaceID, refID); err != nil {
		return nil, fmt.Errorf("open parcels by reference: %w", err)
	}
	opened := now.UTC()
	for i := range parcels {
		parcels[i].OpenedAt = &opened
	}
	return parcels, nil
}

// DeleteForWorkspace removes every parcel of a deleted workspace.
func (s *SQLiteParcelStore) DeleteForWorkspace(ctx context.Context, workspaceID string) (int64, error) {
	if err := s.available(); err != nil {
		return 0, err
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM result_parcels WHERE workspace_id = ?`, strings.TrimSpace(workspaceID))
	if err != nil {
		return 0, fmt.Errorf("delete workspace parcels: %w", err)
	}
	n, _ := result.RowsAffected()
	return n, nil
}

// DeleteForTask removes every parcel of a deleted task.
func (s *SQLiteParcelStore) DeleteForTask(ctx context.Context, workspaceID, taskID string) (int64, error) {
	if err := s.available(); err != nil {
		return 0, err
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM result_parcels
		WHERE kind = ? AND workspace_id = ? AND ref_id = ?`,
		string(KindTask), strings.TrimSpace(workspaceID), strings.TrimSpace(taskID))
	if err != nil {
		return 0, fmt.Errorf("delete task parcels: %w", err)
	}
	n, _ := result.RowsAffected()
	return n, nil
}

// SweepUnopenedOlderThan marks old unopened parcels opened (FR41).
func (s *SQLiteParcelStore) SweepUnopenedOlderThan(ctx context.Context, cutoff, now time.Time) (int64, error) {
	if err := s.available(); err != nil {
		return 0, err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE result_parcels SET opened_at = ?
		WHERE opened_at IS NULL AND produced_at < ?`, formatTime(now), formatTime(cutoff))
	if err != nil {
		return 0, fmt.Errorf("sweep old parcels: %w", err)
	}
	n, _ := result.RowsAffected()
	return n, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanParcel(row rowScanner) (Parcel, error) {
	var (
		p                             Parcel
		kind, outcome                 string
		startedAt, openedAt           sql.NullString
		producedAt                    string
		progressBefore, progressAfter float64
	)
	if err := row.Scan(
		&p.ID, &p.WorkspaceID, &kind, &p.RefID, &p.RunKey, &p.AgentName, &p.Title, &p.Summary,
		&outcome, &p.FailureReason, &startedAt, &producedAt, &openedAt,
		&p.XP.Awarded, &p.XP.LevelBefore, &p.XP.LevelAfter, &progressBefore, &progressAfter,
		&p.XP.StageBefore, &p.XP.StageAfter,
	); err != nil {
		return Parcel{}, err
	}
	p.Kind = Kind(kind)
	p.Outcome = Outcome(outcome)
	p.XP.ProgressBefore = progressBefore
	p.XP.ProgressAfter = progressAfter
	if t, err := time.Parse(time.RFC3339Nano, producedAt); err == nil {
		p.ProducedAt = t
	}
	p.StartedAt = parseNullableTime(startedAt)
	p.OpenedAt = parseNullableTime(openedAt)
	return p, nil
}

func parseNullableTime(value sql.NullString) *time.Time {
	if !value.Valid || value.String == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339Nano, value.String)
	if err != nil {
		return nil
	}
	return &t
}

func (s *SQLiteParcelStore) scanOne(ctx context.Context, query string, args ...any) (Parcel, error) {
	parcel, err := scanParcel(s.db.QueryRowContext(ctx, query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return Parcel{}, ErrParcelNotFound
	}
	if err != nil {
		return Parcel{}, fmt.Errorf("read parcel: %w", err)
	}
	return parcel, nil
}

func (s *SQLiteParcelStore) scanMany(ctx context.Context, query string, args ...any) ([]Parcel, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list parcels: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var parcels []Parcel
	for rows.Next() {
		parcel, err := scanParcel(rows)
		if err != nil {
			return nil, fmt.Errorf("read parcel: %w", err)
		}
		parcels = append(parcels, parcel)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list parcels: %w", err)
	}
	return parcels, nil
}
