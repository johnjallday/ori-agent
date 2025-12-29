package sessionfiles

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewManifest(t *testing.T) {
	m := NewManifest("test-session")

	if m.SessionID != "test-session" {
		t.Errorf("expected session ID 'test-session', got '%s'", m.SessionID)
	}

	if len(m.Files) != 0 {
		t.Errorf("expected empty files list, got %d files", len(m.Files))
	}

	if m.MaxFiles != 50 {
		t.Errorf("expected max files 50, got %d", m.MaxFiles)
	}

	if m.Preferences.DefaultMode != "copy" {
		t.Errorf("expected default mode 'copy', got '%s'", m.Preferences.DefaultMode)
	}
}

func TestManifest_AddFile(t *testing.T) {
	m := NewManifest("test-session")

	entry := FileEntry{
		ID:       "file-1",
		Name:     "test.txt",
		Path:     "test.txt",
		Size:     100,
		MimeType: "text/plain",
		Status:   FileStatusOK,
		AddedAt:  time.Now(),
	}

	if err := m.AddFile(entry); err != nil {
		t.Fatalf("failed to add file: %v", err)
	}

	if m.FileCount() != 1 {
		t.Errorf("expected 1 file, got %d", m.FileCount())
	}
}

func TestManifest_AddFile_DuplicateID(t *testing.T) {
	m := NewManifest("test-session")

	entry := FileEntry{
		ID:       "file-1",
		Name:     "test.txt",
		Path:     "test.txt",
		Size:     100,
		MimeType: "text/plain",
		Status:   FileStatusOK,
		AddedAt:  time.Now(),
	}

	if err := m.AddFile(entry); err != nil {
		t.Fatalf("failed to add first file: %v", err)
	}

	// Try to add duplicate
	err := m.AddFile(entry)
	if err == nil {
		t.Error("expected error when adding duplicate file ID")
	}
}

func TestManifest_AddFile_MaxLimit(t *testing.T) {
	m := NewManifest("test-session")
	m.MaxFiles = 2 // Set low limit for testing

	// Add first file
	if err := m.AddFile(FileEntry{ID: "file-1", Name: "test1.txt"}); err != nil {
		t.Fatalf("failed to add first file: %v", err)
	}

	// Add second file
	if err := m.AddFile(FileEntry{ID: "file-2", Name: "test2.txt"}); err != nil {
		t.Fatalf("failed to add second file: %v", err)
	}

	// Third file should fail
	err := m.AddFile(FileEntry{ID: "file-3", Name: "test3.txt"})
	if err == nil {
		t.Error("expected error when exceeding max file limit")
	}
}

func TestManifest_RemoveFile(t *testing.T) {
	m := NewManifest("test-session")

	entry := FileEntry{
		ID:   "file-1",
		Name: "test.txt",
	}

	if err := m.AddFile(entry); err != nil {
		t.Fatalf("failed to add file: %v", err)
	}

	if err := m.RemoveFile("file-1"); err != nil {
		t.Fatalf("failed to remove file: %v", err)
	}

	if m.FileCount() != 0 {
		t.Errorf("expected 0 files after removal, got %d", m.FileCount())
	}
}

func TestManifest_RemoveFile_NotFound(t *testing.T) {
	m := NewManifest("test-session")

	err := m.RemoveFile("nonexistent")
	if err == nil {
		t.Error("expected error when removing nonexistent file")
	}
}

func TestManifest_GetFile(t *testing.T) {
	m := NewManifest("test-session")

	entry := FileEntry{
		ID:       "file-1",
		Name:     "test.txt",
		Size:     100,
		MimeType: "text/plain",
	}

	if err := m.AddFile(entry); err != nil {
		t.Fatalf("failed to add file: %v", err)
	}

	retrieved, err := m.GetFile("file-1")
	if err != nil {
		t.Fatalf("failed to get file: %v", err)
	}

	if retrieved.Name != "test.txt" {
		t.Errorf("expected name 'test.txt', got '%s'", retrieved.Name)
	}
}

func TestManifest_GetFile_NotFound(t *testing.T) {
	m := NewManifest("test-session")

	_, err := m.GetFile("nonexistent")
	if err == nil {
		t.Error("expected error when getting nonexistent file")
	}
}

func TestManifest_UpdateFileStatus(t *testing.T) {
	m := NewManifest("test-session")

	entry := FileEntry{
		ID:     "file-1",
		Name:   "test.txt",
		Status: FileStatusOK,
	}

	if err := m.AddFile(entry); err != nil {
		t.Fatalf("failed to add file: %v", err)
	}

	if err := m.UpdateFileStatus("file-1", FileStatusBroken); err != nil {
		t.Fatalf("failed to update file status: %v", err)
	}

	retrieved, _ := m.GetFile("file-1")
	if retrieved.Status != FileStatusBroken {
		t.Errorf("expected status 'broken', got '%s'", retrieved.Status)
	}
}

func TestManifest_ToJSON_FromJSON(t *testing.T) {
	m := NewManifest("test-session")
	_ = m.AddFile(FileEntry{
		ID:       "file-1",
		Name:     "test.txt",
		Path:     "test.txt",
		Size:     100,
		MimeType: "text/plain",
		Status:   FileStatusOK,
		AddedAt:  time.Now(),
	})

	data, err := m.ToJSON()
	if err != nil {
		t.Fatalf("failed to serialize manifest: %v", err)
	}

	restored, err := FromJSON(data)
	if err != nil {
		t.Fatalf("failed to deserialize manifest: %v", err)
	}

	if restored.SessionID != m.SessionID {
		t.Errorf("session ID mismatch: expected '%s', got '%s'", m.SessionID, restored.SessionID)
	}

	if restored.FileCount() != m.FileCount() {
		t.Errorf("file count mismatch: expected %d, got %d", m.FileCount(), restored.FileCount())
	}
}

func TestLoadSaveManifest(t *testing.T) {
	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "manifest-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	manifestPath := filepath.Join(tmpDir, "manifest.json")

	// Create and save manifest
	m := NewManifest("test-session")
	_ = m.AddFile(FileEntry{
		ID:       "file-1",
		Name:     "test.txt",
		Path:     "test.txt",
		Size:     100,
		MimeType: "text/plain",
		Status:   FileStatusOK,
		AddedAt:  time.Now(),
	})

	if err := SaveManifest(m, manifestPath); err != nil {
		t.Fatalf("failed to save manifest: %v", err)
	}

	// Load manifest
	loaded, err := LoadManifest(manifestPath)
	if err != nil {
		t.Fatalf("failed to load manifest: %v", err)
	}

	if loaded.SessionID != m.SessionID {
		t.Errorf("session ID mismatch: expected '%s', got '%s'", m.SessionID, loaded.SessionID)
	}

	if loaded.FileCount() != m.FileCount() {
		t.Errorf("file count mismatch: expected %d, got %d", m.FileCount(), loaded.FileCount())
	}
}

func TestLoadManifest_NotFound(t *testing.T) {
	_, err := LoadManifest("/nonexistent/path/manifest.json")
	if err == nil {
		t.Error("expected error when loading nonexistent manifest")
	}
}
