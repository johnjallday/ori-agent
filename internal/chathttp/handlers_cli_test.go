package chathttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/client"
	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/toolapi"
	"github.com/johnjallday/ori-agent/internal/types"
)

// scriptedChatProvider answers each Chat call with the next scripted
// response and records every request, standing in for a CLI provider whose
// tool calls arrive already decoded.
type scriptedChatProvider struct {
	name          string
	supportsTools bool
	responses     []llm.ChatResponse
	requests      []llm.ChatRequest
}

func (p *scriptedChatProvider) Chat(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	p.requests = append(p.requests, req)
	if len(p.requests) > len(p.responses) {
		return nil, fmt.Errorf("%s: unexpected request %d", p.name, len(p.requests))
	}
	resp := p.responses[len(p.requests)-1]
	return &resp, nil
}

func (p *scriptedChatProvider) StreamChat(context.Context, llm.ChatRequest) (llm.StreamReader, error) {
	return nil, errors.New("not implemented")
}

func (p *scriptedChatProvider) Name() string { return p.name }

func (p *scriptedChatProvider) Type() llm.ProviderType { return llm.ProviderTypeCloud }

func (p *scriptedChatProvider) Capabilities() llm.ProviderCapabilities {
	return llm.ProviderCapabilities{
		SupportsTools:        p.supportsTools,
		SupportsSystemPrompt: true,
	}
}

func (p *scriptedChatProvider) ValidateConfig(llm.ProviderConfig) error { return nil }

func (p *scriptedChatProvider) DefaultModels() []string { return nil }

// cliChatCases are the two CLI providers' workspace chat routes.
var cliChatCases = []struct{ provider, model string }{
	{provider: "codex", model: "gpt-5.3-codex"},
	{provider: "claude_code", model: "sonnet"},
}

// newCLIChatTestHandler creates one agent on the given CLI provider, a
// handler whose factory serves the scripted provider under that name, and a
// "record_fact" tool that records the arguments it was called with.
func newCLIChatTestHandler(t *testing.T, providerName, model string, provider *scriptedChatProvider) (*Handler, *[]string) {
	t.Helper()
	st, err := store.NewFileStore(filepath.Join(t.TempDir(), "agents_index.json"), types.Settings{
		Model:       "gpt-5-nano",
		Temperature: 1.0,
	})
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	if err := st.CreateAgent("cli-agent", &store.CreateAgentConfig{
		Model:        model,
		LLMProvider:  providerName,
		Temperature:  1.0,
		SystemPrompt: "You are helpful.",
	}); err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}
	ag, ok := st.GetAgent("cli-agent")
	if !ok || ag == nil {
		t.Fatal("expected cli-agent")
	}
	ag.Settings.Provider = providerName
	ag.Settings.Model = model
	ag.Settings.ReasoningEffort = "high"
	if err := st.SetAgent("cli-agent", ag); err != nil {
		t.Fatalf("failed to update agent: %v", err)
	}

	recorded := []string{}
	registry := &UtilityToolRegistry{tools: map[string]toolapi.Tool{}}
	registry.register("record_fact", &nativeUtilityTool{
		definition: toolapi.ToolDefinition{
			Name:        "record_fact",
			Description: "Record a fact for this workspace.",
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{"fact": map[string]any{"type": "string"}},
			},
		},
		call: func(_ context.Context, args string) (string, error) {
			recorded = append(recorded, args)
			return `{"recorded":true}`, nil
		},
	})

	h := NewHandler(st, client.NewFactory(""))
	h.SetUtilityToolRegistry(registry)
	factory := llm.NewFactory()
	factory.Register(providerName, provider)
	h.SetLLMFactory(factory)
	return h, &recorded
}

func postCLIChat(t *testing.T, h *Handler, question, sessionID string) map[string]any {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"question":   question,
		"agent_name": "cli-agent",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/chat", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if sessionID != "" {
		req.Header.Set("X-Session-ID", sessionID)
	}
	rr := httptest.NewRecorder()
	h.ChatHandler(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var payload map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	return payload
}

func requestHasTool(req llm.ChatRequest, name string) bool {
	for _, tool := range req.Tools {
		if tool.Name == name {
			return true
		}
	}
	return false
}

func TestChatHandler_CLIToolCallRunsThroughSharedToolLoop(t *testing.T) {
	for _, tc := range cliChatCases {
		t.Run(tc.provider, func(t *testing.T) {
			callID := tc.provider + "_tool_1"
			provider := &scriptedChatProvider{
				name:          tc.provider,
				supportsTools: true,
				responses: []llm.ChatResponse{
					{
						FinishReason: llm.FinishReasonToolCalls,
						ToolCalls: []llm.ToolCall{{
							ID:        callID,
							Name:      "record_fact",
							Arguments: `{"fact":"the song is in D minor"}`,
						}},
					},
					{Content: "Noted: the song is in D minor."},
				},
			}
			h, recorded := newCLIChatTestHandler(t, tc.provider, tc.model, provider)

			payload := postCLIChat(t, h, "Remember for this workspace: the song is in D minor", "")

			if len(*recorded) != 1 || (*recorded)[0] != `{"fact":"the song is in D minor"}` {
				t.Fatalf("expected record_fact to run once with the model's arguments, got %#v", *recorded)
			}
			if len(provider.requests) != 2 {
				t.Fatalf("expected an initial and a follow-up request, got %d", len(provider.requests))
			}

			initial := provider.requests[0]
			if !requestHasTool(initial, "record_fact") {
				t.Fatalf("expected the initial request to carry the chat tools, got %#v", initial.Tools)
			}
			if !strings.Contains(initial.SystemPrompt, "Available tools: record_fact") {
				t.Fatalf("expected the system prompt to name the tools, got %q", initial.SystemPrompt)
			}
			if initial.ReasoningEffort != "high" {
				t.Fatalf("expected the agent's reasoning effort on the initial request, got %q", initial.ReasoningEffort)
			}

			followUp := provider.requests[1]
			if !requestHasTool(followUp, "record_fact") {
				t.Fatalf("expected the follow-up request to carry the tools, got %#v", followUp.Tools)
			}
			if followUp.ReasoningEffort != "high" {
				t.Fatalf("expected the follow-up request to carry the reasoning effort, got %q", followUp.ReasoningEffort)
			}
			if !strings.Contains(followUp.SystemPrompt, getFollowUpSystemPrompt()) {
				t.Fatalf("expected the follow-up prompt to append the shared follow-up guidance, got %q", followUp.SystemPrompt)
			}
			last := followUp.Messages[len(followUp.Messages)-1]
			if last.Role != llm.RoleTool || last.ToolCallID != callID || !strings.Contains(last.Content, `"recorded":true`) {
				t.Fatalf("expected the follow-up to end with the tool result, got %+v", last)
			}

			if got, _ := payload["response"].(string); got != "Noted: the song is in D minor." {
				t.Fatalf("expected the final answer, got %q", got)
			}
			toolCalls, _ := payload["toolCalls"].([]any)
			if len(toolCalls) != 1 {
				t.Fatalf("expected one toolCalls entry, got %#v", payload["toolCalls"])
			}
			call, _ := toolCalls[0].(map[string]any)
			if call["function"] != "record_fact" || call["success"] != true {
				t.Fatalf("expected a successful record_fact call, got %#v", call)
			}
			if count, _ := payload["action_count"].(float64); count != 1 {
				t.Fatalf("expected one action receipt, got %#v", payload["action_count"])
			}
		})
	}
}

func TestChatHandler_CLIPlainAnswerKeepsPayload(t *testing.T) {
	for _, tc := range cliChatCases {
		t.Run(tc.provider, func(t *testing.T) {
			provider := &scriptedChatProvider{
				name:          tc.provider,
				supportsTools: true,
				responses:     []llm.ChatResponse{{Content: "Hello there."}},
			}
			h, recorded := newCLIChatTestHandler(t, tc.provider, tc.model, provider)

			payload := postCLIChat(t, h, "hello", "")

			if len(*recorded) != 0 {
				t.Fatalf("expected no tool execution, got %#v", *recorded)
			}
			if len(provider.requests) != 1 {
				t.Fatalf("expected one request, got %d", len(provider.requests))
			}
			if !requestHasTool(provider.requests[0], "record_fact") {
				t.Fatalf("expected the request to offer the chat tools, got %#v", provider.requests[0].Tools)
			}
			if got, _ := payload["response"].(string); got != "Hello there." {
				t.Fatalf("expected the plain answer, got %q", got)
			}
			if got, _ := payload["route_mode"].(string); got != routeModeAssistantChat {
				t.Fatalf("expected assistant chat route mode, got %q", got)
			}
			for _, key := range []string{"toolCalls", "action_receipts", "dependency_resolution"} {
				if _, ok := payload[key]; ok {
					t.Fatalf("expected no %q in a plain answer, got %#v", key, payload[key])
				}
			}
		})
	}
}

// The router stores the user's message before dispatch; the CLI handler
// stores only the reply, so a turn adds each message to the session once.
func TestChatHandler_CLITurnStoresTheUserMessageOnce(t *testing.T) {
	for _, tc := range cliChatCases {
		t.Run(tc.provider, func(t *testing.T) {
			ctx := context.Background()
			db, err := database.Open(ctx, &database.Config{InMemory: true})
			if err != nil {
				t.Fatalf("failed to open test database: %v", err)
			}
			defer func() { _ = db.Close() }()
			sessionStore := session.NewHybridStoreWithDB(db, 10)
			now := time.Now()
			if err := sessionStore.CreateSession(ctx, &session.Session{ID: "turn-session", Title: "turn", AgentName: "cli-agent", CreatedAt: now, UpdatedAt: now}); err != nil {
				t.Fatalf("failed to create session: %v", err)
			}

			provider := &scriptedChatProvider{
				name:          tc.provider,
				supportsTools: true,
				responses:     []llm.ChatResponse{{Content: "Hi!"}},
			}
			h, _ := newCLIChatTestHandler(t, tc.provider, tc.model, provider)
			h.SetSessionStore(sessionStore)

			postCLIChat(t, h, "hello there", "turn-session")

			messages, err := sessionStore.GetMessages(ctx, "turn-session")
			if err != nil {
				t.Fatalf("failed to read session: %v", err)
			}
			counts := map[session.MessageRole]int{}
			for _, msg := range messages {
				counts[msg.Role]++
			}
			if len(messages) != 2 || counts[session.RoleUser] != 1 || counts[session.RoleAssistant] != 1 {
				t.Fatalf("expected one user and one assistant message, got %+v", messages)
			}
		})
	}
}
