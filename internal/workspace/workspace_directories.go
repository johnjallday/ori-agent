package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

// AddDirectoryReference adds a directory reference to the workspace
func (w *Workspace) AddDirectoryReference(dir DirectoryReference) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if dir.Name == "" {
		return fmt.Errorf("directory reference name is required")
	}
	if dir.Path == "" {
		return fmt.Errorf("directory reference path is required")
	}

	// Validate path exists and is a directory
	info, err := os.Stat(dir.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("directory does not exist: %s", dir.Path)
		}
		return fmt.Errorf("failed to access directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("path is not a directory: %s", dir.Path)
	}

	if dir.ID == "" {
		dir.ID = uuid.New().String()
	}
	now := time.Now()
	if dir.CreatedAt.IsZero() {
		dir.CreatedAt = now
	}
	dir.UpdatedAt = now
	dir.WorkspaceID = w.ID

	w.DirectoryReferences = append(w.DirectoryReferences, dir)
	w.UpdatedAt = now
	return nil
}

// UpdateDirectoryReference updates an existing directory reference in the workspace
func (w *Workspace) UpdateDirectoryReference(dir DirectoryReference) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Validate path if it changed
	if dir.Path != "" {
		info, err := os.Stat(dir.Path)
		if err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("directory does not exist: %s", dir.Path)
			}
			return fmt.Errorf("failed to access directory: %w", err)
		}
		if !info.IsDir() {
			return fmt.Errorf("path is not a directory: %s", dir.Path)
		}
	}

	for i := range w.DirectoryReferences {
		if w.DirectoryReferences[i].ID == dir.ID {
			dir.UpdatedAt = time.Now()
			dir.WorkspaceID = w.ID
			// Preserve created timestamp
			dir.CreatedAt = w.DirectoryReferences[i].CreatedAt
			w.DirectoryReferences[i] = dir
			w.UpdatedAt = dir.UpdatedAt
			return nil
		}
	}

	return fmt.Errorf("directory reference %s not found in workspace", dir.ID)
}

// DeleteDirectoryReference removes a directory reference from the workspace
func (w *Workspace) DeleteDirectoryReference(id string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	for i := range w.DirectoryReferences {
		if w.DirectoryReferences[i].ID == id {
			w.DirectoryReferences = append(w.DirectoryReferences[:i], w.DirectoryReferences[i+1:]...)
			w.UpdatedAt = time.Now()
			return nil
		}
	}

	return fmt.Errorf("directory reference %s not found in workspace", id)
}

// GetDirectoryReference retrieves a directory reference by ID
func (w *Workspace) GetDirectoryReference(id string) (*DirectoryReference, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	for i := range w.DirectoryReferences {
		if w.DirectoryReferences[i].ID == id {
			dirCopy := w.DirectoryReferences[i]
			return &dirCopy, nil
		}
	}

	return nil, fmt.Errorf("directory reference %s not found in workspace", id)
}

// ListDirectoryFiles lists all files in a directory reference
func (w *Workspace) ListDirectoryFiles(dirID string) ([]FileInfo, error) {
	dir, err := w.GetDirectoryReference(dirID)
	if err != nil {
		return nil, err
	}

	var files []FileInfo
	err = filepath.Walk(dir.Path, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Get relative path from directory root
		relPath, err := filepath.Rel(dir.Path, path)
		if err != nil {
			return err
		}

		// Skip the root directory itself
		if relPath == "." {
			return nil
		}

		files = append(files, FileInfo{
			Name:         info.Name(),
			RelativePath: relPath,
			Size:         info.Size(),
			IsDir:        info.IsDir(),
			ModTime:      info.ModTime(),
		})

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to list directory files: %w", err)
	}

	return files, nil
}

// ReadDirectoryFile reads the content of a file in a directory reference
func (w *Workspace) ReadDirectoryFile(dirID string, relativePath string) ([]byte, error) {
	dir, err := w.GetDirectoryReference(dirID)
	if err != nil {
		return nil, err
	}

	// Validate the relative path to prevent directory traversal attacks
	if err := validateRelativePath(relativePath); err != nil {
		return nil, err
	}

	// Construct the full path
	fullPath := filepath.Join(dir.Path, relativePath)

	// Verify the resolved path is still within the directory
	absPath, err := filepath.Abs(fullPath)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve path: %w", err)
	}
	absDirPath, err := filepath.Abs(dir.Path)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve directory path: %w", err)
	}

	if !strings.HasPrefix(absPath, absDirPath) {
		return nil, fmt.Errorf("access denied: path is outside directory")
	}

	// Read the file
	content, err := os.ReadFile(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("file not found: %s", relativePath)
		}
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	return content, nil
}

// validateRelativePath checks for directory traversal attempts
func validateRelativePath(path string) error {
	if path == "" {
		return fmt.Errorf("path is required")
	}

	// Clean the path to normalize it
	cleanPath := filepath.Clean(path)

	// Check for absolute paths
	if filepath.IsAbs(cleanPath) {
		return fmt.Errorf("absolute paths are not allowed")
	}

	// Check for directory traversal
	if strings.HasPrefix(cleanPath, "..") || strings.Contains(cleanPath, string(filepath.Separator)+"..") {
		return fmt.Errorf("directory traversal is not allowed")
	}

	return nil
}
