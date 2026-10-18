package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/toolapi"
	"github.com/johnjallday/ori-agent/internal/types"
)

// recordingProvider wraps a real provider and records each request it is
// handed, so a test can check the task path's request while the provider's
// own CLI adapter still runs.
type recordingProvider struct {
	llm.Provider
	requests []llm.ChatRequest
}

func (p *recordingProvider) Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	p.requests = append(p.requests, req)
	return p.Provider.Chat(ctx, req)
}

// installFakeClaudeCLI puts a claude stand-in first on PATH. Run n records
// its argv in dir/args<n> and prints dir/reply<n>.json as the CLI's
// --output-format json result.
func installFakeClaudeCLI(t *testing.T, replies ...string) string {
	t.Helper()
	dir := t.TempDir()
	script := `#!/bin/sh
dir="` + dir + `"
n=$(cat "$dir/count" 2>/dev/null || echo 0)
n=$((n + 1))
echo "$n" > "$dir/count"
printf '%s\0' "$@" > "$dir/args$n"
cat "$dir/reply$n.json"
`
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte(script), 0o700); err != nil {
		t.Fatalf("write fake claude cli: %v", err)
	}
	for i, reply := range replies {
		name := filepath.Join(dir, "reply"+string(rune('1'+i))+".json")
		if err := os.WriteFile(name, []byte(reply), 0o600); err != nil {
			t.Fatalf("write reply: %v", err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", t.TempDir())
	return dir
}

func readFakeClaudeArgs(t *testing.T, dir string, run int) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "args"+string(rune('0'+run))))
	if err != nil {
		t.Fatalf("read args of run %d: %v", run, err)
	}
	return strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00")
}

func TestExecuteTask_ClaudeCodeRunCallsBrokeredMemoryTool(t *testing.T) {
	cliDir := installFakeClaudeCLI(t,
		`{"is_error":false,"result":"","structured_output":{"kind":"tool_call","content":"","tool_name":"memory_write","arguments_json":"{\"text\":\"Decision: ship on Friday\"}"}}`,
		`{"is_error":false,"result":"","structured_output":{"kind":"final","content":"Recorded the decision to ship on Friday.","tool_name":"","arguments_json":"{}"}}`,
	)
	claudeCode, err := llm.NewClaudeCodeProvider()
	if err != nil {
		t.Fatalf("claude code provider: %v", err)
	}
	provider := &recordingProvider{Provider: claudeCode}
	factory := llm.NewFactory()
	factory.Register("claude_code", provider)

	store := NewInMemoryStore()
	if err := store.Save(&Workspace{ID: "ws-cc", Name: "Release", Status: StatusActive}); err != nil {
		t.Fatal(err)
	}
	memoryTool := &taskHandlerToolFunc{name: "memory_write", result: `{"saved":true}`}
	handler := &LLMTaskHandler{
		agentStore: &resolverAgentStoreStub{agents: map[string]*agent.Agent{
			"Ori": {Settings: types.Settings{Provider: "claude_code", Model: "sonnet"}},
		}},
		llmFactory:     factory,
		workspaceStore: store,
	}
	handler.SetWorkspaceToolFactory(func(string, string, string) []toolapi.Tool {
		return []toolapi.Tool{memoryTool}
	})

	result, err := handler.ExecuteTask(context.Background(), "Ori", Task{
		ID:          "task-decision",
		WorkspaceID: "ws-cc",
		Description: "Record the decision to ship on Friday in workspace memory.",
		To:          "Ori",
	})
	if err != nil {
		t.Fatalf("ExecuteTask failed: %v", err)
	}
	if memoryTool.calls != 1 {
		t.Fatalf("expected memory_write to run once, got %d", memoryTool.calls)
	}
	if !strings.Contains(result, "Recorded the decision to ship on Friday.") {
		t.Fatalf("expected the final envelope's content, got %q", result)
	}

	if len(provider.requests) != 2 {
		t.Fatalf("expected a tool round and a final round, got %d requests", len(provider.requests))
	}
	first := provider.requests[0]
	hasMemoryWrite := false
	for _, tool := range first.Tools {
		hasMemoryWrite = hasMemoryWrite || tool.Name == "memory_write"
	}
	if !hasMemoryWrite {
		t.Fatalf("expected the task request to carry memory_write, got %#v", first.Tools)
	}
	for _, msg := range first.Messages {
		if strings.Contains(msg.Content, "Tool calling is unavailable for this model") {
			t.Fatalf("a claude_code run must not be told tools are unavailable: %q", msg.Content)
		}
	}

	args := readFakeClaudeArgs(t, cliDir, 1)
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--json-schema") || !strings.Contains(joined, "--permission-mode dontAsk") {
		t.Fatalf("expected the envelope schema on the text-only posture, got %q", joined)
	}
	if prompt := args[len(args)-1]; !strings.Contains(prompt, "Available brokered tools") || !strings.Contains(prompt, "memory_write") {
		t.Fatalf("expected the brokered protocol in the prompt, got %q", prompt)
	}
	second := readFakeClaudeArgs(t, cliDir, 2)
	if prompt := second[len(second)-1]; !strings.Contains(prompt, "Brokered tool request") || !strings.Contains(prompt, `{"saved":true}`) {
		t.Fatalf("expected the second round to see the tool request and its result, got %q", prompt)
	}
}
