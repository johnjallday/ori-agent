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
// It sends ChatRequest.SystemPrompt as the request's `system` and drops
// system-role messages. A caller that advertises tools must therefore carry
// the rules for using them in SystemPrompt: the assistant's operation tools
// do (internal/agenthttp runModel), and this pins the behaviour they rely on.
func TestClaudeProvider_SendsSystemPromptAndDropsSystemRoleMessages(t *testing.T) {
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
		SystemPrompt: "RULES-FOR-THE-TOOLS",
		Messages: []Message{
			NewSystemMessage("TEXT-IN-A-SYSTEM-ROLE-MESSAGE"),
			NewUserMessage("Open the Album workspace"),
		},
		Tools: []Tool{{Name: "assistant_operate_open", Description: "Open a workspace.", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var sent struct {
		System []struct {
			Text string `json:"text"`
		} `json:"system"`
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(body, &sent); err != nil {
		t.Fatalf("request body: %v (%s)", err, body)
	}
	if len(sent.System) != 1 || sent.System[0].Text != "RULES-FOR-THE-TOOLS" {
		t.Fatalf("SystemPrompt must be sent as the request's system text: %+v", sent.System)
	}
	if len(sent.Tools) != 1 || sent.Tools[0].Name != "assistant_operate_open" {
		t.Fatalf("tools: %+v", sent.Tools)
	}
	// The premise: this provider does not send a system-role message at all.
	// If that ever changes, the note above and its callers can be simplified.
	if strings.Contains(string(body), "TEXT-IN-A-SYSTEM-ROLE-MESSAGE") {
		t.Fatal("the provider now sends system-role messages; revisit the callers that work around it")
	}
}
