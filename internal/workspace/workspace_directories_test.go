package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAddDirectoryReference(t *testing.T) {
	ws := NewWorkspace(CreateWorkspaceParams{
		Name:        "Test Workspace",
		Description: "Test",
	})

	// Create a temporary directory for testing
	tmpDir, err := os.MkdirTemp("", "test-dir-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	dir := DirectoryReference{
		Name: "Test Dir",
		Path: tmpDir,
		X:    100,
		Y:    200,
	}

	err = ws.AddDirectoryReference(dir)
	if err != nil {
		t.Fatalf("Failed to add directory reference: %v", err)
	}

	if len(ws.DirectoryReferences) != 1 {
		t.Errorf("Expected 1 directory reference, got %d", len(ws.DirectoryReferences))
	}

	added := ws.DirectoryReferences[0]
	if added.Name != "Test Dir" {
		t.Errorf("Expected name 'Test Dir', got '%s'", added.Name)
	}
	if added.Path != tmpDir {
		t.Errorf("Expected path '%s', got '%s'", tmpDir, added.Path)
	}
	if added.ID == "" {
		t.Error("Expected ID to be set")
	}
	if added.WorkspaceID != ws.ID {
		t.Errorf("Expected workspace ID '%s', got '%s'", ws.ID, added.WorkspaceID)
	}
}

func TestAddDirectoryReference_InvalidPath(t *testing.T) {
	ws := NewWorkspace(CreateWorkspaceParams{
		Name: "Test Workspace",
	})

	dir := DirectoryReference{
		Name: "Test Dir",
		Path: "/nonexistent/path/that/does/not/exist",
	}

	err := ws.AddDirectoryReference(dir)
	if err == nil {
		t.Error("Expected error for nonexistent path")
	}
}

func TestAddDirectoryReference_NotADirectory(t *testing.T) {
	ws := NewWorkspace(CreateWorkspaceParams{
		Name: "Test Workspace",
	})

	// Create a temporary file (not a directory)
	tmpFile, err := os.CreateTemp("", "test-file-*")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer func() { _ = os.Remove(tmpFile.Name()) }()
	_ = tmpFile.Close()

	dir := DirectoryReference{
		Name: "Test Dir",
		Path: tmpFile.Name(),
	}

	err = ws.AddDirectoryReference(dir)
	if err == nil {
		t.Error("Expected error for file path (not directory)")
	}
}

func TestGetDirectoryReference(t *testing.T) {
	ws := NewWorkspace(CreateWorkspaceParams{
		Name: "Test Workspace",
	})

	tmpDir, err := os.MkdirTemp("", "test-dir-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	dir := DirectoryReference{
		ID:   "test-id",
		Name: "Test Dir",
		Path: tmpDir,
	}

	err = ws.AddDirectoryReference(dir)
	if err != nil {
		t.Fatalf("Failed to add directory reference: %v", err)
	}

	retrieved, err := ws.GetDirectoryReference("test-id")
	if err != nil {
		t.Fatalf("Failed to get directory reference: %v", err)
	}

	if retrieved.Name != "Test Dir" {
		t.Errorf("Expected name 'Test Dir', got '%s'", retrieved.Name)
	}
}

func TestDeleteDirectoryReference(t *testing.T) {
	ws := NewWorkspace(CreateWorkspaceParams{
		Name: "Test Workspace",
	})

	tmpDir, err := os.MkdirTemp("", "test-dir-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	dir := DirectoryReference{
		ID:   "test-id",
		Name: "Test Dir",
		Path: tmpDir,
	}

	err = ws.AddDirectoryReference(dir)
	if err != nil {
		t.Fatalf("Failed to add directory reference: %v", err)
	}

	err = ws.DeleteDirectoryReference("test-id")
	if err != nil {
		t.Fatalf("Failed to delete directory reference: %v", err)
	}

	if len(ws.DirectoryReferences) != 0 {
		t.Errorf("Expected 0 directory references, got %d", len(ws.DirectoryReferences))
	}
}

func TestListDirectoryFiles(t *testing.T) {
	ws := NewWorkspace(CreateWorkspaceParams{
		Name: "Test Workspace",
	})

	// Create a temporary directory with some files
	tmpDir, err := os.MkdirTemp("", "test-dir-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Create test files
	testFiles := []string{"file1.txt", "file2.go", "file3.md"}
	for _, name := range testFiles {
		filePath := filepath.Join(tmpDir, name)
		if err := os.WriteFile(filePath, []byte("test content"), 0644); err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}
	}

	// Create a subdirectory
	subDir := filepath.Join(tmpDir, "subdir")
	if err := os.Mkdir(subDir, 0755); err != nil {
		t.Fatalf("Failed to create subdir: %v", err)
	}

	dir := DirectoryReference{
		ID:   "test-id",
		Name: "Test Dir",
		Path: tmpDir,
	}

	err = ws.AddDirectoryReference(dir)
	if err != nil {
		t.Fatalf("Failed to add directory reference: %v", err)
	}

	files, err := ws.ListDirectoryFiles("test-id")
	if err != nil {
		t.Fatalf("Failed to list files: %v", err)
	}

	// Should have 3 files + 1 subdir = 4 entries
	if len(files) != 4 {
		t.Errorf("Expected 4 files/dirs, got %d", len(files))
	}

	// Check that subdir is marked as directory
	foundSubdir := false
	for _, f := range files {
		if f.Name == "subdir" {
			foundSubdir = true
			if !f.IsDir {
				t.Error("Expected subdir to be marked as directory")
			}
		}
	}
	if !foundSubdir {
		t.Error("Expected to find subdir in file list")
	}
}

func TestReadDirectoryFile(t *testing.T) {
	ws := NewWorkspace(CreateWorkspaceParams{
		Name: "Test Workspace",
	})

	tmpDir, err := os.MkdirTemp("", "test-dir-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Create a test file with content
	testContent := "Hello, World!"
	testFilePath := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFilePath, []byte(testContent), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	dir := DirectoryReference{
		ID:   "test-id",
		Name: "Test Dir",
		Path: tmpDir,
	}

	err = ws.AddDirectoryReference(dir)
	if err != nil {
		t.Fatalf("Failed to add directory reference: %v", err)
	}

	content, err := ws.ReadDirectoryFile("test-id", "test.txt")
	if err != nil {
		t.Fatalf("Failed to read file: %v", err)
	}

	if string(content) != testContent {
		t.Errorf("Expected content '%s', got '%s'", testContent, string(content))
	}
}

func TestReadDirectoryFile_PathTraversal(t *testing.T) {
	ws := NewWorkspace(CreateWorkspaceParams{
		Name: "Test Workspace",
	})

	tmpDir, err := os.MkdirTemp("", "test-dir-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	dir := DirectoryReference{
		ID:   "test-id",
		Name: "Test Dir",
		Path: tmpDir,
	}

	err = ws.AddDirectoryReference(dir)
	if err != nil {
		t.Fatalf("Failed to add directory reference: %v", err)
	}

	// Attempt path traversal
	_, err = ws.ReadDirectoryFile("test-id", "../../../etc/passwd")
	if err == nil {
		t.Error("Expected error for path traversal attempt")
	}

	_, err = ws.ReadDirectoryFile("test-id", "../../secret.txt")
	if err == nil {
		t.Error("Expected error for path traversal attempt")
	}
}

func TestValidateRelativePath(t *testing.T) {
	tests := []struct {
		path    string
		wantErr bool
	}{
		{"file.txt", false},
		{"subdir/file.txt", false},
		{"deep/nested/path/file.txt", false},
		{"../escape.txt", true},
		{"subdir/../file.txt", false}, // This normalizes to "file.txt" which is safe
		{"/absolute/path", true},
		{"", true},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			err := validateRelativePath(tt.path)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateRelativePath(%q) error = %v, wantErr %v", tt.path, err, tt.wantErr)
			}
		})
	}
}
