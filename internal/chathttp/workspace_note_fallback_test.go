package chathttp

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/openai/openai-go/v3"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type workspaceNoteFallbackFixture struct {
	ctx          context.Context
	handler      *Handler
	agent        *resolvedChatAgent
	sessionStore session.HybridStore
	workspaceID  string
}

func newWorkspaceNoteFallbackFixture(t *testing.T) workspaceNoteFallbackFixture {
	ctx := context.Background()
	db, err := database.Open(ctx, &database.Config{InMemory: true})
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	sessionStore := session.NewHybridStoreWithDB(db, 10)
	wsStore := workspace.NewInMemoryStore()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{
		Name:   "Travel",
		Agents: []string{"Ori"},
	})
	if err := wsStore.Save(ws); err != nil {
		t.Fatalf("failed to save workspace: %v", err)
	}
	if err := sessionStore.CreateWorkspace(ctx, &session.Workspace{
		ID:        ws.ID,
		Name:      ws.Name,
		CreatedAt: ws.CreatedAt,
		UpdatedAt: ws.UpdatedAt,
	}); err != nil {
		t.Fatalf("failed to seed session workspace: %v", err)
	}

	h := NewHandler(newPreflightStore("Ori", &agent.Agent{}), nil)
	h.SetSessionStore(sessionStore)
	h.SetWorkspaceStore(wsStore)

	return workspaceNoteFallbackFixture{
		ctx:     ctx,
		handler: h,
		agent: &resolvedChatAgent{
			Agent:          &agent.Agent{},
			WorkspaceTools: NewWorkspaceToolProvider(sessionStore, wsStore, ws.ID),
		},
		sessionStore: sessionStore,
		workspaceID:  ws.ID,
	}
}

func createWorkspaceNoteForTest(t *testing.T, fixture workspaceNoteFallbackFixture, id, name, content string, updatedAt time.Time) {
	t.Helper()

	if err := fixture.sessionStore.CreateNote(fixture.ctx, &session.WorkspaceNote{
		ID:          id,
		WorkspaceID: fixture.workspaceID,
		Name:        name,
		Content:     content,
		CreatedAt:   updatedAt.Add(-time.Minute),
		UpdatedAt:   updatedAt,
	}); err != nil {
		t.Fatalf("failed to seed workspace note %q: %v", name, err)
	}
}

func TestMaybeHandleWorkspaceSaveNoteWithoutModel_SavesLatestAssistantReply(t *testing.T) {
	fixture := newWorkspaceNoteFallbackFixture(t)
	fixture.agent.Messages = append(fixture.agent.Messages, openai.AssistantMessage("Things to Buy in Spain\n\n- Jamon iberico\n- Olive oil"))

	rec := httptest.NewRecorder()
	handled := fixture.handler.maybeHandleWorkspaceSaveNoteWithoutModel(rec, fixture.agent, "Ori", "I like it, store it to a note", fixture.ctx, "", nil)
	if !handled {
		t.Fatal("expected save-note fallback to handle the request")
	}

	notes, err := fixture.sessionStore.ListNotesByWorkspace(fixture.ctx, fixture.workspaceID)
	if err != nil {
		t.Fatalf("failed to list notes: %v", err)
	}
	if len(notes) != 1 {
		t.Fatalf("expected 1 saved note, got %d", len(notes))
	}

	note, err := fixture.sessionStore.GetNote(fixture.ctx, notes[0].ID)
	if err != nil {
		t.Fatalf("failed to load saved note: %v", err)
	}
	if note.Name != "Things to Buy in Spain" {
		t.Fatalf("expected derived note name, got %q", note.Name)
	}
	if !strings.Contains(note.Content, "Olive oil") {
		t.Fatalf("expected note content to include assistant reply, got %q", note.Content)
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response payload: %v", err)
	}
	responseText, _ := payload["response"].(string)
	if !strings.Contains(responseText, `Saved the previous answer to note "Things to Buy in Spain".`) {
		t.Fatalf("unexpected response text: %q", responseText)
	}
}

func TestMaybeHandleWorkspaceSaveNoteWithoutModel_AppendsToMostRecentNoteOnAffirmation(t *testing.T) {
	fixture := newWorkspaceNoteFallbackFixture(t)
	createWorkspaceNoteForTest(t, fixture, "madrid-trip", "Madrid Trip", "# Existing plan", time.Now())
	fixture.agent.Messages = append(fixture.agent.Messages, openai.AssistantMessage("Tapas Spots in Madrid\n\n- Bodega de la Ardosa\n- Casa Revuelta\n\nWant me to add this to your Madrid trip note?"))

	rec := httptest.NewRecorder()
	handled := fixture.handler.maybeHandleWorkspaceSaveNoteWithoutModel(rec, fixture.agent, "Ori", "yes", fixture.ctx, "", nil)
	if !handled {
		t.Fatal("expected append-note fallback to handle the request")
	}

	note, err := fixture.sessionStore.GetNote(fixture.ctx, "madrid-trip")
	if err != nil {
		t.Fatalf("failed to load updated note: %v", err)
	}
	if !strings.Contains(note.Content, "# Existing plan") {
		t.Fatalf("expected note to preserve existing content, got %q", note.Content)
	}
	if !strings.Contains(note.Content, "Casa Revuelta") {
		t.Fatalf("expected note to include assistant content, got %q", note.Content)
	}
	if strings.Contains(note.Content, "Want me to add this") {
		t.Fatalf("expected prompt text to be stripped from appended note content, got %q", note.Content)
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response payload: %v", err)
	}
	responseText, _ := payload["response"].(string)
	if !strings.Contains(responseText, `Added the previous answer to note "Madrid Trip".`) {
		t.Fatalf("unexpected response text: %q", responseText)
	}
}

func TestMaybeHandleWorkspaceSaveNoteWithoutModel_CreatesSeparateNoteFromFollowUp(t *testing.T) {
	fixture := newWorkspaceNoteFallbackFixture(t)
	createWorkspaceNoteForTest(t, fixture, "madrid-trip", "Madrid Trip", "# Existing plan", time.Now())
	fixture.agent.Messages = append(fixture.agent.Messages, openai.AssistantMessage("San Sebastian Food Guide\n\n- Ganbara\n- Bar Nestor\n\nWant me to add this to your Madrid trip note? Or start a separate note?"))

	rec := httptest.NewRecorder()
	handled := fixture.handler.maybeHandleWorkspaceSaveNoteWithoutModel(rec, fixture.agent, "Ori", "start another note", fixture.ctx, "", nil)
	if !handled {
		t.Fatal("expected separate-note fallback to handle the request")
	}

	notes, err := fixture.sessionStore.ListNotesByWorkspace(fixture.ctx, fixture.workspaceID)
	if err != nil {
		t.Fatalf("failed to list notes: %v", err)
	}
	if len(notes) != 2 {
		t.Fatalf("expected 2 notes after creating a separate note, got %d", len(notes))
	}

	var savedID string
	for _, note := range notes {
		if note.ID != "madrid-trip" {
			savedID = note.ID
			break
		}
	}
	if savedID == "" {
		t.Fatal("expected a newly created note")
	}

	savedNote, err := fixture.sessionStore.GetNote(fixture.ctx, savedID)
	if err != nil {
		t.Fatalf("failed to load saved note: %v", err)
	}
	if savedNote.Name != "San Sebastian Food Guide" {
		t.Fatalf("expected derived note name, got %q", savedNote.Name)
	}
	if strings.Contains(savedNote.Content, "Want me to add this") {
		t.Fatalf("expected prompt text to be stripped from new note content, got %q", savedNote.Content)
	}
	if !strings.Contains(savedNote.Content, "Bar Nestor") {
		t.Fatalf("expected saved note to include assistant content, got %q", savedNote.Content)
	}

	original, err := fixture.sessionStore.GetNote(fixture.ctx, "madrid-trip")
	if err != nil {
		t.Fatalf("failed to reload original note: %v", err)
	}
	if original.Content != "# Existing plan" {
		t.Fatalf("expected original note to remain unchanged, got %q", original.Content)
	}
}

func TestDetectWorkspaceNoteFallbackMode(t *testing.T) {
	latestAssistant := "Madrid Food Guide\n\n- Casa Dani\n\nWant me to add this to your Madrid trip note? Or start a separate note?"

	tests := []struct {
		name          string
		userMessage   string
		latestMessage string
		want          workspaceNoteFallbackMode
	}{
		{
			name:          "affirmation appends even when separate note is offered",
			userMessage:   "yes",
			latestMessage: latestAssistant,
			want:          workspaceNoteFallbackAppendRecent,
		},
		{
			name:          "separate note follow-up creates a new note",
			userMessage:   "separate note",
			latestMessage: latestAssistant,
			want:          workspaceNoteFallbackCreateNew,
		},
		{
			name:          "start another note creates a new note",
			userMessage:   "start another note",
			latestMessage: latestAssistant,
			want:          workspaceNoteFallbackCreateNew,
		},
		{
			name:          "unrelated follow-up ignored",
			userMessage:   "refine it",
			latestMessage: latestAssistant,
			want:          workspaceNoteFallbackNone,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := detectWorkspaceNoteFallbackMode(tt.userMessage, tt.latestMessage); got != tt.want {
				t.Fatalf("detectWorkspaceNoteFallbackMode(%q) = %q, want %q", tt.userMessage, got, tt.want)
			}
		})
	}
}

func TestMatchesWorkspaceSaveNoteIntent(t *testing.T) {
	tests := []struct {
		message string
		want    bool
	}{
		{message: "save it to a note", want: true},
		{message: "store this", want: true},
		{message: "remember that", want: true},
		{message: "refine the list", want: false},
		{message: "what else should I buy?", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.message, func(t *testing.T) {
			if got := matchesWorkspaceSaveNoteIntent(tt.message); got != tt.want {
				t.Fatalf("matchesWorkspaceSaveNoteIntent(%q) = %v, want %v", tt.message, got, tt.want)
			}
		})
	}
}
