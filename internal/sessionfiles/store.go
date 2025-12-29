package sessionfiles

import (
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/logger"
)

// Store manages file storage for sessions
type Store struct {
	basePath  string
	manifests map[string]*Manifest
	mu        sync.RWMutex
}

// NewStore creates a new session files store
func NewStore(basePath string) (*Store, error) {
	// Ensure base directory exists
	if err := os.MkdirAll(basePath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create session files directory: %w", err)
	}

	store := &Store{
		basePath:  basePath,
		manifests: make(map[string]*Manifest),
	}

	// Load existing manifests
	if err := store.loadManifests(); err != nil {
		logger.Error("Failed to load manifests", logger.Fields{"error": err})
	}

	return store, nil
}

// getSessionPath returns the path for a session's files directory
func (s *Store) getSessionPath(sessionID string) string {
	return filepath.Join(s.basePath, sessionID)
}

// getFilesPath returns the path for a session's files subdirectory
func (s *Store) getFilesPath(sessionID string) string {
	return filepath.Join(s.getSessionPath(sessionID), "files")
}

// getManifestPath returns the path for a session's manifest file
func (s *Store) getManifestPath(sessionID string) string {
	return filepath.Join(s.getSessionPath(sessionID), "manifest.json")
}

// ensureSessionDir creates the session directory structure if it doesn't exist
func (s *Store) ensureSessionDir(sessionID string) error {
	filesPath := s.getFilesPath(sessionID)
	return os.MkdirAll(filesPath, 0755)
}

// getOrCreateManifest returns the manifest for a session, creating one if it doesn't exist
func (s *Store) getOrCreateManifest(sessionID string) *Manifest {
	s.mu.Lock()
	defer s.mu.Unlock()

	if m, ok := s.manifests[sessionID]; ok {
		return m
	}

	// Try to load from disk
	manifestPath := s.getManifestPath(sessionID)
	m, err := LoadManifest(manifestPath)
	if err != nil {
		// Create new manifest
		m = NewManifest(sessionID)
	}

	s.manifests[sessionID] = m
	return m
}

// saveManifest saves the manifest for a session
func (s *Store) saveManifest(sessionID string) error {
	s.mu.RLock()
	m, ok := s.manifests[sessionID]
	s.mu.RUnlock()

	if !ok {
		return fmt.Errorf("manifest for session %s not found", sessionID)
	}

	manifestPath := s.getManifestPath(sessionID)
	return SaveManifest(m, manifestPath)
}

// AddFile copies a file into the session folder
func (s *Store) AddFile(sessionID string, srcPath string, name string) (*FileEntry, error) {
	// Ensure session directory exists
	if err := s.ensureSessionDir(sessionID); err != nil {
		return nil, fmt.Errorf("failed to create session directory: %w", err)
	}

	// Get or create manifest
	manifest := s.getOrCreateManifest(sessionID)

	// Check file limit
	if manifest.FileCount() >= manifest.MaxFiles {
		return nil, fmt.Errorf("maximum file limit (%d) reached", manifest.MaxFiles)
	}

	// Open source file
	srcFile, err := os.Open(srcPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open source file: %w", err)
	}
	defer func() { _ = srcFile.Close() }()

	// Get file info
	srcInfo, err := srcFile.Stat()
	if err != nil {
		return nil, fmt.Errorf("failed to stat source file: %w", err)
	}

	// Generate file ID and destination path
	fileID := uuid.New().String()
	destPath := filepath.Join(s.getFilesPath(sessionID), name)

	// Copy file
	destFile, err := os.Create(destPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create destination file: %w", err)
	}
	defer func() { _ = destFile.Close() }()

	if _, err := io.Copy(destFile, srcFile); err != nil {
		_ = os.Remove(destPath) // Cleanup on failure
		return nil, fmt.Errorf("failed to copy file: %w", err)
	}

	// Detect MIME type
	mimeType := detectMimeType(name)

	// Create file entry
	entry := FileEntry{
		ID:       fileID,
		Name:     name,
		Path:     name, // Relative path within files folder
		Size:     srcInfo.Size(),
		MimeType: mimeType,
		IsLink:   false,
		Status:   FileStatusOK,
		AddedAt:  time.Now(),
	}

	// Add to manifest
	s.mu.Lock()
	if err := manifest.AddFile(entry); err != nil {
		s.mu.Unlock()
		_ = os.Remove(destPath) // Cleanup on failure
		return nil, err
	}
	s.mu.Unlock()

	// Save manifest
	if err := s.saveManifest(sessionID); err != nil {
		logger.Error("Failed to save manifest after adding file", logger.Fields{
			"session_id": sessionID,
			"file_id":    fileID,
			"error":      err,
		})
	}

	logger.Info("Added file to session", logger.Fields{
		"session_id": sessionID,
		"file_id":    fileID,
		"name":       name,
		"size":       srcInfo.Size(),
	})

	return &entry, nil
}

// AddFileFromReader copies file content from a reader into the session folder
func (s *Store) AddFileFromReader(sessionID string, reader io.Reader, name string, size int64) (*FileEntry, error) {
	// Ensure session directory exists
	if err := s.ensureSessionDir(sessionID); err != nil {
		return nil, fmt.Errorf("failed to create session directory: %w", err)
	}

	// Get or create manifest
	manifest := s.getOrCreateManifest(sessionID)

	// Check file limit
	if manifest.FileCount() >= manifest.MaxFiles {
		return nil, fmt.Errorf("maximum file limit (%d) reached", manifest.MaxFiles)
	}

	// Generate file ID and destination path
	fileID := uuid.New().String()
	destPath := filepath.Join(s.getFilesPath(sessionID), name)

	// Create file
	destFile, err := os.Create(destPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create destination file: %w", err)
	}
	defer func() { _ = destFile.Close() }()

	written, err := io.Copy(destFile, reader)
	if err != nil {
		_ = os.Remove(destPath) // Cleanup on failure
		return nil, fmt.Errorf("failed to write file: %w", err)
	}

	// Detect MIME type
	mimeType := detectMimeType(name)

	// Create file entry
	entry := FileEntry{
		ID:       fileID,
		Name:     name,
		Path:     name,
		Size:     written,
		MimeType: mimeType,
		IsLink:   false,
		Status:   FileStatusOK,
		AddedAt:  time.Now(),
	}

	// Add to manifest
	s.mu.Lock()
	if err := manifest.AddFile(entry); err != nil {
		s.mu.Unlock()
		_ = os.Remove(destPath)
		return nil, err
	}
	s.mu.Unlock()

	// Save manifest
	if err := s.saveManifest(sessionID); err != nil {
		logger.Error("Failed to save manifest after adding file", logger.Fields{
			"session_id": sessionID,
			"file_id":    fileID,
			"error":      err,
		})
	}

	return &entry, nil
}

// LinkFile creates a symlink to an external file
func (s *Store) LinkFile(sessionID string, originalPath string, name string) (*FileEntry, error) {
	// Ensure session directory exists
	if err := s.ensureSessionDir(sessionID); err != nil {
		return nil, fmt.Errorf("failed to create session directory: %w", err)
	}

	// Verify source file exists
	srcInfo, err := os.Stat(originalPath)
	if err != nil {
		return nil, fmt.Errorf("source file not accessible: %w", err)
	}

	// Get or create manifest
	manifest := s.getOrCreateManifest(sessionID)

	// Check file limit
	if manifest.FileCount() >= manifest.MaxFiles {
		return nil, fmt.Errorf("maximum file limit (%d) reached", manifest.MaxFiles)
	}

	// Generate file ID and link path
	fileID := uuid.New().String()
	linkPath := filepath.Join(s.getFilesPath(sessionID), name)

	// Try to create symlink
	err = os.Symlink(originalPath, linkPath)
	if err != nil {
		// On Windows, symlinks may fail due to permissions
		// Fall back to copying the file
		if runtime.GOOS == "windows" {
			logger.Info("Symlink failed on Windows, falling back to copy", logger.Fields{
				"session_id":    sessionID,
				"original_path": originalPath,
			})
			return s.AddFile(sessionID, originalPath, name)
		}
		return nil, fmt.Errorf("failed to create symlink: %w", err)
	}

	// Detect MIME type
	mimeType := detectMimeType(name)

	// Create file entry
	entry := FileEntry{
		ID:           fileID,
		Name:         name,
		Path:         name,
		Size:         srcInfo.Size(),
		MimeType:     mimeType,
		IsLink:       true,
		OriginalPath: originalPath,
		Status:       FileStatusOK,
		AddedAt:      time.Now(),
	}

	// Add to manifest
	s.mu.Lock()
	if err := manifest.AddFile(entry); err != nil {
		s.mu.Unlock()
		_ = os.Remove(linkPath)
		return nil, err
	}
	s.mu.Unlock()

	// Save manifest
	if err := s.saveManifest(sessionID); err != nil {
		logger.Error("Failed to save manifest after linking file", logger.Fields{
			"session_id": sessionID,
			"file_id":    fileID,
			"error":      err,
		})
	}

	logger.Info("Linked file to session", logger.Fields{
		"session_id":    sessionID,
		"file_id":       fileID,
		"name":          name,
		"original_path": originalPath,
	})

	return &entry, nil
}

// RemoveFile removes a file from the session
func (s *Store) RemoveFile(sessionID string, fileID string) error {
	manifest := s.getOrCreateManifest(sessionID)

	// Get file entry
	entry, err := manifest.GetFile(fileID)
	if err != nil {
		return err
	}

	// Remove physical file/link
	filePath := filepath.Join(s.getFilesPath(sessionID), entry.Path)
	if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
		logger.Error("Failed to remove file from disk", logger.Fields{
			"session_id": sessionID,
			"file_id":    fileID,
			"path":       filePath,
			"error":      err,
		})
	}

	// Remove from manifest
	s.mu.Lock()
	if err := manifest.RemoveFile(fileID); err != nil {
		s.mu.Unlock()
		return err
	}
	s.mu.Unlock()

	// Save manifest
	if err := s.saveManifest(sessionID); err != nil {
		logger.Error("Failed to save manifest after removing file", logger.Fields{
			"session_id": sessionID,
			"file_id":    fileID,
			"error":      err,
		})
	}

	logger.Info("Removed file from session", logger.Fields{
		"session_id": sessionID,
		"file_id":    fileID,
	})

	return nil
}

// GetFile returns a file entry by ID
func (s *Store) GetFile(sessionID string, fileID string) (*FileEntry, error) {
	manifest := s.getOrCreateManifest(sessionID)
	return manifest.GetFile(fileID)
}

// ListFiles returns all files in a session
func (s *Store) ListFiles(sessionID string) ([]FileEntry, error) {
	manifest := s.getOrCreateManifest(sessionID)

	// Return a copy to prevent external modification
	files := make([]FileEntry, len(manifest.Files))
	copy(files, manifest.Files)

	return files, nil
}

// ValidateLinks checks all linked files and updates their status
func (s *Store) ValidateLinks(sessionID string) ([]FileEntry, error) {
	manifest := s.getOrCreateManifest(sessionID)

	var brokenLinks []FileEntry

	s.mu.Lock()
	for i := range manifest.Files {
		if manifest.Files[i].IsLink {
			// Check if original file still exists
			_, err := os.Stat(manifest.Files[i].OriginalPath)
			if err != nil {
				manifest.Files[i].Status = FileStatusBroken
				brokenLinks = append(brokenLinks, manifest.Files[i])
			} else {
				manifest.Files[i].Status = FileStatusOK
			}
		}
	}
	s.mu.Unlock()

	// Save updated manifest
	if err := s.saveManifest(sessionID); err != nil {
		logger.Error("Failed to save manifest after validating links", logger.Fields{
			"session_id": sessionID,
			"error":      err,
		})
	}

	return brokenLinks, nil
}

// GetFilePath returns the absolute path to a file for reading
func (s *Store) GetFilePath(sessionID string, fileID string) (string, error) {
	entry, err := s.GetFile(sessionID, fileID)
	if err != nil {
		return "", err
	}

	if entry.IsLink {
		// For links, check if original exists
		if _, err := os.Stat(entry.OriginalPath); err != nil {
			return "", fmt.Errorf("linked file not accessible: %w", err)
		}
		return entry.OriginalPath, nil
	}

	// For copied files, return path in session folder
	return filepath.Join(s.getFilesPath(sessionID), entry.Path), nil
}

// GetSessionFilesPath returns the path to a session's files directory
func (s *Store) GetSessionFilesPath(sessionID string) string {
	return s.getFilesPath(sessionID)
}

// RelinkFile updates a broken link to point to a new location
func (s *Store) RelinkFile(sessionID string, fileID string, newPath string) error {
	manifest := s.getOrCreateManifest(sessionID)

	entry, err := manifest.GetFile(fileID)
	if err != nil {
		return err
	}

	if !entry.IsLink {
		return fmt.Errorf("file %s is not a link", fileID)
	}

	// Verify new path exists
	newInfo, err := os.Stat(newPath)
	if err != nil {
		return fmt.Errorf("new path not accessible: %w", err)
	}

	// Remove old symlink
	oldLinkPath := filepath.Join(s.getFilesPath(sessionID), entry.Path)
	_ = os.Remove(oldLinkPath)

	// Create new symlink
	if err := os.Symlink(newPath, oldLinkPath); err != nil {
		return fmt.Errorf("failed to create new symlink: %w", err)
	}

	// Update entry
	s.mu.Lock()
	entry.OriginalPath = newPath
	entry.Size = newInfo.Size()
	entry.Status = FileStatusOK
	manifest.UpdatedAt = time.Now()
	s.mu.Unlock()

	// Save manifest
	if err := s.saveManifest(sessionID); err != nil {
		return fmt.Errorf("failed to save manifest: %w", err)
	}

	logger.Info("Relinked file", logger.Fields{
		"session_id": sessionID,
		"file_id":    fileID,
		"new_path":   newPath,
	})

	return nil
}

// loadManifests loads all existing manifests on startup
func (s *Store) loadManifests() error {
	entries, err := os.ReadDir(s.basePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			sessionID := entry.Name()
			manifestPath := s.getManifestPath(sessionID)

			m, err := LoadManifest(manifestPath)
			if err != nil {
				continue // Skip sessions without manifests
			}

			s.manifests[sessionID] = m
		}
	}

	return nil
}

// DeleteSession removes all files for a session
func (s *Store) DeleteSession(sessionID string) error {
	s.mu.Lock()
	delete(s.manifests, sessionID)
	s.mu.Unlock()

	sessionPath := s.getSessionPath(sessionID)
	if err := os.RemoveAll(sessionPath); err != nil {
		return fmt.Errorf("failed to delete session directory: %w", err)
	}

	logger.Info("Deleted session files", logger.Fields{"session_id": sessionID})

	return nil
}

// detectMimeType returns the MIME type for a file based on its extension
func detectMimeType(filename string) string {
	ext := filepath.Ext(filename)
	mimeType := mime.TypeByExtension(ext)
	if mimeType == "" {
		return "application/octet-stream"
	}
	return mimeType
}
