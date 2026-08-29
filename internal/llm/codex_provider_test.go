package llm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCodexProviderDefaultModels_UsesCachedModels(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)

	cache := `{
  "models": [
    {"slug": "gpt-5.3-codex", "visibility": "list"},
    {"slug": "gpt-5.2", "visibility": "list"},
    {"slug": "gpt-5.3-codex", "visibility": "list"},
    {"slug": "gpt-5.2-codex", "visibility": "list"},
    {"slug": "hidden-codex", "visibility": "hidden"},
    {"slug": "gpt-5.1-codex", "visibility": "hide"}
  ]
}`
	cachePath := filepath.Join(codexHome, "models_cache.json")
	if err := os.WriteFile(cachePath, []byte(cache), 0600); err != nil {
		t.Fatalf("write cache: %v", err)
	}

	provider := &CodexProvider{cliPath: "codex"}
	models := provider.DefaultModels()

	if !containsModel(models, "gpt-5.3-codex") {
		t.Fatalf("expected gpt-5.3-codex in models, got %v", models)
	}
	if !containsModel(models, "gpt-5.2-codex") {
		t.Fatalf("expected gpt-5.2-codex in models, got %v", models)
	}
	if !containsModel(models, "gpt-5.2") {
		t.Fatalf("expected visible non-codex model in models, got %v", models)
	}
	if countModel(models, "gpt-5.3-codex") != 1 {
		t.Fatalf("expected duplicate cached model to be de-duplicated, got %v", models)
	}
	if containsModel(models, "hidden-codex") {
		t.Fatalf("hidden model should not be included, got %v", models)
	}
	if containsModel(models, "gpt-5.1-codex") {
		t.Fatalf("hide visibility model should not be included, got %v", models)
	}
}

func TestCodexProviderDefaultModels_FallsBackToCurated(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())

	provider := &CodexProvider{cliPath: "codex"}
	models := provider.DefaultModels()

	if len(models) == 0 {
		t.Fatal("expected fallback codex models, got none")
	}

	containsCodexFamily := false
	for _, model := range models {
		if strings.Contains(strings.ToLower(model), "codex") {
			containsCodexFamily = true
			break
		}
	}
	if !containsCodexFamily {
		t.Fatalf("expected at least one codex-family model in fallback list, got %v", models)
	}
}

func TestCodexProviderDefaultModels_PrioritizesGPT53(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)

	cache := `{
  "models": [
    {"slug": "gpt-5.2-codex", "visibility": "list"},
    {"slug": "gpt-5-codex", "visibility": "list"},
    {"slug": "gpt-5.3-codex", "visibility": "list"},
    {"slug": "gpt-5.1-codex", "visibility": "list"}
  ]
}`
	cachePath := filepath.Join(codexHome, "models_cache.json")
	if err := os.WriteFile(cachePath, []byte(cache), 0600); err != nil {
		t.Fatalf("write cache: %v", err)
	}

	provider := &CodexProvider{cliPath: "codex"}
	models := provider.DefaultModels()

	if len(models) == 0 {
		t.Fatal("expected codex models, got none")
	}
	if models[0] != "gpt-5.3-codex" {
		t.Fatalf("expected gpt-5.3-codex to be first, got %q (full list: %v)", models[0], models)
	}
}

func TestCodexProviderDefaultModels_IncludesVisibleGPT54FromCache(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)

	cache := `{
  "models": [
    {"slug": "gpt-5.4", "visibility": "list"},
    {"slug": "gpt-5.3-codex", "visibility": "list"}
  ]
}`
	cachePath := filepath.Join(codexHome, "models_cache.json")
	if err := os.WriteFile(cachePath, []byte(cache), 0600); err != nil {
		t.Fatalf("write cache: %v", err)
	}

	provider := &CodexProvider{cliPath: "codex"}
	models := provider.DefaultModels()

	if !containsModel(models, "gpt-5.4") {
		t.Fatalf("expected gpt-5.4 in models, got %v", models)
	}
}

func containsModel(models []string, target string) bool {
	for _, model := range models {
		if model == target {
			return true
		}
	}
	return false
}

func countModel(models []string, target string) int {
	count := 0
	for _, model := range models {
		if model == target {
			count++
		}
	}
	return count
}

func TestCodexProviderCapabilitiesIncludeBrokeredTools(t *testing.T) {
	capabilities := (&CodexProvider{}).Capabilities()
	if !capabilities.SupportsTools || !capabilities.SupportsNativeMCP || !capabilities.SupportsStructuredOutput {
		t.Fatalf("codex capabilities = %+v", capabilities)
	}
}

func TestCodexBrokeredToolProtocolAndResponse(t *testing.T) {
	tools := []Tool{{
		Name: "plugin_reaper_tidy_survey", Description: "Run the survey.",
		Parameters: map[string]any{"type": "object", "additionalProperties": false},
	}}
	prompt, err := appendCodexBrokeredToolProtocol("User:\nSurvey", tools)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"not Codex MCP servers", "plugin_reaper_tidy_survey", "arguments_json"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("tool prompt missing %q: %s", want, prompt)
		}
	}

	toolResponse, err := decodeCodexBrokeredToolResponse(
		`{"kind":"tool_call","content":"","tool_name":"plugin_reaper_tidy_survey","arguments_json":"{}"}`,
		tools,
		&ChatResponse{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if toolResponse.FinishReason != FinishReasonToolCalls || len(toolResponse.ToolCalls) != 1 ||
		toolResponse.ToolCalls[0].Name != tools[0].Name || toolResponse.ToolCalls[0].Arguments != "{}" || toolResponse.ToolCalls[0].ID == "" {
		t.Fatalf("tool response = %+v", toolResponse)
	}

	finalResponse, err := decodeCodexBrokeredToolResponse(
		`{"kind":"final","content":"Survey complete.","tool_name":"","arguments_json":"{}"}`,
		tools,
		&ChatResponse{},
	)
	if err != nil || finalResponse.Content != "Survey complete." || len(finalResponse.ToolCalls) != 0 {
		t.Fatalf("final response = %+v, %v", finalResponse, err)
	}
}

func TestCodexBrokeredToolResponseRejectsUntrustedShape(t *testing.T) {
	tools := []Tool{{Name: "allowed", Parameters: map[string]any{"type": "object"}}}
	cases := []string{
		`{"kind":"tool_call","content":"","tool_name":"other","arguments_json":"{}"}`,
		`{"kind":"tool_call","content":"","tool_name":"allowed","arguments_json":"[]"}`,
		`{"kind":"final","content":"done","tool_name":"allowed","arguments_json":"{}"}`,
		`{"kind":"final","content":"done","tool_name":"","arguments_json":"{}"} {}`,
	}
	for _, input := range cases {
		if _, err := decodeCodexBrokeredToolResponse(input, tools, &ChatResponse{}); err == nil {
			t.Fatalf("expected refusal for %s", input)
		}
	}
}

func TestNormalizeCodexReasoningEffort(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{input: "low", want: "low"},
		{input: "medium", want: "medium"},
		{input: "high", want: "high"},
		{input: "xhigh", want: "xhigh"},
		{input: " HIGH ", want: "high"},
		{input: "invalid", want: "medium"},
		{input: "", want: "medium"},
	}

	for _, tt := range tests {
		if got := normalizeCodexReasoningEffort(tt.input); got != tt.want {
			t.Fatalf("normalizeCodexReasoningEffort(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestRunCodexExecDeadlineWrapsContextError(t *testing.T) {
	dir := t.TempDir()
	cliPath := filepath.Join(dir, "codex")
	script := "#!/bin/sh\nprintf 'fake codex run\\n' >&2\nexec sleep 5\n"
	if err := os.WriteFile(cliPath, []byte(script), 0700); err != nil {
		t.Fatalf("write fake codex cli: %v", err)
	}

	provider := &CodexProvider{cliPath: cliPath}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err := provider.runCodexExec(ctx, "gpt-test", "return json", "medium", map[string]any{
		"type": "object",
	}, nil)
	if err == nil {
		t.Fatal("expected deadline error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected error to wrap context deadline exceeded, got %v", err)
	}
}

func TestBuildCodexArgs_TextOnlyUnchanged(t *testing.T) {
	args := buildCodexArgs("gpt-5.5", "medium", "/tmp/schema.json", "/tmp/out.txt", nil)
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--sandbox read-only") {
		t.Errorf("text-only must stay read-only: %q", joined)
	}
	for _, bad := range []string{"--profile", "workspace-write", "approval_policy"} {
		if strings.Contains(joined, bad) {
			t.Errorf("text-only must not contain %q: %q", bad, joined)
		}
	}
	if !strings.Contains(joined, "--output-schema /tmp/schema.json") {
		t.Errorf("schema flag missing: %q", joined)
	}
	if args[len(args)-1] != "-" {
		t.Errorf("last arg must be '-', got %q", args[len(args)-1])
	}
}

func TestBuildCodexArgs_NativeMCP(t *testing.T) {
	nat := &codexNativeMCP{ProfileName: "ori-ws-abc", WorkspaceDir: "/ws/files"}
	args := buildCodexArgs("gpt-5.5", "high", "/tmp/schema.json", "/tmp/out.txt", nat)
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"--sandbox workspace-write",
		`approval_policy="never"`,
		"sandbox_workspace_write.network_access=true", // localhost network for Web Remote etc.
		"--profile ori-ws-abc",
		"--output-schema /tmp/schema.json", // structured output coexists with MCP
		"--model gpt-5.5",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("native args missing %q in %q", want, joined)
		}
	}
	if strings.Contains(joined, "read-only") {
		t.Errorf("native run must not be read-only: %q", joined)
	}
	if args[len(args)-1] != "-" {
		t.Errorf("last arg must be '-', got %q", args[len(args)-1])
	}
}

func TestBuildCodexArgs_RuntimeCapabilityScope(t *testing.T) {
	nat := &codexNativeMCP{
		WorkspaceDir: "/workspace", AdditionalWritableRoots: []string{"/runner"},
		NetworkPosture: CLINetworkCapabilityLocal, Scoped: true,
	}
	args := buildCodexArgs("gpt-5.5", "medium", "", "/tmp/out.txt", nat)
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"--sandbox workspace-write",
		`approval_policy="never"`,
		"sandbox_workspace_write.network_access=false",
		"--ignore-user-config",
		"--ignore-rules",
		"--ephemeral",
		"--add-dir /runner",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("scoped args missing %q in %q", want, joined)
		}
	}
	for _, forbidden := range []string{"network_access=true", "danger-full-access", "dangerously-bypass"} {
		if strings.Contains(joined, forbidden) {
			t.Errorf("scoped args contain %q: %q", forbidden, joined)
		}
	}
	if strings.Contains(joined, "--profile") {
		t.Errorf("skill-only scoped run should not need a profile: %q", joined)
	}
}

func TestPrepareCodexRuntimeScopeCanonicalizesRootsWithoutMCPProfile(t *testing.T) {
	base := t.TempDir()
	workspaceDir := filepath.Join(base, "workspace")
	runnerDir := filepath.Join(base, "runner")
	for _, dir := range []string{workspaceDir, runnerDir} {
		if err := os.Mkdir(dir, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	provider := &CodexProvider{mcpStore: newCLIMCPConfigStoreAt(t.TempDir(), t.TempDir())}
	nat, err := provider.prepareNativeMCP(nil, "ws-1", "", &CLIExecutionScope{
		WorkspaceRoot: workspaceDir, AdditionalWritableRoots: []string{runnerDir},
		NetworkPosture: CLINetworkCapabilityLocal,
	})
	if err != nil {
		t.Fatal(err)
	}
	if nat == nil || !nat.Scoped || nat.ProfileName != "" || nat.WorkspaceDir == "" || len(nat.AdditionalWritableRoots) != 1 {
		t.Fatalf("prepared scoped skill-only run = %+v", nat)
	}
}

// TestBuildCodexArgs_SkillOnlyNoProfile covers a skill-only agent (opted in, no
// MCP servers): elevated posture (workspace-write + network + auto-approve) but
// NO --profile.
func TestBuildCodexArgs_SkillOnlyNoProfile(t *testing.T) {
	nat := &codexNativeMCP{WorkspaceDir: "/ws/files"} // no ProfileName
	args := buildCodexArgs("gpt-5.5", "medium", "", "/tmp/out.txt", nat)
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"--sandbox workspace-write",
		`approval_policy="never"`,
		"sandbox_workspace_write.network_access=true",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("skill-only args missing %q in %q", want, joined)
		}
	}
	if strings.Contains(joined, "--profile") {
		t.Errorf("skill-only run (no MCP) must not pass --profile: %q", joined)
	}
	if strings.Contains(joined, "read-only") {
		t.Errorf("elevated run must not be read-only: %q", joined)
	}
}
