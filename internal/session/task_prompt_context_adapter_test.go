package session

import (
	"context"
	"testing"
	"time"
)

func TestWorkspaceTaskContextAdapter_ListsWorkspaceNotesAndSessions(t *testing.T) {
	store, cleanup := setupHybridStore(t, 10)
	defer cleanup()

	ctx := context.Background()
	now := time.Date(2026, time.March, 8, 12, 0, 0, 0, time.UTC)

	for _, ws := range []*Workspace{
		{ID: "ws-1", Name: "Workspace 1", CreatedAt: now, UpdatedAt: now},
		{ID: "ws-2", Name: "Workspace 2", CreatedAt: now, UpdatedAt: now},
	} {
		if err := store.CreateWorkspace(ctx, ws); err != nil {
			t.Fatalf("failed to create workspace %q: %v", ws.ID, err)
		}
	}

	for _, note := range []*WorkspaceNote{
		{ID: "note-1", WorkspaceID: "ws-1", Name: "Alpha", Content: "Alpha preview text", CreatedAt: now, UpdatedAt: now},
		{ID: "note-2", WorkspaceID: "ws-1", Name: "Beta", Content: "Beta preview text", CreatedAt: now.Add(time.Minute), UpdatedAt: now.Add(time.Minute)},
		{ID: "note-3", WorkspaceID: "ws-2", Name: "Other", Content: "Other workspace note", CreatedAt: now, UpdatedAt: now},
	} {
		if err := store.CreateNote(ctx, note); err != nil {
			t.Fatalf("failed to create note %q: %v", note.ID, err)
		}
	}

	for _, sess := range []*Session{
		{ID: "session-1", Title: "Older", AgentName: "Ori", FolderID: "ws-1", CreatedAt: now, UpdatedAt: now},
		{ID: "session-2", Title: "Latest", AgentName: "Ori", FolderID: "ws-1", CreatedAt: now.Add(2 * time.Minute), UpdatedAt: now.Add(2 * time.Minute)},
		{ID: "session-3", Title: "Ignored", AgentName: "Reviewer", FolderID: "ws-2", CreatedAt: now, UpdatedAt: now},
	} {
		if err := store.CreateSession(ctx, sess); err != nil {
			t.Fatalf("failed to create session %q: %v", sess.ID, err)
		}
	}

	adapter := NewWorkspaceTaskContextAdapter(store)

	notes, err := adapter.ListNotesByWorkspace(ctx, "ws-1")
	if err != nil {
		t.Fatalf("ListNotesByWorkspace failed: %v", err)
	}
	if len(notes) != 2 {
		t.Fatalf("expected 2 notes, got %d", len(notes))
	}
	if notes[0].Name != "Beta" {
		t.Fatalf("expected newest note first, got %q", notes[0].Name)
	}

	sessions, total, err := adapter.ListSessionsByWorkspace(ctx, "ws-1", 1)
	if err != nil {
		t.Fatalf("ListSessionsByWorkspace failed: %v", err)
	}
	if total != 2 {
		t.Fatalf("expected total session count 2, got %d", total)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected limited session list of size 1, got %d", len(sessions))
	}
	if sessions[0].Title != "Latest" {
		t.Fatalf("expected most recent session first, got %q", sessions[0].Title)
	}
	if sessions[0].AgentName != "Ori" {
		t.Fatalf("expected agent name Ori, got %q", sessions[0].AgentName)
	}
}
