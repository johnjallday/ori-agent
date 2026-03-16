package chathttp

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openai/openai-go/v3"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestMaybeHandleWorkspaceSaveNoteWithoutModel_SavesLatestAssistantReply(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, &database.Config{InMemory: true})
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}
	defer func() { _ = db.Close() }()

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

	ag := &resolvedChatAgent{
		Agent:          &agent.Agent{},
		WorkspaceTools: NewWorkspaceToolProvider(sessionStore, wsStore, ws.ID),
	}
	ag.Messages = append(ag.Messages, openai.AssistantMessage("Things to Buy in Spain\n\n- Jamon iberico\n- Olive oil"))

	rec := httptest.NewRecorder()
	handled := h.maybeHandleWorkspaceSaveNoteWithoutModel(rec, ag, "Ori", "I like it, store it to a note", ctx, "", nil)
	if !handled {
		t.Fatal("expected save-note fallback to handle the request")
	}

	notes, err := sessionStore.ListNotesByWorkspace(ctx, ws.ID)
	if err != nil {
		t.Fatalf("failed to list notes: %v", err)
	}
	if len(notes) != 1 {
		t.Fatalf("expected 1 saved note, got %d", len(notes))
	}

	note, err := sessionStore.GetNote(ctx, notes[0].ID)
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
