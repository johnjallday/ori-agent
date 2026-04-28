package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/vaultref"
)

// ============================================================================
// Workspace Note Operations
// ============================================================================

// CreateNote creates a new workspace note in the database.
func (s *SQLiteStore) CreateNote(ctx context.Context, note *WorkspaceNote) error {
	if note.ID == "" {
		return ErrInvalidID
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO workspace_notes (id, workspace_id, name, content, vault_reference_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, note.ID, note.WorkspaceID, note.Name, note.Content, encodeNoteVaultReference(note.VaultRef), note.CreatedAt, note.UpdatedAt)

	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint") {
			return ErrDuplicateID
		}
		if strings.Contains(err.Error(), "FOREIGN KEY constraint") {
			return ErrWorkspaceNotFound
		}
		return fmt.Errorf("failed to create note: %w", err)
	}

	return nil
}

// GetNote retrieves a note by ID.
func (s *SQLiteStore) GetNote(ctx context.Context, id string) (*WorkspaceNote, error) {
	note := &WorkspaceNote{}
	var vaultReferenceJSON sql.NullString

	err := s.db.QueryRowContext(ctx, `
		SELECT id, workspace_id, name, content, COALESCE(vault_reference_json, ''), created_at, updated_at
		FROM workspace_notes WHERE id = ?
	`, id).Scan(&note.ID, &note.WorkspaceID, &note.Name, &note.Content,
		&vaultReferenceJSON, &note.CreatedAt, &note.UpdatedAt)

	if err == sql.ErrNoRows {
		return nil, ErrNoteNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get note: %w", err)
	}
	if note.VaultRef, err = decodeNoteVaultReference(vaultReferenceJSON.String); err != nil {
		return nil, fmt.Errorf("failed to decode note vault reference: %w", err)
	}

	return note, nil
}

// UpdateNote updates note metadata and content.
func (s *SQLiteStore) UpdateNote(ctx context.Context, note *WorkspaceNote) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE workspace_notes
		SET name = ?, content = ?, workspace_id = ?, vault_reference_json = ?, updated_at = ?
		WHERE id = ?
	`, note.Name, note.Content, note.WorkspaceID, encodeNoteVaultReference(note.VaultRef), note.UpdatedAt, note.ID)

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
	result, err := s.db.ExecContext(ctx, "DELETE FROM workspace_notes WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("failed to delete note: %w", err)
	}

	if err := database.CheckRowsAffectedWithError(result, "note", ErrNoteNotFound); err != nil {
		return err
	}

	return nil
}

// ListNotesByWorkspace returns all notes in a workspace.
func (s *SQLiteStore) ListNotesByWorkspace(ctx context.Context, workspaceID string) ([]WorkspaceNoteListItem, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, workspace_id, name,
		       CASE WHEN LENGTH(content) > 100 THEN SUBSTR(content, 1, 100) || '...' ELSE content END as preview,
		       COALESCE(vault_reference_json, ''), created_at, updated_at
		FROM workspace_notes
		WHERE workspace_id = ?
		ORDER BY updated_at DESC
	`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("failed to list notes: %w", err)
	}
	defer func() { _ = rows.Close() }()

	notes := make([]WorkspaceNoteListItem, 0)
	for rows.Next() {
		var note WorkspaceNoteListItem
		var vaultReferenceRaw string
		if err := rows.Scan(&note.ID, &note.WorkspaceID, &note.Name, &note.Preview,
			&vaultReferenceRaw, &note.CreatedAt, &note.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan note: %w", err)
		}
		note.VaultRef, err = decodeNoteVaultReference(vaultReferenceRaw)
		if err != nil {
			return nil, fmt.Errorf("failed to decode note vault reference: %w", err)
		}
		notes = append(notes, note)
	}

	return notes, nil
}

func encodeNoteVaultReference(ref *vaultref.Reference) string {
	clean := vaultref.Normalize(ref)
	if clean == nil {
		return ""
	}
	data, err := json.Marshal(clean)
	if err != nil {
		return ""
	}
	return string(data)
}

func decodeNoteVaultReference(raw string) (*vaultref.Reference, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" {
		return nil, nil
	}
	var ref vaultref.Reference
	if err := json.Unmarshal([]byte(raw), &ref); err != nil {
		return nil, err
	}
	return vaultref.Normalize(&ref), nil
}

// SearchNotes performs full-text search across note names and content.
func (s *SQLiteStore) SearchNotes(ctx context.Context, query string, limit int) ([]NoteSearchResult, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT n.id, n.workspace_id, n.name,
		       CASE WHEN LENGTH(n.content) > 100 THEN SUBSTR(n.content, 1, 100) || '...' ELSE n.content END as preview,
		       n.created_at, n.updated_at,
		       w.name as workspace_name,
		       snippet(workspace_notes_fts, 2, '<mark>', '</mark>', '...', 32) as snippet
		FROM workspace_notes n
		INNER JOIN workspace_notes_fts fts ON n.id = fts.note_id
		LEFT JOIN workspaces w ON n.workspace_id = w.id
		WHERE workspace_notes_fts MATCH ?
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
		var workspaceName sql.NullString
		var snippet string

		if err := rows.Scan(&result.ID, &result.WorkspaceID, &result.Name, &result.Preview,
			&result.CreatedAt, &result.UpdatedAt, &workspaceName, &snippet); err != nil {
			return nil, fmt.Errorf("failed to scan search result: %w", err)
		}

		result.WorkspaceName = workspaceName.String
		if snippet != "" {
			result.Snippets = []string{snippet}
		}

		results = append(results, result)
	}

	return results, nil
}
