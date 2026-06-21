package llm

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func sampleSpecs() []MCPServerSpec {
	return []MCPServerSpec{
		{
			Name:    "ori-reaper",
			Command: "/abs/plugins/reaper/bin/reaper-plugin",
			Args:    []string{"--stdio"},
			Env:     map[string]string{"REAPER_WEB_REMOTE_PORT": "2307"},
		},
	}
}

func TestBuildClaudeMCPConfigJSON(t *testing.T) {
	b, err := buildClaudeMCPConfigJSON(sampleSpecs())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	var cfg claudeMCPConfig
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	srv, ok := cfg.MCPServers["ori-reaper"]
	if !ok {
		t.Fatalf("missing alias key; got %v", cfg.MCPServers)
	}
	if srv.Command != "/abs/plugins/reaper/bin/reaper-plugin" {
		t.Errorf("command = %q", srv.Command)
	}
	if srv.Env["REAPER_WEB_REMOTE_PORT"] != "2307" {
		t.Errorf("env not passed through: %v", srv.Env)
	}
}

func TestBuildCodexProfileTOML(t *testing.T) {
	b, err := buildCodexProfileTOML(sampleSpecs(), DefaultCLIAgentPosture())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	var prof codexProfile
	if _, err := toml.Decode(string(b), &prof); err != nil {
		t.Fatalf("decode: %v\n%s", err, b)
	}
	if prof.SandboxMode != "workspace-write" || prof.ApprovalPolicy != "never" {
		t.Errorf("posture not encoded: sandbox=%q approval=%q", prof.SandboxMode, prof.ApprovalPolicy)
	}
	srv, ok := prof.MCPServers["ori-reaper"]
	if !ok {
		t.Fatalf("missing mcp_servers.ori-reaper; got %v", prof.MCPServers)
	}
	if srv.Command == "" || srv.Env["REAPER_WEB_REMOTE_PORT"] != "2307" {
		t.Errorf("server fields wrong: %+v", srv)
	}
	// Scalars must precede the first table for valid TOML.
	if i, j := strings.Index(string(b), "sandbox_mode"), strings.Index(string(b), "[mcp_servers"); i < 0 || j < 0 || i > j {
		t.Errorf("sandbox_mode must precede [mcp_servers] table:\n%s", b)
	}
}

func TestDedupeSpecsByName(t *testing.T) {
	specs := []MCPServerSpec{
		{Name: "b", Command: "/b"},
		{Name: "a", Command: "/a-first"},
		{Name: "a", Command: "/a-second"},
		{Name: "  ", Command: "/blank"},
	}
	got := dedupeSpecsByName(specs)
	if len(got) != 2 {
		t.Fatalf("want 2 (deduped, blank dropped), got %d: %+v", len(got), got)
	}
	if got[0].Name != "a" || got[1].Name != "b" {
		t.Errorf("not sorted: %+v", got)
	}
	if got[0].Command != "/a-first" {
		t.Errorf("dedupe should keep first occurrence, got %q", got[0].Command)
	}
}

func TestWriteIfChangedIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "f.json")
	content := []byte("hello\n")

	changed, err := writeIfChanged(path, content)
	if err != nil || !changed {
		t.Fatalf("first write: changed=%v err=%v", changed, err)
	}
	changed, err = writeIfChanged(path, content)
	if err != nil || changed {
		t.Fatalf("second write should be a no-op: changed=%v err=%v", changed, err)
	}
	changed, err = writeIfChanged(path, []byte("different\n"))
	if err != nil || !changed {
		t.Fatalf("changed content should rewrite: changed=%v err=%v", changed, err)
	}
}

func TestEnsureConfigsAndRemove(t *testing.T) {
	claudeDir := t.TempDir()
	codexHome := t.TempDir()
	s := newCLIMCPConfigStoreAt(claudeDir, codexHome)
	ws := "868aeadd-8d08-4495-a397-6b0630ba7d14"

	claudePath, err := s.EnsureClaudeConfig(ws, sampleSpecs())
	if err != nil {
		t.Fatalf("ensure claude: %v", err)
	}
	if claudePath != s.ClaudeConfigPath(ws) {
		t.Errorf("path mismatch: %q vs %q", claudePath, s.ClaudeConfigPath(ws))
	}
	if _, err := os.Stat(claudePath); err != nil {
		t.Fatalf("claude config not written: %v", err)
	}

	profName, err := s.EnsureCodexProfile(ws, sampleSpecs(), DefaultCLIAgentPosture())
	if err != nil {
		t.Fatalf("ensure codex: %v", err)
	}
	if profName != s.CodexProfileName(ws) {
		t.Errorf("profile name mismatch: %q", profName)
	}
	if _, err := os.Stat(s.CodexProfilePath(ws)); err != nil {
		t.Fatalf("codex profile not written: %v", err)
	}

	// Empty specs removes stale files and returns "".
	if p, err := s.EnsureClaudeConfig(ws, nil); err != nil || p != "" {
		t.Fatalf("empty claude: p=%q err=%v", p, err)
	}
	if _, err := os.Stat(s.ClaudeConfigPath(ws)); !os.IsNotExist(err) {
		t.Errorf("stale claude config not removed")
	}

	// Recreate then Remove() clears both.
	_, _ = s.EnsureClaudeConfig(ws, sampleSpecs())
	_, _ = s.EnsureCodexProfile(ws, sampleSpecs(), DefaultCLIAgentPosture())
	if err := s.Remove(ws); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := os.Stat(s.ClaudeConfigPath(ws)); !os.IsNotExist(err) {
		t.Errorf("claude config left after Remove")
	}
	if _, err := os.Stat(s.CodexProfilePath(ws)); !os.IsNotExist(err) {
		t.Errorf("codex profile left after Remove")
	}
	// Remove on a clean workspace is a no-op.
	if err := s.Remove(ws); err != nil {
		t.Errorf("remove (clean) should be nil, got %v", err)
	}
}

func TestCLISafeName(t *testing.T) {
	cases := map[string]string{
		"ori-reaper":         "ori-reaper",
		"reaper-plugin/ori":  "reaper-plugin_ori",
		"ws:abc:mcp:x:y":     "ws_abc_mcp_x_y",
		"868aeadd-8d08-4495": "868aeadd-8d08-4495",
		"  ":                 "default",
	}
	for in, want := range cases {
		if got := cliSafeName(in); got != want {
			t.Errorf("cliSafeName(%q) = %q, want %q", in, got, want)
		}
	}
}
