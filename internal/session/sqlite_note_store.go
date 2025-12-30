package session

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/database"
)

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

	if err := database.CheckRowsAffectedWithError(result, "note", ErrNoteNotFound); err != nil {
		return err
	}

	return nil
}

// DeleteNote removes a note.
func (s *SQLiteStore) DeleteNote(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, "DELETE FROM folder_notes WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("failed to delete note: %w", err)
	}

	if err := database.CheckRowsAffectedWithError(result, "note", ErrNoteNotFound); err != nil {
		return err
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
	defer func() { _ = rows.Close() }()

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
	defer func() { _ = rows.Close() }()

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
