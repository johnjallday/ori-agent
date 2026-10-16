package llm

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	anthropicoption "github.com/anthropics/anthropic-sdk-go/option"
	"github.com/openai/openai-go/v3"
	openaioption "github.com/openai/openai-go/v3/option"
)

// The workspace goal must reach every provider's request (workspace-goal-
// suggestions SM1). Two shapes carry it:
//
//   - task execution renders it in the user turn, behind a system-role
//     message with the agent's prompt (internal/workspace task_handler.go);
//   - workspace chat puts the runtime context, goal included, in
//     ChatRequest.SystemPrompt (internal/chathttp composeRuntimeSystemPrompt).
//
// The Claude API provider drops system-role messages, so the task shape must
// never rely on one; these tests pin that the goal survives on the wire.
const wireGoal = "Keep the brand voice consistent across every channel"

func taskShapedGoalRequest(model string) ChatRequest {
	return ChatRequest{
		Model: model,
		Messages: []Message{
			NewSystemMessage("You are the Brand Desk manager."),
			NewUserMessage("# Task Assignment\n\n## Workspace Goal\n\nGoal: " + wireGoal + "\n\n## Task Description\n\nWhat should I work on?"),
		},
	}
}

func chatShapedGoalRequest(model string) ChatRequest {
	return ChatRequest{
		Model:        model,
		SystemPrompt: "You are the Brand Desk manager.\n\n---\n# Runtime Context\n## Workspace Snapshot\nWorkspace:\n- Goal: \"" + wireGoal + "\"",
		Messages:     []Message{NewUserMessage("What should I work on?")},
	}
}

func captureServer(t *testing.T, response string) (*httptest.Server, *[]byte) {
	t.Helper()
	var body []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, response)
	}))
	t.Cleanup(server.Close)
	return server, &body
}

func TestWorkspaceGoalReachesTheWire(t *testing.T) {
	const claudeReply = `{"id":"msg_1","type":"message","role":"assistant","model":"claude-fixture",
		"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`
	const openAIReply = `{"id":"chatcmpl-1","object":"chat.completion","created":0,"model":"gpt-fixture",
		"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],
		"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`
	const ollamaReply = `{"model":"llama-fixture","message":{"role":"assistant","content":"ok"},"done":true}`

	providers := []struct {
		name  string
		reply string
		model string
		build func(server *httptest.Server) Provider
	}{
		{"claude", claudeReply, "claude-fixture", func(server *httptest.Server) Provider {
			return &ClaudeProvider{apiKey: "test-key", httpClient: server.Client(), client: anthropic.NewClient(
				anthropicoption.WithBaseURL(server.URL), anthropicoption.WithHTTPClient(server.Client()),
				anthropicoption.WithAPIKey("test-key"), anthropicoption.WithMaxRetries(0))}
		}},
		{"openai", openAIReply, "gpt-fixture", func(server *httptest.Server) Provider {
			return &OpenAIProvider{apiKey: "test-key", httpClient: server.Client(), client: openai.NewClient(
				openaioption.WithBaseURL(server.URL), openaioption.WithHTTPClient(server.Client()),
				openaioption.WithAPIKey("test-key"), openaioption.WithMaxRetries(0))}
		}},
		{"ollama", ollamaReply, "llama-fixture", func(server *httptest.Server) Provider {
			return NewOllamaProvider(ProviderConfig{BaseURL: server.URL})
		}},
	}
	shapes := []struct {
		name    string
		request func(model string) ChatRequest
	}{
		{"task user turn", taskShapedGoalRequest},
		{"chat system prompt", chatShapedGoalRequest},
	}
	for _, provider := range providers {
		for _, shape := range shapes {
			t.Run(provider.name+"/"+shape.name, func(t *testing.T) {
				server, body := captureServer(t, provider.reply)
				if _, err := provider.build(server).Chat(t.Context(), shape.request(provider.model)); err != nil {
					t.Fatalf("Chat: %v", err)
				}
				if !strings.Contains(string(*body), wireGoal) {
					t.Fatalf("the workspace goal is missing from the %s request: %s", provider.name, *body)
				}
			})
		}
	}
}

// The CLI-agent adapters build one prompt string from the system prompt and
// the messages; the goal must be in it for both shapes.
func TestWorkspaceGoalReachesTheCLIAgentPrompts(t *testing.T) {
	builders := map[string]func(string, []Message) string{
		"codex":       buildCodexPrompt,
		"claude-code": buildClaudeCodePrompt,
	}
	for name, build := range builders {
		for shapeName, request := range map[string]ChatRequest{
			"task user turn":     taskShapedGoalRequest("cli"),
			"chat system prompt": chatShapedGoalRequest("cli"),
		} {
			if prompt := build(request.SystemPrompt, request.Messages); !strings.Contains(prompt, wireGoal) {
				t.Fatalf("%s/%s prompt lacks the goal: %s", name, shapeName, prompt)
			}
		}
	}
}
