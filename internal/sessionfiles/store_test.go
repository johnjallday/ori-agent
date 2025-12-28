package sessionfiles

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupTestStore(t *testing.T) (*Store, string) {
	tmpDir, err := os.MkdirTemp("", "store-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	store, err := NewStore(tmpDir)
	if err != nil {
		os.RemoveAll(tmpDir)
		t.Fatalf("failed to create store: %v", err)
	}

	return store, tmpDir
}

func createTestFile(t *testing.T, dir, name, content string) string {
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}
	return path
}

func TestNewStore(t *testing.T) {
	store, tmpDir := setupTestStore(t)
	defer os.RemoveAll(tmpDir)

	if store == nil {
		t.Fatal("store should not be nil")
	}

	if store.basePath != tmpDir {
		t.Errorf("expected base path '%s', got '%s'", tmpDir, store.basePath)
	}
}

func TestStore_AddFile(t *testing.T) {
	store, tmpDir := setupTestStore(t)
	defer os.RemoveAll(tmpDir)

	// Create a source file
	srcPath := createTestFile(t, tmpDir, "source.txt", "hello world")

	// Add file to session
	entry, err := store.AddFile("session-1", srcPath, "test.txt")
	if err != nil {
		t.Fatalf("failed to add file: %v", err)
	}

	if entry.Name != "test.txt" {
		t.Errorf("expected name 'test.txt', got '%s'", entry.Name)
	}

	if entry.IsLink {
		t.Error("expected IsLink to be false for copied file")
	}

	if entry.Size != 11 { // "hello world" = 11 bytes
		t.Errorf("expected size 11, got %d", entry.Size)
	}

	// Verify file exists in session folder
	destPath := filepath.Join(store.getFilesPath("session-1"), "test.txt")
	if _, err := os.Stat(destPath); os.IsNotExist(err) {
		t.Error("file should exist in session folder")
	}
}

func TestStore_AddFileFromReader(t *testing.T) {
	store, tmpDir := setupTestStore(t)
	defer os.RemoveAll(tmpDir)

	content := "test content from reader"
	reader := strings.NewReader(content)

	entry, err := store.AddFileFromReader("session-1", reader, "reader-file.txt", int64(len(content)))
	if err != nil {
		t.Fatalf("failed to add file from reader: %v", err)
	}

	if entry.Name != "reader-file.txt" {
		t.Errorf("expected name 'reader-file.txt', got '%s'", entry.Name)
	}

	if entry.Size != int64(len(content)) {
		t.Errorf("expected size %d, got %d", len(content), entry.Size)
	}

	// Verify content
	destPath := filepath.Join(store.getFilesPath("session-1"), "reader-file.txt")
	data, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("failed to read destination file: %v", err)
	}

	if string(data) != content {
		t.Errorf("content mismatch: expected '%s', got '%s'", content, string(data))
	}
}

func TestStore_LinkFile(t *testing.T) {
	store, tmpDir := setupTestStore(t)
	defer os.RemoveAll(tmpDir)

	// Create a source file
	srcPath := createTestFile(t, tmpDir, "original.txt", "original content")

	// Link file to session
	entry, err := store.LinkFile("session-1", srcPath, "linked.txt")
	if err != nil {
		t.Fatalf("failed to link file: %v", err)
	}

	if entry.Name != "linked.txt" {
		t.Errorf("expected name 'linked.txt', got '%s'", entry.Name)
	}

	if !entry.IsLink {
		t.Error("expected IsLink to be true for linked file")
	}

	if entry.OriginalPath != srcPath {
		t.Errorf("expected original path '%s', got '%s'", srcPath, entry.OriginalPath)
	}

	// Verify symlink exists
	linkPath := filepath.Join(store.getFilesPath("session-1"), "linked.txt")
	info, err := os.Lstat(linkPath)
	if err != nil {
		t.Fatalf("failed to stat link: %v", err)
	}

	if info.Mode()&os.ModeSymlink == 0 {
		t.Error("expected symlink, got regular file")
	}
}

func TestStore_RemoveFile(t *testing.T) {
	store, tmpDir := setupTestStore(t)
	defer os.RemoveAll(tmpDir)

	// Create and add a file
	srcPath := createTestFile(t, tmpDir, "source.txt", "content")
	entry, _ := store.AddFile("session-1", srcPath, "test.txt")

	// Remove file
	if err := store.RemoveFile("session-1", entry.ID); err != nil {
		t.Fatalf("failed to remove file: %v", err)
	}

	// Verify file is removed from manifest
	files, _ := store.ListFiles("session-1")
	if len(files) != 0 {
		t.Errorf("expected 0 files after removal, got %d", len(files))
	}

	// Verify file is removed from disk
	destPath := filepath.Join(store.getFilesPath("session-1"), "test.txt")
	if _, err := os.Stat(destPath); !os.IsNotExist(err) {
		t.Error("file should be removed from disk")
	}
}

func TestStore_GetFile(t *testing.T) {
	store, tmpDir := setupTestStore(t)
	defer os.RemoveAll(tmpDir)

	srcPath := createTestFile(t, tmpDir, "source.txt", "content")
	entry, _ := store.AddFile("session-1", srcPath, "test.txt")

	retrieved, err := store.GetFile("session-1", entry.ID)
	if err != nil {
		t.Fatalf("failed to get file: %v", err)
	}

	if retrieved.Name != entry.Name {
		t.Errorf("expected name '%s', got '%s'", entry.Name, retrieved.Name)
	}
}

func TestStore_GetFile_NotFound(t *testing.T) {
	store, tmpDir := setupTestStore(t)
	defer os.RemoveAll(tmpDir)

	_, err := store.GetFile("session-1", "nonexistent")
	if err == nil {
		t.Error("expected error when getting nonexistent file")
	}
}

func TestStore_ListFiles(t *testing.T) {
	store, tmpDir := setupTestStore(t)
	defer os.RemoveAll(tmpDir)

	// Add multiple files
	srcPath1 := createTestFile(t, tmpDir, "source1.txt", "content1")
	srcPath2 := createTestFile(t, tmpDir, "source2.txt", "content2")

	store.AddFile("session-1", srcPath1, "file1.txt")
	store.AddFile("session-1", srcPath2, "file2.txt")

	files, err := store.ListFiles("session-1")
	if err != nil {
		t.Fatalf("failed to list files: %v", err)
	}

	if len(files) != 2 {
		t.Errorf("expected 2 files, got %d", len(files))
	}
}

func TestStore_ValidateLinks(t *testing.T) {
	store, tmpDir := setupTestStore(t)
	defer os.RemoveAll(tmpDir)

	// Create and link a file
	srcPath := createTestFile(t, tmpDir, "original.txt", "content")
	entry, _ := store.LinkFile("session-1", srcPath, "linked.txt")

	// Validate links (should be OK)
	brokenLinks, _ := store.ValidateLinks("session-1")
	if len(brokenLinks) != 0 {
		t.Errorf("expected 0 broken links, got %d", len(brokenLinks))
	}

	// Remove original file
	os.Remove(srcPath)

	// Validate links (should be broken)
	brokenLinks, _ = store.ValidateLinks("session-1")
	if len(brokenLinks) != 1 {
		t.Errorf("expected 1 broken link, got %d", len(brokenLinks))
	}

	if brokenLinks[0].ID != entry.ID {
		t.Errorf("expected broken link ID '%s', got '%s'", entry.ID, brokenLinks[0].ID)
	}
}

func TestStore_GetFilePath(t *testing.T) {
	store, tmpDir := setupTestStore(t)
	defer os.RemoveAll(tmpDir)

	// Test with copied file
	srcPath := createTestFile(t, tmpDir, "source.txt", "content")
	entry, _ := store.AddFile("session-1", srcPath, "test.txt")

	path, err := store.GetFilePath("session-1", entry.ID)
	if err != nil {
		t.Fatalf("failed to get file path: %v", err)
	}

	expectedPath := filepath.Join(store.getFilesPath("session-1"), "test.txt")
	if path != expectedPath {
		t.Errorf("expected path '%s', got '%s'", expectedPath, path)
	}
}

func TestStore_GetFilePath_Link(t *testing.T) {
	store, tmpDir := setupTestStore(t)
	defer os.RemoveAll(tmpDir)

	// Test with linked file
	srcPath := createTestFile(t, tmpDir, "original.txt", "content")
	entry, _ := store.LinkFile("session-1", srcPath, "linked.txt")

	path, err := store.GetFilePath("session-1", entry.ID)
	if err != nil {
		t.Fatalf("failed to get file path: %v", err)
	}

	if path != srcPath {
		t.Errorf("expected original path '%s', got '%s'", srcPath, path)
	}
}

func TestStore_MaxFileLimit(t *testing.T) {
	store, tmpDir := setupTestStore(t)
	defer os.RemoveAll(tmpDir)

	// Set a low limit for testing
	manifest := store.getOrCreateManifest("session-1")
	manifest.MaxFiles = 2

	srcPath1 := createTestFile(t, tmpDir, "source1.txt", "content1")
	srcPath2 := createTestFile(t, tmpDir, "source2.txt", "content2")
	srcPath3 := createTestFile(t, tmpDir, "source3.txt", "content3")

	store.AddFile("session-1", srcPath1, "file1.txt")
	store.AddFile("session-1", srcPath2, "file2.txt")

	// Third file should fail
	_, err := store.AddFile("session-1", srcPath3, "file3.txt")
	if err == nil {
		t.Error("expected error when exceeding max file limit")
	}
}

func TestStore_DeleteSession(t *testing.T) {
	store, tmpDir := setupTestStore(t)
	defer os.RemoveAll(tmpDir)

	// Add files to session
	srcPath := createTestFile(t, tmpDir, "source.txt", "content")
	store.AddFile("session-1", srcPath, "test.txt")

	// Verify session directory exists
	sessionPath := store.getSessionPath("session-1")
	if _, err := os.Stat(sessionPath); os.IsNotExist(err) {
		t.Fatal("session directory should exist")
	}

	// Delete session
	if err := store.DeleteSession("session-1"); err != nil {
		t.Fatalf("failed to delete session: %v", err)
	}

	// Verify session directory is removed
	if _, err := os.Stat(sessionPath); !os.IsNotExist(err) {
		t.Error("session directory should be removed")
	}
}

func TestStore_RelinkFile(t *testing.T) {
	store, tmpDir := setupTestStore(t)
	defer os.RemoveAll(tmpDir)

	// Create and link a file
	srcPath := createTestFile(t, tmpDir, "original.txt", "original content")
	entry, _ := store.LinkFile("session-1", srcPath, "linked.txt")

	// Create a new file to link to
	newPath := createTestFile(t, tmpDir, "new-original.txt", "new content")

	// Relink
	if err := store.RelinkFile("session-1", entry.ID, newPath); err != nil {
		t.Fatalf("failed to relink file: %v", err)
	}

	// Verify the link points to new file
	retrieved, _ := store.GetFile("session-1", entry.ID)
	if retrieved.OriginalPath != newPath {
		t.Errorf("expected original path '%s', got '%s'", newPath, retrieved.OriginalPath)
	}
}

func TestStore_RelinkFile_NotALink(t *testing.T) {
	store, tmpDir := setupTestStore(t)
	defer os.RemoveAll(tmpDir)

	// Add a copied file (not a link)
	srcPath := createTestFile(t, tmpDir, "source.txt", "content")
	entry, _ := store.AddFile("session-1", srcPath, "test.txt")

	// Try to relink (should fail)
	newPath := createTestFile(t, tmpDir, "new.txt", "new content")
	err := store.RelinkFile("session-1", entry.ID, newPath)
	if err == nil {
		t.Error("expected error when relinking a non-link file")
	}
}

func TestDetectMimeType(t *testing.T) {
	tests := []struct {
		filename string
		expected string
	}{
		{"test.txt", "text/plain; charset=utf-8"},
		{"image.png", "image/png"},
		{"document.pdf", "application/pdf"},
		{"script.js", "text/javascript; charset=utf-8"},
		{"data.json", "application/json"},
		{"unknown.qzx", "application/octet-stream"},
	}

	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			result := detectMimeType(tt.filename)
			if result != tt.expected {
				t.Errorf("detectMimeType(%s) = %s, want %s", tt.filename, result, tt.expected)
			}
		})
	}
}
