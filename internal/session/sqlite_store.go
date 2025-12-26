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
		INSERT INTO sessions (id, title, agent_name, folder_id, message_count, created_at, updated_at)
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

	var folderID sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT id, title, agent_name, folder_id, message_count, created_at, updated_at
		FROM sessions WHERE id = ?
	`, id).Scan(&session.ID, &session.Title, &session.AgentName, &folderID,
		&session.MessageCount, &session.CreatedAt, &session.UpdatedAt)

	if err == sql.ErrNoRows {
		return nil, ErrSessionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get session: %w", err)
	}

	session.FolderID = folderID.String

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
		SET title = ?, agent_name = ?, folder_id = NULLIF(?, ''),
		    message_count = ?, updated_at = ?
		WHERE id = ?
	`, session.Title, session.AgentName, session.FolderID,
		session.MessageCount, session.UpdatedAt, session.ID)

	if err != nil {
		return fmt.Errorf("failed to update session: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrSessionNotFound
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

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrSessionNotFound
	}

	return nil
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
		SELECT s.id, s.title, s.agent_name, s.folder_id, s.message_count, s.created_at, s.updated_at
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
		var folderID sql.NullString

		if err := rows.Scan(&item.ID, &item.Title, &item.AgentName, &folderID,
			&item.MessageCount, &item.CreatedAt, &item.UpdatedAt); err != nil {
			rows.Close()
			return nil, fmt.Errorf("failed to scan session: %w", err)
		}

		item.FolderID = folderID.String
		sessions = append(sessions, item)
	}
	rows.Close() // Close rows before making additional queries

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
	defer rows.Close()

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
		SELECT s.id, s.title, s.agent_name, s.folder_id, s.message_count,
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
		var folderID sql.NullString
		var snippet string

		if err := rows.Scan(&result.ID, &result.Title, &result.AgentName, &folderID,
			&result.MessageCount, &result.CreatedAt, &result.UpdatedAt, &snippet); err != nil {
			rows.Close()
			return nil, 0, fmt.Errorf("failed to scan search result: %w", err)
		}

		result.FolderID = folderID.String
		if snippet != "" {
			result.Snippets = []string{snippet}
		}

		results = append(results, result)
	}
	rows.Close() // Close rows before making additional queries

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
	defer rows.Close()

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

// CreateFolder creates a new folder.
func (s *SQLiteStore) CreateFolder(ctx context.Context, folder *Folder) error {
	if folder.ID == "" {
		return ErrInvalidID
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO folders (id, name, description, parent_id, color, session_count, created_at, updated_at)
		VALUES (?, ?, ?, NULLIF(?, ''), ?, ?, ?, ?)
	`, folder.ID, folder.Name, folder.Description, folder.ParentID, folder.Color,
		folder.SessionCount, folder.CreatedAt, folder.UpdatedAt)

	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint") {
			return ErrDuplicateID
		}
		return fmt.Errorf("failed to create folder: %w", err)
	}

	return nil
}

// GetFolder retrieves a folder by ID.
func (s *SQLiteStore) GetFolder(ctx context.Context, id string) (*Folder, error) {
	folder := &Folder{}

	var parentID sql.NullString
	var color sql.NullString
	var description sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT id, name, description, parent_id, color, session_count, created_at, updated_at
		FROM folders WHERE id = ?
	`, id).Scan(&folder.ID, &folder.Name, &description, &parentID, &color,
		&folder.SessionCount, &folder.CreatedAt, &folder.UpdatedAt)

	if err == sql.ErrNoRows {
		return nil, ErrFolderNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get folder: %w", err)
	}

	folder.Description = description.String
	folder.ParentID = parentID.String
	folder.Color = color.String

	return folder, nil
}

// UpdateFolder updates folder metadata.
func (s *SQLiteStore) UpdateFolder(ctx context.Context, folder *Folder) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE folders
		SET name = ?, description = ?, parent_id = NULLIF(?, ''), color = ?, updated_at = ?
		WHERE id = ?
	`, folder.Name, folder.Description, folder.ParentID, folder.Color, folder.UpdatedAt, folder.ID)

	if err != nil {
		return fmt.Errorf("failed to update folder: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrFolderNotFound
	}

	return nil
}

// DeleteFolder removes a folder, moving sessions and subfolders to root.
func (s *SQLiteStore) DeleteFolder(ctx context.Context, id string) error {
	return s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		// Check folder exists
		var exists bool
		err := tx.QueryRowContext(ctx, "SELECT 1 FROM folders WHERE id = ?", id).Scan(&exists)
		if err == sql.ErrNoRows {
			return ErrFolderNotFound
		}
		if err != nil {
			return fmt.Errorf("failed to check folder: %w", err)
		}

		// Move sessions to root
		_, err = tx.ExecContext(ctx, "UPDATE sessions SET folder_id = NULL WHERE folder_id = ?", id)
		if err != nil {
			return fmt.Errorf("failed to move sessions: %w", err)
		}

		// Move subfolders to root
		_, err = tx.ExecContext(ctx, "UPDATE folders SET parent_id = NULL WHERE parent_id = ?", id)
		if err != nil {
			return fmt.Errorf("failed to move subfolders: %w", err)
		}

		// Delete the folder
		_, err = tx.ExecContext(ctx, "DELETE FROM folders WHERE id = ?", id)
		if err != nil {
			return fmt.Errorf("failed to delete folder: %w", err)
		}

		return nil
	})
}

// ListFolders returns all folders as a flat list.
func (s *SQLiteStore) ListFolders(ctx context.Context) ([]Folder, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, description, parent_id, color, session_count, created_at, updated_at
		FROM folders
		ORDER BY name ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to list folders: %w", err)
	}
	defer rows.Close()

	folders := make([]Folder, 0)
	for rows.Next() {
		var folder Folder
		var parentID, color, description sql.NullString

		if err := rows.Scan(&folder.ID, &folder.Name, &description, &parentID, &color,
			&folder.SessionCount, &folder.CreatedAt, &folder.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan folder: %w", err)
		}

		folder.Description = description.String
		folder.ParentID = parentID.String
		folder.Color = color.String
		folders = append(folders, folder)
	}

	return folders, nil
}

// GetFolderTree returns folders organized as a tree structure.
func (s *SQLiteStore) GetFolderTree(ctx context.Context) ([]Folder, error) {
	folders, err := s.ListFolders(ctx)
	if err != nil {
		return nil, err
	}

	// Build lookup map
	folderMap := make(map[string]*Folder)
	for i := range folders {
		folders[i].Children = []Folder{} // Initialize children slice
		folderMap[folders[i].ID] = &folders[i]
	}

	// Build tree
	roots := make([]Folder, 0)
	for i := range folders {
		folder := &folders[i]
		if folder.ParentID == "" {
			roots = append(roots, *folder)
		} else if parent, ok := folderMap[folder.ParentID]; ok {
			parent.Children = append(parent.Children, *folder)
		} else {
			// Orphaned folder - treat as root
			roots = append(roots, *folder)
		}
	}

	return roots, nil
}

// GetSubfolderIDs returns all descendant folder IDs.
func (s *SQLiteStore) GetSubfolderIDs(ctx context.Context, folderID string) ([]string, error) {
	// Use recursive CTE to get all descendants
	rows, err := s.db.QueryContext(ctx, `
		WITH RECURSIVE descendants AS (
			SELECT id FROM folders WHERE parent_id = ?
			UNION ALL
			SELECT f.id FROM folders f
			INNER JOIN descendants d ON f.parent_id = d.id
		)
		SELECT id FROM descendants
	`, folderID)
	if err != nil {
		return nil, fmt.Errorf("failed to get subfolder IDs: %w", err)
	}
	defer rows.Close()

	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("failed to scan subfolder ID: %w", err)
		}
		ids = append(ids, id)
	}

	return ids, nil
}

// Helper methods

func (s *SQLiteStore) getSessionTags(ctx context.Context, sessionID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT tag FROM session_tags WHERE session_id = ?", sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

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

func (s *SQLiteStore) buildWhereClause(filter *SessionFilter) (string, []interface{}) {
	if filter == nil {
		return "", nil
	}

	conditions := []string{}
	args := []interface{}{}

	if filter.AgentName != "" {
		conditions = append(conditions, "s.agent_name = ?")
		args = append(args, filter.AgentName)
	}

	if filter.FolderID != nil {
		if *filter.FolderID == "" {
			conditions = append(conditions, "s.folder_id IS NULL")
		} else {
			conditions = append(conditions, "s.folder_id = ?")
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

// ============================================================================
// Folder Note Operations
// ============================================================================

// CreateNote creates a new folder note in the database.
func (s *SQLiteStore) CreateNote(ctx context.Context, note *FolderNote) error {
	if note.ID == "" {
		return ErrInvalidID
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO folder_notes (id, folder_id, name, content, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, note.ID, note.FolderID, note.Name, note.Content, note.CreatedAt, note.UpdatedAt)

	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint") {
			return ErrDuplicateID
		}
		if strings.Contains(err.Error(), "FOREIGN KEY constraint") {
			return ErrFolderNotFound
		}
		return fmt.Errorf("failed to create note: %w", err)
	}

	return nil
}

// GetNote retrieves a note by ID.
func (s *SQLiteStore) GetNote(ctx context.Context, id string) (*FolderNote, error) {
	note := &FolderNote{}

	err := s.db.QueryRowContext(ctx, `
		SELECT id, folder_id, name, content, created_at, updated_at
		FROM folder_notes WHERE id = ?
	`, id).Scan(&note.ID, &note.FolderID, &note.Name, &note.Content,
		&note.CreatedAt, &note.UpdatedAt)

	if err == sql.ErrNoRows {
		return nil, ErrNoteNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get note: %w", err)
	}

	return note, nil
}

// UpdateNote updates note metadata and content.
func (s *SQLiteStore) UpdateNote(ctx context.Context, note *FolderNote) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE folder_notes
		SET name = ?, content = ?, folder_id = ?, updated_at = ?
		WHERE id = ?
	`, note.Name, note.Content, note.FolderID, note.UpdatedAt, note.ID)

	if err != nil {
		return fmt.Errorf("failed to update note: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrNoteNotFound
	}

	return nil
}

// DeleteNote removes a note.
func (s *SQLiteStore) DeleteNote(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, "DELETE FROM folder_notes WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("failed to delete note: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrNoteNotFound
	}

	return nil
}

// ListNotesByFolder returns all notes in a folder.
func (s *SQLiteStore) ListNotesByFolder(ctx context.Context, folderID string) ([]FolderNoteListItem, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, folder_id, name,
		       CASE WHEN LENGTH(content) > 100 THEN SUBSTR(content, 1, 100) || '...' ELSE content END as preview,
		       created_at, updated_at
		FROM folder_notes
		WHERE folder_id = ?
		ORDER BY updated_at DESC
	`, folderID)
	if err != nil {
		return nil, fmt.Errorf("failed to list notes: %w", err)
	}
	defer rows.Close()

	notes := make([]FolderNoteListItem, 0)
	for rows.Next() {
		var note FolderNoteListItem
		if err := rows.Scan(&note.ID, &note.FolderID, &note.Name, &note.Preview,
			&note.CreatedAt, &note.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan note: %w", err)
		}
		notes = append(notes, note)
	}

	return notes, nil
}

// SearchNotes performs full-text search across note names and content.
func (s *SQLiteStore) SearchNotes(ctx context.Context, query string, limit int) ([]NoteSearchResult, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT n.id, n.folder_id, n.name,
		       CASE WHEN LENGTH(n.content) > 100 THEN SUBSTR(n.content, 1, 100) || '...' ELSE n.content END as preview,
		       n.created_at, n.updated_at,
		       f.name as folder_name,
		       snippet(folder_notes_fts, 2, '<mark>', '</mark>', '...', 32) as snippet
		FROM folder_notes n
		INNER JOIN folder_notes_fts fts ON n.id = fts.note_id
		LEFT JOIN folders f ON n.folder_id = f.id
		WHERE folder_notes_fts MATCH ?
		ORDER BY rank
		LIMIT ?
	`, query, limit)
	if err != nil {
		// If FTS match fails (e.g., empty query), return empty results
		if strings.Contains(err.Error(), "fts5") {
			return []NoteSearchResult{}, nil
		}
		return nil, fmt.Errorf("failed to search notes: %w", err)
	}
	defer rows.Close()

	results := make([]NoteSearchResult, 0)
	for rows.Next() {
		var result NoteSearchResult
		var folderName sql.NullString
		var snippet string

		if err := rows.Scan(&result.ID, &result.FolderID, &result.Name, &result.Preview,
			&result.CreatedAt, &result.UpdatedAt, &folderName, &snippet); err != nil {
			return nil, fmt.Errorf("failed to scan search result: %w", err)
		}

		result.FolderName = folderName.String
		if snippet != "" {
			result.Snippets = []string{snippet}
		}

		results = append(results, result)
	}

	return results, nil
}
