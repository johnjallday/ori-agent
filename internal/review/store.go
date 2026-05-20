package review

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/database"
)

// Store provides persistence for review-related data.
type Store interface {
	// Issue operations
	AddIssue(ctx context.Context, issue *Issue) error
	GetIssues(ctx context.Context, opts IssueQueryOptions) ([]Issue, error)
	GetIssueByHash(ctx context.Context, sessionID, contentHash string) (*Issue, error)

	// ReviewRun operations
	CreateReviewRun(ctx context.Context) (*ReviewRun, error)
	UpdateReviewRun(ctx context.Context, run *ReviewRun) error
	GetReviewRun(ctx context.Context, id string) (*ReviewRun, error)
	GetReviewRuns(ctx context.Context, limit int) ([]ReviewRun, error)

	// SessionReviewStatus operations
	GetSessionReviewStatus(ctx context.Context, sessionID string) (*SessionReviewStatus, error)
	UpdateSessionReviewStatus(ctx context.Context, status *SessionReviewStatus) error
}

// IssueQueryOptions defines filtering options for querying issues.
type IssueQueryOptions struct {
	AgentName string
	SessionID string
	IssueType IssueType
	Since     time.Time
	Limit     int
}

// SQLiteStore implements Store using SQLite.
type SQLiteStore struct {
	db *database.DB
}

// NewSQLiteStore creates a new SQLite-based store.
func NewSQLiteStore(db *database.DB) *SQLiteStore {
	return &SQLiteStore{db: db}
}

// AddIssue stores a new review issue.
func (s *SQLiteStore) AddIssue(ctx context.Context, issue *Issue) error {
	if issue.ID == "" {
		issue.ID = uuid.New().String()
	}
	if issue.CreatedAt.IsZero() {
		issue.CreatedAt = time.Now()
	}

	query := `
		INSERT INTO review_issues (
			id, session_id, agent_name, issue_type, tool_name,
			occurrence_count, first_message_id, last_message_id,
			context_summary, content_hash, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	_, err := s.db.ExecContext(ctx, query,
		issue.ID,
		issue.SessionID,
		issue.AgentName,
		string(issue.Type),
		issue.ToolName,
		issue.OccurrenceCount,
		issue.FirstMessageID,
		issue.LastMessageID,
		issue.ContextSummary,
		issue.ContentHash,
		issue.CreatedAt,
	)
	return err
}

// GetIssues retrieves issues matching the given options.
func (s *SQLiteStore) GetIssues(ctx context.Context, opts IssueQueryOptions) ([]Issue, error) {
	query := `
		SELECT id, session_id, agent_name, issue_type, tool_name,
			occurrence_count, first_message_id, last_message_id,
			context_summary, content_hash, created_at
		FROM review_issues
		WHERE 1=1
	`
	args := []any{}

	if opts.AgentName != "" {
		query += " AND agent_name = ?"
		args = append(args, opts.AgentName)
	}
	if opts.SessionID != "" {
		query += " AND session_id = ?"
		args = append(args, opts.SessionID)
	}
	if opts.IssueType != "" {
		query += " AND issue_type = ?"
		args = append(args, string(opts.IssueType))
	}
	if !opts.Since.IsZero() {
		query += " AND created_at >= ?"
		args = append(args, opts.Since)
	}

	query += " ORDER BY created_at DESC"

	if opts.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, opts.Limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var issues []Issue
	for rows.Next() {
		var issue Issue
		var issueType string
		err := rows.Scan(
			&issue.ID,
			&issue.SessionID,
			&issue.AgentName,
			&issueType,
			&issue.ToolName,
			&issue.OccurrenceCount,
			&issue.FirstMessageID,
			&issue.LastMessageID,
			&issue.ContextSummary,
			&issue.ContentHash,
			&issue.CreatedAt,
		)
		if err != nil {
			return nil, err
		}
		issue.Type = IssueType(issueType)
		issues = append(issues, issue)
	}

	return issues, rows.Err()
}

// GetIssueByHash finds an existing issue by session and content hash (for deduplication).
func (s *SQLiteStore) GetIssueByHash(ctx context.Context, sessionID, contentHash string) (*Issue, error) {
	query := `
		SELECT id, session_id, agent_name, issue_type, tool_name,
			occurrence_count, first_message_id, last_message_id,
			context_summary, content_hash, created_at
		FROM review_issues
		WHERE session_id = ? AND content_hash = ?
		LIMIT 1
	`

	var issue Issue
	var issueType string
	err := s.db.QueryRowContext(ctx, query, sessionID, contentHash).Scan(
		&issue.ID,
		&issue.SessionID,
		&issue.AgentName,
		&issueType,
		&issue.ToolName,
		&issue.OccurrenceCount,
		&issue.FirstMessageID,
		&issue.LastMessageID,
		&issue.ContextSummary,
		&issue.ContentHash,
		&issue.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	issue.Type = IssueType(issueType)
	return &issue, nil
}

// CreateReviewRun creates a new review run and returns it.
func (s *SQLiteStore) CreateReviewRun(ctx context.Context) (*ReviewRun, error) {
	run := &ReviewRun{
		ID:        uuid.New().String(),
		StartedAt: time.Now(),
		Status:    ReviewRunStatusRunning,
	}

	query := `
		INSERT INTO review_runs (id, started_at, completed_at, sessions_reviewed, issues_found, status, error_message)
		VALUES (?, ?, NULL, 0, 0, ?, '')
	`

	_, err := s.db.ExecContext(ctx, query, run.ID, run.StartedAt, string(ReviewRunStatusRunning))
	if err != nil {
		return nil, err
	}
	return run, nil
}

// UpdateReviewRun updates an existing review run.
func (s *SQLiteStore) UpdateReviewRun(ctx context.Context, run *ReviewRun) error {
	query := `
		UPDATE review_runs
		SET completed_at = ?, sessions_reviewed = ?, issues_found = ?, status = ?, error_message = ?
		WHERE id = ?
	`

	var completedAt any
	if !run.CompletedAt.IsZero() {
		completedAt = run.CompletedAt
	}

	_, err := s.db.ExecContext(ctx, query,
		completedAt,
		run.SessionsReviewed,
		run.IssuesFound,
		string(run.Status),
		run.ErrorMessage,
		run.ID,
	)
	return err
}

// GetReviewRun retrieves a review run by ID.
func (s *SQLiteStore) GetReviewRun(ctx context.Context, id string) (*ReviewRun, error) {
	query := `
		SELECT id, started_at, completed_at, sessions_reviewed, issues_found, status, error_message
		FROM review_runs
		WHERE id = ?
	`

	var run ReviewRun
	var completedAt sql.NullTime
	var status string

	err := s.db.QueryRowContext(ctx, query, id).Scan(
		&run.ID,
		&run.StartedAt,
		&completedAt,
		&run.SessionsReviewed,
		&run.IssuesFound,
		&status,
		&run.ErrorMessage,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	if completedAt.Valid {
		run.CompletedAt = completedAt.Time
	}
	run.Status = ReviewRunStatus(status)
	return &run, nil
}

// GetReviewRuns retrieves the most recent review runs.
func (s *SQLiteStore) GetReviewRuns(ctx context.Context, limit int) ([]ReviewRun, error) {
	query := `
		SELECT id, started_at, completed_at, sessions_reviewed, issues_found, status, error_message
		FROM review_runs
		ORDER BY started_at DESC
		LIMIT ?
	`

	rows, err := s.db.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var runs []ReviewRun
	for rows.Next() {
		var run ReviewRun
		var completedAt sql.NullTime
		var status string

		err := rows.Scan(
			&run.ID,
			&run.StartedAt,
			&completedAt,
			&run.SessionsReviewed,
			&run.IssuesFound,
			&status,
			&run.ErrorMessage,
		)
		if err != nil {
			return nil, err
		}

		if completedAt.Valid {
			run.CompletedAt = completedAt.Time
		}
		run.Status = ReviewRunStatus(status)
		runs = append(runs, run)
	}

	return runs, rows.Err()
}

// GetSessionReviewStatus retrieves the review status for a session.
func (s *SQLiteStore) GetSessionReviewStatus(ctx context.Context, sessionID string) (*SessionReviewStatus, error) {
	query := `
		SELECT session_id, last_reviewed_at, last_message_id
		FROM session_review_status
		WHERE session_id = ?
	`

	var status SessionReviewStatus
	err := s.db.QueryRowContext(ctx, query, sessionID).Scan(
		&status.SessionID,
		&status.LastReviewedAt,
		&status.LastMessageID,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &status, nil
}

// UpdateSessionReviewStatus creates or updates the review status for a session.
func (s *SQLiteStore) UpdateSessionReviewStatus(ctx context.Context, status *SessionReviewStatus) error {
	query := `
		INSERT INTO session_review_status (session_id, last_reviewed_at, last_message_id)
		VALUES (?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET
			last_reviewed_at = excluded.last_reviewed_at,
			last_message_id = excluded.last_message_id
	`

	_, err := s.db.ExecContext(ctx, query,
		status.SessionID,
		status.LastReviewedAt,
		status.LastMessageID,
	)
	return err
}
