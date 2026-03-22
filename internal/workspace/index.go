package workspace

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/johnjallday/ori-agent/internal/logger"
	_ "modernc.org/sqlite"
)

const (
	// IndexDBFile is the filename for the global workspace index database.
	IndexDBFile = "index.db"
)

// IndexEntry represents a workspace entry in the global index.
type IndexEntry struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	FolderPath string    `json:"folder_path"` // Path relative to workspaces root
	ParentID   string    `json:"parent_id,omitempty"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// Index manages a global SQLite index of all registered workspaces.
// It is a cache — the workspace folders on disk are the source of truth.
type Index struct {
	db       *sql.DB
	basePath string // workspaces root directory
}

// NewIndex opens or creates the workspace index database.
func NewIndex(basePath string) (*Index, error) {
	dbPath := filepath.Join(basePath, IndexDBFile)

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open workspace index: %w", err)
	}

	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	idx := &Index{db: db, basePath: basePath}

	if err := idx.init(); err != nil {
		db.Close()
		return nil, err
	}

	return idx, nil
}

// init creates the index schema if it doesn't exist.
func (idx *Index) init() error {
	_, err := idx.db.Exec(`
		PRAGMA journal_mode = WAL;
		PRAGMA foreign_keys = ON;
		PRAGMA busy_timeout = 5000;

		CREATE TABLE IF NOT EXISTS workspaces (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			folder_path TEXT NOT NULL,
			parent_id TEXT,
			updated_at DATETIME NOT NULL
		);

		CREATE INDEX IF NOT EXISTS idx_workspaces_parent ON workspaces(parent_id);
		CREATE INDEX IF NOT EXISTS idx_workspaces_folder ON workspaces(folder_path);
	`)
	if err != nil {
		return fmt.Errorf("failed to initialize workspace index schema: %w", err)
	}
	return nil
}

// Register adds or updates a workspace entry in the index.
func (idx *Index) Register(entry IndexEntry) error {
	_, err := idx.db.Exec(`
		INSERT INTO workspaces (id, name, folder_path, parent_id, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			folder_path = excluded.folder_path,
			parent_id = excluded.parent_id,
			updated_at = excluded.updated_at
	`, entry.ID, entry.Name, entry.FolderPath, nullString(entry.ParentID), entry.UpdatedAt)
	if err != nil {
		return fmt.Errorf("failed to register workspace in index: %w", err)
	}
	return nil
}

// Unregister removes a workspace entry from the index.
func (idx *Index) Unregister(workspaceID string) error {
	_, err := idx.db.Exec(`DELETE FROM workspaces WHERE id = ?`, workspaceID)
	if err != nil {
		return fmt.Errorf("failed to unregister workspace from index: %w", err)
	}
	return nil
}

// List returns all registered workspace entries.
func (idx *Index) List() ([]IndexEntry, error) {
	rows, err := idx.db.Query(`
		SELECT id, name, folder_path, COALESCE(parent_id, ''), updated_at
		FROM workspaces
		ORDER BY name
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to list workspaces from index: %w", err)
	}
	defer rows.Close()

	var entries []IndexEntry
	for rows.Next() {
		var e IndexEntry
		if err := rows.Scan(&e.ID, &e.Name, &e.FolderPath, &e.ParentID, &e.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan workspace index row: %w", err)
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// Get retrieves a single workspace entry by ID.
func (idx *Index) Get(workspaceID string) (*IndexEntry, error) {
	var e IndexEntry
	err := idx.db.QueryRow(`
		SELECT id, name, folder_path, COALESCE(parent_id, ''), updated_at
		FROM workspaces WHERE id = ?
	`, workspaceID).Scan(&e.ID, &e.Name, &e.FolderPath, &e.ParentID, &e.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("workspace %s not found in index", workspaceID)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get workspace from index: %w", err)
	}
	return &e, nil
}

// Rebuild scans the workspace root directory recursively and repopulates the
// index from workspace.json files found on disk. Existing entries are replaced.
func (idx *Index) Rebuild() error {
	ctx := context.Background()
	tx, err := idx.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin rebuild transaction: %w", err)
	}

	// Clear existing entries
	if _, err := tx.Exec(`DELETE FROM workspaces`); err != nil {
		tx.Rollback()
		return fmt.Errorf("failed to clear index for rebuild: %w", err)
	}

	// Scan and register all workspaces
	if err := idx.scanDir(tx, idx.basePath, "", 0); err != nil {
		tx.Rollback()
		return fmt.Errorf("failed to scan workspaces for rebuild: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit rebuild: %w", err)
	}

	logger.Info("Workspace index rebuilt", logger.Fields{"base_path": idx.basePath})
	return nil
}

// scanDir recursively scans a directory for workspace folders.
func (idx *Index) scanDir(tx *sql.Tx, dir, parentID string, depth int) error {
	if depth > MaxNestingDepth {
		return nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		folderPath := filepath.Join(dir, entry.Name())
		configPath := filepath.Join(folderPath, WorkspaceConfigFile)

		data, err := os.ReadFile(configPath)
		if err != nil {
			continue // Not a workspace folder
		}

		ws, err := FromJSON(data)
		if err != nil {
			logger.Warn("Skipping invalid workspace.json during rebuild", logger.Fields{
				"path":  configPath,
				"error": err.Error(),
			})
			continue
		}

		// Compute relative folder path from basePath
		relPath, err := filepath.Rel(idx.basePath, folderPath)
		if err != nil {
			relPath = entry.Name()
		}

		_, err = tx.Exec(`
			INSERT INTO workspaces (id, name, folder_path, parent_id, updated_at)
			VALUES (?, ?, ?, ?, ?)
		`, ws.ID, ws.Name, relPath, nullString(parentID), ws.UpdatedAt)
		if err != nil {
			return fmt.Errorf("failed to insert workspace %s: %w", ws.ID, err)
		}

		// Recurse into sub-workspaces
		subDir := filepath.Join(folderPath, SubWorkspacesDir)
		if err := idx.scanDir(tx, subDir, ws.ID, depth+1); err != nil {
			logger.Warn("Error scanning sub-workspaces", logger.Fields{
				"dir":   subDir,
				"error": err.Error(),
			})
		}
	}

	return nil
}

// Close closes the index database.
func (idx *Index) Close() error {
	return idx.db.Close()
}

// nullString converts an empty string to sql NULL.
func nullString(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}
