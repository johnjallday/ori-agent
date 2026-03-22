package chathttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/openai/openai-go/v3"

	"github.com/johnjallday/ori-agent/internal/client"
	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
)

type capturingClaudeCodeProvider struct {
	requests []llm.ChatRequest
}

func (m *capturingClaudeCodeProvider) Chat(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	m.requests = append(m.requests, req)
	return &llm.ChatResponse{
		Content:  "ok",
		Model:    "sonnet",
		Provider: "claude_code",
	}, nil
}

func (m *capturingClaudeCodeProvider) StreamChat(context.Context, llm.ChatRequest) (llm.StreamReader, error) {
	return nil, errors.New("not implemented")
}

func (m *capturingClaudeCodeProvider) Name() string { return "claude_code" }

func (m *capturingClaudeCodeProvider) Type() llm.ProviderType { return llm.ProviderTypeCloud }

func (m *capturingClaudeCodeProvider) Capabilities() llm.ProviderCapabilities {
	return llm.ProviderCapabilities{
		SupportsSystemPrompt: true,
		RequiresAPIKey:       false,
	}
}

func (m *capturingClaudeCodeProvider) ValidateConfig(llm.ProviderConfig) error { return nil }

func (m *capturingClaudeCodeProvider) DefaultModels() []string { return []string{"sonnet"} }

func TestBuildLLMConversationMessages_IncludesHistoryAndCurrentTurn(t *testing.T) {
	history := []openai.ChatCompletionMessageParamUnion{
		openai.SystemMessage("history-system"),
		openai.UserMessage("first question"),
		openai.AssistantMessage("first answer"),
	}

	got := buildLLMConversationMessages(history, "follow up", nil)
	if len(got) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(got))
	}
	if got[0].Role != llm.RoleSystem || got[0].Content != "history-system" {
		t.Fatalf("unexpected system message: %+v", got[0])
	}
	if got[1].Role != llm.RoleUser || got[1].Content != "first question" {
		t.Fatalf("unexpected first user message: %+v", got[1])
	}
	if got[2].Role != llm.RoleAssistant || got[2].Content != "first answer" {
		t.Fatalf("unexpected assistant message: %+v", got[2])
	}
	if got[3].Role != llm.RoleUser || got[3].Content != "follow up" {
		t.Fatalf("unexpected current user message: %+v", got[3])
	}
}

func TestChatHandler_ClaudeCodeUsesSessionHistoryPerSession(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, &database.Config{InMemory: true})
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}
	defer func() { _ = db.Close() }()

	sessionStore := session.NewHybridStoreWithDB(db, 10)
	seedSession := func(id, contentA, contentB string) {
		t.Helper()
		now := time.Now()
		if err := sessionStore.CreateSession(ctx, &session.Session{
			ID:        id,
			Title:     id,
			AgentName: "claude-agent",
			CreatedAt: now,
			UpdatedAt: now,
		}); err != nil {
			t.Fatalf("failed to create session %s: %v", id, err)
		}
		if err := sessionStore.AddMessage(ctx, id, &session.Message{Role: session.RoleUser, Content: contentA}); err != nil {
			t.Fatalf("failed to add user message for %s: %v", id, err)
		}
		if err := sessionStore.AddMessage(ctx, id, &session.Message{Role: session.RoleAssistant, Content: contentB}); err != nil {
			t.Fatalf("failed to add assistant message for %s: %v", id, err)
		}
	}
	seedSession("session-a", "trip to madrid", "What dates are you traveling?")
	seedSession("session-b", "favorite color is blue", "I noted that your favorite color is blue.")

	path := filepath.Join(t.TempDir(), "agents_index.json")
	st, err := store.NewFileStore(path, types.Settings{
		Model:       "gpt-5-nano",
		Temperature: 1.0,
	})
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	if err := st.CreateAgent("claude-agent", &store.CreateAgentConfig{
		Type:         "general",
		Model:        "sonnet",
		LLMProvider:  "claude_code",
		Temperature:  1.0,
		SystemPrompt: "You are helpful.",
	}); err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}
	ag, ok := st.GetAgent("claude-agent")
	if !ok || ag == nil {
		t.Fatalf("expected claude-agent")
	}
	ag.Settings.Provider = "claude_code"
	ag.Settings.Model = "sonnet"
	ag.Messages = []openai.ChatCompletionMessageParamUnion{
		openai.UserMessage("stale shared state"),
		openai.AssistantMessage("this should not leak"),
	}
	if err := st.SetAgent("claude-agent", ag); err != nil {
		t.Fatalf("failed to update agent: %v", err)
	}

	h := NewHandler(st, client.NewFactory(""))
	h.SetSessionStore(sessionStore)
	factory := llm.NewFactory()
	mockProvider := &capturingClaudeCodeProvider{}
	factory.Register("claude_code", mockProvider)
	h.SetLLMFactory(factory)

	send := func(sessionID, question string) {
		t.Helper()
		body, _ := json.Marshal(map[string]any{
			"question": question,
		})
		req := httptest.NewRequest(http.MethodPost, "/api/chat", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Session-ID", sessionID)
		rr := httptest.NewRecorder()
		h.ChatHandler(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("expected status 200 for %s, got %d body=%s", sessionID, rr.Code, rr.Body.String())
		}
	}

	send("session-a", "follow up a")
	send("session-b", "follow up b")

	if len(mockProvider.requests) != 2 {
		t.Fatalf("expected 2 provider requests, got %d", len(mockProvider.requests))
	}

	first := mockProvider.requests[0].Messages
	if len(first) != 3 {
		t.Fatalf("expected 3 messages in first request, got %d", len(first))
	}
	if first[0].Content != "trip to madrid" || first[1].Content != "What dates are you traveling?" || first[2].Content != "follow up a" {
		t.Fatalf("unexpected first request history: %+v", first)
	}

	second := mockProvider.requests[1].Messages
	if len(second) != 3 {
		t.Fatalf("expected 3 messages in second request, got %d", len(second))
	}
	if second[0].Content != "favorite color is blue" || second[1].Content != "I noted that your favorite color is blue." || second[2].Content != "follow up b" {
		t.Fatalf("unexpected second request history: %+v", second)
	}
	for _, msg := range second {
		if msg.Content == "stale shared state" || msg.Content == "this should not leak" {
			t.Fatalf("shared in-memory agent history leaked into session-scoped request: %+v", second)
		}
	}
}
