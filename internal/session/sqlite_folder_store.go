package session

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/database"
)

// ============================================================================
// Folder Operations
// ============================================================================

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

	if err := database.CheckRowsAffectedWithError(result, "folder", ErrFolderNotFound); err != nil {
		return err
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
	defer func() { _ = rows.Close() }()

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
	defer func() { _ = rows.Close() }()

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
