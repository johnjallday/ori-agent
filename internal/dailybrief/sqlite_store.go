package dailybrief

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/database"
)

// SQLiteStore implements Store over the shared application database.
type SQLiteStore struct {
	db *database.DB
}

// NewSQLiteStore constructs a Daily Brief store.
func NewSQLiteStore(db *database.DB) *SQLiteStore {
	return &SQLiteStore{db: db}
}

var _ Store = (*SQLiteStore)(nil)

func isUniqueConstraintError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint")
}

// ---- Config ----

func (s *SQLiteStore) GetConfig(ctx context.Context, workspaceID string) (*Config, error) {
	var cfg Config
	var daysJSON, selectedJSON string
	var scheduleEnabled, includeFuture, notify int
	var createdAt, updatedAt time.Time
	err := s.db.QueryRowContext(ctx, `
		SELECT workspace_id, user_id, timezone, schedule_days, schedule_time, schedule_enabled,
			scope, selected_workspace_ids, include_future_workspaces, notify_on_ready,
			config_revision, created_at, updated_at
		FROM daily_brief_config WHERE workspace_id = ?
	`, workspaceID).Scan(
		&cfg.WorkspaceID, &cfg.UserID, &cfg.Timezone, &daysJSON, &cfg.ScheduleTime, &scheduleEnabled,
		&cfg.Scope, &selectedJSON, &includeFuture, &notify,
		&cfg.ConfigRevision, &createdAt, &updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrConfigNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get daily brief config: %w", err)
	}
	_ = json.Unmarshal([]byte(daysJSON), &cfg.ScheduleDays)
	_ = json.Unmarshal([]byte(selectedJSON), &cfg.SelectedWorkspaceIDs)
	cfg.ScheduleEnabled = scheduleEnabled != 0
	cfg.IncludeFutureWorkspaces = includeFuture != 0
	cfg.NotifyOnReady = notify != 0
	cfg.CreatedAt = createdAt
	cfg.UpdatedAt = updatedAt
	return &cfg, nil
}

func (s *SQLiteStore) UpsertConfig(ctx context.Context, cfg *Config) error {
	daysJSON, err := json.Marshal(emptySlice(cfg.ScheduleDays))
	if err != nil {
		return fmt.Errorf("failed to encode schedule days: %w", err)
	}
	selectedJSON, err := json.Marshal(emptySlice(cfg.SelectedWorkspaceIDs))
	if err != nil {
		return fmt.Errorf("failed to encode selected workspace ids: %w", err)
	}
	now := time.Now().UTC()
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO daily_brief_config
			(workspace_id, user_id, timezone, schedule_days, schedule_time, schedule_enabled,
			 scope, selected_workspace_ids, include_future_workspaces, notify_on_ready,
			 config_revision, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)
		ON CONFLICT(workspace_id) DO UPDATE SET
			user_id = excluded.user_id,
			timezone = excluded.timezone,
			schedule_days = excluded.schedule_days,
			schedule_time = excluded.schedule_time,
			schedule_enabled = excluded.schedule_enabled,
			scope = excluded.scope,
			selected_workspace_ids = excluded.selected_workspace_ids,
			include_future_workspaces = excluded.include_future_workspaces,
			notify_on_ready = excluded.notify_on_ready,
			config_revision = daily_brief_config.config_revision + 1,
			updated_at = excluded.updated_at
	`, cfg.WorkspaceID, cfg.UserID, cfg.Timezone, string(daysJSON), cfg.ScheduleTime, boolToInt(cfg.ScheduleEnabled),
		string(cfg.Scope), string(selectedJSON), boolToInt(cfg.IncludeFutureWorkspaces), boolToInt(cfg.NotifyOnReady),
		now, now)
	if err != nil {
		return fmt.Errorf("failed to upsert daily brief config: %w", err)
	}
	return nil
}

// ---- Generation claims ----

func (s *SQLiteStore) ClaimGeneration(ctx context.Context, req *GenerationRequest) (*GenerationRequest, bool, error) {
	if req.ID == "" {
		req.ID = uuid.New().String()
	}
	claimedAt := req.ClaimedAt
	if claimedAt.IsZero() {
		claimedAt = time.Now().UTC()
	}
	status := req.Status
	if status == "" {
		status = GenerationPending
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO daily_brief_generation_claim
			(id, workspace_id, local_date, trigger_type, status, revision_id, error, claimed_at)
		VALUES (?, ?, ?, ?, ?, '', '', ?)
	`, req.ID, req.WorkspaceID, req.LocalDate, string(req.Trigger), string(status), claimedAt)
	if err == nil {
		req.ClaimedAt = claimedAt
		req.Status = status
		return req, true, nil
	}
	if !isUniqueConstraintError(err) || req.Trigger == TriggerManual {
		return nil, false, fmt.Errorf("failed to claim daily brief generation: %w", err)
	}
	// A non-manual claim already exists for this workspace/date: return it.
	existing, getErr := s.GetActiveClaim(ctx, req.WorkspaceID, req.LocalDate)
	if getErr != nil {
		return nil, false, fmt.Errorf("failed to load existing daily brief claim: %w", getErr)
	}
	if existing == nil {
		return nil, false, fmt.Errorf("failed to claim daily brief generation: %w", err)
	}
	return existing, false, nil
}

func (s *SQLiteStore) UpdateGenerationStatus(ctx context.Context, id string, status GenerationStatus, revisionID, errMsg string) error {
	var finishedAt any
	if status == GenerationSucceeded || status == GenerationPartial || status == GenerationFailed {
		finishedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE daily_brief_generation_claim
		SET status = ?, revision_id = ?, error = ?, finished_at = COALESCE(?, finished_at)
		WHERE id = ?
	`, string(status), revisionID, errMsg, finishedAt, id)
	if err != nil {
		return fmt.Errorf("failed to update daily brief generation status: %w", err)
	}
	return nil
}

func (s *SQLiteStore) GetGenerationRequest(ctx context.Context, id string) (*GenerationRequest, error) {
	return s.scanClaim(ctx, `
		SELECT id, workspace_id, local_date, trigger_type, status, revision_id, error, claimed_at, finished_at
		FROM daily_brief_generation_claim WHERE id = ?
	`, id)
}

func (s *SQLiteStore) GetActiveClaim(ctx context.Context, workspaceID, localDate string) (*GenerationRequest, error) {
	claim, err := s.scanClaim(ctx, `
		SELECT id, workspace_id, local_date, trigger_type, status, revision_id, error, claimed_at, finished_at
		FROM daily_brief_generation_claim
		WHERE workspace_id = ? AND local_date = ? AND trigger_type != 'manual'
		ORDER BY claimed_at DESC LIMIT 1
	`, workspaceID, localDate)
	if errors.Is(err, ErrRequestNotFound) {
		return nil, nil
	}
	return claim, err
}

func (s *SQLiteStore) scanClaim(ctx context.Context, query string, args ...any) (*GenerationRequest, error) {
	var req GenerationRequest
	var triggerType, status string
	var finishedAt sql.NullTime
	err := s.db.QueryRowContext(ctx, query, args...).Scan(
		&req.ID, &req.WorkspaceID, &req.LocalDate, &triggerType, &status, &req.RevisionID, &req.Error, &req.ClaimedAt, &finishedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrRequestNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get daily brief generation request: %w", err)
	}
	req.Trigger = Trigger(triggerType)
	req.Status = GenerationStatus(status)
	if finishedAt.Valid {
		req.FinishedAt = finishedAt.Time
	}
	return &req, nil
}

// ---- Revisions ----

func (s *SQLiteStore) NextRevisionNumber(ctx context.Context, workspaceID, localDate string) (int, error) {
	var maxNum sql.NullInt64
	err := s.db.QueryRowContext(ctx, `
		SELECT MAX(revision_number) FROM daily_brief_revision WHERE workspace_id = ? AND local_date = ?
	`, workspaceID, localDate).Scan(&maxNum)
	if err != nil {
		return 0, fmt.Errorf("failed to compute next revision number: %w", err)
	}
	if !maxNum.Valid {
		return 1, nil
	}
	return int(maxNum.Int64) + 1, nil
}

func (s *SQLiteStore) CreateRevision(ctx context.Context, rev *Revision) error {
	if rev.ID == "" {
		rev.ID = uuid.New().String()
	}
	now := time.Now().UTC()
	if rev.CreatedAt.IsZero() {
		rev.CreatedAt = now
	}
	var sourceStart, sourceEnd, generatedAt any
	if !rev.SourceWindowStart.IsZero() {
		sourceStart = rev.SourceWindowStart
	}
	if !rev.SourceWindowEnd.IsZero() {
		sourceEnd = rev.SourceWindowEnd
	}
	if !rev.GeneratedAt.IsZero() {
		generatedAt = rev.GeneratedAt
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO daily_brief_revision
			(id, workspace_id, user_id, local_date, revision_number, is_current, trigger_type, status,
			 config_revision, content_json, source_window_start, source_window_end, failure_reason, generated_at, created_at)
		VALUES (?, ?, ?, ?, ?, 0, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, rev.ID, rev.WorkspaceID, rev.UserID, rev.LocalDate, rev.RevisionNumber, string(rev.Trigger), string(rev.Status),
		rev.ConfigRevision, rev.ContentJSON, sourceStart, sourceEnd, rev.FailureReason, generatedAt, rev.CreatedAt)
	if err != nil {
		if isUniqueConstraintError(err) {
			return fmt.Errorf("revision %d for %s already exists: %w", rev.RevisionNumber, rev.LocalDate, err)
		}
		return fmt.Errorf("failed to create daily brief revision: %w", err)
	}
	return nil
}

func (s *SQLiteStore) SetCurrentRevision(ctx context.Context, workspaceID, revisionID string) error {
	return s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			UPDATE daily_brief_revision SET is_current = 0 WHERE workspace_id = ? AND is_current = 1
		`, workspaceID); err != nil {
			return fmt.Errorf("failed to clear prior current revision: %w", err)
		}
		res, err := tx.ExecContext(ctx, `
			UPDATE daily_brief_revision SET is_current = 1 WHERE id = ? AND workspace_id = ?
		`, revisionID, workspaceID)
		if err != nil {
			return fmt.Errorf("failed to set current revision: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrRevisionNotFound
		}
		return nil
	})
}

func (s *SQLiteStore) GetCurrentRevision(ctx context.Context, workspaceID string) (*Revision, error) {
	return s.scanRevision(ctx, `
		SELECT id, workspace_id, user_id, local_date, revision_number, is_current, trigger_type, status,
			config_revision, content_json, source_window_start, source_window_end, failure_reason, generated_at, created_at
		FROM daily_brief_revision WHERE workspace_id = ? AND is_current = 1
	`, workspaceID)
}

func (s *SQLiteStore) GetRevision(ctx context.Context, id string) (*Revision, error) {
	return s.scanRevision(ctx, `
		SELECT id, workspace_id, user_id, local_date, revision_number, is_current, trigger_type, status,
			config_revision, content_json, source_window_start, source_window_end, failure_reason, generated_at, created_at
		FROM daily_brief_revision WHERE id = ?
	`, id)
}

func (s *SQLiteStore) scanRevision(ctx context.Context, query string, args ...any) (*Revision, error) {
	var rev Revision
	var triggerType, status string
	var isCurrent int
	var sourceStart, sourceEnd, generatedAt sql.NullTime
	err := s.db.QueryRowContext(ctx, query, args...).Scan(
		&rev.ID, &rev.WorkspaceID, &rev.UserID, &rev.LocalDate, &rev.RevisionNumber, &isCurrent, &triggerType, &status,
		&rev.ConfigRevision, &rev.ContentJSON, &sourceStart, &sourceEnd, &rev.FailureReason, &generatedAt, &rev.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrRevisionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get daily brief revision: %w", err)
	}
	rev.Trigger = Trigger(triggerType)
	rev.Status = GenerationStatus(status)
	rev.IsCurrent = isCurrent != 0
	if sourceStart.Valid {
		rev.SourceWindowStart = sourceStart.Time
	}
	if sourceEnd.Valid {
		rev.SourceWindowEnd = sourceEnd.Time
	}
	if generatedAt.Valid {
		rev.GeneratedAt = generatedAt.Time
	}
	return &rev, nil
}

// ---- History & retention ----

func (s *SQLiteStore) ListHistory(ctx context.Context, workspaceID string, limit int) ([]HistorySummary, error) {
	if limit <= 0 {
		limit = 30
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT local_date,
			(SELECT id FROM daily_brief_revision r2
				WHERE r2.workspace_id = r.workspace_id AND r2.local_date = r.local_date AND r2.is_current = 1
				LIMIT 1) as current_id,
			COUNT(*) as revision_count,
			MAX(status) as status,
			MAX(generated_at) as generated_at
		FROM daily_brief_revision r
		WHERE workspace_id = ?
		GROUP BY local_date
		ORDER BY local_date DESC
		LIMIT ?
	`, workspaceID, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to list daily brief history: %w", err)
	}
	defer rows.Close()

	var out []HistorySummary
	for rows.Next() {
		var h HistorySummary
		var currentID sql.NullString
		var status string
		var generatedAt sql.NullString
		if err := rows.Scan(&h.LocalDate, &currentID, &h.RevisionCount, &status, &generatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan daily brief history row: %w", err)
		}
		h.CurrentRevisionID = currentID.String
		h.Status = GenerationStatus(status)
		if generatedAt.Valid {
			if t, err := time.Parse(time.RFC3339Nano, generatedAt.String); err == nil {
				h.GeneratedAt = t
			} else if t, err := time.Parse("2006-01-02 15:04:05.999999999-07:00", generatedAt.String); err == nil {
				h.GeneratedAt = t
			}
		}
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate daily brief history: %w", err)
	}
	return out, nil
}

func (s *SQLiteStore) PruneHistory(ctx context.Context, workspaceID string, keepDays int) error {
	if keepDays <= 0 {
		return nil
	}
	// Never delete the current revision, and never touch another workspace.
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM daily_brief_revision
		WHERE workspace_id = ?
		AND is_current = 0
		AND local_date NOT IN (
			SELECT local_date FROM (
				SELECT DISTINCT local_date FROM daily_brief_revision
				WHERE workspace_id = ?
				ORDER BY local_date DESC
				LIMIT ?
			)
		)
	`, workspaceID, workspaceID, keepDays)
	if err != nil {
		return fmt.Errorf("failed to prune daily brief history: %w", err)
	}
	return nil
}

// ---- Notifications ----

func (s *SQLiteStore) RecordNotification(ctx context.Context, revisionID, workspaceID string) (bool, error) {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO daily_brief_notification (revision_id, workspace_id, notified_at)
		VALUES (?, ?, ?)
		ON CONFLICT(revision_id) DO NOTHING
	`, revisionID, workspaceID, time.Now().UTC())
	if err != nil {
		return false, fmt.Errorf("failed to record daily brief notification: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("failed to check daily brief notification insert: %w", err)
	}
	return n > 0, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func emptySlice(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
