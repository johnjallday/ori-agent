package session

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
)

// SQLiteStore implements SessionStore and FolderStore using SQLite.
type SQLiteStore struct {
	db *database.DB
}

// NewSQLiteStore creates a new SQLite-backed session store.
func NewSQLiteStore(db *database.DB) *SQLiteStore {
	return &SQLiteStore{db: db}
}

// CreateSession creates a new session in the database.
func (s *SQLiteStore) CreateSession(ctx context.Context, session *Session) error {
	if session.ID == "" {
		return ErrInvalidID
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sessions (id, title, agent_name, workspace_id, message_count, created_at, updated_at)
		VALUES (?, ?, ?, NULLIF(?, ''), ?, ?, ?)
	`, session.ID, session.Title, session.AgentName, session.FolderID,
		session.MessageCount, session.CreatedAt, session.UpdatedAt)

	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint") {
			return ErrDuplicateID
		}
		return fmt.Errorf("failed to create session: %w", err)
	}

	// Insert tags if any
	if len(session.Tags) > 0 {
		if err := s.updateTagsInternal(ctx, session.ID, session.Tags); err != nil {
			return fmt.Errorf("failed to insert tags: %w", err)
		}
	}

	return nil
}

// GetSession retrieves a session by ID including all messages.
func (s *SQLiteStore) GetSession(ctx context.Context, id string) (*Session, error) {
	session := &Session{}

	var workspaceID sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT id, title, agent_name, workspace_id, message_count, created_at, updated_at
		FROM sessions WHERE id = ?
	`, id).Scan(&session.ID, &session.Title, &session.AgentName, &workspaceID,
		&session.MessageCount, &session.CreatedAt, &session.UpdatedAt)

	if err == sql.ErrNoRows {
		return nil, ErrSessionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get session: %w", err)
	}

	session.FolderID = workspaceID.String

	// Get tags
	tags, err := s.getSessionTags(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("failed to get session tags: %w", err)
	}
	session.Tags = tags

	// Get messages
	messages, err := s.GetMessages(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("failed to get messages: %w", err)
	}
	session.Messages = messages

	return session, nil
}

// UpdateSession updates session metadata.
func (s *SQLiteStore) UpdateSession(ctx context.Context, session *Session) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE sessions
		SET title = ?, agent_name = ?, workspace_id = NULLIF(?, ''),
		    message_count = ?, updated_at = ?
		WHERE id = ?
	`, session.Title, session.AgentName, session.FolderID,
		session.MessageCount, session.UpdatedAt, session.ID)

	if err != nil {
		return fmt.Errorf("failed to update session: %w", err)
	}

	if err := database.CheckRowsAffectedWithError(result, "session", ErrSessionNotFound); err != nil {
		return err
	}

	// Update tags
	if err := s.updateTagsInternal(ctx, session.ID, session.Tags); err != nil {
		return fmt.Errorf("failed to update tags: %w", err)
	}

	return nil
}

// DeleteSession removes a session and all its messages.
func (s *SQLiteStore) DeleteSession(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("failed to delete session: %w", err)
	}

	if err := database.CheckRowsAffectedWithError(result, "session", ErrSessionNotFound); err != nil {
		return err
	}

	return nil
}

// DeleteSessionsByAgent removes every session whose agent_name matches the
// given name. Dependent rows in messages, tool_calls, session_tags,
// session_review_status, and review_issues are removed by ON DELETE CASCADE
// foreign keys. Returns the number of sessions removed.
func (s *SQLiteStore) DeleteSessionsByAgent(ctx context.Context, agentName string) (int, error) {
	result, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE agent_name = ?", agentName)
	if err != nil {
		return 0, fmt.Errorf("failed to delete sessions for agent %q: %w", agentName, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to read rows affected: %w", err)
	}
	return int(affected), nil
}

// ListSessions returns sessions matching the filter with pagination.
func (s *SQLiteStore) ListSessions(ctx context.Context, filter *SessionFilter, opts *ListOptions) (*ListResult, error) {
	if opts == nil {
		opts = &ListOptions{Limit: 50, Sort: SortByUpdatedDesc}
	}
	if opts.Limit <= 0 || opts.Limit > 100 {
		opts.Limit = 50
	}
	if opts.Sort == "" {
		opts.Sort = SortByUpdatedDesc
	}

	// Build query with filters
	whereClause, args := s.buildWhereClause(filter)

	// Count total matching
	countQuery := "SELECT COUNT(*) FROM sessions s " + whereClause
	var total int
	if err := s.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, fmt.Errorf("failed to count sessions: %w", err)
	}

	// Get paginated results
	orderBy := s.getOrderBy(opts.Sort)
	query := fmt.Sprintf(`
		SELECT s.id, s.title, s.agent_name, s.workspace_id, s.message_count, s.created_at, s.updated_at
		FROM sessions s
		%s
		ORDER BY %s
		LIMIT ? OFFSET ?
	`, whereClause, orderBy)

	args = append(args, opts.Limit, opts.Offset)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list sessions: %w", err)
	}

	// Collect all sessions first, then close rows before making more queries
	sessions := make([]SessionListItem, 0)
	for rows.Next() {
		var item SessionListItem
		var workspaceID sql.NullString

		if err := rows.Scan(&item.ID, &item.Title, &item.AgentName, &workspaceID,
			&item.MessageCount, &item.CreatedAt, &item.UpdatedAt); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("failed to scan session: %w", err)
		}

		item.FolderID = workspaceID.String
		sessions = append(sessions, item)
	}
	_ = rows.Close() // Close rows before making additional queries

	// Now fetch tags and previews for each session
	for i := range sessions {
		tags, _ := s.getSessionTags(ctx, sessions[i].ID)
		sessions[i].Tags = tags
		sessions[i].Preview = s.getSessionPreview(ctx, sessions[i].ID)
	}

	return &ListResult{
		Sessions: sessions,
		Total:    total,
		HasMore:  opts.Offset+len(sessions) < total,
	}, nil
}

// AddMessage appends a message to a session.
func (s *SQLiteStore) AddMessage(ctx context.Context, sessionID string, message *Message) error {
	// Check session exists
	var exists bool
	err := s.db.QueryRowContext(ctx, "SELECT 1 FROM sessions WHERE id = ?", sessionID).Scan(&exists)
	if err == sql.ErrNoRows {
		return ErrSessionNotFound
	}
	if err != nil {
		return fmt.Errorf("failed to check session: %w", err)
	}

	return s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		// Insert message
		_, err := tx.ExecContext(ctx, `
			INSERT INTO messages (id, session_id, role, content, model, tokens_used, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)
		`, message.ID, sessionID, message.Role, message.Content,
			message.Model, message.TokensUsed, message.CreatedAt)
		if err != nil {
			return fmt.Errorf("failed to insert message: %w", err)
		}

		// Update session
		_, err = tx.ExecContext(ctx, `
			UPDATE sessions
			SET message_count = message_count + 1, updated_at = ?
			WHERE id = ?
		`, time.Now(), sessionID)
		if err != nil {
			return fmt.Errorf("failed to update session: %w", err)
		}

		// Update FTS content with message text
		_, err = tx.ExecContext(ctx, `
			UPDATE sessions_fts
			SET content = content || ' ' || ?
			WHERE session_id = ?
		`, message.Content, sessionID)
		if err != nil {
			return fmt.Errorf("failed to update FTS: %w", err)
		}

		return nil
	})
}

// GetMessages retrieves all messages for a session.
func (s *SQLiteStore) GetMessages(ctx context.Context, sessionID string) ([]Message, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, session_id, role, content, model, tokens_used, created_at
		FROM messages
		WHERE session_id = ?
		ORDER BY created_at ASC
	`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to get messages: %w", err)
	}
	defer func() { _ = rows.Close() }()

	messages := make([]Message, 0)
	for rows.Next() {
		var msg Message
		var model sql.NullString

		if err := rows.Scan(&msg.ID, &msg.SessionID, &msg.Role, &msg.Content,
			&model, &msg.TokensUsed, &msg.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan message: %w", err)
		}

		msg.Model = model.String
		messages = append(messages, msg)
	}

	return messages, nil
}

// Search performs full-text search across session titles and message content.
func (s *SQLiteStore) Search(ctx context.Context, query string, filter *SessionFilter, opts *ListOptions) ([]SearchResult, int, error) {
	if opts == nil {
		opts = &ListOptions{Limit: 50}
	}
	if opts.Limit <= 0 || opts.Limit > 100 {
		opts.Limit = 50
	}

	// Build filter clause
	whereClause, filterArgs := s.buildWhereClause(filter)

	// Full-text search query
	// We join with the FTS table and add ranking
	countQuery := fmt.Sprintf(`
		SELECT COUNT(DISTINCT s.id)
		FROM sessions s
		INNER JOIN sessions_fts fts ON s.id = fts.session_id
		%s
		AND sessions_fts MATCH ?
	`, strings.Replace(whereClause, "WHERE", "WHERE 1=1 AND", 1))

	// Add query for FTS
	allArgs := append(filterArgs, query)

	var total int
	if err := s.db.QueryRowContext(ctx, countQuery, allArgs...).Scan(&total); err != nil {
		// If FTS match fails (e.g., empty query), return empty results
		if strings.Contains(err.Error(), "fts5") {
			return []SearchResult{}, 0, nil
		}
		return nil, 0, fmt.Errorf("failed to count search results: %w", err)
	}

	// Get search results with snippets
	searchQuery := fmt.Sprintf(`
		SELECT s.id, s.title, s.agent_name, s.workspace_id, s.message_count,
		       s.created_at, s.updated_at,
		       snippet(sessions_fts, 2, '<mark>', '</mark>', '...', 32) as snippet
		FROM sessions s
		INNER JOIN sessions_fts fts ON s.id = fts.session_id
		%s
		AND sessions_fts MATCH ?
		ORDER BY rank
		LIMIT ? OFFSET ?
	`, strings.Replace(whereClause, "WHERE", "WHERE 1=1 AND", 1))

	allArgs = append(allArgs, opts.Limit, opts.Offset)
	rows, err := s.db.QueryContext(ctx, searchQuery, allArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to search sessions: %w", err)
	}

	// Collect all results first, then close rows before making more queries
	results := make([]SearchResult, 0)
	for rows.Next() {
		var result SearchResult
		var workspaceID sql.NullString
		var snippet string

		if err := rows.Scan(&result.ID, &result.Title, &result.AgentName, &workspaceID,
			&result.MessageCount, &result.CreatedAt, &result.UpdatedAt, &snippet); err != nil {
			_ = rows.Close()
			return nil, 0, fmt.Errorf("failed to scan search result: %w", err)
		}

		result.FolderID = workspaceID.String
		if snippet != "" {
			result.Snippets = []string{snippet}
		}

		results = append(results, result)
	}
	_ = rows.Close() // Close rows before making additional queries

	// Fetch tags for each result
	for i := range results {
		results[i].Tags, _ = s.getSessionTags(ctx, results[i].ID)
	}

	return results, total, nil
}

// UpdateTags replaces all tags for a session.
func (s *SQLiteStore) UpdateTags(ctx context.Context, sessionID string, tags []string) error {
	// Check session exists
	var exists bool
	err := s.db.QueryRowContext(ctx, "SELECT 1 FROM sessions WHERE id = ?", sessionID).Scan(&exists)
	if err == sql.ErrNoRows {
		return ErrSessionNotFound
	}
	if err != nil {
		return fmt.Errorf("failed to check session: %w", err)
	}

	return s.updateTagsInternal(ctx, sessionID, tags)
}

// GetAllTags returns all unique tags with usage counts.
func (s *SQLiteStore) GetAllTags(ctx context.Context) ([]Tag, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT name, usage_count FROM tag_counts ORDER BY usage_count DESC")
	if err != nil {
		return nil, fmt.Errorf("failed to get tags: %w", err)
	}
	defer func() { _ = rows.Close() }()

	tags := make([]Tag, 0)
	for rows.Next() {
		var tag Tag
		if err := rows.Scan(&tag.Name, &tag.UsageCount); err != nil {
			return nil, fmt.Errorf("failed to scan tag: %w", err)
		}
		tags = append(tags, tag)
	}

	return tags, nil
}

// Helper methods

func (s *SQLiteStore) getSessionTags(ctx context.Context, sessionID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT tag FROM session_tags WHERE session_id = ?", sessionID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	tags := make([]string, 0)
	for rows.Next() {
		var tag string
		if err := rows.Scan(&tag); err != nil {
			return nil, err
		}
		tags = append(tags, tag)
	}

	return tags, nil
}

func (s *SQLiteStore) getSessionPreview(ctx context.Context, sessionID string) string {
	var content string
	_ = s.db.QueryRowContext(ctx, `
		SELECT SUBSTR(content, 1, 100)
		FROM messages
		WHERE session_id = ?
		ORDER BY created_at ASC
		LIMIT 1
	`, sessionID).Scan(&content)

	return content
}

// RenameSessionTag renames a tag across all sessions, merging when the new
// name already exists on a session. Returns the number of affected sessions.
func (s *SQLiteStore) RenameSessionTag(ctx context.Context, from, to string) (int, error) {
	affected := 0
	err := s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx,
			"SELECT COUNT(DISTINCT session_id) FROM session_tags WHERE tag = ?", from,
		).Scan(&affected); err != nil {
			return err
		}
		if affected == 0 {
			return nil
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT OR IGNORE INTO session_tags (session_id, tag) SELECT session_id, ? FROM session_tags WHERE tag = ?",
			to, from,
		); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "DELETE FROM session_tags WHERE tag = ?", from)
		return err
	})
	if err != nil {
		return 0, fmt.Errorf("failed to rename session tag: %w", err)
	}
	return affected, nil
}

// RemoveSessionTag removes a tag from all sessions. Returns the number of
// affected sessions.
func (s *SQLiteStore) RemoveSessionTag(ctx context.Context, tag string) (int, error) {
	result, err := s.db.ExecContext(ctx, "DELETE FROM session_tags WHERE tag = ?", tag)
	if err != nil {
		return 0, fmt.Errorf("failed to remove session tag: %w", err)
	}
	affected, _ := result.RowsAffected()
	return int(affected), nil
}

func (s *SQLiteStore) updateTagsInternal(ctx context.Context, sessionID string, tags []string) error {
	return s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		// Delete existing tags
		_, err := tx.ExecContext(ctx, "DELETE FROM session_tags WHERE session_id = ?", sessionID)
		if err != nil {
			return err
		}

		// Insert new tags (normalized)
		for _, tag := range tags {
			normalizedTag := strings.ToLower(strings.TrimSpace(tag))
			if normalizedTag == "" {
				continue
			}

			_, err := tx.ExecContext(ctx,
				"INSERT INTO session_tags (session_id, tag) VALUES (?, ?)",
				sessionID, normalizedTag)
			if err != nil {
				return err
			}
		}

		return nil
	})
}

func (s *SQLiteStore) buildWhereClause(filter *SessionFilter) (string, []any) {
	if filter == nil {
		return "", nil
	}

	conditions := []string{}
	args := []any{}

	if filter.AgentName != "" {
		conditions = append(conditions, "s.agent_name = ?")
		args = append(args, filter.AgentName)
	}

	if filter.FolderID != nil {
		if *filter.FolderID == "" {
			conditions = append(conditions, "s.workspace_id IS NULL")
		} else {
			conditions = append(conditions, "s.workspace_id = ?")
			args = append(args, *filter.FolderID)
		}
	}

	if filter.CreatedAfter != nil {
		conditions = append(conditions, "s.created_at >= ?")
		args = append(args, *filter.CreatedAfter)
	}

	if filter.CreatedBefore != nil {
		conditions = append(conditions, "s.created_at <= ?")
		args = append(args, *filter.CreatedBefore)
	}

	if filter.UpdatedAfter != nil {
		conditions = append(conditions, "s.updated_at >= ?")
		args = append(args, *filter.UpdatedAfter)
	}

	if filter.UpdatedBefore != nil {
		conditions = append(conditions, "s.updated_at <= ?")
		args = append(args, *filter.UpdatedBefore)
	}

	// Tags with AND logic
	if len(filter.Tags) > 0 {
		placeholders := make([]string, len(filter.Tags))
		for i, tag := range filter.Tags {
			placeholders[i] = "?"
			args = append(args, strings.ToLower(strings.TrimSpace(tag)))
		}
		conditions = append(conditions, fmt.Sprintf(`
			s.id IN (
				SELECT session_id FROM session_tags
				WHERE tag IN (%s)
				GROUP BY session_id
				HAVING COUNT(DISTINCT tag) = %d
			)
		`, strings.Join(placeholders, ","), len(filter.Tags)))
	}

	// Tags with OR logic
	if len(filter.AnyTags) > 0 {
		placeholders := make([]string, len(filter.AnyTags))
		for i, tag := range filter.AnyTags {
			placeholders[i] = "?"
			args = append(args, strings.ToLower(strings.TrimSpace(tag)))
		}
		conditions = append(conditions, fmt.Sprintf(`
			s.id IN (
				SELECT session_id FROM session_tags
				WHERE tag IN (%s)
			)
		`, strings.Join(placeholders, ",")))
	}

	if len(conditions) == 0 {
		return "", nil
	}

	return "WHERE " + strings.Join(conditions, " AND "), args
}

func (s *SQLiteStore) getOrderBy(sort SessionSort) string {
	switch sort {
	case SortByUpdatedAsc:
		return "s.updated_at ASC"
	case SortByCreatedDesc:
		return "s.created_at DESC"
	case SortByCreatedAsc:
		return "s.created_at ASC"
	case SortByTitleAsc:
		return "s.title ASC"
	case SortByTitleDesc:
		return "s.title DESC"
	default:
		return "s.updated_at DESC"
	}
}
