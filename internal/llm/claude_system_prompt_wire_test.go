package llm

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// What the Anthropic API provider puts on the wire for system text.
//
// The API takes its system text apart from the messages, so the provider sends
// ChatRequest.SystemPrompt and then every system-role message, in order, as the
// request's `system`. Callers still put their rules in SystemPrompt, the
// contract every provider sends; folding system-role messages in is the
// backstop for one that does not, which every other provider would honour and
// this one used to drop without an error.
func TestClaudeProvider_SendsSystemPromptAndSystemRoleMessagesAsTheSystemText(t *testing.T) {
	for _, tc := range []struct {
		name         string
		systemPrompt string
		messages     []Message
		want         string
	}{
		{
			name:         "SystemPrompt then a system message",
			systemPrompt: "RULES-FOR-THE-TOOLS",
			messages:     []Message{NewSystemMessage("TEXT-IN-A-SYSTEM-ROLE-MESSAGE"), NewUserMessage("Open the Album workspace")},
			want:         "RULES-FOR-THE-TOOLS\n\nTEXT-IN-A-SYSTEM-ROLE-MESSAGE",
		},
		{
			name:     "only a system message",
			messages: []Message{NewSystemMessage("TEXT-IN-A-SYSTEM-ROLE-MESSAGE"), NewUserMessage("Open the Album workspace")},
			want:     "TEXT-IN-A-SYSTEM-ROLE-MESSAGE",
		},
		{
			name:         "several system messages in order, empty ones left out",
			systemPrompt: "FIRST",
			messages: []Message{
				NewSystemMessage("SECOND"), NewUserMessage("Open the Album workspace"),
				NewSystemMessage("  "), NewAssistantMessage("Opening it."), NewSystemMessage("THIRD"),
			},
			want: "FIRST\n\nSECOND\n\nTHIRD",
		},
		{
			name:     "no system text",
			messages: []Message{NewUserMessage("Open the Album workspace")},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var body []byte
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ = io.ReadAll(r.Body)
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"id":"msg_1","type":"message","role":"assistant","model":"claude-fixture",
					"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
			}))
			defer server.Close()

			provider := &ClaudeProvider{apiKey: "test-key", httpClient: server.Client(), client: anthropic.NewClient(
				option.WithBaseURL(server.URL), option.WithHTTPClient(server.Client()), option.WithAPIKey("test-key"), option.WithMaxRetries(0))}
			_, err := provider.Chat(t.Context(), ChatRequest{
				Model:        "claude-fixture",
				SystemPrompt: tc.systemPrompt,
				Messages:     tc.messages,
				Tools:        []Tool{{Name: "assistant_operate_open", Description: "Open a workspace.", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}}},
			})
			if err != nil {
				t.Fatal(err)
			}
			var sent struct {
				System []struct {
					Text string `json:"text"`
				} `json:"system"`
				Messages []struct {
					Role string `json:"role"`
				} `json:"messages"`
				Tools []struct {
					Name string `json:"name"`
				} `json:"tools"`
			}
			if err := json.Unmarshal(body, &sent); err != nil {
				t.Fatalf("request body: %v (%s)", err, body)
			}
			var system []string
			for _, block := range sent.System {
				system = append(system, block.Text)
			}
			if got := strings.Join(system, "\n\n"); got != tc.want {
				t.Fatalf("system = %q, want %q", got, tc.want)
			}
			for _, message := range sent.Messages {
				if message.Role != "user" && message.Role != "assistant" {
					t.Fatalf("a %q message reached the wire:\n%s", message.Role, body)
				}
			}
			if strings.Contains(string(body), `"role":"system"`) {
				t.Fatalf("a system-role message reached the messages:\n%s", body)
			}
			if len(sent.Tools) != 1 || sent.Tools[0].Name != "assistant_operate_open" {
				t.Fatalf("tools: %+v", sent.Tools)
			}
		})
	}
}
