package chathttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/session"
)

// assistant-reads-projects FR41 to FR43: the Home's app assistant, chatting in
// one song's workspace, is given that song's saved facts in every turn, on every
// provider kind that carries a runtime prompt (the command-line providers
// included), built for the turn and never stored in the conversation.

const testSongFacts = "## Song Facts\n\nThe owner let Ori read this song's project file.\n\n<<<SONG-FACTS-abc123\nSong: Night Drive\n- Night Drive · 92 BPM\nSONG-FACTS-abc123>>>"

// songFactsReader answers for one workspace and one agent only, as the server's
// does, and counts how often it was asked.
type songFactsReader struct {
	mu        sync.Mutex
	workspace string
	agent     string
	text      string
	asked     []string
}

func (r *songFactsReader) SongFacts(_ context.Context, workspaceID, agentName string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.asked = append(r.asked, workspaceID+"|"+agentName)
	if workspaceID == r.workspace && agentName == r.agent {
		return r.text
	}
	return ""
}

func (r *songFactsReader) set(text string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.text = text
}

func postSongChat(t *testing.T, h *Handler, workspaceID, question, sessionID string) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"question": question, "agent_name": "cli-agent",
		"route_context": map[string]any{"surface": "workspace", "workspace_id": workspaceID},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/chat", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Session-ID", sessionID)
	rr := httptest.NewRecorder()
	h.ChatHandler(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
}

func TestChat_SongAssistantGetsFactsEveryTurnAndOtherAgentsNone(t *testing.T) {
	for _, tc := range cliChatCases {
		t.Run(tc.provider, func(t *testing.T) {
			ctx := context.Background()
			db, err := database.Open(ctx, &database.Config{InMemory: true})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			sessions := session.NewHybridStoreWithDB(db, 10)
			now := time.Now()
			if err := sessions.CreateSession(ctx, &session.Session{ID: "song-chat", Title: "song", AgentName: "cli-agent", CreatedAt: now, UpdatedAt: now}); err != nil {
				t.Fatal(err)
			}
			provider := &scriptedChatProvider{name: tc.provider, supportsTools: true,
				responses: []llm.ChatResponse{{Content: "First."}, {Content: "Second."}, {Content: "Third."}}}
			h, _ := newCLIChatTestHandler(t, tc.provider, tc.model, provider)
			h.SetSessionStore(sessions)
			reader := &songFactsReader{workspace: "song-a", agent: "cli-agent", text: testSongFacts}
			h.SetSongFactsReader(reader)

			// The assistant, in its song: the turn's prompt carries the facts.
			postSongChat(t, h, "song-a", "What tempo is this?", "song-chat")
			if prompt := provider.requests[0].SystemPrompt; !strings.Contains(prompt, "92 BPM") || !strings.Contains(prompt, "<<<SONG-FACTS-abc123") {
				t.Fatalf("the turn's prompt lacks the song's facts: %q", prompt)
			}

			// Never stored in the conversation: nothing the session holds has them.
			stored, err := sessions.GetMessages(ctx, "song-chat")
			if err != nil {
				t.Fatal(err)
			}
			for _, message := range stored {
				if strings.Contains(message.Content, "92 BPM") || strings.Contains(message.Content, "SONG-FACTS") {
					t.Fatalf("the facts were written into the conversation: %+v", message)
				}
			}

			// Built for each turn from what is saved at that moment: song details go
			// off, and the next turn has none, with the history carrying nothing.
			reader.set("")
			postSongChat(t, h, "song-a", "And now?", "song-chat")
			second := provider.requests[1]
			if strings.Contains(second.SystemPrompt, "92 BPM") {
				t.Fatalf("a later turn still carried the facts: %q", second.SystemPrompt)
			}
			for _, message := range second.Messages {
				if strings.Contains(message.Content, "92 BPM") {
					t.Fatalf("the history carried the facts: %+v", message)
				}
			}

			// Another song's workspace, or an agent that is not the assistant: none.
			reader.set(testSongFacts)
			postSongChat(t, h, "song-b", "Hello", "song-chat")
			if prompt := provider.requests[2].SystemPrompt; strings.Contains(prompt, "92 BPM") {
				t.Fatalf("another song's workspace was given the facts: %q", prompt)
			}
		})
	}
}

// With no reader wired, or no workspace in the route context, chat is unchanged.
func TestChat_NoSongFactsWithoutAReaderOrAWorkspace(t *testing.T) {
	h := &Handler{}
	if got := h.songFactsPromptSection(context.Background(), "song-a", "cli-agent"); got != "" {
		t.Fatalf("no reader gave %q", got)
	}
	h.SetSongFactsReader(&songFactsReader{workspace: "song-a", agent: "cli-agent", text: testSongFacts})
	for _, tc := range [][2]string{{"", "cli-agent"}, {"song-a", ""}, {"  ", "cli-agent"}} {
		if got := h.songFactsPromptSection(context.Background(), tc[0], tc[1]); got != "" {
			t.Fatalf("%q gave %q", tc, got)
		}
	}
	if got := h.songFactsPromptSection(context.Background(), "song-a", "cli-agent"); got != testSongFacts {
		t.Fatalf("the assistant in its song = %q", got)
	}
}
