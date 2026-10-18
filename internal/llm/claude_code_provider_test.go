package llm

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeClaudeCLI is a claude stand-in that records its argv in dir/args and
// prints dir/reply.json as the CLI's --output-format json result.
type fakeClaudeCLI struct {
	t   *testing.T
	dir string
}

func newFakeClaudeCLI(t *testing.T) (*fakeClaudeCLI, *ClaudeCodeProvider) {
	t.Helper()
	dir := t.TempDir()
	cliPath := filepath.Join(dir, "claude")
	script := "#!/bin/sh\nprintf '%s\\0' \"$@\" > \"" + dir + "/args\"\ncat \"" + dir + "/reply.json\"\n"
	if err := os.WriteFile(cliPath, []byte(script), 0700); err != nil {
		t.Fatalf("write fake claude cli: %v", err)
	}
	provider := &ClaudeCodeProvider{cliPath: cliPath, mcpStore: newCLIMCPConfigStoreAt(t.TempDir(), t.TempDir())}
	return &fakeClaudeCLI{t: t, dir: dir}, provider
}

// reply sets the next CLI result. A non-empty envelope is the structured
// output; result is the plain result text.
func (f *fakeClaudeCLI) reply(structuredOutput, result string) {
	f.t.Helper()
	payload := map[string]any{"is_error": false, "result": result}
	if structuredOutput != "" {
		payload["structured_output"] = json.RawMessage(structuredOutput)
	}
	encoded, _ := json.Marshal(payload)
	if err := os.WriteFile(filepath.Join(f.dir, "reply.json"), encoded, 0600); err != nil {
		f.t.Fatalf("write reply: %v", err)
	}
}

// args returns the argv of the last run; the prompt is the last element.
func (f *fakeClaudeCLI) args() []string {
	f.t.Helper()
	raw, err := os.ReadFile(filepath.Join(f.dir, "args"))
	if err != nil {
		f.t.Fatalf("read args: %v", err)
	}
	return strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00")
}

func argValue(args []string, flag string) (string, bool) {
	for i := range args {
		if args[i] == flag && i+1 < len(args) {
			return args[i+1], true
		}
	}
	return "", false
}

var claudeBrokeredTestTools = []Tool{{
	Name: "memory_write", Description: "Write a workspace memory entry.",
	Parameters: map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}},
}}

func TestClaudeCodeProviderCapabilitiesIncludeBrokeredTools(t *testing.T) {
	capabilities := (&ClaudeCodeProvider{}).Capabilities()
	if !capabilities.SupportsTools || !capabilities.SupportsNativeMCP {
		t.Fatalf("claude_code capabilities = %+v", capabilities)
	}
}

func TestClaudeCodeChat_WithToolsSendsSchemaAndProtocol(t *testing.T) {
	cli, provider := newFakeClaudeCLI(t)
	cli.reply(`{"kind":"tool_call","content":"","tool_name":"memory_write","arguments_json":"{\"text\":\"D minor\"}"}`, "")

	resp, err := provider.Chat(context.Background(), ChatRequest{
		Model:    "sonnet",
		Messages: []Message{NewUserMessage("Remember the key")},
		Tools:    claudeBrokeredTestTools,
	})
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if resp.FinishReason != FinishReasonToolCalls || len(resp.ToolCalls) != 1 ||
		resp.ToolCalls[0].Name != "memory_write" || resp.ToolCalls[0].Arguments != `{"text":"D minor"}` ||
		!strings.HasPrefix(resp.ToolCalls[0].ID, "claude_code_tool_") {
		t.Fatalf("tool response = %+v", resp)
	}

	args := cli.args()
	schema, ok := argValue(args, "--json-schema")
	wantSchema, _ := json.Marshal(brokeredToolResponseSchema())
	if !ok || schema != string(wantSchema) {
		t.Fatalf("--json-schema = %q (present %v), want %s", schema, ok, wantSchema)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--tools  --permission-mode dontAsk") {
		t.Fatalf("text-only posture must keep its flags with tools: %q", joined)
	}
	prompt := args[len(args)-1]
	definitions, _ := json.Marshal(claudeBrokeredTestTools)
	for _, want := range []string{"User:\nRemember the key", string(definitions), "not Claude Code's own MCP servers", "Return exactly one JSON object with all four fields"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestClaudeCodeChat_WithoutToolsIsUnchanged(t *testing.T) {
	cli, provider := newFakeClaudeCLI(t)
	cli.reply("", "Plain answer.")

	resp, err := provider.Chat(context.Background(), ChatRequest{
		Model:    "sonnet",
		Messages: []Message{NewUserMessage("hello")},
	})
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if resp.Content != "Plain answer." || len(resp.ToolCalls) != 0 || resp.FinishReason != "" {
		t.Fatalf("plain response = %+v", resp)
	}
	args := cli.args()
	if _, ok := argValue(args, "--json-schema"); ok {
		t.Fatalf("a request without tools must not pass --json-schema: %q", args)
	}
	if strings.Contains(args[len(args)-1], "Ori brokers") {
		t.Fatalf("a request without tools must not carry the protocol: %q", args[len(args)-1])
	}
}

func TestClaudeCodeChat_FinalEnvelopeBecomesContent(t *testing.T) {
	cli, provider := newFakeClaudeCLI(t)
	cli.reply(`{"kind":"final","content":"Saved it.","tool_name":"","arguments_json":"{}"}`, "")

	resp, err := provider.Chat(context.Background(), ChatRequest{
		Model:    "sonnet",
		Messages: []Message{NewUserMessage("Remember the key")},
		Tools:    claudeBrokeredTestTools,
	})
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if resp.Content != "Saved it." || len(resp.ToolCalls) != 0 || resp.FinishReason != FinishReasonStop {
		t.Fatalf("final response = %+v", resp)
	}
}

func TestClaudeCodeChat_RejectsUntrustedEnvelope(t *testing.T) {
	cases := map[string]struct{ structured, result string }{
		"unknown tool":          {structured: `{"kind":"tool_call","content":"","tool_name":"Bash","arguments_json":"{}"}`},
		"final smuggles a tool": {structured: `{"kind":"final","content":"done","tool_name":"memory_write","arguments_json":"{}"}`},
		"invalid arguments":     {structured: `{"kind":"tool_call","content":"","tool_name":"memory_write","arguments_json":"not json"}`},
		"trailing data":         {result: `{"kind":"final","content":"done","tool_name":"","arguments_json":"{}"} {}`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			cli, provider := newFakeClaudeCLI(t)
			cli.reply(tc.structured, tc.result)
			_, err := provider.Chat(context.Background(), ChatRequest{
				Model:    "sonnet",
				Messages: []Message{NewUserMessage("Remember the key")},
				Tools:    claudeBrokeredTestTools,
			})
			if err == nil || !strings.HasPrefix(err.Error(), "claude_code ") {
				t.Fatalf("expected a claude_code-named refusal, got %v", err)
			}
		})
	}
}

func TestClaudeCodeChat_NativePostureKeepsMCPConfigWithBrokeredTools(t *testing.T) {
	cli, provider := newFakeClaudeCLI(t)
	cli.reply(`{"kind":"final","content":"Done.","tool_name":"","arguments_json":"{}"}`, "")
	workspaceDir := t.TempDir()

	resp, err := provider.Chat(context.Background(), ChatRequest{
		Model:        "sonnet",
		Messages:     []Message{NewUserMessage("Remember the key")},
		Tools:        claudeBrokeredTestTools,
		WorkspaceID:  "ws-1",
		WorkspaceDir: workspaceDir,
		MCPServers:   []MCPServerSpec{{Name: "reaper", Command: "/usr/bin/true"}},
	})
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if resp.Content != "Done." {
		t.Fatalf("native response = %+v", resp)
	}
	args := cli.args()
	if _, ok := argValue(args, "--mcp-config"); !ok {
		t.Fatalf("native posture must keep --mcp-config: %q", args)
	}
	if _, ok := argValue(args, "--json-schema"); !ok {
		t.Fatalf("native posture must pass the envelope schema with tools: %q", args)
	}
	if mode, _ := argValue(args, "--permission-mode"); mode != "bypassPermissions" {
		t.Fatalf("native posture flags changed: --permission-mode %q", mode)
	}
	if !strings.Contains(args[len(args)-1], "Available brokered tools") {
		t.Fatalf("native posture prompt must carry the protocol: %q", args[len(args)-1])
	}
}

func TestBuildClaudeArgs_TextOnlyUnchanged(t *testing.T) {
	args, err := buildClaudeArgs("opus", "", "hi", nil, nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	joined := strings.Join(args, " ")
	// Text-only path must keep the legacy flags and add no MCP.
	if !strings.Contains(joined, "--tools  --permission-mode dontAsk") {
		t.Errorf("text-only flags missing/changed: %q", joined)
	}
	if strings.Contains(joined, "--mcp-config") || strings.Contains(joined, "bypassPermissions") {
		t.Errorf("text-only run must not enable MCP/bypass: %q", joined)
	}
	if args[len(args)-1] != "hi" {
		t.Errorf("prompt must be last arg, got %q", args[len(args)-1])
	}
	if strings.Contains(joined, "--effort") {
		t.Errorf("an unset effort must send no --effort flag: %q", joined)
	}
}

func TestBuildClaudeArgs_EffortOnlyForAcceptedLevels(t *testing.T) {
	for _, tc := range []struct{ effort, want string }{
		{"max", "max"},
		{" High ", "high"},
		{"xhigh", "xhigh"},
		{"extreme", ""},
		{"", ""},
	} {
		args, err := buildClaudeArgs("opus", tc.effort, "hi", nil, nil)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		got := ""
		for i := range args {
			if args[i] == "--effort" && i+1 < len(args) {
				got = args[i+1]
			}
		}
		if got != tc.want {
			t.Errorf("effort %q: --effort = %q, want %q (args %q)", tc.effort, got, tc.want, args)
		}
		if args[len(args)-1] != "hi" {
			t.Errorf("effort %q: prompt must stay the last arg, got %q", tc.effort, args[len(args)-1])
		}
	}
}

func TestBuildClaudeArgs_NativeMCP(t *testing.T) {
	nat := &claudeNativeMCP{ConfigPath: "/cfg/ws.mcp.json", WorkspaceDir: "/ws/files"}
	args, err := buildClaudeArgs("sonnet", "", "do it", map[string]any{"type": "object"}, nat)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"--permission-mode bypassPermissions",
		"--mcp-config /cfg/ws.mcp.json",
		"--add-dir /ws/files",
		"--json-schema", // structured output must coexist with MCP
		"--model sonnet",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("native args missing %q in %q", want, joined)
		}
	}
	if strings.Contains(joined, "--tools  ") || strings.Contains(joined, "dontAsk") {
		t.Errorf("native run must drop --tools \"\"/dontAsk: %q", joined)
	}
	if args[len(args)-1] != "do it" {
		t.Errorf("prompt must be last arg, got %q", args[len(args)-1])
	}
}

func TestBuildClaudeArgs_RuntimeCapabilityScope(t *testing.T) {
	nat := &claudeNativeMCP{
		ConfigPath: "/cfg/runtime.mcp.json", WorkspaceDir: "/workspace",
		AdditionalWritableRoots: []string{"/runner"},
		AllowedMCPServers:       []string{"trusted_runtime"},
		NetworkPosture:          CLINetworkCapabilityLocal,
		Scoped:                  true,
	}
	args, err := buildClaudeArgs("haiku", "", "do it", nil, nat)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"--permission-mode acceptEdits",
		"--setting-sources  --tools Read,Write,Edit",
		"--strict-mcp-config",
		"--mcp-config /cfg/runtime.mcp.json",
		"--add-dir /runner",
		"--allowedTools mcp__trusted_runtime__*",
		"--no-session-persistence",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("runtime scope args missing %q in %q", want, joined)
		}
	}
	for _, forbidden := range []string{"bypassPermissions", "Bash", "WebFetch", "dangerously-skip"} {
		if strings.Contains(joined, forbidden) {
			t.Errorf("runtime scope contains %q: %q", forbidden, joined)
		}
	}
	if args[len(args)-1] != "do it" {
		t.Fatalf("prompt was consumed by a variadic option: %q", args[len(args)-1])
	}
}

func TestPrepareClaudeRuntimeScopeCanonicalizesRootsWithoutMCPConfig(t *testing.T) {
	base := t.TempDir()
	workspaceDir := filepath.Join(base, "workspace")
	runnerDir := filepath.Join(base, "runner")
	for _, dir := range []string{workspaceDir, runnerDir} {
		if err := os.Mkdir(dir, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	provider := &ClaudeCodeProvider{mcpStore: newCLIMCPConfigStoreAt(t.TempDir(), t.TempDir())}
	nat, err := provider.prepareNativeMCP(nil, "ws-1", "", &CLIExecutionScope{
		WorkspaceRoot: workspaceDir, AdditionalWritableRoots: []string{runnerDir},
		NetworkPosture: CLINetworkCapabilityLocal,
	})
	if err != nil {
		t.Fatal(err)
	}
	if nat == nil || !nat.Scoped || nat.ConfigPath != "" || nat.WorkspaceDir == "" || len(nat.AdditionalWritableRoots) != 1 {
		t.Fatalf("prepared scoped skill-only run = %+v", nat)
	}
}

// TestBuildClaudeArgs_SkillOnlyNoConfig covers a legacy broad skill-only agent
// (opted in, no MCP servers): full toolset + bypassPermissions + workspace confinement, but
// NO --mcp-config.
func TestBuildClaudeArgs_SkillOnlyNoConfig(t *testing.T) {
	nat := &claudeNativeMCP{WorkspaceDir: "/ws/files"} // no ConfigPath
	args, err := buildClaudeArgs("sonnet", "", "do it", nil, nat)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{"--permission-mode bypassPermissions", "--add-dir /ws/files"} {
		if !strings.Contains(joined, want) {
			t.Errorf("skill-only args missing %q in %q", want, joined)
		}
	}
	if strings.Contains(joined, "--mcp-config") {
		t.Errorf("skill-only run (no MCP) must not pass --mcp-config: %q", joined)
	}
	if strings.Contains(joined, "--tools  ") || strings.Contains(joined, "dontAsk") {
		t.Errorf("elevated run must not be text-only: %q", joined)
	}
}
