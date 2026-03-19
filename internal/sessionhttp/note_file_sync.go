package sessionhttp

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// syncNoteToFile writes a note as a markdown file in the workspace's notes directory.
// The file is written with YAML frontmatter containing the note ID and timestamps,
// followed by the note content. This makes notes portable with the workspace folder.
func (h *Handler) syncNoteToFile(note *session.WorkspaceNote) {
	if h.workspaceStore == nil {
		return
	}

	folderPath, err := h.workspaceStore.GetFolderPath(note.WorkspaceID)
	if err != nil {
		logger.Debug("Cannot sync note to file: workspace folder not found", logger.Fields{
			"note_id":      note.ID,
			"workspace_id": note.WorkspaceID,
			"error":        err,
		})
		return
	}

	notesDir := filepath.Join(folderPath, workspace.NotesDir)
	if err := os.MkdirAll(notesDir, 0755); err != nil {
		logger.Error("Failed to create notes directory", logger.Fields{
			"path":  notesDir,
			"error": err,
		})
		return
	}

	filename := noteFilename(note.Name, note.ID)
	filePath := filepath.Join(notesDir, filename)

	// Build file content with YAML frontmatter
	var sb strings.Builder
	sb.WriteString("---\n")
	fmt.Fprintf(&sb, "id: %q\n", note.ID)
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

// syncNoteToFileAfterRename writes the note to its new file path and removes
// the old file if the name changed.
func (h *Handler) syncNoteToFileAfterRename(note *session.WorkspaceNote, oldName string) {
	if h.workspaceStore == nil {
		return
	}

	// If name changed, remove the old file first
	if oldName != "" && oldName != note.Name {
		h.deleteNoteFileByName(note.WorkspaceID, oldName, note.ID)
	}

	h.syncNoteToFile(note)
}

// deleteNoteFile removes a note's markdown file from the workspace's notes directory.
func (h *Handler) deleteNoteFile(note *session.WorkspaceNote) {
	h.deleteNoteFileByName(note.WorkspaceID, note.Name, note.ID)
}

// deleteNoteFileByName removes a note file given the workspace ID, note name, and note ID.
func (h *Handler) deleteNoteFileByName(workspaceID, noteName, noteID string) {
	if h.workspaceStore == nil {
		return
	}

	folderPath, err := h.workspaceStore.GetFolderPath(workspaceID)
	if err != nil {
		return
	}

	notesDir := filepath.Join(folderPath, workspace.NotesDir)
	filename := noteFilename(noteName, noteID)
	filePath := filepath.Join(notesDir, filename)

	if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
		logger.Error("Failed to delete note file", logger.Fields{
			"path":    filePath,
			"note_id": noteID,
			"error":   err,
		})
	}
}

// noteFilename generates a filename for a note.
// Format: {slugified-name}--{first 8 chars of ID}.md
// The ID suffix ensures uniqueness even if two notes share a name.
func noteFilename(name, id string) string {
	slug := workspace.Slugify(name)

	// Use first 8 chars of the ID for uniqueness
	shortID := id
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}

	return slug + "--" + shortID + ".md"
}
