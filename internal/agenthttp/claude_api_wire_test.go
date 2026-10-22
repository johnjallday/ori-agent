package agenthttp

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/llm"
)

// An assistant panel turn on a claude-* API model, end to end through the
// real Anthropic API provider: the turn's rules reach the request's `system`
// and no message carries them. The provider used to drop the system-role
// message the rules travelled in, leaving the model without them.
func TestConversation_ClaudeAPIModelReceivesTheTurnsRulesOnTheWire(t *testing.T) {
	var posted [][]byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		posted = append(posted, body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"msg_1","type":"message","role":"assistant","model":"claude-sonnet-4-5",
			"content":[{"type":"text","text":"Happy birthday, Mina!"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
	}))
	defer server.Close()
	// The SDK takes its endpoint from the environment, so the provider under
	// test is the one production builds.
	t.Setenv("ANTHROPIC_BASE_URL", server.URL)

	f := newConversationFixture(t)
	factory := llm.NewFactory()
	factory.Register("claude", llm.NewClaudeProvider(llm.ProviderConfig{APIKey: "test-key"}))
	f.handler.LLMFactory = factory
	f.handler.SystemModel = stubSystemModel{provider: "claude", model: "claude-sonnet-4-5"}

	resp := f.say("Write a short birthday greeting for my friend Mina.", "")
	if resp.Response != "Happy birthday, Mina!" || resp.ModelUnavailable {
		t.Fatalf("turn: %+v", resp)
	}
	if len(posted) != 1 {
		t.Fatalf("posted %d requests, want 1", len(posted))
	}
	var sent struct {
		System []struct {
			Text string `json:"text"`
		} `json:"system"`
		Messages []struct {
			Role    string `json:"role"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(posted[0], &sent); err != nil {
		t.Fatalf("request body: %v (%s)", err, posted[0])
	}
	var system strings.Builder
	for _, block := range sent.System {
		system.WriteString(block.Text)
	}
	// Markers from the conversation rules (turn.system) and the plain-text
	// reply rule appended to them.
	rules := []string{`displayed as "Nova"`, "Never invent personal facts", "Do not use Markdown markup"}
	for _, want := range rules {
		if !strings.Contains(system.String(), want) {
			t.Errorf("the posted system text lacks %q", want)
		}
	}
	if len(sent.Messages) != 1 || sent.Messages[0].Role != "user" ||
		len(sent.Messages[0].Content) == 0 || !strings.HasPrefix(sent.Messages[0].Content[0].Text, "Write a short birthday greeting") {
		t.Fatalf("posted messages: %s", posted[0])
	}
	for _, want := range rules {
		if strings.Contains(sent.Messages[0].Content[0].Text, want) {
			t.Errorf("the rule %q travelled in a message", want)
		}
	}
}
