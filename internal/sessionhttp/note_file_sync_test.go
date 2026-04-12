package sessionhttp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestNoteFilename(t *testing.T) {
	tests := []struct {
		name   string
		noteID string
		want   string
	}{
		{"Meeting Notes", "abc12345-6789-0000-0000-000000000000", "meeting-notes--abc12345.md"},
		{"Untitled Note", "deadbeef-1234-5678-9abc-def012345678", "untitled-note--deadbeef.md"},
		{"My API Keys", "12345678-abcd-efgh-ijkl-mnopqrstuvwx", "my-api-keys--12345678.md"},
	}

	for _, tt := range tests {
		got := workspace.NoteFilename(tt.name, tt.noteID)
		if got != tt.want {
			t.Errorf("noteFilename(%q, %q) = %q, want %q", tt.name, tt.noteID, got, tt.want)
		}
	}
}

func TestSyncNoteToFile(t *testing.T) {
	// Create a temp workspace folder structure
	dir := t.TempDir()
	store, err := workspace.NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	// Create a workspace
	now := time.Now()
	ws := &workspace.Workspace{
		ID:         "ws-test-123",
		Name:       "Test Workspace",
		Status:     workspace.StatusActive,
		CreatedAt:  now,
		UpdatedAt:  now,
		SharedData: make(map[string]interface{}),
	}
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}

	// Create a handler with the workspace store
	h := &Handler{workspaceStore: store}

	// Sync a note to file
	note := &session.WorkspaceNote{
		ID:          "note-abc12345",
		WorkspaceID: "ws-test-123",
		Name:        "Test Note",
		Content:     "# Hello\n\nThis is a test note.",
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	h.syncNoteToFile(note)

	// Verify the file was created
	folderPath, err := store.GetFolderPath("ws-test-123")
	if err != nil {
		t.Fatal(err)
	}

	expectedFile := filepath.Join(folderPath, workspace.NotesDir, "test-note--note-abc.md")
	data, err := os.ReadFile(expectedFile)
	if err != nil {
		t.Fatalf("Expected note file at %s: %v", expectedFile, err)
	}

	content := string(data)

	// Verify frontmatter
	if !strings.Contains(content, "---") {
		t.Error("Expected YAML frontmatter delimiters")
	}
	if !strings.Contains(content, `id: "note-abc12345"`) {
		t.Error("Expected note ID in frontmatter")
	}

	// Verify note content
	if !strings.Contains(content, "# Hello") {
		t.Error("Expected note content")
	}
	if !strings.Contains(content, "This is a test note.") {
		t.Error("Expected note content body")
	}
}

func TestSyncNoteToFileAfterRename(t *testing.T) {
	dir := t.TempDir()
	store, err := workspace.NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	now := time.Now()
	ws := &workspace.Workspace{
		ID:         "ws-rename-test",
		Name:       "Rename Test",
		Status:     workspace.StatusActive,
		CreatedAt:  now,
		UpdatedAt:  now,
		SharedData: make(map[string]interface{}),
	}
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}

	h := &Handler{workspaceStore: store}

	// Create initial note file
	note := &session.WorkspaceNote{
		ID:          "note-rename-123",
		WorkspaceID: "ws-rename-test",
		Name:        "Old Name",
		Content:     "Some content",
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	h.syncNoteToFile(note)

	folderPath, _ := store.GetFolderPath("ws-rename-test")
	oldFile := filepath.Join(folderPath, workspace.NotesDir, workspace.NoteFilename("Old Name", note.ID))

	// Verify old file exists
	if _, err := os.Stat(oldFile); err != nil {
		t.Fatalf("Old file should exist: %v", err)
	}

	// Rename the note
	note.Name = "New Name"
	note.UpdatedAt = time.Now()
	h.syncNoteToFileAfterRename(note, "Old Name")

	// Verify old file is removed
	if _, err := os.Stat(oldFile); !os.IsNotExist(err) {
		t.Error("Old file should be removed after rename")
	}

	// Verify new file exists
	newFile := filepath.Join(folderPath, workspace.NotesDir, workspace.NoteFilename("New Name", note.ID))
	if _, err := os.Stat(newFile); err != nil {
		t.Errorf("New file should exist: %v", err)
	}
}

func TestDeleteNoteFile(t *testing.T) {
	dir := t.TempDir()
	store, err := workspace.NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	now := time.Now()
	ws := &workspace.Workspace{
		ID:         "ws-delete-test",
		Name:       "Delete Test",
		Status:     workspace.StatusActive,
		CreatedAt:  now,
		UpdatedAt:  now,
		SharedData: make(map[string]interface{}),
	}
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}

	h := &Handler{workspaceStore: store}

	// Create a note file
	note := &session.WorkspaceNote{
		ID:          "note-delete-123",
		WorkspaceID: "ws-delete-test",
		Name:        "To Delete",
		Content:     "Will be deleted",
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	h.syncNoteToFile(note)

	folderPath, _ := store.GetFolderPath("ws-delete-test")
	noteFile := filepath.Join(folderPath, workspace.NotesDir, workspace.NoteFilename("To Delete", note.ID))

	// Verify file exists
	if _, err := os.Stat(noteFile); err != nil {
		t.Fatalf("Note file should exist before delete: %v", err)
	}

	// Delete the note file
	h.deleteNoteFile(note)

	// Verify file is removed
	if _, err := os.Stat(noteFile); !os.IsNotExist(err) {
		t.Error("Note file should be removed after delete")
	}
}

func TestSyncNoteToFile_NilWorkspaceStore(t *testing.T) {
	// Handler without workspace store should not panic
	h := &Handler{}
	note := &session.WorkspaceNote{
		ID:          "note-nil-test",
		WorkspaceID: "ws-nil",
		Name:        "Test",
		Content:     "Content",
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	h.syncNoteToFile(note)                   // Should be a no-op
	h.deleteNoteFile(note)                   // Should be a no-op
	h.syncNoteToFileAfterRename(note, "Old") // Should be a no-op
}
