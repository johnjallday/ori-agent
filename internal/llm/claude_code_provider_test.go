package llm

import (
	"os"
	"path/filepath"
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

func TestBuildClaudeArgs_RuntimeCapabilityScope(t *testing.T) {
	nat := &claudeNativeMCP{
		ConfigPath: "/cfg/runtime.mcp.json", WorkspaceDir: "/workspace",
		AdditionalWritableRoots: []string{"/runner"},
		AllowedMCPServers:       []string{"trusted_runtime"},
		NetworkPosture:          CLINetworkCapabilityLocal,
		Scoped:                  true,
	}
	args, err := buildClaudeArgs("haiku", "do it", nil, nat)
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
	args, err := buildClaudeArgs("sonnet", "do it", nil, nat)
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
