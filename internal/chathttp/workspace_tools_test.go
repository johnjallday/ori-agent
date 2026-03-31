package chathttp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/session"
)

func setupWorkspaceToolSessionStore(t *testing.T) (session.HybridStore, func()) {
	t.Helper()

	db, err := database.Open(context.Background(), &database.Config{InMemory: true})
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}

	store := session.NewHybridStoreWithDB(db, 10)
	return store, func() { _ = store.Close() }
}

func TestWorkspaceSessionDetailResolvesUniqueTitleFallback(t *testing.T) {
	ctx := context.Background()
	sessionStore, cleanup := setupWorkspaceToolSessionStore(t)
	defer cleanup()

	workspaceID := "workspace-1"
	if err := sessionStore.CreateWorkspace(ctx, &session.Workspace{ID: workspaceID, Name: "Test"}); err != nil {
		t.Fatalf("failed to create workspace: %v", err)
	}

	sess := &session.Session{
		Title:     "Help me plan out 3 days in San Sebastian",
		AgentName: "spain Manager",
		FolderID:  workspaceID,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := sessionStore.CreateSession(ctx, sess); err != nil {
		t.Fatalf("failed to create session: %v", err)
	}
	if err := sessionStore.AddMessage(ctx, sess.ID, &session.Message{
		Role:      session.RoleUser,
		Content:   "Help me plan out 3 days in San Sebastian",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("failed to add message: %v", err)
	}

	provider := NewWorkspaceToolProvider(sessionStore, nil, workspaceID)
	tool := provider.readSessionDetailTool()
	result, err := tool.Call(ctx, `{"session_id":"Help me plan out 3 days in San Sebastian"}`)
	if err != nil {
		t.Fatalf("expected title fallback to succeed, got error: %v", err)
	}

	var payload struct {
		SessionID string `json:"session_id"`
		Title     string `json:"title"`
		Messages  []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		t.Fatalf("failed to decode result: %v", err)
	}

	if payload.SessionID != sess.ID {
		t.Fatalf("expected session id %q, got %q", sess.ID, payload.SessionID)
	}
	if payload.Title != sess.Title {
		t.Fatalf("expected title %q, got %q", sess.Title, payload.Title)
	}
	if len(payload.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(payload.Messages))
	}
}

func TestWorkspaceSessionDetailRejectsAmbiguousTitleFallback(t *testing.T) {
	ctx := context.Background()
	sessionStore, cleanup := setupWorkspaceToolSessionStore(t)
	defer cleanup()

	workspaceID := "workspace-2"
	if err := sessionStore.CreateWorkspace(ctx, &session.Workspace{ID: workspaceID, Name: "Test"}); err != nil {
		t.Fatalf("failed to create workspace: %v", err)
	}

	for i := 0; i < 2; i++ {
		sess := &session.Session{
			Title:     "Trip Planning",
			AgentName: "spain Manager",
			FolderID:  workspaceID,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		if err := sessionStore.CreateSession(ctx, sess); err != nil {
			t.Fatalf("failed to create session %d: %v", i, err)
		}
	}

	provider := NewWorkspaceToolProvider(sessionStore, nil, workspaceID)
	tool := provider.readSessionDetailTool()
	result, err := tool.Call(ctx, `{"session_id":"Trip Planning"}`)
	if err != nil {
		t.Fatalf("expected ambiguous title fallback to return guidance, got %v", err)
	}

	var payload struct {
		SessionFound     bool                     `json:"session_found"`
		RequestedSession string                   `json:"requested_session"`
		Message          string                   `json:"message"`
		Available        []map[string]interface{} `json:"available_sessions"`
	}
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		t.Fatalf("failed to decode result: %v", err)
	}

	if payload.SessionFound {
		t.Fatal("expected ambiguous title guidance payload, got resolved session")
	}
	if !strings.Contains(payload.Message, "Multiple workspace sessions share the title") {
		t.Fatalf("expected ambiguous title guidance, got %q", payload.Message)
	}
	if len(payload.Available) != 2 {
		t.Fatalf("expected 2 available sessions, got %d", len(payload.Available))
	}
}

func TestWorkspaceSessionDetailReturnsGuidanceForMissingSessionID(t *testing.T) {
	ctx := context.Background()
	sessionStore, cleanup := setupWorkspaceToolSessionStore(t)
	defer cleanup()

	workspaceID := "workspace-3"
	if err := sessionStore.CreateWorkspace(ctx, &session.Workspace{ID: workspaceID, Name: "Test"}); err != nil {
		t.Fatalf("failed to create workspace: %v", err)
	}

	sess := &session.Session{
		Title:     "San Sebastian Notes",
		AgentName: "spain Manager",
		FolderID:  workspaceID,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := sessionStore.CreateSession(ctx, sess); err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	provider := NewWorkspaceToolProvider(sessionStore, nil, workspaceID)
	tool := provider.readSessionDetailTool()
	result, err := tool.Call(ctx, `{"session_id":"123e4567-e89b-12d3-a456-426614174000"}`)
	if err != nil {
		t.Fatalf("expected missing session id to return guidance, got %v", err)
	}

	var payload struct {
		SessionFound     bool                     `json:"session_found"`
		RequestedSession string                   `json:"requested_session"`
		Message          string                   `json:"message"`
		Available        []map[string]interface{} `json:"available_sessions"`
	}
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		t.Fatalf("failed to decode result: %v", err)
	}

	if payload.SessionFound {
		t.Fatal("expected guidance payload, got resolved session")
	}
	if payload.RequestedSession != "123e4567-e89b-12d3-a456-426614174000" {
		t.Fatalf("unexpected requested session %q", payload.RequestedSession)
	}
	if !strings.Contains(payload.Message, "was not found in this workspace") {
		t.Fatalf("expected missing session guidance, got %q", payload.Message)
	}
	if len(payload.Available) != 1 {
		t.Fatalf("expected 1 available session, got %d", len(payload.Available))
	}
}

func TestWorkspaceNotesResolvesUniqueNameFallback(t *testing.T) {
	ctx := context.Background()
	sessionStore, cleanup := setupWorkspaceToolSessionStore(t)
	defer cleanup()

	workspaceID := "workspace-notes"
	if err := sessionStore.CreateWorkspace(ctx, &session.Workspace{ID: workspaceID, Name: "Notes"}); err != nil {
		t.Fatalf("failed to create workspace: %v", err)
	}

	note := &session.WorkspaceNote{
		ID:          "note-1",
		WorkspaceID: workspaceID,
		Name:        "Workspace Brief",
		Content:     "# Brief\n\nTrip planning context",
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if err := sessionStore.CreateNote(ctx, note); err != nil {
		t.Fatalf("failed to create note: %v", err)
	}

	provider := NewWorkspaceToolProvider(sessionStore, nil, workspaceID)
	tool := provider.readNotesTool()
	result, err := tool.Call(ctx, `{"note_id":"Workspace Brief"}`)
	if err != nil {
		t.Fatalf("expected name fallback to succeed, got error: %v", err)
	}

	var payload struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		t.Fatalf("failed to decode result: %v", err)
	}

	if payload.ID != note.ID {
		t.Fatalf("expected note id %q, got %q", note.ID, payload.ID)
	}
	if payload.Name != note.Name {
		t.Fatalf("expected note name %q, got %q", note.Name, payload.Name)
	}
	if payload.Content != note.Content {
		t.Fatalf("expected note content %q, got %q", note.Content, payload.Content)
	}
}

func TestWorkspaceNotesRejectsAmbiguousNameFallback(t *testing.T) {
	ctx := context.Background()
	sessionStore, cleanup := setupWorkspaceToolSessionStore(t)
	defer cleanup()

	workspaceID := "workspace-notes-ambiguous"
	if err := sessionStore.CreateWorkspace(ctx, &session.Workspace{ID: workspaceID, Name: "Notes"}); err != nil {
		t.Fatalf("failed to create workspace: %v", err)
	}

	for i := 0; i < 2; i++ {
		note := &session.WorkspaceNote{
			ID:          "note-" + string(rune('A'+i)),
			WorkspaceID: workspaceID,
			Name:        "Workspace Brief",
			Content:     "content",
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}
		if err := sessionStore.CreateNote(ctx, note); err != nil {
			t.Fatalf("failed to create note %d: %v", i, err)
		}
	}

	provider := NewWorkspaceToolProvider(sessionStore, nil, workspaceID)
	tool := provider.readNotesTool()
	_, err := tool.Call(ctx, `{"note_id":"Workspace Brief"}`)
	if err == nil {
		t.Fatal("expected ambiguous note name fallback to fail")
	}
	if !strings.Contains(err.Error(), "multiple workspace notes share the name") {
		t.Fatalf("expected ambiguous note name error, got %v", err)
	}
}
