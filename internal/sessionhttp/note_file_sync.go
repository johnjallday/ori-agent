package sessionhttp

import (
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// syncNoteToFile writes a note as a markdown file in the workspace's notes directory.
func (h *Handler) syncNoteToFile(note *session.WorkspaceNote) {
	workspace.SyncNoteFile(h.workspaceStore, workspace.NoteFileParams{
		ID:          note.ID,
		WorkspaceID: note.WorkspaceID,
		Name:        note.Name,
		Content:     note.Content,
		CreatedAt:   note.CreatedAt,
		UpdatedAt:   note.UpdatedAt,
	})
}

// syncNoteToFileAfterRename writes the note to its new file path and removes
// the old file if the name changed.
func (h *Handler) syncNoteToFileAfterRename(note *session.WorkspaceNote, oldName string) {
	if oldName != "" && oldName != note.Name {
		workspace.DeleteNoteFile(h.workspaceStore, note.WorkspaceID, note.ID, oldName)
	}
	h.syncNoteToFile(note)
}

// deleteNoteFile removes a note's markdown file from the workspace's notes directory.
func (h *Handler) deleteNoteFile(note *session.WorkspaceNote) {
	workspace.DeleteNoteFile(h.workspaceStore, note.WorkspaceID, note.ID, note.Name)
}
