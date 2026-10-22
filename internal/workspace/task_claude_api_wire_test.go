package workspace

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/types"
)

// A task run on a claude-* API model, end to end through the real Anthropic
// API provider: the agent's prompt and the task system prompt reach the
// request's `system`, and the only message is the task. The provider used to
// drop the system-role message the whole task prompt travelled in.
func TestExecuteTask_ClaudeAPIModelReceivesTheTaskSystemPromptOnTheWire(t *testing.T) {
	var posted [][]byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		posted = append(posted, body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"msg_1","type":"message","role":"assistant","model":"claude-sonnet-4-5",
			"content":[{"type":"text","text":"Release notes drafted. Signed, Ori."}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
	}))
	defer server.Close()
	// The SDK takes its endpoint from the environment, so the provider under
	// test is the one production builds.
	t.Setenv("ANTHROPIC_BASE_URL", server.URL)

	factory := llm.NewFactory()
	factory.Register("claude", llm.NewClaudeProvider(llm.ProviderConfig{APIKey: "test-key"}))
	handler := &LLMTaskHandler{llmFactory: factory, agentStore: &resolverAgentStoreStub{agents: map[string]*agent.Agent{
		"Ori": {Settings: types.Settings{Provider: "claude", Model: "claude-sonnet-4-5", SystemPrompt: "AGENT-PROMPT-7c1e: you are {{agent.name}}. End every result with a sign-off."}},
	}}}

	result, err := handler.ExecuteTask(context.Background(), "Ori", Task{
		ID: "task-notes", To: "Ori", Description: "draft the release notes",
		RuntimeSkillPrompts: []ResolvedSkill{{Name: "release-notes", Prompt: "SKILL-PROMPT-3b9d", Enabled: true, Trusted: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "Signed, Ori.") {
		t.Fatalf("result = %q", result)
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
	for _, want := range []string{"AGENT-PROMPT-7c1e: you are Ori. End every result with a sign-off.", "SKILL-PROMPT-3b9d"} {
		if !strings.Contains(system.String(), want) {
			t.Errorf("the posted system text lacks %q:\n%s", want, system.String())
		}
	}
	if len(sent.Messages) != 1 || sent.Messages[0].Role != "user" || len(sent.Messages[0].Content) == 0 ||
		!strings.Contains(sent.Messages[0].Content[0].Text, "draft the release notes") ||
		strings.Contains(sent.Messages[0].Content[0].Text, "AGENT-PROMPT-7c1e") {
		t.Fatalf("posted messages: %s", posted[0])
	}
}
