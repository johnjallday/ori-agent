package chathttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/client"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
)

type mockClaudeCodeProvider struct {
	called bool
}

func (m *mockClaudeCodeProvider) Chat(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
	m.called = true
	return &llm.ChatResponse{
		Content:  "claude-code-ok",
		Model:    "sonnet",
		Provider: "claude_code",
	}, nil
}

func (m *mockClaudeCodeProvider) StreamChat(context.Context, llm.ChatRequest) (llm.StreamReader, error) {
	return nil, errors.New("not implemented")
}

func (m *mockClaudeCodeProvider) Name() string { return "claude_code" }

func (m *mockClaudeCodeProvider) Type() llm.ProviderType { return llm.ProviderTypeCloud }

func (m *mockClaudeCodeProvider) Capabilities() llm.ProviderCapabilities {
	return llm.ProviderCapabilities{
		SupportsSystemPrompt: true,
		RequiresAPIKey:       false,
	}
}

func (m *mockClaudeCodeProvider) ValidateConfig(llm.ProviderConfig) error { return nil }

func (m *mockClaudeCodeProvider) DefaultModels() []string { return []string{"sonnet"} }

func TestChatHandler_RoutesClaudeCodeWithoutOpenAIKey(t *testing.T) {
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
	current := "claude-agent"
	ag.Settings.Provider = "claude_code"
	ag.Settings.Model = "sonnet"
	ag.Settings.APIKey = ""
	if err := st.SetAgent(current, ag); err != nil {
		t.Fatalf("failed to update agent: %v", err)
	}

	h := NewHandler(st, client.NewFactory(""))
	factory := llm.NewFactory()
	mockProvider := &mockClaudeCodeProvider{}
	factory.Register("claude_code", mockProvider)
	h.SetLLMFactory(factory)

	body, _ := json.Marshal(map[string]any{
		"question":   "hello",
		"agent_name": "claude-agent",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/chat", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.ChatHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	if !mockProvider.called {
		t.Fatalf("expected claude_code provider to be called")
	}

	var resp map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if got, _ := resp["response"].(string); got != "claude-code-ok" {
		t.Fatalf("expected claude-code response, got %q", got)
	}
}

func TestChatHandler_RoutesAssistantModeToOriWithoutCurrentAgent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agents_index.json")
	st, err := store.NewFileStore(path, types.Settings{
		Model:       "gpt-5-nano",
		Temperature: 1.0,
	})
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	if err := st.CreateAgent(assistantExecutionAgentName, &store.CreateAgentConfig{
		Type:         "general",
		Model:        "sonnet",
		LLMProvider:  "claude_code",
		Temperature:  1.0,
		SystemPrompt: "You are helpful.",
	}); err != nil {
		t.Fatalf("failed to create assistant agent: %v", err)
	}

	ag, ok := st.GetAgent(assistantExecutionAgentName)
	if !ok || ag == nil {
		t.Fatalf("expected %s agent", assistantExecutionAgentName)
	}
	ag.Settings.Provider = "claude_code"
	ag.Settings.Model = "sonnet"
	ag.Settings.APIKey = ""
	if err := st.SetAgent(assistantExecutionAgentName, ag); err != nil {
		t.Fatalf("failed to update assistant agent: %v", err)
	}

	h := NewHandler(st, client.NewFactory(""))
	factory := llm.NewFactory()
	mockProvider := &mockClaudeCodeProvider{}
	factory.Register("claude_code", mockProvider)
	h.SetLLMFactory(factory)

	body, _ := json.Marshal(map[string]any{
		"question": "hello",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/chat", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.ChatHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if !mockProvider.called {
		t.Fatal("expected Assistant runtime provider to be called")
	}
}

func TestChatHandler_ReturnsErrorForProviderlessAgent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agents_index.json")
	st, err := store.NewFileStore(path, types.Settings{
		Model:       "gpt-5-nano",
		Temperature: 1.0,
	})
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	if err := st.CreateAgent("skills", &store.CreateAgentConfig{
		Type:        "tool-calling",
		Model:       "gpt-5-nano",
		Temperature: 1.0,
	}); err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	ag, ok := st.GetAgent("skills")
	if !ok || ag == nil {
		t.Fatalf("expected skills agent")
	}
	ag.Settings.Provider = ""
	ag.Settings.Model = "gpt-5-nano"
	if err := st.SetAgent("skills", ag); err != nil {
		t.Fatalf("failed to update agent: %v", err)
	}

	h := NewHandler(st, client.NewFactory("sk-test1234567890abcdefghijklmnopqrstuvwxyz"))

	body, _ := json.Marshal(map[string]any{
		"question":   "hello",
		"agent_name": "skills",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/chat", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.ChatHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rr.Code, rr.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	got, _ := resp["response"].(string)
	if !strings.Contains(got, "no provider configured") {
		t.Fatalf("expected provider configuration error, got %q", got)
	}
}
