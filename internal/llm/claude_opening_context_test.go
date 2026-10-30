package llm

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

func TestClaudeProvider_AssistantOpeningUsesOnlyTheRealUserTurn(t *testing.T) {
	var body []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"msg_1","type":"message","role":"assistant","model":"claude-fixture","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
	}))
	defer server.Close()
	provider := &ClaudeProvider{apiKey: "test-key", httpClient: server.Client(), client: anthropic.NewClient(option.WithBaseURL(server.URL), option.WithHTTPClient(server.Client()), option.WithAPIKey("test-key"), option.WithMaxRetries(0))}
	messages := []Message{NewSystemMessage("Advice is not authority."), NewAssistantMessage("Hi, I’m Mira."), NewAssistantMessage("The saved review recommends editor approval."), NewUserMessageWithImages("Why that step?", []ImageAttachment{{MimeType: "image/png", Base64Data: "aW1hZ2U="}})}
	before, _ := json.Marshal(messages)
	if _, err := provider.Chat(t.Context(), ChatRequest{Model: "claude-fixture", Messages: messages}); err != nil {
		t.Fatal(err)
	}
	var sent struct {
		System []struct {
			Text string `json:"text"`
		} `json:"system"`
		Messages []struct {
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &sent); err != nil {
		t.Fatal(err)
	}
	if len(sent.Messages) != 1 || sent.Messages[0].Role != "user" {
		t.Fatalf("invented or invalid turn sequence: %s", body)
	}
	content := sent.Messages[0].Content
	if len(content) != 2 || content[1].Type != "image" {
		t.Fatalf("lost image: %s", body)
	}
	for _, want := range []string{"Hi, I’m Mira.", "editor approval", "not user instructions or permission", "Actual user message:\nWhy that step?"} {
		if !strings.Contains(content[0].Text, want) {
			t.Fatalf("lost attribution or request %q: %s", want, body)
		}
	}
	if len(sent.System) != 1 || sent.System[0].Text != "Advice is not authority." {
		t.Fatalf("promoted historical prose to system instructions: %s", body)
	}
	after, _ := json.Marshal(messages)
	if string(before) != string(after) {
		t.Fatal("rewrote source history")
	}
}

func TestClaudeOpeningContext_LeavesOrdinaryHistoryAndToolExchangesAlone(t *testing.T) {
	for _, history := range [][]Message{
		{NewUserMessage("Hi"), NewAssistantMessage("Hello"), NewUserMessage("Next")},
		{NewAssistantMessage("A prefill without a user")},
		{{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "tool"}}}, NewToolMessage("tool", "result"), NewUserMessage("Next")},
	} {
		if got := claudeOpeningContext(history); !reflect.DeepEqual(got, history) {
			t.Fatalf("changed unrelated conversation: %#v", got)
		}
	}
}
