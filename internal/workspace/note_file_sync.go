package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/logger"
)

// NoteFileParams contains the parameters needed to sync a note to disk.
type NoteFileParams struct {
	ID          string
	WorkspaceID string
	Name        string
	Content     string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// SyncNoteFile writes a note as a markdown file in the workspace's notes directory.
// The file contains YAML frontmatter (id, timestamps) followed by the note content.
// This is a no-op if the FileStore is nil or the workspace folder is not found.
func SyncNoteFile(fs *FileStore, note NoteFileParams) {
	if fs == nil {
		return
	}

	folderPath, err := fs.GetFolderPath(note.WorkspaceID)
	if err != nil {
		logger.Debug("Cannot sync note to file: workspace folder not found", logger.Fields{
			"note_id":      note.ID,
			"workspace_id": note.WorkspaceID,
			"error":        err,
		})
		return
	}

	notesDir := filepath.Join(folderPath, NotesDir)
	if err := os.MkdirAll(notesDir, 0755); err != nil {
		logger.Error("Failed to create notes directory", logger.Fields{
			"path":  notesDir,
			"error": err,
		})
		return
	}

	filename := NoteFilename(note.Name, note.ID)
	filePath := filepath.Join(notesDir, filename)

	// Ensure the file path stays within the notes directory
	if !strings.HasPrefix(filepath.Clean(filePath), filepath.Clean(notesDir)+string(filepath.Separator)) {
		logger.Error("Note filename escapes notes directory", logger.Fields{"filename": filename})
		return
	}

	// Build file content with YAML frontmatter
	var sb strings.Builder
	sb.WriteString("---\n")
	fmt.Fprintf(&sb, "id: %q\n", note.ID)
	fmt.Fprintf(&sb, "name: %q\n", note.Name)
	fmt.Fprintf(&sb, "created_at: %q\n", note.CreatedAt.Format("2006-01-02T15:04:05Z07:00"))
	fmt.Fprintf(&sb, "updated_at: %q\n", note.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"))
	sb.WriteString("---\n\n")
	sb.WriteString(note.Content)

	// Ensure content ends with a newline
	if !strings.HasSuffix(note.Content, "\n") {
		sb.WriteString("\n")
	}

	if err := os.WriteFile(filePath, []byte(sb.String()), 0644); err != nil {
		logger.Error("Failed to write note file", logger.Fields{
			"path":    filePath,
			"note_id": note.ID,
			"error":   err,
		})
		return
	}

	logger.Debug("Note synced to file", logger.Fields{
		"note_id": note.ID,
		"path":    filePath,
	})
}

// DeleteNoteFile removes a note's markdown file from the workspace's notes directory.
// This is a no-op if the FileStore is nil or the workspace folder is not found.
func DeleteNoteFile(fs *FileStore, workspaceID, noteID, noteName string) {
	if fs == nil {
		return
	}

	folderPath, err := fs.GetFolderPath(workspaceID)
	if err != nil {
		return
	}

	notesDir := filepath.Join(folderPath, NotesDir)
	filename := NoteFilename(noteName, noteID)
	filePath := filepath.Join(notesDir, filename)

	// Ensure the file path stays within the notes directory
	if !strings.HasPrefix(filepath.Clean(filePath), filepath.Clean(notesDir)+string(filepath.Separator)) {
		logger.Error("Note filename escapes notes directory", logger.Fields{"filename": filename})
		return
	}

	if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
		logger.Error("Failed to delete note file", logger.Fields{
			"path":    filePath,
			"note_id": noteID,
			"error":   err,
		})
	}
}

// NoteFilename generates a filename for a note.
// Format: {slugified-name}--{first 8 chars of ID}.md
func NoteFilename(name, id string) string {
	slug := Slugify(name)

	shortID := id
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}

	return slug + "--" + shortID + ".md"
}
