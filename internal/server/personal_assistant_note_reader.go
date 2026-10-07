package server

import (
	"context"
	"errors"

	"github.com/johnjallday/ori-agent/internal/agenthttp"
	"github.com/johnjallday/ori-agent/internal/session"
)

// personalAssistantNoteStore is the part of the canonical session store the
// assistant panel may use for notes: two reads and nothing else.
type personalAssistantNoteStore interface {
	GetNote(ctx context.Context, id string) (*session.WorkspaceNote, error)
	ListNotesByWorkspace(ctx context.Context, workspaceID string) ([]session.WorkspaceNoteListItem, error)
}

// personalAssistantNoteAdapter exposes canonical notes to the panel's brokered
// readers. It reads the same rows the Notes page shows; there is no second
// note store, and no write, tag or search operation is reachable through it.
// The store's listing preview is dropped here: a listing carries titles only.
type personalAssistantNoteAdapter struct {
	store personalAssistantNoteStore
}

func (a personalAssistantNoteAdapter) Note(ctx context.Context, id string) (agenthttp.AssistantNote, error) {
	if a.store == nil {
		return agenthttp.AssistantNote{}, errors.New("note store unavailable")
	}
	note, err := a.store.GetNote(ctx, id)
	if errors.Is(err, session.ErrNoteNotFound) {
		return agenthttp.AssistantNote{}, agenthttp.ErrAssistantSourceNotFound
	}
	if err != nil || note == nil {
		return agenthttp.AssistantNote{}, errors.New("note read failed")
	}
	return agenthttp.AssistantNote{ID: note.ID, WorkspaceID: note.WorkspaceID, Name: note.Name, Content: note.Content, UpdatedAt: note.UpdatedAt}, nil
}

func (a personalAssistantNoteAdapter) Notes(ctx context.Context, workspaceID string) ([]agenthttp.AssistantNoteSummary, error) {
	if a.store == nil {
		return nil, errors.New("note store unavailable")
	}
	listed, err := a.store.ListNotesByWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, errors.New("note listing failed")
	}
	notes := make([]agenthttp.AssistantNoteSummary, 0, len(listed))
	for _, note := range listed {
		notes = append(notes, agenthttp.AssistantNoteSummary{ID: note.ID, WorkspaceID: note.WorkspaceID, Name: note.Name, UpdatedAt: note.UpdatedAt})
	}
	return notes, nil
}
