package session

import (
	"context"
	"fmt"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// TicketNoteLookup adapts this package's NoteStore to the narrow interface the
// canonical Ticket service depends on
// (tasks/prd-workspace-ticket-management.md FR-17, FR-71).
//
// The adapter lives here rather than in internal/workspace because this
// package already imports internal/workspace; taking the dependency the other
// way would be an import cycle. The workspace package declares what it needs
// and this package supplies it.
type TicketNoteLookup struct {
	notes NoteStore
}

// NewTicketNoteLookup wraps a NoteStore for Ticket note-link validation.
func NewTicketNoteLookup(notes NoteStore) *TicketNoteLookup {
	return &TicketNoteLookup{notes: notes}
}

// LookupNote returns the Note's identity for validation and display.
//
// It returns only ID, title, and owning workspace — never the body. A Ticket
// references a Note; it does not contain one, and widening this would make it
// easy to accidentally copy Note content into Ticket surfaces or Run context
// (FR-79).
func (l *TicketNoteLookup) LookupNote(ctx context.Context, noteID string) (workspace.TicketNoteRef, error) {
	if l == nil || l.notes == nil {
		return workspace.TicketNoteRef{}, fmt.Errorf("no note store configured")
	}
	note, err := l.notes.GetNote(ctx, noteID)
	if err != nil {
		return workspace.TicketNoteRef{}, err
	}
	if note == nil {
		return workspace.TicketNoteRef{}, ErrNoteNotFound
	}
	return workspace.TicketNoteRef{
		ID:          note.ID,
		Title:       note.Name,
		WorkspaceID: note.WorkspaceID,
	}, nil
}
