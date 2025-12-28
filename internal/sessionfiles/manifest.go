package sessionfiles

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// FileStatus represents the current status of a file entry
type FileStatus string

const (
	// FileStatusOK indicates the file is accessible and valid
	FileStatusOK FileStatus = "ok"
	// FileStatusBroken indicates a linked file is no longer accessible
	FileStatusBroken FileStatus = "broken"

	// MaxFilesPerSession is the default maximum number of files per session
	MaxFilesPerSession = 50
)

// FileEntry represents a file in a session
type FileEntry struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	Path         string     `json:"path"` // Relative path within session files folder
	Size         int64      `json:"size"`
	MimeType     string     `json:"mime_type"`
	IsLink       bool       `json:"is_link"`
	OriginalPath string     `json:"original_path,omitempty"` // Original path for linked files
	Status       FileStatus `json:"status"`
	AddedAt      time.Time  `json:"added_at"`
}

// Manifest holds metadata about all files in a session
type Manifest struct {
	SessionID   string      `json:"session_id"`
	Files       []FileEntry `json:"files"`
	MaxFiles    int         `json:"max_files"`
	UpdatedAt   time.Time   `json:"updated_at"`
	Preferences Preferences `json:"preferences"`
}

// Preferences stores user preferences for file handling in this session
type Preferences struct {
	DefaultMode  string `json:"default_mode"` // "copy" or "link"
	RememberMode bool   `json:"remember_mode"`
}

// NewManifest creates a new empty manifest for a session
func NewManifest(sessionID string) *Manifest {
	return &Manifest{
		SessionID: sessionID,
		Files:     make([]FileEntry, 0),
		MaxFiles:  50, // Default max files per session
		UpdatedAt: time.Now(),
		Preferences: Preferences{
			DefaultMode:  "copy",
			RememberMode: false,
		},
	}
}

// AddFile adds a file entry to the manifest
func (m *Manifest) AddFile(entry FileEntry) error {
	if len(m.Files) >= m.MaxFiles {
		return fmt.Errorf("maximum file limit (%d) reached", m.MaxFiles)
	}

	// Check for duplicate ID
	for _, f := range m.Files {
		if f.ID == entry.ID {
			return fmt.Errorf("file with ID %s already exists", entry.ID)
		}
	}

	m.Files = append(m.Files, entry)
	m.UpdatedAt = time.Now()
	return nil
}

// RemoveFile removes a file entry from the manifest by ID
func (m *Manifest) RemoveFile(fileID string) error {
	for i, f := range m.Files {
		if f.ID == fileID {
			m.Files = append(m.Files[:i], m.Files[i+1:]...)
			m.UpdatedAt = time.Now()
			return nil
		}
	}
	return fmt.Errorf("file with ID %s not found", fileID)
}

// GetFile returns a file entry by ID
func (m *Manifest) GetFile(fileID string) (*FileEntry, error) {
	for i := range m.Files {
		if m.Files[i].ID == fileID {
			return &m.Files[i], nil
		}
	}
	return nil, fmt.Errorf("file with ID %s not found", fileID)
}

// UpdateFileStatus updates the status of a file entry
func (m *Manifest) UpdateFileStatus(fileID string, status FileStatus) error {
	for i := range m.Files {
		if m.Files[i].ID == fileID {
			m.Files[i].Status = status
			m.UpdatedAt = time.Now()
			return nil
		}
	}
	return fmt.Errorf("file with ID %s not found", fileID)
}

// FileCount returns the number of files in the manifest
func (m *Manifest) FileCount() int {
	return len(m.Files)
}

// ToJSON serializes the manifest to JSON
func (m *Manifest) ToJSON() ([]byte, error) {
	return json.MarshalIndent(m, "", "  ")
}

// FromJSON deserializes a manifest from JSON
func FromJSON(data []byte) (*Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("failed to unmarshal manifest: %w", err)
	}
	return &m, nil
}

// LoadManifest loads a manifest from a file path
func LoadManifest(filePath string) (*Manifest, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("manifest file not found: %s", filePath)
		}
		return nil, fmt.Errorf("failed to read manifest file: %w", err)
	}

	return FromJSON(data)
}

// SaveManifest saves a manifest to a file path
func SaveManifest(m *Manifest, filePath string) error {
	// Ensure directory exists
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create manifest directory: %w", err)
	}

	data, err := m.ToJSON()
	if err != nil {
		return fmt.Errorf("failed to serialize manifest: %w", err)
	}

	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write manifest file: %w", err)
	}

	return nil
}
