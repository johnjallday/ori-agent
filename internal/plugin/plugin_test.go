package plugin

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDetectClaude(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".claude-plugin", "plugin.json"), `{"name":"demo","version":"1.0.0"}`)
	m, err := DetectManifest(root, "")
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if m.format != FormatClaude {
		t.Errorf("format = %s, want claude", m.format)
	}
	if m.raw.Name != "demo" {
		t.Errorf("name = %q", m.raw.Name)
	}
}

func TestDetectCodexVersioned(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "1.0.809", ".codex-plugin", "plugin.json"), `{"name":"cu"}`)
	m, err := DetectManifest(root, "")
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if m.format != FormatCodex {
		t.Errorf("format = %s, want codex", m.format)
	}
	if filepath.Base(m.root) != "1.0.809" {
		t.Errorf("root = %s, want version subdir", m.root)
	}
}

func TestDetectDualPrecedence(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".claude-plugin", "plugin.json"), `{"name":"c"}`)
	writeFile(t, filepath.Join(root, ".codex-plugin", "plugin.json"), `{"name":"x"}`)

	m, err := DetectManifest(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if m.format != FormatClaude {
		t.Errorf("default precedence = %s, want claude", m.format)
	}

	m2, err := DetectManifest(root, FormatCodex)
	if err != nil {
		t.Fatal(err)
	}
	if m2.format != FormatCodex {
		t.Errorf("override = %s, want codex", m2.format)
	}
}

func TestDetectNone(t *testing.T) {
	if _, err := DetectManifest(t.TempDir(), ""); !errors.Is(err, ErrNoManifest) {
		t.Fatalf("err = %v, want ErrNoManifest", err)
	}
}

func TestNormalizeClaudeBareMCP(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".claude-plugin", "plugin.json"), `{"name":"reaper","version":"0.1.0","description":"d","author":"jj"}`)
	writeFile(t, filepath.Join(root, ".mcp.json"), `{"ori-reaper":{"command":"/abs/reaper-mcp","args":[]}}`)
	writeFile(t, filepath.Join(root, "skills", "reaper-session-setup", "SKILL.md"), "---\nname: x\n---\n")

	d, err := Load(root, "", "")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if d.SourceFormat != FormatClaude {
		t.Errorf("format = %s", d.SourceFormat)
	}
	if d.Author != "jj" {
		t.Errorf("author = %q", d.Author)
	}
	if len(d.MCPServers) != 1 || d.MCPServers[0].Name != "ori-reaper" {
		t.Fatalf("mcp servers = %+v", d.MCPServers)
	}
	if d.MCPServers[0].Command != "/abs/reaper-mcp" {
		t.Errorf("command = %q", d.MCPServers[0].Command)
	}
	if len(d.Skills) != 1 || d.Skills[0].Name != "reaper-session-setup" {
		t.Errorf("skills = %+v", d.Skills)
	}
	if len(d.Unsupported) != 0 {
		t.Errorf("unexpected unsupported = %+v", d.Unsupported)
	}
}

func TestNormalizeCodexWrappedMCP(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".codex-plugin", "plugin.json"),
		`{"name":"cu","version":"1.0.809","mcpServers":"./.mcp.json","skills":"./skills/","interface":{"displayName":"Computer Use","category":"Productivity"},"hooks":{"x":1}}`)
	writeFile(t, filepath.Join(root, ".mcp.json"),
		`{"mcpServers":{"computer-use":{"command":"./app/bin","args":["mcp"],"cwd":"."}}}`)
	writeFile(t, filepath.Join(root, "skills", "computer-use", "SKILL.md"), "---\nname: cu\n---\n")

	d, err := Load(root, "", "")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if d.SourceFormat != FormatCodex {
		t.Errorf("format = %s", d.SourceFormat)
	}
	if len(d.MCPServers) != 1 || d.MCPServers[0].Name != "computer-use" {
		t.Fatalf("mcp servers = %+v", d.MCPServers)
	}
	if d.MCPServers[0].Cwd != "." {
		t.Errorf("cwd = %q, want .", d.MCPServers[0].Cwd)
	}
	if d.Interface == nil || d.Interface.DisplayName != "Computer Use" {
		t.Fatalf("interface = %+v", d.Interface)
	}
	if len(d.Skills) != 1 || d.Skills[0].Name != "computer-use" {
		t.Errorf("skills = %+v", d.Skills)
	}
	hook := false
	for _, u := range d.Unsupported {
		if u.Kind == "hook" {
			hook = true
		}
	}
	if !hook {
		t.Errorf("expected hook in unsupported = %+v", d.Unsupported)
	}
}

func TestResolveSourceLocalDir(t *testing.T) {
	root := t.TempDir()
	got, err := ResolveSource(root, "")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != root {
		t.Errorf("got %q, want %q", got, root)
	}
	if _, err := ResolveSource("", ""); !errors.Is(err, ErrEmptySource) {
		t.Errorf("empty source err = %v", err)
	}
}
