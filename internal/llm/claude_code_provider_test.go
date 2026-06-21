package llm

import (
	"strings"
	"testing"
)

func TestBuildClaudeArgs_TextOnlyUnchanged(t *testing.T) {
	args, err := buildClaudeArgs("opus", "hi", nil, nil)
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
}

func TestBuildClaudeArgs_NativeMCP(t *testing.T) {
	nat := &claudeNativeMCP{ConfigPath: "/cfg/ws.mcp.json", WorkspaceDir: "/ws/files"}
	args, err := buildClaudeArgs("sonnet", "do it", map[string]any{"type": "object"}, nat)
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
